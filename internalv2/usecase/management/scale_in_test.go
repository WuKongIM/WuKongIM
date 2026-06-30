package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

func TestScaleInStatusBlocksWhenTargetHealthIsStale(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	snap.Nodes[3].Health.Freshness = control.NodeHealthStale
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByHealth || status.HealthFresh ||
		status.HealthStatus != string(control.NodeAlive) ||
		!containsString(status.BlockedReasons, "target_health_stale") {
		t.Fatalf("status = %#v, want target stale health blocker", status)
	}
}

func TestScaleInStatusBlocksWhenEligibleNodeHasNotObservedRevision(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	snap.Revision = 22
	snap.Nodes[0].Roles = []control.Role{control.RoleController, control.RoleData}
	snap.Nodes[0].Health.ObservedControlRevision = 21
	snap.Nodes[1].Health.ObservedControlRevision = 22
	snap.Nodes[2].Health.ObservedControlRevision = 22
	snap.Nodes[3].Health.ObservedControlRevision = 22
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByHealth || !status.BlockedByStaleRevision ||
		!containsString(status.BlockedReasons, "eligible_node_health_revision_stale") {
		t.Fatalf("status = %#v, want eligible stale health revision blocker", status)
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
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
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

func TestScaleInStatusBlocksWhenChannelInventoryReferencesTarget(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{{ChannelID: "ch", ChannelType: 1, Leader: 4, Replicas: []uint64{2, 4}, ISR: []uint64{4}}},
			done:  true,
		},
	}
	snap := scaleInReadyNoSlotReplicaSnapshot()
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: reader,
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByChannels || status.ChannelLeaderCount != 1 || status.ChannelReplicaCount != 1 || status.ChannelISRCount != 1 {
		t.Fatalf("status = %#v, want channel blockers", status)
	}
}

func TestScaleInStatusBlocksWhenChannelInventoryUnknown(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	app := New(Options{
		Cluster:           fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:    fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus: scaleInSafeSlotRuntimeReader{},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByChannels || !status.UnknownChannelInventory {
		t.Fatalf("status = %#v, want unknown channel inventory blocker", status)
	}
}

func TestScaleInStatusBlocksWhenRuntimeDrainIsNotEmpty(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	summaries := scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)
	summaries[4] = NodeRuntimeSummary{
		NodeID:               4,
		ControlRevision:      snap.Revision,
		Draining:             true,
		AcceptingNewSessions: false,
		GatewaySessions:      1,
		ActiveOnline:         1,
		TotalOnline:          2,
		PendingActivations:   1,
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: summaries},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.SafeToProceed || status.SafeToRemove || !status.BlockedByRuntimeDrain || status.RuntimeUnknown ||
		!status.GatewayDraining || status.AcceptingNewSessions ||
		status.GatewaySessions != 1 || status.ActiveOnline != 1 || status.TotalOnline != 2 ||
		status.PendingActivations != 1 {
		t.Fatalf("status = %#v, want progress-safe but remove-blocked runtime drain counters", status)
	}
}

func TestScaleInStatusBlocksRemoveWhenOnlyPendingActivationsRemain(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	summaries := scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)
	summaries[4] = NodeRuntimeSummary{
		NodeID:             4,
		ControlRevision:    snap.Revision,
		Draining:           true,
		PendingActivations: 1,
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: summaries},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.SafeToProceed || status.SafeToRemove || !status.BlockedByRuntimeDrain ||
		status.PendingActivations != 1 || status.GatewaySessions != 0 || status.ActiveOnline != 0 {
		t.Fatalf("status = %#v, want pending activation to block final removal only", status)
	}
}

func TestScaleInStatusSafeToRemoveAfterChannelAndRuntimeDrainClear(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	summaries := scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)
	summaries[4] = NodeRuntimeSummary{
		NodeID:               4,
		ControlRevision:      snap.Revision,
		Draining:             true,
		AcceptingNewSessions: false,
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: summaries},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.SafeToProceed || !status.SafeToRemove || status.BlockedByRuntimeDrain ||
		!status.GatewayDraining || status.AcceptingNewSessions ||
		status.GatewaySessions != 0 || status.ActiveOnline != 0 || status.TotalOnline != 0 ||
		status.PendingActivations != 0 {
		t.Fatalf("status = %#v, want final remove-safe state", status)
	}
}

