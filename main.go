package main

import (
	"fmt"

	"golang.org/x/net/context"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/runner/agent"
	"github.com/iron-io/titan/runner/configloader"
	"github.com/iron-io/titan/runner/drivers"
	"github.com/iron-io/titan/runner/drivers/docker"
	"github.com/iron-io/titan/runner/drivers/mock"
	"github.com/iron-io/titan/runner/tasker"
)

func main() {

	ctx := agent.BaseContext(context.Background())

	runnerConfig := configloader.RunnerConfiguration()
	au := agent.ConfigAuth{runnerConfig.Registries}

	l := log.WithFields(log.Fields{})

	// TODO: can we just ditch environment here since we have a global Runner object now?
	env := common.NewEnvironment(func(e *common.Environment) {
		// Put stats initialization based off config over here.
	})

	// Create
	tasker := tasker.New(configloader.ApiURL(), l, &au)
	driver, err := selectDriver(env, runnerConfig)
	if err != nil {
		l.WithError(err).Fatalln("error selecting container driver")
	}

	runner := agent.NewRunner(env, runnerConfig, tasker, driver)
	runner.Run(ctx)
}

func selectDriver(env *common.Environment, conf *agent.Config) (drivers.Driver, error) {
	switch conf.Driver {
	case "docker":
		docker := docker.NewDocker(env, conf.DriverConfig)
		return docker, nil
	case "mock":
		return mock.New(), nil
	}
	return nil, fmt.Errorf("driver %v not found", conf.Driver)
}
