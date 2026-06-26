package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
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

func TestStartNodeOnboardingCreatesBoundedMoveTask(t *testing.T) {
	writer := &fakeSlotReplicaMoveWriter{
		result: control.SlotReplicaMoveResult{Created: true},
	}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: nodeOnboardingSnapshot()},
		SlotReplicaMove: writer,
	})

	got, err := app.StartNodeOnboarding(context.Background(), NodeOnboardingStartRequest{TargetNodeID: 4, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("StartNodeOnboarding() error = %v", err)
	}

	if got.TargetNodeID != 4 || got.StateRevision != 12 || got.Created != 1 || len(got.Results) != 1 {
		t.Fatalf("start response = %#v, want one created task at revision 12", got)
	}
	if len(writer.requests) != 1 {
		t.Fatalf("requests = %#v, want one bounded move request", writer.requests)
	}
	first := writer.requests[0]
	if first.SlotID != 1 || first.SourceNode != 1 || first.TargetNode != 4 || first.ConfigEpoch != 7 || first.StateRevision != 12 {
		t.Fatalf("first request = %#v, want slot 1 source 1 target 4 epoch 7 revision 12", first)
	}
	if !sameUint64Slice(first.TargetPeers, []uint64{4, 2, 3}) {
		t.Fatalf("first target peers = %v, want [4 2 3]", first.TargetPeers)
	}
}

func TestStartNodeOnboardingReplansBetweenRealControlWrites(t *testing.T) {
	rt := startNodeOnboardingControlRuntime(t)
	activateNodeOnboardingTarget(t, rt, 4)
	app := New(Options{
		Cluster:         controlRuntimeSnapshotReader{runtime: rt},
		SlotReplicaMove: rt,
	})

	got, err := app.StartNodeOnboarding(context.Background(), NodeOnboardingStartRequest{TargetNodeID: 4, MaxSlotMoves: 2})
	if err != nil {
		t.Fatalf("StartNodeOnboarding() error = %v", err)
	}

	if got.Created != 2 || len(got.Results) != 2 {
		t.Fatalf("start response = %#v, want two created tasks", got)
	}
	snap := waitForNodeOnboardingSnapshot(t, rt, func(snap control.Snapshot) bool {
		count := 0
		for _, task := range snap.Tasks {
			if task.Kind == control.TaskKindSlotReplicaMove && task.TargetNode == 4 {
				count++
			}
		}
		return count == 2
	})
	if len(snap.Tasks) != 2 {
		t.Fatalf("tasks = %#v, want exactly two active replica-move tasks", snap.Tasks)
	}
}

func TestStartNodeOnboardingReturnsPartialSuccessWhenConflictPersists(t *testing.T) {
	writer := &sequencedSlotReplicaMoveWriter{
		results: []control.SlotReplicaMoveResult{{Created: true}},
		errs: []error{
			nil,
			cv2raft.ProposalRejectedError{Reason: "expected_revision_mismatch"},
			cv2raft.ProposalRejectedError{Reason: "expected_revision_mismatch"},
			cv2raft.ProposalRejectedError{Reason: "expected_revision_mismatch"},
			cv2raft.ProposalRejectedError{Reason: "expected_revision_mismatch"},
			cv2raft.ProposalRejectedError{Reason: "expected_revision_mismatch"},
		},
	}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: nodeOnboardingSnapshot()},
		SlotReplicaMove: writer,
	})

	got, err := app.StartNodeOnboarding(context.Background(), NodeOnboardingStartRequest{TargetNodeID: 4, MaxSlotMoves: 2})
	if err != nil {
		t.Fatalf("StartNodeOnboarding() error = %v", err)
	}

	if got.Created != 1 || len(got.Results) != 1 {
		t.Fatalf("start response = %#v, want one partial created result", got)
	}
	if len(got.Skipped) == 0 || got.Skipped[0].Reason != "control_conflict" {
		t.Fatalf("skipped = %#v, want control_conflict", got.Skipped)
	}
}

