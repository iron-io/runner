package main

import (
	"github.com/iron-io/titan/api/client"
	// "github.com/iron-io/titan/api/models"
	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/runner/docker"
	"os"
	"time"
)

func main() {
	log.SetLevel(log.DebugLevel)

	host := os.Getenv("API_URL")
	if host == "" {
		host = "http://localhost:8080"
	}

	jc := client.JobClient{
		Host: host,
	}
	for {
		log.Infoln("Asking for job")
		job, err := jc.GetJob()
		if err != nil {
			log.Errorln("We've got an error!", err)
			continue
		}
		if job == nil {
			time.Sleep(1 * time.Second)
			continue
		}
		job.StartedAt = time.Now().Unix()
		log.Infoln("Got job:", job)
		s, err := docker.DockerRun(job)
		job.FinishedAt = time.Now().Unix()
		if err != nil {
			log.Errorln("We've got an error!", err)
			job.Status = "error"
			job.Error = err.Error()
			jc.UpdateJob(*job)
			continue
		}
		job.Status = "success"
		jc.UpdateJob(*job)
		log.Infoln("output:", s)
	}
}