func TestMarkNodeRemovedRequiresSafeToRemove(t *testing.T) {
	writer := &nodeLifecycleWriterStub{}
	snap := scaleInSnapshot(17)
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{snapshot: snap},
		NodeLifecycle: writer,
	})

	_, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 2})
	if !errors.Is(err, ErrNodeScaleInUnsafe) {
		t.Fatalf("MarkNodeRemoved() error = %v, want ErrNodeScaleInUnsafe", err)
	}
	if writer.removedReq.NodeID != 0 {
		t.Fatalf("writer removed request = %#v, want not called", writer.removedReq)
	}
}

func TestMarkNodeRemovedDelegatesWhenSafe(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	writer := &nodeLifecycleWriterStub{removedResult: control.MarkNodeRemovedResult{
		Changed:  true,
		Node:     control.Node{NodeID: 4, JoinState: control.NodeJoinStateRemoved},
		Revision: snap.Revision + 1,
	}}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{
			1: {NodeID: 1, ControlRevision: snap.Revision},
			2: {NodeID: 2, ControlRevision: snap.Revision},
			3: {NodeID: 3, ControlRevision: snap.Revision},
			4: {NodeID: 4, ControlRevision: snap.Revision, Draining: true, AcceptingNewSessions: false},
		}},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
		NodeLifecycle:      writer,
	})

	resp, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !resp.Changed || resp.NodeID != 4 || resp.JoinState != "removed" || writer.removedReq.NodeID != 4 || writer.removedReq.StateRevision != snap.Revision {
		t.Fatalf("response=%#v request=%#v, want removed node 4", resp, writer.removedReq)
	}
}

func TestMarkNodeRemovedMapsRevisionMismatchToConflict(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	writer := &nodeLifecycleWriterStub{removedErr: cv2.ErrExpectedRevisionMismatch}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{
			1: {NodeID: 1, ControlRevision: snap.Revision},
			2: {NodeID: 2, ControlRevision: snap.Revision},
			3: {NodeID: 3, ControlRevision: snap.Revision},
			4: {NodeID: 4, ControlRevision: snap.Revision, Draining: true, AcceptingNewSessions: false},
		}},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
		NodeLifecycle:      writer,
	})

	_, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeRemoved() error = %v, want ErrNodeLifecycleConflict", err)
	}
}

func TestMarkNodeRemovedDelegatesWhenAlreadyRemoved(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	snap.Nodes[3].JoinState = control.NodeJoinStateRemoved
	snap.Nodes[3].Status = control.NodeDown
	writer := &nodeLifecycleWriterStub{removedResult: control.MarkNodeRemovedResult{
		Changed:  false,
		Node:     control.Node{NodeID: 4, JoinState: control.NodeJoinStateRemoved},
		Revision: snap.Revision,
	}}
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{snapshot: snap},
		NodeLifecycle: writer,
	})

	resp, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if resp.Changed || resp.NodeID != 4 || resp.JoinState != "removed" || writer.removedReq.NodeID != 4 || writer.removedReq.StateRevision != 0 {
		t.Fatalf("response=%#v request=%#v, want idempotent removed node 4", resp, writer.removedReq)
	}
}

func TestScaleInStatusRequiresDataNodeRole(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	snap.Nodes[3].Roles = nil
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(snap.Revision, scaleInSnapshotNodeIDs(snap)...)},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || status.SafeToRemove || !status.BlockedByDataRole {
		t.Fatalf("status = %#v, want non-data node blocked from scale-in", status)
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
				Cluster:            fakeNodeSnapshotReader{snapshot: snap},
				RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, scaleInSnapshotNodeIDs(snap)...)},
				ChannelRuntimeMeta: newChannelDrainMetaReader(),
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
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
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

func TestAdvanceNodeScaleInAllowsSlotDrainWhenChannelInventoryUnknown(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
	writer := &fakeSlotReplicaMoveWriter{result: control.SlotReplicaMoveResult{Created: true}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:  fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}}},
		},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.UnknownChannelInventory {
		t.Fatalf("status = %#v, want final status blocked by unknown channel inventory", status)
	}

	got, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("AdvanceNodeScaleIn() error = %v", err)
	}
	if got.Created != 1 || len(writer.requests) != 1 {
		t.Fatalf("advance = %#v requests=%#v, want slot drain to continue", got, writer.requests)
	}
}

