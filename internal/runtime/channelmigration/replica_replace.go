package channelmigration

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func (e *Executor) runReplicaReplacePhase(ctx context.Context, task Task, nowMS int64) error {
	if task.EmbeddedLeaderTransfer && isEmbeddedLeaderTransferPhase(task) {
		return e.runLeaderTransferPhase(ctx, task, nowMS)
	}

	switch task.Phase {
	case slotmeta.ChannelMigrationPhaseValidate:
		return e.replicaReplaceValidate(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseAddLearner:
		return e.replicaReplaceAddLearner(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseBootstrapTarget:
		return e.replicaReplaceBootstrapTarget(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseWarmCatchUp:
		return e.replicaReplaceWarmCatchUp(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseCutoverFence:
		return e.replicaReplaceCutoverFence(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseFinalTargetCatchUp:
		return e.replicaReplaceFinalTargetCatchUp(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhasePromoteAndRemove:
		return e.replicaReplacePromoteAndRemove(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseVerifyMembership:
		return e.replicaReplaceVerifyMembership(ctx, task, nowMS)
	default:
		return ErrPhaseNotImplemented
	}
}

func (e *Executor) replicaReplaceValidate(ctx context.Context, task Task, nowMS int64) error {
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if task.Kind != slotmeta.ChannelMigrationKindReplicaReplace {
		return e.failTask(ctx, task, afterReadMS, "task is not a replica replacement")
	}
	if err := validateReplicaReplaceMeta(task, meta); err != nil {
		return e.failTask(ctx, task, afterReadMS, err.Error())
	}
	if meta.Leader == task.SourceNode {
		desired, ok := selectEmbeddedDesiredLeader(task, meta)
		if !ok {
			return e.failTask(ctx, task, afterReadMS, "no eligible embedded transfer target")
		}
		return e.advanceTask(ctx, task, afterReadMS, task.Status, slotmeta.ChannelMigrationPhaseProbeTarget, advanceTaskOptions{
			EmbeddedDesiredLeader: desired,
		})
	}
	return e.advanceTask(ctx, task, afterReadMS, task.Status, slotmeta.ChannelMigrationPhaseAddLearner, advanceTaskOptions{})
}

func (e *Executor) replicaReplaceAddLearner(ctx context.Context, task Task, nowMS int64) error {
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if meta.Leader == task.SourceNode {
		desired, ok := selectEmbeddedDesiredLeader(task, meta)
		if !ok {
			return e.failTask(ctx, task, afterReadMS, "no eligible embedded transfer target")
		}
		return e.advanceTask(ctx, task, afterReadMS, task.Status, slotmeta.ChannelMigrationPhaseProbeTarget, advanceTaskOptions{
			EmbeddedDesiredLeader: desired,
		})
	}
	if err := e.ensureTaskOwnership(ctx, task, afterReadMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationAddLearnerRequest{
		Guard:        guardFromTask(task),
		RuntimeGuard: runtimeGuardFromMeta(meta),
		Status:       slotmeta.ChannelMigrationStatusRunning,
		Phase:        slotmeta.ChannelMigrationPhaseBootstrapTarget,
		TargetNode:   task.TargetNode,
		UpdatedAtMS:  nextUpdatedAtMS(afterReadMS, task.UpdatedAtMS),
	}
	if err := e.store.AddChannelLearner(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("add channel learner", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) replicaReplaceBootstrapTarget(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	report, err := e.probeClient.ProbeChannel(ctx, channel.NodeID(task.TargetNode), projected)
	afterProbeMS := e.freshNowMS(nowMS)
	if err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if err := validateTargetReportFence(projected, channel.NodeID(task.TargetNode), report); err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	progress := task.Progress
	progress.TargetLEO = report.LogEndOffset
	progress.TargetCheckpointHW = report.CheckpointHW
	if report.SnapshotRequired {
		return e.blockTask(ctx, task, afterProbeMS, slotmeta.ChannelMigrationBlockerNeedsSnapshotBootstrap, channel.ErrSnapshotRequired.Error(), progress)
	}
	return e.advanceTask(ctx, task, afterProbeMS, task.Status, slotmeta.ChannelMigrationPhaseWarmCatchUp, advanceTaskOptions{Progress: progress})
}

func (e *Executor) replicaReplaceWarmCatchUp(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	leader, target, afterProbeMS, err := e.probeLeaderAndTarget(ctx, projected, channel.NodeID(meta.Leader), channel.NodeID(task.TargetNode), nowMS)
	if err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	progress := replicaReplaceProgressFromReports(task.Progress, leader, target)
	if !replicaReplaceWithinCatchUpThreshold(leader, target, e.cfg.CatchUpLagThreshold) {
		progress.StableSinceMS = 0
		return e.retryTask(ctx, task, afterProbeMS, channel.ErrNotReady, progress)
	}
	if progress.StableSinceMS == 0 {
		progress.StableSinceMS = afterProbeMS
		return e.advanceTask(ctx, task, afterProbeMS, task.Status, task.Phase, advanceTaskOptions{Progress: progress})
	}
	if afterProbeMS-progress.StableSinceMS < e.cfg.CatchUpStableWindow.Milliseconds() {
		return e.advanceTask(ctx, task, afterProbeMS, task.Status, task.Phase, advanceTaskOptions{Progress: progress})
	}
	return e.advanceTask(ctx, task, afterProbeMS, task.Status, slotmeta.ChannelMigrationPhaseCutoverFence, advanceTaskOptions{Progress: progress})
}

func (e *Executor) replicaReplaceCutoverFence(ctx context.Context, task Task, nowMS int64) error {
	if task.FenceToken == "" {
		return e.replicaReplaceSetCutoverFence(ctx, task, nowMS)
	}
	return e.replicaReplaceDrainCutover(ctx, task, nowMS)
}

func (e *Executor) replicaReplaceSetCutoverFence(ctx context.Context, task Task, nowMS int64) error {
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if err := e.ensureTaskOwnership(ctx, task, afterReadMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationFenceRequest{
		Guard:        guardFromTask(task),
		RuntimeGuard: runtimeGuardFromMeta(meta),
		Status:       slotmeta.ChannelMigrationStatusRunning,
		Phase:        slotmeta.ChannelMigrationPhaseCutoverFence,
		FenceReason:  uint8(channel.WriteFenceReasonMigration),
		FenceUntilMS: afterReadMS + e.cfg.FenceLease.Milliseconds(),
		UpdatedAtMS:  nextUpdatedAtMS(afterReadMS, task.UpdatedAtMS),
	}
	if err := e.store.SetChannelWriteFence(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("set replica replacement write fence", task, req.Phase, req.RuntimeGuard.ExpectedFenceVersion+1)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) replicaReplaceDrainCutover(ctx context.Context, task Task, nowMS int64) error {
	if e.migrationControl == nil {
		return fmt.Errorf("%w: migration control", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if e.fenceExpired(task, meta, afterReadMS) {
		return e.resetExpiredReplicaReplaceFence(ctx, task, meta, afterReadMS)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterReadMS); err != nil {
		return err
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	drain, err := e.migrationControl.FenceAndDrain(ctx, channel.NodeID(meta.Leader), channel.FenceAndDrainRequest{
		ChannelKey:           projected.Key,
		TaskID:               task.TaskID,
		WriteFenceToken:      task.FenceToken,
		WriteFenceVersion:    task.FenceVersion,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       channel.NodeID(meta.Leader),
	})
	afterDrainMS := e.freshNowMS(afterReadMS)
	if err != nil {
		return e.retryTask(ctx, task, afterDrainMS, err, task.Progress)
	}
	if err := validateDrainProof(projected, task, meta, drain); err != nil {
		return e.retryTask(ctx, task, afterDrainMS, err, task.Progress)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterDrainMS); err != nil {
		return err
	}
	progress := task.Progress
	progress.LeaderLEO = drain.LEO
	progress.LeaderHW = drain.HW
	return e.advanceTask(ctx, task, afterDrainMS, task.Status, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp, advanceTaskOptions{
		Progress: progress,
		CutoverProof: slotmeta.ChannelMigrationCutoverProof{
			CutoverLEO:               drain.LEO,
			CutoverHW:                drain.HW,
			DrainedLeaderNode:        uint64(meta.Leader),
			DrainedRuntimeGeneration: drain.RuntimeGeneration,
			DrainedChannelEpoch:      drain.ChannelEpoch,
			DrainedLeaderEpoch:       drain.LeaderEpoch,
			DrainedFenceVersion:      drain.WriteFenceVersion,
		},
	})
}

func (e *Executor) replicaReplaceFinalTargetCatchUp(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if e.fenceExpired(task, meta, afterReadMS) {
		return e.resetExpiredReplicaReplaceFence(ctx, task, meta, afterReadMS)
	}
	if task.DrainedFenceVersion != meta.WriteFenceVersion {
		return e.retryTask(ctx, task, afterReadMS, slotmeta.ErrStaleMeta, task.Progress)
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	report, err := e.probeClient.ProbeChannel(ctx, channel.NodeID(task.TargetNode), projected)
	afterProbeMS := e.freshNowMS(afterReadMS)
	if err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterProbeMS); err != nil {
		return err
	}
	progress := task.Progress
	progress.TargetLEO = report.LogEndOffset
	progress.TargetCheckpointHW = report.CheckpointHW
	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(FinalTargetProofRequest{
		Meta:               projected,
		TargetNode:         channel.NodeID(task.TargetNode),
		CutoverLEO:         task.CutoverLEO,
		CutoverHW:          task.CutoverHW,
		CutoverOffsetEpoch: task.DrainedChannelEpoch,
		Target:             report,
	})
	if err != nil {
		if errors.Is(err, channel.ErrSnapshotRequired) {
			return e.blockTask(ctx, task, afterProbeMS, slotmeta.ChannelMigrationBlockerNeedsSnapshotBootstrap, channel.ErrSnapshotRequired.Error(), progress)
		}
		return e.retryTask(ctx, task, afterProbeMS, err, progress)
	}
	progress.TargetLEO = proof.TargetLEO
	progress.TargetCheckpointHW = proof.TargetCheckpointHW
	return e.advanceTask(ctx, task, afterProbeMS, task.Status, slotmeta.ChannelMigrationPhasePromoteAndRemove, advanceTaskOptions{Progress: progress})
}

func (e *Executor) replicaReplacePromoteAndRemove(ctx context.Context, task Task, nowMS int64) error {
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if e.fenceExpired(task, meta, afterReadMS) {
		return e.resetExpiredReplicaReplaceFence(ctx, task, meta, afterReadMS)
	}
	if meta.Leader == task.SourceNode {
		return e.retryTask(ctx, task, afterReadMS, slotmeta.ErrStaleMeta, task.Progress)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterReadMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationPromoteLearnerRequest{
		Guard:        guardFromTask(task),
		RuntimeGuard: runtimeGuardFromMeta(meta),
		Status:       slotmeta.ChannelMigrationStatusRunning,
		Phase:        slotmeta.ChannelMigrationPhaseVerifyMembership,
		SourceNode:   task.SourceNode,
		TargetNode:   task.TargetNode,
		NowMS:        afterReadMS,
		UpdatedAtMS:  nextUpdatedAtMS(afterReadMS, task.UpdatedAtMS),
	}
	if err := e.store.PromoteLearnerAndRemoveReplica(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("promote learner and remove source replica", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) replicaReplaceVerifyMembership(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if !replicaReplaceMembershipCommitted(task, meta) {
		return e.retryTask(ctx, task, afterReadMS, channel.ErrNotReady, task.Progress)
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	leader, target, afterProbeMS, err := e.probeLeaderAndTarget(ctx, projected, channel.NodeID(meta.Leader), channel.NodeID(task.TargetNode), afterReadMS)
	if err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if leader.Leader != channel.NodeID(meta.Leader) ||
		leader.Role != channel.ReplicaRoleLeader ||
		!leader.CommitReady ||
		target.Leader != channel.NodeID(meta.Leader) ||
		target.Role != channel.ReplicaRoleFollower ||
		!target.CommitReady {
		return e.retryTask(ctx, task, afterProbeMS, channel.ErrNotReady, task.Progress)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterProbeMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationClearFenceRequest{
		Guard:         guardFromTask(task),
		RuntimeGuard:  runtimeGuardFromMeta(meta),
		Status:        slotmeta.ChannelMigrationStatusCompleted,
		Phase:         slotmeta.ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   nextUpdatedAtMS(afterProbeMS, task.UpdatedAtMS),
		CompletedAtMS: nextUpdatedAtMS(afterProbeMS, task.UpdatedAtMS),
	}
	if err := e.store.ClearChannelWriteFence(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("clear replica replacement write fence", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) resetExpiredReplicaReplaceFence(ctx context.Context, task Task, meta slotmeta.ChannelRuntimeMeta, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationResetFenceRequest{
		Guard:        guardFromTask(task),
		RuntimeGuard: runtimeGuardFromMeta(meta),
		Status:       slotmeta.ChannelMigrationStatusRunning,
		Phase:        slotmeta.ChannelMigrationPhaseWarmCatchUp,
		NowMS:        nowMS,
		UpdatedAtMS:  nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	}
	if err := e.store.ResetChannelWriteFenceToPreCutover(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("reset expired replica replacement write fence", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) probeLeaderAndTarget(ctx context.Context, meta channel.Meta, leaderNode, targetNode channel.NodeID, floorMS int64) (ProbeReport, ProbeReport, int64, error) {
	leader, err := e.probeClient.ProbeChannel(ctx, leaderNode, meta)
	afterLeaderMS := e.freshNowMS(floorMS)
	if err != nil {
		return ProbeReport{}, ProbeReport{}, afterLeaderMS, err
	}
	if err := validateTargetReportFence(meta, leaderNode, leader); err != nil {
		return ProbeReport{}, ProbeReport{}, afterLeaderMS, err
	}
	target, err := e.probeClient.ProbeChannel(ctx, targetNode, meta)
	afterTargetMS := e.freshNowMS(afterLeaderMS)
	if err != nil {
		return leader, ProbeReport{}, afterTargetMS, err
	}
	if err := validateTargetReportFence(meta, targetNode, target); err != nil {
		return leader, ProbeReport{}, afterTargetMS, err
	}
	return leader, target, afterTargetMS, nil
}

func validateReplicaReplaceMeta(task Task, meta slotmeta.ChannelRuntimeMeta) error {
	if task.SourceNode == 0 || task.TargetNode == 0 || task.SourceNode == task.TargetNode {
		return channel.ErrInvalidArgument
	}
	if meta.ChannelEpoch != task.BaseChannelEpoch || meta.LeaderEpoch != task.BaseLeaderEpoch {
		return slotmeta.ErrStaleMeta
	}
	if meta.Status != uint8(channel.StatusActive) ||
		!containsUint64(meta.Replicas, task.SourceNode) ||
		!containsUint64(meta.ISR, task.SourceNode) ||
		containsUint64(meta.Replicas, task.TargetNode) ||
		containsUint64(meta.ISR, task.TargetNode) ||
		meta.Leader == task.TargetNode ||
		int(meta.MinISR) > len(meta.ISR) {
		return channel.ErrInvalidMeta
	}
	return nil
}

func selectEmbeddedDesiredLeader(task Task, meta slotmeta.ChannelRuntimeMeta) (uint64, bool) {
	for _, node := range meta.ISR {
		if node != task.SourceNode && node != task.TargetNode {
			return node, true
		}
	}
	return 0, false
}

func replicaReplaceProgressFromReports(progress slotmeta.ChannelMigrationProgress, leader, target ProbeReport) slotmeta.ChannelMigrationProgress {
	progress.LeaderLEO = leader.LogEndOffset
	progress.LeaderHW = leader.CheckpointHW
	progress.TargetLEO = target.LogEndOffset
	progress.TargetCheckpointHW = target.CheckpointHW
	if leader.LogEndOffset > target.LogEndOffset {
		progress.LagRecords = leader.LogEndOffset - target.LogEndOffset
	} else {
		progress.LagRecords = 0
	}
	return progress
}

func replicaReplaceCaughtUp(leader, target ProbeReport) bool {
	return target.LogEndOffset >= leader.LogEndOffset && target.CheckpointHW >= leader.CheckpointHW
}

func replicaReplaceWithinCatchUpThreshold(leader, target ProbeReport, lagThreshold uint64) bool {
	if target.CheckpointHW < leader.CheckpointHW {
		return false
	}
	if target.LogEndOffset >= leader.LogEndOffset {
		return true
	}
	return leader.LogEndOffset-target.LogEndOffset <= lagThreshold
}

func replicaReplaceMembershipCommitted(task Task, meta slotmeta.ChannelRuntimeMeta) bool {
	return !containsUint64(meta.Replicas, task.SourceNode) &&
		!containsUint64(meta.ISR, task.SourceNode) &&
		containsUint64(meta.Replicas, task.TargetNode) &&
		containsUint64(meta.ISR, task.TargetNode) &&
		meta.WriteFenceToken == task.FenceToken &&
		meta.WriteFenceVersion == task.FenceVersion
}

func isEmbeddedLeaderTransferPhase(task Task) bool {
	switch task.Phase {
	case slotmeta.ChannelMigrationPhaseProbeTarget,
		slotmeta.ChannelMigrationPhaseWriteFence,
		slotmeta.ChannelMigrationPhaseDrainLeader,
		slotmeta.ChannelMigrationPhaseCommitLeaderMeta,
		slotmeta.ChannelMigrationPhaseVerifyNewLeader:
		return true
	case slotmeta.ChannelMigrationPhaseFinalTargetCatchUp:
		return task.DrainedLeaderNode == task.SourceNode &&
			task.DrainedChannelEpoch == task.BaseChannelEpoch
	default:
		return false
	}
}
