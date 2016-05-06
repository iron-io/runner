// Interface for all container drivers

package drivers

import (
	"errors"
	"io"

	"golang.org/x/net/context"
)

type Driver interface {
	// Run executes the task. If task runs, drivers.RunResult will be returned. If something fails outside the task (eg: Docker), it will return error.
	Run(ctx context.Context, task ContainerTask) (RunResult, error)
}

// RunResult will provide methods to access the job completion status, logs, etc.
type RunResult interface {
	// RunResult implementations should do any cleanup in here. All fields should
	// be considered invalid after Close() is called.
	io.Closer

	// Error is an actionable/checkable error from the container.
	Error() error

	// Status should return the current status of the task.
	// It must never return Enqueued.
	Status() string
}

type ContainerTask interface {
	Command() string
	Config() string
	EnvVars() map[string]string
	Id() string
	Image() string
	Payload() string
	Timeout() uint
	Auth() string
	// Drivers should write output log to this writer. Must be non-nil. Use
	// io.Discard if log is irrelevant.
	Logger() io.Writer
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
