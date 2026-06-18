package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerControllerRaftRPCReadsStatus(t *testing.T) {
	service := &fakeManagerControllerRaftService{
		status: managementusecase.ControllerRaftStatus{
			NodeID:     2,
			Role:       "leader",
			LeaderID:   2,
			Term:       3,
			FirstIndex: 1,
			LastIndex:  8,
		},
	}
	adapter := New(Options{ManagerControllerRaft: service})
	body, err := encodeManagerControllerRaftRequest(managerControllerRaftRPCRequest{Op: managerControllerRaftOpStatus, NodeID: 2})
	if err != nil {
		t.Fatalf("encodeManagerControllerRaftRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerControllerRaftRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerControllerRaftRPC() error = %v", err)
	}
	resp, err := decodeManagerControllerRaftResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerControllerRaftResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || resp.RaftStatus.NodeID != 2 || resp.RaftStatus.Role != "leader" {
		t.Fatalf("response = %#v, want ok controller raft status", resp)
	}
	if service.statusNodeID != 2 {
		t.Fatalf("status node id = %d, want 2", service.statusNodeID)
	}
}

func TestManagerControllerRaftRPCClientCompactsNode(t *testing.T) {
	service := &fakeManagerControllerRaftService{
		compact: managementusecase.ControllerRaftCompactionResult{
			NodeID:             2,
			AppliedIndex:       8,
			Compacted:          true,
			AfterSnapshotIndex: 8,
		},
	}
	adapter := New(Options{ManagerControllerRaft: service})
	node := &fakeManagerLogRPCNode{handler: adapter.HandleManagerControllerRaftRPC}
	client := NewClient(node)

	got, err := client.CompactManagerControllerRaftLog(context.Background(), 2)
	if err != nil {
		t.Fatalf("CompactManagerControllerRaftLog() error = %v", err)
	}

	if !got.Compacted || got.AfterSnapshotIndex != 8 {
		t.Fatalf("compaction result = %#v, want compacted snapshot 8", got)
	}
	if service.compactNodeID != 2 {
		t.Fatalf("compact node id = %d, want 2", service.compactNodeID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerControllerRaftRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerControllerRaftRPCServiceID)
	}
}

type fakeManagerControllerRaftService struct {
	statusNodeID  uint64
	compactNodeID uint64
	status        managementusecase.ControllerRaftStatus
	compact       managementusecase.ControllerRaftCompactionResult
}

func (f *fakeManagerControllerRaftService) ControllerRaftStatus(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error) {
	f.statusNodeID = nodeID
	return f.status, nil
}

func (f *fakeManagerControllerRaftService) CompactControllerRaftLog(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error) {
	f.compactNodeID = nodeID
	return f.compact, nil
}