func TestAdvanceNodeScaleInAllowsSlotDrainWhenLeavingNodeIsSlotLeader(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
	writer := &fakeSlotReplicaMoveWriter{result: control.SlotReplicaMoveResult{Created: true}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:  fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}}},
		},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.BlockedBySlotLeadership {
		t.Fatalf("status = %#v, want source Slot leadership blocker visible", status)
	}

	got, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("AdvanceNodeScaleIn() error = %v", err)
	}
	if got.Created != 1 || len(writer.requests) != 1 {
		t.Fatalf("advance = %#v requests=%#v, want slot drain task despite source Slot leadership", got, writer.requests)
	}
}

func TestAdvanceNodeScaleInMapsActiveTaskConflictToScaleInConflict(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
	writer := &fakeSlotReplicaMoveWriter{err: cv2.ErrSlotActiveTaskConflict}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:  fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}}},
		},
	})

	_, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 1})
	if !errors.Is(err, ErrNodeScaleInConflict) {
		t.Fatalf("AdvanceNodeScaleIn() error = %v, want %v", err, ErrNodeScaleInConflict)
	}
	if len(writer.requests) < 2 {
		t.Fatalf("requests = %d, want active-task conflict to retry before bounded conflict", len(writer.requests))
	}
}

func TestAdvanceNodeScaleInRefreshesRevisionBetweenCandidates(t *testing.T) {
	snapshot := scaleInSnapshot(17)
	snapshot.Nodes = append(snapshot.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snapshot.Revision))
	snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, PreferredLeader: 1})
	cluster := &scaleInMutableSnapshotReader{snapshot: snapshot}
	writer := &scaleInRevisionFencedMoveWriter{cluster: cluster}
	app := New(Options{
		Cluster:         cluster,
		RuntimeSummary:  scaleInSnapshotRuntimeSummaryReader{cluster: cluster},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
		}},
	})

	got, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 2})
	if err != nil {
		t.Fatalf("AdvanceNodeScaleIn() error = %v", err)
	}
	if got.Created != 2 || len(writer.requests) != 2 {
		t.Fatalf("advance = %#v requests=%#v, want two created moves", got, writer.requests)
	}
	if writer.requests[0].StateRevision != 17 || writer.requests[1].StateRevision != 18 {
		t.Fatalf("requests = %#v, want second request to refresh control revision", writer.requests)
	}
}

func TestAdvanceNodeScaleInStopsWhenFreshStatusBecomesBlocked(t *testing.T) {
	snapshot := scaleInSnapshot(17)
	snapshot.Nodes = append(snapshot.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snapshot.Revision))
	snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, PreferredLeader: 1})
	cluster := &scaleInMutableSnapshotReader{snapshot: snapshot}
	writer := &scaleInRevisionFencedMoveWriter{
		cluster: cluster,
		afterCreate: func(snapshot *control.Snapshot) {
			snapshot.Tasks = append(snapshot.Tasks, control.ReconcileTask{
				TaskID:      "external-failed-scale-in-task",
				SlotID:      99,
				Kind:        control.TaskKindSlotReplicaMove,
				SourceNode:  5,
				TargetNode:  6,
				TargetPeers: []uint64{1, 2, 6},
				Status:      control.TaskStatusFailed,
			})
		},
	}
	app := New(Options{
		Cluster:         cluster,
		RuntimeSummary:  scaleInSnapshotRuntimeSummaryReader{cluster: cluster},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
		}},
	})

	got, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 2})
	if err != nil {
		t.Fatalf("AdvanceNodeScaleIn() error = %v", err)
	}
	if got.Created != 1 || len(writer.requests) != 1 {
		t.Fatalf("advance = %#v requests=%#v, want one created move before fresh status blocker", got, writer.requests)
	}
}

func scaleInSnapshot(revision uint64) control.Snapshot {
	return control.Snapshot{
		Revision: revision,
		Nodes: []control.Node{
			scaleInHealthNode(1, []control.Role{control.RoleController, control.RoleData}, control.NodeJoinStateActive, revision),
			scaleInHealthNode(2, []control.Role{control.RoleData}, control.NodeJoinStateLeaving, revision),
			scaleInHealthNode(3, []control.Role{control.RoleData}, control.NodeJoinStateActive, revision),
		},
		Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1}},
	}
}

