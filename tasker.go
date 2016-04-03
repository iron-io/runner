package main

import (
	"io/ioutil"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	httptransport "github.com/go-swagger/go-swagger/httpkit/client"
	strfmt "github.com/go-swagger/go-swagger/strfmt"
	client_models "github.com/iron-io/titan/runner/client/models"
	titan_client "github.com/iron-io/titan/runner/client/titan"
	"github.com/iron-io/titan/runner/client/titan/jobs"
)

type Tasker struct {
	api   *titan_client.Titan
	dummy os.File
}

// Titan tasker.
func NewTasker(config *Config, log log.FieldLogger) *Tasker {
	api := titan_client.New(httptransport.New("localhost:8080", "/v1", []string{"http"}), strfmt.Default)
	f, _ := ioutil.TempFile("", "crap-")
	return &Tasker{api, *f}
}

func (t *Tasker) Job() *client_models.Job {
	l := t.log.WithField("action", "DequeueJob")
	var job *client_models.Job
	for {
		jobs, err := t.api.Jobs.GetJobsConsume(jobs.NewGetJobsConsumeParams())
		if err != nil {
			l.WithError(err).Errorln("dequeue job from api")
		} else if len(jobs.Payload.Jobs) > 0 && jobs.Payload.Jobs[0] != nil {
			job = jobs.Payload.Jobs[0]
			break
		}
		time.Sleep(1 * time.Second)
	}
	return job
}

func (t *Tasker) Update(job *client_models.Job) error {
	l := t.log.WithFields(log.Fields{
		"action": "UpdateJob",
		"job_id": job.Id,
	})
	l.Debugln("Sending PATCH to update job", job)
	j, err := t.api.Jobs.PatchJobID(jobs.NewPatchJobIDParams().WithID(job.ID).WithBody(&client_models.JobWrapper{job}))
	if err != nil {
		l.WithError(err).Errorln("Update failed")
		return err
	}
	l.Infoln("Got back", j)
	return nil
}

// TODO: this should be on server side
func (t *Tasker) RetryTask(job *client_models.Job) error {
	panic("Not implemented Retry")
}

func (t *Tasker) IsCancelled(job *client_models.Job) bool {
	wrapper, err := t.api.Jobs.GetJobID(jobs.NewGetJobIDParams().WithID(job.ID))
	if err != nil {
		log.Errorln("JobIdGet from Cancel", "err", err)
		return false
	}

	return *wrapper.Payload.Job.Status == "cancelled"
}

func (t *Tasker) Succeeded(job *client_models.Job, r *os.File) error {
	param := jobs.NewPostJobIDSuccessParams().WithID(job.ID)
	if r != nil {
		param = param.WithLog(*r)
	} else {
		param = param.WithLog(t.dummy)
	}
	_, err := t.api.Jobs.PostJobIDSuccess(param)
	if err != nil {
		log.Errorln("JobIdSuccessPost", "jobId", job.ID, "err", err)
	}
	return nil
}

func (t *Tasker) Failed(job *client_models.Job, reason string, r *os.File) error {
	param := jobs.NewPostJobIDFailParams().WithID(job.ID).WithReason(reason)
	if r != nil {
		param = param.WithLog(*r)
	} else {
		param = param.WithLog(t.dummy)
	}

	_, err := t.api.Jobs.PostJobIDFail(param)
	if err != nil {
		log.Errorln("JobIdFailPost", "jobId", job.ID, "err", err)
	}
	return nil
}

func (t *Tasker) Log(job *client_models.Job, logFile *os.File) error {
	panic("Not implemented")
	return nil
}
