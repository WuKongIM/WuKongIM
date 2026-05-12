package channelmigration

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var errOwnerCheckFailed = errors.New("channelmigration: owner check failed")

func (e *Executor) runLeaderTransferPhase(ctx context.Context, task Task, nowMS int64) error {
	switch task.Phase {
	case slotmeta.ChannelMigrationPhaseValidate:
		return e.leaderTransferValidate(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseProbeTarget:
		return e.leaderTransferProbeTarget(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseWriteFence:
		return e.leaderTransferWriteFence(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseDrainLeader:
		return e.leaderTransferDrainLeader(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseFinalTargetCatchUp:
		return e.leaderTransferFinalTargetCatchUp(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseCommitLeaderMeta:
		return e.leaderTransferCommitLeaderMeta(ctx, task, nowMS)
	case slotmeta.ChannelMigrationPhaseVerifyNewLeader:
		return e.leaderTransferVerifyNewLeader(ctx, task, nowMS)
	default:
		return ErrPhaseNotImplemented
	}
}

func (e *Executor) leaderTransferValidate(ctx context.Context, task Task, nowMS int64) error {
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	if task.Kind != slotmeta.ChannelMigrationKindLeaderTransfer {
		return e.failTask(ctx, task, nowMS, "task is not a leader transfer")
	}
	if task.TargetNode == 0 || task.DesiredLeader != task.TargetNode {
		return e.failTask(ctx, task, nowMS, "invalid leader transfer target")
	}
	if !containsUint64(meta.ISR, task.TargetNode) {
		return e.failTask(ctx, task, nowMS, "target outside ISR")
	}
	if meta.Leader != task.SourceNode || meta.Leader == task.TargetNode {
		return e.failTask(ctx, task, nowMS, "source is not current leader")
	}
	if meta.ChannelEpoch != task.BaseChannelEpoch || meta.LeaderEpoch != task.BaseLeaderEpoch {
		return e.failTask(ctx, task, nowMS, "base epoch changed")
	}
	return e.advanceTask(ctx, task, nowMS, task.Status, slotmeta.ChannelMigrationPhaseProbeTarget, advanceTaskOptions{})
}

func (e *Executor) leaderTransferProbeTarget(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	report, err := e.probeClient.ProbeChannel(ctx, channel.NodeID(task.TargetNode), projected)
	if err != nil {
		return e.retryTask(ctx, task, nowMS, err, task.Progress)
	}
	if err := validateTargetReportFence(projected, channel.NodeID(task.TargetNode), report); err != nil {
		return e.retryTask(ctx, task, nowMS, err, task.Progress)
	}
	progress := task.Progress
	progress.TargetLEO = report.LogEndOffset
	progress.TargetCheckpointHW = report.CheckpointHW
	if report.LogEndOffset >= progress.LeaderLEO {
		progress.LagRecords = 0
	} else {
		progress.LagRecords = progress.LeaderLEO - report.LogEndOffset
	}
	return e.advanceTask(ctx, task, nowMS, task.Status, slotmeta.ChannelMigrationPhaseWriteFence, advanceTaskOptions{Progress: progress})
}

func (e *Executor) leaderTransferWriteFence(ctx context.Context, task Task, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationFenceRequest{
		Guard:        guardFromTask(task),
		RuntimeGuard: runtimeGuardFromMeta(meta),
		Status:       slotmeta.ChannelMigrationStatusRunning,
		Phase:        slotmeta.ChannelMigrationPhaseDrainLeader,
		FenceReason:  uint8(channel.WriteFenceReasonMigration),
		FenceUntilMS: nowMS + e.cfg.FenceLease.Milliseconds(),
		UpdatedAtMS:  nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	}
	if err := e.store.SetChannelWriteFence(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("set write fence", task, req.Phase, req.RuntimeGuard.ExpectedFenceVersion+1)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) leaderTransferDrainLeader(ctx context.Context, task Task, nowMS int64) error {
	if e.migrationControl == nil {
		return fmt.Errorf("%w: migration control", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	if e.fenceExpired(task, meta, nowMS) {
		return e.resetExpiredLeaderTransferFence(ctx, task, meta, nowMS)
	}
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
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
	if err != nil {
		return e.retryTask(ctx, task, nowMS, err, task.Progress)
	}
	if err := validateDrainProof(projected, task, meta, drain); err != nil {
		return err
	}
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	progress := task.Progress
	progress.LeaderLEO = drain.LEO
	progress.LeaderHW = drain.HW
	return e.advanceTask(ctx, task, nowMS, task.Status, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp, advanceTaskOptions{
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

func (e *Executor) leaderTransferFinalTargetCatchUp(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	if e.fenceExpired(task, meta, nowMS) {
		return e.resetExpiredLeaderTransferFence(ctx, task, meta, nowMS)
	}
	if task.DrainedFenceVersion != meta.WriteFenceVersion {
		return slotmeta.ErrStaleMeta
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	report, err := e.probeClient.ProbeChannel(ctx, channel.NodeID(task.TargetNode), projected)
	if err != nil {
		return e.retryTask(ctx, task, nowMS, err, task.Progress)
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
		return e.retryTask(ctx, task, nowMS, err, progress)
	}
	progress.TargetLEO = proof.TargetLEO
	progress.TargetCheckpointHW = proof.TargetCheckpointHW
	return e.advanceTask(ctx, task, nowMS, task.Status, slotmeta.ChannelMigrationPhaseCommitLeaderMeta, advanceTaskOptions{Progress: progress})
}

func (e *Executor) leaderTransferCommitLeaderMeta(ctx context.Context, task Task, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationLeaderTransferRequest{
		Guard:           guardFromTask(task),
		RuntimeGuard:    runtimeGuardFromMeta(meta),
		Status:          slotmeta.ChannelMigrationStatusRunning,
		Phase:           slotmeta.ChannelMigrationPhaseVerifyNewLeader,
		DesiredLeader:   channelMigrationDesiredLeader(task),
		NextLeaderEpoch: meta.LeaderEpoch + 1,
		LeaseUntilMS:    nowMS + e.cfg.LeaderLease.Milliseconds(),
		NowMS:           nowMS,
		UpdatedAtMS:     nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	}
	if err := e.store.CommitChannelLeaderTransfer(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("commit leader transfer", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) leaderTransferVerifyNewLeader(ctx context.Context, task Task, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	if meta.Leader != channelMigrationDesiredLeader(task) {
		return e.retryTask(ctx, task, nowMS, channel.ErrNotReady, task.Progress)
	}
	req := slotmeta.ChannelMigrationClearFenceRequest{
		Guard:         guardFromTask(task),
		RuntimeGuard:  runtimeGuardFromMeta(meta),
		Status:        slotmeta.ChannelMigrationStatusCompleted,
		Phase:         slotmeta.ChannelMigrationPhaseClearFence,
		UpdatedAtMS:   nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
		CompletedAtMS: nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	}
	if err := e.store.ClearChannelWriteFence(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("clear write fence", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

func (e *Executor) resetExpiredLeaderTransferFence(ctx context.Context, task Task, meta slotmeta.ChannelRuntimeMeta, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationResetFenceRequest{
		Guard:        guardFromTask(task),
		RuntimeGuard: runtimeGuardFromMeta(meta),
		Status:       slotmeta.ChannelMigrationStatusRunning,
		Phase:        slotmeta.ChannelMigrationPhaseWriteFence,
		NowMS:        nowMS,
		UpdatedAtMS:  nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	}
	if err := e.store.ResetChannelWriteFenceToPreCutover(ctx, req); err != nil {
		return err
	}
	e.recordLeaderTransferCommand("reset expired write fence", task, req.Phase, meta.WriteFenceVersion)
	e.recordPhaseTransition(task, req.Status, req.Phase)
	return nil
}

type advanceTaskOptions struct {
	Progress     slotmeta.ChannelMigrationProgress
	CutoverProof slotmeta.ChannelMigrationCutoverProof
	LastError    string
	NextRunAtMS  int64
	CompletedAt  int64
}

func (e *Executor) advanceTask(ctx context.Context, task Task, nowMS int64, status slotmeta.ChannelMigrationStatus, phase slotmeta.ChannelMigrationPhase, opts advanceTaskOptions) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	progress := opts.Progress
	if progress == (slotmeta.ChannelMigrationProgress{}) {
		progress = task.Progress
	}
	req := AdvanceRequest{
		Guard:         guardFromTask(task),
		Status:        status,
		Phase:         phase,
		Attempt:       task.Attempt + 1,
		NextRunAtMS:   opts.NextRunAtMS,
		LastError:     opts.LastError,
		UpdatedAtMS:   nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
		CompletedAtMS: opts.CompletedAt,
		Progress:      progress,
		CutoverProof:  opts.CutoverProof,
	}
	if err := e.store.AdvanceChannelMigrationTask(ctx, req); err != nil {
		return err
	}
	e.recordPhaseTransition(task, status, phase)
	return nil
}

func (e *Executor) retryTask(ctx context.Context, task Task, nowMS int64, cause error, progress slotmeta.ChannelMigrationProgress) error {
	if cause == nil {
		cause = channel.ErrNotReady
	}
	return e.advanceTask(ctx, task, nowMS, slotmeta.ChannelMigrationStatusRunning, task.Phase, advanceTaskOptions{
		Progress:    progress,
		LastError:   cause.Error(),
		NextRunAtMS: nowMS + e.cfg.RetryBackoff.Milliseconds(),
	})
}

func (e *Executor) failTask(ctx context.Context, task Task, nowMS int64, reason string) error {
	return e.advanceTask(ctx, task, nowMS, slotmeta.ChannelMigrationStatusFailed, task.Phase, advanceTaskOptions{
		LastError:   reason,
		CompletedAt: nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	})
}

func (e *Executor) ensureTaskOwnership(ctx context.Context, task Task, nowMS int64) error {
	if ok := e.confirmLocalSlotLeader(ctx, task); !ok {
		return errOwnerCheckFailed
	}
	if !e.hasLocalOwnerLease(task, nowMS) {
		return errOwnerCheckFailed
	}
	return nil
}

func (e *Executor) fenceExpired(task Task, meta slotmeta.ChannelRuntimeMeta, nowMS int64) bool {
	return task.FenceToken != "" &&
		meta.WriteFenceToken == task.FenceToken &&
		task.FenceVersion == meta.WriteFenceVersion &&
		meta.WriteFenceUntilMS > 0 &&
		nowMS > meta.WriteFenceUntilMS
}

func (e *Executor) recordPhaseTransition(task Task, status slotmeta.ChannelMigrationStatus, phase slotmeta.ChannelMigrationPhase) {
	e.metrics.RecordPhaseTransition(PhaseTransition{
		TaskID:     task.TaskID,
		ChannelID:  task.ChannelID,
		FromStatus: task.Status,
		ToStatus:   status,
		FromPhase:  task.Phase,
		ToPhase:    phase,
	})
}

func (e *Executor) recordLeaderTransferCommand(message string, task Task, phase slotmeta.ChannelMigrationPhase, fenceVersion uint64) {
	e.log.Info(message,
		wklog.String("taskID", task.TaskID),
		wklog.ChannelID(task.ChannelID),
		wklog.ChannelType(task.ChannelType),
		wklog.Uint64("ownerNodeID", task.OwnerNodeID),
		wklog.Uint64("fenceVersion", fenceVersion),
		wklog.Uint64("fromPhase", uint64(task.Phase)),
		wklog.Uint64("toPhase", uint64(phase)),
	)
}

func runtimeGuardFromMeta(meta slotmeta.ChannelRuntimeMeta) slotmeta.ChannelMigrationRuntimeGuard {
	return slotmeta.ChannelMigrationRuntimeGuard{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedFenceToken:   meta.WriteFenceToken,
		ExpectedFenceVersion: meta.WriteFenceVersion,
	}
}

func validateDrainProof(meta channel.Meta, task Task, runtimeMeta slotmeta.ChannelRuntimeMeta, drain channel.DrainResult) error {
	if drain.ChannelKey != meta.Key ||
		drain.ChannelEpoch != runtimeMeta.ChannelEpoch ||
		drain.LeaderEpoch != runtimeMeta.LeaderEpoch ||
		drain.WriteFenceVersion != task.FenceVersion ||
		drain.HW > drain.LEO {
		return channel.ErrStaleMeta
	}
	return nil
}

func channelMigrationDesiredLeader(task Task) uint64 {
	if task.EmbeddedLeaderTransfer && task.EmbeddedDesiredLeader != 0 {
		return task.EmbeddedDesiredLeader
	}
	return task.DesiredLeader
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
