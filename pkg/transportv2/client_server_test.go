package transportv2

import (
	"context"
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
