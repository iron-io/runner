package main

import (
	"io/ioutil"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/runner/drivers"
	"github.com/iron-io/titan/runner/drivers/common"
	titan_go "github.com/iron-io/titan_go"
)

func main() {
	log.SetLevel(log.DebugLevel)

	host := os.Getenv("API_URL")
	if host == "" {
		host = "http://localhost:8080"
	}

	var cfg = common.Config{}
	cfg.Defaults()
	cfg.Root = "."
	cfg.Memory = 128 * 1024 * 1024
	drv, err := FactoryDriver(Docker, cfg, "localhost")
	if err != nil {
		log.Fatalln("error creating Docker client", err)
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
		log.Infof("Got job: %+v\n", job)
		cancelledChan := make(chan bool)
		job.StartedAt = time.Now()
		s := drv.Run(&JobWrapper{&job}, cancelledChan)
		job.CompletedAt = time.Now()

		if hasErroredOrTimedOut(s) {
			err := s.Error()
			job.Status = s.Status()
			job.Error_ = err.Error()

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
				log.Infoln("ja:", ja)
				job.RetryId = ja.Jobs[0].Id
			}
			if _, err := jc.JobIdPatch(job.Id, titan_go.JobWrapper{job}); err != nil {
				log.Errorln("ERROR PATCHING:", err)
			}
			continue
		}
		log.Infoln("Job status:", s.Status())
		bytes, err := ioutil.ReadAll(s.Log())
		if err != nil {
			log.Errorln("Could not read log", err)
		} else {
			log.Println(string(bytes))
		}
		job.Status = drivers.StatusSuccess
		_, err = jc.JobIdPatch(job.Id, titan_go.JobWrapper{job})
		if err != nil {
			log.Errorln("ERROR PATCHING:", err)
		}
	}
}

func hasErroredOrTimedOut(s drivers.RunResult) bool {
	return s.Error() != nil || s.Status() == drivers.StatusTimeout
}
