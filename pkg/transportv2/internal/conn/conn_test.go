package conn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

func TestConnSendWritesFrame(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	c.Start()
	defer c.Close(nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Send(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 7,
			Payload:   core.CopyOwnedBuffer([]byte("hello")),
		})
	}()

	frameCh := make(chan wire.Frame, 1)
	go func() {
		frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
		if err != nil {
			t.Errorf("ReadFrame() error = %v", err)
			return
		}
		frameCh <- frame
	}()

	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	frame := waitFrame(t, frameCh)
	defer frame.Body.Release()

	if frame.Header.Kind != core.FrameKindData {
		t.Fatalf("kind = %v, want %v", frame.Header.Kind, core.FrameKindData)
	}
	if frame.Header.Priority != core.PriorityRPC {
		t.Fatalf("priority = %v, want %v", frame.Header.Priority, core.PriorityRPC)
	}
	if frame.Header.ServiceID != 7 {
		t.Fatalf("service id = %d, want 7", frame.Header.ServiceID)
	}
	if got := string(frame.Body.Bytes()); got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}

func TestConnWriteLoopSetsWriteDeadline(t *testing.T) {
	raw := newDeadlineConn()
	c := New(raw, Config{Limits: testLimitsWithWriteTimeout(50 * time.Millisecond)}, nil)

	if err := c.Send(context.Background(), Outbound{
		Priority:  core.PriorityRPC,
		ServiceID: 8,
		Payload:   core.CopyOwnedBuffer([]byte("deadline")),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	go c.writeLoop()
	deadline := waitDeadline(t, raw.writeDeadlineCh)
	if time.Until(deadline) <= 0 {
		t.Fatalf("write deadline = %v, want future deadline", deadline)
	}

	c.shutdown(nil)
	waitClosed(t, c.writeDone)
}

func TestConnDispatchesInboundFrame(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	inboundCh := make(chan Inbound, 1)
	c := New(raw, Config{Limits: testLimits()}, DispatchFunc(func(ctx context.Context, in Inbound) {
		inboundCh <- in
	}))
	c.Start()
	defer c.Close(nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- wire.WriteFrame(peer, wire.Frame{
			Header: wire.Header{
				Kind:      core.FrameKindNotify,
				Priority:  core.PriorityControl,
				ServiceID: 9,
				RequestID: 11,
			},
			Body: core.CopyOwnedBuffer([]byte("notify")),
		}, testLimits().MaxFrameBodyBytes)
	}()

	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	in := waitInbound(t, inboundCh)
	defer in.Payload.Release()

	if in.Conn != c {
		t.Fatalf("in.Conn = %p, want %p", in.Conn, c)
	}
	if in.Kind != core.FrameKindNotify || in.Priority != core.PriorityControl || in.ServiceID != 9 || in.RequestID != 11 {
		t.Fatalf("inbound metadata = %+v", in)
	}
	if got := string(in.Payload.Bytes()); got != "notify" {
		t.Fatalf("payload = %q, want notify", got)
	}
}

func TestConnCloseFailsPendingRPC(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	c.Start()

	callCh := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 3,
			Payload:   core.CopyOwnedBuffer([]byte("request")),
		})
		callCh <- err
	}()

	frameCh := make(chan wire.Frame, 1)
	go func() {
		frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
		if err != nil {
			t.Errorf("ReadFrame() error = %v", err)
			return
		}
		frameCh <- frame
	}()
	frame := waitFrame(t, frameCh)
	frame.Body.Release()

	c.Close(nil)
	if err := waitErr(t, callCh); !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Call() error = %v, want %v", err, core.ErrStopped)
	}
}

func TestConnCallMapsCanceledContextBeforeSend(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	defer raw.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Call(ctx, Outbound{
		Priority:  core.PriorityRPC,
		ServiceID: 4,
		Payload:   core.CopyOwnedBuffer([]byte("request")),
	})
	if !errors.Is(err, core.ErrCanceled) {
		t.Fatalf("Call() error = %v, want %v", err, core.ErrCanceled)
	}
}

func TestConnCallSkipsQueuedRPCWhenContextCanceledBeforeWrite(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	callCh := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 6,
			Payload:   core.CopyOwnedBuffer([]byte("queued")),
		})
		callCh <- err
	}()

	waitPendingLen(t, c, 1)
	cancel()
	if err := waitErr(t, callCh); !errors.Is(err, core.ErrCanceled) {
		t.Fatalf("Call() error = %v, want %v", err, core.ErrCanceled)
	}

	c.Start()
	if err := peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
	if err == nil {
		frame.Body.Release()
		t.Fatalf("ReadFrame() succeeded, want timeout because queued RPC was canceled")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("ReadFrame() error = %v, want timeout", err)
	}
	c.Close(nil)
}

func TestConnCloseBeforeStartDoesNotHang(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	done := make(chan struct{})
	go func() {
		c.Close(nil)
		close(done)
	}()
	waitClosed(t, done)
}

