package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/go/common"
	gofercommon "github.com/iron-io/go/runner/gofer/common"
	"github.com/iron-io/titan/jobserver/models"
	"github.com/iron-io/titan/runner/drivers"
	"github.com/iron-io/titan/runner/drivers/docker"
	"github.com/iron-io/titan_go"
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
}

func (g *goferTask) Command() string            { return g.command }
func (g *goferTask) Config() string             { return g.config }
func (g *goferTask) EnvVars() map[string]string { return g.envVars }
func (g *goferTask) Id() string                 { return g.id }
func (g *goferTask) Image() string              { return g.image }
func (g *goferTask) Payload() string            { return g.payload }
func (g *goferTask) Timeout() uint              { return g.timeout }

type gofer struct {
	conf       *Config
	tasker     *Tasker
	clock      gofercommon.Clock
	hostname   string
	instanceID string
	driver     drivers.Driver
	*common.Environment
}

func newGofer(conf *Config, tasker *Tasker, clock gofercommon.Clock, hostname string, driver drivers.Driver) (*gofer, error) {
	var err error
	g := &gofer{
		conf:        conf,
		tasker:      tasker,
		clock:       clock,
		driver:      driver,
		hostname:    hostname,
		Environment: common.NewEnvironment(),
	}
	g.instanceID, err = instanceID()
	if err != nil {
		return nil, err
	}

	return g, nil
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

func Run(conf *Config, tasker *Tasker, clock gofercommon.Clock, done <-chan struct{}) {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal("couldn't resolve hostname", "err", err)
	}

	docker, err := docker.NewDocker(conf.DriverConfig, hostname)
	if err != nil {
		log.Fatal("couldn't start container driver", "err", err)
	}

	g, err := newGofer(conf, tasker, clock, hostname, docker)
	if err != nil {
		log.Fatal("couldn't start runner", "err", err)
	}

	log.Infoln("starting runners", "n", conf.Concurrency)
	fin := make(chan struct{}, conf.Concurrency)
	for i := 0; i < conf.Concurrency; i++ {
		go func(i int) {
			g.runner(i, done)
			fin <- struct{}{}
		}(i)
	}

	<-done
	log.Info("shutting down, let all tasks finish! or else...")
	for i := 1; i <= conf.Concurrency; i++ {
		<-fin
		log.Info("task finished", "still_running", conf.Concurrency-i)
	}
	log.Info("all tasks done, exiting cleanly. thank you, come again.")
}

func (g *gofer) runner(i int, done <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			g.Inc("runner", "panicked", 1, 0.1)
			g.Inc("runner", g.instanceID+".panicked", 1, 0.1)
			log.Warnln("recovered from panic, restarting runner: stack", r)
			go g.runner(i, done)
		}
	}()

	// tasks only exists to allow shutdown to interrupt waiting for job
	tasks := make(chan *titan.Job, 1)
	for {
		ctx := common.NewContext(g.Environment, "runner", uint64(i))
		ctx.Debug("getting task")
		sw := ctx.Time("get task")
		select {
		case <-done:
			return
		default: // don't spawn getting a job if we're done
			select {
			case <-done:
				return
			case tasks <- g.tasker.Job(ctx):
				sw.Stop()
				task := <-tasks // never blocks
				ctx.Debug("starting task", "id", task.Id)

				sw = ctx.Time("run task")
				g.runTask(ctx, task)
				sw.Stop()

				ctx.Debug("task finished", "id", task.Id)
			}
		}
	}
}

func (g *gofer) logAndLeave(ctx *common.Context, job *titan.Job, msg string, err error) {
	g.tasker.Update(ctx, job)
	// TODO: panic("How do we implement")
	// ctx.Error(msg, "err", err)
}

func (g *gofer) recordTaskCompletion(job *titan.Job, status string, duration time.Duration) {
	statName := fmt.Sprintf("completion.%s", status)
	// todo: remove project stuff
	projectStatName := fmt.Sprintf("p.%s.completion.%s", status)

	g.Inc("task", statName, 1, 1.0)
	g.Inc("task", projectStatName, 1, 1.0)
	g.Time("task", statName, duration, 1.0)
	g.Time("task", projectStatName, duration, 1.0)
}

