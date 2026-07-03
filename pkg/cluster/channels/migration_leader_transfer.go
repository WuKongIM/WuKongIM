package channels

import (
	"context"
	"fmt"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	migrationBlockInvalidLeaderTransfer = "invalid_leader_transfer"
	migrationBlockTargetNotReady        = "target_not_ready"
	migrationBlockTargetLagging         = "target_lagging"
	migrationBlockDrainNotComplete      = "drain_not_complete"
	migrationBlockNewLeaderNotReady     = "new_leader_not_ready"
)

func (e *MigrationExecutor) runLeaderTransferPhase(ctx context.Context, task metadb.ChannelMigrationTask) error {
	switch task.Phase {
	case metadb.ChannelMigrationPhaseValidate:
		return e.runLeaderTransferValidate(ctx, task)
	case metadb.ChannelMigrationPhaseProbeTarget:
		return e.runLeaderTransferProbeTarget(ctx, task)
	case metadb.ChannelMigrationPhaseWriteFence:
		return e.store.SetWriteFence(ctx, task, migrationFenceReasonForTask(task))
	case metadb.ChannelMigrationPhaseDrainLeader:
		return e.runLeaderTransferDrainLeader(ctx, task)
	case metadb.ChannelMigrationPhaseFinalTargetCatchUp:
		return e.runLeaderTransferFinalTargetCatchUp(ctx, task)
	case metadb.ChannelMigrationPhaseCommitLeaderMeta:
		return e.runLeaderTransferCommitLeaderMeta(ctx, task)
	case metadb.ChannelMigrationPhaseVerifyNewLeader:
		return e.runLeaderTransferVerifyNewLeader(ctx, task)
	default:
		return e.blockTask(ctx, task, migrationBlockInvalidLeaderTransfer)
	}
}

func (e *MigrationExecutor) runLeaderTransferValidate(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, _, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if err := validateLeaderTransferTask(task, meta); err != nil {
		return e.blockTask(ctx, task, migrationBlockInvalidLeaderTransfer)
	}
	return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseProbeTarget, metadb.ChannelMigrationStatusRunning, "")
}

func (e *MigrationExecutor) runLeaderTransferProbeTarget(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if task.Kind == metadb.ChannelMigrationKindLeaderFailover {
		return e.runLeaderFailoverProbeTarget(ctx, task, meta, id)
	}
	source, err := e.runtime.ProbeChannel(ctx, task.SourceNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderTransferRuntimeProbe(source, meta, ch.RoleLeader); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderTransferRuntimeProbe(probe, meta, ch.RoleFollower); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	if probe.HW < source.HW {
		return e.blockTask(ctx, task, migrationBlockTargetLagging)
	}
	return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseWriteFence, metadb.ChannelMigrationStatusRunning, "")
}

func (e *MigrationExecutor) runLeaderTransferDrainLeader(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if task.FenceVersion == 0 || task.FenceToken != task.TaskID || meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		return e.blockTask(ctx, task, migrationBlockDrainNotComplete)
	}
	if meta.Leader != task.SourceNode {
		return e.blockTask(ctx, task, migrationBlockInvalidLeaderTransfer)
	}
	if task.Kind == metadb.ChannelMigrationKindLeaderFailover {
		return e.advanceLeaderFailoverTargetProof(ctx, task, meta, id)
	}
	return e.advanceLeaderTransferDrainProof(ctx, task, meta, id, metadb.ChannelMigrationPhaseFinalTargetCatchUp)
}

func (e *MigrationExecutor) runLeaderTransferFinalTargetCatchUp(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if !taskHasCutoverProof(task) {
		return e.advanceLeaderTransferDrainProof(ctx, task, meta, id, metadb.ChannelMigrationPhaseFinalTargetCatchUp)
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
	return e.store.AdvanceWithProof(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseCommitLeaderMeta, metadb.ChannelMigrationStatusRunning, "", progress, taskCutoverProof(task))
}

func (e *MigrationExecutor) runLeaderTransferCommitLeaderMeta(ctx context.Context, task metadb.ChannelMigrationTask) error {
	if !taskHasCutoverProof(task) || task.DrainedFenceVersion != task.FenceVersion {
		return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseFinalTargetCatchUp, metadb.ChannelMigrationStatusRunning, "")
	}
	return e.store.CommitLeaderTransfer(ctx, task)
}

