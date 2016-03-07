// Interface for all container drivers

package drivers

type Driver interface {
	Run() RunResult
}

// RunResult will provide methods to access the job completion status, logs, etc.
type RunResult interface {
}