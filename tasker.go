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
	log   log.FieldLogger
}

// Titan tasker.
func NewTasker(config *Config, log log.FieldLogger) *Tasker {
	api := titan_client.New(httptransport.New("localhost:8080", "/v1", []string{"http"}), strfmt.Default)
	f, _ := ioutil.TempFile("", "crap-")
	return &Tasker{api, *f, log}
}

func (t *Tasker) Job() *client_models.Job {
	l := t.log.WithField("action", "DequeueJob")
	var job *client_models.Job
	param := jobs.NewGetJobsParams()
	for {
		jobs, err := t.api.Jobs.GetJobs(param)
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
		"job_id": job.ID,
	})
	l.Debugln("Sending PATCH to update job", job)
	j, err := t.api.Jobs.PatchGroupsGroupNameJobsID(jobs.NewPatchGroupsGroupNameJobsIDParams().WithGroupName(job.GroupName).WithID(job.ID).WithBody(&client_models.JobWrapper{job}))
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
	wrapper, err := t.api.Jobs.GetGroupsGroupNameJobsID(jobs.NewGetGroupsGroupNameJobsIDParams().WithGroupName(job.GroupName).WithID(job.ID))
	if err != nil {
		log.Errorln("JobIdGet from Cancel", "err", err)
		return false
	}

	return wrapper.Payload.Job.Status == "cancelled"
}

func (t *Tasker) Succeeded(job *client_models.Job, r *os.File) error {
	param := jobs.NewPostGroupsGroupNameJobsIDSuccessParams().WithGroupName(job.GroupName).WithID(job.ID)
	_, err := t.api.Jobs.PostGroupsGroupNameJobsIDSuccess(param)
	if err != nil {
		log.Errorln("JobIdSuccessPost", "jobId", job.ID, "err", err)
	}
	return t.Log(job, r)
}

func (t *Tasker) Failed(job *client_models.Job, reason string, r *os.File) error {
	param := jobs.NewPostGroupsGroupNameJobsIDErrorParams().WithGroupName(job.GroupName).WithID(job.ID).WithReason(reason)

	_, err := t.api.Jobs.PostGroupsGroupNameJobsIDError(param)
	if err != nil {
		log.Errorln("JobIdFailPost", "jobId", job.ID, "err", err)
	}
	return t.Log(job, r)
}

func (t *Tasker) Log(job *client_models.Job, logFile *os.File) error {
	param := jobs.NewPostGroupsGroupNameJobsIDLogParams().WithGroupName(job.GroupName).WithID(job.ID)
	if logFile != nil {
		param.WithLog(*logFile)
	} else {
		param.WithLog(t.dummy)
	}
	r, err := t.api.Jobs.PostGroupsGroupNameJobsIDLog(param)
	log.Println(r, err)
	if err != nil {
		log.WithError(err).Errorln("Uploading log")
	}

	return err
}
