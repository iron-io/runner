package runner

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/common/stats"
	"github.com/iron-io/titan/runner/drivers"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"github.com/iron-io/titan/runner/drivers/docker"
	"golang.org/x/net/context"
)

type BoxTime struct{}

func (BoxTime) Now() time.Time                         { return time.Now() }
func (BoxTime) Sleep(d time.Duration)                  { time.Sleep(d) }
func (BoxTime) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Config struct {
	Concurrency  int                  `json:"concurrency"`
	DriverConfig *drivercommon.Config `json:"driver"`
}

type gofer struct {
	*common.Environment
	conf       *Config
	tasker     Tasker
	stopper    Stopper
	clock      common.Clock
	instanceID string
	runnerNum  int
	driver     drivers.Driver
	log.FieldLogger
}

func newGofer(env *common.Environment, conf *Config, tasker Tasker, clock common.Clock, runnerNum int, driver drivers.Driver, stopper Stopper, logger log.FieldLogger) (*gofer, error) {
	var err error
	instanceID, err := instanceID()
	if err != nil {
		return nil, err
	}
	logger = logger.WithFields(log.Fields{
		"instance_id": instanceID,
		"runner_num":  runnerNum,
	})

	g := &gofer{
		instanceID:  instanceID,
		runnerNum:   runnerNum,
		conf:        conf,
		tasker:      tasker,
		stopper:     stopper,
		clock:       clock,
		driver:      driver,
		FieldLogger: logger,
		Environment: env,
	}
	return g, nil
}

type Timer struct {
	*stats.Timer
}

func (t *Timer) Stop() {
	t.Measure()
}

func (g *gofer) timer(name string) *Timer {
	return &Timer{g.NewTimer("runner", name, 0.1)}
}

// instanceID returns the EC2 instance ID if we're on EC2.
// Otherwise it returns hostname.
func instanceID() (string, error) {
	// See http://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html
	resp, err := http.Get("http://instance-data/latest/meta-data/instance-id")
	if err != nil {
		// TODO: check for specific type of error?
		return os.Hostname()
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if resp.ContentLength >= 0 {
		buf.Grow(int(resp.ContentLength))
	}
	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// TODO: split gofer and runner into separate files

type Runner struct {
	cancel context.CancelFunc
	log.FieldLogger
	fin      chan struct{}
	conf     *Config
	tasker   Tasker
	clock    common.Clock
	ctx      context.Context
	env      *common.Environment
	hostname string
}

func NewRunner(env *common.Environment, ctx context.Context, conf *Config, tasker Tasker) *Runner {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal("couldn't resolve hostname", "err", err)
	}

	l := log.WithFields(log.Fields{
		"hostname": hostname,
	})
	r := &Runner{
		ctx:         ctx,
		env:         env,
		conf:        conf,
		tasker:      tasker,
		FieldLogger: l,
		clock:       BoxTime{},
		hostname:    hostname,
	}
	return r
}

func (r *Runner) Run() {

	docker, err := docker.NewDocker(r.conf.DriverConfig, r.hostname)
	if err != nil {
		r.Fatal("couldn't start container driver", "err", err)
	}

	// making a new context with cancel here so we can cancel from here down the tree, while still respecting the SIG cancel in main.
	ctx2, cancel := context.WithCancel(r.ctx)
	r.cancel = cancel

	r.Infoln("starting", r.conf.Concurrency, "runners")
	fin := make(chan struct{}, r.conf.Concurrency)
	r.fin = fin
	for i := 0; i < r.conf.Concurrency; i++ {
		go func(i int) {
			defer func() {
				fin <- struct{}{}
			}()
			g, err := newGofer(r.env, r.conf, r.tasker, r.clock, i, docker, r, r)
			if err != nil {
				r.WithError(err).Errorln("Error creating runner", i)
				return
			}
			g.run(ctx2)
		}(i)
	}

	<-r.ctx.Done() // from ctrl-c or something external
	r.Info("shutting down, let all tasks finish! or else...")
	r.waitForGophersToFinish()
	r.Info("all tasks finished, exiting cleanly. thank you, come again.")

}
func (r *Runner) Stop() {
	r.cancel()
	r.waitForGophersToFinish()
	// TODO: Restart docker, check memory issues, check disk space issues, etc. Also do those checks in a timer so it doesn't get to this point.
	r.Run()
}

func (r *Runner) waitForGophersToFinish() {
	for i := 1; i <= r.conf.Concurrency; i++ {
		<-r.fin
		r.Info("task finished.", r.conf.Concurrency-i, "still running")
	}
}

func (g *gofer) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			g.Inc("runner", "panicked", 1, 0.1)
			g.Inc("runner", g.instanceID+".panicked", 1, 0.1)
			g.Warnln("recovered from panic, restarting runner: stack", r)
			debug.PrintStack()
			go g.run(ctx)
		}
	}()

	// drivers.ContainerTask() blocks indefinitely until it can get a job. We want to respect
	// `ctx.Done()` for shutdowns, so we cannot directly stick drivers.ContainerTask() into the
	// select. We use this channel and goroutine to get around it.  This does
	// mean that the goroutine wrapping drivers.ContainerTask() never shuts down, this is OK since
	// the process is about to quit.
	tasks := make(chan drivers.ContainerTask, 1)
	go func() {
		for {
			tasks <- g.tasker.Job()
		}
	}()
	for {
		g.Debug("getting task")
		sw := g.timer("get task")
		select {
		case <-ctx.Done():
			return
		default: // don't spawn getting a job if we're done
			select {
			case <-ctx.Done():
				return
			case task := <-tasks:
				sw.Stop()
				g.Debug("starting job", "job_id", task.Id())

				sw = g.timer("run_job")
				g.runTask(ctx, task)
				sw.Stop()

				g.Debug("job finished", "job_id", task.Id())
			}
		}
	}
}

