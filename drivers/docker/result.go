package docker

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
