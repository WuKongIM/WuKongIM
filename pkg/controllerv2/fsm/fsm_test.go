package fsm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	"github.com/stretchr/testify/require"
)

type countingStore struct {
	*statefile.Store
	saves atomic.Int32
}

func (s *countingStore) Save(ctx context.Context, st state.ClusterState) error {
	s.saves.Add(1)
	return s.Store.Save(ctx, st)
}

func TestApplyInitClusterStateCreatesRevisionAndHashSlots(t *testing.T) {
	ctx := context.Background()
	sm, store := newTestStateMachine(t)

	result, err := sm.Apply(ctx, 10, initCommand())
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 1, AppliedRaftIndex: 10}, result)

	snap := sm.Snapshot(ctx)
	require.Equal(t, "wk-fsm-test", snap.ClusterID)
	require.Equal(t, uint64(1), snap.Revision)
	require.Equal(t, uint64(10), snap.AppliedRaftIndex)
	require.Len(t, snap.HashSlots.Ranges, int(snap.Config.SlotCount))
	require.NoError(t, snap.Validate())

	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, snap.Revision, persisted.Revision)
	require.Equal(t, snap.AppliedRaftIndex, persisted.AppliedRaftIndex)
}

func TestApplyUpsertNodeNoopDoesNotIncrementRevisionButAdvancesAppliedIndex(t *testing.T) {
	ctx := context.Background()
	sm, store := initializedStateMachine(t, 1)
	node := baseNodes()[0]
	expected := uint64(1)

	result, err := sm.Apply(ctx, 2, command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &expected, Node: &node})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonNoChange, Revision: 1, AppliedRaftIndex: 2}, result)

	snap := sm.Snapshot(ctx)
	require.Equal(t, uint64(1), snap.Revision)
	require.Equal(t, uint64(2), snap.AppliedRaftIndex)
	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), persisted.AppliedRaftIndex)
}

func TestApplyReportNodeHealthPersistsWithoutLogicalRevisionBump(t *testing.T) {
	sm := newLoadedStateMachine(t)
	before := sm.Snapshot(context.Background())

	report := state.NodeHealthReport{
		NodeID:                  1,
		Status:                  state.NodeStatusAlive,
		RuntimeReady:            true,
		ObservedControlRevision: before.Revision,
		ObservedSlotRevision:    12,
		ReportSeq:               1,
		ReportedAtUnixMilli:     1710000000000,
	}
	result, err := sm.Apply(context.Background(), before.AppliedRaftIndex+1, command.Command{
		Kind:       command.KindReportNodeHealth,
		NodeHealth: &report,
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Updated || result.Changed || result.Revision != before.Revision {
		t.Fatalf("Apply() = %#v, want Updated without logical revision bump from %d", result, before.Revision)
	}
	after := sm.Snapshot(context.Background())
	if after.Revision != before.Revision {
		t.Fatalf("Revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if len(after.NodeHealthReports) != 1 || after.NodeHealthReports[0].AppliedRaftIndex != result.AppliedRaftIndex {
		t.Fatalf("NodeHealthReports = %#v, result = %#v", after.NodeHealthReports, result)
	}
}

func TestApplyReportNodeHealthDuplicateNoopsAndAdvancesAppliedIndex(t *testing.T) {
	sm := newLoadedStateMachine(t)
	before := sm.Snapshot(context.Background())
	report := state.NodeHealthReport{
		NodeID:                  1,
		Status:                  state.NodeStatusAlive,
		RuntimeReady:            true,
		ObservedControlRevision: before.Revision,
		ObservedSlotRevision:    12,
		ReportSeq:               1,
		ReportedAtUnixMilli:     1710000000000,
	}

	first, err := sm.Apply(context.Background(), before.AppliedRaftIndex+1, command.Command{
		Kind:       command.KindReportNodeHealth,
		NodeHealth: &report,
	})
	require.NoError(t, err)
	require.True(t, first.Updated)
	require.False(t, first.Changed)

	second, err := sm.Apply(context.Background(), first.AppliedRaftIndex+1, command.Command{
		Kind:       command.KindReportNodeHealth,
		NodeHealth: &report,
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{
		Noop:             true,
		Reason:           ReasonNoChange,
		Revision:         before.Revision,
		AppliedRaftIndex: first.AppliedRaftIndex + 1,
	}, second)

	after := sm.Snapshot(context.Background())
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, first.AppliedRaftIndex+1, after.AppliedRaftIndex)
	require.Len(t, after.NodeHealthReports, 1)
	require.Equal(t, first.AppliedRaftIndex, after.NodeHealthReports[0].AppliedRaftIndex)
}

func TestApplyReportNodeHealthRejectsUnknownNode(t *testing.T) {
	sm := newLoadedStateMachine(t)
	report := state.NodeHealthReport{NodeID: 99, Status: state.NodeStatusAlive, ReportedAtUnixMilli: 1710000000000}
	result, err := sm.Apply(context.Background(), 2, command.Command{Kind: command.KindReportNodeHealth, NodeHealth: &report})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Rejected || result.Reason != ReasonInvalidState {
		t.Fatalf("Apply() = %#v, want invalid state reject", result)
	}
}

func TestApplyBatchPersistsOnceAndReturnsPerEntryResults(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := &countingStore{Store: statefile.New(filepath.Join(dir, "cluster-state.json"))}
	sm, err := New(store)
	require.NoError(t, err)

	init := initCommand()
	node := baseNodes()[0]
	node.Name = "node-1-batched"
	expected := uint64(1)

	result, err := sm.ApplyBatch(ctx, []AppliedCommand{
		{Index: 10, Term: 1, Command: init},
		{Index: 11, Term: 1, Command: command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &expected, Node: &node}},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 2)
	require.Equal(t, uint64(1), result.Results[0].Revision)
	require.Equal(t, uint64(2), result.Results[1].Revision)
	require.Equal(t, int32(1), store.saves.Load())

	snap := sm.Snapshot(ctx)
	require.Equal(t, uint64(2), snap.Revision)
	require.Equal(t, uint64(11), snap.AppliedRaftIndex)
	var got state.Node
	for _, candidate := range snap.Nodes {
		if candidate.NodeID == 1 {
			got = candidate
			break
		}
	}
	require.Equal(t, "node-1-batched", got.Name)
}

func TestApplyAssignmentAndTaskWritesAtomically(t *testing.T) {
	ctx := context.Background()
	sm, store := initializedStateMachine(t, 1)
	cmd := bootstrapCommand(1, 1, []uint64{1, 2, 3})

	result, err := sm.Apply(ctx, 2, cmd)
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 2, AppliedRaftIndex: 2}, result)

	snap := sm.Snapshot(ctx)
	require.Len(t, snap.Slots, 1)
	require.Len(t, snap.Tasks, 1)
	require.Equal(t, snap.Slots[0].DesiredPeers, snap.Tasks[0].TargetPeers)
	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Len(t, persisted.Slots, 1)
	require.Len(t, persisted.Tasks, 1)
}

func TestApplyAssignmentAndTaskRejectsSlotMismatchBeforeMutation(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(2, 1, []uint64{1, 2, 3}))
	cmd := bootstrapCommand(1, 2, []uint64{1, 2, 3})
	cmd.Task.SlotID = 2
	cmd.Task.TaskID = "slot-2-bootstrap-1"

	result, err := sm.Apply(ctx, 3, cmd)
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskSlotMismatch, Revision: 2, AppliedRaftIndex: 3}, result)

	snap := sm.Snapshot(ctx)
	require.Len(t, snap.Slots, 1)
	require.Equal(t, uint32(2), snap.Slots[0].SlotID)
}

