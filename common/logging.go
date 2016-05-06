package common

import (
	log "github.com/Sirupsen/logrus"
	"github.com/spf13/viper"
)

// SetLogLevel will read in a LOG_LEVEL env var and set the level accordingly.
func SetLogLevel() {
	ll := viper.GetString("log_level")
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
