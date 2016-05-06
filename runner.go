package runner

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/common/stats"
	"github.com/iron-io/titan/runner/drivers"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"golang.org/x/net/context"
)

type BoxTime struct{}

func (BoxTime) Now() time.Time                         { return time.Now() }
func (BoxTime) Sleep(d time.Duration)                  { time.Sleep(d) }
func (BoxTime) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Config struct {
	Driver       string               `json:"driver"`
	Concurrency  int                  `json:"concurrency"`
	DriverConfig *drivercommon.Config `json:"driver_config"`
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
	log.FieldLogger
	// cancel   context.CancelFunc
	fin      chan struct{}
	stopChan chan struct{}
	conf     *Config
	tasker   Tasker
	driver   drivers.Driver
	clock    common.Clock
	env      *common.Environment
	hostname string
}

func NewRunner(env *common.Environment, conf *Config, tasker Tasker, driver drivers.Driver) *Runner {
	hostname, err := os.Hostname()
	if err != nil {
		log.WithError(err).Fatal("couldn't resolve hostname")
	}

	l := log.WithFields(log.Fields{
		"type":     "runner",
		"hostname": hostname,
	})

	r := &Runner{
		env:         env,
		conf:        conf,
		tasker:      tasker,
		driver:      driver,
		FieldLogger: l,
		clock:       BoxTime{},
		hostname:    hostname,
	}
	return r
}

func (r *Runner) Run(ctx context.Context) {

	r.Infoln("Runner starting...")

	// making a new context with cancel here so we can cancel from here down the tree, while still respecting the SIG cancel in main.
	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	// r.cancel = cancel

	r.Infoln("starting", r.conf.Concurrency, "gofers")
	fin := make(chan struct{}, r.conf.Concurrency)
	r.fin = fin

	r.stopChan = make(chan struct{}, 1)

	for i := 0; i < r.conf.Concurrency; i++ {
		go func(i int) {
			defer func() {
				fin <- struct{}{}
			}()
			g, err := newGofer(r.env, r.conf, r.tasker, r.clock, i, r.driver, r, r)
			if err != nil {
				r.WithError(err).Errorln("Error creating gofer", i)
				return
			}
			g.run(ctx2)
		}(i)
	}

Out:
	for {
		select {
		case <-ctx.Done(): // from ctrl-c or something external
			r.Infoln("got done")
			break Out
		case <-r.stopChan:
			r.Infoln("got stopChan")
			cancel()
			r.waitForGofersToFinish()
			// TODO: Restart docker, check memory issues, check disk space issues, etc. Also do those checks in a timer so it doesn't get to this point.
			r.Run(ctx)
			return
		}
	}

	r.Info("shutting down, let all tasks finish! or else...")
	r.waitForGofersToFinish()
	r.Info("all tasks finished, exiting cleanly. thank you, come again.")
}

func (r *Runner) Stop() {
	r.stopChan <- struct{}{}
}

func (r *Runner) waitForGofersToFinish() {
	r.Infoln("waiting for gofers to finish...")
	for i := 1; i <= r.conf.Concurrency; i++ {
		<-r.fin
		r.Infoln("task finished. ", r.conf.Concurrency-i, " still running")
	}
	r.Infoln("all gofers have finished processing.")
}
