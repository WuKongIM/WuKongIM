package cluster

import (
	"context"
	"reflect"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

var _ ManagementLeaderTransferNode = (*cluster.Node)(nil)
var _ ManagementSlotReplicaMoveNode = (*cluster.Node)(nil)

func TestManagementLeaderTransferAdapterUsesControlIntent(t *testing.T) {
	node := &fakeManagementLeaderTransferNode{
		result: control.SlotLeaderTransferResult{Created: true},
	}
	adapter := NewManagementLeaderTransferAdapter(node)

	got, err := adapter.RequestSlotLeaderTransfer(context.Background(), control.SlotLeaderTransferRequest{
		SlotID:     1,
		TargetNode: 2,
	})
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}

	if !got.Created {
		t.Fatalf("RequestSlotLeaderTransfer() = %#v, want created result", got)
	}
	if node.request.TargetNode != 2 {
		t.Fatalf("target node = %d, want 2", node.request.TargetNode)
	}
}

func TestManagementSlotReplicaMoveAdapterUsesControlIntent(t *testing.T) {
	node := &fakeManagementSlotReplicaMoveNode{
		result: control.SlotReplicaMoveResult{Created: true},
	}
	adapter := NewManagementSlotReplicaMoveAdapter(node)

	got, err := adapter.RequestSlotReplicaMove(context.Background(), control.SlotReplicaMoveRequest{
		SlotID:      1,
		SourceNode:  1,
		TargetNode:  4,
		TargetPeers: []uint64{4, 2, 3},
	})
	if err != nil {
		t.Fatalf("RequestSlotReplicaMove() error = %v", err)
	}

	if !got.Created {
		t.Fatalf("RequestSlotReplicaMove() = %#v, want created result", got)
	}
	if node.request.TargetNode != 4 {
		t.Fatalf("target node = %d, want 4", node.request.TargetNode)
	}
}

func TestManagementSlotRuntimeStatusReaderUsesLocalSlotStatus(t *testing.T) {
	node := &fakeManagementSlotRaftNode{
		nodeID: 1,
		status: cluster.SlotRaftStatus{
			NodeID:        1,
			SlotID:        1,
			LeaderID:      1,
			CurrentVoters: []uint64{1, 2, 3},
		},
	}
	reader := NewManagementSlotRuntimeStatusReader(node)

	got, err := reader.SlotRuntimeStatus(context.Background(), 1, []uint64{1})
	if err != nil {
		t.Fatalf("SlotRuntimeStatus() error = %v", err)
	}

	if got.SlotID != 1 || got.LeaderID != 1 || !reflect.DeepEqual(got.CurrentVoters, []uint64{1, 2, 3}) {
		t.Fatalf("SlotRuntimeStatus() = %#v, want slot 1 leader 1 voters [1 2 3]", got)
	}
	node.status.CurrentVoters[0] = 9
	if got.CurrentVoters[0] != 1 {
		t.Fatalf("CurrentVoters was not cloned: got %v after source mutation", got.CurrentVoters)
	}
}

func TestManagementSlotRuntimeStatusReaderSkipsLocalSlotNotFoundAndReadsRemoteCandidate(t *testing.T) {
	service := &fakeRemoteSlotRaftService{
		status: managementusecase.SlotNodeLogStatus{
			NodeID:        2,
			LeaderID:      2,
			Role:          "leader",
			CommitIndex:   93,
			AppliedIndex:  91,
			CurrentVoters: []uint64{1, 2, 3},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerSlotRaft: service})
	node := &fakeManagementSlotRaftNode{
		nodeID:    1,
		statusErr: cluster.ErrSlotNotFound,
		handler:   adapter.HandleManagerSlotRaftRPC,
	}
	reader := NewManagementSlotRuntimeStatusReader(node)

	got, err := reader.SlotRuntimeStatus(context.Background(), 1, []uint64{1, 2, 3})
	if err != nil {
		t.Fatalf("SlotRuntimeStatus() error = %v", err)
	}

	if got.SlotID != 1 || got.LeaderID != 2 || !reflect.DeepEqual(got.CurrentVoters, []uint64{1, 2, 3}) {
		t.Fatalf("SlotRuntimeStatus() = %#v, want remote slot 1 leader 2 voters [1 2 3]", got)
	}
	if node.localStatusSlotID != 1 || service.nodeID != 2 || service.slotID != 1 {
		t.Fatalf("status attempts local=%d remote=%d/%d, want local slot 1 then remote node 2 slot 1", node.localStatusSlotID, service.nodeID, service.slotID)
	}
}

type fakeManagementLeaderTransferNode struct {
	request control.SlotLeaderTransferRequest
	result  control.SlotLeaderTransferResult
}

func (f *fakeManagementLeaderTransferNode) RequestSlotLeaderTransfer(_ context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	f.request = req
	return f.result, nil
}

type fakeManagementSlotReplicaMoveNode struct {
	request control.SlotReplicaMoveRequest
	result  control.SlotReplicaMoveResult
}

func (f *fakeManagementSlotReplicaMoveNode) RequestSlotReplicaMove(_ context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	f.request = req
	return f.result, nil
}