func (g *gofer) recordTaskCompletion(job drivers.ContainerTask, status string, duration time.Duration) {
	statName := fmt.Sprintf("completion.%s", status)
	// todo: remove project stuff
	projectStatName := fmt.Sprintf("p.%s.completion.%s", status)

	g.Inc("task", statName, 1, 1.0)
	g.Inc("task", projectStatName, 1, 1.0)
	g.Time("task", statName, duration, 1.0)
	g.Time("task", projectStatName, duration, 1.0)
}

func (g *gofer) updateTaskStatusAndLog(ctx context.Context, job drivers.ContainerTask, runResult drivers.RunResult) error {
	g.Debug("updating task")
	defer runResult.Close()

	var reason string
	if runResult.Status() == "success" {
		return g.tasker.Succeeded(job)
	} else if runResult.Status() == "error" {
		// TODO: this isn't necessarily true, the error could have been anything along the way, like image not found or something.
		reason = "bad_exit"
		// TODO: Need a way to pass this to the tasker
		// I feel this state checking can move to the tasker.
		//job.Error = runResult.Error().Error()
	} else if runResult.Status() == "killed" {
		// same as cancelled
		//job.Status = models.StatusCancelled
		//reason = "killed"
		return nil // see cancelled case
	} else if runResult.Status() == "timeout" {
		reason = "timeout"
	} else if runResult.Status() == "cancelled" {
		//job.Status = models.StatusCancelled
		//reason = "client_request"
		// FIXME(nikhil): Implement
		// This should already be cancelled on server side, so might not even need to send back. Only reason would be to show that it may have partially ran?
		// g.tasker.Cancelled( job)
		return nil
	}

	// FIXME(nikhil): Set job error details field.
	if err := runResult.Error(); err != nil {
		g.Debug("drivers.ContainerTask failure ", err)
	}

	//g.recordTaskCompletion(job, job.Status, now.Sub(job.StartedAt))
	g.Debugln("reason", reason)
	return g.tasker.Failed(job, reason)
}

func (g *gofer) runTask(ctx context.Context, job drivers.ContainerTask) {
	// We need this channel until the shared driver code can work with context.Context.
	isCancelledChn := make(chan bool)
	isCancelledCtx, isCancelledStopSignal := context.WithCancel(ctx)
	defer isCancelledStopSignal()
	go g.emitCancellationSignal(isCancelledCtx, job, isCancelledChn)

	l := g.WithFields(log.Fields{
		"job_id": job.Id(),
	})
	l.Debugln("starting job")

	err := g.tasker.Start(job)
	if err != nil {
		l.WithError(err).Errorln("Job Server forbade starting job, skipping")
		return
	}

	l.Debugln("About to run", job.Id())
	runResult, err := g.driver.Run(job, isCancelledChn)
	if err != nil {
		// If err is set, then this is a Titan system error, not an error in the user code execution
		l.WithError(err).Errorln("system error executing job!")
		// initiate shutdown and recovery sequence, this is most likely Docker f'ing up.
		// TODO: What should we do about job here?  Send error back and let it retry on another server?
		// If Docker never started the job, we can probably tell swapi to give it to someone else without doing a retry or anything.
		g.stopper.Stop()
	} else {
		// Job ran ok.
		l.WithFields(log.Fields{
			"status":    runResult.Status(),
			"job_error": runResult.Error(),
		}).Debugln("job result")
		g.updateTaskStatusAndLog(ctx, job, runResult)
	}
}

func (g *gofer) emitCancellationSignal(ctx context.Context, job drivers.ContainerTask, isCancelled chan bool) {
	defer func() {
		if e := recover(); e != nil {
			log.Errorln("emitCancellationSignal panic", e)
			debug.PrintStack()
			go g.emitCancellationSignal(ctx, job, isCancelled)
		}

	}()
	for {
		ic := g.tasker.IsCancelled(job)
		if ic {
			select {
			case <-ctx.Done():
				return
			case isCancelled <- true:
				return
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case <-g.clock.After(5 * time.Second):
			}
		}
	}
}

// call f up to n times until f returns true.
// backoff will be called after each failure.
func retry(n int, backoff func(), f func() bool) {
	for i := 0; i < n; i++ {
		ok := f()
		if ok {
			break
		}
		backoff()
	}
}

func hasErroredOrTimedOut(s drivers.RunResult) bool {
	return s.Error() != nil || s.Status() == drivers.StatusTimeout
}
