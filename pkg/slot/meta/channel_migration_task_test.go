package meta

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMigrationTaskCreateAndGet(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-create", "channel-create")

	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, task, got)

	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, task.ChannelID, task.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task, active)

	list, err := shard.ListChannelMigrationTasks(ctx)
	require.NoError(t, err)
	require.Equal(t, []ChannelMigrationTask{task}, list)
}

func TestChannelMigrationTaskRejectsSecondActiveTaskForChannel(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	first := testChannelMigrationTask("task-active-1", "channel-active")
	second := testChannelMigrationTask("task-active-2", "channel-active")

	require.NoError(t, shard.CreateChannelMigrationTask(ctx, first))
	err := shard.CreateChannelMigrationTask(ctx, second)
	require.ErrorIs(t, err, ErrAlreadyExists)

	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, first.ChannelID, first.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first.TaskID, active.TaskID)
}

func TestChannelMigrationTaskRejectsLeaderTransferDesiredLeaderDifferentFromTarget(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-leader-desired-mismatch", "channel-leader-desired-mismatch")
	task.Kind = ChannelMigrationKindLeaderTransfer
	task.TargetNode = 2
	task.DesiredLeader = 3

	err := shard.CreateChannelMigrationTask(ctx, task)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestChannelMigrationTaskClaimOwnerLeaseUsesExpectedOwnerLease(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-claim", "channel-claim")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	claim := channelMigrationTaskClaimRequest(task, 3, 1750000005000, 1750000001000)
	require.NoError(t, shard.ClaimChannelMigrationTask(ctx, claim))

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, claim.OwnerNodeID, got.OwnerNodeID)
	require.Equal(t, claim.OwnerLeaseUntilMS, got.OwnerLeaseUntilMS)
	require.Equal(t, ChannelMigrationStatusRunning, got.Status)

	stale := channelMigrationTaskClaimRequest(task, 4, 1750000006000, 1750000002000)
	err = shard.ClaimChannelMigrationTask(ctx, stale)
	require.ErrorIs(t, err, ErrStaleMeta)

	got, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, claim.OwnerNodeID, got.OwnerNodeID)
	require.Equal(t, claim.OwnerLeaseUntilMS, got.OwnerLeaseUntilMS)
}

func TestWriteBatchClaimChannelMigrationTaskRevalidatesOwnerLeaseAtCommit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	task := testChannelMigrationTask("task-claim-race", "channel-claim-race")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	first := db.NewWriteBatch()
	defer first.Close()
	second := db.NewWriteBatch()
	defer second.Close()
	require.NoError(t, first.ClaimChannelMigrationTask(7, channelMigrationTaskClaimRequest(task, 3, 1750000005000, 1750000001000)))
	require.NoError(t, second.ClaimChannelMigrationTask(7, channelMigrationTaskClaimRequest(task, 4, 1750000006000, 1750000002000)))

	require.NoError(t, first.Commit())
	require.ErrorIs(t, second.Commit(), ErrStaleMeta)

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, uint64(3), got.OwnerNodeID)
	require.Equal(t, int64(1750000005000), got.OwnerLeaseUntilMS)
}

func TestWriteBatchCreateChannelMigrationTaskWithRuntimeMetaCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	task := testChannelMigrationTask("task-batch-create", "channel-batch-create")
	meta := testRuntimeMeta(task.ChannelID, task.ChannelType)

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.CreateChannelMigrationTask(7, task))
	require.NoError(t, wb.UpsertChannelRuntimeMeta(7, meta))
	require.NoError(t, wb.Commit())

	gotTask, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, task, gotTask)
	gotMeta, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	require.NoError(t, err)
	require.Equal(t, normalizeChannelRuntimeMeta(meta), gotMeta)
}

func TestWriteBatchCreateChannelMigrationTaskRejectsSameBatchActiveDuplicate(t *testing.T) {
	db := openTestDB(t)
	first := testChannelMigrationTask("task-batch-duplicate-1", "channel-batch-duplicate")
	second := testChannelMigrationTask("task-batch-duplicate-2", "channel-batch-duplicate")

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.CreateChannelMigrationTask(7, first))
	require.ErrorIs(t, wb.CreateChannelMigrationTask(7, second), ErrAlreadyExists)
}

func TestWriteBatchCreateChannelMigrationTaskRevalidatesActiveDuplicateAtCommit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	first := testChannelMigrationTask("task-batch-race-1", "channel-batch-race")
	second := testChannelMigrationTask("task-batch-race-2", "channel-batch-race")

	firstBatch := db.NewWriteBatch()
	defer firstBatch.Close()
	secondBatch := db.NewWriteBatch()
	defer secondBatch.Close()
	require.NoError(t, firstBatch.CreateChannelMigrationTask(7, first))
	require.NoError(t, secondBatch.CreateChannelMigrationTask(7, second))

	require.NoError(t, firstBatch.Commit())
	require.ErrorIs(t, secondBatch.Commit(), ErrAlreadyExists)

	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, first.ChannelID, first.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first.TaskID, active.TaskID)
	_, err = shard.GetChannelMigrationTask(ctx, second.ChannelID, second.ChannelType, second.TaskID)
	requireChannelMigrationTaskNotFound(t, err)
}

