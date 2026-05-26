package gnet

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestHandleWSTrafficCopiesInboundBufferOnlyOnce(t *testing.T) {
	encoded := encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{1, 2, 3, 4}, []byte("hello websocket"))
	conn := &allocTestGnetConn{next: encoded}
	state := &connState{
		mode:      connModeWSFrames,
		wsInbound: make([]byte, 0, len(encoded)),
		queue:     make([]connEvent, 0, 1),
	}
	group := &engineGroup{}

	assertWSHandleResult := func() {
		t.Helper()
		state.wsInbound = state.wsInbound[:0]
		state.queue = state.queue[:0]
		conn.next = encoded
		group.handleWSTraffic(conn, state)
		if len(state.queue) != 1 {
			t.Fatalf("queued events = %d, want 1", len(state.queue))
		}
		event := state.queue[0]
		if event.kind != connEventData || event.op != wsOpcodeText {
			t.Fatalf("event = {kind:%d op:%d}, want data text", event.kind, event.op)
		}
		if !bytes.Equal(event.data, []byte("hello websocket")) {
			t.Fatalf("payload = %q, want %q", event.data, "hello websocket")
		}
	}
	assertWSHandleResult()

	allocs := testing.AllocsPerRun(1000, func() {
		state.wsInbound = state.wsInbound[:0]
		state.queue = state.queue[:0]
		conn.next = encoded
		group.handleWSTraffic(conn, state)
	})
	if allocs > 2 {
		t.Fatalf("allocs = %.0f, want <= 2", allocs)
	}
}

func TestHandleWSTrafficAvoidsInboundBufferCopyForSingleFrame(t *testing.T) {
	encoded := encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{1, 2, 3, 4}, []byte("hello websocket"))
	conn := &allocTestGnetConn{next: encoded}
	state := &connState{
		mode:      connModeWSFrames,
		queue:     make([]connEvent, 0, 1),
		wsInbound: make([]byte, 0, len(encoded)),
	}
	group := &engineGroup{}

	allocs := testing.AllocsPerRun(1000, func() {
		state.wsInbound = state.wsInbound[:0]
		state.queue = state.queue[:0]
		conn.next = encoded
		group.handleWSTraffic(conn, state)
	})
	if allocs != 0 {
		t.Fatalf("allocs = %.0f, want 0 for single complete websocket frame", allocs)
	}
}

type allocTestGnetConn struct {
	next              []byte
	ctx               any
	lastAsyncWrite    []byte
	lastAsyncWritev   [][]byte
	autoAsyncCallback bool
	outboundBuffered  int
}

func (c *allocTestGnetConn) Read(p []byte) (int, error)         { return 0, io.EOF }
func (c *allocTestGnetConn) WriteTo(w io.Writer) (int64, error) { return 0, nil }
func (c *allocTestGnetConn) Next(n int) ([]byte, error) {
	if n == -1 || n >= len(c.next) {
		buf := c.next
		c.next = nil
		return buf, nil
	}
	buf := c.next[:n]
	c.next = c.next[n:]
	return buf, nil
}
func (c *allocTestGnetConn) Peek(n int) ([]byte, error)                    { return c.next, nil }
func (c *allocTestGnetConn) Discard(n int) (int, error)                    { return n, nil }
func (c *allocTestGnetConn) InboundBuffered() int                          { return len(c.next) }
func (c *allocTestGnetConn) Write(p []byte) (int, error)                   { return len(p), nil }
func (c *allocTestGnetConn) ReadFrom(r io.Reader) (int64, error)           { return 0, nil }
func (c *allocTestGnetConn) SendTo(buf []byte, addr net.Addr) (int, error) { return len(buf), nil }
func (c *allocTestGnetConn) Writev(bs [][]byte) (int, error)               { return 0, nil }
func (c *allocTestGnetConn) Flush() error                                  { return nil }
func (c *allocTestGnetConn) OutboundBuffered() int                         { return c.outboundBuffered }
func (c *allocTestGnetConn) AsyncWrite(buf []byte, callback gnetv2.AsyncCallback) error {
	c.lastAsyncWrite = buf
	c.lastAsyncWritev = nil
	if c.autoAsyncCallback && callback != nil {
		_ = callback(c, nil)
	}
	return nil
}
func (c *allocTestGnetConn) AsyncWritev(bs [][]byte, callback gnetv2.AsyncCallback) error {
	c.lastAsyncWrite = nil
	c.lastAsyncWritev = bs
	if c.autoAsyncCallback && callback != nil {
		_ = callback(c, nil)
	}
	return nil
}
func (c *allocTestGnetConn) Fd() int                                  { return 0 }
func (c *allocTestGnetConn) Dup() (int, error)                        { return 0, nil }
func (c *allocTestGnetConn) SetReadBuffer(size int) error             { return nil }
func (c *allocTestGnetConn) SetWriteBuffer(size int) error            { return nil }
func (c *allocTestGnetConn) SetLinger(secs int) error                 { return nil }
func (c *allocTestGnetConn) SetKeepAlivePeriod(d time.Duration) error { return nil }
func (c *allocTestGnetConn) SetKeepAlive(enabled bool, idle, intvl time.Duration, cnt int) error {
	return nil
}
func (c *allocTestGnetConn) SetNoDelay(noDelay bool) error                         { return nil }
func (c *allocTestGnetConn) Context() any                                          { return c.ctx }
func (c *allocTestGnetConn) EventLoop() gnetv2.EventLoop                           { return nil }
func (c *allocTestGnetConn) SetContext(ctx any)                                    { c.ctx = ctx }
func (c *allocTestGnetConn) LocalAddr() net.Addr                                   { return allocTestAddr("local") }
func (c *allocTestGnetConn) RemoteAddr() net.Addr                                  { return allocTestAddr("remote") }
func (c *allocTestGnetConn) Wake(callback gnetv2.AsyncCallback) error              { return nil }
func (c *allocTestGnetConn) CloseWithCallback(callback gnetv2.AsyncCallback) error { return nil }
func (c *allocTestGnetConn) Close() error                                          { return nil }
func (c *allocTestGnetConn) SetDeadline(t time.Time) error                         { return nil }
func (c *allocTestGnetConn) SetReadDeadline(t time.Time) error                     { return nil }
func (c *allocTestGnetConn) SetWriteDeadline(t time.Time) error                    { return nil }

type allocTestAddr string

func (a allocTestAddr) Network() string { return string(a) }
func (a allocTestAddr) String() string  { return string(a) }
