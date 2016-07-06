package common

import (
	"io/ioutil"
	"net/url"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/Sirupsen/logrus/hooks/syslog"
)

func SetLogLevel(ll string) {
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

func SetLogDest(to, prefix string) {
	if to == "stderr" {
		return
	}

	// possible schemes: { udp, tcp, file }
	// file url must contain only a path, syslog must contain only a host[:port]
	// expect: [scheme://][host][:port][/path]
	// default scheme to udp:// if none given

	url, err := url.Parse(to)
	if url.Host == "" && url.Path == "" {
		logrus.WithFields(logrus.Fields{"to": to}).Warn("No scheme on logging url, adding udp://")
		// this happens when no scheme like udp:// is present
		to = "udp://" + to
		url, err = url.Parse(to)
	}
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"to": to}).Error("could not parse logging URI, defaulting to stderr")
		return
	}

	// File URL must contain only `url.Path`. Syslog location must contain only `url.Host`
	if (url.Host == "" && url.Path == "") || (url.Host != "" && url.Path != "") {
		logrus.WithFields(logrus.Fields{"to": to, "uri": url}).Error("invalid logging location, defaulting to stderr")
		return
	}

	switch url.Scheme {
	case "udp", "tcp":
		syslog, err := logrus_syslog.NewSyslogHook(url.Scheme, url.Host, 0, prefix)
		if err != nil {
			logrus.WithFields(logrus.Fields{"uri": url, "to": to}).WithError(err).Error("unable to connect to syslog, defaulting to stderr")
			return
		}
		logrus.AddHook(syslog)
		// TODO we could support multiple destinations...
		logrus.SetOutput(ioutil.Discard)
	case "file":
		f, err := os.OpenFile(url.Path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"to": to, "path": url.Path}).Error("cannot open file, defaulting to stderr")
			return
		}
		logrus.SetOutput(f)
	default:
		logrus.WithFields(logrus.Fields{"scheme": url.Scheme, "to": to}).Error("unknown logging location scheme, defaulting to stderr")
	}
}
