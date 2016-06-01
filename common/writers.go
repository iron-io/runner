package common

import (
	"bytes"
	"errors"
	"io"
)

// lineWriter will break apart a stream of data into individual lines.
// Downstream writer will be called for each complete new line. When Flush
// is called, a newline will be appended if there isn't one at the end.
// Not thread-safe
type LineWriter struct {
	b *bytes.Buffer
	w io.Writer
}

func NewLineWriter(w io.Writer) *LineWriter {
	return &LineWriter{
		w: w,
		b: bytes.NewBuffer(make([]byte, 0, 1024)),
	}
}

func (li *LineWriter) Write(p []byte) (int, error) {
	n, err := li.b.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, errors.New("short write")
	}

	for {
		b := li.b.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}

		l := b[:i+1]
		ns, err := li.w.Write(l)
		if err != nil {
			return ns, err
		}
		li.b.Next(len(l))
	}

	return n, nil
}

func (li *LineWriter) Flush() (int, error) {
	b := li.b.Bytes()
	if len(b) == 0 {
		return 0, nil
	}

	if b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return li.w.Write(b)
}

// lastWritesWriter stores the last N writes in buffers
// Previously was putting a line-parsing writer upstream from this
type LastWritesWriter struct {
	tail int
	b    []*bytes.Buffer
}

func NewLastWritesWriter(n int) (*LastWritesWriter, error) {
	if n == 0 {
		return nil, errors.New("LastWriteWriter's buffer must be 1 or larger")
	}

	return &LastWritesWriter{
		tail: -1,
		b:    make([]*bytes.Buffer, n),
	}, nil
}

func (lnw *LastWritesWriter) Write(p []byte) (n int, err error) {
	newtail := (lnw.tail + 1) % len(lnw.b)

	t := lnw.b[newtail]
	if t == nil {
		t = bytes.NewBuffer(p)
	} else {
		t.Reset()
		t.Write(p)
	}
	lnw.b[newtail] = t

	lnw.tail = newtail

	return len(p), nil
}

func (lnw *LastWritesWriter) Fetch() [][]byte {
	var r [][]byte
	for y := 0; y < len(lnw.b); y++ {
		i := (lnw.tail + y + 1) % len(lnw.b)
		b := lnw.b[i]
		if b != nil {
			r = append(r, b.Bytes())
		}
	}
	return r
}
