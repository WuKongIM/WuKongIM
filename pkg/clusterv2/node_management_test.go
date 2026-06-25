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

func TestNodeRequestSlotReplicaMoveDelegatesToControl(t *testing.T) {
	controller := control.NewStaticController(control.Snapshot{})
	node := &Node{control: controller}
	node.started.Store(true)

	req := control.SlotReplicaMoveRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    4,
		TargetPeers:   []uint64{4, 2, 3},
		ConfigEpoch:   7,
		StateRevision: 12,
	}
	got, err := node.RequestSlotReplicaMove(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestSlotReplicaMove() error = %v", err)
	}

	if !got.Created || got.Task == nil || got.Task.TargetNode != 4 {
		t.Fatalf("RequestSlotReplicaMove() = %#v, want created target-node task", got)
	}
	if len(controller.SlotReplicaMoves) != 1 || controller.SlotReplicaMoves[0].TargetNode != 4 {
		t.Fatalf("controller moves = %#v, want one target node 4 request", controller.SlotReplicaMoves)
	}
}

func TestNodeMarkNodeLeavingDelegatesToControl(t *testing.T) {
	controller := &recordingMarkNodeLeavingController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		result: control.MarkNodeLeavingResult{
			Changed:  true,
			Node:     control.Node{NodeID: 4, JoinState: control.NodeJoinStateLeaving},
			Revision: 12,
		},
	}
	node := &Node{control: controller}
	node.started.Store(true)

	got, err := node.MarkNodeLeaving(context.Background(), control.MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}

	if !got.Changed || got.Node.NodeID != 4 || got.Node.JoinState != control.NodeJoinStateLeaving || got.Revision != 12 {
		t.Fatalf("MarkNodeLeaving() = %#v, want changed leaving node revision 12", got)
	}
	if len(controller.requests) != 1 || controller.requests[0].NodeID != 4 {
		t.Fatalf("controller mark-leaving requests = %#v, want one node 4 request", controller.requests)
	}
}

func TestNodeMarkNodeLeavingRequiresForegroundNode(t *testing.T) {
	controller := &recordingMarkNodeLeavingController{StaticController: control.NewStaticController(control.Snapshot{})}
	node := &Node{control: controller}

	_, err := node.MarkNodeLeaving(context.Background(), control.MarkNodeLeavingRequest{NodeID: 4})
	if err != ErrNotStarted {
		t.Fatalf("MarkNodeLeaving() error = %v, want %v", err, ErrNotStarted)
	}
}

type recordingMarkNodeLeavingController struct {
	*control.StaticController
	requests []control.MarkNodeLeavingRequest
	result   control.MarkNodeLeavingResult
	err      error
}

func (c *recordingMarkNodeLeavingController) MarkNodeLeaving(_ context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return control.MarkNodeLeavingResult{}, c.err
	}
	return c.result, nil
}
