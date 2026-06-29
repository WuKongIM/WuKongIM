package fsm

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
)

func TestApplyTaskCommandsReturnTaskTransitions(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	issuedAt := time.Date(2026, 6, 29, 10, 0, 0, 0, time.FixedZone("cst", 8*60*60))
	cmd := bootstrapCommand(1, 1, []uint64{1, 2, 3})
	cmd.IssuedAt = issuedAt

	result, err := sm.ApplyBatch(ctx, []AppliedCommand{{Index: 2, Term: 9, Command: cmd}})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.True(t, result.Results[0].Changed)
	require.Len(t, result.Results[0].TaskTransitions, 1)
	transition := result.Results[0].TaskTransitions[0]
	require.Equal(t, uint64(2), transition.AppliedRaftIndex)
	require.Equal(t, uint64(9), transition.AppliedRaftTerm)
	require.Equal(t, command.KindUpsertSlotAssignmentAndTask, transition.CommandKind)
	require.Equal(t, issuedAt.UTC(), transition.IssuedAt)
	require.False(t, transition.BeforeValid)
	require.True(t, transition.AfterValid)
	require.Equal(t, "slot-1-bootstrap-1", transition.After.TaskID)
	require.Equal(t, state.TaskKindBootstrap, transition.After.Kind)
}

func TestApplyTaskResultTransitionsForFailAndComplete(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	fail, err := sm.ApplyBatch(ctx, []AppliedCommand{{
		Index: 3,
		Term:  10,
		Command: command.Command{
			Kind: command.KindFailTask,
			TaskResult: &command.TaskResult{
				TaskID:      "slot-1-bootstrap-1",
				SlotID:      1,
				TaskKind:    state.TaskKindBootstrap,
				ConfigEpoch: 1,
				Attempt:     0,
				Err:         "not ready",
			},
		},
	}})
	require.NoError(t, err)
	require.Len(t, fail.Results[0].TaskTransitions, 1)
	failed := fail.Results[0].TaskTransitions[0]
	require.True(t, failed.BeforeValid)
	require.True(t, failed.AfterValid)
	require.Equal(t, state.TaskStatusPending, failed.Before.Status)
	require.Equal(t, state.TaskStatusFailed, failed.After.Status)
	require.Equal(t, uint32(1), failed.After.Attempt)

	complete, err := sm.ApplyBatch(ctx, []AppliedCommand{{
		Index: 4,
		Term:  11,
		Command: command.Command{
			Kind: command.KindCompleteTask,
			TaskResult: &command.TaskResult{
				TaskID:      "slot-1-bootstrap-1",
				SlotID:      1,
				TaskKind:    state.TaskKindBootstrap,
				ConfigEpoch: 1,
				Attempt:     1,
			},
		},
	}})
	require.NoError(t, err)
	require.Len(t, complete.Results[0].TaskTransitions, 1)
	completed := complete.Results[0].TaskTransitions[0]
	require.Equal(t, uint64(4), completed.AppliedRaftIndex)
	require.Equal(t, uint64(11), completed.AppliedRaftTerm)
	require.Equal(t, command.KindCompleteTask, completed.CommandKind)
	require.True(t, completed.BeforeValid)
	require.False(t, completed.AfterValid)
	require.Equal(t, "slot-1-bootstrap-1", completed.Before.TaskID)
}

func TestApplyTaskProgressTransitionCarriesParticipantNode(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	result, err := sm.ApplyBatch(ctx, []AppliedCommand{{
		Index: 3,
		Term:  12,
		Command: command.Command{
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
		},
	}})

	require.NoError(t, err)
	require.Len(t, result.Results[0].TaskTransitions, 1)
	transition := result.Results[0].TaskTransitions[0]
	require.Equal(t, uint64(2), transition.ParticipantNode)
	require.True(t, transition.BeforeValid)
	require.True(t, transition.AfterValid)
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(transition.Before, 2))
	require.Equal(t, state.TaskParticipantStatusDone, participantStatus(transition.After, 2))
}

func TestApplyTaskNoopAndRejectDoNotReturnTransitions(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	noopResult, err := sm.ApplyBatch(ctx, []AppliedCommand{{
		Index:   3,
		Term:    13,
		Command: bootstrapCommand(1, 1, []uint64{3, 2, 1}),
	}})
	require.NoError(t, err)
	require.True(t, noopResult.Results[0].Noop)
	require.Empty(t, noopResult.Results[0].TaskTransitions)

	rejectCommand := bootstrapCommand(1, 2, []uint64{1, 2, 3})
	rejectCommand.Task.SlotID = 2
	rejectCommand.Task.TaskID = "slot-2-bootstrap-1"
	rejectResult, err := sm.ApplyBatch(ctx, []AppliedCommand{{
		Index:   4,
		Term:    14,
		Command: rejectCommand,
	}})
	require.NoError(t, err)
	require.True(t, rejectResult.Results[0].Rejected)
	require.Empty(t, rejectResult.Results[0].TaskTransitions)
}

func TestTaskTransitionsAreDeepCopied(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)

	result, err := sm.ApplyBatch(ctx, []AppliedCommand{{
		Index:   2,
		Term:    15,
		Command: bootstrapCommand(1, 1, []uint64{1, 2, 3}),
	}})
	require.NoError(t, err)
	require.Len(t, result.Results[0].TaskTransitions, 1)

	transition := result.Results[0].TaskTransitions[0]
	transition.After.TargetPeers[0] = 99
	transition.After.ParticipantProgress[0].NodeID = 99

	snap := sm.Snapshot(ctx)
	require.Equal(t, []uint64{1, 2, 3}, snap.Tasks[0].TargetPeers)
	require.Equal(t, uint64(1), snap.Tasks[0].ParticipantProgress[0].NodeID)
}
