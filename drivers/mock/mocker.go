package mock

import (
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	"github.com/iron-io/worker/runner/drivers"
	"golang.org/x/net/context"
)

func New() drivers.Driver {
	return &Mocker{}
}

type Mocker struct {
	count int
}

func (m *Mocker) Prepare(context.Context, drivers.ContainerTask) (io.Closer, error) {
	return ioutil.NopCloser(strings.NewReader("")), nil // dummy closer
}

func (m *Mocker) Run(ctx context.Context, task drivers.ContainerTask) (drivers.RunResult, error) {
	m.count++
	if m.count%100 == 0 {
		return nil, fmt.Errorf("Mocker error! Bad.")
	}
	return &runResult{
		error:       nil,
		StatusValue: "success",
	}, nil
}

type runResult struct {
	error
	StatusValue string
}

func (runResult *runResult) Status() string {
	return runResult.StatusValue
}
