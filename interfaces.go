package runner

import (
	"io"

	"github.com/iron-io/titan/runner/drivers"
)

type Tasker interface {
	// drivers.ContainerTask returns a new Task that is ready to be run. It should never
	// return a 'nil' drivers.ContainerTask, rather, the implementer should never return
	// until there is a valid task to return.
	Job() drivers.ContainerTask

	// IsCancelled checks to see whether the task has been cancelled.
	// On any error, IsCancelled should return false.
	IsCancelled(drivers.ContainerTask) bool

	Start(drivers.ContainerTask) error
	Succeeded(drivers.ContainerTask, io.ReadSeeker) error
	Failed(drivers.ContainerTask, string, io.ReadSeeker) error
}

type Logger interface {
	// Log attempts to upload a given log for a task once [for now].
	Log(drivers.ContainerTask, io.ReadSeeker) error
}
