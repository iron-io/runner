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
	Timeout() uint
	Auth() string
	// Drivers should write output log to this writer. Must be non-nil. Use
	// io.Discard if log is irrelevant.
	Logger() (stdout, stderr io.Writer)
	// Volumes may return an array of 2-element tuples, where the first element
	// is the path on the host, and the second element is the path in the
	// container. If at least one tuple is returned, the first tuple is set to
	// the working directory for the execution.
	//
	// Example:
	//   []string {
	//     []string{ "/my/task/dir", "/mnt" }
	//   }
	Volumes() [][2]string
	// return working directory to use in container. empty string
	// will not set this and default to container defaults.
	WorkDir() string
	// Close should be safe to call multiple times. Any errors occurred
	// during close should be logged from within.
	Close()
}

// Set of acceptable errors coming from container engines to TaskRunner
var (
	// ErrOutOfMemory for OOM in container engine
	ErrOutOfMemory = errors.New("out of memory error")
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
