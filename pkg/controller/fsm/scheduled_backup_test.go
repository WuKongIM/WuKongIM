package fsm

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/stretchr/testify/require"
)

func TestFSMReplacesScheduledBackupStateWithRevisionFence(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	expected := uint64(1)
	replacement := scheduledBackupState()

	result, err := sm.Apply(ctx, 2, command.Command{
		Kind:             command.KindReplaceScheduledBackupState,
		ExpectedRevision: &expected,
		ScheduledBackup:  &replacement,
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 2, AppliedRaftIndex: 2}, result)
	require.Equal(t, &replacement, sm.Snapshot(ctx).ScheduledBackup)

	stale := uint64(1)
	changed := replacement.Clone()
	changed.ActiveBackup.Slots[0].Status = state.BackupSlotStatusRunning
	result, err = sm.Apply(ctx, 3, command.Command{
		Kind:             command.KindReplaceScheduledBackupState,
		ExpectedRevision: &stale,
		ScheduledBackup:  &changed,
	})
	require.NoError(t, err)
	require.Equal(t, ApplyResult{
		Rejected:         true,
		Reason:           ReasonExpectedRevisionMismatch,
		Revision:         2,
		AppliedRaftIndex: 3,
	}, result)
	require.Equal(t, &replacement, sm.Snapshot(ctx).ScheduledBackup)
}

func TestFSMRejectsScheduledBackupWithIncompleteSlotTable(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	expected := uint64(1)
	replacement := scheduledBackupState()
	replacement.ActiveBackup.Slots = replacement.ActiveBackup.Slots[:state.BackupHashSlotCount-1]

	result, err := sm.Apply(ctx, 2, command.Command{
		Kind:             command.KindReplaceScheduledBackupState,
		ExpectedRevision: &expected,
		ScheduledBackup:  &replacement,
	})
	require.NoError(t, err)
	require.True(t, result.Rejected)
	require.Equal(t, ReasonInvalidState, result.Reason)
	require.Nil(t, sm.Snapshot(ctx).ScheduledBackup)
}

func TestFSMAcceptsAuxiliaryBackupTaskHistory(t *testing.T) {
	for _, kind := range []string{"verification", "retention"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			sm, _ := initializedStateMachine(t, 1)
			expected := uint64(1)
			replacement := scheduledBackupState()
			replacement.ActiveBackup = nil
			replacement.History = []state.BackupTaskRecord{{
				ID: kind + "-1", Kind: kind, Status: "succeeded",
				StartedUnixMillis:   1_800_000_000_000,
				CompletedUnixMillis: 1_800_000_001_000,
			}}

			result, err := sm.Apply(ctx, 2, command.Command{
				Kind:             command.KindReplaceScheduledBackupState,
				ExpectedRevision: &expected,
				ScheduledBackup:  &replacement,
			})
			require.NoError(t, err)
			require.False(t, result.Rejected, result.Reason)
			require.Equal(t, &replacement, sm.Snapshot(ctx).ScheduledBackup)
		})
	}
}

func scheduledBackupState() state.ScheduledBackupState {
	slots := make([]state.BackupSlotProgress, state.BackupHashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = state.BackupSlotProgress{
			HashSlot: uint16(hashSlot),
			Status:   state.BackupSlotStatusPending,
		}
	}
	return state.ScheduledBackupState{
		Revision: 1,
		Plan: &state.BackupPlan{
			Revision:                 1,
			Enabled:                  true,
			Store:                    state.BackupStoreConfig{Kind: state.BackupStoreKindFile},
			Cron:                     "0 1 * * *",
			TimeZone:                 "Asia/Shanghai",
			RetentionCount:           7,
			RateBytesPerSec:          50 * 1024 * 1024,
			WorkersPerNode:           1,
			MaxDurationMillis:        12 * 60 * 60 * 1000,
			ScheduleCursorUnixMillis: 1_800_000_000_000,
			CreatedUnixMillis:        1_800_000_000_000,
			UpdatedUnixMillis:        1_800_000_000_000,
		},
		ActiveBackup: &state.ScheduledBackupJob{
			ID:                  "backup-1",
			Trigger:             state.BackupTriggerInitial,
			Status:              state.BackupJobStatusPreparing,
			PlanRevision:        1,
			StartedAtUnixMillis: 1_800_000_000_000,
			DeadlineUnixMillis:  1_800_043_200_000,
			UpdatedUnixMillis:   1_800_000_000_000,
			Slots:               slots,
		},
		History: []state.BackupTaskRecord{},
	}
}
