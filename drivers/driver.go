// Interface for all container drivers

package drivers

import (
	"errors"
	"io"
	"time"

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
	// WriteStat writes a single Stat, implementation need not be thread safe.
	WriteStat(Stat)
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

// Stat is a bucket of stats from a driver at a point in time for a certain task.
type Stat struct {
	Timestamp time.Time
	Metrics   map[string]uint64
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

// Decimate will down sample to a max number of points in a given sample by
// averaging samples together. i.e. max=240, if we have 240 samples, return
// them all, if we have 480 samples, every 2 samples average them (and time
// distance), and return 240 samples. This is relatively naive and if len(in) >
// max, <= max points will be returned, not necessarily max: length(out) =
// ceil(length(in)/max) -- feel free to fix this, setting a relatively high max
// will allow good enough granularity at higher lengths, i.e. for max of 1 hour
// tasks, sampling every 1s, decimate will return 15s samples if max=240.
// Large gaps in time between samples (a factor > (last-start)/max) will result
// in a shorter list being returned to account for lost samples.
// Decimate will modify the input list for efficiency, it is not copy safe.
// Input must be sorted by timestamp or this will fail gloriously.
func Decimate(maxSamples int, stats []Stat) []Stat {
	if len(stats) <= maxSamples {
		return stats
	} else if maxSamples <= 0 { // protect from pricks
		return nil
	}
	// this is relatively naive decimation, but will do the trick. in place

	// make fixed windows, fit samples into them
	windowDur := stats[len(stats)-1].Timestamp.Sub(stats[0].Timestamp) / time.Duration(maxSamples)
	start := stats[0].Timestamp
	var offset int // the gap between the last stored and the next
	for i := 0; i+offset < len(stats); i++ {
		var t time.Duration // sum duration from start, avg
		firstOff := offset
		stats[i] = stats[i+offset] // sum everything here

		for i+offset < len(stats) {
			cur := stats[i+offset].Timestamp
			st := start.Add(time.Duration(i) * windowDur)
			end := start.Add(time.Duration(1+i) * windowDur)
			lastOne := i+offset+1 == len(stats) // since integer division will be imprecise, re-fit this one
			if end.Before(cur) && !lastOne {
				if offset-firstOff > 0 {
					break
				}
			} else if lastOne || end.Equal(cur) || cur.After(st) && cur.Before(end) {
				t += stats[i+offset].Timestamp.Sub(stats[i].Timestamp)
				for k, v := range stats[i+offset].Metrics {
					stats[i].Metrics[k] += v
				}
			}
			offset++
		}
		// avg
		if offset-firstOff > 0 {
			stats[i].Timestamp = stats[i].Timestamp.Add(t / time.Duration(offset-firstOff))
			for k, v := range stats[i].Metrics {
				stats[i].Metrics[k] = v / uint64(offset-firstOff)
			}
		}
		offset-- // since i will get incremented, want to keep same place
	}
	return stats[:len(stats)-offset]
}