func (e *MigrationExecutor) runLeaderTransferVerifyNewLeader(ctx context.Context, task metadb.ChannelMigrationTask) error {
	meta, id, err := e.readLeaderTransferMeta(ctx, task)
	if err != nil {
		return err
	}
	if !taskHasCutoverProof(task) || task.DrainedFenceVersion != task.FenceVersion {
		return e.blockTask(ctx, task, migrationBlockInvalidLeaderTransfer)
	}
	if meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		return e.blockTask(ctx, task, migrationBlockNewLeaderNotReady)
	}
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if probe.ChannelID != id ||
		probe.ChannelEpoch != meta.ChannelEpoch ||
		probe.LeaderEpoch != meta.LeaderEpoch ||
		probe.Role != ch.RoleLeader ||
		probe.Status != ch.StatusActive ||
		meta.Leader != task.TargetNode ||
		probe.HW < task.CutoverLEO ||
		probe.LEO < task.CutoverLEO ||
		!writeFenceMatchesTask(probe.WriteFence, task) {
		// The committed leader metadata may reach the new runtime before its HW,
		// LEO, and fence snapshot are fully visible. Keep the task runnable so
		// the executor can clear the fence once the new leader catches up.
		return nil
	}
	return e.clearWriteFenceAndObserve(ctx, task)
}

func (e *MigrationExecutor) advanceLeaderTransferDrainProof(ctx context.Context, task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, id ch.ChannelID, phase metadb.ChannelMigrationPhase) error {
	if task.FenceVersion == 0 || task.FenceToken != task.TaskID || meta.WriteFenceToken != task.TaskID || meta.WriteFenceVersion != task.FenceVersion {
		return e.blockTask(ctx, task, migrationBlockDrainNotComplete)
	}
	if meta.Leader != task.SourceNode {
		return e.blockTask(ctx, task, migrationBlockInvalidLeaderTransfer)
	}
	result, err := e.runtime.DrainChannel(ctx, task.SourceNode, ch.DrainChannelRequest{
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
		DrainedLeaderNode:        task.SourceNode,
		DrainedRuntimeGeneration: meta.RouteGeneration,
		DrainedChannelEpoch:      meta.ChannelEpoch,
		DrainedLeaderEpoch:       meta.LeaderEpoch,
		DrainedFenceVersion:      task.FenceVersion,
	}
	progress := metadb.ChannelMigrationProgress{LeaderLEO: result.LEO, LeaderHW: result.HW}
	return e.store.AdvanceWithProof(ctx, task, task.UpdatedAtMS, phase, metadb.ChannelMigrationStatusRunning, "", progress, proof)
}

func (e *MigrationExecutor) runLeaderFailoverProbeTarget(ctx context.Context, task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, id ch.ChannelID) error {
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderFailoverRuntimeProbe(probe, meta, ch.RoleFollower); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	if leaderFailoverTargetLagging(task, probe) {
		return e.blockTask(ctx, task, migrationBlockTargetLagging)
	}
	return e.store.Advance(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseWriteFence, metadb.ChannelMigrationStatusRunning, "")
}

func (e *MigrationExecutor) advanceLeaderFailoverTargetProof(ctx context.Context, task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, id ch.ChannelID) error {
	probe, err := e.runtime.ProbeChannel(ctx, task.TargetNode, id.ID, id.Type)
	if err != nil {
		return err
	}
	if err := validateLeaderFailoverRuntimeProbe(probe, meta, ch.RoleFollower); err != nil {
		return e.blockTask(ctx, task, migrationBlockTargetNotReady)
	}
	if leaderFailoverTargetLagging(task, probe) {
		return e.blockTask(ctx, task, migrationBlockTargetLagging)
	}
	cutoverHW := probe.HW
	proof := metadb.ChannelMigrationCutoverProof{
		CutoverLEO:               cutoverHW,
		CutoverHW:                cutoverHW,
		DrainedLeaderNode:        task.SourceNode,
		DrainedRuntimeGeneration: meta.RouteGeneration,
		DrainedChannelEpoch:      meta.ChannelEpoch,
		DrainedLeaderEpoch:       meta.LeaderEpoch,
		DrainedFenceVersion:      task.FenceVersion,
	}
	progress := metadb.ChannelMigrationProgress{
		LeaderLEO:          cutoverHW,
		LeaderHW:           cutoverHW,
		TargetLEO:          probe.LEO,
		TargetCheckpointHW: probe.CheckpointHW,
	}
	return e.store.AdvanceWithProof(ctx, task, task.UpdatedAtMS, metadb.ChannelMigrationPhaseCommitLeaderMeta, metadb.ChannelMigrationStatusRunning, "", progress, proof)
}

func (e *MigrationExecutor) readLeaderTransferMeta(ctx context.Context, task metadb.ChannelMigrationTask) (metadb.ChannelRuntimeMeta, ch.ChannelID, error) {
	id, err := migrationChannelIDFromTask(task)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, ch.ChannelID{}, err
	}
	meta, err := e.meta.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, ch.ChannelID{}, err
	}
	return metadb.NormalizeChannelRuntimeMeta(meta), id, nil
}

