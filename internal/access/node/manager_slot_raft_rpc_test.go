package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
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

func TestManagerSlotRaftRPCClientReadsNodeSlotStatus(t *testing.T) {
	service := &fakeManagerSlotRaftService{
		status: managementusecase.SlotNodeLogStatus{
			NodeID:        2,
			LeaderID:      1,
			Role:          "follower",
			CurrentVoters: []uint64{1, 2, 3},
			CommitIndex:   93,
			AppliedIndex:  91,
		},
	}
	adapter := New(Options{ManagerSlotRaft: service})
	node := &fakeManagerLogRPCNode{handler: adapter.HandleManagerSlotRaftRPC}
	client := NewClient(node)

	got, err := client.GetManagerSlotRaftStatus(context.Background(), 2, 9)
	if err != nil {
		t.Fatalf("GetManagerSlotRaftStatus() error = %v", err)
	}

	if got.NodeID != 2 || got.LeaderID != 1 || got.Role != "follower" || !sameNodeRPCUint64s(got.CurrentVoters, []uint64{1, 2, 3}) || got.CommitIndex != 93 || got.AppliedIndex != 91 {
		t.Fatalf("status = %#v, want follower status with local watermarks", got)
	}
	if service.nodeID != 2 || service.slotID != 9 {
		t.Fatalf("status target = node:%d slot:%d, want 2/9", service.nodeID, service.slotID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerSlotRaftRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerSlotRaftRPCServiceID)
	}
}

type fakeManagerSlotRaftService struct {
	nodeID uint64
	slotID uint32
	status managementusecase.SlotNodeLogStatus
	result managementusecase.SlotRaftCompactionResult
}

func (f *fakeManagerSlotRaftService) SlotRaftStatus(_ context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotNodeLogStatus, error) {
	f.nodeID = nodeID
	f.slotID = slotID
	return f.status, nil
}

func (f *fakeManagerSlotRaftService) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionResult, error) {
	f.nodeID = nodeID
	f.slotID = slotID
	return f.result, nil
}

func sameNodeRPCUint64s(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