func TestWriteBatchCreateChannelMigrationTaskRevalidatesPrimaryDuplicateAtCommit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	first := testChannelMigrationTask("task-batch-primary-race", "channel-batch-primary-race")
	second := first
	second.SourceNode = 3
	second.TargetNode = 4
	second.DesiredLeader = 3
	second.BaseChannelEpoch = 30
	second.BaseLeaderEpoch = 40
	second.UpdatedAtMS = 1750000001000

	firstBatch := db.NewWriteBatch()
	defer firstBatch.Close()
	secondBatch := db.NewWriteBatch()
	defer secondBatch.Close()
	require.NoError(t, firstBatch.CreateChannelMigrationTask(7, first))
	require.NoError(t, secondBatch.CreateChannelMigrationTask(7, second))

	require.NoError(t, firstBatch.Commit())
	require.ErrorIs(t, secondBatch.Commit(), ErrAlreadyExists)

	got, err := shard.GetChannelMigrationTask(ctx, first.ChannelID, first.ChannelType, first.TaskID)
	require.NoError(t, err)
	require.Equal(t, first, got)
}

func TestChannelMigrationTaskGetReturnsCorruptValueForMalformedWorkflow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	task := testChannelMigrationTask("task-corrupt-workflow", "channel-corrupt-workflow")
	task.Phase = ChannelMigrationPhaseWarmCatchUp
	key := encodeChannelMigrationTaskPrimaryKey(7, task.ChannelID, task.ChannelType, task.TaskID, channelMigrationTaskPrimaryFamilyID)
	value := encodeChannelMigrationTaskFamilyValue(task, key)

	func() {
		db.mu.Lock()
		defer db.mu.Unlock()
		batch := db.db.NewBatch()
		defer batch.Close()
		require.NoError(t, batch.Set(key, value, nil))
		require.NoError(t, batch.Commit(nil))
	}()

	_, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestWriteBatchChannelMigrationTaskTerminalTransitionClearsActiveIndexForSameBatchCreate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	active := testChannelMigrationTask("task-batch-terminal-1", "channel-batch-terminal")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, active))

	completed := active
	completed.Status = ChannelMigrationStatusCompleted
	completed.Phase = ChannelMigrationPhaseVerifyMembership
	completed.CompletedAtMS = 1750000010000
	completed.UpdatedAtMS = 1750000010000
	next := testChannelMigrationTask("task-batch-terminal-2", "channel-batch-terminal")
	next.CreatedAtMS = 1750000011000
	next.UpdatedAtMS = 1750000011000

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.AdvanceChannelMigrationTask(7, channelMigrationTaskAdvanceRequest(active, completed)))
	require.NoError(t, wb.CreateChannelMigrationTask(7, next))
	require.NoError(t, wb.Commit())

	got, ok, err := shard.GetActiveChannelMigrationTask(ctx, next.ChannelID, next.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, next.TaskID, got.TaskID)
}

func TestWriteBatchChannelMigrationTaskStaleTerminalTransitionDoesNotClearNewActiveIndex(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	active := testChannelMigrationTask("task-batch-stale-terminal-1", "channel-batch-stale-terminal")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, active))

	completed := active
	completed.Status = ChannelMigrationStatusCompleted
	completed.Phase = ChannelMigrationPhaseVerifyMembership
	completed.CompletedAtMS = 1750000010000
	completed.UpdatedAtMS = 1750000010000
	next := testChannelMigrationTask("task-batch-stale-terminal-2", "channel-batch-stale-terminal")
	next.CreatedAtMS = 1750000011000
	next.UpdatedAtMS = 1750000011000

	staleBatch := db.NewWriteBatch()
	defer staleBatch.Close()
	require.NoError(t, staleBatch.AdvanceChannelMigrationTask(7, channelMigrationTaskAdvanceRequest(active, completed)))

	newActiveBatch := db.NewWriteBatch()
	defer newActiveBatch.Close()
	require.NoError(t, newActiveBatch.AdvanceChannelMigrationTask(7, channelMigrationTaskAdvanceRequest(active, completed)))
	require.NoError(t, newActiveBatch.CreateChannelMigrationTask(7, next))
	require.NoError(t, newActiveBatch.Commit())

	require.NoError(t, staleBatch.Commit())

	got, ok, err := shard.GetActiveChannelMigrationTask(ctx, next.ChannelID, next.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, next.TaskID, got.TaskID)
}

