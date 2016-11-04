// Copyright 2016 Iron.io
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mock

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	"github.com/iron-io/runner/drivers"
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
