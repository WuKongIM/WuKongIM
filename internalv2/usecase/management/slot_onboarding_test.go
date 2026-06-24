package management

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestPlanNodeOnboardingSelectsBoundedSlotMoves(t *testing.T) {
	snap := nodeOnboardingSnapshot()
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snap}})

	plan, err := app.PlanNodeOnboarding(context.Background(), NodeOnboardingPlanRequest{TargetNodeID: 4, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("PlanNodeOnboarding() error = %v", err)
	}

	if plan.TargetNodeID != 4 || plan.StateRevision != 12 || plan.MaxSlotMoves != 1 {
		t.Fatalf("plan identity = %#v, want target 4 revision 12 max 1", plan)
	}
	if len(plan.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one bounded candidate", plan.Candidates)
	}
	candidate := plan.Candidates[0]
	if candidate.SlotID != 1 || candidate.SourceNodeID != 1 || candidate.TargetNodeID != 4 || candidate.ConfigEpoch != 7 {
		t.Fatalf("candidate = %#v, want slot 1 source 1 target 4 epoch 7", candidate)
	}
	if !sameUint64Slice(candidate.TargetPeers, []uint64{4, 2, 3}) {
		t.Fatalf("target peers = %v, want [4 2 3]", candidate.TargetPeers)
	}
}

func TestPlanNodeOnboardingRejectsNonActiveTarget(t *testing.T) {
	snap := nodeOnboardingSnapshot()
	snap.Nodes[3].JoinState = control.NodeJoinStateJoining
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snap}})

	_, err := app.PlanNodeOnboarding(context.Background(), NodeOnboardingPlanRequest{TargetNodeID: 4})

	if !errors.Is(err, ErrNodeOnboardingTargetNotActive) {
		t.Fatalf("PlanNodeOnboarding() error = %v, want %v", err, ErrNodeOnboardingTargetNotActive)
	}
}

func TestStartNodeOnboardingCreatesBoundedMoveTasks(t *testing.T) {
	writer := &fakeSlotReplicaMoveWriter{
		result: control.SlotReplicaMoveResult{Created: true},
	}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: nodeOnboardingSnapshot()},
		SlotReplicaMove: writer,
	})

	got, err := app.StartNodeOnboarding(context.Background(), NodeOnboardingStartRequest{TargetNodeID: 4, MaxSlotMoves: 2})
	if err != nil {
		t.Fatalf("StartNodeOnboarding() error = %v", err)
	}

	if got.TargetNodeID != 4 || got.StateRevision != 12 || got.Created != 2 || len(got.Results) != 2 {
		t.Fatalf("start response = %#v, want two created tasks at revision 12", got)
	}
	if len(writer.requests) != 2 {
		t.Fatalf("requests = %#v, want two bounded move requests", writer.requests)
	}
	first := writer.requests[0]
	if first.SlotID != 1 || first.SourceNode != 1 || first.TargetNode != 4 || first.ConfigEpoch != 7 || first.StateRevision != 12 {
		t.Fatalf("first request = %#v, want slot 1 source 1 target 4 epoch 7 revision 12", first)
	}
	if !sameUint64Slice(first.TargetPeers, []uint64{4, 2, 3}) {
		t.Fatalf("first target peers = %v, want [4 2 3]", first.TargetPeers)
	}
	if writer.requests[1].SlotID != 2 {
		t.Fatalf("second request = %#v, want slot 2", writer.requests[1])
	}
}

func TestNodeOnboardingStatusSummarizesReplicaMoveTasks(t *testing.T) {
	snap := nodeOnboardingSnapshot()
	snap.Tasks = []control.ReconcileTask{
		{TaskID: "pending", SlotID: 1, Kind: control.TaskKindSlotReplicaMove, TargetNode: 4, Status: control.TaskStatusPending},
		{TaskID: "running", SlotID: 2, Kind: control.TaskKindSlotReplicaMove, TargetNode: 4, Status: control.TaskStatusRunning},
		{TaskID: "failed", SlotID: 3, Kind: control.TaskKindSlotReplicaMove, TargetNode: 4, Status: control.TaskStatusFailed},
		{TaskID: "leader", SlotID: 4, Kind: control.TaskKindLeaderTransfer, TargetNode: 4, Status: control.TaskStatusPending},
		{TaskID: "other-target", SlotID: 5, Kind: control.TaskKindSlotReplicaMove, TargetNode: 2, Status: control.TaskStatusPending},
	}
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snap}})

	got, err := app.NodeOnboardingStatus(context.Background(), NodeOnboardingStatusRequest{TargetNodeID: 4})
	if err != nil {
		t.Fatalf("NodeOnboardingStatus() error = %v", err)
	}

	if got.TargetNodeID != 4 || got.StateRevision != 12 || len(got.Tasks) != 3 {
		t.Fatalf("status = %#v, want target 4 revision 12 and three replica-move tasks", got)
	}
	if got.Summary.TotalActive != 3 || got.Summary.Pending != 1 || got.Summary.Running != 1 || got.Summary.Failed != 1 {
		t.Fatalf("summary = %#v, want one pending/running/failed", got.Summary)
	}
}

func nodeOnboardingSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 12,
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, PreferredLeader: 2},
			{SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9, PreferredLeader: 3},
		},
	}
}

type fakeSlotReplicaMoveWriter struct {
	requests []control.SlotReplicaMoveRequest
	result   control.SlotReplicaMoveResult
	err      error
}

func (f *fakeSlotReplicaMoveWriter) RequestSlotReplicaMove(_ context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return control.SlotReplicaMoveResult{}, f.err
	}
	return f.result, nil
}
