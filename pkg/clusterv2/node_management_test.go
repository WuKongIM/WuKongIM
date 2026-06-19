package clusterv2

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestLocalControlSnapshotReturnsClone(t *testing.T) {
	node := &Node{
		controlSnapshot: control.Snapshot{
			ControllerID: 1,
			Nodes: []control.Node{{
				NodeID: 1,
				Addr:   "127.0.0.1:7011",
				Roles:  []control.Role{control.RoleController, control.RoleData},
				Status: control.NodeAlive,
			}},
		},
	}
	node.started.Store(true)

	got, err := node.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot() error = %v", err)
	}
	got.Nodes[0].Addr = "mutated"
	got.Nodes[0].Roles[0] = control.RoleData

	again, err := node.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot() second error = %v", err)
	}
	if again.Nodes[0].Addr != "127.0.0.1:7011" {
		t.Fatalf("snapshot node addr = %q, want original", again.Nodes[0].Addr)
	}
	if again.Nodes[0].Roles[0] != control.RoleController {
		t.Fatalf("snapshot role = %q, want original", again.Nodes[0].Roles[0])
	}
}

func TestLocalControlSnapshotRequiresForegroundNode(t *testing.T) {
	node := &Node{}

	_, err := node.LocalControlSnapshot(context.Background())
	if err != ErrNotStarted {
		t.Fatalf("LocalControlSnapshot() error = %v, want %v", err, ErrNotStarted)
	}
}

func TestNodeRequestSlotLeaderTransferDelegatesToControl(t *testing.T) {
	controller := control.NewStaticController(control.Snapshot{})
	node := &Node{control: controller}
	node.started.Store(true)

	req := control.SlotLeaderTransferRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    2,
		TargetPeers:   []uint64{1, 2, 3},
		ConfigEpoch:   4,
		StateRevision: 9,
	}
	got, err := node.RequestSlotLeaderTransfer(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}

	if !got.Created || got.Task == nil || got.Task.TargetNode != 2 {
		t.Fatalf("RequestSlotLeaderTransfer() = %#v, want created target-node task", got)
	}
	if len(controller.LeaderTransfers) != 1 || controller.LeaderTransfers[0].TargetNode != 2 {
		t.Fatalf("controller transfers = %#v, want one target node 2 request", controller.LeaderTransfers)
	}
}

func TestNodeRequestSlotLeaderTransferRequiresForegroundNode(t *testing.T) {
	node := &Node{control: control.NewStaticController(control.Snapshot{})}

	_, err := node.RequestSlotLeaderTransfer(context.Background(), control.SlotLeaderTransferRequest{SlotID: 1, TargetNode: 2})
	if err != ErrNotStarted {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v, want %v", err, ErrNotStarted)
	}
}
