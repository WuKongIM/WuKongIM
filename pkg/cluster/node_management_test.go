package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

type controllerVoterPreparationFacade interface {
	PrepareControllerVoter(context.Context, controller.PrepareControllerVoterRequest) (controller.PrepareControllerVoterResult, error)
	LocalControllerRaftStatus(context.Context) (ControllerRaftStatus, error)
}

var _ controllerVoterPreparationFacade = (*Node)(nil)

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

func TestLocalControlSnapshotIncludesChannelDataPlaneLease(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	ttl := 30 * time.Second
	guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, ttl)
	guard.MarkVisible(now.Add(-5 * time.Second))
	node := &Node{
		controlSnapshot:       control.Snapshot{ControllerID: 1},
		channelDataPlaneLease: guard,
	}
	node.started.Store(true)

	got, err := node.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot() error = %v", err)
	}
	if !got.ChannelDataPlaneLease.Ready {
		t.Fatalf("ChannelDataPlaneLease.Ready = false, want true")
	}
	if !got.ChannelDataPlaneLease.LastVisibleAt.Equal(now.Add(-5 * time.Second)) {
		t.Fatalf("ChannelDataPlaneLease.LastVisibleAt = %s, want %s", got.ChannelDataPlaneLease.LastVisibleAt, now.Add(-5*time.Second))
	}
	if got.ChannelDataPlaneLease.TTL != ttl {
		t.Fatalf("ChannelDataPlaneLease.TTL = %s, want %s", got.ChannelDataPlaneLease.TTL, ttl)
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

func TestNodeRequestSlotReplicaMoveNormalizesControllerLeadershipError(t *testing.T) {
	controlSource := &recordingSlotReplicaMoveController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		err:              controller.ErrNotLeader,
	}
	node := &Node{control: controlSource}
	node.started.Store(true)

	_, err := node.RequestSlotReplicaMove(context.Background(), control.SlotReplicaMoveRequest{SlotID: 1, TargetNode: 4})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("RequestSlotReplicaMove() error = %v, want cluster ErrNotLeader", err)
	}
	if !errors.Is(err, controller.ErrNotLeader) {
		t.Fatalf("RequestSlotReplicaMove() error = %v, want preserved controller ErrNotLeader", err)
	}
}

func TestNormalizeControlWriteError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  error
		facade error
	}{
		{name: "not leader", input: controller.ErrNotLeader, facade: ErrNotLeader},
		{name: "not started", input: controller.ErrNotStarted, facade: ErrNotStarted},
		{name: "stopped", input: controller.ErrStopped, facade: ErrStopping},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := normalizeControlWriteError(tc.input)
			if !errors.Is(err, tc.facade) {
				t.Fatalf("normalizeControlWriteError() = %v, want facade %v", err, tc.facade)
			}
			if !errors.Is(err, tc.input) {
				t.Fatalf("normalizeControlWriteError() = %v, want preserved cause %v", err, tc.input)
			}
		})
	}

	other := errors.New("other")
	if got := normalizeControlWriteError(other); got != other {
		t.Fatalf("normalizeControlWriteError(other) = %v, want original error", got)
	}
	if got := normalizeControlWriteError(nil); got != nil {
		t.Fatalf("normalizeControlWriteError(nil) = %v, want nil", got)
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

func TestNodeMarkNodeRemovedDelegatesToControl(t *testing.T) {
	controller := &recordingMarkNodeRemovedController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		result: control.MarkNodeRemovedResult{
			Changed:  true,
			Node:     control.Node{NodeID: 4, JoinState: control.NodeJoinStateRemoved, Status: control.NodeDown},
			Revision: 22,
		},
	}
	node := &Node{control: controller}
	node.started.Store(true)

	got, err := node.MarkNodeRemoved(context.Background(), control.MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}

	if !got.Changed || got.Node.NodeID != 4 || got.Node.JoinState != control.NodeJoinStateRemoved || got.Revision != 22 {
		t.Fatalf("MarkNodeRemoved() = %#v, want changed removed node revision 22", got)
	}
	if len(controller.requests) != 1 || controller.requests[0].NodeID != 4 {
		t.Fatalf("controller mark-removed requests = %#v, want one node 4 request", controller.requests)
	}
}

func TestNodeMarkNodeRemovedRequiresForegroundNode(t *testing.T) {
	controller := &recordingMarkNodeRemovedController{StaticController: control.NewStaticController(control.Snapshot{})}
	node := &Node{control: controller}

	_, err := node.MarkNodeRemoved(context.Background(), control.MarkNodeRemovedRequest{NodeID: 4})
	if err != ErrNotStarted {
		t.Fatalf("MarkNodeRemoved() error = %v, want %v", err, ErrNotStarted)
	}
}

func TestNodePromoteControllerVoterDelegatesToControl(t *testing.T) {
	controller := &recordingPromoteControllerVoterController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		result: control.PromoteControllerVoterResult{
			Changed:        true,
			Node:           control.Node{NodeID: 4, Roles: []control.Role{control.RoleData, control.RoleController}, JoinState: control.NodeJoinStateActive},
			Revision:       22,
			PreviousVoters: []uint64{1, 2, 3},
			NextVoters:     []uint64{1, 2, 3, 4},
			Warnings:       []string{"controller_voter_count_even"},
		},
	}
	node := &Node{control: controller}
	node.started.Store(true)

	req := control.PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedRevision:    21,
		ExpectedVoters:      []uint64{1, 2, 3},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 2, 3, 4},
	}
	got, err := node.PromoteControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}

	if !got.Changed || got.Node.NodeID != 4 || got.Revision != 22 || len(got.Warnings) != 1 {
		t.Fatalf("PromoteControllerVoter() = %#v, want changed promoted node revision 22", got)
	}
	if len(controller.requests) != 1 || !reflect.DeepEqual(controller.requests[0], req) {
		t.Fatalf("controller promote requests = %#v, want %#v", controller.requests, req)
	}
}

