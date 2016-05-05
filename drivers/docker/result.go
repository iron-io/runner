package docker

import "os"

type runResult struct {
	Dir         string
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
	if runResult.Dir != "" {
		return os.RemoveAll(runResult.Dir)
	}

	return nil
}
