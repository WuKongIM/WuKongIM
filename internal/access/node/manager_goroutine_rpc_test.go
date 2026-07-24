package node

import (
	"context"
	"testing"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

type managerGoroutineRPCNode struct {
	adapter   *Adapter
	nodeID    uint64
	serviceID uint8
}

func (n *managerGoroutineRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	n.nodeID = nodeID
	n.serviceID = serviceID
	return n.adapter.HandleManagerGoroutineRPC(ctx, payload)
}

func TestManagerGoroutineRPCPublishesRegistrySnapshot(t *testing.T) {
	registry := goruntimeregistry.New()
	adapter := New(Options{ManagerGoroutines: registry})
	node := &managerGoroutineRPCNode{adapter: adapter}

	snapshot, err := NewClient(node).ManagerGoroutineSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("ManagerGoroutineSnapshot() error = %v", err)
	}
	if node.nodeID != 2 || node.serviceID != ManagerGoroutineRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerGoroutineRPCServiceID)
	}
	if snapshot.BootID == "" || snapshot.ProcessTotal == 0 {
		t.Fatalf("snapshot = %+v, want process identity and total", snapshot)
	}
}
