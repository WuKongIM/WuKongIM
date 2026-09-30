package backup

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// ScheduledBackupController is the narrow Controller seam used by the
// simplified scheduled full-backup subsystem.
type ScheduledBackupController interface {
	LocalState(context.Context) (controller.ClusterState, error)
	ReplaceScheduledBackupState(context.Context, uint64, controller.ScheduledBackupState) error
	BackupControllerFence(context.Context) (uint64, uint64, error)
}

// ScheduledControllerStateStore persists bounded backup state through Controller Raft.
type ScheduledControllerStateStore struct {
	controller ScheduledBackupController
	// minimumRevision preserves read-your-writes after a forwarded CAS returns
	// before this node's Controller mirror has published the committed revision.
	minimumRevision atomic.Uint64
}

// NewScheduledControllerStateStore creates a Controller-backed scheduled state store.
func NewScheduledControllerStateStore(
	runtime ScheduledBackupController,
) (*ScheduledControllerStateStore, error) {
	if runtime == nil {
		return nil, fmt.Errorf("backup infra: Controller runtime is required")
	}
	return &ScheduledControllerStateStore{controller: runtime}, nil
}

// Load returns a detached usecase state using the Controller cluster revision
// as its compare-and-swap revision.
func (s *ScheduledControllerStateStore) Load(
	ctx context.Context,
) (backupcontract.SystemState, error) {
	clusterState, err := s.controller.LocalState(ctx)
	if err != nil {
		return backupcontract.SystemState{}, err
	}
	if clusterState.Revision < s.minimumRevision.Load() {
		clusterState, err = s.waitForVisibleControllerState(ctx)
		if err != nil {
			return backupcontract.SystemState{}, err
		}
	}
	if clusterState.ScheduledBackup == nil {
		return backupcontract.SystemState{Revision: clusterState.Revision}, nil
	}
	result := scheduledStateFromController(*clusterState.ScheduledBackup)
	result.Revision = clusterState.Revision
	return result, nil
}

// waitForVisibleControllerState only polls this node's mirror. Normal reads
// allocate no timer or RPC; lagging reads retain cancellation and a fixed bound.
func (s *ScheduledControllerStateStore) waitForVisibleControllerState(ctx context.Context) (controller.ClusterState, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return controller.ClusterState{}, ctx.Err()
		case <-ticker.C:
		}
		current, err := s.controller.LocalState(ctx)
		if err != nil {
			return controller.ClusterState{}, err
		}
		if current.Revision >= s.minimumRevision.Load() {
			return current, nil
		}
	}
}

// CompareAndSwap replaces the complete scheduled state only at the expected
// Controller cluster revision.
func (s *ScheduledControllerStateStore) CompareAndSwap(
	ctx context.Context,
	expectedRevision uint64,
	next backupcontract.SystemState,
) error {
	if expected, ok := backupcontract.CoordinatorFenceFromContext(ctx); ok {
		nodeID, term, err := s.controller.BackupControllerFence(ctx)
		if err != nil {
			return err
		}
		if nodeID != expected.NodeID || term != expected.Term {
			return backupusecase.ErrStateConflict
		}
	}
	replacement := scheduledStateToController(next)
	if err := s.controller.ReplaceScheduledBackupState(
		ctx, expectedRevision, replacement,
	); err != nil {
		if controller.IsExpectedRevisionMismatch(err) {
			return backupusecase.ErrStateConflict
		}
		return err
	}
	// Usecase mutations advance next.Revision; these cannot be Controller no-ops.
	// A caller repeating an unchanged revision must not wait for a nonexistent
	// increment. Concurrent writes may finish out of order, so the floor never falls.
	minimum := expectedRevision
	if next.Revision > expectedRevision {
		minimum++
	}
	for previous := s.minimumRevision.Load(); previous < minimum; previous = s.minimumRevision.Load() {
		if s.minimumRevision.CompareAndSwap(previous, minimum) {
			break
		}
	}
	return nil
}