func TestConnRPCResponseDecode(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		raw, peer := net.Pipe()
		defer peer.Close()
		c := New(raw, Config{Limits: testLimits()}, nil)
		c.Start()
		defer c.Close(nil)

		callCh := make(chan struct {
			payload []byte
			err     error
		}, 1)
		go func() {
			payload, err := c.Call(context.Background(), Outbound{
				Priority:  core.PriorityRPC,
				ServiceID: 5,
				Payload:   core.CopyOwnedBuffer([]byte("request")),
			})
			callCh <- struct {
				payload []byte
				err     error
			}{payload: payload, err: err}
		}()

		req := readPeerFrame(t, peer)
		req.Body.Release()
		writePeerFrame(t, peer, wire.Frame{
			Header: wire.Header{
				Kind:      core.FrameKindRPCResponse,
				Priority:  core.PriorityRPC,
				ServiceID: req.Header.ServiceID,
				RequestID: req.Header.RequestID,
			},
			Body: EncodeRPCResponse(wire.ResponseOK, []byte("response")),
		})

		got := waitCall(t, callCh)
		if got.err != nil {
			t.Fatalf("Call() error = %v", got.err)
		}
		if !bytes.Equal(got.payload, []byte("response")) {
			t.Fatalf("payload = %q, want response", got.payload)
		}
	})

	t.Run("remote error", func(t *testing.T) {
		raw, peer := net.Pipe()
		defer peer.Close()
		c := New(raw, Config{Limits: testLimits()}, nil)
		c.Start()
		defer c.Close(nil)

		callCh := make(chan struct {
			payload []byte
			err     error
		}, 1)
		go func() {
			payload, err := c.Call(context.Background(), Outbound{
				Priority:  core.PriorityRPC,
				ServiceID: 5,
				Payload:   core.CopyOwnedBuffer([]byte("request")),
			})
			callCh <- struct {
				payload []byte
				err     error
			}{payload: payload, err: err}
		}()

		req := readPeerFrame(t, peer)
		req.Body.Release()
		writePeerFrame(t, peer, wire.Frame{
			Header: wire.Header{
				Kind:      core.FrameKindRPCResponse,
				Priority:  core.PriorityRPC,
				ServiceID: req.Header.ServiceID,
				RequestID: req.Header.RequestID,
			},
			Body: EncodeRPCResponse(wire.ResponseErr, []byte("boom")),
		})

		got := waitCall(t, callCh)
		var remoteErr core.RemoteError
		if !errors.As(got.err, &remoteErr) {
			t.Fatalf("Call() error = %v, want RemoteError", got.err)
		}
		if remoteErr.Code != "remote_error" || remoteErr.Message != "boom" {
			t.Fatalf("remote error = %+v", remoteErr)
		}
		if got.payload != nil {
			t.Fatalf("payload = %q, want nil", got.payload)
		}
	})
}

func testLimits() core.Limits {
	return core.Limits{
		MaxFrameBodyBytes:     1024,
		MaxQueuedBytesPerConn: 4096,
		MaxQueuedItemsPerConn: 16,
		MaxBatchBytes:         1024,
		MaxBatchFrames:        8,
	}
}

func testLimitsWithWriteTimeout(timeout time.Duration) core.Limits {
	limits := testLimits()
	limits.WriteTimeout = timeout
	return limits
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error")
		return nil
	}
}

func waitFrame(t *testing.T, ch <-chan wire.Frame) wire.Frame {
	t.Helper()
	select {
	case frame := <-ch:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
		return wire.Frame{}
	}
}

func waitInbound(t *testing.T, ch <-chan Inbound) Inbound {
	t.Helper()
	select {
	case in := <-ch:
		return in
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound")
		return Inbound{}
	}
}

func waitCall(t *testing.T, ch <-chan struct {
	payload []byte
	err     error
}) struct {
	payload []byte
	err     error
} {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for call")
		return struct {
			payload []byte
			err     error
		}{}
	}
}

func waitDeadline(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case deadline := <-ch:
		return deadline
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write deadline")
		return time.Time{}
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for close")
	}
}

func waitPendingLen(t *testing.T, c *Conn, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if got := c.pending.Len(); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for pending len %d, got %d", want, c.pending.Len())
		case <-ticker.C:
		}
	}
}

func readPeerFrame(t *testing.T, peer net.Conn) wire.Frame {
	t.Helper()
	frameCh := make(chan wire.Frame, 1)
	go func() {
		frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
		if err != nil {
			t.Errorf("ReadFrame() error = %v", err)
			return
		}
		frameCh <- frame
	}()
	return waitFrame(t, frameCh)
}

func writePeerFrame(t *testing.T, peer net.Conn, frame wire.Frame) {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		defer frame.Body.Release()
		errCh <- wire.WriteFrame(peer, frame, testLimits().MaxFrameBodyBytes)
	}()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
}

type deadlineConn struct {
	writeDeadlineCh chan time.Time
}

func newDeadlineConn() *deadlineConn {
	return &deadlineConn{
		writeDeadlineCh: make(chan time.Time, 1),
	}
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (c *deadlineConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *deadlineConn) Close() error {
	return nil
}

func (c *deadlineConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *deadlineConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	select {
	case c.writeDeadlineCh <- t:
	default:
	}
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string {
	return string(a)
}

func (a fakeAddr) String() string {
	return string(a)
}