func TestApplyStaleAssignmentAndTaskRejectsSlotMismatchBeforeNoop(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	stale := bootstrapCommand(1, 1, []uint64{1, 2, 3})
	stale.Task.SlotID = 2
	stale.Task.TaskID = "slot-2-bootstrap-1"

	result, err := sm.Apply(ctx, 3, stale)
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskSlotMismatch, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyStaleBootstrapForAssignedSlotNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	stale := bootstrapCommand(1, 1, []uint64{3, 2, 1})

	result, err := sm.Apply(ctx, 3, stale)
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonStaleBootstrapObsolete, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyStaleBootstrapForMissingSlotRejects(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	stale := bootstrapCommand(2, 0, []uint64{1, 2, 3})

	result, err := sm.Apply(ctx, 2, stale)
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonStaleBootstrapMissingSlot, Revision: 1, AppliedRaftIndex: 2}, result)
	require.Empty(t, sm.Snapshot(ctx).Slots)
}

func TestApplyExpectedRevisionMismatchNonBootstrapRejects(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	node := baseNodes()[0]
	node.Name = "changed"
	expected := uint64(0)

	result, err := sm.Apply(ctx, 2, command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &expected, Node: &node})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonExpectedRevisionMismatch, Revision: 1, AppliedRaftIndex: 2}, result)
}

func TestApplyExpectedRevisionMismatchFailTaskRejectsWhenTaskExists(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	expected := uint64(1)

	result, err := sm.Apply(ctx, 3, command.Command{Kind: command.KindFailTask, ExpectedRevision: &expected, TaskResult: &command.TaskResult{TaskID: "slot-1-bootstrap-1", SlotID: 1, Err: "boom"}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonExpectedRevisionMismatch, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyInvalidPeersReturnsSemanticRejectAndAdvancesAppliedIndex(t *testing.T) {
	ctx := context.Background()
	sm, store := initializedStateMachine(t, 1)
	cmd := bootstrapCommand(1, 1, []uint64{1, 2, 99})

	result, err := sm.Apply(ctx, 2, cmd)
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonInvalidState, Revision: 1, AppliedRaftIndex: 2}, result)
	require.Empty(t, sm.Snapshot(ctx).Slots)
	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), persisted.AppliedRaftIndex)
}

