package main

import (
	"bytes"
	"os"
	"os/signal"
	"syscall"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/runner"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"github.com/iron-io/titan/runner/tasker"
	"github.com/spf13/viper"
	"golang.org/x/net/context"
)

func InitConfig() *runner.Config {
	config := &runner.Config{}
	err := viper.Unmarshal(config)
	if err != nil {
		log.WithError(err).Fatalln("could not unmarshal registries from config")
	}
	config.ApiUrl = viper.GetString("api_url")
	config.Concurrency = viper.GetInt("concurrency")

	dconfig := &drivercommon.Config{}
	dconfig.JobsDir = viper.GetString("jobs_dir")
	dconfig.Memory = int64(viper.GetInt("memory"))
	dconfig.CPUShares = int64(viper.GetInt("cpu_shares"))
	dconfig.DefaultTimeout = uint(viper.GetInt("timeout"))
	dconfig.Defaults()
	config.DriverConfig = dconfig

	return config
}

func main() {
	viper.SetDefault("concurrency", 5)
	viper.SetEnvPrefix("titan")
	viper.SetDefault("api_url", "http://localhost:8080")
	viper.SetConfigName("config")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.UnsupportedConfigError); ok {
			log.Infoln("Couldn't read config file, this is fine, it's not required.", err)
			// ignore
		} else {
			log.WithError(err).Fatalln("Error reading config file")
		}
	}
	if os.Getenv("CONFIG") != "" {
		viper.SetConfigType("json")
		err = viper.ReadConfig(bytes.NewBufferString(os.Getenv("CONFIG")))
		if err != nil {
			log.WithError(err).Fatalln("Error reading CONFIG from env")
		}
	}
	config := InitConfig()
	common.SetLogLevel()

	log.Debugf("Config: %+v", config)
	// viper.Debug()

	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT)
	go func() {
		sig := <-c
		log.Info("received signal", sig)
		cancel()
	}()

	hostname, err := os.Hostname()
	if err != nil {
		log.WithError(err).Fatal("couldn't resolve hostname")
	}
	l := log.WithFields(log.Fields{
		"hostname": hostname,
	})
	tasker := tasker.New(config.ApiUrl, l)

	runner.Run(config, tasker, runner.BoxTime{}, ctx)
}
