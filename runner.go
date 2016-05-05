package runner

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	dc "github.com/fsouza/go-dockerclient"
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

// goferTask implements drivers.ContainerTask interface, which is the only point in which
// Titan and gorunner must agree in order to be able to share blades (container
// engine drivers).
type goferTask struct {
	command string
	config  string
	envVars map[string]string
	id      string
	image   string
	payload string
	timeout uint
	drivers.ContainerTask
	auth string
}

func (g *goferTask) Command() string            { return g.command }
func (g *goferTask) Config() string             { return g.config }
func (g *goferTask) EnvVars() map[string]string { return g.envVars }
func (g *goferTask) Id() string                 { return g.id }
func (g *goferTask) Image() string              { return g.image }
func (g *goferTask) Payload() string            { return g.payload }
func (g *goferTask) Timeout() uint              { return g.timeout }
func (g *goferTask) Auth() string               { return g.auth }

type Config struct {
	ApiUrl       string `json:"api_url"`
	Concurrency  int    `json:"concurrency"`
	DriverConfig *drivercommon.Config
	Registries   map[string]*Registry `json:"registries"`
}

// Registry holds auth for a registry
type Registry struct {
	Auth     string `json:"auth"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type gofer struct {
	*common.Environment
	conf       *Config
	tasker     Tasker
	clock      common.Clock
	hostname   string
	instanceID string
	driver     drivers.Driver
	log.FieldLogger
}

func newGofer(env *common.Environment, conf *Config, tasker Tasker, clock common.Clock, hostname string, driver drivers.Driver, logger log.FieldLogger) (*gofer, error) {
	var err error
	g := &gofer{
		conf:        conf,
		tasker:      tasker,
		clock:       clock,
		driver:      driver,
		hostname:    hostname,
		FieldLogger: logger,
		Environment: env,
	}
	g.instanceID, err = instanceID()
	if err != nil {
		return nil, err
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

func Run(env *common.Environment, conf *Config, tasker Tasker, clock common.Clock, ctx context.Context) {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal("couldn't resolve hostname", "err", err)
	}
	l := log.WithFields(log.Fields{
		"hostname": hostname,
	})

	docker, err := docker.NewDocker(conf.DriverConfig, hostname)
	if err != nil {
		l.Fatal("couldn't start container driver", "err", err)
	}

	l.Infoln("starting", conf.Concurrency, "runners")
	fin := make(chan struct{}, conf.Concurrency)
	for i := 0; i < conf.Concurrency; i++ {
		go func(i int) {
			defer func() {
				fin <- struct{}{}
			}()
			sl := l.WithFields(log.Fields{
				"runner_id": i,
			})
			g, err := newGofer(env, conf, tasker, clock, hostname, docker, sl)
			if err != nil {
				l.Errorln("Error creating runner", i, "err", err)
				return
			}
			g.runner(ctx)
		}(i)
	}

	<-ctx.Done()
	l.Info("shutting down, let all tasks finish! or else...")
	for i := 1; i <= conf.Concurrency; i++ {
		<-fin
		l.Info("task finished.", conf.Concurrency-i, "still_running")
	}
	l.Info("all tasks finished, exiting cleanly. thank you, come again.")
}

func (g *gofer) runner(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			g.Inc("runner", "panicked", 1, 0.1)
			g.Inc("runner", g.instanceID+".panicked", 1, 0.1)
			g.Warnln("recovered from panic, restarting runner: stack", r)
			debug.PrintStack()
			go g.runner(ctx)
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

// This will close logFile
func (g *gofer) updateTaskStatusAndLog(ctx context.Context, job drivers.ContainerTask, runResult drivers.RunResult, logFile *os.File) error {
	g.Debug("updating task")

	// Docker driver should seek!
	// This is REALLY stupid. The swagger online generator has obviously not been
	// tested because it can't generate a correct swagger definition for a form
	// upload that has a file field. It uses Google's query parser but that
	// parser does not support encoding os.File. It seems like go-swagger does
	// this correctly, so I've filed #73. Meanwhile, serialize to a string.
	logFile.Seek(0, 0)
	defer runResult.Close()

	// We can't set job.Reason because Reason is generated as an empty struct for some reason o_O Not looking into this right now.
	var reason string
	if runResult.Status() == "success" {
		return g.tasker.Succeeded(job, logFile)
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
	return g.tasker.Failed(job, reason, logFile)
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

	regHost := "docker.io"
	repo, _ := dc.ParseRepositoryTag(job.Image())
	split := strings.Split(repo, "/")
	if len(split) >= 3 {
		// then we have an explicit registry
		regHost = split[0]
	}
	regAuth := ""
	l.Infof("registries: %+v", g.conf.Registries)
	reg := g.conf.Registries[regHost]
	if reg != nil {
		if reg.Auth != "" {
			regAuth = reg.Auth
		}
		if reg.Username != "" {
			regAuth = base64.StdEncoding.EncodeToString([]byte(reg.Username + ":" + reg.Password))
		}
	}

	containerTask := &goferTask{
		command: "",
		config:  "",
		envVars: map[string]string{},
		id:      job.Id(),
		image:   job.Image(),
		timeout: job.Timeout(),
		auth:    regAuth,
	}
	containerTask.payload = job.Payload()

	l.Debugln("About to run", containerTask)
	runResult := g.driver.Run(containerTask, isCancelledChn)
	l.WithFields(log.Fields{
		"status": runResult.Status(),
		"error":  runResult.Error(),
	}).Debugln("Run result")

	log := runResult.Log()
	g.updateTaskStatusAndLog(ctx, job, runResult, log)
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
