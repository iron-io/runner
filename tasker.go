package main

import (
	"io"
	"io/ioutil"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/go/common"
	"github.com/iron-io/titan_go"
)

type Tasker struct {
	api *titan.JobsApi
}

// Titan tasker.
func NewTasker() *Tasker {
	// FIXME(nikhil): Build API from path obtained from config.
	api := titan.NewJobsApiWithBasePath("http://localhost:8080")
	return &Tasker{api}
}

func (t *Tasker) Job(ctx *common.Context) *titan.Job {
	var job *titan.Job
	for {
		jobs, err := t.api.JobsGet(1)
		if err != nil {
			log.Errorln("Tasker JobsGet", "err", err)
		} else if len(jobs.Jobs) > 0 {
			job = &jobs.Jobs[0]
			break
		}

		time.Sleep(1 * time.Second)
	}
	return job
}

func (t *Tasker) Update(ctx *common.Context, job *titan.Job) {
	_, err := t.api.JobIdPatch(job.Id, titan.JobWrapper{*job})
	if err != nil {
		log.Errorln("Update failed", "job", job.Id, "err", err)
	}
}

func (t *Tasker) RetryTask(ctx *common.Context, job *titan.Job) error {
	panic("Not implemented Retry")
}

func (t *Tasker) IsCancelled(ctx *common.Context, job *titan.Job) bool {
	wrapper, err := t.api.JobIdGet(job.Id)
	if err != nil {
		log.Errorln("JobIdGet from Cancel", "err", err)
		return false
	}

	// FIXME(nikhil) Current branch does not capture cancellation.
	return wrapper.Job.Status == "error"
}

func (t *Tasker) Log(ctx *common.Context, job *titan.Job, r io.Reader) {
	bytes, err := ioutil.ReadAll(r)
	if err != nil {
		log.Errorln("Error reading log", "err", err)
		return
	}

	log.Infoln("Log is ", string(bytes))
	log.Errorln("Titan does not support log upload yet!")
}
