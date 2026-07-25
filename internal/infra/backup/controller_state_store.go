package backup

import (
	"context"
	"fmt"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// CoordinationController is the narrow Controller seam required by backup coordination.
type CoordinationController interface {
	// LoadBackupCoordinationState returns the locally visible Controller state snapshot.
	LoadBackupCoordinationState(ctx context.Context) (controller.ClusterState, error)
	// ReplaceBackupCoordinationState proposes a revision-fenced replacement through Controller Raft.
	ReplaceBackupCoordinationState(ctx context.Context, expectedRevision uint64, replacement controller.BackupCoordinationState) error
}

// ControllerStateStore persists bounded backup coordination state through Controller Raft.
type ControllerStateStore struct {
	controller CoordinationController
}

// NewControllerStateStore creates a Controller-backed backup state store.
func NewControllerStateStore(runtime CoordinationController) (*ControllerStateStore, error) {
	if runtime == nil {
		return nil, fmt.Errorf("backup infra: controller runtime is required")
	}
	return &ControllerStateStore{controller: runtime}, nil
}

// Load returns a detached usecase state whose revision is the Controller cluster revision.
func (s *ControllerStateStore) Load(ctx context.Context) (backupusecase.State, error) {
	clusterState, err := s.controller.LoadBackupCoordinationState(ctx)
	if err != nil {
		return backupusecase.State{}, err
	}
	result := backupusecase.State{Revision: clusterState.Revision, RestorePoints: []backupusecase.RestorePoint{}, PendingGarbage: []backupusecase.RestorePoint{}}
	if clusterState.Backup == nil {
		return result, nil
	}
	result.LastEpoch = clusterState.Backup.LastEpoch
	result.ErasureStreams = erasureStreamsFromController(clusterState.Backup.ErasureStreams)
	result.GenerationGCCursors = generationGCCursorsFromController(clusterState.Backup.GenerationGCCursors)
	result.IntegrityAudit = integrityAuditFromController(clusterState.Backup.IntegrityAudit)
	result.Active = jobFromController(clusterState.Backup.Active)
	result.Verification = verificationTaskFromController(clusterState.Backup.Verification)
	result.RestorePoints = make([]backupusecase.RestorePoint, len(clusterState.Backup.RestorePoints))
	for index, restorePoint := range clusterState.Backup.RestorePoints {
		result.RestorePoints[index] = restorePointFromController(restorePoint)
	}
	result.PendingGarbage = make([]backupusecase.RestorePoint, len(clusterState.Backup.PendingGarbage))
	for index, restorePoint := range clusterState.Backup.PendingGarbage {
		result.PendingGarbage[index] = restorePointFromController(restorePoint)
	}
	result.SlotFrontiers = make([]backupcontract.SlotFrontier, len(clusterState.Backup.SlotFrontiers))
	for index, frontier := range clusterState.Backup.SlotFrontiers {
		result.SlotFrontiers[index] = slotFrontierFromController(frontier)
	}
	result.CatalogHead = catalogPageReferenceFromController(clusterState.Backup.CatalogHead)
	result.CatalogAuditRootSequence =
		clusterState.Backup.CatalogAuditRootSequence
	return result, nil
}

// CompareAndSwap stores next only when the Controller cluster revision still matches revision.
func (s *ControllerStateStore) CompareAndSwap(ctx context.Context, revision uint64, next backupusecase.State) error {
	replacement := controller.BackupCoordinationState{
		LastEpoch:                next.LastEpoch,
		Active:                   jobToController(next.Active),
		Verification:             verificationTaskToController(next.Verification),
		RestorePoints:            make([]controller.BackupRestorePoint, len(next.RestorePoints)),
		PendingGarbage:           make([]controller.BackupRestorePoint, len(next.PendingGarbage)),
		SlotFrontiers:            make([]controller.BackupSlotFrontier, len(next.SlotFrontiers)),
		CatalogHead:              catalogPageReferenceToController(next.CatalogHead),
		CatalogAuditRootSequence: next.CatalogAuditRootSequence,
		ErasureStreams:           erasureStreamsToController(next.ErasureStreams),
		GenerationGCCursors:      generationGCCursorsToController(next.GenerationGCCursors),
		IntegrityAudit:           integrityAuditToController(next.IntegrityAudit),
	}
	for index, restorePoint := range next.RestorePoints {
		replacement.RestorePoints[index] = restorePointToController(restorePoint)
	}
	for index, restorePoint := range next.PendingGarbage {
		replacement.PendingGarbage[index] = restorePointToController(restorePoint)
	}
	for index, frontier := range next.SlotFrontiers {
		replacement.SlotFrontiers[index] = slotFrontierToController(frontier)
	}
	if err := s.controller.ReplaceBackupCoordinationState(ctx, revision, replacement); err != nil {
		if controller.IsExpectedRevisionMismatch(err) {
			return backupusecase.ErrStateConflict
		}
		return err
	}
	return nil
}

func integrityAuditFromController(state controller.BackupIntegrityAuditState) backupcontract.IntegrityAuditState {
	result := backupcontract.IntegrityAuditState{
		Revision: state.Revision, DebtObjects: state.DebtObjects,
		LastSuccessAtUnixMillis: state.LastSuccessAtUnixMillis,
		UpdatedAtUnixMillis:     state.UpdatedAtUnixMillis,
		Slots:                   make([]backupcontract.SlotIntegrityAuditState, len(state.Slots)),
		GCGuards:                make([]backupcontract.IntegrityAuditGCGuard, len(state.GCGuards)),
	}
	if state.Cursor != nil {
		result.Cursor = &backupcontract.IntegrityAuditCursor{
			CycleID: state.Cursor.CycleID, ScrubEpoch: state.Cursor.ScrubEpoch,
			CatalogSequence:     state.Cursor.CatalogSequence,
			CatalogRootSequence: state.Cursor.CatalogRootSequence,
			HashSlot:            state.Cursor.HashSlot, Generation: state.Cursor.Generation,
			Position: state.Cursor.Position, ResumeHashSlot: state.Cursor.ResumeHashSlot,
			ResumeGeneration: state.Cursor.ResumeGeneration, ResumePosition: state.Cursor.ResumePosition,
			ResumePhase:         backupcontract.IntegrityAuditPhase(state.Cursor.ResumePhase),
			Phase:               backupcontract.IntegrityAuditPhase(state.Cursor.Phase),
			Repository:          state.Cursor.Repository,
			Category:            backupcontract.IntegrityCorruptionCategory(state.Cursor.Category),
			UpdatedAtUnixMillis: state.Cursor.UpdatedAtUnixMillis,
		}
	}
	for index, slot := range state.Slots {
		result.Slots[index] = backupcontract.SlotIntegrityAuditState{
			HashSlot: slot.HashSlot, Generation: slot.Generation,
			Health:                  backupcontract.SlotAuditHealth(slot.Health),
			Repository:              slot.Repository,
			Category:                backupcontract.IntegrityCorruptionCategory(slot.Category),
			LastSuccessAtUnixMillis: slot.LastSuccessAtUnixMillis,
			UpdatedAtUnixMillis:     slot.UpdatedAtUnixMillis,
		}
	}
	for index, guard := range state.GCGuards {
		result.GCGuards[index] = backupcontract.IntegrityAuditGCGuard{
			HashSlot: guard.HashSlot, Token: guard.Token,
			AcquiredAtUnixMillis: guard.AcquiredAtUnixMillis,
			ExpiresAtUnixMillis:  guard.ExpiresAtUnixMillis,
		}
	}
	return result
}

func integrityAuditToController(state backupcontract.IntegrityAuditState) controller.BackupIntegrityAuditState {
	result := controller.BackupIntegrityAuditState{
		Revision: state.Revision, DebtObjects: state.DebtObjects,
		LastSuccessAtUnixMillis: state.LastSuccessAtUnixMillis,
		UpdatedAtUnixMillis:     state.UpdatedAtUnixMillis,
		Slots:                   make([]controller.BackupSlotIntegrityAuditState, len(state.Slots)),
		GCGuards:                make([]controller.BackupIntegrityAuditGCGuard, len(state.GCGuards)),
	}
	if state.Cursor != nil {
		result.Cursor = &controller.BackupIntegrityAuditCursor{
			CycleID: state.Cursor.CycleID, ScrubEpoch: state.Cursor.ScrubEpoch,
			CatalogSequence:     state.Cursor.CatalogSequence,
			CatalogRootSequence: state.Cursor.CatalogRootSequence,
			HashSlot:            state.Cursor.HashSlot, Generation: state.Cursor.Generation,
			Position: state.Cursor.Position, ResumeHashSlot: state.Cursor.ResumeHashSlot,
			ResumeGeneration: state.Cursor.ResumeGeneration, ResumePosition: state.Cursor.ResumePosition,
			ResumePhase: string(state.Cursor.ResumePhase),
			Phase:       string(state.Cursor.Phase), Repository: state.Cursor.Repository,
			Category:            string(state.Cursor.Category),
			UpdatedAtUnixMillis: state.Cursor.UpdatedAtUnixMillis,
		}
	}
	for index, slot := range state.Slots {
		result.Slots[index] = controller.BackupSlotIntegrityAuditState{
			HashSlot: slot.HashSlot, Generation: slot.Generation,
			Health: string(slot.Health), Repository: slot.Repository,
			Category:                string(slot.Category),
			LastSuccessAtUnixMillis: slot.LastSuccessAtUnixMillis,
			UpdatedAtUnixMillis:     slot.UpdatedAtUnixMillis,
		}
	}
	for index, guard := range state.GCGuards {
		result.GCGuards[index] = controller.BackupIntegrityAuditGCGuard{
			HashSlot: guard.HashSlot, Token: guard.Token,
			AcquiredAtUnixMillis: guard.AcquiredAtUnixMillis,
			ExpiresAtUnixMillis:  guard.ExpiresAtUnixMillis,
		}
	}
	return result
}

func generationGCCursorsFromController(cursors []controller.BackupGenerationGCCursor) []backupcontract.GenerationGCCursor {
	result := make([]backupcontract.GenerationGCCursor, len(cursors))
	for index, cursor := range cursors {
		result[index] = backupcontract.GenerationGCCursor{
			Repository: cursor.Repository, Revision: cursor.Revision,
			CycleID: cursor.CycleID, AfterKey: cursor.AfterKey,
			CutoffUnixMillis: cursor.CutoffUnixMillis, Complete: cursor.Complete,
			UpdatedAtUnixMillis: cursor.UpdatedAtUnixMillis,
		}
	}
	return result
}

func generationGCCursorsToController(cursors []backupcontract.GenerationGCCursor) []controller.BackupGenerationGCCursor {
	result := make([]controller.BackupGenerationGCCursor, len(cursors))
	for index, cursor := range cursors {
		result[index] = controller.BackupGenerationGCCursor{
			Repository: cursor.Repository, Revision: cursor.Revision,
			CycleID: cursor.CycleID, AfterKey: cursor.AfterKey,
			CutoffUnixMillis: cursor.CutoffUnixMillis, Complete: cursor.Complete,
			UpdatedAtUnixMillis: cursor.UpdatedAtUnixMillis,
		}
	}
	return result
}

func catalogPageReferenceFromController(reference *controller.BackupCatalogPageReference) *backupartifact.CatalogPageReference {
	if reference == nil {
		return nil
	}
	return &backupartifact.CatalogPageReference{
		Sequence: reference.Sequence, Key: reference.Key, SHA256: reference.SHA256,
		Bytes: reference.Bytes, LatestCheckpointID: reference.LatestCheckpointID,
	}
}

func catalogPageReferenceToController(reference *backupartifact.CatalogPageReference) *controller.BackupCatalogPageReference {
	if reference == nil {
		return nil
	}
	return &controller.BackupCatalogPageReference{
		Sequence: reference.Sequence, Key: reference.Key, SHA256: reference.SHA256,
		Bytes: reference.Bytes, LatestCheckpointID: reference.LatestCheckpointID,
	}
}

func slotFrontierFromController(frontier controller.BackupSlotFrontier) backupcontract.SlotFrontier {
	return backupcontract.SlotFrontier{
		Revision: frontier.Revision, HashSlot: frontier.HashSlot, Generation: frontier.Generation,
		GenerationStartedAtUnixMillis: frontier.GenerationStartedAtUnixMillis,
		Lease:                         slotCaptureLeaseFromController(frontier.Lease),
		SourceSlotID:                  frontier.SourceSlotID,
		SourcePinStartedAtUnixMillis:  frontier.SourcePinStartedAtUnixMillis,
		Baseline:                      slotBaselineFromController(frontier.Baseline),
		Rebase:                        slotRebaseFromController(frontier.Rebase),
		LastPromotion:                 slotPromotionFromController(frontier.LastPromotion),
		Metadata:                      streamFrontierFromController(frontier.Metadata),
		Messages:                      streamFrontierFromController(frontier.Messages),
		WatermarkAtUnixMillis:         frontier.WatermarkAtUnixMillis,
		UpdatedAtUnixMillis:           frontier.UpdatedAtUnixMillis,
	}
}

func slotFrontierToController(frontier backupcontract.SlotFrontier) controller.BackupSlotFrontier {
	return controller.BackupSlotFrontier{
		Revision: frontier.Revision, HashSlot: frontier.HashSlot, Generation: frontier.Generation,
		GenerationStartedAtUnixMillis: frontier.GenerationStartedAtUnixMillis,
		Lease:                         slotCaptureLeaseToController(frontier.Lease),
		SourceSlotID:                  frontier.SourceSlotID,
		SourcePinStartedAtUnixMillis:  frontier.SourcePinStartedAtUnixMillis,
		Baseline:                      slotBaselineToController(frontier.Baseline),
		Rebase:                        slotRebaseToController(frontier.Rebase),
		LastPromotion:                 slotPromotionToController(frontier.LastPromotion),
		Metadata:                      streamFrontierToController(frontier.Metadata),
		Messages:                      streamFrontierToController(frontier.Messages),
		WatermarkAtUnixMillis:         frontier.WatermarkAtUnixMillis,
		UpdatedAtUnixMillis:           frontier.UpdatedAtUnixMillis,
	}
}

func slotPromotionFromController(
	promotion *controller.BackupSlotGenerationPromotion,
) *backupcontract.SlotGenerationPromotion {
	if promotion == nil {
		return nil
	}
	return &backupcontract.SlotGenerationPromotion{
		PreviousGeneration:   promotion.PreviousGeneration,
		Reason:               promotion.Reason,
		PromotedAtUnixMillis: promotion.PromotedAtUnixMillis,
	}
}

func slotPromotionToController(
	promotion *backupcontract.SlotGenerationPromotion,
) *controller.BackupSlotGenerationPromotion {
	if promotion == nil {
		return nil
	}
	return &controller.BackupSlotGenerationPromotion{
		PreviousGeneration:   promotion.PreviousGeneration,
		Reason:               promotion.Reason,
		PromotedAtUnixMillis: promotion.PromotedAtUnixMillis,
	}
}

func slotBaselineFromController(reference *controller.BackupSlotBaselineReference) *backupcontract.SlotBaselineReference {
	if reference == nil {
		return nil
	}
	return &backupcontract.SlotBaselineReference{
		PlaintextBytes: reference.PlaintextBytes,
		Partition: backupartifact.PartitionReference{
			HashSlot: reference.Partition.HashSlot, Key: reference.Partition.Key,
			SHA256: reference.Partition.SHA256, Bytes: reference.Partition.Bytes,
			ObjectCount: reference.Partition.ObjectCount, CiphertextBytes: reference.Partition.CiphertextBytes,
			Evidence: reference.Partition.Evidence,
		},
	}
}

func slotBaselineToController(reference *backupcontract.SlotBaselineReference) *controller.BackupSlotBaselineReference {
	if reference == nil {
		return nil
	}
	return &controller.BackupSlotBaselineReference{
		PlaintextBytes: reference.PlaintextBytes,
		Partition: controller.BackupPartitionReference{
			HashSlot: reference.Partition.HashSlot, Key: reference.Partition.Key,
			SHA256: reference.Partition.SHA256, Bytes: reference.Partition.Bytes,
			ObjectCount: reference.Partition.ObjectCount, CiphertextBytes: reference.Partition.CiphertextBytes,
			Evidence: reference.Partition.Evidence,
		},
	}
}

func slotRebaseFromController(rebase *controller.BackupSlotRebase) *backupcontract.SlotRebase {
	if rebase == nil {
		return nil
	}
	return &backupcontract.SlotRebase{
		TargetGeneration: rebase.TargetGeneration,
		Epoch:            rebase.Epoch,
		Reason:           rebase.Reason, StartedAtUnixMillis: rebase.StartedAtUnixMillis,
	}
}

func slotRebaseToController(rebase *backupcontract.SlotRebase) *controller.BackupSlotRebase {
	if rebase == nil {
		return nil
	}
	return &controller.BackupSlotRebase{
		TargetGeneration: rebase.TargetGeneration,
		Epoch:            rebase.Epoch,
		Reason:           rebase.Reason, StartedAtUnixMillis: rebase.StartedAtUnixMillis,
	}
}

func slotCaptureLeaseFromController(lease controller.BackupSlotCaptureLease) backupcontract.SlotCaptureLease {
	return backupcontract.SlotCaptureLease{
		SlotID: lease.SlotID, LeaderTerm: lease.LeaderTerm, ConfigEpoch: lease.ConfigEpoch,
		HolderNodeID: lease.HolderNodeID, Generation: lease.Generation,
		Sequence: lease.Sequence, AcquiredAtUnixMillis: lease.AcquiredAtUnixMillis,
	}
}

func slotCaptureLeaseToController(lease backupcontract.SlotCaptureLease) controller.BackupSlotCaptureLease {
	return controller.BackupSlotCaptureLease{
		SlotID: lease.SlotID, LeaderTerm: lease.LeaderTerm, ConfigEpoch: lease.ConfigEpoch,
		HolderNodeID: lease.HolderNodeID, Generation: lease.Generation,
		Sequence: lease.Sequence, AcquiredAtUnixMillis: lease.AcquiredAtUnixMillis,
	}
}

func streamFrontierFromController(frontier controller.BackupStreamFrontier) backupcontract.StreamFrontier {
	return backupcontract.StreamFrontier{
		Sequence: frontier.Sequence, Head: segmentReferenceFromController(frontier.Head),
		CursorHead:         segmentReferenceFromController(frontier.CursorHead),
		BaselineCursorHead: segmentReferenceFromController(frontier.BaselineCursorHead),
		SourceCursor:       frontier.SourceCursor, SourceHighWatermark: frontier.SourceHighWatermark,
		WatermarkAtUnixMillis:  frontier.WatermarkAtUnixMillis,
		CapturedPlaintextBytes: frontier.CapturedPlaintextBytes,
	}
}

func streamFrontierToController(frontier backupcontract.StreamFrontier) controller.BackupStreamFrontier {
	return controller.BackupStreamFrontier{
		Sequence: frontier.Sequence, Head: segmentReferenceToController(frontier.Head),
		CursorHead:         segmentReferenceToController(frontier.CursorHead),
		BaselineCursorHead: segmentReferenceToController(frontier.BaselineCursorHead),
		SourceCursor:       frontier.SourceCursor, SourceHighWatermark: frontier.SourceHighWatermark,
		WatermarkAtUnixMillis:  frontier.WatermarkAtUnixMillis,
		CapturedPlaintextBytes: frontier.CapturedPlaintextBytes,
	}
}

func segmentReferenceFromController(reference *controller.BackupSegmentReference) *backupartifact.SegmentReference {
	if reference == nil {
		return nil
	}
	return &backupartifact.SegmentReference{
		SegmentID: reference.SegmentID, CommitKey: reference.CommitKey, CommitSHA256: reference.CommitSHA256,
		PlaintextBytes: reference.PlaintextBytes,
	}
}

func segmentReferenceToController(reference *backupartifact.SegmentReference) *controller.BackupSegmentReference {
	if reference == nil {
		return nil
	}
	return &controller.BackupSegmentReference{
		SegmentID: reference.SegmentID, CommitKey: reference.CommitKey, CommitSHA256: reference.CommitSHA256,
		PlaintextBytes: reference.PlaintextBytes,
	}
}

func erasureLedgerReferenceFromController(reference *controller.BackupErasureLedgerReference) *backupusecase.ErasureLedgerRecordReference {
	if reference == nil {
		return nil
	}
	return &backupusecase.ErasureLedgerRecordReference{
		HashSlot: reference.HashSlot, Sequence: reference.Sequence, EventID: reference.EventID, RecordKey: reference.RecordKey, RecordSHA256: reference.RecordSHA256,
	}
}

func erasureLedgerReferenceToController(reference *backupusecase.ErasureLedgerRecordReference) *controller.BackupErasureLedgerReference {
	if reference == nil {
		return nil
	}
	return &controller.BackupErasureLedgerReference{
		HashSlot: reference.HashSlot, Sequence: reference.Sequence, EventID: reference.EventID, RecordKey: reference.RecordKey, RecordSHA256: reference.RecordSHA256,
	}
}

func erasureStreamsFromController(streams []controller.BackupErasureStreamState) []backupusecase.ErasureStreamState {
	result := make([]backupusecase.ErasureStreamState, len(streams))
	for index, stream := range streams {
		result[index] = backupusecase.ErasureStreamState{
			HashSlot: stream.HashSlot, Head: cloneErasureStreamHead(stream.Head),
			Pending:       erasureLedgerReferenceFromController(stream.Pending),
			LastCommitted: erasureLedgerReferenceFromController(stream.LastCommitted),
		}
	}
	return result
}

func erasureStreamsToController(streams []backupusecase.ErasureStreamState) []controller.BackupErasureStreamState {
	result := make([]controller.BackupErasureStreamState, len(streams))
	for index, stream := range streams {
		result[index] = controller.BackupErasureStreamState{
			HashSlot: stream.HashSlot, Head: cloneErasureStreamHead(stream.Head),
			Pending:       erasureLedgerReferenceToController(stream.Pending),
			LastCommitted: erasureLedgerReferenceToController(stream.LastCommitted),
		}
	}
	return result
}

func cloneErasureStreamHead(head *backupartifact.ErasureStreamHead) *backupartifact.ErasureStreamHead {
	if head == nil {
		return nil
	}
	cloned := *head
	return &cloned
}

func jobFromController(job *controller.BackupJob) *backupusecase.Job {
	if job == nil {
		return nil
	}
	result := &backupusecase.Job{
		ID:                  job.ID,
		Epoch:               job.Epoch,
		Kind:                backupartifact.RestorePointKind(job.Kind),
		Status:              backupusecase.JobStatus(job.Status),
		HashSlotCount:       job.HashSlotCount,
		ConfigFingerprint:   job.ConfigFingerprint,
		RestorePointID:      job.RestorePointID,
		BaseRestorePointID:  job.BaseRestorePointID,
		StartedAtUnixMillis: job.StartedAtUnixMillis,
		UpdatedAtUnixMillis: job.UpdatedAtUnixMillis,
		Partitions:          make([]backupusecase.PartitionReport, len(job.Partitions)),
		FailureCategory:     job.FailureCategory,
	}
	for index, report := range job.Partitions {
		result.Partitions[index] = backupusecase.PartitionReport{
			JobID:                 report.JobID,
			BackupEpoch:           report.BackupEpoch,
			HashSlot:              report.HashSlot,
			RaftIndex:             report.RaftIndex,
			CommittedAtUnixMillis: report.CommittedAtUnixMillis,
			ManifestKey:           report.ManifestKey,
			ManifestSHA256:        report.ManifestSHA256,
			ObjectCount:           report.ObjectCount,
			CiphertextBytes:       report.CiphertextBytes,
		}
	}
	return result
}

func jobToController(job *backupusecase.Job) *controller.BackupJob {
	if job == nil {
		return nil
	}
	result := &controller.BackupJob{
		ID:                  job.ID,
		Epoch:               job.Epoch,
		Kind:                controller.BackupRestorePointKind(job.Kind),
		Status:              controller.BackupJobStatus(job.Status),
		HashSlotCount:       job.HashSlotCount,
		ConfigFingerprint:   job.ConfigFingerprint,
		RestorePointID:      job.RestorePointID,
		BaseRestorePointID:  job.BaseRestorePointID,
		StartedAtUnixMillis: job.StartedAtUnixMillis,
		UpdatedAtUnixMillis: job.UpdatedAtUnixMillis,
		Partitions:          make([]controller.BackupPartitionReport, len(job.Partitions)),
		FailureCategory:     job.FailureCategory,
	}
	for index, report := range job.Partitions {
		result.Partitions[index] = controller.BackupPartitionReport{
			JobID:                 report.JobID,
			BackupEpoch:           report.BackupEpoch,
			HashSlot:              report.HashSlot,
			RaftIndex:             report.RaftIndex,
			CommittedAtUnixMillis: report.CommittedAtUnixMillis,
			ManifestKey:           report.ManifestKey,
			ManifestSHA256:        report.ManifestSHA256,
			ObjectCount:           report.ObjectCount,
			CiphertextBytes:       report.CiphertextBytes,
		}
	}
	return result
}

func restorePointFromController(restorePoint controller.BackupRestorePoint) backupusecase.RestorePoint {
	return backupusecase.RestorePoint{
		ID:                    restorePoint.ID,
		JobID:                 restorePoint.JobID,
		BackupEpoch:           restorePoint.BackupEpoch,
		Kind:                  backupartifact.RestorePointKind(restorePoint.Kind),
		EffectiveAtUnixMillis: restorePoint.EffectiveAtUnixMillis,
		CreatedAtUnixMillis:   restorePoint.CreatedAtUnixMillis,
		ManifestSHA256:        restorePoint.ManifestSHA256,
		PrimaryVerified:       restorePoint.PrimaryVerified,
		SecondaryVerified:     restorePoint.SecondaryVerified,
		Held:                  restorePoint.Held,
		LastVerification:      verificationEvidenceFromController(restorePoint.LastVerification),
	}
}

func restorePointToController(restorePoint backupusecase.RestorePoint) controller.BackupRestorePoint {
	return controller.BackupRestorePoint{
		ID:                    restorePoint.ID,
		JobID:                 restorePoint.JobID,
		BackupEpoch:           restorePoint.BackupEpoch,
		Kind:                  controller.BackupRestorePointKind(restorePoint.Kind),
		EffectiveAtUnixMillis: restorePoint.EffectiveAtUnixMillis,
		CreatedAtUnixMillis:   restorePoint.CreatedAtUnixMillis,
		ManifestSHA256:        restorePoint.ManifestSHA256,
		PrimaryVerified:       restorePoint.PrimaryVerified,
		SecondaryVerified:     restorePoint.SecondaryVerified,
		Held:                  restorePoint.Held,
		LastVerification:      verificationEvidenceToController(restorePoint.LastVerification),
	}
}

func verificationTaskFromController(task *controller.BackupVerificationTask) *backupusecase.VerificationTask {
	if task == nil {
		return nil
	}
	return &backupusecase.VerificationTask{
		ID: task.ID, RestorePointID: task.RestorePointID,
		VerificationEvidence: *verificationEvidenceFromController(&task.BackupVerificationEvidence),
	}
}

func verificationTaskToController(task *backupusecase.VerificationTask) *controller.BackupVerificationTask {
	if task == nil {
		return nil
	}
	return &controller.BackupVerificationTask{
		ID: task.ID, RestorePointID: task.RestorePointID,
		BackupVerificationEvidence: *verificationEvidenceToController(&task.VerificationEvidence),
	}
}

func verificationEvidenceFromController(evidence *controller.BackupVerificationEvidence) *backupusecase.VerificationEvidence {
	if evidence == nil {
		return nil
	}
	return &backupusecase.VerificationEvidence{
		Status:              backupusecase.VerificationTaskStatus(evidence.Status),
		StartedAtUnixMillis: evidence.StartedAtUnixMillis, CompletedAtUnixMillis: evidence.CompletedAtUnixMillis,
		PrimaryVerified: evidence.PrimaryVerified, SecondaryVerified: evidence.SecondaryVerified,
		ManifestSHA256: evidence.ManifestSHA256, FailureCategory: evidence.FailureCategory,
	}
}

func verificationEvidenceToController(evidence *backupusecase.VerificationEvidence) *controller.BackupVerificationEvidence {
	if evidence == nil {
		return nil
	}
	return &controller.BackupVerificationEvidence{
		Status:              controller.BackupVerificationTaskStatus(evidence.Status),
		StartedAtUnixMillis: evidence.StartedAtUnixMillis, CompletedAtUnixMillis: evidence.CompletedAtUnixMillis,
		PrimaryVerified: evidence.PrimaryVerified, SecondaryVerified: evidence.SecondaryVerified,
		ManifestSHA256: evidence.ManifestSHA256, FailureCategory: evidence.FailureCategory,
	}
}

var _ backupusecase.StateStore = (*ControllerStateStore)(nil)
