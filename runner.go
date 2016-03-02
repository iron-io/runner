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

	jc := titan_go.NewCoreApiWithBasePath(host)
	for {
		log.Infoln("Asking for job")
		jobs, err := jc.JobsGet()
		if err != nil {
			log.Errorln("We've got an error!", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if len(jobs.Jobs) < 1 {
			time.Sleep(1 * time.Second)
			continue
		}
		job := jobs.Jobs[0]
		job.StartedAt = time.Now()
		log.Infoln("Got job:", job)
		s, err := docker.DockerRun(job)
		job.CompletedAt = time.Now()
		if err != nil {
			log.Errorln("We've got an error!", err)
			job.Status = "error"
			job.Error = err.Error()
			jc.JobIdPatch(job.Id, titan_go.JobWrapper{job})
			continue
		}
		job.Status = "success"
		jc.JobIdPatch(job.Id, titan_go.JobWrapper{job})
		log.Infoln("output:", s)
	}
}
