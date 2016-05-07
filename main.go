package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/common"
	"github.com/iron-io/titan/runner/agent"
	"github.com/iron-io/titan/runner/drivers"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"github.com/iron-io/titan/runner/drivers/docker"
	"github.com/iron-io/titan/runner/drivers/mock"
	"github.com/iron-io/titan/runner/tasker"
	"github.com/spf13/viper"
	"golang.org/x/net/context"
)

func InitConfig() *agent.Config {
	// Set up defaults.
	dconfig := &drivercommon.Config{}
	dconfig.Defaults()
	config := &agent.Config{
		DriverConfig: dconfig,
	}
	err := viper.Unmarshal(config)
	if err != nil {
		log.WithError(err).Fatalln("could not unmarshal registries from config")
	}

	// The env var overrides we allow. It is unfortunate that viper.Unwrap() does
	// not deal with this.
	config.Driver = viper.GetString("driver")
	config.Concurrency = viper.GetInt("concurrency")
	config.ApiUrl = viper.GetString("api_url")
	return config
}

func main() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()
	viper.SetDefault("driver", "docker")
	viper.SetDefault("concurrency", 5)
	viper.SetDefault("api_url", "http://localhost:8080")

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

	{
		ll := viper.GetString("log.level")
		if ll == "" {
			ll = "info"
		}
		log.Infoln("Setting log level to", ll)
		logLevel, err := log.ParseLevel(ll)
		if err != nil {
			log.Warnln("Could not parse log level", ll, ". Setting to info")
			logLevel = log.InfoLevel
		}
		log.SetLevel(logLevel)
	}

	log.Infoln("regSSFASDF", viper.Get("registries"))

	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT)
	go func() {
		sig := <-c
		log.Infoln("received signal:", sig)
		cancel()
	}()

	hostname, err := os.Hostname()
	if err != nil {
		log.WithError(err).Fatal("couldn't resolve hostname")
	}
	l := log.WithFields(log.Fields{
		"hostname": hostname,
	})

	au := agent.ConfigAuth{config.Registries}

	tasker := tasker.New(config.ApiUrl, l, &au)
	l.Infoln("Using", config.Driver, "container driver")
	driver, err := selectDriver(l, config, hostname)
	if err != nil {
		l.WithError(err).Fatalln("error selecting container driver")
	}

	// TODO: can we just ditch environment here since we have a global Runner object now?
	env := common.NewEnvironment(func(e *common.Environment) {
		// Put stats initialization based off config over here.
	})
	runner := agent.NewRunner(env, config, tasker, driver)
	runner.Run(ctx)
}

func selectDriver(l log.FieldLogger, conf *agent.Config, hostname string) (drivers.Driver, error) {
	switch conf.Driver {
	case "docker":
		docker, err := docker.NewDocker(conf.DriverConfig, hostname)
		if err != nil {
			l.Fatal("couldn't start container driver", "err", err)
		}
		return docker, nil
	case "mock":
		return mock.New(), nil
	}
	return nil, fmt.Errorf("driver %v not found", conf.Driver)
}
