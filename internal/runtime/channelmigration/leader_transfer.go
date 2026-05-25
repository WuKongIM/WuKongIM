package channelmigration

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
	afterReadMS := e.freshNowMS(nowMS)
	if task.Kind != slotmeta.ChannelMigrationKindLeaderTransfer {
		return e.failTask(ctx, task, afterReadMS, "task is not a leader transfer")
	}
	if task.TargetNode == 0 || task.DesiredLeader != task.TargetNode {
		return e.failTask(ctx, task, afterReadMS, "invalid leader transfer target")
	}
	if !containsUint64(meta.ISR, task.TargetNode) {
		return e.failTask(ctx, task, afterReadMS, "target outside ISR")
	}
	if meta.Leader != task.SourceNode || meta.Leader == task.TargetNode {
		return e.failTask(ctx, task, afterReadMS, "source is not current leader")
	}
	if meta.ChannelEpoch != task.BaseChannelEpoch || meta.LeaderEpoch != task.BaseLeaderEpoch {
		return e.failTask(ctx, task, afterReadMS, "base epoch changed")
	}
	return e.advanceTask(ctx, task, afterReadMS, task.Status, slotmeta.ChannelMigrationPhaseProbeTarget, advanceTaskOptions{})
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
	targetNode := leaderTransferTargetNode(task)
	report, err := e.probeClient.ProbeChannel(ctx, targetNode, projected)
	afterProbeMS := e.now().UnixMilli()
	if afterProbeMS < nowMS {
		afterProbeMS = nowMS
	}
	if err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if err := validateTargetReportFence(projected, targetNode, report); err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterProbeMS); err != nil {
		return err
	}
	progress := task.Progress
	progress.TargetLEO = report.LogEndOffset
	progress.TargetCheckpointHW = report.CheckpointHW
	if report.LogEndOffset >= progress.LeaderLEO {
		progress.LagRecords = 0
	} else {
		progress.LagRecords = progress.LeaderLEO - report.LogEndOffset
	}
	return e.advanceTask(ctx, task, afterProbeMS, task.Status, slotmeta.ChannelMigrationPhaseWriteFence, advanceTaskOptions{Progress: progress})
}

