package meta

import "reflect"

// SetChannelWriteFence stages a write-fence set or renewal with the matching
// task phase transition in the same batch.
func (b *WriteBatch) SetChannelWriteFence(hashSlot uint16, req ChannelMigrationFenceRequest) error {
	if err := validateChannelMigrationFenceRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationSetFenceTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireNoForeignChannelMigrationFence(task, meta); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		nextTask := clearChannelMigrationTaskProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.FenceToken = task.TaskID
		nextTask.FenceVersion = meta.WriteFenceVersion + 1
		nextTask.FenceUntilMS = req.FenceUntilMS
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		nextMeta.WriteFenceToken = task.TaskID
		nextMeta.WriteFenceVersion = meta.WriteFenceVersion + 1
		nextMeta.WriteFenceReason = req.FenceReason
		nextMeta.WriteFenceUntilMS = req.FenceUntilMS
		return nextTask, nextMeta, nil
	})
}

// ResetChannelWriteFenceToPreCutover stages recovery from an expired matching
// cutover fence by clearing task proof fields and bumping the fence version.
func (b *WriteBatch) ResetChannelWriteFenceToPreCutover(hashSlot uint16, req ChannelMigrationResetFenceRequest) error {
	if err := validateChannelMigrationResetFenceRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationResetFenceTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, 0, true); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.NowMS <= meta.WriteFenceUntilMS {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, ErrStaleMeta
		}
		nextTask := clearChannelMigrationTaskFenceAndProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := clearChannelRuntimeMetaFence(meta)
		return nextTask, nextMeta, nil
	})
}

// CommitChannelLeaderTransfer stages a fenced leader metadata change with the
// matching task phase transition.
func (b *WriteBatch) CommitChannelLeaderTransfer(hashSlot uint16, req ChannelMigrationLeaderTransferRequest) error {
	if err := validateChannelMigrationLeaderTransferRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationLeaderTransferTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, req.NowMS, false); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireChannelMigrationCutoverProof(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.DesiredLeader != channelMigrationTaskDesiredLeader(task) || !containsUint64(meta.ISR, req.DesiredLeader) || req.NextLeaderEpoch <= meta.LeaderEpoch {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, ErrStaleMeta
		}
		nextTask := task
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		nextMeta.Leader = req.DesiredLeader
		nextMeta.LeaderEpoch = req.NextLeaderEpoch
		nextMeta.LeaseUntilMS = req.LeaseUntilMS
		return nextTask, nextMeta, nil
	})
}

// AddChannelLearner stages a learner addition by adding TargetNode to Replicas
// and incrementing ChannelEpoch.
func (b *WriteBatch) AddChannelLearner(hashSlot uint16, req ChannelMigrationAddLearnerRequest) error {
	if err := validateChannelMigrationAddLearnerRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationAddLearnerTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.TargetNode != task.TargetNode ||
			!containsUint64(meta.Replicas, task.SourceNode) ||
			!containsUint64(meta.ISR, task.SourceNode) ||
			containsUint64(meta.Replicas, req.TargetNode) ||
			containsUint64(meta.ISR, req.TargetNode) {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, ErrStaleMeta
		}
		nextTask := task
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		if !containsUint64(nextMeta.Replicas, req.TargetNode) {
			nextMeta.Replicas = append(nextMeta.Replicas, req.TargetNode)
			nextMeta.ChannelEpoch++
		}
		return nextTask, nextMeta, nil
	})
}

// PromoteLearnerAndRemoveReplica stages a fenced source-to-target replacement.
func (b *WriteBatch) PromoteLearnerAndRemoveReplica(hashSlot uint16, req ChannelMigrationPromoteLearnerRequest) error {
	if err := validateChannelMigrationPromoteLearnerRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationPromoteLearnerTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, req.NowMS, false); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireChannelMigrationCutoverProof(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.SourceNode != task.SourceNode ||
			req.TargetNode != task.TargetNode ||
			!containsUint64(meta.Replicas, req.SourceNode) ||
			!containsUint64(meta.Replicas, req.TargetNode) ||
			!containsUint64(meta.ISR, req.SourceNode) ||
			containsUint64(meta.ISR, req.TargetNode) {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, ErrStaleMeta
		}
		nextTask := task
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		nextMeta.Replicas = replaceUint64Member(nextMeta.Replicas, req.SourceNode, req.TargetNode)
		nextMeta.ISR = replaceUint64Member(nextMeta.ISR, req.SourceNode, req.TargetNode)
		nextMeta.ChannelEpoch++
		return nextTask, nextMeta, nil
	})
}