func TestApplyInvalidCommandBeforeInitReportsRaftIndexWithoutPersisting(t *testing.T) {
	ctx := context.Background()
	sm, store := newTestStateMachine(t)

	result, err := sm.Apply(ctx, 7, command.Command{Kind: command.KindUpsertNode})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonInvalidCommand, Revision: 0, AppliedRaftIndex: 7}, result)
	require.Zero(t, sm.Snapshot(ctx))
	_, err = store.Load(ctx)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestResetClearsWarmStateForReplay(t *testing.T) {
	ctx := context.Background()
	sm, store := initializedStateMachine(t, 3)
	require.Equal(t, uint64(1), sm.Snapshot(ctx).Revision)

	sm.Reset()
	require.Zero(t, sm.Snapshot(ctx))

	result, err := sm.Apply(ctx, 3, initCommand())
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 1, AppliedRaftIndex: 3}, result)
	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), persisted.Revision)
	require.Equal(t, uint64(3), persisted.AppliedRaftIndex)
}

func TestApplyUsesIssuedAtForDeterministicSnapshots(t *testing.T) {
	ctx := context.Background()
	issuedAt := time.Date(2026, 5, 24, 20, 30, 0, 123, time.FixedZone("plus-eight", 8*60*60))
	init := initCommand()
	init.IssuedAt = issuedAt
	bootstrap := bootstrapCommand(1, 1, []uint64{1, 2, 3})
	bootstrap.IssuedAt = issuedAt.Add(time.Minute)

	sm1, _ := newTestStateMachine(t)
	applyOK(t, sm1, 1, init)
	time.Sleep(5 * time.Millisecond)
	applyOK(t, sm1, 2, bootstrap)
	snap1 := sm1.Snapshot(ctx)

	time.Sleep(5 * time.Millisecond)
	sm2, _ := newTestStateMachine(t)
	applyOK(t, sm2, 1, init)
	time.Sleep(5 * time.Millisecond)
	applyOK(t, sm2, 2, bootstrap)
	snap2 := sm2.Snapshot(ctx)

	require.Equal(t, issuedAt.Add(time.Minute).UTC(), snap1.UpdatedAt)
	require.Equal(t, snap1, snap2)
	require.NotEmpty(t, snap1.Checksum)
	require.Equal(t, snap1.Checksum, snap2.Checksum)
}

func TestApplyCompleteTaskRemovesTask(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	expected := uint64(2)

	result, err := sm.Apply(ctx, 3, command.Command{Kind: command.KindCompleteTask, ExpectedRevision: &expected, TaskResult: &command.TaskResult{TaskID: "slot-1-bootstrap-1", SlotID: 1, TaskKind: state.TaskKindBootstrap, ConfigEpoch: 1, Attempt: 0, FinishedAt: time.Now().UTC()}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 3, AppliedRaftIndex: 3}, result)
	require.Empty(t, sm.Snapshot(ctx).Tasks)
}

func TestApplyFailTaskKeepsFailedTaskWithBoundedError(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	longErr := strings.Repeat("界", 500)

	result, err := sm.Apply(ctx, 3, command.Command{Kind: command.KindFailTask, TaskResult: &command.TaskResult{TaskID: "slot-1-bootstrap-1", SlotID: 1, TaskKind: state.TaskKindBootstrap, ConfigEpoch: 1, Attempt: 0, Err: longErr}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 3, AppliedRaftIndex: 3}, result)

	task := sm.Snapshot(ctx).Tasks[0]
	require.Equal(t, state.TaskStatusFailed, task.Status)
	require.Equal(t, uint32(1), task.Attempt)
	require.LessOrEqual(t, len([]byte(task.LastError)), MaxTaskLastErrorBytes)
	require.True(t, utf8.ValidString(task.LastError))
	require.NotEmpty(t, task.LastError)
}

func TestApplyLeaderTransferTaskUpsertAndComplete(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, leaderTransferCommand(1, 1, 1, 2, []uint64{1, 2, 3}, 7))

	snap := sm.Snapshot(ctx)
	require.Len(t, snap.Slots, 1)
	require.Equal(t, uint64(2), snap.Slots[0].PreferredLeader)
	require.Len(t, snap.Tasks, 1)
	require.Equal(t, state.TaskKindLeaderTransfer, snap.Tasks[0].Kind)
	require.Equal(t, state.TaskStepTransferLeader, snap.Tasks[0].Step)
	require.Empty(t, snap.Tasks[0].ParticipantProgress)

	expected := snap.Revision
	result, err := sm.Apply(ctx, 3, command.Command{Kind: command.KindCompleteTask, ExpectedRevision: &expected, TaskResult: &command.TaskResult{TaskID: "slot-1-leader-transfer-7-r1", SlotID: 1, TaskKind: state.TaskKindLeaderTransfer, ConfigEpoch: 7, Attempt: 0, FinishedAt: time.Now().UTC()}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 3, AppliedRaftIndex: 3}, result)
	require.Empty(t, sm.Snapshot(ctx).Tasks)
}

func TestApplyLeaderTransferStaleEquivalentTaskNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, leaderTransferCommand(1, 1, 1, 2, []uint64{1, 2, 3}, 7))

	result, err := sm.Apply(ctx, 3, leaderTransferCommand(1, 0, 1, 2, []uint64{1, 2, 3}, 7))

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonNoChange, Revision: 2, AppliedRaftIndex: 3}, result)
	snap := sm.Snapshot(ctx)
	require.Len(t, snap.Tasks, 1)
	require.Equal(t, "slot-1-leader-transfer-7-r1", snap.Tasks[0].TaskID)
}

