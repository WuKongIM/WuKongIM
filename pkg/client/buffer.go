package client

import (
	"bytes"
	"io"
	"net"
	"time"
)

type limWriter struct {
	w       io.Writer
	bufs    []byte
	limit   int
	pending *bytes.Buffer
	plimit  int
}

func (w *limWriter) appendString(str string) error {
	return w.appendBufs([]byte(str))
}

func (w *limWriter) appendBufs(bufs ...[]byte) error {
	for _, buf := range bufs {
		if len(buf) == 0 {
			continue
		}
		if w.pending != nil {
			w.pending.Write(buf)
		} else {
			w.bufs = append(w.bufs, buf...)
		}
	}
	if w.pending == nil && len(w.bufs) >= w.limit {
		return w.flush()
	}
	return nil
}

func (w *limWriter) writeDirect(data []byte) error {
	if _, err := w.w.Write(data); err != nil {
		return err
	}
	return nil
}

var i = 0

func (w *limWriter) flush() error {
	i++
	// fmt.Println("flush--->", i)
	// If a pending buffer is set, we don't flush. Code that needs to
	// write directly to the socket, by-passing buffers during (re)connect,
	// will use the writeDirect() API.
	if w.pending != nil {
		return nil
	}
	// Do not skip calling w.w.Write() here if len(w.bufs) is 0 because
	// the actual writer (if websocket for instance) may have things
	// to do such as sending control frames, etc..
	_, err := w.w.Write(w.bufs)
	w.bufs = w.bufs[:0]
	return err
}

func (w *limWriter) buffered() int {
	if w.pending != nil {
		return w.pending.Len()
	}
	return len(w.bufs)
}

func (w *limWriter) switchToPending() {
	w.pending = new(bytes.Buffer)
}

func (w *limWriter) flushPendingBuffer() error {
	if w.pending == nil || w.pending.Len() == 0 {
		return nil
	}
	_, err := w.w.Write(w.pending.Bytes())
	// Reset the pending buffer at this point because we don't want
	// to take the risk of sending duplicates or partials.
	w.pending.Reset()
	return err
}

func (w *limWriter) atLimitIfUsingPending() bool {
	if w.pending == nil {
		return false
	}
	return w.pending.Len() >= w.plimit
}

func (w *limWriter) doneWithPending() {
	w.pending = nil
}

type timeoutWriter struct {
	timeout time.Duration
	conn    net.Conn
	err     error
}

// Write implements the io.Writer interface.
func (tw *timeoutWriter) Write(p []byte) (int, error) {
	if tw.err != nil {
		return 0, tw.err
	}

	var n int
	tw.conn.SetWriteDeadline(time.Now().Add(tw.timeout))
	n, tw.err = tw.conn.Write(p)
	tw.conn.SetWriteDeadline(time.Time{})
	return n, tw.err
}

type limReader struct {
	r   io.Reader
	buf []byte
	off int
	n   int
}

func (r *limReader) doneWithConnect() {

}

func (r *limReader) Read() ([]byte, error) {
	if r.off >= 0 {
		off := r.off
		r.off = -1
		return r.buf[off:r.n], nil
	}
	var err error
	r.n, err = r.r.Read(r.buf)
	return r.buf[:r.n], err
}

func (r *limReader) ReadString(delim byte) (string, error) {
	var s string
build_string:
	// First look if we have something in the buffer
	if r.off >= 0 {
		i := bytes.IndexByte(r.buf[r.off:r.n], delim)
		if i >= 0 {
			end := r.off + i + 1
			s += string(r.buf[r.off:end])
			r.off = end
			if r.off >= r.n {
				r.off = -1
			}
			return s, nil
		}
		// We did not find the delim, so will have to read more.
		s += string(r.buf[r.off:r.n])
		r.off = -1
	}
	if _, err := r.Read(); err != nil {
		return s, err
	}
	r.off = 0
	goto build_string
}