func (g *gofer) updateTaskStatusAndLog(ctx *common.Context, job *titan.Job, runResult drivers.RunResult, logFile *os.File) error {
	ctx.Debug("updating task")
	now := time.Now()
	job.CompletedAt = now

	// Docker driver should seek!
	// This is REALLY stupid. The swagger online generator has obviously not been
	// tested because it can't generate a correct swagger definition for a form
	// upload that has a file field. It uses Google's query parser but that
	// parser does not support encoding os.File. It seems like go-swagger does
	// this correctly, so I've filed #73. Meanwhile, serialize to a string.
	logFile.Seek(0, 0)
	var b bytes.Buffer
	io.Copy(&b, logFile)

	// We can't set job.Reason because Reason is generated as an empty struct for some reason o_O Not looking into this right now.
	var reason string
	if runResult.Status() == "success" {
		job.Status = models.StatusSuccess
		// g.tasker.Succeeded(ctx, job, b.String())
	} else if runResult.Status() == "error" {
		job.Status = models.StatusError
		reason = "bad_exit"
	} else if runResult.Status() == "killed" {
		// same as cancelled
		job.Status = models.StatusCancelled
		reason = "killed"
	} else if runResult.Status() == "timeout" {
		job.Status = models.StatusError
		reason = "timeout"
	} else if runResult.Status() == "cancelled" {
		job.Status = models.StatusCancelled
		reason = "client_request"
		// FIXME(nikhil): Implement
		// This should already be cancelled on server side, so might not even need to send back. Only reason would be to show that it may have partially ran?
		// g.tasker.Cancelled(ctx, job)
		// return nil
	}

	// FIXME(nikhil): Set job error details field.
	if err := runResult.Error(); err != nil {
		ctx.Debug("Job failure ", err)
	}

	// g.tasker.Failed(ctx, job, reason, b.String())
	// g.recordTaskCompletion(job, job.Status, now.Sub(job.StartedAt))

	log.Debugln("reason", reason)

	err := g.tasker.Update(ctx, job)
	if err != nil {
		log.Errorln("failed to update job!")
		return err
	}

	// TODO: deal with log. If it's small enough, just upload with job, if it's big, send to separate endpoint.

	// ctx.Debug("uploading log")
	// sw := ctx.Time("upload log")

	// // Docker driver should seek!
	// logFile.Seek(0, 0)
	// g.tasker.Log(ctx, job, logFile)
	// sw.Stop()
	return nil
}

func (g *gofer) runTask(ctx *common.Context, job *titan.Job) {
	isCancelledChn := make(chan bool)
	isCancelledCtx, isCancelledStopSignal := context.WithCancel(context.Background())
	defer isCancelledStopSignal()
	go g.emitCancellationSignal(ctx, job, isCancelledCtx, isCancelledChn)

	ctx.Debug("setting task status to running", "task_id", job.Id)
	now := g.clock.Now()
	job.StartedAt = now
	job.Status = gofercommon.StatusRunning
	g.tasker.Update(ctx, job)

	containerTask := &goferTask{
		command: "",
		config:  "",
		envVars: map[string]string{},
		id:      job.Id,
		image:   job.Image,
		payload: job.Payload,
		timeout: uint(job.Timeout),
	}
	log.Infoln("About to run", containerTask)
	runResult := g.driver.Run(containerTask, isCancelledChn)
	log.Infoln("Run result", "err", runResult.Error(), "status", runResult.Status())
	log := runResult.Log()
	defer log.Close()

	if runResult.Error() != nil {
		job.Status = "error"
		// TODO: retries on server side, we just send status back
		// g.retryTask(ctx, job)
	}

	g.updateTaskStatusAndLog(ctx, job, runResult, log)
}

func (g *gofer) emitCancellationSignal(ctx *common.Context, job *titan.Job, isCancelledCtx context.Context, isCancelled chan bool) {
	defer func() {
		if e := recover(); e != nil {
			log.Errorln("emitCancellationSignal panic", e)
			go g.emitCancellationSignal(ctx, job, isCancelledCtx, isCancelled)
		}

	}()
	for {
		ic := g.tasker.IsCancelled(ctx, job)
		if ic {
			select {
			case <-isCancelledCtx.Done():
				return
			case isCancelled <- true:
				return
			}
		} else {
			select {
			case <-isCancelledCtx.Done():
				return
			case <-g.clock.After(5 * time.Second):
			}
		}
	}
}

func (g *gofer) retryTask(ctx *common.Context, job *titan.Job) {
	// FIXME(nikhil): Handle retry count.
	// FIXME(nikhil): Handle retry delay.
	err := g.tasker.RetryTask(ctx, job)
	if err != nil {
		ctx.Error("unable to get retry task", "err", err, "task_id", job)
		return
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
