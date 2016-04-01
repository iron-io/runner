package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/Sirupsen/logrus"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"github.com/spf13/viper"
)

type Config struct {
	ApiUrl       string
	Concurrency  int
	DriverConfig *drivercommon.Config
}

func InitConfig(v *viper.Viper) *Config {
	config := &Config{}
	apiUrl := v.GetString("API_URL")
	config.ApiUrl = apiUrl
	config.Concurrency = v.GetInt("concurrency")

	dconfig := &drivercommon.Config{}
	dconfig.Memory = int64(v.GetInt("memory"))
	dconfig.CPUShares = int64(v.GetInt("cpu_shares"))
	dconfig.DefaultTimeout = uint(v.GetInt("timeout"))
	dconfig.Defaults()
	config.DriverConfig = dconfig

	return config
}

func main() {
	v := viper.New()
	v.SetDefault("concurrency", 5)
	v.SetDefault("API_URL", "http://localhost:8080")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetConfigName("config")
	v.AddConfigPath(".")
	v.AutomaticEnv() // picks up env vars automatically
	err := v.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.UnsupportedConfigError); ok {
			log.Infoln("Couldn't read config file", err)
			// ignore
		} else {
			log.Fatalln("Error reading config file", err)
		}
	}

	config := InitConfig(v)
	tasker := NewTasker(config)

	done := make(chan struct{}, 1)
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-c
		log.Info("received signal", "signal", sig)
		close(done)
	}()

	Run(config, tasker, BoxTime{}, done)
}