func TestApplyLeaderTransferStaleDifferentTaskRejects(t *testing.T) {
	tests := []struct {
		name string
		cmd  command.Command
	}{
		{
			name: "different target",
			cmd:  leaderTransferCommand(1, 0, 1, 3, []uint64{1, 2, 3}, 7),
		},
		{
			name: "different config epoch",
			cmd:  leaderTransferCommand(1, 0, 1, 2, []uint64{1, 2, 3}, 8),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			sm, _ := initializedStateMachine(t, 1)
			applyOK(t, sm, 2, leaderTransferCommand(1, 1, 1, 2, []uint64{1, 2, 3}, 7))

			result, err := sm.Apply(ctx, 3, tt.cmd)

			require.NoError(t, err)
			require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonExpectedRevisionMismatch, Revision: 2, AppliedRaftIndex: 3}, result)
		})
	}
}

func TestApplyLeaderTransferTaskFailKeepsActiveTask(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, leaderTransferCommand(1, 1, 1, 2, []uint64{1, 2, 3}, 7))

	result, err := sm.Apply(ctx, 3, command.Command{Kind: command.KindFailTask, TaskResult: &command.TaskResult{TaskID: "slot-1-leader-transfer-7-r1", SlotID: 1, TaskKind: state.TaskKindLeaderTransfer, ConfigEpoch: 7, Attempt: 0, Err: "not leader"}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 3, AppliedRaftIndex: 3}, result)

	task := sm.Snapshot(ctx).Tasks[0]
	require.Equal(t, state.TaskKindLeaderTransfer, task.Kind)
	require.Equal(t, state.TaskStatusFailed, task.Status)
	require.Equal(t, uint32(1), task.Attempt)
	require.Equal(t, "not leader", task.LastError)
	require.Empty(t, task.ParticipantProgress)
}

func TestApplyAdvanceSlotReplicaMovePhasePersistsFence(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.Step = state.TaskStepAddLearner
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &command.SlotReplicaMovePhaseAdvance{
			TaskID:              task.TaskID,
			SlotID:              1,
			ConfigEpoch:         7,
			Attempt:             0,
			ExpectedPhaseIndex:  0,
			NextStep:            state.TaskStepPromoteLearner,
			ObservedConfigIndex: 33,
			ObservedVoters:      []uint64{1, 2, 3},
			ObservedLearners:    []uint64{4},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 5, AppliedRaftIndex: 5}, result)
	got := sm.Snapshot(ctx).Tasks[0]
	require.Equal(t, state.TaskStepPromoteLearner, got.Step)
	require.Equal(t, uint32(1), got.PhaseIndex)
	require.Equal(t, uint64(33), got.ObservedConfigIndex)
	require.Equal(t, []uint64{1, 2, 3}, got.ObservedVoters)
	require.Equal(t, []uint64{4}, got.ObservedLearners)
}

func TestApplyAdvanceSlotReplicaMovePhaseRejectsStalePhase(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.PhaseIndex = 1
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &command.SlotReplicaMovePhaseAdvance{
			TaskID:             task.TaskID,
			SlotID:             1,
			ConfigEpoch:        7,
			Attempt:            0,
			ExpectedPhaseIndex: 0,
			NextStep:           state.TaskStepRemoveVoter,
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskPhaseMismatch, Revision: 4, AppliedRaftIndex: 5}, result)
	require.Equal(t, uint32(1), sm.Snapshot(ctx).Tasks[0].PhaseIndex)
}

func TestApplyAdvanceSlotReplicaMovePhaseRejectsIllegalStepJump(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &command.SlotReplicaMovePhaseAdvance{
			TaskID:              task.TaskID,
			SlotID:              1,
			ConfigEpoch:         7,
			Attempt:             0,
			ExpectedPhaseIndex:  0,
			NextStep:            state.TaskStepCommitAssignment,
			ObservedConfigIndex: 55,
			ObservedVoters:      []uint64{2, 3, 4},
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskStepMismatch, Revision: 4, AppliedRaftIndex: 5}, result)
	require.Equal(t, state.TaskStepOpenLearner, sm.Snapshot(ctx).Tasks[0].Step)
}

func TestApplyAdvanceSlotReplicaMovePhaseRejectsMissingLearnerObservation(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.Step = state.TaskStepAddLearner
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &command.SlotReplicaMovePhaseAdvance{
			TaskID:              task.TaskID,
			SlotID:              1,
			ConfigEpoch:         7,
			Attempt:             0,
			ExpectedPhaseIndex:  0,
			NextStep:            state.TaskStepPromoteLearner,
			ObservedConfigIndex: 55,
			ObservedVoters:      []uint64{1, 2, 3},
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskObservedLearnersMismatch, Revision: 4, AppliedRaftIndex: 5}, result)
	require.Equal(t, state.TaskStepAddLearner, sm.Snapshot(ctx).Tasks[0].Step)
}

