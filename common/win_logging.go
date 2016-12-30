// +build !linux,!darwin

package common

import "errors"

func NewSyslogHook(scheme, host string, port int, prefix string) error {
	return errors.New("Syslog not supported on this system.")
}
