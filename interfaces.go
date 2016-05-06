package runner

import (
	"math"
	"math/rand"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/runner/drivers"
)

type Tasker interface {
	// drivers.ContainerTask returns a new Task that is ready to be run. It should never
	// return a 'nil' drivers.ContainerTask, rather, the implementer should never return
	// until there is a valid task to return.
	Job() drivers.ContainerTask

	// IsCancelled checks to see whether the task has been cancelled.
	// On any error, IsCancelled should return false.
	IsCancelled(task drivers.ContainerTask) bool

	// Start will set the task to a running state.
	Start(task drivers.ContainerTask) error

	// Succeeded will set the task to a completed state.
	Succeeded(task drivers.ContainerTask) error

	// Failed will set the task to an error state.
	Failed(task drivers.ContainerTask, err string) error
}

type Temporary interface {
	Temporary() bool
}

func isTemporary(err error) bool {
	v, ok := err.(Temporary)
	return ok && v.Temporary()
}

type retryTasker struct {
	Tasker
	rng *rand.Rand
}

// RetryTasker will retry any Tasker methods that it can,
// the exceptions currently being IsCancelled and Job as
// their APIs already gracefully handle errors. If the error
// received does not implement Temporary or is not a
// temporary error (of type Temporary), we'll return the error.
func RetryTasker(t Tasker) Tasker {
	return &retryTasker{t, newRNG(time.Now().UnixNano())}
}

func (r *retryTasker) Start(t drivers.ContainerTask) error {
	return r.retry(func() error { return r.Tasker.Start(t) })
}

func (r *retryTasker) Succeeded(t drivers.ContainerTask) error {
	return r.retry(func() error { return r.Tasker.Succeeded(t) })
}

func (r *retryTasker) Failed(t drivers.ContainerTask, errString string) error {
	return r.retry(func() error { return r.Tasker.Failed(t, errString) })
}

// retries forever with exponential backoff until we get
// an error that isn't temporary or no error from f().
func (r *retryTasker) retry(f func() error) error {
	var b backoff
	var err error
	for i := 0; ; i++ {
		err = f()
		if err == nil || !isTemporary(err) {
			break
		}
		if i%10 == 0 {
			log.Errorln("Retrying job endpoint many times, is the Tasker dead?", "retry", i, "err", err)
		}
		b.sleep(r.rng)
	}
	return err
}

type backoff int

func (b *backoff) sleep(rng *rand.Rand) {
	const (
		maxexp   = 7
		interval = 25 * time.Millisecond
	)

	// 25-50ms, 50-100ms, 100-200ms, 200-400ms, 400-800ms, 800-1600ms, 1600-3200ms, 3200-6400ms
	d := time.Duration(math.Pow(2, float64(*b))) * interval
	d += (d * time.Duration(rng.Float64()))

	time.Sleep(d)

	if *b < maxexp {
		(*b)++
	}
}

func newRNG(seed int64) *rand.Rand {
	return rand.New(&lockedSource{src: rand.NewSource(seed)})
}

// taken from go1.5.1 math/rand/rand.go +233-250
// bla bla if it puts a hole in the earth don't sue them
type lockedSource struct {
	lk  sync.Mutex
	src rand.Source
}

func (r *lockedSource) Int63() (n int64) {
	r.lk.Lock()
	n = r.src.Int63()
	r.lk.Unlock()
	return
}

func (r *lockedSource) Seed(seed int64) {
	r.lk.Lock()
	r.src.Seed(seed)
	r.lk.Unlock()
}
