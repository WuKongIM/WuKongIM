package stdnet_test

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport/stdnet"
	"github.com/gorilla/websocket"
)

func TestTCPListenerDeliversOpenDataAndClose(t *testing.T) {
	handler := newConnRecordingHandler()
	listener, err := stdnet.NewTCPListener(transport.ListenerOptions{
		Name:    "tcp-wkproto",
		Address: "127.0.0.1:0",
	}, handler)
	if err != nil {
		t.Fatalf("NewTCPListener: %v", err)
	}
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", listener.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool {
		return handler.OpenCount() == 1 && handler.DataCount() == 1 && handler.CloseCount() == 1
	})
}

func TestFactoryBuildReturnsOneListenerPerSpec(t *testing.T) {
	handler := newConnRecordingHandler()
	factory := stdnet.NewFactory()

	listeners, err := factory.Build([]transport.ListenerSpec{
		{
			Options: transport.ListenerOptions{
				Name:    "tcp-wkproto",
				Network: "tcp",
				Address: "127.0.0.1:0",
			},
			Handler: handler,
		},
		{
			Options: transport.ListenerOptions{
				Name:    "ws-jsonrpc",
				Network: "websocket",
				Address: "127.0.0.1:0",
			},
			Handler: handler,
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := len(listeners), 2; got != want {
		t.Fatalf("Build returned %d listeners, want %d", got, want)
	}

	tcpListener, ok := listeners[0].(*stdnet.TCPListener)
	if !ok {
		t.Fatalf("listener[0] = %T, want *stdnet.TCPListener", listeners[0])
	}
	wsListener, ok := listeners[1].(*stdnet.WSListener)
	if !ok {
		t.Fatalf("listener[1] = %T, want *stdnet.WSListener", listeners[1])
	}

	defer func() { _ = tcpListener.Stop() }()
	defer func() { _ = wsListener.Stop() }()

	if err := tcpListener.Start(); err != nil {
		t.Fatalf("tcp Start: %v", err)
	}
	if err := wsListener.Start(); err != nil {
		t.Fatalf("ws Start: %v", err)
	}

	conn, err := net.Dial("tcp", tcpListener.Addr())
	if err != nil {
		t.Fatalf("tcp Dial: %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("tcp Write: %v", err)
	}
	_ = conn.Close()

	url := "ws://" + strings.TrimPrefix(wsListener.Addr(), "http://")
	wsConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("ws Dial: %v", err)
	}
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","method":"ping","id":"1"}`)); err != nil {
		t.Fatalf("ws WriteMessage: %v", err)
	}
	_ = wsConn.Close()

	waitUntil(t, time.Second, func() bool {
		return handler.OpenCount() == 2 && handler.DataCount() == 2
	})
}

func TestTCPListenerStopClosesOpenConnections(t *testing.T) {
	handler := newConnRecordingHandler()
	listener, err := stdnet.NewTCPListener(transport.ListenerOptions{
		Name:    "tcp-wkproto",
		Address: "127.0.0.1:0",
	}, handler)
	if err != nil {
		t.Fatalf("NewTCPListener: %v", err)
	}

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", listener.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	waitUntil(t, time.Second, func() bool { return handler.OpenCount() == 1 })

	stopped := make(chan error, 1)
	go func() {
		stopped <- listener.Stop()
	}()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not return")
	}

	waitUntil(t, time.Second, func() bool { return handler.CloseCount() == 1 })

	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected client read to fail after listener stop")
	}
}

type connRecordingHandler struct {
	mu     sync.Mutex
	opens  int
	data   [][]byte
	closes int
}

func newConnRecordingHandler() *connRecordingHandler {
	return &connRecordingHandler{}
}

func (h *connRecordingHandler) OnOpen(conn transport.Conn) error {
	h.mu.Lock()
	h.opens++
	h.mu.Unlock()
	return nil
}

func (h *connRecordingHandler) OnData(conn transport.Conn, data []byte) error {
	h.mu.Lock()
	h.data = append(h.data, append([]byte(nil), data...))
	h.mu.Unlock()
	return nil
}

func (h *connRecordingHandler) OnClose(conn transport.Conn, err error) {
	h.mu.Lock()
	h.closes++
	h.mu.Unlock()
}

func (h *connRecordingHandler) OpenCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opens
}

func (h *connRecordingHandler) DataCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.data)
}

func (h *connRecordingHandler) CloseCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closes
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

var _ transport.ConnHandler = (*connRecordingHandler)(nil)