func TestChannelMigrationTaskValidationRejectsWorkflowInconsistentPhase(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "pending_warm_catch_up",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-pending-warm", "channel-invalid-pending-warm")
				task.Phase = ChannelMigrationPhaseWarmCatchUp
				return task
			}(),
		},
		{
			name: "blocked_validate",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocked-validate", "channel-invalid-blocked-validate")
				task.Status = ChannelMigrationStatusBlocked
				task.Phase = ChannelMigrationPhaseValidate
				task.BlockerCode = ChannelMigrationBlockerNeedsSnapshotBootstrap
				task.BlockerMessage = "target requires a channel snapshot before catch-up"
				return task
			}(),
		},
		{
			name: "leader_transfer_add_learner",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-lt-add", "channel-invalid-lt-add")
				task.Kind = ChannelMigrationKindLeaderTransfer
				task.DesiredLeader = task.TargetNode
				task.Phase = ChannelMigrationPhaseAddLearner
				return task
			}(),
		},
		{
			name: "leader_transfer_promote_and_remove",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-lt-promote", "channel-invalid-lt-promote")
				task.Kind = ChannelMigrationKindLeaderTransfer
				task.DesiredLeader = task.TargetNode
				task.Phase = ChannelMigrationPhasePromoteAndRemove
				return task
			}(),
		},
		{
			name: "replica_replace_commit_leader_meta_without_embedded_transfer",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-rr-commit", "channel-invalid-rr-commit")
				task.Phase = ChannelMigrationPhaseCommitLeaderMeta
				return task
			}(),
		},
		{
			name: "completed_validate",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-completed", "channel-invalid-completed")
				task.Status = ChannelMigrationStatusCompleted
				task.Phase = ChannelMigrationPhaseValidate
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				return task
			}(),
		},
		{
			name: "embedded_replica_replace_completed_verify_new_leader",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-completed-embedded-leader", "channel-invalid-completed-embedded-leader")
				task.Status = ChannelMigrationStatusCompleted
				task.Phase = ChannelMigrationPhaseVerifyNewLeader
				task.EmbeddedLeaderTransfer = true
				task.EmbeddedDesiredLeader = task.DesiredLeader
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateChannelMigrationTask(tt.task), ErrInvalidArgument)
		})
	}
}

func TestChannelMigrationTaskValidationRejectsInvalidTimestamps(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "updated_before_created",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-updated-before-created", "channel-invalid-updated-before-created")
				task.UpdatedAtMS = task.CreatedAtMS - 1
				return task
			}(),
		},
		{
			name: "completed_before_created",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-completed-before-created", "channel-invalid-completed-before-created")
				task.Status = ChannelMigrationStatusCompleted
				task.Phase = ChannelMigrationPhaseVerifyMembership
				task.UpdatedAtMS = task.CreatedAtMS + 100
				task.CompletedAtMS = task.CreatedAtMS - 1
				return task
			}(),
		},
		{
			name: "completed_after_updated",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-completed-after-updated", "channel-invalid-completed-after-updated")
				task.Status = ChannelMigrationStatusCompleted
				task.Phase = ChannelMigrationPhaseVerifyMembership
				task.UpdatedAtMS = task.CreatedAtMS + 100
				task.CompletedAtMS = task.UpdatedAtMS + 1
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateChannelMigrationTask(tt.task), ErrInvalidArgument)
		})
	}
}

func TestChannelMigrationTaskValidationRejectsPartialFenceState(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "version_without_token",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-fence-version", "channel-invalid-fence-version")
				task.FenceVersion = 1
				return task
			}(),
		},
		{
			name: "until_without_token",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-fence-until", "channel-invalid-fence-until")
				task.FenceUntilMS = 1750000010000
				return task
			}(),
		},
		{
			name: "token_without_version",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-fence-token-version", "channel-invalid-fence-token-version")
				task.FenceToken = "fence-token"
				task.FenceUntilMS = 1750000010000
				return task
			}(),
		},
		{
			name: "token_without_until",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-fence-token-until", "channel-invalid-fence-token-until")
				task.FenceToken = "fence-token"
				task.FenceVersion = 1
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateChannelMigrationTask(tt.task), ErrInvalidArgument)
		})
	}
}

