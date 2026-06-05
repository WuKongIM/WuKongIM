package transportv2

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

func TestClientCallRoundTrip(t *testing.T) {
	server, client := newClientServerPair(t, func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("ok:"), payload...), nil
	})
	defer server.Stop()
	defer client.Stop()

	got, err := client.Call(context.Background(), 2, 42, PriorityRPC, 7, []byte("hello"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(got) != "ok:hello" {
		t.Fatalf("Call() payload = %q, want ok:hello", got)
	}
}

func TestClientStopFailsPendingCall(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server, client := newClientServerPair(t, func(ctx context.Context, payload []byte) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return []byte("late"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer server.Stop()
	defer close(release)

	errCh := make(chan error, 1)
	go func() {
		_, err := client.Call(context.Background(), 2, 42, PriorityRPC, 7, []byte("block"))
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	client.Stop()
	if err := waitTransportErr(t, errCh); !errors.Is(err, ErrStopped) {
		t.Fatalf("Call() error = %v, want %v", err, ErrStopped)
	}
}

func TestClientCallMissingServiceReturnsRemoteError(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2, Limits: DefaultLimits()})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatalf("ListenAndServe() error = %v", err)
	}

	client := newTestClient(t, server.Addr())
	defer client.Stop()

	_, err = client.Call(context.Background(), 2, 42, PriorityRPC, 99, []byte("missing"))
	var remoteErr RemoteError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("Call() error = %v, want RemoteError", err)
	}
	if remoteErr.Message == "" {
		t.Fatal("RemoteError.Message is empty")
	}
}

func TestClientCallHandlerErrorReturnsRemoteError(t *testing.T) {
	server, client := newClientServerPair(t, func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, errors.New("handler boom")
	})
	defer server.Stop()
	defer client.Stop()

	_, err := client.Call(context.Background(), 2, 42, PriorityRPC, 7, []byte("hello"))
	var remoteErr RemoteError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("Call() error = %v, want RemoteError", err)
	}
	if remoteErr.Message != "handler boom" {
		t.Fatalf("RemoteError.Message = %q, want handler boom", remoteErr.Message)
	}
}

func TestClientCallCopiesPayload(t *testing.T) {
	writeStarted := make(chan struct{})
	allowWrite := make(chan struct{})
	peerCh := make(chan net.Conn, 1)
	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: "pipe"},
		PoolSize:  1,
		Limits:    DefaultLimits(),
		Dialer: func(network, addr string, timeout time.Duration) (net.Conn, error) {
			clientSide, serverSide := net.Pipe()
			peerCh <- serverSide
			return &blockingWriteConn{Conn: clientSide, started: writeStarted, release: allowWrite}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()

	payload := []byte("hello")
	callCh := make(chan struct {
		payload []byte
		err     error
	}, 1)
	go func() {
		resp, err := client.Call(context.Background(), 2, 42, PriorityRPC, 7, payload)
		callCh <- struct {
			payload []byte
			err     error
		}{payload: resp, err: err}
	}()

	var peer net.Conn
	select {
	case peer = <-peerCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dialed peer")
	}
	defer peer.Close()

	select {
	case <-writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client write")
	}
	payload[0] = 'x'
	close(allowWrite)

	frame := readTransportFrame(t, peer)
	defer frame.Body.Release()
	if got := string(frame.Body.Bytes()); got != "hello" {
		t.Fatalf("RPC request body = %q, want hello", got)
	}

	if err := wire.WriteFrame(peer, wire.Frame{
		Header: wire.Header{
			Kind:      FrameKindRPCResponse,
			Priority:  PriorityRPC,
			ServiceID: frame.Header.ServiceID,
			RequestID: frame.Header.RequestID,
		},
		Body: CopyOwnedBuffer([]byte{wire.ResponseOK}),
	}, DefaultLimits().MaxFrameBodyBytes); err != nil {
		t.Fatalf("WriteFrame(response) error = %v", err)
	}
	got := waitTransportCall(t, callCh)
	if got.err != nil {
		t.Fatalf("Call() error = %v", got.err)
	}
}

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

func newClientServerPair(t *testing.T, handler Handler) (*Server, *Client) {
	t.Helper()

	server, err := NewServer(ServerConfig{
		NodeID: 2,
		Limits: DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if err := server.Handle(7, handler, ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 1024}); err != nil {
		server.Stop()
		t.Fatalf("Handle() error = %v", err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		server.Stop()
		t.Fatalf("ListenAndServe() error = %v", err)
	}

	client := newTestClient(t, server.Addr())
	return server, client
}

func newTestClient(t *testing.T, addr string) *Client {
	t.Helper()

	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: addr},
		PoolSize:  1,
		Limits:    DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func waitTransportErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transport error")
		return nil
	}
}

func waitTransportCall(t *testing.T, ch <-chan struct {
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
		t.Fatal("timed out waiting for transport call")
		return struct {
			payload []byte
			err     error
		}{}
	}
}

func readTransportFrame(t *testing.T, peer net.Conn) wire.Frame {
	t.Helper()
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	frame, err := wire.ReadFrame(peer, DefaultLimits().MaxFrameBodyBytes)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	return frame
}

type blockingWriteConn struct {
	net.Conn
	once    sync.Once
	started chan<- struct{}
	release <-chan struct{}
}

func (c *blockingWriteConn) Write(p []byte) (int, error) {
	c.once.Do(func() {
		close(c.started)
		<-c.release
	})
	return c.Conn.Write(p)
}
