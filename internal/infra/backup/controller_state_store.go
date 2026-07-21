package backup

import (
	"context"
	"fmt"

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
	result.Active = jobFromController(clusterState.Backup.Active)
	result.RestorePoints = make([]backupusecase.RestorePoint, len(clusterState.Backup.RestorePoints))
	for index, restorePoint := range clusterState.Backup.RestorePoints {
		result.RestorePoints[index] = restorePointFromController(restorePoint)
	}
	result.PendingGarbage = make([]backupusecase.RestorePoint, len(clusterState.Backup.PendingGarbage))
	for index, restorePoint := range clusterState.Backup.PendingGarbage {
		result.PendingGarbage[index] = restorePointFromController(restorePoint)
	}
	return result, nil
}

// CompareAndSwap stores next only when the Controller cluster revision still matches revision.
func (s *ControllerStateStore) CompareAndSwap(ctx context.Context, revision uint64, next backupusecase.State) error {
	replacement := controller.BackupCoordinationState{
		LastEpoch:      next.LastEpoch,
		Active:         jobToController(next.Active),
		RestorePoints:  make([]controller.BackupRestorePoint, len(next.RestorePoints)),
		PendingGarbage: make([]controller.BackupRestorePoint, len(next.PendingGarbage)),
	}
	for index, restorePoint := range next.RestorePoints {
		replacement.RestorePoints[index] = restorePointToController(restorePoint)
	}
	for index, restorePoint := range next.PendingGarbage {
		replacement.PendingGarbage[index] = restorePointToController(restorePoint)
	}
	if err := s.controller.ReplaceBackupCoordinationState(ctx, revision, replacement); err != nil {
		if controller.IsExpectedRevisionMismatch(err) {
			return backupusecase.ErrStateConflict
		}
		return err
	}
	return nil
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
	}
}

var _ backupusecase.StateStore = (*ControllerStateStore)(nil)
