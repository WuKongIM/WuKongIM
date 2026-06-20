package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestPlanSlotLeaderTransfersSelectsCandidatesAndSkipsInStableOrder(t *testing.T) {
	generatedAt := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
				3: {SlotID: 3, LeaderID: 1, CurrentVoters: []uint64{1, 3}},
			},
		},
		Now: func() time.Time { return generatedAt },
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		SlotIDs:      []uint32{3, 1, 2, 1},
		MaxTasks:     8,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if !got.GeneratedAt.Equal(generatedAt) || got.StateRevision != 22 || got.SourceNodeID != 1 || got.TargetPolicy != SlotLeaderTransferTargetPolicyLeastLeaders || got.MaxTasks != 8 {
		t.Fatalf("plan identity = %#v, want generated time/revision/source/policy/max tasks", got)
	}
	if got.PlanID == "" {
		t.Fatalf("PlanID is empty, want deterministic ID")
	}
	if got.Summary != (SlotLeaderTransferBatchPlanSummary{Scanned: 3, Candidates: 1, Skipped: 2, ExistingTasks: 0, WouldCreate: 1}) {
		t.Fatalf("summary = %#v, want scanned 3 candidate 1 skipped 2 would_create 1 existing 0", got.Summary)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one candidate", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.SlotID != 1 || candidate.SourceNodeID != 1 || candidate.TargetNodeID != 2 || candidate.PreferredLeader != 1 || candidate.ActualLeader != 1 || candidate.ConfigEpoch != 7 || candidate.Action != SlotLeaderTransferBatchActionCreate {
		t.Fatalf("candidate = %#v, want slot 1 create candidate from source 1 to target 2", candidate)
	}
	if !sameUint64Slice(candidate.DesiredPeers, []uint64{1, 2, 3}) || !sameUint64Slice(candidate.CurrentVoters, []uint64{1, 2, 3}) {
		t.Fatalf("candidate peers/voters = %#v, want desired/current [1 2 3]", candidate)
	}
	if len(got.Skipped) != 2 {
		t.Fatalf("skipped = %#v, want two skipped rows", got.Skipped)
	}
	if got.Skipped[0].SlotID != 2 || got.Skipped[0].Reason != SlotLeaderTransferBatchSkipSourceNotLeaderOrPreferred {
		t.Fatalf("first skip = %#v, want slot 2 source_not_leader_or_preferred", got.Skipped[0])
	}
	if got.Skipped[1].SlotID != 3 || got.Skipped[1].Reason != SlotLeaderTransferBatchSkipTargetNotCurrentVoter {
		t.Fatalf("second skip = %#v, want slot 3 target_not_current_voter", got.Skipped[1])
	}

	reordered, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		SlotIDs:      []uint32{1, 2, 3},
		MaxTasks:     8,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})
	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers(reordered) error = %v", err)
	}
	if reordered.PlanID != got.PlanID {
		t.Fatalf("PlanID = %q, want deterministic %q for normalized equivalent request", reordered.PlanID, got.PlanID)
	}
}

func TestPlanSlotLeaderTransfersCorrectsPreferredLeaderAfterActualMoved(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = []control.SlotAssignment{{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		ConfigEpoch:     7,
		PreferredLeader: 1,
	}}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, TargetNodeID: 2})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one preferred-leader correction candidate", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.ActualLeader != 3 || candidate.PreferredLeader != 1 || candidate.TargetNodeID != 2 || candidate.Action != SlotLeaderTransferBatchActionCreate {
		t.Fatalf("candidate = %#v, want actual leader 3 target 2 create", candidate)
	}
}

func TestPlanSlotLeaderTransfersCorrectsPreferredLeaderWhenActualAlreadyTarget(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = []control.SlotAssignment{{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		ConfigEpoch:     7,
		PreferredLeader: 1,
	}}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		MaxTasks:     4,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 1 || len(got.Skipped) != 0 {
		t.Fatalf("plan = %#v, want one preferred correction candidate and no already_on_target skip", got)
	}
	candidate := got.Candidates[0]
	if candidate.SlotID != 1 || candidate.ActualLeader != 2 || candidate.TargetNodeID != 2 || candidate.Action != SlotLeaderTransferBatchActionCreate {
		t.Fatalf("candidate = %#v, want actual leader 2 target 2 create", candidate)
	}
	if got.Summary.WouldCreate != 1 || got.Summary.Skipped != 0 {
		t.Fatalf("summary = %#v, want would_create 1 skipped 0", got.Summary)
	}
}

func TestPlanSlotLeaderTransfersReportsExistingMatchingTasks(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Tasks = []control.ReconcileTask{
		batchLeaderTransferTask("existing-transfer", 1, 1, 2, []uint64{1, 2, 3}, 7),
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				3: {SlotID: 3, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, TargetNodeID: 2, MaxTasks: 8})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if got.Summary.ExistingTasks != 1 || got.Summary.WouldCreate != 2 || got.Summary.Candidates != 3 {
		t.Fatalf("summary = %#v, want existing 1 would_create 2 candidates 3", got.Summary)
	}
	if len(got.Candidates) != 3 {
		t.Fatalf("candidates = %#v, want three candidates", got.Candidates)
	}
	first := got.Candidates[0]
	if first.SlotID != 1 || first.Action != SlotLeaderTransferBatchActionExisting || first.ExistingTaskID != "existing-transfer" {
		t.Fatalf("first candidate = %#v, want existing matching task", first)
	}
}

