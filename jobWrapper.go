package main

import (
	titan_go "github.com/iron-io/titan_go"
)

type JobWrapper struct {
	job *titan_go.Job
}

func (jw *JobWrapper) Command() string {
	return ""
}

func (jw *JobWrapper) Config() string {
	return ""
}

func (jw *JobWrapper) EnvVars() map[string]string {
	return map[string]string{}
}

func (jw *JobWrapper) Id() string {
	return jw.job.Id
}

func (jw *JobWrapper) Image() string {
	return jw.job.Image
}

func (jw *JobWrapper) Payload() string {
	return jw.job.Payload
}

func (jw *JobWrapper) Timeout() uint {
	return uint(jw.job.Timeout)
}