func (e *Executor) leaderTransferWriteFence(ctx context.Context, task Task, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
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
		Phase:        slotmeta.ChannelMigrationPhaseDrainLeader,
		FenceReason:  uint8(channel.WriteFenceReasonMigration),
		FenceUntilMS: afterReadMS + e.cfg.FenceLease.Milliseconds(),
		UpdatedAtMS:  nextUpdatedAtMS(afterReadMS, task.UpdatedAtMS),
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
	afterReadMS := e.freshNowMS(nowMS)
	if e.fenceExpired(task, meta, afterReadMS) {
		return e.resetExpiredLeaderTransferFence(ctx, task, meta, afterReadMS)
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

func (e *Executor) leaderTransferFinalTargetCatchUp(ctx context.Context, task Task, nowMS int64) error {
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if e.fenceExpired(task, meta, afterReadMS) {
		return e.resetExpiredLeaderTransferFence(ctx, task, meta, afterReadMS)
	}
	if task.DrainedFenceVersion != meta.WriteFenceVersion {
		return e.retryTask(ctx, task, afterReadMS, slotmeta.ErrStaleMeta, task.Progress)
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	targetNode := leaderTransferTargetNode(task)
	report, err := e.probeClient.ProbeChannel(ctx, targetNode, projected)
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
		TargetNode:         targetNode,
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
	return e.advanceTask(ctx, task, afterProbeMS, task.Status, slotmeta.ChannelMigrationPhaseCommitLeaderMeta, advanceTaskOptions{Progress: progress})
}

func (e *Executor) leaderTransferCommitLeaderMeta(ctx context.Context, task Task, nowMS int64) error {
	if err := e.ensureTaskOwnership(ctx, task, nowMS); err != nil {
		return err
	}
	meta, err := e.store.GetChannelRuntimeMeta(ctx, task.ChannelID, task.ChannelType)
	if err != nil {
		return err
	}
	afterReadMS := e.freshNowMS(nowMS)
	if e.fenceExpired(task, meta, afterReadMS) {
		return e.resetExpiredLeaderTransferFence(ctx, task, meta, afterReadMS)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterReadMS); err != nil {
		return err
	}
	req := slotmeta.ChannelMigrationLeaderTransferRequest{
		Guard:           guardFromTask(task),
		RuntimeGuard:    runtimeGuardFromMeta(meta),
		Status:          slotmeta.ChannelMigrationStatusRunning,
		Phase:           slotmeta.ChannelMigrationPhaseVerifyNewLeader,
		DesiredLeader:   channelMigrationDesiredLeader(task),
		NextLeaderEpoch: meta.LeaderEpoch + 1,
		LeaseUntilMS:    afterReadMS + e.cfg.LeaderLease.Milliseconds(),
		NowMS:           afterReadMS,
		UpdatedAtMS:     nextUpdatedAtMS(afterReadMS, task.UpdatedAtMS),
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
	afterReadMS := e.freshNowMS(nowMS)
	if meta.Leader != channelMigrationDesiredLeader(task) {
		return e.retryTask(ctx, task, afterReadMS, channel.ErrNotReady, task.Progress)
	}
	if e.probeClient == nil {
		return fmt.Errorf("%w: probe client", ErrMissingDependency)
	}
	projected := channelmeta.ProjectChannelMeta(meta)
	targetNode := leaderTransferTargetNode(task)
	report, err := e.probeClient.ProbeChannel(ctx, targetNode, projected)
	afterProbeMS := e.freshNowMS(afterReadMS)
	if err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if err := validateTargetReportFence(projected, targetNode, report); err != nil {
		return e.retryTask(ctx, task, afterProbeMS, err, task.Progress)
	}
	if report.Leader != channel.NodeID(channelMigrationDesiredLeader(task)) ||
		report.Role != channel.ReplicaRoleLeader ||
		!report.CommitReady {
		return e.retryTask(ctx, task, afterProbeMS, channel.ErrNotReady, task.Progress)
	}
	if err := e.ensureTaskOwnership(ctx, task, afterProbeMS); err != nil {
		return err
	}
	status := slotmeta.ChannelMigrationStatusCompleted
	phase := slotmeta.ChannelMigrationPhaseClearFence
	completedAtMS := nextUpdatedAtMS(afterProbeMS, task.UpdatedAtMS)
	if task.Kind == slotmeta.ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer {
		status = slotmeta.ChannelMigrationStatusRunning
		phase = slotmeta.ChannelMigrationPhaseAddLearner
		completedAtMS = 0
	}
	req := slotmeta.ChannelMigrationClearFenceRequest{
		Guard:         guardFromTask(task),
		RuntimeGuard:  runtimeGuardFromMeta(meta),
		Status:        status,
		Phase:         phase,
		UpdatedAtMS:   nextUpdatedAtMS(afterProbeMS, task.UpdatedAtMS),
		CompletedAtMS: completedAtMS,
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
	Progress              slotmeta.ChannelMigrationProgress
	CutoverProof          slotmeta.ChannelMigrationCutoverProof
	EmbeddedDesiredLeader uint64
	LastError             string
	BlockerCode           string
	BlockerMsg            string
	NextRunAtMS           int64
	CompletedAt           int64
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
		Guard:                 guardFromTask(task),
		Status:                status,
		Phase:                 phase,
		Attempt:               task.Attempt + 1,
		NextRunAtMS:           opts.NextRunAtMS,
		BlockerCode:           opts.BlockerCode,
		BlockerMessage:        opts.BlockerMsg,
		LastError:             opts.LastError,
		UpdatedAtMS:           nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
		CompletedAtMS:         opts.CompletedAt,
		Progress:              progress,
		CutoverProof:          opts.CutoverProof,
		EmbeddedDesiredLeader: opts.EmbeddedDesiredLeader,
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

func (e *Executor) blockTask(ctx context.Context, task Task, nowMS int64, code, message string, progress slotmeta.ChannelMigrationProgress) error {
	e.metrics.RecordBlocker(task, code)
	return e.advanceTask(ctx, task, nowMS, slotmeta.ChannelMigrationStatusBlocked, task.Phase, advanceTaskOptions{
		Progress:    progress,
		BlockerCode: code,
		BlockerMsg:  message,
		LastError:   message,
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
	e.log.Info("channel migration phase transition",
		wklog.String("taskID", task.TaskID),
		wklog.String("channelKey", string(channelKeyFromTask(task))),
		wklog.ChannelID(task.ChannelID),
		wklog.ChannelType(task.ChannelType),
		wklog.Uint64("ownerNodeID", task.OwnerNodeID),
		wklog.String("fenceToken", task.FenceToken),
		wklog.Uint64("fenceVersion", task.FenceVersion),
		wklog.String("fromPhase", migrationPhaseString(task.Phase)),
		wklog.String("toPhase", migrationPhaseString(phase)),
		wklog.Uint64("baseChannelEpoch", task.BaseChannelEpoch),
		wklog.Uint64("baseLeaderEpoch", task.BaseLeaderEpoch),
		wklog.Uint64("drainedChannelEpoch", task.DrainedChannelEpoch),
		wklog.Uint64("drainedLeaderEpoch", task.DrainedLeaderEpoch),
	)
}

func (e *Executor) recordLeaderTransferCommand(message string, task Task, phase slotmeta.ChannelMigrationPhase, fenceVersion uint64) {
	e.log.Info(message,
		wklog.String("taskID", task.TaskID),
		wklog.String("channelKey", string(channelKeyFromTask(task))),
		wklog.ChannelID(task.ChannelID),
		wklog.ChannelType(task.ChannelType),
		wklog.Uint64("ownerNodeID", task.OwnerNodeID),
		wklog.String("fenceToken", task.FenceToken),
		wklog.Uint64("fenceVersion", fenceVersion),
		wklog.String("fromPhase", migrationPhaseString(task.Phase)),
		wklog.String("toPhase", migrationPhaseString(phase)),
		wklog.Uint64("baseChannelEpoch", task.BaseChannelEpoch),
		wklog.Uint64("baseLeaderEpoch", task.BaseLeaderEpoch),
		wklog.Uint64("drainedChannelEpoch", task.DrainedChannelEpoch),
		wklog.Uint64("drainedLeaderEpoch", task.DrainedLeaderEpoch),
	)
}

func channelKeyFromTask(task Task) channel.ChannelKey {
	return channelhandler.KeyFromChannelID(channel.ChannelID{ID: task.ChannelID, Type: uint8(task.ChannelType)})
}

func migrationPhaseString(phase slotmeta.ChannelMigrationPhase) string {
	switch phase {
	case slotmeta.ChannelMigrationPhaseValidate:
		return "Validate"
	case slotmeta.ChannelMigrationPhaseProbeTarget:
		return "ProbeTarget"
	case slotmeta.ChannelMigrationPhaseWriteFence:
		return "WriteFence"
	case slotmeta.ChannelMigrationPhaseDrainLeader:
		return "DrainLeader"
	case slotmeta.ChannelMigrationPhaseFinalTargetCatchUp:
		return "FinalTargetCatchUp"
	case slotmeta.ChannelMigrationPhaseCommitLeaderMeta:
		return "CommitLeaderMeta"
	case slotmeta.ChannelMigrationPhaseVerifyNewLeader:
		return "VerifyNewLeader"
	case slotmeta.ChannelMigrationPhaseClearFence:
		return "ClearFence"
	case slotmeta.ChannelMigrationPhaseAddLearner:
		return "AddLearner"
	case slotmeta.ChannelMigrationPhaseBootstrapTarget:
		return "BootstrapTarget"
	case slotmeta.ChannelMigrationPhaseWarmCatchUp:
		return "WarmCatchUp"
	case slotmeta.ChannelMigrationPhaseCutoverFence:
		return "CutoverFence"
	case slotmeta.ChannelMigrationPhasePromoteAndRemove:
		return "PromoteAndRemove"
	case slotmeta.ChannelMigrationPhaseVerifyMembership:
		return "VerifyMembership"
	default:
		return fmt.Sprintf("Unknown(%d)", phase)
	}
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

func leaderTransferTargetNode(task Task) channel.NodeID {
	if task.EmbeddedLeaderTransfer && task.EmbeddedDesiredLeader != 0 {
		return channel.NodeID(task.EmbeddedDesiredLeader)
	}
	return channel.NodeID(task.TargetNode)
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
