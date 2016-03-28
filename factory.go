package main

import (
	"github.com/iron-io/titan/runner/drivers"
	"github.com/iron-io/titan/runner/drivers/common"
	"github.com/iron-io/titan/runner/drivers/docker"
)

type DriverType string

const (
	Docker DriverType = "docker"
)

func FactoryDriver(driverName DriverType, cfg common.Config, hostname string) (drivers.Driver, error) {
	switch driverName {
	case Docker:
		return docker.NewDocker(cfg, hostname)
	default:
		panic("invalid driver")
	}
}
