package runner

// Stopper interface is used for things that can be stopped.
type Stopper interface {

	// Stop tells the thing to stop processing immediately.
	Stop()
}
