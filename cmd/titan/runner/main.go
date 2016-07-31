package main

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/common/stats"
	"github.com/iron-io/titan/runner/agent"
	"github.com/iron-io/titan/runner/config"
	"github.com/iron-io/titan/runner/drivers"
	"github.com/iron-io/titan/runner/drivers/docker"
	"github.com/iron-io/titan/runner/drivers/mock"
	"github.com/iron-io/titan/runner/tasker"
	"golang.org/x/net/context"
)

type Config struct {
	Stats stats.Config `json:"stats"`

	config.Config
}

func main() {
	ctx := agent.BaseContext(context.Background())

	var conf Config
	config.Load(&conf)

	runnerConfig := conf.RunnerConfig
	au := agent.ConfigAuth{runnerConfig.Registries}

	// TODO: can we just ditch environment here since we have a global Runner object now?
	// +1
	env := common.NewEnvironment(func(env *common.Environment) {
		env.Statter = stats.New(conf.Stats)
	})

	// Create
	tasker := tasker.New(conf.ApiURL, logrus.WithFields(logrus.Fields{}), &au)
	driver, err := selectDriver(env, runnerConfig)
	if err != nil {
		logrus.WithError(err).Fatal("error selecting container driver")
	}

	runner := agent.NewRunner(env, runnerConfig, tasker, driver)
	runner.Run(ctx)
}

func selectDriver(env *common.Environment, conf agent.Config) (drivers.Driver, error) {
	switch conf.Driver {
	case "docker":
		docker := docker.NewDocker(env, conf.DriverConfig)
		return docker, nil
	case "mock":
		return mock.New(), nil
	}
	return nil, fmt.Errorf("driver %v not found", conf.Driver)
}