func scheduledStateFromController(
	value controller.ScheduledBackupState,
) backupcontract.SystemState {
	result := backupcontract.SystemState{
		Revision:            value.Revision,
		ManagerSessionEpoch: value.ManagerSessionEpoch,
		History:             make([]backupcontract.TaskRecord, len(value.History)),
	}
	if value.Plan != nil {
		plan := planFromController(*value.Plan)
		result.Plan = &plan
	}
	if value.ActiveBackup != nil {
		job := backupJobFromController(*value.ActiveBackup)
		result.ActiveBackup = &job
	}
	if value.ActiveRestore != nil {
		result.ActiveRestore = &backupcontract.RestoreJob{
			ID:                 value.ActiveRestore.ID,
			BackupID:           value.ActiveRestore.BackupID,
			Initiator:          value.ActiveRestore.Initiator,
			Status:             backupcontract.RestoreStatus(value.ActiveRestore.Status),
			StartedUnixMillis:  value.ActiveRestore.StartedUnixMillis,
			DeadlineUnixMillis: value.ActiveRestore.DeadlineUnixMillis,
			UpdatedUnixMillis:  value.ActiveRestore.UpdatedUnixMillis,
			CancelRequested:    value.ActiveRestore.CancelRequested,
			MaintenanceEntered: value.ActiveRestore.MaintenanceEntered,
			PreviousActivation: value.ActiveRestore.PreviousActivation,
			TargetActivation:   value.ActiveRestore.TargetActivation,
			LogicalBytes:       value.ActiveRestore.LogicalBytes,
			MaxMessageID:       value.ActiveRestore.MaxMessageID,
			ErrorCode:          value.ActiveRestore.ErrorCode,
			Slots: make(
				[]backupcontract.RestoreSlotProgress,
				len(value.ActiveRestore.Slots),
			),
		}
		for index, slot := range value.ActiveRestore.Slots {
			result.ActiveRestore.Slots[index] =
				backupcontract.RestoreSlotProgress{
					HashSlot: slot.HashSlot,
					Status:   backupcontract.RestoreSlotStatus(slot.Status),
					Attempt:  slot.Attempt,
					ReplicaNodeIDs: append(
						[]uint64(nil), slot.ReplicaNodeIDs...,
					),
					LogicalBytes:      slot.LogicalBytes,
					UpdatedUnixMillis: slot.UpdatedUnixMillis,
					ErrorCode:         slot.ErrorCode,
				}
		}
	}
	if value.ActiveArchiveOperation != nil {
		result.ActiveArchiveOperation = &backupcontract.ArchiveOperation{
			Token:             value.ActiveArchiveOperation.Token,
			Kind:              value.ActiveArchiveOperation.Kind,
			ArchiveID:         value.ActiveArchiveOperation.ArchiveID,
			CoordinatorNodeID: value.ActiveArchiveOperation.CoordinatorNodeID,
			CoordinatorTerm:   value.ActiveArchiveOperation.CoordinatorTerm,
			StartedUnixMillis: value.ActiveArchiveOperation.StartedUnixMillis,
			ExpiresUnixMillis: value.ActiveArchiveOperation.ExpiresUnixMillis,
		}
	}
	for index, record := range value.History {
		result.History[index] = backupcontract.TaskRecord{
			ID:                  record.ID,
			Kind:                record.Kind,
			Initiator:           record.Initiator,
			Trigger:             backupcontract.Trigger(record.Trigger),
			Status:              record.Status,
			StartedUnixMillis:   record.StartedUnixMillis,
			CompletedUnixMillis: record.CompletedUnixMillis,
			ScheduledUnixMillis: record.ScheduledUnixMillis,
			ErrorCode:           record.ErrorCode,
		}
	}
	return result
}