func TestApplyAdvanceSlotReplicaMovePhaseRejectsFences(t *testing.T) {
	tests := []struct {
		name  string
		phase command.SlotReplicaMovePhaseAdvance
		want  ApplyResult
	}{
		{
			name: "missing task",
			phase: command.SlotReplicaMovePhaseAdvance{
				TaskID:              "missing",
				SlotID:              1,
				ConfigEpoch:         7,
				Attempt:             0,
				ExpectedPhaseIndex:  0,
				NextStep:            state.TaskStepAddLearner,
				ObservedConfigIndex: 33,
			},
			want: ApplyResult{Noop: true, Reason: ReasonTaskMissing, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "slot mismatch",
			phase: command.SlotReplicaMovePhaseAdvance{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              2,
				ConfigEpoch:         7,
				Attempt:             0,
				ExpectedPhaseIndex:  0,
				NextStep:            state.TaskStepAddLearner,
				ObservedConfigIndex: 33,
			},
			want: ApplyResult{Rejected: true, Reason: ReasonTaskSlotMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "epoch mismatch",
			phase: command.SlotReplicaMovePhaseAdvance{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              1,
				ConfigEpoch:         8,
				Attempt:             0,
				ExpectedPhaseIndex:  0,
				NextStep:            state.TaskStepAddLearner,
				ObservedConfigIndex: 33,
			},
			want: ApplyResult{Noop: true, Reason: ReasonTaskEpochMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "attempt mismatch",
			phase: command.SlotReplicaMovePhaseAdvance{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              1,
				ConfigEpoch:         7,
				Attempt:             1,
				ExpectedPhaseIndex:  0,
				NextStep:            state.TaskStepAddLearner,
				ObservedConfigIndex: 33,
			},
			want: ApplyResult{Noop: true, Reason: ReasonTaskAttemptMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			sm := slotReplicaMoveStateMachine(t)
			task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
			applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

			result, err := sm.Apply(ctx, 5, command.Command{
				Kind:                 command.KindAdvanceSlotReplicaMovePhase,
				SlotReplicaMovePhase: &tt.phase,
			})

			require.NoError(t, err)
			require.Equal(t, tt.want, result)
			require.Equal(t, state.TaskStepOpenLearner, sm.Snapshot(ctx).Tasks[0].Step)
		})
	}
}

func TestApplyCommitSlotReplicaMoveUpdatesAssignmentAndRemovesTask(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.Step = state.TaskStepCommitAssignment
	task.PhaseIndex = 4
	task.ObservedConfigIndex = 55
	task.ObservedVoters = []uint64{4, 2, 3}
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindCommitSlotReplicaMove,
		SlotReplicaMoveCommit: &command.SlotReplicaMoveCommit{
			TaskID:              task.TaskID,
			SlotID:              1,
			ConfigEpoch:         7,
			Attempt:             0,
			ObservedConfigIndex: 55,
			ObservedVoters:      []uint64{4, 2, 3},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 5, AppliedRaftIndex: 5}, result)
	snap := sm.Snapshot(ctx)
	require.Empty(t, snap.Tasks)
	require.Equal(t, []uint64{2, 3, 4}, snap.Slots[0].DesiredPeers)
	require.Equal(t, uint64(8), snap.Slots[0].ConfigEpoch)
}

func TestApplyCommitSlotReplicaMoveRejectsObservedVoterMismatch(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.Step = state.TaskStepCommitAssignment
	task.ObservedConfigIndex = 55
	task.ObservedVoters = []uint64{4, 2, 9}
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindCommitSlotReplicaMove,
		SlotReplicaMoveCommit: &command.SlotReplicaMoveCommit{
			TaskID:              task.TaskID,
			SlotID:              1,
			ConfigEpoch:         7,
			Attempt:             0,
			ObservedConfigIndex: 55,
			ObservedVoters:      []uint64{4, 2, 3},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskObservedVotersMismatch, Revision: 4, AppliedRaftIndex: 5}, result)
	snap := sm.Snapshot(ctx)
	require.Len(t, snap.Tasks, 1)
	require.Equal(t, []uint64{1, 2, 3}, snap.Slots[0].DesiredPeers)
	require.Equal(t, uint64(7), snap.Slots[0].ConfigEpoch)
}

func TestApplyCommitSlotReplicaMoveRejectsMissingCommitObservation(t *testing.T) {
	ctx := context.Background()
	sm := slotReplicaMoveStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.Step = state.TaskStepCommitAssignment
	task.ObservedConfigIndex = 55
	task.ObservedVoters = []uint64{4, 2, 3}
	applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

	result, err := sm.Apply(ctx, 5, command.Command{
		Kind: command.KindCommitSlotReplicaMove,
		SlotReplicaMoveCommit: &command.SlotReplicaMoveCommit{
			TaskID:      task.TaskID,
			SlotID:      1,
			ConfigEpoch: 7,
			Attempt:     0,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskObservedConfigMissing, Revision: 4, AppliedRaftIndex: 5}, result)
	snap := sm.Snapshot(ctx)
	require.Len(t, snap.Tasks, 1)
	require.Equal(t, []uint64{1, 2, 3}, snap.Slots[0].DesiredPeers)
}

func TestApplyCommitSlotReplicaMoveRejectsFences(t *testing.T) {
	tests := []struct {
		name       string
		mutateTask func(*state.ReconcileTask)
		commit     command.SlotReplicaMoveCommit
		want       ApplyResult
	}{
		{
			name: "slot mismatch",
			commit: command.SlotReplicaMoveCommit{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              2,
				ConfigEpoch:         7,
				Attempt:             0,
				ObservedConfigIndex: 55,
				ObservedVoters:      []uint64{4, 2, 3},
			},
			want: ApplyResult{Rejected: true, Reason: ReasonTaskSlotMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "epoch mismatch",
			commit: command.SlotReplicaMoveCommit{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              1,
				ConfigEpoch:         8,
				Attempt:             0,
				ObservedConfigIndex: 55,
				ObservedVoters:      []uint64{4, 2, 3},
			},
			want: ApplyResult{Noop: true, Reason: ReasonTaskEpochMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "attempt mismatch",
			commit: command.SlotReplicaMoveCommit{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              1,
				ConfigEpoch:         7,
				Attempt:             1,
				ObservedConfigIndex: 55,
				ObservedVoters:      []uint64{4, 2, 3},
			},
			want: ApplyResult{Noop: true, Reason: ReasonTaskAttemptMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "wrong step",
			mutateTask: func(task *state.ReconcileTask) {
				task.Step = state.TaskStepRemoveVoter
			},
			commit: command.SlotReplicaMoveCommit{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              1,
				ConfigEpoch:         7,
				Attempt:             0,
				ObservedConfigIndex: 55,
				ObservedVoters:      []uint64{4, 2, 3},
			},
			want: ApplyResult{Rejected: true, Reason: ReasonTaskStepMismatch, Revision: 4, AppliedRaftIndex: 5},
		},
		{
			name: "missing observed config",
			mutateTask: func(task *state.ReconcileTask) {
				task.ObservedConfigIndex = 0
			},
			commit: command.SlotReplicaMoveCommit{
				TaskID:              "slot-1-replica-move-1-to-4-r9",
				SlotID:              1,
				ConfigEpoch:         7,
				Attempt:             0,
				ObservedConfigIndex: 55,
				ObservedVoters:      []uint64{4, 2, 3},
			},
			want: ApplyResult{Rejected: true, Reason: ReasonTaskObservedConfigMissing, Revision: 4, AppliedRaftIndex: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			sm := slotReplicaMoveStateMachine(t)
			task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
			task.Step = state.TaskStepCommitAssignment
			task.ObservedConfigIndex = 55
			task.ObservedVoters = []uint64{4, 2, 3}
			if tt.mutateTask != nil {
				tt.mutateTask(&task)
			}
			applyOK(t, sm, 4, command.Command{Kind: command.KindUpsertSlotReplicaMoveTask, Task: &task})

			result, err := sm.Apply(ctx, 5, command.Command{
				Kind:                  command.KindCommitSlotReplicaMove,
				SlotReplicaMoveCommit: &tt.commit,
			})

			require.NoError(t, err)
			require.Equal(t, tt.want, result)
			require.Len(t, sm.Snapshot(ctx).Tasks, 1)
		})
	}
}

func TestApplyFailTaskMissingTaskNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)

	result, err := sm.Apply(ctx, 2, command.Command{Kind: command.KindFailTask, TaskResult: &command.TaskResult{TaskID: "missing-task", SlotID: 1, Err: "obsolete"}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonTaskMissing, Revision: 1, AppliedRaftIndex: 2}, result)
}

func TestApplyFailTaskRejectsMissingResultOrSlotMismatch(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	result, err := sm.Apply(ctx, 2, command.Command{Kind: command.KindFailTask})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonInvalidTaskResult, Revision: 1, AppliedRaftIndex: 2}, result)

	sm, _ = initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	result, err = sm.Apply(ctx, 3, command.Command{Kind: command.KindFailTask, TaskResult: &command.TaskResult{TaskID: "slot-1-bootstrap-1", SlotID: 2, TaskKind: state.TaskKindBootstrap, ConfigEpoch: 1, Attempt: 0, Err: "wrong slot"}})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskSlotMismatch, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyReportTaskProgressUpdatesOnlyParticipant(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusDone,
			FinishedAt:         time.Now().UTC(),
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 3, AppliedRaftIndex: 3}, result)
	task := sm.Snapshot(ctx).Tasks[0]
	require.Equal(t, state.TaskParticipantStatusDone, participantStatus(task, 2))
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(task, 1))
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(task, 3))
}

