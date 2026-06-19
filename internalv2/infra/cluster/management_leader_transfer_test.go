package cluster

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

var _ ManagementLeaderTransferNode = (*clusterv2.Node)(nil)

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

func TestManagementSlotRuntimeStatusReaderUsesLocalSlotStatus(t *testing.T) {
	node := &fakeManagementSlotRuntimeStatusNode{
		status: clusterv2.SlotRaftStatus{
			NodeID:        1,
			SlotID:        1,
			LeaderID:      1,
			CurrentVoters: []uint64{1, 2, 3},
		},
	}
	reader := NewManagementSlotRuntimeStatusReader(node)

	got, err := reader.SlotRuntimeStatus(context.Background(), 1)
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

type fakeManagementLeaderTransferNode struct {
	request control.SlotLeaderTransferRequest
	result  control.SlotLeaderTransferResult
}

func (f *fakeManagementLeaderTransferNode) RequestSlotLeaderTransfer(_ context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	f.request = req
	return f.result, nil
}

type fakeManagementSlotRuntimeStatusNode struct {
	status clusterv2.SlotRaftStatus
}

func (f *fakeManagementSlotRuntimeStatusNode) LocalSlotRaftStatus(_ context.Context, _ uint32) (clusterv2.SlotRaftStatus, error) {
	return f.status, nil
}
