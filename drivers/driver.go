// Interface for all container drivers

package drivers

import (
	"errors"
	"io"

	"golang.org/x/net/context"
)

type Driver interface {
	// Run should execute task on the implementation.
	// RunResult captures the result of task execution. This means if task
	// execution fails due to a problem in the task, Run() MUST return a valid
	// RunResult and nil as the error. The RunResult's Error() and Status()
	// should be used to indicate failure.
	// If the implementation itself suffers problems (lost of network, out of
	// disk etc.), a nil RunResult and an error message is preferred.
	//
	// Run() MUST monitor the context. task cancellation is indicated by
	// cancelling the context.
	// In addition, Run() should respect the task's timeout.
	Run(ctx context.Context, task ContainerTask) (RunResult, error)
}

// RunResult indicates only the final state of the task.
type RunResult interface {
	// RunResult implementations should do any cleanup in here. All fields should
	// be considered invalid after Close() is called.
	io.Closer

	// Error is an actionable/checkable error from the container.
	Error() error

	// Status should return the current status of the task.
	// Only valid options are {"error", "success", "timeout", "killed", "cancelled"}.
	Status() string
}

// The ContainerTask interface guides task execution across a wide variety of
// container oriented runtimes.
// This interface is unstable.
//
// FIXME: This interface is large, and it is currently a little Docker specific.
type ContainerTask interface {
	// Command returns the command to run within the container.
	Command() string
	// EnvVars returns environment variable key-value pairs.
	EnvVars() map[string]string
	Id() string
	// Image returns the runtime specific image to run.
	Image() string
	// Timeout is in seconds.
	Timeout() uint
	// Driver will write output log from task execution to these writers. Must be
	// non-nil. Use io.Discard if log is irrelevant.
	Logger() (stdout, stderr io.Writer)
	// Volumes returns an array of 2-element tuples indicating storage volume mounts.
	// The first element is the path on the host, and the second element is the
	// path in the container.
	Volumes() [][2]string
	// WorkDir returns the working directory to use for the task. Empty string
	// leaves it unset.
	WorkDir() string

	// Close is used to perform cleanup after task execution.
	// Close should be safe to call multiple times.
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

type Config struct {
	Docker         string `json:"docker" envconfig:"default=unix:///var/run/docker.sock,DOCKER"`
	Memory         uint64 `json:"memory" envconfig:"-"` // TODO uses outer now
	CPUShares      int64  `json:"cpu_shares" envconfig:"default=2,CPU_SHARES"`
	DefaultTimeout uint   `json:"timeout" envconfig:"default=3600,TASK_TIMEOUT"`
}

// for tests
func DefaultConfig() Config {
	return Config{
		Docker:         "unix:///var/run/docker.sock",
		Memory:         256 * 1024 * 1024,
		CPUShares:      0,
		DefaultTimeout: 3600,
	}
}