func TestApplyReportTaskProgressRejectsUnexpectedParticipant(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:            "slot-1-bootstrap-1",
			SlotID:            1,
			TaskKind:          state.TaskKindBootstrap,
			ConfigEpoch:       1,
			TaskAttempt:       0,
			ParticipantNodeID: 9,
			Status:            state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskParticipantUnexpected, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyReportTaskProgressStaleParticipantAttemptNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	applyOK(t, sm, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusFailed,
			Err:                "first failure",
		},
	})

	result, err := sm.Apply(ctx, 4, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonTaskParticipantAttemptStale, Revision: 3, AppliedRaftIndex: 4}, result)
	require.Equal(t, state.TaskParticipantStatusFailed, participantStatus(sm.Snapshot(ctx).Tasks[0], 2))
}

func TestApplyReportTaskProgressAcceptsAdvancedParticipantAttemptAfterFailure(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	applyOK(t, sm, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusFailed,
			Err:                "first failure",
		},
	})
	failedTask := sm.Snapshot(ctx).Tasks[0]
	var failedProgress state.TaskParticipantProgress
	for _, item := range failedTask.ParticipantProgress {
		if item.NodeID == 2 {
			failedProgress = item
			break
		}
	}
	require.Equal(t, state.TaskParticipantStatusFailed, failedProgress.Status)
	require.Equal(t, uint32(1), failedProgress.Attempt)

	result, err := sm.Apply(ctx, 4, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 1,
			Status:             state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 4, AppliedRaftIndex: 4}, result)
	task := sm.Snapshot(ctx).Tasks[0]
	var got state.TaskParticipantProgress
	for _, item := range task.ParticipantProgress {
		if item.NodeID == 2 {
			got = item
			break
		}
	}
	require.Equal(t, state.TaskParticipantStatusDone, got.Status)
	require.Equal(t, uint32(1), got.Attempt)
	require.Empty(t, got.LastError)
}