func TestChannelMigrationTaskValidationRejectsIllegalFenceAndDrainStates(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "fence_before_fence_phase",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-fence-before-phase", "channel-invalid-fence-before-phase")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseWarmCatchUp
				setChannelMigrationTaskFence(&task, 7)
				return task
			}(),
		},
		{
			name: "pending_validate_with_fence",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-pending-fence", "channel-invalid-pending-fence")
				setChannelMigrationTaskFence(&task, 7)
				return task
			}(),
		},
		{
			name: "drain_proof_without_fence",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-proof-without-fence", "channel-invalid-proof-without-fence")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
				setChannelMigrationTaskDrainProof(&task, 7)
				return task
			}(),
		},
		{
			name: "partial_drain_tuple",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-partial-proof", "channel-invalid-partial-proof")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
				setChannelMigrationTaskFence(&task, 7)
				task.CutoverLEO = 10
				return task
			}(),
		},
		{
			name: "drained_fence_version_mismatch",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-proof-fence-version", "channel-invalid-proof-fence-version")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
				setChannelMigrationTaskFence(&task, 7)
				setChannelMigrationTaskDrainProof(&task, 8)
				return task
			}(),
		},
		{
			name: "missing_runtime_generation",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-proof-runtime-generation", "channel-invalid-proof-runtime-generation")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
				setChannelMigrationTaskFence(&task, 7)
				setChannelMigrationTaskDrainProof(&task, 7)
				task.DrainedRuntimeGeneration = 0
				return task
			}(),
		},
		{
			name: "cutover_hw_after_leo",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-proof-hw", "channel-invalid-proof-hw")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
				setChannelMigrationTaskFence(&task, 7)
				setChannelMigrationTaskDrainProof(&task, 7)
				task.CutoverLEO = 10
				task.CutoverHW = 11
				return task
			}(),
		},
		{
			name: "completed_with_active_fence",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-completed-fence", "channel-invalid-completed-fence")
				task.Status = ChannelMigrationStatusCompleted
				task.Phase = ChannelMigrationPhaseVerifyMembership
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				setChannelMigrationTaskFence(&task, 7)
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateChannelMigrationTask(tt.task), ErrInvalidArgument)
		})
	}
}

func TestChannelMigrationTaskValidationAcceptsLegalFenceAndDrainStates(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "active_fence_at_allowed_phase",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-valid-fence", "channel-valid-fence")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseCutoverFence
				setChannelMigrationTaskFence(&task, 7)
				return task
			}(),
		},
		{
			name: "complete_drain_proof_at_allowed_phase",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-valid-proof", "channel-valid-proof")
				task.Status = ChannelMigrationStatusRunning
				task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
				setChannelMigrationTaskFence(&task, 7)
				setChannelMigrationTaskDrainProof(&task, 7)
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, validateChannelMigrationTask(tt.task))
		})
	}
}

func TestChannelMigrationTaskValidationRejectsIllegalEmbeddedLeaderTransfer(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "standalone_leader_transfer_with_embedded_transfer",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-lt-embedded", "channel-invalid-lt-embedded")
				task.Kind = ChannelMigrationKindLeaderTransfer
				task.TargetNode = 2
				task.DesiredLeader = 2
				task.EmbeddedLeaderTransfer = true
				task.EmbeddedDesiredLeader = 3
				return task
			}(),
		},
		{
			name: "embedded_desired_leader_equals_source",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-embedded-source", "channel-invalid-embedded-source")
				task.EmbeddedLeaderTransfer = true
				task.EmbeddedDesiredLeader = task.SourceNode
				return task
			}(),
		},
		{
			name: "embedded_desired_leader_equals_target",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-embedded-target", "channel-invalid-embedded-target")
				task.EmbeddedLeaderTransfer = true
				task.EmbeddedDesiredLeader = task.TargetNode
				return task
			}(),
		},
		{
			name: "embedded_desired_leader_without_embedded_transfer",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-embedded-desired-without-flag", "channel-invalid-embedded-desired-without-flag")
				task.EmbeddedDesiredLeader = 3
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateChannelMigrationTask(tt.task), ErrInvalidArgument)
		})
	}
}

func TestChannelMigrationTaskValidationRejectsInconsistentBlockerState(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "non_blocked_with_code",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocker-code", "channel-invalid-blocker-code")
				task.BlockerCode = "RetryPaused"
				return task
			}(),
		},
		{
			name: "non_blocked_with_message",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocker-message", "channel-invalid-blocker-message")
				task.BlockerMessage = "blocked details without blocked status"
				return task
			}(),
		},
		{
			name: "blocked_missing_code",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocked-code", "channel-invalid-blocked-code")
				task.Status = ChannelMigrationStatusBlocked
				task.Phase = ChannelMigrationPhaseBootstrapTarget
				task.BlockerMessage = "target requires a channel snapshot before catch-up"
				return task
			}(),
		},
		{
			name: "blocked_missing_message",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocked-message", "channel-invalid-blocked-message")
				task.Status = ChannelMigrationStatusBlocked
				task.Phase = ChannelMigrationPhaseBootstrapTarget
				task.BlockerCode = ChannelMigrationBlockerNeedsSnapshotBootstrap
				return task
			}(),
		},
		{
			name: "blocked_unsupported_code",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocked-code-unsupported", "channel-invalid-blocked-code-unsupported")
				task.Status = ChannelMigrationStatusBlocked
				task.Phase = ChannelMigrationPhaseBootstrapTarget
				task.BlockerCode = "RetryPaused"
				task.BlockerMessage = "blocked by an unsupported reason"
				return task
			}(),
		},
		{
			name: "needs_snapshot_bootstrap_at_unsupported_phase",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-invalid-blocked-phase", "channel-invalid-blocked-phase")
				task.Status = ChannelMigrationStatusBlocked
				task.Phase = ChannelMigrationPhaseValidate
				task.BlockerCode = ChannelMigrationBlockerNeedsSnapshotBootstrap
				task.BlockerMessage = "target requires a channel snapshot before catch-up"
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateChannelMigrationTask(tt.task), ErrInvalidArgument)
		})
	}
}

