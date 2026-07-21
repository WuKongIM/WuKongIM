package backup

import (
	"context"
	"fmt"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// RestoreCoordinationController is the narrow Controller recovery seam.
type RestoreCoordinationController interface {
	LoadRestoreCoordinationState(context.Context) (controller.ClusterState, error)
	ReplaceRestoreCoordinationState(context.Context, uint64, controller.RestoreCoordinationState) error
}

// ControllerRestoreStateStore persists explicit recovery progress through Controller Raft.
type ControllerRestoreStateStore struct{ controller RestoreCoordinationController }

// NewControllerRestoreStateStore creates a Controller-backed recovery store.
func NewControllerRestoreStateStore(runtime RestoreCoordinationController) (*ControllerRestoreStateStore, error) {
	if runtime == nil {
		return nil, fmt.Errorf("backup restore store: controller is required")
	}
	return &ControllerRestoreStateStore{controller: runtime}, nil
}

func (s *ControllerRestoreStateStore) Load(ctx context.Context) (backupusecase.RestoreState, error) {
	state, err := s.controller.LoadRestoreCoordinationState(ctx)
	if err != nil {
		return backupusecase.RestoreState{}, err
	}
	result := backupusecase.RestoreState{Revision: state.Revision}
	if state.Restore != nil {
		result.Plan = restorePlanFromController(state.Restore.Plan)
	}
	return result, nil
}

func (s *ControllerRestoreStateStore) CompareAndSwap(ctx context.Context, revision uint64, next backupusecase.RestoreState) error {
	replacement := controller.RestoreCoordinationState{Plan: restorePlanToController(next.Plan)}
	if err := s.controller.ReplaceRestoreCoordinationState(ctx, revision, replacement); err != nil {
		if controller.IsExpectedRevisionMismatch(err) {
			return backupusecase.ErrStateConflict
		}
		return err
	}
	return nil
}

func restorePlanFromController(plan *controller.RestorePlan) *backupusecase.RestorePlan {
	if plan == nil {
		return nil
	}
	result := &backupusecase.RestorePlan{
		ID: plan.ID, RestorePointID: plan.RestorePointID, ManifestSHA256: plan.ManifestSHA256,
		Repository: plan.Repository, SourceClusterID: plan.SourceClusterID, SourceGeneration: plan.SourceGeneration,
		TargetClusterID: plan.TargetClusterID, TargetGeneration: plan.TargetGeneration, HashSlotCount: plan.HashSlotCount,
		InvalidateTokens: plan.InvalidateTokens, EstimatedPlainBytes: plan.EstimatedPlainBytes, EstimatedCipherBytes: plan.EstimatedCipherBytes,
		Status: backupusecase.RestoreStatus(plan.Status), CreatedAtUnixMillis: plan.CreatedAtUnixMillis, UpdatedAtUnixMillis: plan.UpdatedAtUnixMillis,
		VerifiedAtUnixMillis: plan.VerifiedAtUnixMillis, ActivatedAtUnixMillis: plan.ActivatedAtUnixMillis, ActivationFenceDigest: plan.ActivationFenceDigest,
		Partitions: make([]backupusecase.RestorePartition, len(plan.Partitions)),
	}
	if plan.EstimatedPlainBytes != nil {
		value := *plan.EstimatedPlainBytes
		result.EstimatedPlainBytes = &value
	}
	if plan.EstimatedCipherBytes != nil {
		value := *plan.EstimatedCipherBytes
		result.EstimatedCipherBytes = &value
	}
	for index, partition := range plan.Partitions {
		result.Partitions[index] = backupusecase.RestorePartition{
			HashSlot: partition.HashSlot, Installed: partition.Installed, Verified: partition.Verified,
			PlainBytes: partition.PlainBytes, MessageCount: partition.MessageCount, MetadataSHA256: partition.MetadataSHA256,
			FailureCategory: partition.FailureCategory, UpdatedAtUnixMillis: partition.UpdatedAtUnixMillis,
		}
	}
	return result
}

func restorePlanToController(plan *backupusecase.RestorePlan) *controller.RestorePlan {
	if plan == nil {
		return nil
	}
	result := &controller.RestorePlan{
		ID: plan.ID, RestorePointID: plan.RestorePointID, ManifestSHA256: plan.ManifestSHA256,
		Repository: plan.Repository, SourceClusterID: plan.SourceClusterID, SourceGeneration: plan.SourceGeneration,
		TargetClusterID: plan.TargetClusterID, TargetGeneration: plan.TargetGeneration, HashSlotCount: plan.HashSlotCount,
		InvalidateTokens: plan.InvalidateTokens, EstimatedPlainBytes: plan.EstimatedPlainBytes, EstimatedCipherBytes: plan.EstimatedCipherBytes,
		Status: controller.RestoreStatus(plan.Status), CreatedAtUnixMillis: plan.CreatedAtUnixMillis, UpdatedAtUnixMillis: plan.UpdatedAtUnixMillis,
		VerifiedAtUnixMillis: plan.VerifiedAtUnixMillis, ActivatedAtUnixMillis: plan.ActivatedAtUnixMillis, ActivationFenceDigest: plan.ActivationFenceDigest,
		Partitions: make([]controller.RestorePartition, len(plan.Partitions)),
	}
	if plan.EstimatedPlainBytes != nil {
		value := *plan.EstimatedPlainBytes
		result.EstimatedPlainBytes = &value
	}
	if plan.EstimatedCipherBytes != nil {
		value := *plan.EstimatedCipherBytes
		result.EstimatedCipherBytes = &value
	}
	for index, partition := range plan.Partitions {
		result.Partitions[index] = controller.RestorePartition{
			HashSlot: partition.HashSlot, Installed: partition.Installed, Verified: partition.Verified,
			PlainBytes: partition.PlainBytes, MessageCount: partition.MessageCount, MetadataSHA256: partition.MetadataSHA256,
			FailureCategory: partition.FailureCategory, UpdatedAtUnixMillis: partition.UpdatedAtUnixMillis,
		}
	}
	return result
}

var _ backupusecase.RestorePlanStore = (*ControllerRestoreStateStore)(nil)
