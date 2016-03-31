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

func InitConfig(v *viper.Viper) drivercommon.Config {
	config := drivercommon.Config{}
	config.Concurrency = int(v.GetFloat64("concurrency"))
	config.Root = v.GetString("root")
	config.Memory = int64(v.GetInt("memory"))
	config.CPUShares = int64(v.GetInt("cpu_shares"))
	config.DefaultTimeout = uint(v.GetInt("timeout"))

	config.Defaults()
	return config
}

func main() {
	v := viper.New()
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
	tasker := NewTasker()

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
