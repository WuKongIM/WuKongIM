package transport

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerStartStop(t *testing.T) {
	s := NewServer()
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if s.Listener() == nil {
		t.Fatal("Listener() = nil")
	}
	s.Stop()
}

func TestServerHandleMessage(t *testing.T) {
	s := NewServer()
	var received atomic.Int32
	s.Handle(1, func(body []byte) {
		if string(body) == "ping" {
			received.Add(1)
		}
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var bufs net.Buffers
	writeFrame(&bufs, 1, []byte("ping"))
	if _, err := bufs.WriteTo(conn); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	requireEventually(t, func() bool { return received.Load() == 1 })
}

func TestServerObserverTracksReceivedBytes(t *testing.T) {
	var (
		gotType  uint8
		gotBytes int
		calls    atomic.Int32
	)
	s := NewServerWithConfig(ServerConfig{
		ConnConfig: ConnConfig{
			Observer: ObserverHooks{
				OnReceive: func(msgType uint8, bytes int) {
					gotType = msgType
					gotBytes = bytes
					calls.Add(1)
				},
			},
		},
	})
	s.Handle(1, func(body []byte) {})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var bufs net.Buffers
	writeFrame(&bufs, 1, []byte("ping"))
	if _, err := bufs.WriteTo(conn); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	requireEventually(t, func() bool { return calls.Load() == 1 })
	if gotType != 1 {
		t.Fatalf("observer msgType = %d, want 1", gotType)
	}
	if gotBytes != len("ping") {
		t.Fatalf("observer bytes = %d, want %d", gotBytes, len("ping"))
	}
}

func TestServerHandleRPC(t *testing.T) {
	s := NewServer()
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		return append([]byte("echo:"), body...), nil
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var req net.Buffers
	writeFrame(&req, MsgTypeRPCRequest, encodeRPCRequest(42, []byte("hello")))
	if _, err := req.WriteTo(conn); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	msgType, respBody, release, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	defer release()
	if msgType != MsgTypeRPCResponse {
		t.Fatalf("msgType = %d, want %d", msgType, MsgTypeRPCResponse)
	}
	reqID, errCode, data, err := decodeRPCResponse(respBody)
	if err != nil {
		t.Fatalf("decodeRPCResponse() error = %v", err)
	}
	if reqID != 42 || errCode != 0 || string(data) != "echo:hello" {
		t.Fatalf("unexpected response: reqID=%d errCode=%d data=%q", reqID, errCode, data)
	}
}

func TestServerHandleRPCMux(t *testing.T) {
	s := NewServer()
	mux := NewRPCMux()
	mux.Handle(3, func(ctx context.Context, body []byte) ([]byte, error) {
		return []byte("mux:" + string(body)), nil
	})
	s.HandleRPCMux(mux)
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var req net.Buffers
	writeFrame(&req, MsgTypeRPCRequest, encodeRPCRequest(7, encodeRPCServicePayload(3, []byte("ok"))))
	if _, err := req.WriteTo(conn); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	msgType, respBody, release, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	defer release()
	if msgType != MsgTypeRPCResponse {
		t.Fatalf("msgType = %d, want %d", msgType, MsgTypeRPCResponse)
	}
	_, errCode, data, err := decodeRPCResponse(respBody)
	if err != nil {
		t.Fatalf("decodeRPCResponse() error = %v", err)
	}
	if errCode != 0 || string(data) != "mux:ok" {
		t.Fatalf("unexpected response errCode=%d data=%q", errCode, data)
	}
}

func TestServerHandleRPCCancelsHandlerWhenConnectionCloses(t *testing.T) {
	s := NewServer()
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	var req net.Buffers
	writeFrame(&req, MsgTypeRPCRequest, encodeRPCRequest(51, []byte("block")))
	if _, err := req.WriteTo(conn); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc handler to start")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("expected rpc handler context to cancel after client close")
	}
}

func TestServerStopClosesAcceptedConnections(t *testing.T) {
	s := NewServer()
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	s.Stop()

	_, _, _, err = readFrame(conn)
	if err == nil {
		t.Fatal("readFrame() error = nil after Stop")
	}
	_ = conn.Close()
}