func TestChannelMigrationTaskValidationAcceptsWorkflowConsistentTerminalPhase(t *testing.T) {
	tests := []struct {
		name string
		task ChannelMigrationTask
	}{
		{
			name: "completed_leader_transfer_verify_new_leader",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-completed-lt-verify", "channel-completed-lt-verify")
				task.Kind = ChannelMigrationKindLeaderTransfer
				task.Phase = ChannelMigrationPhaseVerifyNewLeader
				task.DesiredLeader = task.TargetNode
				task.Status = ChannelMigrationStatusCompleted
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				return task
			}(),
		},
		{
			name: "completed_replica_replace_verify_membership",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-completed-rr-verify", "channel-completed-rr-verify")
				task.Phase = ChannelMigrationPhaseVerifyMembership
				task.Status = ChannelMigrationStatusCompleted
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				return task
			}(),
		},
		{
			name: "failed_validate",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-failed-validate", "channel-failed-validate")
				task.Status = ChannelMigrationStatusFailed
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				task.LastError = "validation failed"
				return task
			}(),
		},
		{
			name: "aborted_validate",
			task: func() ChannelMigrationTask {
				task := testChannelMigrationTask("task-aborted-validate", "channel-aborted-validate")
				task.Status = ChannelMigrationStatusAborted
				task.CompletedAtMS = 1750000010000
				task.UpdatedAtMS = 1750000010000
				return task
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, validateChannelMigrationTask(tt.task))
		})
	}
}

func TestChannelMigrationTaskAdvancePersistsProgressAndRetry(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-advance", "channel-advance")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	advanced := task
	advanced.Status = ChannelMigrationStatusRunning
	advanced.Phase = ChannelMigrationPhaseWarmCatchUp
	advanced.Attempt = 2
	advanced.NextRunAtMS = 1750000009000
	advanced.LastError = "target lagging"
	advanced.UpdatedAtMS = 1750000002000
	advanced.Progress = ChannelMigrationProgress{
		LeaderLEO:          100,
		LeaderHW:           98,
		TargetLEO:          91,
		TargetCheckpointHW: 90,
		LagRecords:         9,
		StableSinceMS:      1750000003000,
	}
	require.NoError(t, shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(task, advanced)))

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, advanced.Phase, got.Phase)
	require.Equal(t, advanced.Attempt, got.Attempt)
	require.Equal(t, advanced.NextRunAtMS, got.NextRunAtMS)
	require.Equal(t, advanced.LastError, got.LastError)
	require.Equal(t, advanced.Progress, got.Progress)

	stale := task
	stale.Status = ChannelMigrationStatusRunning
	stale.Phase = ChannelMigrationPhaseAddLearner
	stale.UpdatedAtMS = 1750000003000
	err = shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(task, stale))
	require.ErrorIs(t, err, ErrStaleMeta)

	got, err = shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, advanced.Phase, got.Phase)
	require.Equal(t, advanced.Progress, got.Progress)
}

func TestChannelMigrationTaskAdvancePersistsEmbeddedLeaderTransferDecision(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-advance-embedded", "channel-advance-embedded")
	task.SourceNode = 1
	task.TargetNode = 3
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	advanced := task
	advanced.Status = ChannelMigrationStatusRunning
	advanced.Phase = ChannelMigrationPhaseProbeTarget
	advanced.UpdatedAtMS = 1750000002000
	advanced.EmbeddedLeaderTransfer = true
	advanced.EmbeddedDesiredLeader = 2
	require.NoError(t, shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(task, advanced)))

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.True(t, got.EmbeddedLeaderTransfer)
	require.Equal(t, uint64(2), got.EmbeddedDesiredLeader)
	require.Equal(t, ChannelMigrationPhaseProbeTarget, got.Phase)
}

