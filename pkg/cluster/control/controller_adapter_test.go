package control

import (
	"context"
	"strings"
	"testing"
	"time"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestControllerSnapshotMapping(t *testing.T) {
	snap, err := SnapshotFromController(controllerState())
	if err != nil {
		t.Fatalf("SnapshotFromController() error = %v", err)
	}
	if snap.Revision != 7 || snap.ControllerID != 1 {
		t.Fatalf("snapshot revision/controller = %d/%d, want 7/1", snap.Revision, snap.ControllerID)
	}
	if len(snap.Nodes) != 3 || snap.Nodes[0].Roles[0] != RoleController || snap.Nodes[0].Roles[1] != RoleData {
		t.Fatalf("nodes = %#v, want controller+data first node", snap.Nodes)
	}
	if snap.Nodes[0].JoinState != NodeJoinStateActive || snap.Nodes[0].CapacityWeight != 1 {
		t.Fatalf("first node lifecycle = %q capacity=%d, want active capacity 1", snap.Nodes[0].JoinState, snap.Nodes[0].CapacityWeight)
	}
	if len(snap.Slots) != 1 || snap.Slots[0].SlotID != 1 || snap.Slots[0].PreferredLeader != 1 {
		t.Fatalf("slots = %#v, want slot 1 preferred leader 1", snap.Slots)
	}
	if snap.HashSlots.Count != 4 || len(snap.HashSlots.Ranges) != 1 || snap.HashSlots.Ranges[0].To != 3 {
		t.Fatalf("hash slots = %#v, want one range 0..3", snap.HashSlots)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].Kind != TaskKindBootstrap {
		t.Fatalf("tasks = %#v, want bootstrap task", snap.Tasks)
	}
	task := snap.Tasks[0]
	if task.Step != TaskStepCreateSlot || task.SourceNode != 4 || task.Status != TaskStatusFailed || task.Attempt != 1 || task.LastError != "quorum missing" {
		t.Fatalf("mapped task = %#v, want full task read model", task)
	}
	if task.CompletionPolicy != TaskCompletionPolicyAllTargetPeers || len(task.ParticipantProgress) != 3 || task.ParticipantProgress[2].LastError != "open failed" {
		t.Fatalf("mapped participant progress = %#v", task.ParticipantProgress)
	}
}

func TestControllerSnapshotMappingPreservesNonActiveLifecycle(t *testing.T) {
	st := controllerState()
	st.Config.ReplicaCount = 1
	st.Slots[0].DesiredPeers = []uint64{1}
	st.Tasks = nil
	st.Nodes[1].JoinState = controller.NodeJoinStateJoining
	st.Nodes[1].CapacityWeight = 7
	st.Nodes[2].JoinState = controller.NodeJoinStateLeaving
	st.Nodes = append(st.Nodes, controller.Node{
		NodeID:         4,
		Addr:           "127.0.0.1:1004",
		Roles:          []controller.NodeRole{controller.NodeRoleData},
		JoinState:      controller.NodeJoinStateRemoved,
		Status:         controller.NodeStatusDown,
		CapacityWeight: 3,
	})

	snap, err := SnapshotFromController(st)
	if err != nil {
		t.Fatalf("SnapshotFromController() error = %v", err)
	}
	if snap.Nodes[1].JoinState != NodeJoinStateJoining || snap.Nodes[1].CapacityWeight != 7 {
		t.Fatalf("node 2 lifecycle = %q capacity=%d, want joining capacity 7", snap.Nodes[1].JoinState, snap.Nodes[1].CapacityWeight)
	}
	if snap.Nodes[2].JoinState != NodeJoinStateLeaving {
		t.Fatalf("node 3 lifecycle = %q, want leaving", snap.Nodes[2].JoinState)
	}
	if snap.Nodes[3].JoinState != NodeJoinStateRemoved || snap.Nodes[3].CapacityWeight != 3 {
		t.Fatalf("node 4 lifecycle = %q capacity=%d, want removed capacity 3", snap.Nodes[3].JoinState, snap.Nodes[3].CapacityWeight)
	}
}

func TestControllerAdapterMapsNodeHealthFreshness(t *testing.T) {
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	st := controller.ClusterState{
		Revision: 9,
		Nodes:    []controller.Node{{NodeID: 1, Addr: "n1", Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive}},
		NodeHealthReports: []controller.NodeHealthReport{{
			NodeID:                  1,
			Status:                  controller.NodeStatusAlive,
			RuntimeReady:            true,
			ObservedControlRevision: 9,
			ReportSeq:               3,
			ReportedAtUnixMilli:     now.Add(-5 * time.Second).UnixMilli(),
		}},
	}
	snap := snapshotFromControllerState(st, 1, now, 30*time.Second)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Health.Freshness != NodeHealthFresh {
		t.Fatalf("snapshot nodes = %#v, want fresh health", snap.Nodes)
	}
}

func TestControllerAdapterMapsMissingNodeHealth(t *testing.T) {
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	st := controller.ClusterState{
		Revision: 9,
		Nodes:    []controller.Node{{NodeID: 1, Addr: "n1", Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive}},
	}
	snap := snapshotFromControllerState(st, 1, now, 30*time.Second)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Health.Freshness != NodeHealthMissing {
		t.Fatalf("snapshot nodes = %#v, want missing health", snap.Nodes)
	}
}

func TestControllerAdapterMapsStaleNodeHealth(t *testing.T) {
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	st := controller.ClusterState{
		Revision: 9,
		Nodes:    []controller.Node{{NodeID: 1, Addr: "n1", Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive}},
		NodeHealthReports: []controller.NodeHealthReport{{
			NodeID:              1,
			Status:              controller.NodeStatusAlive,
			ReportedAtUnixMilli: now.Add(-31 * time.Second).UnixMilli(),
		}},
	}
	snap := snapshotFromControllerState(st, 1, now, 30*time.Second)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Health.Freshness != NodeHealthStale {
		t.Fatalf("snapshot nodes = %#v, want stale health", snap.Nodes)
	}
}

func TestControllerAdapterMapsFutureNodeHealthAsStale(t *testing.T) {
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	st := controller.ClusterState{
		Revision: 9,
		Nodes:    []controller.Node{{NodeID: 1, Addr: "n1", Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive}},
		NodeHealthReports: []controller.NodeHealthReport{{
			NodeID:              1,
			Status:              controller.NodeStatusAlive,
			ReportedAtUnixMilli: now.Add(5 * time.Second).UnixMilli(),
		}},
	}
	snap := snapshotFromControllerState(st, 1, now, 30*time.Second)
	if len(snap.Nodes) != 1 || snap.Nodes[0].Health.Freshness != NodeHealthStale || snap.Nodes[0].Health.ReportAge < 0 {
		t.Fatalf("snapshot nodes = %#v, want future health stale with non-negative age", snap.Nodes)
	}
}

func TestControllerSnapshotMappingRejectsInvalidState(t *testing.T) {
	st := controllerState()
	st.HashSlots.Ranges = nil
	if _, err := SnapshotFromController(st); err == nil {
		t.Fatal("SnapshotFromController() error = nil, want invalid state")
	}
}

func TestControllerSnapshotMappingIncludesOpsMCPDesiredState(t *testing.T) {
	st := controllerState()
	st.OpsMCP = &controller.OpsMCPState{
		Enabled:                     true,
		OwnerNodeID:                 2,
		ProfileFenceUntilUnixMillis: 1710000030000,
		Credentials: []controller.OpsMCPCredential{{
			ID:                  "token-a",
			DigestSHA256:        strings.Repeat("a", 64),
			CreatedAtUnixMillis: 1710000001000,
		}},
	}

	snapshot, err := SnapshotFromController(st)
	if err != nil {
		t.Fatalf("SnapshotFromController() error = %v", err)
	}
	if snapshot.OpsMCP == nil || !snapshot.OpsMCP.Enabled || snapshot.OpsMCP.OwnerNodeID != 2 ||
		snapshot.OpsMCP.ProfileFenceUntilUnixMillis != 1710000030000 {
		t.Fatalf("OpsMCP = %#v, want enabled owner 2", snapshot.OpsMCP)
	}
	clone := snapshot.Clone()
	clone.OpsMCP.Credentials[0].ID = "changed"
	if snapshot.OpsMCP.Credentials[0].ID != "token-a" {
		t.Fatalf("Clone() aliased OpsMCP credentials: %#v", snapshot.OpsMCP.Credentials)
	}
}

func TestControllerAdapterReportsAreExplicitBestEffort(t *testing.T) {
	adapter := NewControllerAdapter(ControllerConfig{Source: &fakeStateSource{state: controllerState()}})
	if err := adapter.ReportNode(context.Background(), NodeReport{NodeID: 1}); err != nil {
		t.Fatalf("ReportNode() error = %v", err)
	}
	if err := adapter.ReportSlots(context.Background(), SlotRuntimeReport{NodeID: 1}); err != nil {
		t.Fatalf("ReportSlots() error = %v", err)
	}
}

func TestControllerAdapterPublishesAfterRefresh(t *testing.T) {
	source := &fakeStateSource{state: controllerState()}
	adapter := NewControllerAdapter(ControllerConfig{Source: source})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	next := controllerState()
	next.Revision = 8
	source.state = next
	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	select {
	case ev := <-adapter.Watch():
		if ev.Snapshot.Revision != 8 {
			t.Fatalf("event revision = %d, want 8", ev.Snapshot.Revision)
		}
	default:
		t.Fatal("missing refresh event")
	}
}

func controllerState() controller.ClusterState {
	return controller.ClusterState{
		SchemaVersion: controller.CurrentSchemaVersion,
		ClusterID:     "cluster-a",
		Revision:      7,
		Config:        controller.ClusterConfig{SlotCount: 1, HashSlotCount: 4, ReplicaCount: 3},
		Controllers:   []controller.ControllerVoter{{NodeID: 1, Addr: "127.0.0.1:1001", Role: controller.ControllerRoleVoter}},
		Nodes: []controller.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []controller.NodeRole{controller.NodeRoleControllerVoter, controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive, CapacityWeight: 1},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive, CapacityWeight: 1},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive, Status: controller.NodeStatusAlive, CapacityWeight: 1},
		},
		Slots:     []controller.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 2, PreferredLeader: 1}},
		HashSlots: controller.HashSlotTable{Version: controller.CurrentHashSlotTableVersion, SlotCount: 4, Ranges: []controller.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		Tasks: []controller.ReconcileTask{{
			TaskID:           "bootstrap-1",
			SlotID:           1,
			Kind:             controller.TaskKindBootstrap,
			Step:             controller.TaskStepCreateSlot,
			SourceNode:       4,
			TargetNode:       1,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: controller.TaskCompletionPolicyAllTargetPeers,
			ParticipantProgress: []controller.TaskParticipantProgress{
				{NodeID: 1, Status: controller.TaskParticipantStatusDone},
				{NodeID: 2, Status: controller.TaskParticipantStatusPending},
				{NodeID: 3, Status: controller.TaskParticipantStatusFailed, Attempt: 1, LastError: "open failed"},
			},
			ConfigEpoch: 2,
			Attempt:     1,
			Status:      controller.TaskStatusFailed,
			LastError:   "quorum missing",
		}},
	}
}

type fakeStateSource struct{ state controller.ClusterState }

func (s *fakeStateSource) Snapshot(context.Context) controller.ClusterState { return s.state }
