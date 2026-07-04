package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestMuxConnSendWritesFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	mc := newMuxConn(client, func(uint8, []byte, func()) {}, ConnConfig{
		QueueSizes: [numPriorities]int{4, 4, 4},
	})
	defer mc.Close()

	if err := mc.Send(PriorityRaft, 9, []byte("ping")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	msgType, body, release, err := readFrame(server)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	release()
	if msgType != 9 {
		t.Fatalf("msgType = %d, want 9", msgType)
	}
	if string(body) != "ping" {
		t.Fatalf("body = %q, want %q", body, "ping")
	}
}

func TestMuxConnRPCMatchesResponse(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	mc := newMuxConn(client, func(uint8, []byte, func()) {}, ConnConfig{
		QueueSizes: [numPriorities]int{4, 4, 4},
	})
	defer mc.Close()

	go func() {
		msgType, body, release, err := readFrame(server)
		if err != nil {
			return
		}
		defer release()
		if msgType != MsgTypeRPCRequest {
			return
		}
		reqID := decodeRequestID(body)
		var bufs net.Buffers
		writeFrame(&bufs, MsgTypeRPCResponse, encodeRPCResponse(reqID, 0, []byte("ok")))
		_, _ = bufs.WriteTo(server)
	}()

	resp, err := mc.RPC(context.Background(), PriorityRPC, 41, encodeRPCRequest(41, []byte("req")))
	if err != nil {
		t.Fatalf("RPC() error = %v", err)
	}
	if !bytes.Equal(resp, []byte("ok")) {
		t.Fatalf("resp = %q, want %q", resp, "ok")
	}
}

func TestMuxConnRPCDoesNotAllocateWriteAckChannel(t *testing.T) {
	writer := &priorityWriter{stopCh: make(chan struct{})}
	for i := range writer.queues {
		writer.queues[i] = make(chan writeItem, 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mc := &MuxConn{
		writer:     writer,
		pending:    newPendingMap(16),
		readerDone: make(chan struct{}),
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := mc.RPC(ctx, PriorityRPC, 7, encodeRPCRequest(7, []byte("req")))
		errCh <- err
	}()

	var item writeItem
	select {
	case item = <-writer.queues[PriorityRPC]:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc write item")
	}
	if item.done != nil {
		t.Fatal("RPC write item has a per-call ack channel")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RPC() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc cancellation")
	}
}

func TestMuxConnRPCWriteFailureWakesPendingCall(t *testing.T) {
	raw := newWriteFailConn()
	mc := newMuxConn(raw, func(uint8, []byte, func()) {}, ConnConfig{
		QueueSizes: [numPriorities]int{4, 4, 4},
	})
	defer mc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := mc.RPC(ctx, PriorityRPC, 7, encodeRPCRequest(7, []byte("req")))
	if err == nil {
		t.Fatal("RPC() error = nil, want write failure")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RPC() waited for context deadline after write failure: %v", err)
	}
}

func TestMuxConnCloseFailsPendingRPC(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	mc := newMuxConn(client, func(uint8, []byte, func()) {}, ConnConfig{
		QueueSizes: [numPriorities]int{4, 4, 4},
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := mc.RPC(context.Background(), PriorityRPC, 7, encodeRPCRequest(7, []byte("req")))
		errCh <- err
	}()

	_, _, release, err := readFrame(server)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	release()

	mc.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("RPC() error = nil, want failure")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for pending RPC to fail")
	}
}

func TestMuxConnDispatchReleasesFrameAfterHandler(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	var released atomic.Int32
	releaseCh := make(chan struct{}, 1)
	mc := newMuxConn(client, func(msgType uint8, body []byte, release func()) {
		if msgType != 3 || string(body) != "msg" {
			t.Errorf("dispatch got type=%d body=%q", msgType, body)
		}
		release()
		released.Add(1)
		releaseCh <- struct{}{}
	}, ConnConfig{
		QueueSizes: [numPriorities]int{4, 4, 4},
	})
	defer mc.Close()

	var bufs net.Buffers
	writeFrame(&bufs, 3, []byte("msg"))
	if _, err := bufs.WriteTo(server); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	select {
	case <-releaseCh:
		if released.Load() != 1 {
			t.Fatalf("release count = %d, want 1", released.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for dispatch")
	}
}

func decodeRequestID(body []byte) uint64 {
	if len(body) < 8 {
		return 0
	}
	return bytesToUint64(body[:8])
}

func bytesToUint64(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = (v << 8) | uint64(x)
	}
	return v
}

func TestMuxConnRPCContextCancel(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	mc := newMuxConn(client, func(uint8, []byte, func()) {}, ConnConfig{
		QueueSizes: [numPriorities]int{4, 4, 4},
	})
	defer mc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	go func() {
		_, _, release, err := readFrame(server)
		if err == nil {
			release()
		}
	}()

	_, err := mc.RPC(ctx, PriorityRPC, 8, encodeRPCRequest(8, []byte("req")))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RPC() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestMuxConnHandleRPCResponseCopiesBody(t *testing.T) {
	mc := &MuxConn{
		pending: newPendingMap(16),
	}
	ch := make(chan rpcResponse, 1)
	mc.pending.Store(9, ch)

	body := encodeRPCResponse(9, 0, []byte("ok"))
	mc.handleRPCResponse(body)
	body[9] = 'x'

	resp := <-ch
	if string(resp.body) != "ok" {
		t.Fatalf("resp.body = %q, want %q", resp.body, "ok")
	}
}

type writeFailConn struct {
	closed chan struct{}
}

func newWriteFailConn() *writeFailConn {
	return &writeFailConn{closed: make(chan struct{})}
}

func (c *writeFailConn) Read(_ []byte) (int, error) {
	<-c.closed
	return 0, io.ErrClosedPipe
}

func (c *writeFailConn) Write(_ []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func (c *writeFailConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *writeFailConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *writeFailConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *writeFailConn) SetDeadline(time.Time) error      { return nil }
func (c *writeFailConn) SetReadDeadline(time.Time) error  { return nil }
func (c *writeFailConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }
