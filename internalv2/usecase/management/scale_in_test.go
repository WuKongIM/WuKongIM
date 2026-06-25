package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestScaleInStatusFailsClosedWhenRuntimeSummaryUnknown(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: scaleInSnapshot(17)},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{
			1: {NodeID: 1, ControlRevision: 17},
			2: {NodeID: 2, Unknown: true},
			3: {NodeID: 3, ControlRevision: 17},
		}},
	})
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.UnknownRuntime || status.BlockedByControlRevision || status.UnknownControlRevision {
		t.Fatalf("status = %#v, want fail-closed unknown runtime without stale control revision blocker", status)
	}
}

func TestScaleInStatusBlocksWhenControlRevisionIsStale(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: scaleInSnapshot(17)},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{
			1: {NodeID: 1, ControlRevision: 17},
			2: {NodeID: 2, ControlRevision: 16},
			3: {NodeID: 3, ControlRevision: 17},
		}},
	})
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByControlRevision || status.UnknownControlRevision {
		t.Fatalf("status = %#v, want stale control revision blocker", status)
	}
}

func TestScaleInStatusBlocksWhenSlotDesiredPeersContainTarget(t *testing.T) {
	app := New(Options{
		Cluster:        fakeNodeSnapshotReader{snapshot: scaleInSnapshot(17)},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummaries(17)},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
		}},
	})
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedBySlots || status.SlotReplicaCount != 1 {
		t.Fatalf("status = %#v, want unsafe with one target slot replica", status)
	}
}

func TestScaleInStatusBlocksWhenRuntimeVotersStillContainTargetAfterDesiredPeersMoved(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, control.Node{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive})
	snap.Slots[0].DesiredPeers = []uint64{1, 3, 4}
	app := New(Options{
		Cluster:        fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
		}},
	})
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedBySlotRuntime || status.SlotReplicaCount != 1 {
		t.Fatalf("status = %#v, want unsafe with live runtime voter blocker", status)
	}
}

func TestScaleInStatusReportsSafetyBlockerCategories(t *testing.T) {
	tests := []struct {
		name                string
		nodeID              uint64
		mutate              func(*control.Snapshot, *SlotRuntimeStatus)
		wantSafe            bool
		wantMissingNode     bool
		wantJoinState       bool
		wantControllerRole  bool
		wantSlotLeadership  bool
		wantTaskBlocker     bool
		wantActiveTaskCount int
		wantFailedTaskCount int
	}{
		{
			name:            "missing target",
			nodeID:          99,
			wantMissingNode: true,
		},
		{
			name:   "non leaving target",
			nodeID: 2,
			mutate: func(snapshot *control.Snapshot, _ *SlotRuntimeStatus) {
				snapshot.Nodes[1].JoinState = control.NodeJoinStateActive
			},
			wantJoinState: true,
		},
		{
			name:   "controller role target",
			nodeID: 2,
			mutate: func(snapshot *control.Snapshot, _ *SlotRuntimeStatus) {
				snapshot.Nodes[1].Roles = []control.Role{control.RoleController, control.RoleData}
			},
			wantControllerRole: true,
		},
		{
			name:   "live slot leadership",
			nodeID: 2,
			mutate: func(_ *control.Snapshot, runtime *SlotRuntimeStatus) {
				runtime.LeaderID = 2
			},
			wantSlotLeadership: true,
		},
		{
			name:   "active task references target",
			nodeID: 2,
			mutate: func(snapshot *control.Snapshot, _ *SlotRuntimeStatus) {
				snapshot.Tasks = []control.ReconcileTask{{
					TaskID:     "slot-1-replica-move-2-to-4-r17",
					SlotID:     1,
					Kind:       control.TaskKindSlotReplicaMove,
					SourceNode: 2,
					TargetNode: 4,
					Status:     control.TaskStatusPending,
				}}
			},
			wantTaskBlocker:     true,
			wantActiveTaskCount: 1,
		},
		{
			name:   "failed task references target",
			nodeID: 2,
			mutate: func(snapshot *control.Snapshot, _ *SlotRuntimeStatus) {
				snapshot.Tasks = []control.ReconcileTask{{
					TaskID:      "slot-1-replica-move-4-to-5-r17",
					SlotID:      1,
					Kind:        control.TaskKindSlotReplicaMove,
					SourceNode:  4,
					TargetNode:  5,
					TargetPeers: []uint64{1, 2, 5},
					Status:      control.TaskStatusFailed,
				}}
			},
			wantTaskBlocker:     true,
			wantFailedTaskCount: 1,
		},
		{
			name:     "safe after slot drain",
			nodeID:   2,
			wantSafe: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := scaleInDrainedSnapshot(17)
			runtime := SlotRuntimeStatus{SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 3, 4}}
			if tt.mutate != nil {
				tt.mutate(&snap, &runtime)
			}
			app := New(Options{
				Cluster:        fakeNodeSnapshotReader{snapshot: snap},
				RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, scaleInSnapshotNodeIDs(snap)...)},
				SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{statuses: map[uint32]SlotRuntimeStatus{
					1: runtime,
				}},
			})

			status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: tt.nodeID})
			if err != nil {
				t.Fatalf("NodeScaleInStatus() error = %v", err)
			}
			if status.SafeToProceed != tt.wantSafe ||
				status.BlockedByMissingNode != tt.wantMissingNode ||
				status.BlockedByJoinState != tt.wantJoinState ||
				status.BlockedByControllerRole != tt.wantControllerRole ||
				status.BlockedBySlotLeadership != tt.wantSlotLeadership ||
				status.BlockedByTasks != tt.wantTaskBlocker ||
				status.ActiveTaskCount != tt.wantActiveTaskCount ||
				status.FailedTaskCount != tt.wantFailedTaskCount {
				t.Fatalf("status = %#v, want safe=%v missing=%v join=%v controller=%v leadership=%v tasks=%v active=%d failed=%d",
					status,
					tt.wantSafe,
					tt.wantMissingNode,
					tt.wantJoinState,
					tt.wantControllerRole,
					tt.wantSlotLeadership,
					tt.wantTaskBlocker,
					tt.wantActiveTaskCount,
					tt.wantFailedTaskCount,
				)
			}
		})
	}
}

