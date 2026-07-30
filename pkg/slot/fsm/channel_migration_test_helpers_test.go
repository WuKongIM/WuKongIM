package fsm

import (
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func fsmTestChannelMigrationTask(taskID, channelID string) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       2,
		TargetNode:       3,
		DesiredLeader:    1,
		BaseChannelEpoch: 10,
		BaseLeaderEpoch:  20,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

func fsmTestLeaderTransferTask(taskID, channelID string) metadb.ChannelMigrationTask {
	task := fsmTestChannelMigrationTask(taskID, channelID)
	task.Kind = metadb.ChannelMigrationKindLeaderTransfer
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseProbeTarget
	task.SourceNode = 1
	task.TargetNode = 2
	task.DesiredLeader = 2
	task.UpdatedAtMS = 1750000001000
	return task
}

func fsmTestRuntimeMeta(channelID string, channelType int64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  channelType,
		ChannelEpoch: 10,
		LeaderEpoch:  20,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       1,
		Features:     1,
		LeaseUntilMS: 1750000010000,
	}
}

func fsmTestFencedRuntimeMeta(channelID string, channelType int64, taskID string, version uint64) metadb.ChannelRuntimeMeta {
	meta := fsmTestRuntimeMeta(channelID, channelType)
	meta.WriteFenceToken = taskID
	meta.WriteFenceVersion = version
	meta.WriteFenceReason = 1
	meta.WriteFenceUntilMS = 1750000010000
	return meta
}

func fsmTestTaskGuard(task metadb.ChannelMigrationTask) metadb.ChannelMigrationTaskGuard {
	return metadb.ChannelMigrationTaskGuard{
		ChannelID:                 task.ChannelID,
		ChannelType:               task.ChannelType,
		TaskID:                    task.TaskID,
		ExpectedStatus:            task.Status,
		ExpectedPhase:             task.Phase,
		ExpectedOwnerNodeID:       task.OwnerNodeID,
		ExpectedOwnerLeaseUntilMS: task.OwnerLeaseUntilMS,
		ExpectedUpdatedAtMS:       task.UpdatedAtMS,
	}
}

func fsmTestRuntimeGuard(meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationRuntimeGuard {
	return metadb.ChannelMigrationRuntimeGuard{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedFenceToken:   meta.WriteFenceToken,
		ExpectedFenceVersion: meta.WriteFenceVersion,
	}
}

func fsmTestChannelMigrationClaim(task metadb.ChannelMigrationTask, owner uint64, leaseUntilMS, updatedAtMS int64) metadb.ChannelMigrationTaskClaim {
	return metadb.ChannelMigrationTaskClaim{
		Guard:             fsmTestTaskGuard(task),
		Status:            metadb.ChannelMigrationStatusRunning,
		Phase:             task.Phase,
		OwnerNodeID:       owner,
		OwnerLeaseUntilMS: leaseUntilMS,
		NowMS:             updatedAtMS,
		UpdatedAtMS:       updatedAtMS,
	}
}

func fsmTestChannelMigrationAdvance(task metadb.ChannelMigrationTask, status metadb.ChannelMigrationStatus, phase metadb.ChannelMigrationPhase, updatedAtMS int64) metadb.ChannelMigrationTaskAdvance {
	return metadb.ChannelMigrationTaskAdvance{
		Guard:       fsmTestTaskGuard(task),
		Status:      status,
		Phase:       phase,
		UpdatedAtMS: updatedAtMS,
	}
}

func fsmTestSetFenceRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, fenceUntilMS, updatedAtMS int64) metadb.ChannelMigrationFenceRequest {
	return metadb.ChannelMigrationFenceRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseCutoverFence,
		FenceReason:  1,
		FenceUntilMS: fenceUntilMS,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestResetFenceRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, phase metadb.ChannelMigrationPhase, updatedAtMS int64) metadb.ChannelMigrationResetFenceRequest {
	return metadb.ChannelMigrationResetFenceRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        phase,
		NowMS:        meta.WriteFenceUntilMS + 1,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestCommitLeaderRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationLeaderTransferRequest {
	return metadb.ChannelMigrationLeaderTransferRequest{
		Guard:           fsmTestTaskGuard(task),
		RuntimeGuard:    fsmTestRuntimeGuard(meta),
		Status:          metadb.ChannelMigrationStatusRunning,
		Phase:           metadb.ChannelMigrationPhaseVerifyNewLeader,
		DesiredLeader:   task.TargetNode,
		NextLeaderEpoch: meta.LeaderEpoch + 1,
		LeaseUntilMS:    meta.LeaseUntilMS + 1000,
		NowMS:           meta.WriteFenceUntilMS - 1,
		UpdatedAtMS:     updatedAtMS,
	}
}

func fsmTestAddLearnerRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationAddLearnerRequest {
	return metadb.ChannelMigrationAddLearnerRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseBootstrapTarget,
		TargetNode:   task.TargetNode,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestPromoteRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationPromoteLearnerRequest {
	return metadb.ChannelMigrationPromoteLearnerRequest{
		Guard:        fsmTestTaskGuard(task),
		RuntimeGuard: fsmTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseVerifyMembership,
		SourceNode:   task.SourceNode,
		TargetNode:   task.TargetNode,
		NowMS:        meta.WriteFenceUntilMS - 1,
		UpdatedAtMS:  updatedAtMS,
	}
}

func fsmTestClearFenceRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationClearFenceRequest {
	return metadb.ChannelMigrationClearFenceRequest{
		Guard:         fsmTestTaskGuard(task),
		RuntimeGuard:  fsmTestRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusCompleted,
		Phase:         metadb.ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: updatedAtMS,
	}
}

func fsmTestAbortRequest(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, updatedAtMS int64) metadb.ChannelMigrationAbortRequest {
	return metadb.ChannelMigrationAbortRequest{
		Guard:         fsmTestTaskGuard(task),
		RuntimeGuard:  fsmTestRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusAborted,
		Phase:         task.Phase,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: updatedAtMS,
		LastError:     "operator aborted",
	}
}

func setFSMTestDrainProof(task *metadb.ChannelMigrationTask, fenceVersion uint64) {
	task.CutoverLEO = 100
	task.CutoverHW = 99
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 2
	task.DrainedChannelEpoch = task.BaseChannelEpoch
	task.DrainedLeaderEpoch = task.BaseLeaderEpoch
	task.DrainedFenceVersion = fenceVersion
}

func requireFSMStaleResult(t *testing.T, result []byte, err error) {
	t.Helper()
	if err != nil && !errors.Is(err, metadb.ErrStaleMeta) {
		t.Fatalf("unexpected stale result error = %v", err)
	}
	if got := string(result); got != ApplyResultStaleMeta {
		t.Fatalf("stale result = %q, want %q", got, ApplyResultStaleMeta)
	}
}
