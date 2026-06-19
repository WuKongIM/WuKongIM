package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestManagementSlotRaftOperatorUsesLocalCompact(t *testing.T) {
	node := &fakeManagementSlotRaftNode{
		nodeID: 1,
		compact: clusterv2.SlotRaftCompactionResult{
			NodeID:             1,
			SlotID:             9,
			AppliedIndex:       8,
			Compacted:          true,
			AfterSnapshotIndex: 8,
		},
	}
	operator := NewManagementSlotRaftOperator(node)

	got, err := operator.CompactSlotRaftLog(context.Background(), 1, 9)
	if err != nil {
		t.Fatalf("CompactSlotRaftLog() error = %v", err)
	}

	if got.NodeID != 1 || got.SlotID != 9 || !got.Compacted || got.AfterSnapshotIndex != 8 {
		t.Fatalf("compaction = %#v, want local compact result", got)
	}
	if node.localSlotID != 9 || node.calledServiceID != 0 {
		t.Fatalf("local slot=%d service=%d, want local slot 9 without remote rpc", node.localSlotID, node.calledServiceID)
	}
}

func TestManagementSlotRaftOperatorRoutesRemoteCompact(t *testing.T) {
	service := &fakeRemoteSlotRaftService{
		result: managementusecase.SlotRaftCompactionResult{
			NodeID:             2,
			SlotID:             9,
			AppliedIndex:       8,
			Compacted:          true,
			AfterSnapshotIndex: 8,
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerSlotRaft: service})
	node := &fakeManagementSlotRaftNode{
		nodeID:  1,
		handler: adapter.HandleManagerSlotRaftRPC,
	}
	operator := NewManagementSlotRaftOperator(node)

	got, err := operator.CompactSlotRaftLog(context.Background(), 2, 9)
	if err != nil {
		t.Fatalf("CompactSlotRaftLog() error = %v", err)
	}

	if got.NodeID != 2 || got.SlotID != 9 || !got.Compacted || got.AfterSnapshotIndex != 8 {
		t.Fatalf("compaction = %#v, want remote compact result", got)
	}
	if service.nodeID != 2 || service.slotID != 9 {
		t.Fatalf("remote compact target = node:%d slot:%d, want 2/9", service.nodeID, service.slotID)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerSlotRaftRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerSlotRaftRPCServiceID)
	}
}

type fakeManagementSlotRaftNode struct {
	nodeID          uint64
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
	localSlotID     uint32
	compact         clusterv2.SlotRaftCompactionResult
}

func (f *fakeManagementSlotRaftNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementSlotRaftNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

func (f *fakeManagementSlotRaftNode) LocalCompactSlotRaftLog(_ context.Context, slotID uint32) (clusterv2.SlotRaftCompactionResult, error) {
	f.localSlotID = slotID
	return f.compact, nil
}

type fakeRemoteSlotRaftService struct {
	nodeID uint64
	slotID uint32
	result managementusecase.SlotRaftCompactionResult
}

func (f *fakeRemoteSlotRaftService) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionResult, error) {
	f.nodeID = nodeID
	f.slotID = slotID
	return f.result, nil
}
