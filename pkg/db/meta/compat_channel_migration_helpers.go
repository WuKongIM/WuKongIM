package meta

import (
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func validateChannelMigrationFenceRequest(req ChannelMigrationFenceRequest) error {
	if err := validateChannelMigrationTaskGuard(req.Guard); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(req.RuntimeGuard); err != nil {
		return err
	}
	if !isValidChannelMigrationStatus(req.Status) || !isValidChannelMigrationPhase(req.Phase) || req.FenceReason == 0 || req.FenceUntilMS <= 0 || req.UpdatedAtMS <= req.Guard.ExpectedUpdatedAtMS {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationResetFenceRequest(req ChannelMigrationResetFenceRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.NowMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationLeaderTransferRequest(req ChannelMigrationLeaderTransferRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.DesiredLeader == 0 || req.NextLeaderEpoch == 0 || req.LeaseUntilMS <= 0 || req.NowMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationAddLearnerRequest(req ChannelMigrationAddLearnerRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.TargetNode == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationPromoteLearnerRequest(req ChannelMigrationPromoteLearnerRequest) error {
	if err := validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, 0); err != nil {
		return err
	}
	if req.SourceNode == 0 || req.TargetNode == 0 || req.SourceNode == req.TargetNode || req.NowMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationClearFenceRequest(req ChannelMigrationClearFenceRequest) error {
	return validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, req.CompletedAtMS)
}

func validateChannelMigrationAbortRequest(req ChannelMigrationAbortRequest) error {
	if req.Status != ChannelMigrationStatusAborted {
		return dberrors.ErrInvalidArgument
	}
	return validateChannelMigrationTaskRuntimeTransition(req.Guard, req.RuntimeGuard, req.Status, req.Phase, req.UpdatedAtMS, req.CompletedAtMS)
}

func validateChannelMigrationTaskGCRequest(req ChannelMigrationTaskGCRequest) error {
	if req.BeforeMS <= 0 || req.Limit <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationTaskCreate(req ChannelMigrationTaskCreate) error {
	if err := validateChannelMigrationTask(req.Task); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(req.RuntimeGuard); err != nil {
		return err
	}
	if req.Task.ChannelID != req.RuntimeGuard.ChannelID || req.Task.ChannelType != req.RuntimeGuard.ChannelType {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationTaskRuntimeTransition(guard ChannelMigrationTaskGuard, runtimeGuard ChannelMigrationRuntimeGuard, status ChannelMigrationStatus, phase ChannelMigrationPhase, updatedAtMS, completedAtMS int64) error {
	if err := validateChannelMigrationTaskGuard(guard); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(runtimeGuard); err != nil {
		return err
	}
	if !isValidChannelMigrationStatus(status) || !isValidChannelMigrationPhase(phase) || updatedAtMS <= guard.ExpectedUpdatedAtMS {
		return dberrors.ErrInvalidArgument
	}
	if status == ChannelMigrationStatusCompleted || status == ChannelMigrationStatusFailed || status == ChannelMigrationStatusAborted {
		if completedAtMS <= 0 {
			return dberrors.ErrInvalidArgument
		}
	}
	return nil
}

func requireChannelMigrationSetFenceTransition(task ChannelMigrationTask, req ChannelMigrationFenceRequest) error {
	if req.Status != ChannelMigrationStatusRunning {
		return dberrors.ErrConflict
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer || (task.Kind == ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer && isLeaderTransferPhase(task.Phase)) {
		if (task.Phase == ChannelMigrationPhaseWriteFence && req.Phase == ChannelMigrationPhaseDrainLeader) ||
			(isLeaderTransferFencePhase(task.Phase) && req.Phase == task.Phase) {
			return nil
		}
		return dberrors.ErrConflict
	}
	if task.Kind == ChannelMigrationKindReplicaReplace {
		if (task.Phase == ChannelMigrationPhaseWarmCatchUp && req.Phase == ChannelMigrationPhaseCutoverFence) ||
			(isReplicaReplaceFencePhase(task.Phase) && req.Phase == task.Phase) {
			return nil
		}
	}
	return dberrors.ErrConflict
}

func requireChannelMigrationResetFenceTransition(task ChannelMigrationTask, req ChannelMigrationResetFenceRequest) error {
	if req.Status != ChannelMigrationStatusRunning || !isChannelMigrationFencePhaseAllowed(task) {
		return dberrors.ErrConflict
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer || (task.Kind == ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer && isLeaderTransferFencePhase(task.Phase)) {
		if req.Phase == ChannelMigrationPhaseProbeTarget || req.Phase == ChannelMigrationPhaseWriteFence {
			return nil
		}
		return dberrors.ErrConflict
	}
	if task.Kind == ChannelMigrationKindReplicaReplace && req.Phase == ChannelMigrationPhaseWarmCatchUp {
		return nil
	}
	return dberrors.ErrConflict
}

func requireChannelMigrationLeaderTransferTransition(task ChannelMigrationTask, req ChannelMigrationLeaderTransferRequest) error {
	if req.Status != ChannelMigrationStatusRunning || task.Phase != ChannelMigrationPhaseCommitLeaderMeta || req.Phase != ChannelMigrationPhaseVerifyNewLeader {
		return dberrors.ErrConflict
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer {
		return nil
	}
	if task.Kind == ChannelMigrationKindReplicaReplace && task.EmbeddedLeaderTransfer {
		return nil
	}
	return dberrors.ErrConflict
}

func requireChannelMigrationAddLearnerTransition(task ChannelMigrationTask, req ChannelMigrationAddLearnerRequest) error {
	if task.Kind != ChannelMigrationKindReplicaReplace ||
		task.Phase != ChannelMigrationPhaseAddLearner ||
		req.Status != ChannelMigrationStatusRunning ||
		req.Phase != ChannelMigrationPhaseBootstrapTarget {
		return dberrors.ErrConflict
	}
	return nil
}

func requireChannelMigrationPromoteLearnerTransition(task ChannelMigrationTask, req ChannelMigrationPromoteLearnerRequest) error {
	if task.Kind != ChannelMigrationKindReplicaReplace ||
		task.Phase != ChannelMigrationPhasePromoteAndRemove ||
		req.Status != ChannelMigrationStatusRunning ||
		req.Phase != ChannelMigrationPhaseVerifyMembership {
		return dberrors.ErrConflict
	}
	return nil
}

func requireChannelMigrationClearFenceTransition(task ChannelMigrationTask, req ChannelMigrationClearFenceRequest) error {
	if !isChannelMigrationFencePhaseAllowed(task) {
		return dberrors.ErrConflict
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
		return dberrors.ErrConflict
	}
	if task.Kind == ChannelMigrationKindReplicaReplace &&
		task.EmbeddedLeaderTransfer &&
		task.Phase == ChannelMigrationPhaseVerifyNewLeader &&
		req.Status == ChannelMigrationStatusRunning &&
		req.Phase == ChannelMigrationPhaseAddLearner &&
		req.CompletedAtMS == 0 {
		return nil
	}
	return dberrors.ErrConflict
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
			return dberrors.ErrConflict
		}
		if isReplicaReplaceAbortPhase(task.Phase) {
			return nil
		}
	}
	return dberrors.ErrConflict
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

func requireMatchingFence(meta ChannelRuntimeMeta, token string, version uint64, nowMS int64, allowExpired bool) error {
	if token == "" || version == 0 || meta.WriteFenceToken != token || meta.WriteFenceVersion != version {
		return dberrors.ErrConflict
	}
	if !allowExpired && nowMS > meta.WriteFenceUntilMS {
		return dberrors.ErrConflict
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
		return dberrors.ErrConflict
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
		return dberrors.ErrConflict
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
		return dberrors.ErrConflict
	}
	if task.DrainedFenceVersion != expectedFenceVersion ||
		meta.WriteFenceVersion != expectedFenceVersion ||
		task.DrainedChannelEpoch != meta.ChannelEpoch ||
		task.DrainedLeaderEpoch != meta.LeaderEpoch ||
		task.DrainedLeaderNode != meta.Leader ||
		task.CutoverHW > task.CutoverLEO {
		return dberrors.ErrConflict
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

func isValidChannelMigrationStatus(status ChannelMigrationStatus) bool {
	switch status {
	case ChannelMigrationStatusPending,
		ChannelMigrationStatusRunning,
		ChannelMigrationStatusBlocked,
		ChannelMigrationStatusCompleted,
		ChannelMigrationStatusFailed,
		ChannelMigrationStatusAborted:
		return true
	default:
		return false
	}
}

func isValidChannelMigrationPhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseValidate,
		ChannelMigrationPhaseProbeTarget,
		ChannelMigrationPhaseWriteFence,
		ChannelMigrationPhaseDrainLeader,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhaseCommitLeaderMeta,
		ChannelMigrationPhaseVerifyNewLeader,
		ChannelMigrationPhaseAddLearner,
		ChannelMigrationPhaseBootstrapTarget,
		ChannelMigrationPhaseWarmCatchUp,
		ChannelMigrationPhaseCutoverFence,
		ChannelMigrationPhasePromoteAndRemove,
		ChannelMigrationPhaseVerifyMembership,
		ChannelMigrationPhaseClearFence:
		return true
	default:
		return false
	}
}

func isLeaderTransferPhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseValidate,
		ChannelMigrationPhaseProbeTarget,
		ChannelMigrationPhaseWriteFence,
		ChannelMigrationPhaseDrainLeader,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhaseCommitLeaderMeta,
		ChannelMigrationPhaseVerifyNewLeader,
		ChannelMigrationPhaseClearFence:
		return true
	default:
		return false
	}
}

func isChannelMigrationFencePhaseAllowed(task ChannelMigrationTask) bool {
	switch task.Kind {
	case ChannelMigrationKindLeaderTransfer:
		return isLeaderTransferFencePhase(task.Phase)
	case ChannelMigrationKindReplicaReplace:
		if isReplicaReplaceFencePhase(task.Phase) {
			return true
		}
		return task.EmbeddedLeaderTransfer && isLeaderTransferFencePhase(task.Phase)
	default:
		return false
	}
}

func isLeaderTransferFencePhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseWriteFence,
		ChannelMigrationPhaseDrainLeader,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhaseCommitLeaderMeta,
		ChannelMigrationPhaseVerifyNewLeader,
		ChannelMigrationPhaseClearFence:
		return true
	default:
		return false
	}
}

func isReplicaReplaceFencePhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseCutoverFence,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhasePromoteAndRemove,
		ChannelMigrationPhaseVerifyMembership,
		ChannelMigrationPhaseClearFence:
		return true
	default:
		return false
	}
}

func (proof ChannelMigrationCutoverProof) hasAny() bool {
	return proof.CutoverLEO != 0 ||
		proof.CutoverHW != 0 ||
		proof.DrainedLeaderNode != 0 ||
		proof.DrainedRuntimeGeneration != 0 ||
		proof.DrainedChannelEpoch != 0 ||
		proof.DrainedLeaderEpoch != 0 ||
		proof.DrainedFenceVersion != 0
}

func (proof ChannelMigrationCutoverProof) hasPartial() bool {
	if !proof.hasAny() {
		return false
	}
	return proof.DrainedLeaderNode == 0 ||
		proof.DrainedRuntimeGeneration == 0 ||
		proof.DrainedChannelEpoch == 0 ||
		proof.DrainedLeaderEpoch == 0 ||
		proof.DrainedFenceVersion == 0 ||
		proof.CutoverHW > proof.CutoverLEO
}
