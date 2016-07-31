package main

import (
	"time"

	"github.com/Sirupsen/logrus"
	titan_common "github.com/iron-io/titan/common"
	"github.com/iron-io/titan/common/stats"
	"github.com/iron-io/titan/runner/agent"
	"github.com/iron-io/titan/runner/config"
	"github.com/iron-io/titan/runner/drivers/docker"
	"github.com/iron-io/titan/runner/swapi"
	swapi_client "github.com/iron-io/titan/runner/swapi/client" // TODO tuck this into swapi tasker...
	"golang.org/x/net/context"
)

const VERSION = "3.0.71"

type Config struct {
	Stats stats.Config `json:"stats"`

	swapi.SwapiOptions
	swapi_client.HTTPConfig // TODO tuck into swapi tasker options...
	config.Config
}

func main() {
	ctx := agent.BaseContext(context.Background())

	var conf Config

	config.Load(&conf)

	conf.URL = conf.ApiURL
	conf.Version = VERSION

	if conf.ClusterId == "" || conf.ClusterToken == "" {
		logrus.Fatal("CLUSTER_ID and CLUSTER_TOKEN required.")
	}

	env := titan_common.NewEnvironment(func(env *titan_common.Environment) {
		env.Statter = stats.New(conf.Stats)
	})

	swapiClient, err := swapi_client.NewHTTPClient(conf.HTTPConfig)
	if err != nil {
		logrus.WithError(err).Fatal("error configuring swapi client, check config")
	}

	zzz := 1 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		cluster, err := swapiClient.GetCluster(conf.ClusterId)
		if err == nil {
			// TODO: Restrict disk space cluster.DiskSpace, Titan runner does not support this yet
			logrus.WithFields(logrus.Fields{"c": cluster}).Info("cluster")
			conf.RunnerConfig.DriverConfig.Memory = uint64(cluster.Memory)
			break
		}
		logrus.WithError(err).WithFields(logrus.Fields{"cluster_id": conf.ClusterId}).Error("Could not lookup cluster, retrying indefinitely. Make sure cluster exists and double check token.")
		time.Sleep(zzz)
		zzz *= 2
	}

	var clock agent.BoxTime

	conf.MemoryPerJob = conf.RunnerConfig.DriverConfig.Memory
	swapiBackend, err := swapi.NewSwapi(env, swapiClient, clock, conf.SwapiOptions)
	if err != nil {
		logrus.WithError(err).Fatal("error configuring swapi client, check config")
	}

	docker := docker.NewDocker(env, conf.RunnerConfig.DriverConfig)
	r := agent.NewRunner(env, conf.RunnerConfig, swapiBackend, docker)
	r.Run(ctx)
}
