package common

import (
	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
)

const (
	loggerKey = "logger"
)

func WithLogger(ctx context.Context, l log.FieldLogger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// GetLogger returns the structured logger.
func GetLogger(ctx context.Context) log.FieldLogger {
	l, _ := ctx.Value(loggerKey).(log.FieldLogger)
	return l
}