func scheduledStateToController(
	value backupcontract.SystemState,
) controller.ScheduledBackupState {
	result := controller.ScheduledBackupState{
		Revision:            value.Revision,
		ManagerSessionEpoch: value.ManagerSessionEpoch,
		History:             make([]controller.BackupTaskRecord, len(value.History)),
	}
	if value.Plan != nil {
		plan := planToController(*value.Plan)
		result.Plan = &plan
	}
	if value.ActiveBackup != nil {
		job := backupJobToController(*value.ActiveBackup)
		result.ActiveBackup = &job
	}
	if value.ActiveRestore != nil {
		result.ActiveRestore = &controller.ScheduledRestoreJob{
			ID:                 value.ActiveRestore.ID,
			BackupID:           value.ActiveRestore.BackupID,
			Initiator:          value.ActiveRestore.Initiator,
			Status:             string(value.ActiveRestore.Status),
			StartedUnixMillis:  value.ActiveRestore.StartedUnixMillis,
			DeadlineUnixMillis: value.ActiveRestore.DeadlineUnixMillis,
			UpdatedUnixMillis:  value.ActiveRestore.UpdatedUnixMillis,
			CancelRequested:    value.ActiveRestore.CancelRequested,
			MaintenanceEntered: value.ActiveRestore.MaintenanceEntered,
			PreviousActivation: value.ActiveRestore.PreviousActivation,
			TargetActivation:   value.ActiveRestore.TargetActivation,
			LogicalBytes:       value.ActiveRestore.LogicalBytes,
			MaxMessageID:       value.ActiveRestore.MaxMessageID,
			ErrorCode:          value.ActiveRestore.ErrorCode,
			Slots: make(
				[]controller.RestoreSlotProgress,
				len(value.ActiveRestore.Slots),
			),
		}
		for index, slot := range value.ActiveRestore.Slots {
			result.ActiveRestore.Slots[index] =
				controller.RestoreSlotProgress{
					HashSlot: slot.HashSlot,
					Status:   string(slot.Status),
					Attempt:  slot.Attempt,
					ReplicaNodeIDs: append(
						[]uint64(nil), slot.ReplicaNodeIDs...,
					),
					LogicalBytes:      slot.LogicalBytes,
					UpdatedUnixMillis: slot.UpdatedUnixMillis,
					ErrorCode:         slot.ErrorCode,
				}
		}
	}
	if value.ActiveArchiveOperation != nil {
		result.ActiveArchiveOperation = &controller.BackupArchiveOperation{
			Token:             value.ActiveArchiveOperation.Token,
			Kind:              value.ActiveArchiveOperation.Kind,
			ArchiveID:         value.ActiveArchiveOperation.ArchiveID,
			CoordinatorNodeID: value.ActiveArchiveOperation.CoordinatorNodeID,
			CoordinatorTerm:   value.ActiveArchiveOperation.CoordinatorTerm,
			StartedUnixMillis: value.ActiveArchiveOperation.StartedUnixMillis,
			ExpiresUnixMillis: value.ActiveArchiveOperation.ExpiresUnixMillis,
		}
	}
	for index, record := range value.History {
		result.History[index] = controller.BackupTaskRecord{
			ID:                  record.ID,
			Kind:                record.Kind,
			Initiator:           record.Initiator,
			Trigger:             controller.BackupTrigger(record.Trigger),
			Status:              record.Status,
			StartedUnixMillis:   record.StartedUnixMillis,
			CompletedUnixMillis: record.CompletedUnixMillis,
			ScheduledUnixMillis: record.ScheduledUnixMillis,
			ErrorCode:           record.ErrorCode,
		}
	}
	return result
}

func planFromController(value controller.BackupPlan) backupcontract.Plan {
	result := backupcontract.Plan{
		Revision: value.Revision,
		Enabled:  value.Enabled,
		Store: backupcontract.StoreConfig{
			Kind:                 backupcontract.StoreKind(value.Store.Kind),
			Endpoint:             value.Store.Endpoint,
			Region:               value.Store.Region,
			Bucket:               value.Store.Bucket,
			Prefix:               value.Store.Prefix,
			PathStyle:            value.Store.PathStyle,
			CredentialCiphertext: append([]byte(nil), value.Store.CredentialCiphertext...),
			CredentialRevision:   value.Store.CredentialRevision,
		},
		Cron:                     value.Cron,
		TimeZone:                 value.TimeZone,
		RetentionCount:           value.RetentionCount,
		RateBytesPerSec:          value.RateBytesPerSec,
		WorkersPerNode:           value.WorkersPerNode,
		MaxDurationMillis:        value.MaxDurationMillis,
		ScheduleCursorUnixMillis: value.ScheduleCursorUnixMillis,
		CreatedUnixMillis:        value.CreatedUnixMillis,
		UpdatedUnixMillis:        value.UpdatedUnixMillis,
	}
	if value.RepositoryVerification != nil {
		result.RepositoryVerification = &backupcontract.RepositoryVerification{
			Status: backupcontract.RepositoryVerificationStatus(
				value.RepositoryVerification.Status,
			),
			VerifiedAtUnixMillis: value.RepositoryVerification.VerifiedAtUnixMillis,
		}
	}
	return result
}

