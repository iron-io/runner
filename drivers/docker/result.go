package docker

import "os"

type runResult struct {
	Err         error
	StatusValue string
	LogData     *os.File
}

func (runResult *runResult) Error() error {
	return runResult.Err
}

func (runResult *runResult) Status() string {
	return runResult.StatusValue
}

func (runResult *runResult) Log() *os.File {
	return runResult.LogData
}
