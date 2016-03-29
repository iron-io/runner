package common

import (
	"strings"
)

const (
	whereisdocker = "unix:///var/run/docker.sock"
)

type Config struct {
	Concurrency    int    `json:"concurrent_tasks"`
	Root           string `json:"root"`
	Docker         string `json:"docker"`
	Memory         int64  `json:"memory"`
	CPUShares      int64  `json:"cpu_shares"`
	DefaultTimeout uint   `json:"timeout"`
}

func (c *Config) Defaults() {
	if c.Concurrency == 0 {
		c.Concurrency = 5
	}
	if c.Root == "" {
		c.Root = "/mnt"
	} else {
		c.Root = strings.TrimRight(c.Root, "/")
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