// ClearChannelWriteFence stages a matching fence clear and task advance.
func (b *WriteBatch) ClearChannelWriteFence(hashSlot uint16, req ChannelMigrationClearFenceRequest) error {
	if err := validateChannelMigrationClearFenceRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationClearFenceTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if isChannelMigrationClearFenceIdempotent(task, meta, req) {
			return task, meta, nil
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, 0, true); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		nextTask := clearChannelMigrationTaskFenceAndProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS
		nextTask.CompletedAtMS = req.CompletedAtMS

		nextMeta := clearChannelRuntimeMetaFence(meta)
		return nextTask, nextMeta, nil
	})
}

// AbortChannelMigration stages a terminal abort and removes safe side effects.
func (b *WriteBatch) AbortChannelMigration(hashSlot uint16, req ChannelMigrationAbortRequest) error {
	if err := validateChannelMigrationAbortRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if task.IsTerminal() {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, ErrStaleMeta
		}
		if err := requireChannelMigrationAbortTransition(task); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		nextTask := clearChannelMigrationTaskFenceAndProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS
		nextTask.CompletedAtMS = req.CompletedAtMS
		nextTask.LastError = req.LastError

		nextMeta := meta
		if meta.WriteFenceToken != "" {
			if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
				return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
			}
			if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, 0, true); err != nil {
				return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
			}
			nextMeta = clearChannelRuntimeMetaFence(nextMeta)
		} else if task.FenceToken != "" || task.FenceVersion != 0 || task.FenceUntilMS != 0 {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, ErrStaleMeta
		}
		if task.Kind == ChannelMigrationKindReplicaReplace &&
			canAbortRemoveUnpromotedChannelMigrationLearner(task) &&
			containsUint64(nextMeta.Replicas, task.TargetNode) &&
			!containsUint64(nextMeta.ISR, task.TargetNode) {
			nextMeta.Replicas = removeUint64Member(nextMeta.Replicas, task.TargetNode)
			nextMeta.ChannelEpoch++
		}
		return nextTask, nextMeta, nil
	})
}

type channelMigrationTaskMetaMutator func(ChannelMigrationTask, ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error)

