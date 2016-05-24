package common

const (
	whereisdocker = "unix:///var/run/docker.sock"
)

type Config struct {
	Docker         string `json:"docker"`
	Memory         uint64 `json:"memory"`
	CPUShares      int64  `json:"cpu_shares"`
	DefaultTimeout uint   `json:"timeout"`
}

func (c *Config) Defaults() {
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
