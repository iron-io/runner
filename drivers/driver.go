// Interface for all container drivers

package drivers

import (
	"errors"
	"os"
)

type Driver interface {
	// Run(*models.Job) RunResult
	Run(task ContainerTask, isCancelled chan bool) RunResult
}

// RunResult will provide methods to access the job completion status, logs, etc.
type RunResult interface {
	// Err() is an actionable/checkable error from the container.
	Error() error

	// Status() should return the current status of the task.
	// It must never return Enqueued.
	Status() string

	// Log() will be a Reader interface that allows the driver to read the content
	// of the log output for the task that was run.
	// Each driver is free to implement this in its own way, either streaming or at
	// the end of execution.
	//
	// It must return a valid Reader at any time.
	Log() *os.File // TODO: change to io.Reader
}

type ContainerTask interface {
	Command() string
	Config() string
	EnvVars() map[string]string
	Id() string
	Image() string
	Payload() string
	Timeout() uint
	Auth()	string
}

// Set of acceptable errors coming from container engines to TaskRunner
var (
	// ErrOutOfMemory for OOM in container engine
	ErrOutOfMemory = errors.New("out of memory error")
	// ErrUnknown for unspecified errors
	ErrUnknown = errors.New("unknown error")
)

// TODO: ensure some type is applied to these statuses.
const (
	// task statuses
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusError     = "error"
	StatusTimeout   = "timeout"
	StatusKilled    = "killed"
	StatusCancelled = "cancelled"
)
