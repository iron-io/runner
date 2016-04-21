package main

import (
	"io/ioutil"
	"net/url"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	httptransport "github.com/go-openapi/runtime/client"
	strfmt "github.com/go-openapi/strfmt"
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
	u, err := url.Parse(config.ApiUrl)
	if err != nil {
		log.Fatal(err)
	}
	api := titan_client.New(httptransport.New(u.Host, "/v1", []string{u.Scheme}), strfmt.Default)
	f, _ := ioutil.TempFile("", "crap-") // TODO: what's this for?
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

func (t *Tasker) Start(job *client_models.Job) error {
	l := t.log.WithFields(log.Fields{
		"action": "Start",
		"job_id": job.ID,
	})
	params := jobs.NewPostGroupsGroupNameJobsIDStartParams().WithGroupName(job.GroupName).WithID(job.ID)
	body := &client_models.Start{
		StartedAt: strfmt.DateTime(time.Now().UTC()),
	}
	params.WithBody(body)

	j, err := t.api.Jobs.PostGroupsGroupNameJobsIDStart(params)
	if err != nil {
		l.WithError(err).Errorln("failed")
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
	param.WithBody(&client_models.Complete{CompletedAt: strfmt.DateTime(time.Now().UTC())})
	_, err := t.api.Jobs.PostGroupsGroupNameJobsIDSuccess(param)
	if err != nil {
		log.Errorln("JobIdSuccessPost", "jobId", job.ID, "err", err)
	}
	return t.Log(job, r)
}

func (t *Tasker) Failed(job *client_models.Job, reason string, r *os.File) error {
	param := jobs.NewPostGroupsGroupNameJobsIDErrorParams().WithGroupName(job.GroupName).WithID(job.ID)
	param.WithBody(&client_models.Complete{CompletedAt: strfmt.DateTime(time.Now().UTC()), Reason: reason})

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