func TestApplyReportTaskProgressRejectsExpectedRevisionMismatchWhenTaskExists(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	expected := uint64(1)

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind:             command.KindReportTaskProgress,
		ExpectedRevision: &expected,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonExpectedRevisionMismatch, Revision: 2, AppliedRaftIndex: 3}, result)
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(sm.Snapshot(ctx).Tasks[0], 2))
}

func TestApplyCompleteTaskStaleAttemptNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	applyOK(t, sm, 3, command.Command{Kind: command.KindFailTask, TaskResult: &command.TaskResult{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		TaskKind:    state.TaskKindBootstrap,
		ConfigEpoch: 1,
		Attempt:     0,
		Err:         "global failure",
	}})

	result, err := sm.Apply(ctx, 4, command.Command{Kind: command.KindCompleteTask, TaskResult: &command.TaskResult{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		TaskKind:    state.TaskKindBootstrap,
		ConfigEpoch: 1,
		Attempt:     0,
	}})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonTaskAttemptMismatch, Revision: 3, AppliedRaftIndex: 4}, result)
	require.Len(t, sm.Snapshot(ctx).Tasks, 1)
}

func TestApplySaveFailureDoesNotPublishState(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	fail := false
	store := statefile.New(filepath.Join(t.TempDir(), "cluster-state.json"), statefile.WithAfterTempWriteHook(func() error {
		if fail {
			return boom
		}
		return nil
	}))
	sm, err := New(store)
	require.NoError(t, err)
	applyOK(t, sm, 1, initCommand())
	before := sm.Snapshot(ctx)
	fail = true
	node := before.Nodes[0]
	node.Name = "changed"

	result, err := sm.Apply(ctx, 2, command.Command{Kind: command.KindUpsertNode, Node: &node})
	require.ErrorIs(t, err, boom)
	require.Equal(t, ApplyResult{Changed: true, Revision: 2, AppliedRaftIndex: 2}, result)
	require.True(t, sm.IsDegraded())
	require.Equal(t, before, sm.Snapshot(ctx))

	persisted, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, before.Revision, persisted.Revision)
	require.Equal(t, before.AppliedRaftIndex, persisted.AppliedRaftIndex)
}

func TestSnapshotReturnsDeepCopy(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)

	snap := sm.Snapshot(ctx)
	snap.Nodes[0].Name = "mutated"
	snap.HashSlots.Ranges[0].SlotID = 99

	again := sm.Snapshot(ctx)
	require.NotEqual(t, "mutated", again.Nodes[0].Name)
	require.Equal(t, uint32(1), again.HashSlots.Ranges[0].SlotID)
}

func newTestStateMachine(t *testing.T) (*StateMachine, *statefile.Store) {
	t.Helper()
	store := statefile.New(filepath.Join(t.TempDir(), "cluster-state.json"))
	sm, err := New(store)
	require.NoError(t, err)
	return sm, store
}

func initializedStateMachine(t *testing.T, raftIndex uint64) (*StateMachine, *statefile.Store) {
	t.Helper()
	sm, store := newTestStateMachine(t)
	applyOK(t, sm, raftIndex, initCommand())
	return sm, store
}

func newLoadedStateMachine(t *testing.T) *StateMachine {
	t.Helper()
	sm, _ := initializedStateMachine(t, 1)
	return sm
}

func applyOK(t *testing.T, sm *StateMachine, raftIndex uint64, cmd command.Command) ApplyResult {
	t.Helper()
	result, err := sm.Apply(context.Background(), raftIndex, cmd)
	require.NoError(t, err)
	require.False(t, result.Rejected, result.Reason)
	return result
}

func initCommand() command.Command {
	return command.Command{
		Kind: command.KindInitClusterState,
		Init: &command.InitClusterState{
			ClusterID:   "wk-fsm-test",
			Config:      testConfig(),
			Controllers: baseControllers(),
			Nodes:       baseNodes(),
		},
	}
}

