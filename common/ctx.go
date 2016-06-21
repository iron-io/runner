package common

import (
	"github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
)

// WithLogger stores the logger.
func WithLogger(ctx context.Context, l logrus.FieldLogger) context.Context {
	return context.WithValue(ctx, "logger", l)
}

// Logger returns the structured logger.
func Logger(ctx context.Context) logrus.FieldLogger {
	l, ok := ctx.Value("logger").(logrus.FieldLogger)
	if !ok {
		return logrus.StandardLogger()
	}
	return l
}
