package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerSlotRaftRPCClientCompactsNodeSlot(t *testing.T) {
	service := &fakeManagerSlotRaftService{
		result: managementusecase.SlotRaftCompactionResult{
			NodeID:             2,
			SlotID:             9,
			AppliedIndex:       8,
			Compacted:          true,
			AfterSnapshotIndex: 8,
		},
	}
	adapter := New(Options{ManagerSlotRaft: service})
	node := &fakeManagerLogRPCNode{handler: adapter.HandleManagerSlotRaftRPC}
	client := NewClient(node)

	got, err := client.CompactManagerSlotRaftLog(context.Background(), 2, 9)
	if err != nil {
		t.Fatalf("CompactManagerSlotRaftLog() error = %v", err)
	}

	if got.NodeID != 2 || got.SlotID != 9 || !got.Compacted || got.AfterSnapshotIndex != 8 {
		t.Fatalf("compaction result = %#v, want compacted node 2 slot 9 snapshot 8", got)
	}
	if service.nodeID != 2 || service.slotID != 9 {
		t.Fatalf("compact target = node:%d slot:%d, want 2/9", service.nodeID, service.slotID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerSlotRaftRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerSlotRaftRPCServiceID)
	}
}

type fakeManagerSlotRaftService struct {
	nodeID uint64
	slotID uint32
	result managementusecase.SlotRaftCompactionResult
}

func (f *fakeManagerSlotRaftService) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionResult, error) {
	f.nodeID = nodeID
	f.slotID = slotID
	return f.result, nil
}