func TestStartNodeOnboardingDoesNotRetryNonRevisionProposalRejection(t *testing.T) {
	wantErr := cv2raft.ProposalRejectedError{Reason: "invalid_state"}
	writer := &sequencedSlotReplicaMoveWriter{errs: []error{wantErr}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: nodeOnboardingSnapshot()},
		SlotReplicaMove: writer,
	})

	_, err := app.StartNodeOnboarding(context.Background(), NodeOnboardingStartRequest{TargetNodeID: 4, MaxSlotMoves: 1})
	if !errors.Is(err, cv2raft.ErrProposalRejected) {
		t.Fatalf("StartNodeOnboarding() error = %v, want proposal rejected", err)
	}
	if errors.Is(err, ErrNodeOnboardingConflict) {
		t.Fatalf("StartNodeOnboarding() error = %v, should not be onboarding conflict", err)
	}
	if len(writer.requests) != 1 {
		t.Fatalf("requests = %d, want no retry for invalid_state", len(writer.requests))
	}
}

func TestStartNodeOnboardingMapsActiveTaskConflictToControlConflict(t *testing.T) {
	writer := &fakeSlotReplicaMoveWriter{err: cv2.ErrSlotActiveTaskConflict}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: nodeOnboardingSnapshot()},
		SlotReplicaMove: writer,
	})

	_, err := app.StartNodeOnboarding(context.Background(), NodeOnboardingStartRequest{TargetNodeID: 4, MaxSlotMoves: 1})
	if !errors.Is(err, ErrNodeOnboardingConflict) {
		t.Fatalf("StartNodeOnboarding() error = %v, want %v", err, ErrNodeOnboardingConflict)
	}
	if len(writer.requests) < 2 {
		t.Fatalf("requests = %d, want active-task conflict to retry before bounded conflict", len(writer.requests))
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

type controlRuntimeSnapshotReader struct {
	runtime *control.Runtime
}

func (r controlRuntimeSnapshotReader) NodeID() uint64 { return 1 }

func (r controlRuntimeSnapshotReader) LocalControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	return r.runtime.LocalSnapshot(ctx)
}

func startNodeOnboardingControlRuntime(t *testing.T) *control.Runtime {
	t.Helper()

	rt, err := control.NewRuntime(control.RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "node-onboarding-real-writer",
		Role:             control.RuntimeRoleVoter,
		Voters:           []control.RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 2,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	waitForNodeOnboardingSnapshot(t, rt, func(snap control.Snapshot) bool {
		return snap.Revision > 0 && len(snap.Slots) == 2
	})
	return rt
}

func activateNodeOnboardingTarget(t *testing.T, rt *control.Runtime, nodeID uint64) {
	t.Helper()

	if _, err := rt.JoinNode(context.Background(), control.JoinNodeRequest{
		NodeID:         nodeID,
		Addr:           "127.0.0.1:10004",
		Roles:          []control.Role{control.RoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := rt.ActivateNode(context.Background(), control.ActivateNodeRequest{NodeID: nodeID}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	snap := waitForNodeOnboardingSnapshot(t, rt, func(snap control.Snapshot) bool {
		for _, node := range snap.Nodes {
			if node.NodeID == nodeID && node.JoinState == control.NodeJoinStateActive {
				return true
			}
		}
		return false
	})
	for _, task := range snap.Tasks {
		if err := rt.CompleteTask(context.Background(), control.TaskResult{
			TaskID:      task.TaskID,
			SlotID:      task.SlotID,
			TaskKind:    task.Kind,
			ConfigEpoch: task.ConfigEpoch,
			Attempt:     task.Attempt,
			FinishedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CompleteTask(%s) error = %v", task.TaskID, err)
		}
	}
	waitForNodeOnboardingSnapshot(t, rt, func(snap control.Snapshot) bool {
		return len(snap.Tasks) == 0
	})
}

func waitForNodeOnboardingSnapshot(t *testing.T, rt *control.Runtime, ready func(control.Snapshot) bool) control.Snapshot {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var last control.Snapshot
	for time.Now().Before(deadline) {
		snap, err := rt.LocalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("LocalSnapshot() error = %v", err)
		}
		last = snap
		if ready(snap) {
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control snapshot did not become ready: %#v", last)
	return control.Snapshot{}
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

type sequencedSlotReplicaMoveWriter struct {
	requests []control.SlotReplicaMoveRequest
	results  []control.SlotReplicaMoveResult
	errs     []error
}

func (f *sequencedSlotReplicaMoveWriter) RequestSlotReplicaMove(_ context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	f.requests = append(f.requests, req)
	idx := len(f.requests) - 1
	if idx < len(f.errs) && f.errs[idx] != nil {
		return control.SlotReplicaMoveResult{}, f.errs[idx]
	}
	if idx < len(f.results) {
		return f.results[idx], nil
	}
	return control.SlotReplicaMoveResult{}, nil
}