func scaleInDrainedSnapshot(revision uint64) control.Snapshot {
	snapshot := scaleInSnapshot(revision)
	snapshot.Nodes = append(snapshot.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, revision))
	snapshot.Slots[0].DesiredPeers = []uint64{1, 3, 4}
	return snapshot
}

func scaleInReadyNoSlotReplicaSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 11,
		Nodes: []control.Node{
			scaleInHealthNode(1, []control.Role{control.RoleController}, control.NodeJoinStateActive, 11),
			scaleInHealthNode(2, []control.Role{control.RoleData}, control.NodeJoinStateActive, 11),
			scaleInHealthNode(3, []control.Role{control.RoleData}, control.NodeJoinStateActive, 11),
			scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateLeaving, 11),
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{2, 3}, PreferredLeader: 2, ConfigEpoch: 7},
		},
	}
}

func scaleInHealthNode(nodeID uint64, roles []control.Role, joinState control.NodeJoinState, revision uint64) control.Node {
	return control.Node{
		NodeID:    nodeID,
		Roles:     append([]control.Role(nil), roles...),
		Status:    control.NodeAlive,
		JoinState: joinState,
		Health: control.NodeHealth{
			Status:                  control.NodeAlive,
			Freshness:               control.NodeHealthFresh,
			RuntimeReady:            true,
			ObservedControlRevision: revision,
			ReportAge:               time.Second,
			ReportTTL:               30 * time.Second,
		},
	}
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
		summaries[nodeID] = NodeRuntimeSummary{NodeID: nodeID, ControlRevision: revision, Draining: true}
	}
	return summaries
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type scaleInMutableSnapshotReader struct {
	snapshot control.Snapshot
}

func (r *scaleInMutableSnapshotReader) NodeID() uint64 { return 1 }

func (r *scaleInMutableSnapshotReader) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return r.snapshot.Clone(), nil
}

type scaleInRevisionFencedMoveWriter struct {
	cluster     *scaleInMutableSnapshotReader
	requests    []control.SlotReplicaMoveRequest
	afterCreate func(*control.Snapshot)
}

func (w *scaleInRevisionFencedMoveWriter) RequestSlotReplicaMove(_ context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	w.requests = append(w.requests, req)
	if req.StateRevision != w.cluster.snapshot.Revision {
		return control.SlotReplicaMoveResult{}, cv2.ErrExpectedRevisionMismatch
	}
	task := control.ReconcileTask{
		TaskID:      "test-scale-in-move",
		SlotID:      req.SlotID,
		Kind:        control.TaskKindSlotReplicaMove,
		SourceNode:  req.SourceNode,
		TargetNode:  req.TargetNode,
		TargetPeers: append([]uint64(nil), req.TargetPeers...),
		ConfigEpoch: req.ConfigEpoch,
		Status:      control.TaskStatusPending,
	}
	w.cluster.snapshot.Tasks = append(w.cluster.snapshot.Tasks, task)
	w.cluster.snapshot.Revision++
	for i := range w.cluster.snapshot.Nodes {
		w.cluster.snapshot.Nodes[i].Health.ObservedControlRevision = w.cluster.snapshot.Revision
	}
	if w.afterCreate != nil {
		w.afterCreate(&w.cluster.snapshot)
	}
	return control.SlotReplicaMoveResult{Created: true, Task: &task}, nil
}

type scaleInSnapshotRuntimeSummaryReader struct {
	cluster *scaleInMutableSnapshotReader
}

func (r scaleInSnapshotRuntimeSummaryReader) NodeRuntimeSummary(_ context.Context, nodeID uint64) (NodeRuntimeSummary, error) {
	return NodeRuntimeSummary{NodeID: nodeID, ControlRevision: r.cluster.snapshot.Revision}, nil
}

type scaleInSafeSlotRuntimeReader struct{}

func (scaleInSafeSlotRuntimeReader) SlotRuntimeStatus(_ context.Context, slotID uint32, _ []uint64) (SlotRuntimeStatus, error) {
	return SlotRuntimeStatus{SlotID: slotID, LeaderID: 2, CurrentVoters: []uint64{2, 3}}, nil
}
