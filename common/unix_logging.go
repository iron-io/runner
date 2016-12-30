// +build !windows,!nacl,!plan9

package common

import (
	"io/ioutil"

	"github.com/Sirupsen/logrus"
)

func NewSyslogHook(scheme, host string, port int, prefix string) error {
	syslog, err := logrus_syslog.NewSyslogHook(scheme, host, port, prefix)
	if err != nil {
		logrus.WithFields(logrus.Fields{"uri": url, "to": to}).WithError(err).Error("unable to connect to syslog, defaulting to stderr")
		return
	}
	logrus.AddHook(syslog)
	// TODO we could support multiple destinations...
	logrus.SetOutput(ioutil.Discard)
}
