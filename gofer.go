package runner

import (
	"fmt"
	"runtime/debug"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/runner/drivers"
	"golang.org/x/net/context"
)

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
		"type":        "gofer",
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
	t := time.Duration(g.conf.DriverConfig.DefaultTimeout) * time.Second
	if job.Timeout() != 0 {
		t = time.Duration(job.Timeout()) * time.Second
	}
	// Just in case we make a calculation mistake.
	// TODO: trap this condition
	// if t > 24*time.Hour {
	// 	ctx.Warn("task has really long timeout of greater than a day")
	// }
	// Log task timeout values so we can get a good idea of what people generally set it to.

	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	go g.checkForJobCancellation(ctx, cancel, job)

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
	runResult, err := g.driver.Run(ctx, job)
	if err != nil {
		// If err is set, then this is a Titan system error, not an error in the user code execution
		l.WithError(err).Errorln("system error executing job! May need to shutdown this machine")
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

// checkForJobCancellation checks if job was cancelled in the API, typically by user or some other event.
func (g *gofer) checkForJobCancellation(ctx context.Context, cancel context.CancelFunc, job drivers.ContainerTask) {
	defer func() {
		if e := recover(); e != nil {
			log.Errorln("checkForJobCancellation panic", e)
			debug.PrintStack()
			go g.checkForJobCancellation(ctx, cancel, job)
		}
	}()
	for {
		ic := g.tasker.IsCancelled(job)
		if ic {
			cancel()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-g.clock.After(5 * time.Second):
		}

	}
}
