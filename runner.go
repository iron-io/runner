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
	timeoutString := os.Getenv("TIMEOUT")
	if timeoutString == "" {
		timeoutString = "10"
	}
	timeout := int(timeoutString)

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
		s, err := docker.DockerRun(job, timeout)
		job.CompletedAt = time.Now()
		if err != nil {
			if err.Error() == "Timeout" {
				log.Errorln("Timeout!")
				job.Status = "timeout"
			} else {
				log.Errorln("We've got an error!", err)
				job.Status = "error"
				job.Error = err.Error()
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
