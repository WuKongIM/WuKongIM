package channels

import (
	"context"
	"fmt"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	migrationBlockInvalidReplicaReplace = "invalid_replica_replace"
	migrationBlockSourceIsLeader        = "source_is_leader"
)

func (e *MigrationExecutor) runReplicaReplacePhase(ctx context.Context, task metadb.ChannelMigrationTask) error {
	switch task.Phase {
	case metadb.ChannelMigrationPhaseValidate:
		return e.runReplicaReplaceValidate(ctx, task)
	case metadb.ChannelMigrationPhaseAddLearner:
		return e.store.AddLearner(ctx, task)
	case metadb.ChannelMigrationPhaseBootstrapTarget:
		return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseWarmCatchUp, metadb.ChannelMigrationStatusRunning, "")
	case metadb.ChannelMigrationPhaseWarmCatchUp:
		return e.runReplicaReplaceWarmCatchUp(ctx, task)
	case metadb.ChannelMigrationPhaseCutoverFence:
		return e.runReplicaReplaceCutoverFence(ctx, task)
	case metadb.ChannelMigrationPhaseFinalTargetCatchUp:
		return e.runReplicaReplaceFinalTargetCatchUp(ctx, task)
	case metadb.ChannelMigrationPhasePromoteAndRemove:
		return e.runReplicaReplacePromoteAndRemove(ctx, task)
	case metadb.ChannelMigrationPhaseVerifyMembership:
		return e.runReplicaReplaceVerifyMembership(ctx, task)
	default:
		return e.blockTask(ctx, task, migrationBlockInvalidReplicaReplace)
	}
}

func (e *MigrationExecutor) runReplicaReplaceValidate(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, _, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if meta.Leader == task.SourceNode {
		return e.blockTask(ctx, task, migrationBlockSourceIsLeader)
	}
	if err := validateReplicaReplaceTask(task, meta); err != nil {
		return e.blockTask(ctx, task, migrationBlockInvalidReplicaReplace)
	}
	return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseAddLearner, metadb.ChannelMigrationStatusRunning, "")
}

func (e *MigrationExecutor) runReplicaReplaceWarmCatchUp(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderTransferRuntimeProbe(probe, meta, ch.RoleFollower); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	return e.store.SetWriteFence(ctx, task, ch.WriteFenceReasonReplicaReplace)
}

func (e *MigrationExecutor) runReplicaReplaceCutoverFence(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	return e.advanceReplicaReplaceDrainProof(ctx, task, meta, id, metadb.ChannelMigrationPhaseFinalTargetCatchUp)
}

func (e *MigrationExecutor) runReplicaReplaceFinalTargetCatchUp(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if !taskHasCutoverProof(task) {
		return e.advanceReplicaReplaceDrainProof(ctx, task, meta, id, metadb.ChannelMigrationPhaseFinalTargetCatchUp)
	}
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderTransferRuntimeProbe(probe, meta, ch.RoleFollower); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	if probe.HW < task.CutoverLEO {
		return e.blockTask(ctx, task, migrationBlockTargetLagging)
	}
	progress := metadb.ChannelMigrationProgress{
		LeaderLEO:          task.CutoverLEO,
		LeaderHW:           task.CutoverHW,
		TargetLEO:          probe.LEO,
		TargetCheckpointHW: probe.CheckpointHW,
	}
	return e.store.AdvanceWithProof(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhasePromoteAndRemove, metadb.ChannelMigrationStatusRunning, "", progress, taskCutoverProof(task))
}

func (e *MigrationExecutor) runReplicaReplacePromoteAndRemove(ctx context.Context, task metadb.ChannelMigrationTask) error {
	if !taskHasCutoverProof(task) || task.DrainedFenceVersion != task.FenceVersion {
		return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseFinalTargetCatchUp, metadb.ChannelMigrationStatusRunning, "")
	}
	return e.store.PromoteLearnerAndRemoveSource(ctx, task)
}

func (e *MigrationExecutor) runReplicaReplaceVerifyMembership(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if migrationNodeInList(meta.Replicas, task.SourceNode) ||
		migrationNodeInList(meta.ISR, task.SourceNode) ||
		!migrationNodeInList(meta.Replicas, task.TargetNode) ||
		!migrationNodeInList(meta.ISR, task.TargetNode) ||
		int64(len(meta.ISR)) < meta.MinISR {
		return e.blockTask(ctx, task, migrationBlockInvalidReplicaReplace)
	}
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderTransferRuntimeProbe(probe, meta, ch.RoleFollower); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	if !writeFenceMatchesTask(probe.WriteFence, task) ||
		meta.WriteFenceToken != task.TaskID ||
		meta.WriteFenceVersion != task.FenceVersion {
		return e.blockTask(ctx, task, migrationBlockInvalidReplicaReplace)
	}
	return e.store.ClearWriteFence(ctx, task)
}

func (e *MigrationExecutor) advanceReplicaReplaceDrainProof(ctx context.Context, task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, id ch.ChannelID, phase metadb.ChannelMigrationPhase) error {
	if task.FenceVersion == 0 || task.FenceToken != task.TaskID || meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		return e.blockTask(ctx, task, migrationBlockDrainNotComplete)
	}
	result, err := e.runtime.DrainChannel(ctx, meta.Leader, ch.DrainChannelRequest{
		ChannelID:    id,
		LeaderEpoch:  meta.LeaderEpoch,
		FenceVersion: task.FenceVersion,
	})
	if err != nil {
		return err
	}
	if !result.Drained {
		return e.blockTask(ctx, task, migrationBlockDrainNotComplete)
	}
	proof := metadb.ChannelMigrationCutoverProof{
		CutoverLEO:               result.LEO,
		CutoverHW:                result.HW,
		DrainedLeaderNode:        meta.Leader,
		DrainedRuntimeGeneration: meta.RouteGeneration,
		DrainedChannelEpoch:      meta.ChannelEpoch,
		DrainedLeaderEpoch:       meta.LeaderEpoch,
		DrainedFenceVersion:      task.FenceVersion,
	}
	progress := metadb.ChannelMigrationProgress{LeaderLEO: result.LEO, LeaderHW: result.HW}
	return e.store.AdvanceWithProof(ctx, task, task.UpdatedAtMS, phase, metadb.ChannelMigrationStatusRunning, "", progress, proof)
}

func validateReplicaReplaceTask(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta) error {
	if task.SourceNode == 0 || task.TargetNode == 0 || task.SourceNode == task.TargetNode {
		return fmt.Errorf("%w: invalid source or target", ch.ErrInvalidConfig)
	}
	if !migrationNodeInList(meta.Replicas, task.SourceNode) {
		return fmt.Errorf("%w: source is not a channel replica", ch.ErrInvalidConfig)
	}
	if !migrationNodeInList(meta.ISR, task.SourceNode) && int64(len(meta.ISR)) < meta.MinISR {
		return fmt.Errorf("%w: min ISR is not satisfied without source", ch.ErrInvalidConfig)
	}
	if migrationNodeInList(meta.Replicas, task.TargetNode) || migrationNodeInList(meta.ISR, task.TargetNode) {
		return fmt.Errorf("%w: target already participates", ch.ErrInvalidConfig)
	}
	if int64(len(meta.ISR)) < meta.MinISR {
		return fmt.Errorf("%w: min ISR is not satisfied", ch.ErrInvalidConfig)
	}
	return nil
}