func (b *WriteBatch) stageChannelMigrationTaskAndMeta(hashSlot uint16, guard ChannelMigrationTaskGuard, runtimeGuard ChannelMigrationRuntimeGuard, mutate channelMigrationTaskMetaMutator) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelMigrationTaskGuard(guard); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(runtimeGuard); err != nil {
		return err
	}
	if guard.ChannelID != runtimeGuard.ChannelID || guard.ChannelType != runtimeGuard.ChannelType {
		return ErrInvalidArgument
	}
	taskKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, guard.ChannelID, guard.ChannelType, guard.TaskID, channelMigrationTaskPrimaryFamilyID)
	taskGuardDBState := !b.isChannelMigrationTaskWritten(taskKey)
	task, exists, err := b.loadChannelMigrationTask(hashSlot, taskKey, guard.ChannelID, guard.ChannelType, guard.TaskID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	metaKey := encodeChannelRuntimeMetaPrimaryKey(hashSlot, runtimeGuard.ChannelID, runtimeGuard.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	runtimeGuardDBState := !b.isChannelRuntimeMetaWritten(hashSlot, runtimeGuard.ChannelID, runtimeGuard.ChannelType)
	meta, exists, err := b.loadChannelRuntimeMeta(hashSlot, metaKey, runtimeGuard.ChannelID, runtimeGuard.ChannelType)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	nextTask, nextMeta, err := mutate(task, meta)
	if err != nil {
		return err
	}
	nextMeta = normalizeChannelRuntimeMeta(nextMeta)
	if !guard.matches(task) || !runtimeGuard.matches(meta) {
		if task == nextTask && channelRuntimeMetaEqual(meta, nextMeta) {
			return nil
		}
		return ErrStaleMeta
	}
	if task.IsTerminal() && task != nextTask {
		return ErrStaleMeta
	}
	if err := validateChannelMigrationTask(nextTask); err != nil {
		return err
	}
	if err := validateChannelRuntimeMeta(nextMeta); err != nil {
		return err
	}
	if taskGuardDBState || b.hasChannelMigrationTaskGuard(taskKey) {
		b.rememberChannelMigrationTaskGuard(taskKey, guard, nextTask)
	}
	if runtimeGuardDBState || b.hasChannelMigrationRuntimeGuard(metaKey) {
		b.rememberChannelMigrationRuntimeGuard(metaKey, runtimeGuard, nextMeta)
	}
	if err := b.upsertChannelMigrationTask(hashSlot, nextTask); err != nil {
		return err
	}
	return b.UpsertChannelRuntimeMeta(hashSlot, nextMeta)
}

func validateChannelMigrationRuntimeGuard(guard ChannelMigrationRuntimeGuard) error {
	if err := validateChannelRuntimeMetaChannelID(guard.ChannelID); err != nil {
		return err
	}
	return nil
}

func validateChannelMigrationFenceRequest(req ChannelMigrationFenceRequest) error {
	if err := validateChannelMigrationTaskGuard(req.Guard); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(req.RuntimeGuard); err != nil {
		return err
	}
	if !isValidChannelMigrationStatus(req.Status) || !isValidChannelMigrationPhase(req.Phase) || req.FenceReason == 0 || req.FenceUntilMS <= 0 || req.UpdatedAtMS <= req.Guard.ExpectedUpdatedAtMS {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationResetFenceRequest(req ChannelMigrationResetFenceRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.NowMS <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationLeaderTransferRequest(req ChannelMigrationLeaderTransferRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.DesiredLeader == 0 || req.NextLeaderEpoch == 0 || req.LeaseUntilMS <= 0 || req.NowMS <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationAddLearnerRequest(req ChannelMigrationAddLearnerRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.TargetNode == 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationPromoteLearnerRequest(req ChannelMigrationPromoteLearnerRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.SourceNode == 0 || req.TargetNode == 0 || req.SourceNode == req.TargetNode || req.NowMS <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationClearFenceRequest(req ChannelMigrationClearFenceRequest) error {
	return validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, req.CompletedAtMS)
}

func validateChannelMigrationAbortRequest(req ChannelMigrationAbortRequest) error {
	if req.Status != ChannelMigrationStatusAborted {
		return ErrInvalidArgument
	}
	return validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, req.CompletedAtMS)
}

func validateChannelMigrationTaskRuntimeTransition(guard ChannelMigrationTaskGuard, runtimeGuard ChannelMigrationRuntimeGuard, status ChannelMigrationStatus, phase ChannelMigrationPhase, updatedAtMS, completedAtMS int64) error {
	if err := validateChannelMigrationTaskGuard(guard); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(runtimeGuard); err != nil {
		return err
	}
	if !isValidChannelMigrationStatus(status) || !isValidChannelMigrationPhase(phase) || updatedAtMS <= guard.ExpectedUpdatedAtMS {
		return ErrInvalidArgument
	}
	if status == ChannelMigrationStatusCompleted || status == ChannelMigrationStatusFailed || status == ChannelMigrationStatusAborted {
		if completedAtMS <= 0 {
			return ErrInvalidArgument
		}
	}
	return nil
}

func requireChannelMigrationSetFenceTransition(task ChannelMigrationTask, req ChannelMigrationFenceRequest) error {
	if req.Status != ChannelMigrationStatusRunning {
		return ErrStaleMeta
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer || (task.Kind == ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer && isLeaderTransferPhase(task.Phase)) {
		if (task.Phase == ChannelMigrationPhaseWriteFence && req.Phase == ChannelMigrationPhaseDrainLeader) ||
			(isLeaderTransferFencePhase(task.Phase) && req.Phase == task.Phase) {
			return nil
		}
		return ErrStaleMeta
	}
	if task.Kind == ChannelMigrationKindReplicaReplace {
		if (task.Phase == ChannelMigrationPhaseWarmCatchUp && req.Phase == ChannelMigrationPhaseCutoverFence) ||
			(isReplicaReplaceFencePhase(task.Phase) && req.Phase == task.Phase) {
			return nil
		}
	}
	return ErrStaleMeta
}

func requireChannelMigrationResetFenceTransition(task ChannelMigrationTask, req ChannelMigrationResetFenceRequest) error {
	if req.Status != ChannelMigrationStatusRunning || !isChannelMigrationFencePhaseAllowed(task) {
		return ErrStaleMeta
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer || (task.Kind == ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer && isLeaderTransferFencePhase(task.Phase)) {
		if req.Phase == ChannelMigrationPhaseProbeTarget || req.Phase == ChannelMigrationPhaseWriteFence {
			return nil
		}
		return ErrStaleMeta
	}
	if task.Kind == ChannelMigrationKindReplicaReplace && req.Phase == ChannelMigrationPhaseWarmCatchUp {
		return nil
	}
	return ErrStaleMeta
}

func requireChannelMigrationLeaderTransferTransition(task ChannelMigrationTask, req ChannelMigrationLeaderTransferRequest) error {
	if req.Status != ChannelMigrationStatusRunning || task.Phase != ChannelMigrationPhaseCommitLeaderMeta || req.Phase != ChannelMigrationPhaseVerifyNewLeader {
		return ErrStaleMeta
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer {
		return nil
	}
	if task.Kind == ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer {
		return nil
	}
	return ErrStaleMeta
}

func requireChannelMigrationAddLearnerTransition(task ChannelMigrationTask, req ChannelMigrationAddLearnerRequest) error {
	if task.Kind != ChannelMigrationKindReplicaReplace ||
		task.Phase != ChannelMigrationPhaseAddLearner ||
		req.Status != ChannelMigrationStatusRunning ||
		req.Phase != ChannelMigrationPhaseBootstrapTarget {
		return ErrStaleMeta
	}
	return nil
}

func requireChannelMigrationPromoteLearnerTransition(task ChannelMigrationTask, req ChannelMigrationPromoteLearnerRequest) error {
	if task.Kind != ChannelMigrationKindReplicaReplace ||
		task.Phase != ChannelMigrationPhasePromoteAndRemove ||
		req.Status != ChannelMigrationStatusRunning ||
		req.Phase != ChannelMigrationPhaseVerifyMembership {
		return ErrStaleMeta
	}
	return nil
}

func requireChannelMigrationClearFenceTransition(task ChannelMigrationTask, req ChannelMigrationClearFenceRequest) error {
	if !isChannelMigrationFencePhaseAllowed(task) {
		return ErrStaleMeta
	}
	if req.Status == ChannelMigrationStatusCompleted && req.Phase == ChannelMigrationPhaseClearFence && req.CompletedAtMS > 0 {
		switch task.Kind {
		case ChannelMigrationKindLeaderTransfer:
			if task.Phase == ChannelMigrationPhaseVerifyNewLeader || (task.IsTerminal() && task.Phase == ChannelMigrationPhaseClearFence) {
				return nil
			}
		case ChannelMigrationKindReplicaReplace:
			if task.Phase == ChannelMigrationPhaseVerifyMembership || (task.IsTerminal() && task.Phase == ChannelMigrationPhaseClearFence) {
				return nil
			}
		}
		return ErrStaleMeta
	}
	if task.Kind == ChannelMigrationKindReplicaReplace &&
		task.EmbeddedLeaderTransfer &&
		task.Phase == ChannelMigrationPhaseVerifyNewLeader &&
		req.Status == ChannelMigrationStatusRunning &&
		req.Phase == ChannelMigrationPhaseAddLearner &&
		req.CompletedAtMS == 0 {
		return nil
	}
	return ErrStaleMeta
}

func isChannelMigrationClearFenceIdempotent(task ChannelMigrationTask, meta ChannelRuntimeMeta, req ChannelMigrationClearFenceRequest) bool {
	if req.Status != ChannelMigrationStatusCompleted ||
		req.Phase != ChannelMigrationPhaseClearFence ||
		req.CompletedAtMS <= 0 ||
		!task.IsTerminal() ||
		task.Status != ChannelMigrationStatusCompleted ||
		task.Phase != ChannelMigrationPhaseClearFence ||
		task.UpdatedAtMS != req.UpdatedAtMS ||
		task.CompletedAtMS != req.CompletedAtMS {
		return false
	}
	if task.FenceToken != "" ||
		task.FenceVersion != 0 ||
		task.FenceUntilMS != 0 ||
		task.CutoverLEO != 0 ||
		task.CutoverHW != 0 ||
		task.DrainedLeaderNode != 0 ||
		task.DrainedRuntimeGeneration != 0 ||
		task.DrainedChannelEpoch != 0 ||
		task.DrainedLeaderEpoch != 0 ||
		task.DrainedFenceVersion != 0 {
		return false
	}
	return meta.ChannelID == req.RuntimeGuard.ChannelID &&
		meta.ChannelType == req.RuntimeGuard.ChannelType &&
		meta.ChannelEpoch == req.RuntimeGuard.ExpectedChannelEpoch &&
		meta.LeaderEpoch == req.RuntimeGuard.ExpectedLeaderEpoch &&
		meta.Leader == req.RuntimeGuard.ExpectedLeader &&
		meta.WriteFenceToken == "" &&
		meta.WriteFenceVersion == req.RuntimeGuard.ExpectedFenceVersion+1 &&
		meta.WriteFenceReason == 0 &&
		meta.WriteFenceUntilMS == 0
}

func requireChannelMigrationAbortTransition(task ChannelMigrationTask) error {
	switch task.Kind {
	case ChannelMigrationKindLeaderTransfer:
		if isLeaderTransferAbortPhase(task.Phase) {
			return nil
		}
	case ChannelMigrationKindReplicaReplace:
		if task.EmbeddedLeaderTransfer && isLeaderTransferPhase(task.Phase) {
			if isLeaderTransferAbortPhase(task.Phase) {
				return nil
			}
			return ErrStaleMeta
		}
		if isReplicaReplaceAbortPhase(task.Phase) {
			return nil
		}
	}
	return ErrStaleMeta
}

func canAbortRemoveUnpromotedChannelMigrationLearner(task ChannelMigrationTask) bool {
	switch task.Phase {
	case ChannelMigrationPhaseBootstrapTarget,
		ChannelMigrationPhaseWarmCatchUp,
		ChannelMigrationPhaseCutoverFence,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhasePromoteAndRemove:
		return true
	default:
		return false
	}
}

func isLeaderTransferAbortPhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseValidate,
		ChannelMigrationPhaseProbeTarget,
		ChannelMigrationPhaseWriteFence,
		ChannelMigrationPhaseDrainLeader,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhaseCommitLeaderMeta:
		return true
	default:
		return false
	}
}

func isReplicaReplaceAbortPhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseValidate,
		ChannelMigrationPhaseAddLearner,
		ChannelMigrationPhaseBootstrapTarget,
		ChannelMigrationPhaseWarmCatchUp,
		ChannelMigrationPhaseCutoverFence,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhasePromoteAndRemove:
		return true
	default:
		return false
	}
}

func (guard ChannelMigrationRuntimeGuard) matches(meta ChannelRuntimeMeta) bool {
	return meta.ChannelID == guard.ChannelID &&
		meta.ChannelType == guard.ChannelType &&
		meta.ChannelEpoch == guard.ExpectedChannelEpoch &&
		meta.LeaderEpoch == guard.ExpectedLeaderEpoch &&
		meta.Leader == guard.ExpectedLeader &&
		meta.WriteFenceToken == guard.ExpectedFenceToken &&
		meta.WriteFenceVersion == guard.ExpectedFenceVersion
}

func requireMatchingFence(meta ChannelRuntimeMeta, token string, version uint64, nowMS int64, allowExpired bool) error {
	if token == "" || version == 0 || meta.WriteFenceToken != token || meta.WriteFenceVersion != version {
		return ErrStaleMeta
	}
	if !allowExpired && nowMS > meta.WriteFenceUntilMS {
		return ErrStaleMeta
	}
	return nil
}

func requireNoForeignChannelMigrationFence(task ChannelMigrationTask, meta ChannelRuntimeMeta) error {
	taskHasFence := task.FenceToken != "" || task.FenceVersion != 0 || task.FenceUntilMS != 0
	metaHasFence := meta.WriteFenceToken != ""
	if !taskHasFence && !metaHasFence {
		return nil
	}
	if !taskHasFence || !metaHasFence {
		return ErrStaleMeta
	}
	return requireActiveChannelMigrationTaskFence(task, meta, meta.WriteFenceVersion)
}

func requireActiveChannelMigrationTaskFence(task ChannelMigrationTask, meta ChannelRuntimeMeta, expectedFenceVersion uint64) error {
	if task.FenceToken == "" ||
		task.FenceVersion == 0 ||
		task.FenceUntilMS <= 0 ||
		task.FenceToken != task.TaskID ||
		task.FenceToken != meta.WriteFenceToken ||
		task.FenceVersion != meta.WriteFenceVersion ||
		task.FenceVersion != expectedFenceVersion {
		return ErrStaleMeta
	}
	return nil
}

func requireChannelMigrationCutoverProof(task ChannelMigrationTask, meta ChannelRuntimeMeta, expectedFenceVersion uint64) error {
	proof := ChannelMigrationCutoverProof{
		CutoverLEO:               task.CutoverLEO,
		CutoverHW:                task.CutoverHW,
		DrainedLeaderNode:        task.DrainedLeaderNode,
		DrainedRuntimeGeneration: task.DrainedRuntimeGeneration,
		DrainedChannelEpoch:      task.DrainedChannelEpoch,
		DrainedLeaderEpoch:       task.DrainedLeaderEpoch,
		DrainedFenceVersion:      task.DrainedFenceVersion,
	}
	if expectedFenceVersion == 0 || !proof.hasAny() || proof.hasPartial() {
		return ErrStaleMeta
	}
	if task.DrainedFenceVersion != expectedFenceVersion ||
		meta.WriteFenceVersion != expectedFenceVersion ||
		task.DrainedChannelEpoch != meta.ChannelEpoch ||
		task.DrainedLeaderEpoch != meta.LeaderEpoch ||
		task.DrainedLeaderNode != meta.Leader ||
		task.CutoverHW > task.CutoverLEO {
		return ErrStaleMeta
	}
	return nil
}

func channelMigrationTaskDesiredLeader(task ChannelMigrationTask) uint64 {
	if task.EmbeddedLeaderTransfer && task.EmbeddedDesiredLeader != 0 {
		return task.EmbeddedDesiredLeader
	}
	return task.DesiredLeader
}

func clearChannelRuntimeMetaFence(meta ChannelRuntimeMeta) ChannelRuntimeMeta {
	meta.WriteFenceToken = ""
	meta.WriteFenceVersion++
	meta.WriteFenceReason = 0
	meta.WriteFenceUntilMS = 0
	return meta
}

func clearChannelMigrationTaskFenceAndProof(task ChannelMigrationTask) ChannelMigrationTask {
	task.FenceToken = ""
	task.FenceVersion = 0
	task.FenceUntilMS = 0
	return clearChannelMigrationTaskProof(task)
}

func clearChannelMigrationTaskProof(task ChannelMigrationTask) ChannelMigrationTask {
	task.CutoverLEO = 0
	task.CutoverHW = 0
	task.DrainedLeaderNode = 0
	task.DrainedRuntimeGeneration = 0
	task.DrainedChannelEpoch = 0
	task.DrainedLeaderEpoch = 0
	task.DrainedFenceVersion = 0
	return task
}

func replaceUint64Member(values []uint64, oldValue, newValue uint64) []uint64 {
	next := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == oldValue {
			value = newValue
		}
		next = append(next, value)
	}
	return normalizeUint64Set(next)
}

func removeUint64Member(values []uint64, removed uint64) []uint64 {
	next := make([]uint64, 0, len(values))
	for _, value := range values {
		if value != removed {
			next = append(next, value)
		}
	}
	return normalizeUint64Set(next)
}

func channelRuntimeMetaEqual(a, b ChannelRuntimeMeta) bool {
	a = normalizeChannelRuntimeMeta(a)
	b = normalizeChannelRuntimeMeta(b)
	return reflect.DeepEqual(a, b)
}
