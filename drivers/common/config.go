package common

import (
	"strings"
)

const (
	whereisdocker = "unix:///var/run/docker.sock"
)

type Config struct {
	JobsDir        string `json:"jobs_dir"`
	Docker         string `json:"docker"`
	Memory         int64  `json:"memory"`
	CPUShares      int64  `json:"cpu_shares"`
	DefaultTimeout uint   `json:"timeout"`
}

func (c *Config) Defaults() {
	// todo: move these to Viper defaults, see main.go
	if c.JobsDir == "" {
		c.JobsDir = "/jobs"
	} else {
		c.JobsDir = strings.TrimRight(c.JobsDir, "/")
	}
	if c.Docker == "" {
		c.Docker = whereisdocker
	}
	if c.Memory == 0 {
		c.Memory = 256 * 1024 * 1024
	}
	if c.CPUShares == 0 {
		c.CPUShares = 2
	}
	if c.DefaultTimeout == 0 {
		c.DefaultTimeout = 3600
	}
}
