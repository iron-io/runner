package common

import (
	"os"

	"github.com/Sirupsen/logrus"
)

func SetLogLevel() {

	ll := os.Getenv("LOG_LEVEL")
	if ll == "" {
		ll = "info"
	}
	logrus.WithFields(logrus.Fields{"level": ll}).Info("Setting log level to")
	logLevel, err := logrus.ParseLevel(ll)
	if err != nil {
		logrus.WithFields(logrus.Fields{"level": ll}).Warn("Could not parse log level, setting to INFO")
		logLevel = logrus.InfoLevel
	}
	logrus.SetLevel(logLevel)

}
