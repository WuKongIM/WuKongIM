package cluster

import (
	"context"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestManagementControllerRaftOperatorUsesLocalStatus(t *testing.T) {
	node := &fakeManagementControllerRaftNode{
		nodeID: 1,
		status: clusterv2.ControllerRaftStatus{
			NodeID:        1,
			Role:          "leader",
			LeaderID:      1,
			Term:          3,
			FirstIndex:    4,
			LastIndex:     12,
			CommitIndex:   8,
			AppliedIndex:  7,
			SnapshotIndex: 3,
			SnapshotTerm:  2,
		},
	}
	node.status.Compaction.Enabled = true
	node.status.Compaction.TriggerEntries = 1000
	node.status.Compaction.CheckInterval = 15 * time.Second
	node.status.Compaction.AfterSnapshotIndex = 3
	node.status.Compaction.LastAttemptAt = time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	node.status.Compaction.LastSuccessAt = time.Date(2026, 5, 7, 10, 0, 1, 0, time.UTC)
	node.status.Compaction.LastError = "previous failure"
	node.status.Compaction.LastErrorAt = time.Date(2026, 5, 7, 10, 0, 2, 0, time.UTC)
	operator := NewManagementControllerRaftOperator(node)

	got, err := operator.ControllerRaftStatus(context.Background(), 1)

	if err != nil {
		t.Fatalf("ControllerRaftStatus() error = %v", err)
	}
	if got.NodeID != 1 || got.Role != "leader" || got.AppliedIndex != 7 {
		t.Fatalf("status = %#v, want local status", got)
	}
	if got.FirstIndex != 4 || got.LastIndex != 12 || got.SnapshotIndex != 3 || got.SnapshotTerm != 2 {
		t.Fatalf("watermarks = first:%d last:%d snapshot:%d/%d, want 4 12 3/2", got.FirstIndex, got.LastIndex, got.SnapshotIndex, got.SnapshotTerm)
	}
	if !got.Compaction.Enabled || got.Compaction.TriggerEntries != 1000 || got.Compaction.CheckInterval != 15*time.Second || got.Compaction.LastSnapshotIndex != 3 || !got.Compaction.Degraded || got.Compaction.LastErrorAt.IsZero() {
		t.Fatalf("compaction = %#v, want mapped policy and latest error", got.Compaction)
	}
	if !node.localStatusCalled || node.calledServiceID != 0 {
		t.Fatalf("local=%v service=%d, want local without remote rpc", node.localStatusCalled, node.calledServiceID)
	}
}

func TestManagementControllerRaftOperatorRoutesRemoteCompact(t *testing.T) {
	service := &fakeRemoteControllerRaftService{
		compact: managementusecase.ControllerRaftCompactionResult{
			NodeID:             2,
			AppliedIndex:       9,
			Compacted:          true,
			AfterSnapshotIndex: 9,
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerControllerRaft: service})
	node := &fakeManagementControllerRaftNode{
		nodeID:  1,
		handler: adapter.HandleManagerControllerRaftRPC,
	}
	operator := NewManagementControllerRaftOperator(node)

	got, err := operator.CompactControllerRaftLog(context.Background(), 2)

	if err != nil {
		t.Fatalf("CompactControllerRaftLog() error = %v", err)
	}
	if !got.Compacted || got.AfterSnapshotIndex != 9 {
		t.Fatalf("compaction = %#v, want remote compact result", got)
	}
	if service.compactNodeID != 2 {
		t.Fatalf("remote compact node id = %d, want 2", service.compactNodeID)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerControllerRaftRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerControllerRaftRPCServiceID)
	}
}

type fakeManagementControllerRaftNode struct {
	nodeID             uint64
	calledNodeID       uint64
	calledServiceID    uint8
	handler            func(context.Context, []byte) ([]byte, error)
	status             clusterv2.ControllerRaftStatus
	compact            clusterv2.ControllerRaftCompactionResult
	localStatusCalled  bool
	localCompactCalled bool
}

func (f *fakeManagementControllerRaftNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementControllerRaftNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

func (f *fakeManagementControllerRaftNode) LocalControllerRaftStatus(context.Context) (clusterv2.ControllerRaftStatus, error) {
	f.localStatusCalled = true
	return f.status, nil
}

func (f *fakeManagementControllerRaftNode) LocalCompactControllerRaftLog(context.Context) (clusterv2.ControllerRaftCompactionResult, error) {
	f.localCompactCalled = true
	return f.compact, nil
}

type fakeRemoteControllerRaftService struct {
	statusNodeID  uint64
	compactNodeID uint64
	status        managementusecase.ControllerRaftStatus
	compact       managementusecase.ControllerRaftCompactionResult
}

func (f *fakeRemoteControllerRaftService) ControllerRaftStatus(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error) {
	f.statusNodeID = nodeID
	return f.status, nil
}

func (f *fakeRemoteControllerRaftService) CompactControllerRaftLog(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error) {
	f.compactNodeID = nodeID
	return f.compact, nil
}
