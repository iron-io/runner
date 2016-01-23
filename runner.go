package main

import (
	"fmt"
	"github.com/iron-io/docker-job/api/client"
	// "github.com/iron-io/docker-job/api/models"
	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/docker-job/runner/docker"
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
		log.Infoln("Got job:", job)
		s, err := docker.DockerRun(job)
		if err != nil {
			fmt.Println("We gots an error!", err)
			continue
		}
		fmt.Println("output:", s)
	}
}
