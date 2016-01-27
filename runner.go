package main

import (
	"fmt"
	"github.com/iron-io/titan/api/client"
	// "github.com/iron-io/titan/api/models"
	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/runner/docker"
	"time"
)

func main() {
	log.SetLevel(log.DebugLevel)

	jc := client.JobClient{
		Host: "http://localhost:8080",
	}
	for {
		log.Infoln("Asking for job")
		job, err := jc.GetJob()
		if err != nil {
			fmt.Println("We gots an error!", err)
			continue
		}
		job.StartedAt = time.Now().Unix()
		log.Infoln("Got job:", job)
		s, err := docker.DockerRun(job)
		job.FinishedAt = time.Now().Unix()
		if err != nil {
			fmt.Println("We gots an error!", err)
			job.Status = "error"
			job.Error = err.Error()
			jc.UpdateJob(*job)
			continue
		}
		job.Status = "success"
		jc.UpdateJob(*job)
		fmt.Println("output:", s)
	}
}
