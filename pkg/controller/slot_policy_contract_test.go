package controller

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

func TestSlotReplicaMovePolicyRequiresExactEpochTopologyAndActiveTarget(t *testing.T) {
	assignment := state.SlotAssignment{SlotID: 9, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7}
	clusterState := state.ClusterState{
		Revision: 12,
		Nodes: []state.Node{
			{NodeID: 1, Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive},
			{NodeID: 2, Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive},
			{NodeID: 3, Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive},
			{NodeID: 4, Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive},
		},
		Slots: []state.SlotAssignment{assignment},
	}
	valid := SlotReplicaMoveRequest{
		SlotID: 9, SourceNode: 3, TargetNode: 4, TargetPeers: []uint64{4, 1, 2}, ConfigEpoch: 7, StateRevision: 12,
	}
	if err := validateSlotReplicaMoveRequest(clusterState, assignment, valid); err != nil {
		t.Fatalf("valid move error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*state.ClusterState, *state.SlotAssignment, *SlotReplicaMoveRequest)
		want   error
	}{
		{name: "missing identity", mutate: func(_ *state.ClusterState, _ *state.SlotAssignment, req *SlotReplicaMoveRequest) { req.SlotID = 0 }},
		{name: "stale epoch", mutate: func(_ *state.ClusterState, _ *state.SlotAssignment, req *SlotReplicaMoveRequest) { req.ConfigEpoch++ }},
		{name: "source not peer", mutate: func(_ *state.ClusterState, _ *state.SlotAssignment, req *SlotReplicaMoveRequest) { req.SourceNode = 8 }},
		{name: "target already peer", mutate: func(_ *state.ClusterState, _ *state.SlotAssignment, req *SlotReplicaMoveRequest) { req.TargetNode = 2 }},
		{name: "target not active", mutate: func(st *state.ClusterState, _ *state.SlotAssignment, _ *SlotReplicaMoveRequest) {
			st.Nodes[3].JoinState = state.NodeJoinStateJoining
		}},
		{name: "wrong target peers", mutate: func(_ *state.ClusterState, _ *state.SlotAssignment, req *SlotReplicaMoveRequest) {
			req.TargetPeers = []uint64{1, 2, 5}
		}},
		{name: "active task", mutate: func(st *state.ClusterState, _ *state.SlotAssignment, _ *SlotReplicaMoveRequest) {
			st.Tasks = []state.ReconcileTask{{TaskID: "existing", SlotID: 9}}
		}, want: ErrSlotActiveTaskConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := clusterState.Clone()
			a := assignment
			req := valid
			tt.mutate(&st, &a, &req)
			err := validateSlotReplicaMoveRequest(st, a, req)
			if err == nil {
				t.Fatal("validateSlotReplicaMoveRequest() error = nil")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}

	found, ok := findSlotReplicaMoveAssignment(clusterState, 9)
	if !ok || found.ConfigEpoch != 7 {
		t.Fatalf("find assignment = %#v, %v", found, ok)
	}
	if _, ok := findSlotReplicaMoveAssignment(clusterState, 10); ok {
		t.Fatal("missing assignment was found")
	}
	replaced := replaceSlotReplicaMovePeer(assignment.DesiredPeers, 3, 4)
	if !sameUint64Set(replaced, []uint64{1, 2, 4}) || assignment.DesiredPeers[2] != 3 {
		t.Fatalf("replacement = %#v, original = %#v", replaced, assignment.DesiredPeers)
	}
}

func TestLeaderTransferIdempotencyMatchesOnlyEquivalentPendingIntent(t *testing.T) {
	assignment := state.SlotAssignment{SlotID: 9, DesiredPeers: []uint64{3, 1, 2}, ConfigEpoch: 7, PreferredLeader: 2}
	task := state.ReconcileTask{
		TaskID: "new", SlotID: 9, Kind: state.TaskKindLeaderTransfer, Step: state.TaskStepTransferLeader,
		SourceNode: 1, TargetNode: 2, TargetPeers: []uint64{2, 3, 1}, ConfigEpoch: 7,
		CompletionPolicy: state.TaskCompletionPolicySingleObserver, Status: state.TaskStatusPending,
	}
	existing := task
	existing.TaskID = "existing"
	existing.TargetPeers = []uint64{1, 2, 3}
	clusterState := state.ClusterState{Slots: []state.SlotAssignment{assignment}, Tasks: []state.ReconcileTask{existing}}

	got, ok := equivalentActiveLeaderTransferTask(clusterState, assignment, task)
	if !ok || got.TaskID != "existing" {
		t.Fatalf("equivalent task = %#v, %v", got, ok)
	}
	changedAssignment := assignment
	changedAssignment.PreferredLeader = 3
	if _, ok := equivalentActiveLeaderTransferTask(clusterState, changedAssignment, task); ok {
		t.Fatal("different preferred leader matched existing task")
	}
	clusterState.Tasks[0].Status = state.TaskStatusRunning
	if _, ok := equivalentActiveLeaderTransferTask(clusterState, assignment, task); ok {
		t.Fatal("running task matched pending idempotency intent")
	}
	if sameUint64Set([]uint64{1, 1}, []uint64{1, 2}) {
		t.Fatal("different peer multisets compared equal")
	}
}