func TestPlanSlotLeaderTransfersReportsExistingMatchingTaskAlreadyOnTarget(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = snapshot.Slots[:1]
	snapshot.Tasks = []control.ReconcileTask{
		batchLeaderTransferTask("existing-transfer", 1, 1, 2, []uint64{1, 2, 3}, 7),
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, TargetNodeID: 2, MaxTasks: 8})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if got.Summary.ExistingTasks != 1 || got.Summary.WouldCreate != 0 || len(got.Candidates) != 1 || len(got.Skipped) != 0 {
		t.Fatalf("plan = %#v, want one existing candidate and no already_on_target skip", got)
	}
	if got.Candidates[0].Action != SlotLeaderTransferBatchActionExisting || got.Candidates[0].ExistingTaskID != "existing-transfer" {
		t.Fatalf("candidate = %#v, want existing matching task", got.Candidates[0])
	}
}

func TestPlanSlotLeaderTransfersLeastLeadersBalancesProjectedCounts(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = snapshot.Slots[:2]
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want two candidates", got.Candidates)
	}
	if got.Candidates[0].TargetNodeID != 2 || got.Candidates[1].TargetNodeID != 3 {
		t.Fatalf("targets = [%d %d], want balanced targets [2 3]", got.Candidates[0].TargetNodeID, got.Candidates[1].TargetNodeID)
	}
}

func TestPlanSlotLeaderTransfersLeastLeadersUsesFullObservedDistribution(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = snapshot.Slots[:2]
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one source-led candidate", got.Candidates)
	}
	if got.Candidates[0].SlotID != 1 || got.Candidates[0].TargetNodeID != 3 {
		t.Fatalf("candidate = %#v, want slot 1 target 3 because later slot already counts node 2", got.Candidates[0])
	}
}

func TestPlanSlotLeaderTransfersPreferredCorrectionDoesNotDecrementActualLeader(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = snapshot.Slots[:2]
	snapshot.Slots[1].PreferredLeader = 1
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want preferred correction plus source-led candidate", got.Candidates)
	}
	if got.Candidates[0].SlotID != 1 || got.Candidates[0].ActualLeader != 3 || got.Candidates[0].TargetNodeID != 2 {
		t.Fatalf("first candidate = %#v, want preferred-only correction from actual 3 to target 2", got.Candidates[0])
	}
	if got.Candidates[1].SlotID != 2 || got.Candidates[1].TargetNodeID != 2 {
		t.Fatalf("second candidate = %#v, want target 2 when actual leader 3 was not decremented", got.Candidates[1])
	}
}

func TestPlanSlotLeaderTransfersPreferredCorrectionDoesNotDoubleCountActualTarget(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = []control.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
		{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
		{SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 3},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				3: {SlotID: 3, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want preferred correction plus source-led candidate", got.Candidates)
	}
	if got.Candidates[0].SlotID != 1 || got.Candidates[0].ActualLeader != 2 || got.Candidates[0].TargetNodeID != 2 {
		t.Fatalf("first candidate = %#v, want preferred-only correction already on target 2", got.Candidates[0])
	}
	if got.Candidates[1].SlotID != 2 || got.Candidates[1].TargetNodeID != 2 {
		t.Fatalf("second candidate = %#v, want target 2 without double-counting slot 1 actual target", got.Candidates[1])
	}
}

func TestPlanSlotLeaderTransfersValidatesRequestAndUnavailablePorts(t *testing.T) {
	if _, err := New(Options{}).PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("zero source error = %v, want invalid argument", err)
	}
	if _, err := New(Options{}).PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, MaxTasks: MaxSlotLeaderTransferBatchMaxTasks + 1}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("too-large max error = %v, want invalid argument", err)
	}
	if _, err := New(Options{}).PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, TargetPolicy: "random"}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown policy error = %v, want invalid argument", err)
	}
	if _, err := New(Options{}).PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1}); !errors.Is(err, ErrSlotLeaderTransferUnavailable) {
		t.Fatalf("missing cluster error = %v, want %v", err, ErrSlotLeaderTransferUnavailable)
	}
	if _, err := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()}}).PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1}); !errors.Is(err, ErrSlotRuntimeStatusUnavailable) {
		t.Fatalf("missing runtime error = %v, want %v", err, ErrSlotRuntimeStatusUnavailable)
	}

	var nilApp *App
	if _, err := nilApp.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1}); !errors.Is(err, ErrSlotLeaderTransferUnavailable) {
		t.Fatalf("nil app error = %v, want %v", err, ErrSlotLeaderTransferUnavailable)
	}
}

func batchLeaderTransferSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 22,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "n2", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "n3", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 2},
			{SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
		},
	}
}

func batchLeaderTransferTask(taskID string, slotID uint32, sourceNode, targetNode uint64, peers []uint64, epoch uint64) control.ReconcileTask {
	return control.ReconcileTask{
		TaskID:           taskID,
		SlotID:           slotID,
		Kind:             control.TaskKindLeaderTransfer,
		Step:             control.TaskStepTransferLeader,
		SourceNode:       sourceNode,
		TargetNode:       targetNode,
		TargetPeers:      append([]uint64(nil), peers...),
		CompletionPolicy: control.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      epoch,
		Status:           control.TaskStatusPending,
	}
}
