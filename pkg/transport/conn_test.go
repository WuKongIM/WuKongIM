package transport

import (
	"bytes"
	"context"
	"errors"
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
