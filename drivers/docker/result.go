package docker

type runResult struct {
	Err         error
	StatusValue string
	closer      func()
}

func (runResult *runResult) Error() error {
	return runResult.Err
}

func (runResult *runResult) Status() string {
	return runResult.StatusValue
}

func (runResult *runResult) Close() error {
	if runResult.closer != nil {
		runResult.closer()
	}
	return nil
}