func bootstrapCommand(slotID uint32, expectedRevision uint64, peers []uint64) command.Command {
	assignment := state.SlotAssignment{SlotID: slotID, DesiredPeers: peers, ConfigEpoch: 1, PreferredLeader: peers[0]}
	return command.Command{
		Kind:             command.KindUpsertSlotAssignmentAndTask,
		ExpectedRevision: &expectedRevision,
		Assignment:       &assignment,
		Task: &state.ReconcileTask{
			TaskID:              "slot-" + string(rune('0'+slotID)) + "-bootstrap-1",
			SlotID:              slotID,
			Kind:                state.TaskKindBootstrap,
			Step:                state.TaskStepCreateSlot,
			TargetNode:          peers[0],
			TargetPeers:         peers,
			CompletionPolicy:    state.TaskCompletionPolicyAllTargetPeers,
			ParticipantProgress: participantProgress(peers),
			ConfigEpoch:         1,
			Status:              state.TaskStatusPending,
		},
	}
}

func leaderTransferCommand(slotID uint32, expectedRevision uint64, sourceNode uint64, targetNode uint64, peers []uint64, configEpoch uint64) command.Command {
	assignment := state.SlotAssignment{SlotID: slotID, DesiredPeers: peers, ConfigEpoch: configEpoch, PreferredLeader: targetNode}
	return command.Command{
		Kind:             command.KindUpsertSlotAssignmentAndTask,
		ExpectedRevision: &expectedRevision,
		Assignment:       &assignment,
		Task: &state.ReconcileTask{
			TaskID:           fmt.Sprintf("slot-%d-leader-transfer-%d-r%d", slotID, configEpoch, expectedRevision),
			SlotID:           slotID,
			Kind:             state.TaskKindLeaderTransfer,
			Step:             state.TaskStepTransferLeader,
			SourceNode:       sourceNode,
			TargetNode:       targetNode,
			TargetPeers:      peers,
			CompletionPolicy: state.TaskCompletionPolicySingleObserver,
			ConfigEpoch:      configEpoch,
			Status:           state.TaskStatusPending,
		},
	}
}

func slotReplicaMoveStateMachine(t *testing.T) *StateMachine {
	t.Helper()
	sm, _ := initializedStateMachine(t, 1)
	task := state.ReconcileTask{
		TaskID:              "slot-1-bootstrap-7",
		SlotID:              1,
		Kind:                state.TaskKindBootstrap,
		Step:                state.TaskStepCreateSlot,
		TargetNode:          1,
		TargetPeers:         []uint64{1, 2, 3},
		CompletionPolicy:    state.TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: participantProgress([]uint64{1, 2, 3}),
		ConfigEpoch:         7,
		Status:              state.TaskStatusPending,
	}
	assignment := state.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1}
	applyOK(t, sm, 2, command.Command{Kind: command.KindUpsertSlotAssignmentAndTask, Assignment: &assignment, Task: &task})
	applyOK(t, sm, 3, command.Command{Kind: command.KindCompleteTask, TaskResult: &command.TaskResult{
		TaskID:      task.TaskID,
		SlotID:      task.SlotID,
		TaskKind:    task.Kind,
		ConfigEpoch: task.ConfigEpoch,
		Attempt:     task.Attempt,
	}})
	return sm
}

func stagedSlotReplicaMoveTask(taskID string) state.ReconcileTask {
	return state.ReconcileTask{
		TaskID:           taskID,
		SlotID:           1,
		Kind:             state.TaskKindSlotReplicaMove,
		Step:             state.TaskStepOpenLearner,
		SourceNode:       1,
		TargetNode:       4,
		TargetPeers:      []uint64{4, 2, 3},
		CompletionPolicy: state.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      7,
		Status:           state.TaskStatusPending,
	}
}

func participantProgress(peers []uint64) []state.TaskParticipantProgress {
	out := make([]state.TaskParticipantProgress, 0, len(peers))
	for _, peerID := range peers {
		out = append(out, state.TaskParticipantProgress{NodeID: peerID, Status: state.TaskParticipantStatusPending})
	}
	return out
}

func participantStatus(task state.ReconcileTask, nodeID uint64) state.TaskParticipantStatus {
	for _, progress := range task.ParticipantProgress {
		if progress.NodeID == nodeID {
			return progress.Status
		}
	}
	return ""
}

func testConfig() state.ClusterConfig {
	return state.ClusterConfig{SlotCount: 4, HashSlotCount: 16, ReplicaCount: 3, DefaultCapacityWeight: 10}
}

func baseControllers() []state.ControllerVoter {
	return []state.ControllerVoter{
		{NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter},
		{NodeID: 2, Addr: "n2", Role: state.ControllerRoleVoter},
	}
}

func baseNodes() []state.Node {
	return []state.Node{
		{NodeID: 1, Name: "n1", Addr: "n1", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
		{NodeID: 2, Name: "n2", Addr: "n2", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
		{NodeID: 3, Name: "n3", Addr: "n3", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
		{NodeID: 4, Name: "n4", Addr: "n4", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
	}
}
