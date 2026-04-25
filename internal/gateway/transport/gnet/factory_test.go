package gnet

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

func TestFactoryBuildAssignsAllSpecsToOneSharedGroup(t *testing.T) {
	factory := NewFactory()

	listeners, err := factory.Build([]transport.ListenerSpec{
		{Options: transport.ListenerOptions{Name: "tcp-a", Network: "tcp", Address: "127.0.0.1:5100"}, Handler: noopHandler{}},
		{Options: transport.ListenerOptions{Name: "ws-b", Network: "websocket", Address: "127.0.0.1:5200", Path: "/ws"}, Handler: noopHandler{}},
		{Options: transport.ListenerOptions{Name: "tcp-c", Network: "tcp", Address: "127.0.0.1:5300"}, Handler: noopHandler{}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := len(listeners), 3; got != want {
		t.Fatalf("Build returned %d listeners, want %d", got, want)
	}

	first := requireListenerHandle(t, listeners[0])
	second := requireListenerHandle(t, listeners[1])
	third := requireListenerHandle(t, listeners[2])

	if first.group == nil {
		t.Fatal("listener[0] group is nil")
	}
	if second.group != first.group {
		t.Fatal("listener[1] group does not share listener[0] group")
	}
	if third.group != first.group {
		t.Fatal("listener[2] group does not share listener[0] group")
	}
}

func TestFactoryBuildPreservesLogicalListenerOrdering(t *testing.T) {
	factory := NewFactory()

	listeners, err := factory.Build([]transport.ListenerSpec{
		{Options: transport.ListenerOptions{Name: "first", Network: "tcp", Address: "127.0.0.1:5100"}, Handler: noopHandler{}},
		{Options: transport.ListenerOptions{Name: "second", Network: "websocket", Address: "127.0.0.1:5200", Path: "/ws"}, Handler: noopHandler{}},
		{Options: transport.ListenerOptions{Name: "third", Network: "tcp", Address: "127.0.0.1:5300"}, Handler: noopHandler{}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := []string{
		requireListenerHandle(t, listeners[0]).opts.Name,
		requireListenerHandle(t, listeners[1]).opts.Name,
		requireListenerHandle(t, listeners[2]).opts.Name,
	}
	want := []string{"first", "second", "third"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listener[%d] name = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFactoryBuildReturnsLogicalListenersWithOwnAddresses(t *testing.T) {
	factory := NewFactory()

	listeners, err := factory.Build([]transport.ListenerSpec{
		{Options: transport.ListenerOptions{Name: "tcp-a", Network: "tcp", Address: "127.0.0.1:5100"}, Handler: noopHandler{}},
		{Options: transport.ListenerOptions{Name: "ws-b", Network: "websocket", Address: "127.0.0.1:5200", Path: "/ws"}, Handler: noopHandler{}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	first := requireListenerHandle(t, listeners[0])
	second := requireListenerHandle(t, listeners[1])

	if got, want := first.Addr(), "127.0.0.1:5100"; got != want {
		t.Fatalf("listener[0] Addr() = %q, want %q", got, want)
	}
	if got, want := second.Addr(), "127.0.0.1:5200"; got != want {
		t.Fatalf("listener[1] Addr() = %q, want %q", got, want)
	}
	if first.group == nil || second.group == nil {
		t.Fatal("expected shared group to be initialized")
	}
	if first.group != second.group {
		t.Fatal("logical listeners do not share the same group")
	}
}

func requireListenerHandle(t *testing.T, listener transport.Listener) *listenerHandle {
	t.Helper()

	handle, ok := listener.(*listenerHandle)
	if !ok {
		t.Fatalf("listener = %T, want *listenerHandle", listener)
	}
	return handle
}

type noopHandler struct{}

func (noopHandler) OnOpen(conn transport.Conn) error {
	return nil
}

func (noopHandler) OnData(conn transport.Conn, data []byte) error {
	return nil
}

func (noopHandler) OnClose(conn transport.Conn, err error) {}