func (e *MigrationExecutor) blockTask(ctx context.Context, task metadb.ChannelMigrationTask, reason string) error {
	if err := e.store.Advance(ctx, task, task.UpdatedAtMS, task.Phase, metadb.ChannelMigrationStatusBlocked, reason); err != nil {
		return err
	}
	e.observeMigrationBlocked(task, reason)
	return nil
}

func validateLeaderTransferTask(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta) error {
	if task.SourceNode == 0 || task.TargetNode == 0 || task.SourceNode == task.TargetNode {
		return fmt.Errorf("%w: invalid source or target", ch.ErrInvalidConfig)
	}
	if meta.Leader != task.SourceNode {
		return fmt.Errorf("%w: source is not current leader", ch.ErrInvalidConfig)
	}
	if err := validateLeaderTransferTarget(meta, task.TargetNode); err != nil {
		return err
	}
	if int64(len(meta.ISR)) < meta.MinISR {
		return fmt.Errorf("%w: min ISR is not satisfied", ch.ErrInvalidConfig)
	}
	if meta.WriteFenceToken != "" && meta.WriteFenceToken != task.TaskID {
		return fmt.Errorf("%w: foreign write fence", ch.ErrInvalidConfig)
	}
	return nil
}

func validateLeaderTransferRuntimeProbe(probe ch.RuntimeProbeChannel, meta metadb.ChannelRuntimeMeta, role ch.Role) error {
	if probe.ChannelID.ID != meta.ChannelID ||
		int64(probe.ChannelID.Type) != meta.ChannelType ||
		probe.ChannelEpoch != meta.ChannelEpoch ||
		probe.LeaderEpoch != meta.LeaderEpoch ||
		probe.Role != role ||
		probe.Status != ch.StatusActive {
		return fmt.Errorf("%w: target runtime proof mismatch", ch.ErrNotReady)
	}
	return nil
}

func validateLeaderFailoverRuntimeProbe(probe ch.RuntimeProbeChannel, meta metadb.ChannelRuntimeMeta, role ch.Role) error {
	if probe.ChannelID.ID != meta.ChannelID ||
		int64(probe.ChannelID.Type) != meta.ChannelType ||
		probe.ChannelEpoch != meta.ChannelEpoch ||
		probe.Role != role ||
		probe.Status != ch.StatusActive {
		return fmt.Errorf("%w: failover runtime proof mismatch", ch.ErrNotReady)
	}
	if leaderFailoverEpochCompatible(probe.LeaderEpoch, meta.LeaderEpoch) {
		return nil
	}
	return fmt.Errorf("%w: failover leader epoch mismatch", ch.ErrNotReady)
}

func leaderFailoverEpochCompatible(observed uint64, current uint64) bool {
	if observed == 0 {
		return false
	}
	if observed == current {
		return true
	}
	return current > 0 && observed+1 == current
}

func leaderFailoverTargetLagging(task metadb.ChannelMigrationTask, probe ch.RuntimeProbeChannel) bool {
	return task.Progress.LeaderHW > 0 && probe.HW < task.Progress.LeaderHW
}

func taskHasCutoverProof(task metadb.ChannelMigrationTask) bool {
	return task.DrainedLeaderNode != 0 &&
		task.DrainedRuntimeGeneration != 0 &&
		task.DrainedChannelEpoch != 0 &&
		task.DrainedLeaderEpoch != 0 &&
		task.DrainedFenceVersion != 0 &&
		task.CutoverHW <= task.CutoverLEO
}

func writeFenceMatchesTask(fence ch.WriteFence, task metadb.ChannelMigrationTask) bool {
	return fence.Token == task.TaskID && fence.Version == task.FenceVersion && fence.Set()
}

func taskCutoverProof(task metadb.ChannelMigrationTask) metadb.ChannelMigrationCutoverProof {
	return metadb.ChannelMigrationCutoverProof{
		CutoverLEO:               task.CutoverLEO,
		CutoverHW:                task.CutoverHW,
		DrainedLeaderNode:        task.DrainedLeaderNode,
		DrainedRuntimeGeneration: task.DrainedRuntimeGeneration,
		DrainedChannelEpoch:      task.DrainedChannelEpoch,
		DrainedLeaderEpoch:       task.DrainedLeaderEpoch,
		DrainedFenceVersion:      task.DrainedFenceVersion,
	}
}