func planToController(value backupcontract.Plan) controller.BackupPlan {
	result := controller.BackupPlan{
		Revision: value.Revision,
		Enabled:  value.Enabled,
		Store: controller.BackupStoreConfig{
			Kind:                 controller.BackupStoreKind(value.Store.Kind),
			Endpoint:             value.Store.Endpoint,
			Region:               value.Store.Region,
			Bucket:               value.Store.Bucket,
			Prefix:               value.Store.Prefix,
			PathStyle:            value.Store.PathStyle,
			CredentialCiphertext: append([]byte(nil), value.Store.CredentialCiphertext...),
			CredentialRevision:   value.Store.CredentialRevision,
		},
		Cron:                     value.Cron,
		TimeZone:                 value.TimeZone,
		RetentionCount:           value.RetentionCount,
		RateBytesPerSec:          value.RateBytesPerSec,
		WorkersPerNode:           value.WorkersPerNode,
		MaxDurationMillis:        value.MaxDurationMillis,
		ScheduleCursorUnixMillis: value.ScheduleCursorUnixMillis,
		CreatedUnixMillis:        value.CreatedUnixMillis,
		UpdatedUnixMillis:        value.UpdatedUnixMillis,
	}
	if value.RepositoryVerification != nil {
		result.RepositoryVerification = &controller.BackupRepositoryVerification{
			Status: controller.BackupRepositoryVerificationStatus(
				value.RepositoryVerification.Status,
			),
			VerifiedAtUnixMillis: value.RepositoryVerification.VerifiedAtUnixMillis,
		}
	}
	return result
}

func backupJobFromController(
	value controller.ScheduledBackupJob,
) backupcontract.BackupJob {
	result := backupcontract.BackupJob{
		ID:                    value.ID,
		Trigger:               backupcontract.Trigger(value.Trigger),
		Status:                backupcontract.JobStatus(value.Status),
		PlanRevision:          value.PlanRevision,
		ScheduledAtUnixMillis: value.ScheduledAtUnixMillis,
		StartedAtUnixMillis:   value.StartedAtUnixMillis,
		DeadlineUnixMillis:    value.DeadlineUnixMillis,
		UpdatedUnixMillis:     value.UpdatedUnixMillis,
		CancelRequested:       value.CancelRequested,
		Slots:                 make([]backupcontract.SlotProgress, len(value.Slots)),
		LogicalBytes:          value.LogicalBytes,
		StoredBytes:           value.StoredBytes,
		Records:               value.Records,
		ErrorCode:             value.ErrorCode,
	}
	for index, slot := range value.Slots {
		result.Slots[index] = backupcontract.SlotProgress{
			HashSlot:          slot.HashSlot,
			Status:            backupcontract.SlotStatus(slot.Status),
			Attempt:           slot.Attempt,
			OwnerNodeID:       slot.OwnerNodeID,
			OwnerTerm:         slot.OwnerTerm,
			ManifestKey:       slot.ManifestKey,
			ManifestSHA256:    slot.ManifestSHA256,
			LogicalBytes:      slot.LogicalBytes,
			StoredBytes:       slot.StoredBytes,
			Records:           slot.Records,
			MaxMessageID:      slot.MaxMessageID,
			UpdatedUnixMillis: slot.UpdatedUnixMillis,
			ErrorCode:         slot.ErrorCode,
		}
	}
	return result
}

func backupJobToController(
	value backupcontract.BackupJob,
) controller.ScheduledBackupJob {
	result := controller.ScheduledBackupJob{
		ID:                    value.ID,
		Trigger:               controller.BackupTrigger(value.Trigger),
		Status:                controller.BackupJobStatus(value.Status),
		PlanRevision:          value.PlanRevision,
		ScheduledAtUnixMillis: value.ScheduledAtUnixMillis,
		StartedAtUnixMillis:   value.StartedAtUnixMillis,
		DeadlineUnixMillis:    value.DeadlineUnixMillis,
		UpdatedUnixMillis:     value.UpdatedUnixMillis,
		CancelRequested:       value.CancelRequested,
		Slots:                 make([]controller.BackupSlotProgress, len(value.Slots)),
		LogicalBytes:          value.LogicalBytes,
		StoredBytes:           value.StoredBytes,
		Records:               value.Records,
		ErrorCode:             value.ErrorCode,
	}
	for index, slot := range value.Slots {
		result.Slots[index] = controller.BackupSlotProgress{
			HashSlot:          slot.HashSlot,
			Status:            controller.BackupSlotStatus(slot.Status),
			Attempt:           slot.Attempt,
			OwnerNodeID:       slot.OwnerNodeID,
			OwnerTerm:         slot.OwnerTerm,
			ManifestKey:       slot.ManifestKey,
			ManifestSHA256:    slot.ManifestSHA256,
			LogicalBytes:      slot.LogicalBytes,
			StoredBytes:       slot.StoredBytes,
			Records:           slot.Records,
			MaxMessageID:      slot.MaxMessageID,
			UpdatedUnixMillis: slot.UpdatedUnixMillis,
			ErrorCode:         slot.ErrorCode,
		}
	}
	return result
}
