package mock

import (
	"fmt"

	"github.com/iron-io/titan/runner/drivers"
	"golang.org/x/net/context"
)

func New() drivers.Driver {
	return &Mocker{}
}

type Mocker struct {
	count int
}

func (m *Mocker) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	m.count++
	if m.count%100 == 0 {
		return nil, fmt.Errorf("Mocker error! Bad.")
	}
	return &runResult{
		Err:         nil,
		StatusValue: "success",
	}, nil
}

func (m *Mocker) EnsureUsableImage(ctx context.Context, task drivers.ContainerTask) error {
	return nil
}

type runResult struct {
	Err         error
	StatusValue string
}

func (runResult *runResult) Error() error {
	return runResult.Err
}

func (runResult *runResult) Status() string {
	return runResult.StatusValue
}

func (runResult *runResult) Close() error {
	return nil
}
