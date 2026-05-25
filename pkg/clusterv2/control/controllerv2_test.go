package control

import (
	"context"
	"testing"

	cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

func TestControllerV2SnapshotMapping(t *testing.T) {
	snap, err := SnapshotFromControllerV2(controllerV2State())
	if err != nil {
		t.Fatalf("SnapshotFromControllerV2() error = %v", err)
	}
	if snap.Revision != 7 || snap.ControllerID != 1 {
		t.Fatalf("snapshot revision/controller = %d/%d, want 7/1", snap.Revision, snap.ControllerID)
	}
	if len(snap.Nodes) != 3 || snap.Nodes[0].Roles[0] != RoleController || snap.Nodes[0].Roles[1] != RoleData {
		t.Fatalf("nodes = %#v, want controller+data first node", snap.Nodes)
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
}

func TestControllerV2SnapshotMappingRejectsInvalidState(t *testing.T) {
	st := controllerV2State()
	st.HashSlots.Ranges = nil
	if _, err := SnapshotFromControllerV2(st); err == nil {
		t.Fatal("SnapshotFromControllerV2() error = nil, want invalid state")
	}
}

func TestControllerV2AdapterReportsAreExplicitBestEffort(t *testing.T) {
	adapter := NewControllerV2Adapter(ControllerV2Config{Source: &fakeStateSource{state: controllerV2State()}})
	if err := adapter.ReportNode(context.Background(), NodeReport{NodeID: 1}); err != nil {
		t.Fatalf("ReportNode() error = %v", err)
	}
	if err := adapter.ReportSlots(context.Background(), SlotRuntimeReport{NodeID: 1}); err != nil {
		t.Fatalf("ReportSlots() error = %v", err)
	}
}

func TestControllerV2AdapterPublishesAfterRefresh(t *testing.T) {
	source := &fakeStateSource{state: controllerV2State()}
	adapter := NewControllerV2Adapter(ControllerV2Config{Source: source})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	next := controllerV2State()
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

func controllerV2State() cv2state.ClusterState {
	return cv2state.ClusterState{
		SchemaVersion: cv2state.CurrentSchemaVersion,
		ClusterID:     "cluster-a",
		Revision:      7,
		Config:        cv2state.ClusterConfig{SlotCount: 1, HashSlotCount: 4, ReplicaCount: 3},
		Controllers:   []cv2state.ControllerVoter{{NodeID: 1, Addr: "127.0.0.1:1001", Role: cv2state.ControllerRoleVoter}},
		Nodes: []cv2state.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []cv2state.NodeRole{cv2state.NodeRoleControllerVoter, cv2state.NodeRoleData}, JoinState: cv2state.NodeJoinStateActive, Status: cv2state.NodeStatusAlive, CapacityWeight: 1},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []cv2state.NodeRole{cv2state.NodeRoleData}, JoinState: cv2state.NodeJoinStateActive, Status: cv2state.NodeStatusAlive, CapacityWeight: 1},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []cv2state.NodeRole{cv2state.NodeRoleData}, JoinState: cv2state.NodeJoinStateActive, Status: cv2state.NodeStatusAlive, CapacityWeight: 1},
		},
		Slots:     []cv2state.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 2, PreferredLeader: 1}},
		HashSlots: cv2state.HashSlotTable{Version: cv2state.CurrentHashSlotTableVersion, SlotCount: 4, Ranges: []cv2state.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		Tasks:     []cv2state.ReconcileTask{{TaskID: "bootstrap-1", SlotID: 1, Kind: cv2state.TaskKindBootstrap, Step: cv2state.TaskStepCreateSlot, TargetNode: 1, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 2, Status: cv2state.TaskStatusPending}},
	}
}

type fakeStateSource struct{ state cv2state.ClusterState }

func (s *fakeStateSource) Snapshot(context.Context) cv2state.ClusterState { return s.state }
