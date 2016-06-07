package configloader

import (
	"bytes"
	"os"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/runner/agent"
	drivercommon "github.com/iron-io/titan/runner/drivers/common"
	"github.com/pivotal-golang/bytefmt"
	"github.com/spf13/viper"
)

// This package provides a function to read a complete runner configuration
// from various sources (currently JSON file and environment variables). Just
// importing the configloader package will cause it to read in configuration
// from the sources. Callers may then use RunnerConfiguration() to get an
// initialized runner configuration. Other methods provide access to some common functionality.
// A basic configuration looks like:
//
//     {
//       "runner": {
//         ...
//       },
//       "stats": {
//         ...
//       },
//       "log_level": "..."
//     }
//
// Callers can use LogLevel() to obtain the log level.
// configloader will also set the runner's log package's log level to the value obtained here.
// StatsConfig() returns a stats configuration which can be passed to titan
// stats.New(). The runner's stats are not initialized using this
// configuration. Use the environment passed to agent.NewRunner() to handle
// this for now.
//
// For more configuration sub-trees related to the app specific behaviour that
// wraps a runner, use ExtendedConfiguration(key) to obtain a configuration
// sub-tree.
//
// This package is currently shared by the Titan runner and Swapi runner. Be
// careful when making changes to preserve compatibility.
//
// The following top-level keys are reserved for runner configuration, since
// they reflect the names of the environment variables captured in the process:
// - log_level
// - driver
// - concurrency
// - memory_per_job
//
// configloader will terminate if any configuration does not match valid values.
// For this reason, it should only be used from a binary's main().

var runnerConfig agent.Config
var logLevel string
var apiURL string

func init() {
	// Set up defaults.
	viper.SetConfigName("config")
	viper.AddConfigPath(".")
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()
	viper.SetDefault("log_level", "info")
	viper.SetDefault("driver", "docker")
	viper.SetDefault("api_url", "http://localhost:8080")
	viper.SetDefault("memory_per_job", "256M")
	// If we do not set it here, viper will not load the relevant environment var. Set it to int's default value.
	viper.SetDefault("concurrency", "0")

	// Initialize viper from config.json.
	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.UnsupportedConfigError); ok {
			logrus.WithError(err).Error("Couldn't read config file, this is fine, it's not required")
			// ignore
		} else {
			logrus.WithError(err).Fatal("Error reading config file")
		}
	}

	// Initialize viper from CONFIG environment variable.
	if os.Getenv("CONFIG") != "" {
		viper.SetConfigType("json")
		err = viper.ReadConfig(bytes.NewBufferString(os.Getenv("CONFIG")))
		if err != nil {
			logrus.WithError(err).Fatal("Error reading CONFIG from env")
		}
	}

	// Set up defaults.
	dconfig := drivercommon.DefaultConfig()
	memPerJob, err := bytefmt.ToBytes(viper.GetString("memory_per_job"))
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"memory_per_job": viper.GetString("memory_per_job")}).Fatal("Invalid MEMORY_PER_JOB variable")
	}
	// todo: we should pass the entire config to the driver so they can use the same variable. Or make a config interface and pass it along.
	dconfig.Memory = memPerJob
	runnerConfig.DriverConfig = dconfig

	// Let loaded configuration overwrite defaults.
	if viper.InConfig("runner") {
		err = viper.Sub("runner").Unmarshal(&runnerConfig)
		if err != nil {
			logrus.WithError(err).Fatal("could not unmarshal runner configuration from config")
		}
	}

	// Finally, let top-level reserved keys overwrite any existing settings.
	runnerConfig.Driver = viper.GetString("driver")

	if concurrency := viper.GetInt("concurrency"); concurrency > 0 {
		runnerConfig.Concurrency = concurrency
	}

	apiURL = viper.GetString("api_url")
	logLevel = viper.GetString("log_level")

	logrus.WithFields(logrus.Fields{"runner_configuration": runnerConfig}).Info("configloader runner configuration")
}

func RunnerConfiguration() *agent.Config {
	return &runnerConfig
}

func LogLevel() string {
	return logLevel
}

func ApiURL() string {
	return apiURL
}

func ExtendedConfiguration(key string) *viper.Viper {
	if !viper.InConfig(key) {
		return nil
	}

	return viper.Sub(key)
}