func TestWriteBatchAdvanceChannelMigrationTaskIsIdempotentForDuplicateCommand(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	task := testChannelMigrationTask("task-advance-idempotent", "channel-advance-idempotent")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	advanced := task
	advanced.Status = ChannelMigrationStatusRunning
	advanced.Phase = ChannelMigrationPhaseWarmCatchUp
	advanced.Attempt = 1
	advanced.UpdatedAtMS = 1750000001000
	req := channelMigrationTaskAdvanceRequest(task, advanced)

	first := db.NewWriteBatch()
	defer first.Close()
	duplicate := db.NewWriteBatch()
	defer duplicate.Close()
	require.NoError(t, first.AdvanceChannelMigrationTask(7, req))
	require.NoError(t, duplicate.AdvanceChannelMigrationTask(7, req))

	require.NoError(t, first.Commit())
	require.NoError(t, duplicate.Commit())

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, advanced.Status, got.Status)
	require.Equal(t, advanced.Phase, got.Phase)
	require.Equal(t, advanced.Attempt, got.Attempt)
}

func TestWriteBatchAdvanceChannelMigrationTaskRejectsNonMonotonicUpdatedAt(t *testing.T) {
	db := openTestDB(t)
	task := testChannelMigrationTask("task-advance-non-monotonic", "channel-advance-non-monotonic")

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.CreateChannelMigrationTask(7, task))

	advanced := task
	advanced.Status = ChannelMigrationStatusRunning
	advanced.Phase = ChannelMigrationPhaseWarmCatchUp
	advanced.Attempt = 1

	err := wb.AdvanceChannelMigrationTask(7, channelMigrationTaskAdvanceRequest(task, advanced))
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestChannelMigrationTaskAdvanceRejectsTerminalMutation(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-terminal-mutation", "channel-terminal-mutation")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	completed := task
	completed.Status = ChannelMigrationStatusCompleted
	completed.Phase = ChannelMigrationPhaseVerifyMembership
	completed.CompletedAtMS = 1750000010000
	completed.UpdatedAtMS = 1750000010000
	require.NoError(t, shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(task, completed)))

	resurrected := completed
	resurrected.Status = ChannelMigrationStatusRunning
	resurrected.Phase = ChannelMigrationPhaseWarmCatchUp
	resurrected.CompletedAtMS = 0
	resurrected.UpdatedAtMS = 1750000011000
	err := shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(completed, resurrected))
	require.ErrorIs(t, err, ErrStaleMeta)

	retitledTerminal := completed
	retitledTerminal.Status = ChannelMigrationStatusFailed
	retitledTerminal.Phase = ChannelMigrationPhaseWarmCatchUp
	retitledTerminal.LastError = "changed after terminal"
	retitledTerminal.CompletedAtMS = 1750000011000
	retitledTerminal.UpdatedAtMS = 1750000011000
	err = shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(completed, retitledTerminal))
	require.ErrorIs(t, err, ErrStaleMeta)

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, completed, got)
}

func TestChannelMigrationTaskBlockedNeedsSnapshotBootstrap(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-blocked", "channel-blocked")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	blocked := task
	blocked.Status = ChannelMigrationStatusBlocked
	blocked.Phase = ChannelMigrationPhaseBootstrapTarget
	blocked.BlockerCode = ChannelMigrationBlockerNeedsSnapshotBootstrap
	blocked.BlockerMessage = "target requires a channel snapshot before catch-up"
	blocked.NextRunAtMS = 1750000010000
	blocked.UpdatedAtMS = 1750000003000
	require.NoError(t, shard.AdvanceChannelMigrationTask(ctx, channelMigrationTaskAdvanceRequest(task, blocked)))

	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, ChannelMigrationStatusBlocked, got.Status)
	require.Equal(t, ChannelMigrationBlockerNeedsSnapshotBootstrap, got.BlockerCode)
	require.Equal(t, blocked.BlockerMessage, got.BlockerMessage)
	require.Equal(t, blocked.NextRunAtMS, got.NextRunAtMS)
}

func TestChannelMigrationTaskTerminalAllowsNewTask(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	completed := testChannelMigrationTask("task-terminal-1", "channel-terminal")
	completed.Status = ChannelMigrationStatusCompleted
	completed.Phase = ChannelMigrationPhaseVerifyMembership
	completed.CompletedAtMS = 1750000010000
	completed.UpdatedAtMS = 1750000010000
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, completed))

	next := testChannelMigrationTask("task-terminal-2", "channel-terminal")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, next))

	active, ok, err := shard.GetActiveChannelMigrationTask(ctx, next.ChannelID, next.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, next.TaskID, active.TaskID)
}