func TestNodePromoteControllerVoterRequiresForegroundNode(t *testing.T) {
	controller := &recordingPromoteControllerVoterController{StaticController: control.NewStaticController(control.Snapshot{})}
	node := &Node{control: controller}

	_, err := node.PromoteControllerVoter(context.Background(), control.PromoteControllerVoterRequest{NodeID: 4})
	if err != ErrNotStarted {
		t.Fatalf("PromoteControllerVoter() error = %v, want %v", err, ErrNotStarted)
	}
}

func TestNodePrepareControllerVoterDelegatesToControl(t *testing.T) {
	controlSource := &recordingPrepareControllerVoterController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		result: controller.PrepareControllerVoterResult{
			Prepared:      true,
			StateRevision: 22,
		},
	}
	node := &Node{control: controlSource}
	node.started.Store(true)

	req := controller.PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "cluster-a",
		ExpectedRevision: 21,
		NextVoters:       []controller.Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
	}
	got, err := node.PrepareControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}

	if !got.Prepared || got.StateRevision != 22 {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision 22", got)
	}
	if len(controlSource.requests) != 1 || !reflect.DeepEqual(controlSource.requests[0], req) {
		t.Fatalf("controller prepare requests = %#v, want %#v", controlSource.requests, req)
	}
}

func TestNodePrepareControllerVoterRequiresForegroundNode(t *testing.T) {
	controlSource := &recordingPrepareControllerVoterController{StaticController: control.NewStaticController(control.Snapshot{})}
	node := &Node{control: controlSource}

	_, err := node.PrepareControllerVoter(context.Background(), controller.PrepareControllerVoterRequest{NodeID: 4})
	if err != ErrNotStarted {
		t.Fatalf("PrepareControllerVoter() error = %v, want %v", err, ErrNotStarted)
	}
}

func TestNodeRegistersControlRuntimeRPCHandlersIdempotently(t *testing.T) {
	runtime, err := control.NewRuntime(control.RuntimeConfig{
		NodeID:           4,
		Addr:             "n4",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-a",
		Role:             control.RuntimeRoleVoter,
		Voters:           []control.RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	node := &Node{
		transportServer: clusternet.NewTransportServer(clusternet.TransportServerConfig{NodeID: 4}),
	}

	node.registerControlRuntimeRPCHandlers(runtime)
	node.registerControlRuntimeRPCHandlers(runtime)

	for _, serviceID := range []uint8{
		clusternet.RPCControlRaft,
		clusternet.RPCControlStateSync,
		clusternet.RPCControlTaskResult,
		clusternet.RPCControlWrite,
	} {
		if _, ok := node.registeredRPCHandlers[serviceID]; !ok {
			t.Fatalf("registeredRPCHandlers missing service %d: %#v", serviceID, node.registeredRPCHandlers)
		}
	}
	if len(node.registeredRPCHandlers) != 4 {
		t.Fatalf("registeredRPCHandlers len = %d, want 4: %#v", len(node.registeredRPCHandlers), node.registeredRPCHandlers)
	}
}

type recordingMarkNodeLeavingController struct {
	*control.StaticController
	requests []control.MarkNodeLeavingRequest
	result   control.MarkNodeLeavingResult
	err      error
}

type recordingSlotReplicaMoveController struct {
	*control.StaticController
	requests []control.SlotReplicaMoveRequest
	result   control.SlotReplicaMoveResult
	err      error
}

func (c *recordingSlotReplicaMoveController) RequestSlotReplicaMove(_ context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return control.SlotReplicaMoveResult{}, c.err
	}
	return c.result, nil
}

func (c *recordingMarkNodeLeavingController) MarkNodeLeaving(_ context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return control.MarkNodeLeavingResult{}, c.err
	}
	return c.result, nil
}

type recordingMarkNodeRemovedController struct {
	*control.StaticController
	requests []control.MarkNodeRemovedRequest
	result   control.MarkNodeRemovedResult
	err      error
}

func (c *recordingMarkNodeRemovedController) MarkNodeRemoved(_ context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return control.MarkNodeRemovedResult{}, c.err
	}
	return c.result, nil
}

type recordingPromoteControllerVoterController struct {
	*control.StaticController
	requests []control.PromoteControllerVoterRequest
	result   control.PromoteControllerVoterResult
	err      error
}

func (c *recordingPromoteControllerVoterController) PromoteControllerVoter(_ context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return control.PromoteControllerVoterResult{}, c.err
	}
	return c.result, nil
}

type recordingPrepareControllerVoterController struct {
	*control.StaticController
	requests []controller.PrepareControllerVoterRequest
	result   controller.PrepareControllerVoterResult
	err      error
}

func (c *recordingPrepareControllerVoterController) PrepareControllerVoter(_ context.Context, req controller.PrepareControllerVoterRequest) (controller.PrepareControllerVoterResult, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return controller.PrepareControllerVoterResult{}, c.err
	}
	return c.result, nil
}
