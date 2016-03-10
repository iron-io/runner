package main

import (
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/runner/docker"
	titan_go "github.com/iron-io/titan_go"
)

func main() {
	log.SetLevel(log.DebugLevel)

	host := os.Getenv("API_URL")
	if host == "" {
		host = "http://localhost:8080"
	}

	jc := titan_go.NewJobsApiWithBasePath(host)
	for {
		log.Infoln("Asking for job")
		jobsResp, err := jc.JobsGet(1)
		if err != nil {
			log.Errorln("We've got an error!", err)
			time.Sleep(5 * time.Second)
			continue
		}
		jobs := jobsResp.Jobs
		if len(jobs) < 1 {
			time.Sleep(1 * time.Second)
			continue
		}
		job := jobs[0]
		job.StartedAt = time.Now()
		log.Infoln("Got job:", job)
		result, err := docker.DockerRun(job)
		job.Status = "running"
		_, errUpdate := jc.JobIdPatch(job.Id, titan_go.JobWrapper{job})
		if errUpdate != nil {
			log.Errorln("ERROR PATCHING:", errUpdate)
		}
		s := ""
		if err == nil {
			s, err = docker.Wait(job, result)
		}
		job.CompletedAt = time.Now()
		if err != nil {
			if err.Error() == "timeout" {
				log.Errorln("Timeout!")
				job.Status = "timeout"
			} else {
				log.Errorln("We've got an error!", err)
				job.Status = "error"
				job.Error = err.Error()
			}
			if job.Retries > 0 {
				// then we create a new job
				log.Debugln("Retrying job")
				ja, err := jc.JobsPost(titan_go.NewJobsWrapper{
					Jobs: []titan_go.NewJob{
						titan_go.NewJob{
							Name:         job.Name,
							Image:        job.Image,
							Payload:      job.Payload,
							Delay:        job.RetriesDelay,
							Timeout:      job.Timeout,
							Retries:      job.Retries - 1,
							RetriesDelay: job.RetriesDelay,
							RetryFromId:  job.Id,
						},
					},
				})
				if err != nil {
					log.Errorln("Error posting retry job", err)
				}
				job.RetryId = ja.Jobs[0].Id
			}
			_, err := jc.JobIdPatch(job.Id, titan_go.JobWrapper{job})
			if err != nil {
				log.Errorln("ERROR PATCHING:", err)
			}
			continue
		}
		log.Infoln("output:", s)
		job.Status = "success"
		_, err = jc.JobIdPatch(job.Id, titan_go.JobWrapper{job})
		if err != nil {
			log.Errorln("ERROR PATCHING:", err)
		}
	}
}
