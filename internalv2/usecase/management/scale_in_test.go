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
