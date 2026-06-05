package conn

import (
	"bytes"
	"context"
	"errors"
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
