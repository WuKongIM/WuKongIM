package clusternet

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestDiscoveryUpdatesAtomically(t *testing.T) {
	d := NewDiscovery()
	d.Update([]NodeAddress{{NodeID: 1, Addr: "a"}, {NodeID: 2, Addr: "b"}})
	addr, ok := d.Addr(1)
	if !ok || addr != "a" {
		t.Fatalf("Addr(1) = %q,%v want a,true", addr, ok)
	}
	snap := d.Snapshot()
	d.Update([]NodeAddress{{NodeID: 2, Addr: "bb"}})
	if snap[1] != "a" {
		t.Fatalf("old snapshot mutated: %#v", snap)
	}
	addr, ok = d.Addr(1)
	if ok || addr != "" {
		t.Fatalf("Addr(1) after update = %q,%v want empty,false", addr, ok)
	}
}

func TestLocalNetworkDispatchesRPC(t *testing.T) {
	network := NewLocalNetwork()
	network.Register(2, RPCSlotForwardPropose, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("echo:"), payload...), nil
	}))
	got, err := network.Call(context.Background(), 2, RPCSlotForwardPropose, []byte("hello"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(got) != "echo:hello" {
		t.Fatalf("Call() = %q, want echo:hello", got)
	}
}

func TestLocalNetworkReturnsTypedErrors(t *testing.T) {
	network := NewLocalNetwork()
	if _, err := network.Call(context.Background(), 9, RPCSlotForwardPropose, nil); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node error = %v, want ErrNodeNotFound", err)
	}
	network.Register(1, RPCSlotForwardPropose, HandlerFunc(func(context.Context, []byte) ([]byte, error) { return nil, nil }))
	if _, err := network.Call(context.Background(), 1, RPCChannelPull, nil); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("missing service error = %v, want ErrServiceNotFound", err)
	}
}

func TestCodecHeaderRoundTrip(t *testing.T) {
	payload := []byte("payload")
	frame := PutHeader(nil, 1, 7)
	frame = append(frame, payload...)
	got, err := CheckHeader(frame, 1, 7)
	if err != nil {
		t.Fatalf("CheckHeader() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestCodecRejectsInvalidHeader(t *testing.T) {
	if _, err := CheckHeader([]byte{1}, 1, 7); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("short frame error = %v, want ErrInvalidFrame", err)
	}
	if _, err := CheckHeader([]byte{2, 7}, 1, 7); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("version error = %v, want ErrInvalidFrame", err)
	}
	if _, err := CheckHeader([]byte{1, 8}, 1, 7); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("kind error = %v, want ErrInvalidFrame", err)
	}
}

func TestTransportLoopbackRPC(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	server.Register(RPCSlotForwardPropose, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("resp:"), payload...), nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	got, err := client.Call(context.Background(), 2, RPCSlotForwardPropose, []byte("ping"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(got) != "resp:ping" {
		t.Fatalf("Call() = %q, want resp:ping", got)
	}
}

func TestTransportLoopbackSendDoesNotWaitForResponse(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	received := make(chan []byte, 1)
	release := make(chan struct{})
	server.Register(RPCControlRaft, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		received <- append([]byte(nil), payload...)
		<-release
		return []byte("ignored"), nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()
	defer close(release)

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := client.Send(ctx, 2, RPCControlRaft, []byte("raft")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case got := <-received:
		if string(got) != "raft" {
			t.Fatalf("payload = %q, want raft", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for one-way send")
	}
}
