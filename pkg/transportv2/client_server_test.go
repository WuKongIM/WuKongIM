package transportv2

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

func TestClientSendWritesFrame(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: listener.Addr().String()},
		PoolSize:  1,
		Limits:    DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()

	payload := []byte("hello")
	if err := client.Send(context.Background(), 2, 42, PriorityControl, 7, payload); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	payload[0] = 'x'

	var peer net.Conn
	select {
	case peer = <-accepted:
	case err := <-acceptErr:
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client dial")
	}
	defer peer.Close()

	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	frame, err := wire.ReadFrame(peer, DefaultLimits().MaxFrameBodyBytes)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer frame.Body.Release()

	if frame.Header.Kind != FrameKindData {
		t.Fatalf("Kind = %v, want %v", frame.Header.Kind, FrameKindData)
	}
	if frame.Header.Priority != PriorityControl {
		t.Fatalf("Priority = %v, want %v", frame.Header.Priority, PriorityControl)
	}
	if frame.Header.ServiceID != 7 {
		t.Fatalf("ServiceID = %d, want 7", frame.Header.ServiceID)
	}
	if got := string(frame.Body.Bytes()); got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}

func TestServerHandlesNotify(t *testing.T) {
	server, err := NewServer(ServerConfig{
		NodeID: 2,
		Limits: DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()

	received := make(chan []byte, 1)
	if err := server.Handle(7, func(ctx context.Context, payload []byte) ([]byte, error) {
		received <- append([]byte(nil), payload...)
		return nil, nil
	}, ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 1024}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatalf("ListenAndServe() error = %v", err)
	}
	if server.Addr() == "" {
		t.Fatal("Addr() is empty after ListenAndServe")
	}

	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: server.Addr()},
		PoolSize:  1,
		Limits:    DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()

	payload := []byte("notify")
	if err := client.Notify(context.Background(), 2, 42, PriorityControl, 7, payload); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	payload[0] = 'x'

	select {
	case got := <-received:
		if string(got) != "notify" {
			t.Fatalf("handler payload = %q, want notify", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler payload")
	}
}

func TestServerStopClearsConnectionStats(t *testing.T) {
	server, err := NewServer(ServerConfig{
		NodeID: 2,
		Limits: DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatalf("ListenAndServe() error = %v", err)
	}
	addr := server.Addr()

	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer raw.Close()
	waitServerConnections(t, server, 1)

	server.Stop()
	if got := server.Stats().Connections; got != 0 {
		t.Fatalf("Stats().Connections = %d, want 0 after Stop", got)
	}
	if conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("DialTimeout() after Stop succeeded, want listener closed")
	}
}

func TestServerHandleDuplicateServiceIDReturnsInvalidConfig(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2, Limits: DefaultLimits()})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()

	handler := func(context.Context, []byte) ([]byte, error) { return nil, nil }
	opts := ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}
	if err := server.Handle(7, handler, opts); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}
	err = server.Handle(7, handler, opts)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Handle(duplicate) error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestServerHandleNilHandlerReturnsInvalidConfig(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2, Limits: DefaultLimits()})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()

	err = server.Handle(7, nil, ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Handle(nil) error = %v, want %v", err, ErrInvalidConfig)
	}
}

func waitServerConnections(t *testing.T, server *Server, want int) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if got := server.Stats().Connections; got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d server connections, got %d", want, server.Stats().Connections)
		case <-ticker.C:
		}
	}
}
