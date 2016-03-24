package docker

import (
	"io"
	"sync/atomic"
)

// limitedWriter writes until n bytes are written, then writes
// an overage line and skips any further writes.
type limitedWriter struct {
	W io.Writer
	N int64
}

func (l *limitedWriter) Write(p []byte) (n int, err error) {
	var overrage = []byte("maximum log file size exceeded")

	// we expect there may be concurrent writers, so to be safe..
	left := atomic.LoadInt64(&l.N)
	if left <= 0 {
		return 0, io.EOF // TODO EOF? really? does it matter?
	}
	n, err = l.W.Write(p)
	left = atomic.AddInt64(&l.N, -int64(n))
	if left <= 0 {
		l.W.Write(overrage)
	}
	return n, err
}
