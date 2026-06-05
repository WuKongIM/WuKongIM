package testkit

import (
	"net"
	"sync"
	"time"
)

// BlockingConn is a net.Conn test double whose reads block until Close and whose writes block until ReleaseWrite.
type BlockingConn struct {
	mu               sync.Mutex
	closeOnce        sync.Once
	releaseWriteOnce sync.Once

	closed       chan struct{}
	writeRelease chan struct{}
}

// NewBlockingConn creates a BlockingConn with blocked reads and writes.
func NewBlockingConn() *BlockingConn {
	c := &BlockingConn{}
	c.channels()
	return c
}

// Read blocks until the connection is closed, then returns net.ErrClosed.
func (c *BlockingConn) Read(_ []byte) (int, error) {
	closed, _ := c.channels()
	<-closed
	return 0, net.ErrClosed
}

// Write blocks until ReleaseWrite is called or the connection is closed.
func (c *BlockingConn) Write(p []byte) (int, error) {
	closed, writeRelease := c.channels()
	select {
	case <-writeRelease:
		return len(p), nil
	case <-closed:
		return 0, net.ErrClosed
	}
}

// Close closes the connection and unblocks pending reads and writes; it is idempotent.
func (c *BlockingConn) Close() error {
	closed, _ := c.channels()
	c.closeOnce.Do(func() {
		close(closed)
	})
	return nil
}

// ReleaseWrite unblocks current and future Write calls.
func (c *BlockingConn) ReleaseWrite() {
	_, writeRelease := c.channels()
	c.releaseWriteOnce.Do(func() {
		close(writeRelease)
	})
}

// LocalAddr returns a stable in-memory local address for tests.
func (c *BlockingConn) LocalAddr() net.Addr {
	return blockingAddr("blocking-local")
}

// RemoteAddr returns a stable in-memory remote address for tests.
func (c *BlockingConn) RemoteAddr() net.Addr {
	return blockingAddr("blocking-remote")
}

// SetDeadline accepts deadline updates for net.Conn compatibility.
func (c *BlockingConn) SetDeadline(_ time.Time) error {
	return nil
}

// SetReadDeadline accepts read deadline updates for net.Conn compatibility.
func (c *BlockingConn) SetReadDeadline(_ time.Time) error {
	return nil
}

// SetWriteDeadline accepts write deadline updates for net.Conn compatibility.
func (c *BlockingConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

func (c *BlockingConn) channels() (chan struct{}, chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed == nil {
		c.closed = make(chan struct{})
	}
	if c.writeRelease == nil {
		c.writeRelease = make(chan struct{})
	}
	return c.closed, c.writeRelease
}

type blockingAddr string

func (a blockingAddr) Network() string {
	return "blocking"
}

func (a blockingAddr) String() string {
	return string(a)
}