func TestChannelMigrationTaskGarbageCollectsTerminalAfterRetention(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	oldTerminal := testChannelMigrationTask("task-gc-old", "channel-gc-old")
	oldTerminal.Status = ChannelMigrationStatusCompleted
	oldTerminal.Phase = ChannelMigrationPhaseVerifyMembership
	oldTerminal.CompletedAtMS = 1750000000000
	oldTerminal.UpdatedAtMS = oldTerminal.CompletedAtMS
	newTerminal := testChannelMigrationTask("task-gc-new", "channel-gc-new")
	newTerminal.Status = ChannelMigrationStatusFailed
	newTerminal.CompletedAtMS = 1750000005000
	newTerminal.UpdatedAtMS = newTerminal.CompletedAtMS
	active := testChannelMigrationTask("task-gc-active", "channel-gc-active")
	replacedTerminal := testChannelMigrationTask("task-gc-replaced-old", "channel-gc-replaced")
	replacedTerminal.Status = ChannelMigrationStatusCompleted
	replacedTerminal.Phase = ChannelMigrationPhaseVerifyMembership
	replacedTerminal.CompletedAtMS = 1750000000001
	replacedTerminal.UpdatedAtMS = replacedTerminal.CompletedAtMS
	replacementActive := testChannelMigrationTask("task-gc-replaced-active", "channel-gc-replaced")

	require.NoError(t, shard.CreateChannelMigrationTask(ctx, oldTerminal))
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, newTerminal))
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, active))
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, replacedTerminal))
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, replacementActive))

	deleted, err := shard.DeleteTerminalChannelMigrationTasksBefore(ctx, 1750000004000, 10)
	require.NoError(t, err)
	require.Equal(t, 2, deleted)

	_, err = shard.GetChannelMigrationTask(ctx, oldTerminal.ChannelID, oldTerminal.ChannelType, oldTerminal.TaskID)
	require.ErrorIs(t, err, ErrNotFound)
	gotNew, err := shard.GetChannelMigrationTask(ctx, newTerminal.ChannelID, newTerminal.ChannelType, newTerminal.TaskID)
	require.NoError(t, err)
	require.Equal(t, newTerminal.TaskID, gotNew.TaskID)
	gotActive, err := shard.GetChannelMigrationTask(ctx, active.ChannelID, active.ChannelType, active.TaskID)
	require.NoError(t, err)
	require.Equal(t, active.TaskID, gotActive.TaskID)
	gotReplacementActive, ok, err := shard.GetActiveChannelMigrationTask(ctx, replacementActive.ChannelID, replacementActive.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, replacementActive.TaskID, gotReplacementActive.TaskID)
}

func TestChannelMigrationTaskGCPlanIncludesStaleTerminalIndexRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)
	staleIndex := encodeChannelMigrationTaskTerminalIndexKey(7, 1750000000000, "channel-gc-stale-index", 1, "task-gc-stale-index")
	require.NoError(t, db.db.Set(staleIndex, nil, nil))

	task := testChannelMigrationTask("task-gc-after-stale-index", "channel-gc-after-stale-index")
	task.Status = ChannelMigrationStatusCompleted
	task.Phase = ChannelMigrationPhaseVerifyMembership
	task.CompletedAtMS = 1750000001000
	task.UpdatedAtMS = task.CompletedAtMS
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))

	plan, err := shard.PlanTerminalChannelMigrationTaskGC(ctx, 1750000002000, 1)
	require.NoError(t, err)
	require.Equal(t, ChannelMigrationTaskGCPlan{EntryCount: 1}, plan)

	wb := db.NewWriteBatch()
	defer wb.Close()
	deleted, err := wb.DeleteTerminalChannelMigrationTasksBefore(7, ChannelMigrationTaskGCRequest{
		BeforeMS: 1750000002000,
		Limit:    plan.EntryCount,
	})
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.NoError(t, wb.Commit())

	plan, err = shard.PlanTerminalChannelMigrationTaskGC(ctx, 1750000002000, 1)
	require.NoError(t, err)
	require.Equal(t, ChannelMigrationTaskGCPlan{TaskCount: 1, EntryCount: 1}, plan)
}