func TestAdvanceNodeScaleInCreatesMoveAwayFromLeavingNode(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, control.Node{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive})
	writer := &fakeSlotReplicaMoveWriter{result: control.SlotReplicaMoveResult{Created: true}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:  fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}}},
		},
	})

	got, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("AdvanceNodeScaleIn() error = %v", err)
	}
	if got.Created != 1 || len(writer.requests) != 1 {
		t.Fatalf("advance = %#v requests=%#v, want one created move", got, writer.requests)
	}
	req := writer.requests[0]
	if req.SlotID != 1 || req.SourceNode != 2 || req.TargetNode != 4 || req.StateRevision != 17 || req.ConfigEpoch != 7 {
		t.Fatalf("move request = %#v, want slot 1 source 2 target 4 revision 17 epoch 7", req)
	}
	if !sameUint64Slice(req.TargetPeers, []uint64{1, 4, 3}) {
		t.Fatalf("target peers = %v, want [1 4 3]", req.TargetPeers)
	}
}

func scaleInSnapshot(revision uint64) control.Snapshot {
	return control.Snapshot{
		Revision: revision,
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		},
		Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1}},
	}
}

func scaleInDrainedSnapshot(revision uint64) control.Snapshot {
	snapshot := scaleInSnapshot(revision)
	snapshot.Nodes = append(snapshot.Nodes, control.Node{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive})
	snapshot.Slots[0].DesiredPeers = []uint64{1, 3, 4}
	return snapshot
}

func scaleInSnapshotNodeIDs(snapshot control.Snapshot) []uint64 {
	nodeIDs := make([]uint64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodeIDs = append(nodeIDs, node.NodeID)
	}
	return nodeIDs
}

func scaleInRuntimeSummaries(revision uint64) map[uint64]NodeRuntimeSummary {
	return scaleInRuntimeSummariesFor(revision, 1, 2, 3)
}

func scaleInRuntimeSummariesFor(revision uint64, nodeIDs ...uint64) map[uint64]NodeRuntimeSummary {
	summaries := make(map[uint64]NodeRuntimeSummary, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		summaries[nodeID] = NodeRuntimeSummary{NodeID: nodeID, ControlRevision: revision}
	}
	return summaries
}
