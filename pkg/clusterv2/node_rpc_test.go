package clusterv2

import (
	"context"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

func TestNodeRegisterRPCStoresHandlerBeforeDefaultTransportExists(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := clusternet.HandlerFunc(func(context.Context, []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	node.RegisterRPC(clusternet.RPCPresenceAuthority, handler)
	if got := len(node.pendingRPCHandlers); got != 1 {
		t.Fatalf("pendingRPCHandlers len = %d, want 1", got)
	}
}

func TestNodeCallRPCRequiresStartedTransport(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node.CallRPC(context.Background(), 2, clusternet.RPCPresenceAuthority, []byte("ping"))
	if err == nil {
		t.Fatal("CallRPC() error = nil, want not started")
	}
}

func TestNodeRegisterRPCAfterDefaultTransportIsIdempotent(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.ensureDefaultTransport(); err != nil {
		t.Fatalf("ensureDefaultTransport() error = %v", err)
	}
	handler := clusternet.HandlerFunc(func(context.Context, []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	node.RegisterRPC(clusternet.RPCPresenceAuthority, handler)
	node.RegisterRPC(clusternet.RPCPresenceAuthority, handler)
	if got := len(node.registeredRPCHandlers); got != 1 {
		t.Fatalf("registeredRPCHandlers len = %d, want 1", got)
	}
}

func TestNodeDefaultTransportUsesClusterSizedClientPool(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.ensureDefaultTransport(); err != nil {
		t.Fatalf("ensureDefaultTransport() error = %v", err)
	}
	if got := node.transportClient.PoolSize(); got != 1000 {
		t.Fatalf("default transport pool size = %d, want 1000", got)
	}
}