func TestChannelMigrationTaskEncodeDecodeFullFields(t *testing.T) {
	ctx := context.Background()
	shard := newTestShardStore(t, 7)
	task := testChannelMigrationTask("task-codec", "channel-codec")
	task.Kind = ChannelMigrationKindReplicaReplace
	task.Status = ChannelMigrationStatusBlocked
	task.Phase = ChannelMigrationPhaseFinalTargetCatchUp
	task.SourceNode = 11
	task.TargetNode = 12
	task.DesiredLeader = 13
	task.BaseChannelEpoch = 21
	task.BaseLeaderEpoch = 22
	task.FenceToken = "task-codec"
	task.FenceVersion = 23
	task.FenceUntilMS = 1750000010000
	task.EmbeddedLeaderTransfer = true
	task.EmbeddedDesiredLeader = 13
	task.OwnerNodeID = 14
	task.OwnerLeaseUntilMS = 1750000011000
	task.CutoverLEO = 1000
	task.CutoverHW = 990
	task.DrainedLeaderNode = 15
	task.DrainedRuntimeGeneration = 16
	task.DrainedChannelEpoch = 17
	task.DrainedLeaderEpoch = 18
	task.DrainedFenceVersion = task.FenceVersion
	task.Attempt = 4
	task.NextRunAtMS = 1750000012000
	task.BlockerCode = ChannelMigrationBlockerNeedsSnapshotBootstrap
	task.BlockerMessage = "snapshot bootstrap required"
	task.LastError = "snapshot missing"
	task.CreatedAtMS = 1750000000000
	task.UpdatedAtMS = 1750000009000
	task.Progress = ChannelMigrationProgress{
		LeaderLEO:          2000,
		LeaderHW:           1990,
		TargetLEO:          1800,
		TargetCheckpointHW: 1790,
		LagRecords:         200,
		StableSinceMS:      1750000007000,
	}

	require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))
	got, err := shard.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, task, got)
}

func testChannelMigrationTask(taskID, channelID string) ChannelMigrationTask {
	return ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             ChannelMigrationKindReplicaReplace,
		Status:           ChannelMigrationStatusPending,
		Phase:            ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       1,
		TargetNode:       2,
		DesiredLeader:    1,
		BaseChannelEpoch: 3,
		BaseLeaderEpoch:  4,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

func setChannelMigrationTaskFence(task *ChannelMigrationTask, version uint64) {
	task.FenceToken = "fence-token"
	task.FenceVersion = version
	task.FenceUntilMS = 1750000010000
}

func setChannelMigrationTaskDrainProof(task *ChannelMigrationTask, fenceVersion uint64) {
	task.CutoverLEO = 10
	task.CutoverHW = 9
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 2
	task.DrainedChannelEpoch = 3
	task.DrainedLeaderEpoch = 4
	task.DrainedFenceVersion = fenceVersion
}

func channelMigrationTaskClaimRequest(existing ChannelMigrationTask, ownerNodeID uint64, ownerLeaseUntilMS, updatedAtMS int64) ChannelMigrationTaskClaim {
	return ChannelMigrationTaskClaim{
		Guard: ChannelMigrationTaskGuard{
			ChannelID:                 existing.ChannelID,
			ChannelType:               existing.ChannelType,
			TaskID:                    existing.TaskID,
			ExpectedStatus:            existing.Status,
			ExpectedPhase:             existing.Phase,
			ExpectedOwnerNodeID:       existing.OwnerNodeID,
			ExpectedOwnerLeaseUntilMS: existing.OwnerLeaseUntilMS,
			ExpectedUpdatedAtMS:       existing.UpdatedAtMS,
		},
		Status:            ChannelMigrationStatusRunning,
		Phase:             existing.Phase,
		OwnerNodeID:       ownerNodeID,
		OwnerLeaseUntilMS: ownerLeaseUntilMS,
		UpdatedAtMS:       updatedAtMS,
	}
}

func channelMigrationTaskAdvanceRequest(existing, next ChannelMigrationTask) ChannelMigrationTaskAdvance {
	req := ChannelMigrationTaskAdvance{
		Guard: ChannelMigrationTaskGuard{
			ChannelID:                 existing.ChannelID,
			ChannelType:               existing.ChannelType,
			TaskID:                    existing.TaskID,
			ExpectedStatus:            existing.Status,
			ExpectedPhase:             existing.Phase,
			ExpectedOwnerNodeID:       existing.OwnerNodeID,
			ExpectedOwnerLeaseUntilMS: existing.OwnerLeaseUntilMS,
			ExpectedUpdatedAtMS:       existing.UpdatedAtMS,
		},
		Status:         next.Status,
		Phase:          next.Phase,
		Attempt:        next.Attempt,
		NextRunAtMS:    next.NextRunAtMS,
		BlockerCode:    next.BlockerCode,
		BlockerMessage: next.BlockerMessage,
		LastError:      next.LastError,
		UpdatedAtMS:    next.UpdatedAtMS,
		CompletedAtMS:  next.CompletedAtMS,
		Progress:       next.Progress,
	}
	if next.EmbeddedLeaderTransfer && next.EmbeddedDesiredLeader != 0 && next.EmbeddedDesiredLeader != existing.EmbeddedDesiredLeader {
		req.EmbeddedDesiredLeader = next.EmbeddedDesiredLeader
	}
	return req
}

func requireChannelMigrationTasksEqual(t *testing.T, want, got ChannelMigrationTask) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChannelMigrationTask mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func requireChannelMigrationTaskNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
