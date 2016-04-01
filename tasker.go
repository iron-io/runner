package main

import (
	"io"
	"io/ioutil"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/go/common"
	titan_go "github.com/iron-io/titan_go"
)

type Tasker struct {
	api *titan_go.JobsApi
}

// Titan tasker.
func NewTasker(config *Config) *Tasker {
	api := titan_go.NewJobsApiWithBasePath(config.ApiUrl)
	return &Tasker{api}
}

func (t *Tasker) Job(ctx *common.Context) *titan_go.Job {
	var job *titan_go.Job
	for {
		jobs, err := t.api.JobsConsumeGet(1)
		if err != nil {
			log.Errorln("Tasker JobsConsumeGet", "err", err)
		} else if len(jobs.Jobs) > 0 {
			job = &jobs.Jobs[0]
			break
		}
		time.Sleep(1 * time.Second)
	}
	return job
}

func (t *Tasker) Update(ctx *common.Context, job *titan_go.Job) error {
	log.Debugln("Sending PATCH to update job", job)
	j, err := t.api.JobIdPatch(job.Id, titan_go.JobWrapper{*job})
	if err != nil {
		log.Errorln("Update failed", "job", job.Id, "err", err)
		return err
	}
	log.Infoln("Got back", j)
	return nil
}

// TODO: this should be on server side
func (t *Tasker) RetryTask(ctx *common.Context, job *titan_go.Job) error {
	panic("Not implemented Retry")
}

func (t *Tasker) IsCancelled(ctx *common.Context, job *titan_go.Job) bool {
	wrapper, err := t.api.JobIdGet(job.Id)
	if err != nil {
		log.Errorln("JobIdGet from Cancel", "err", err)
		return false
	}

	// FIXME(nikhil) Current branch does not capture cancellation.
	return wrapper.Job.Status == "error"
}

func (t *Tasker) Log(ctx *common.Context, job *titan_go.Job, r io.Reader) {
	bytes, err := ioutil.ReadAll(r)
	if err != nil {
		log.Errorln("Error reading log", "err", err)
		return
	}

	log.Infoln("Log is ", string(bytes))
	log.Errorln("Titan does not support log upload yet!")
}
