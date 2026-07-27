package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
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

type managerGoroutineReaderStub struct {
	snapshot managementusecase.GoroutineSnapshot
}

func (r managerGoroutineReaderStub) Snapshot() managementusecase.GoroutineSnapshot {
	return r.snapshot
}

func TestManagerGoroutineRPCPublishesSnapshot(t *testing.T) {
	adapter := New(Options{ManagerGoroutines: managerGoroutineReaderStub{
		snapshot: managementusecase.GoroutineSnapshot{BootID: "boot-1", ProcessTotal: 8},
	}})
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
