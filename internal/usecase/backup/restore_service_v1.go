package backup

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// RestorePreflight proves archive lineage, topology stability, node health,
// repository readability, and conservative staging capacity before admission.
type RestorePreflight interface {
	Check(
		context.Context,
		backupcontract.RestoreJob,
		backupcontract.Plan,
		backupartifact.ArchiveManifest,
	) error
}

// RestoreServiceOptions configures current-cluster point-in-time restore.
type RestoreServiceOptions struct {
	StateStore    ScheduledStateStore
	Repository    ArchiveRepositoryProvider
	Preflight     RestorePreflight
	Now           func() time.Time
	NewID         func() string
	NewActivation func() string
}

// RestoreService owns durable restore admission and bounded state transitions.
type RestoreService struct {
	store         ScheduledStateStore
	repository    ArchiveRepositoryProvider
	preflight     RestorePreflight
	now           func() time.Time
	newID         func() string
	newActivation func() string
}

// NewRestoreService creates the maintenance restore use case.
func NewRestoreService(
	options RestoreServiceOptions,
) (*RestoreService, error) {
	if options.StateStore == nil || options.Repository == nil ||
		options.Preflight == nil ||
		options.Now == nil || options.NewID == nil ||
		options.NewActivation == nil {
		return nil, fmt.Errorf("%w: restore dependencies", ErrInvalidRequest)
	}
	return &RestoreService{
		store:         options.StateStore,
		repository:    options.Repository,
		preflight:     options.Preflight,
		now:           options.Now,
		newID:         options.NewID,
		newActivation: options.NewActivation,
	}, nil
}

// StartRestore validates bounded publication metadata before admitting the
// durable job. Staging subsequently streams and verifies every Slot payload.
func (s *RestoreService) StartRestore(
	ctx context.Context,
	archiveID string,
	initiator string,
) (result backupcontract.RestoreJob, resultErr error) {
	archiveID = strings.TrimSpace(archiveID)
	initiator = strings.TrimSpace(initiator)
	if archiveID == "" || initiator == "" || len(initiator) > 128 {
		return backupcontract.RestoreJob{}, ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return backupcontract.RestoreJob{}, err
	}
	if current.Plan == nil {
		return backupcontract.RestoreJob{}, ErrDisabled
	}
	if current.ActiveBackup != nil {
		return backupcontract.RestoreJob{}, ErrBackupJobActive
	}
	if current.ActiveRestore != nil {
		return backupcontract.RestoreJob{}, ErrRestoreJobActive
	}
	operation, err := s.acquireArchiveOperation(
		ctx, current, archiveID,
	)
	if err != nil {
		return backupcontract.RestoreJob{}, err
	}
	defer func() {
		resultErr = errors.Join(
			resultErr,
			s.releaseArchiveOperation(operation.Token),
		)
	}()
	store, err := s.repository.Open(ctx, current.Plan.Store)
	if err != nil {
		return backupcontract.RestoreJob{}, err
	}
	manifest, err := backupartifact.LoadPublishedArchiveMetadata(
		ctx, store, archiveID,
	)
	if err != nil {
		return backupcontract.RestoreJob{}, err
	}
	id := strings.TrimSpace(s.newID())
	activation := strings.TrimSpace(s.newActivation())
	if id == "" || activation == "" {
		return backupcontract.RestoreJob{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	slots := make([]backupcontract.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.RestoreSlotProgress{
			HashSlot: uint16(hashSlot),
			Status:   backupcontract.RestoreSlotStatusPending,
		}
	}
	job := backupcontract.RestoreJob{
		ID:                 id,
		BackupID:           archiveID,
		Initiator:          initiator,
		Status:             backupcontract.RestoreStatusPreparing,
		StartedUnixMillis:  now.UnixMilli(),
		DeadlineUnixMillis: now.Add(48 * time.Hour).UnixMilli(),
		UpdatedUnixMillis:  now.UnixMilli(),
		TargetActivation:   activation,
		Slots:              slots,
		MaxMessageID:       manifest.MaxMessageID,
	}
	if err := s.preflight.Check(ctx, job, *current.Plan, manifest); err != nil {
		return backupcontract.RestoreJob{}, err
	}
	// Preflight verifies the archive identity and every node, so Controller
	// state can legitimately advance while it runs. Reload immediately before CAS to
	// preserve newer schedule/history state and fence any concurrent mutation.
	latest, err := s.store.Load(ctx)
	if err != nil {
		return backupcontract.RestoreJob{}, err
	}
	if latest.Plan == nil ||
		latest.ActiveBackup != nil ||
		latest.ActiveRestore != nil ||
		latest.Plan.Revision != current.Plan.Revision ||
		!reflect.DeepEqual(latest.Plan.Store, current.Plan.Store) {
		return backupcontract.RestoreJob{}, ErrStateConflict
	}
	next := latest.Clone()
	next.Revision++
	next.ActiveRestore = &job
	if err := s.store.CompareAndSwap(ctx, latest.Revision, next); err != nil {
		return backupcontract.RestoreJob{}, err
	}
	return job, nil
}

func (s *RestoreService) acquireArchiveOperation(
	ctx context.Context,
	current backupcontract.SystemState,
	archiveID string,
) (backupcontract.ArchiveOperation, error) {
	now := s.now().UTC()
	if current.ActiveArchiveOperation != nil &&
		now.Before(time.UnixMilli(
			current.ActiveArchiveOperation.ExpiresUnixMillis,
		)) {
		return backupcontract.ArchiveOperation{}, ErrArchiveOperationActive
	}
	token := strings.TrimSpace(s.newID())
	if token == "" || len(token) > 128 {
		return backupcontract.ArchiveOperation{}, ErrInvalidRequest
	}
	operation := backupcontract.ArchiveOperation{
		Token: token, Kind: "restore", ArchiveID: archiveID,
		StartedUnixMillis: now.UnixMilli(),
		ExpiresUnixMillis: now.Add(48 * time.Hour).UnixMilli(),
	}
	next := current.Clone()
	next.Revision++
	next.ActiveArchiveOperation = &operation
	if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
		return backupcontract.ArchiveOperation{}, err
	}
	return operation, nil
}

func (s *RestoreService) releaseArchiveOperation(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for range 16 {
		current, err := s.store.Load(ctx)
		if err != nil {
			return err
		}
		if current.ActiveArchiveOperation == nil {
			return nil
		}
		if current.ActiveArchiveOperation.Token != token {
			return ErrStateConflict
		}
		next := current.Clone()
		next.Revision++
		next.ActiveArchiveOperation = nil
		err = s.store.CompareAndSwap(ctx, current.Revision, next)
		if !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	return ErrStateConflict
}

// RequestCancellation asks a pre-switch restore to roll back.
func (s *RestoreService) RequestCancellation(
	ctx context.Context,
	jobID string,
) error {
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		_ int64,
	) error {
		if job.Status == backupcontract.RestoreStatusSwitching ||
			job.Status == backupcontract.RestoreStatusFinalizing ||
			job.Status == backupcontract.RestoreStatusRollingBack {
			return ErrStateConflict
		}
		job.CancelRequested = true
		return nil
	})
}

// BeginMaintenance publishes the cluster-wide admission fence only after the
// complete archive passed durable preflight verification.
func (s *RestoreService) BeginMaintenance(
	ctx context.Context,
	jobID string,
) error {
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		_ int64,
	) error {
		if job.Status != backupcontract.RestoreStatusValidated {
			return ErrStateConflict
		}
		job.Status = backupcontract.RestoreStatusMaintenance
		job.MaintenanceEntered = true
		return nil
	})
}

// MarkMaintenance records the exact live activation that can be rolled back
// after every node acknowledged the admission fence.
func (s *RestoreService) MarkMaintenance(
	ctx context.Context,
	jobID string,
	previousActivation string,
) error {
	previousActivation = strings.TrimSpace(previousActivation)
	if previousActivation == "" {
		return ErrInvalidRequest
	}
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		_ int64,
	) error {
		if job.Status != backupcontract.RestoreStatusMaintenance ||
			!job.MaintenanceEntered ||
			job.PreviousActivation != "" {
			return ErrStateConflict
		}
		job.PreviousActivation = previousActivation
		return nil
	})
}

// ClaimRestoreSlot starts a new idempotent staging attempt.
func (s *RestoreService) ClaimRestoreSlot(
	ctx context.Context,
	jobID string,
	hashSlot uint16,
) (backupcontract.RestoreSlotProgress, error) {
	var claimed backupcontract.RestoreSlotProgress
	err := s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		now int64,
	) error {
		if int(hashSlot) >= len(job.Slots) ||
			(job.Status != backupcontract.RestoreStatusMaintenance &&
				job.Status != backupcontract.RestoreStatusStaging) {
			return ErrStateConflict
		}
		slot := &job.Slots[hashSlot]
		if slot.Status == backupcontract.RestoreSlotStatusVerified {
			return ErrStateConflict
		}
		slot.Status = backupcontract.RestoreSlotStatusStaging
		slot.Attempt++
		slot.ReplicaNodeIDs = nil
		slot.ErrorCode = ""
		slot.UpdatedUnixMillis = now
		job.Status = backupcontract.RestoreStatusStaging
		claimed = *slot
		return nil
	})
	return claimed, err
}

// CompleteRestoreSlot records staging on every current replica.
func (s *RestoreService) CompleteRestoreSlot(
	ctx context.Context,
	jobID string,
	hashSlot uint16,
	attempt uint32,
	replicaNodeIDs []uint64,
	logicalBytes uint64,
) error {
	nodes := append([]uint64(nil), replicaNodeIDs...)
	slices.Sort(nodes)
	nodes = slices.Compact(nodes)
	if attempt == 0 || len(nodes) == 0 || nodes[0] == 0 ||
		logicalBytes == 0 {
		return ErrInvalidRequest
	}
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		now int64,
	) error {
		if int(hashSlot) >= len(job.Slots) {
			return ErrStateConflict
		}
		slot := &job.Slots[hashSlot]
		if slot.Status != backupcontract.RestoreSlotStatusStaging ||
			slot.Attempt != attempt {
			return ErrStateConflict
		}
		slot.Status = backupcontract.RestoreSlotStatusStaged
		slot.ReplicaNodeIDs = nodes
		slot.LogicalBytes = logicalBytes
		slot.UpdatedUnixMillis = now
		job.LogicalBytes += logicalBytes
		return nil
	})
}

// VerifyRestoreSlot records semantic verification of every staged replica.
func (s *RestoreService) VerifyRestoreSlot(
	ctx context.Context,
	jobID string,
	hashSlot uint16,
	attempt uint32,
) error {
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		now int64,
	) error {
		if int(hashSlot) >= len(job.Slots) {
			return ErrStateConflict
		}
		slot := &job.Slots[hashSlot]
		if slot.Status != backupcontract.RestoreSlotStatusStaged ||
			slot.Attempt != attempt ||
			len(slot.ReplicaNodeIDs) == 0 {
			return ErrStateConflict
		}
		slot.Status = backupcontract.RestoreSlotStatusVerified
		slot.UpdatedUnixMillis = now
		return nil
	})
}

// SetRestorePhase advances a cluster-wide non-terminal phase.
func (s *RestoreService) SetRestorePhase(
	ctx context.Context,
	jobID string,
	status backupcontract.RestoreStatus,
) error {
	switch status {
	case backupcontract.RestoreStatusValidated,
		backupcontract.RestoreStatusVerifying,
		backupcontract.RestoreStatusSwitching,
		backupcontract.RestoreStatusFinalizing,
		backupcontract.RestoreStatusRollingBack:
	default:
		return ErrInvalidRequest
	}
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		_ int64,
	) error {
		if status == backupcontract.RestoreStatusValidated &&
			job.Status != backupcontract.RestoreStatusPreparing {
			return ErrStateConflict
		}
		if status == backupcontract.RestoreStatusSwitching {
			for _, slot := range job.Slots {
				if slot.Status != backupcontract.RestoreSlotStatusVerified {
					return ErrStateConflict
				}
			}
		}
		if status == backupcontract.RestoreStatusFinalizing &&
			job.Status != backupcontract.RestoreStatusSwitching {
			return ErrStateConflict
		}
		job.Status = status
		return nil
	})
}

// BeginRollback durably records a bounded execution failure before the runner
// starts restoring the captured previous activation.
func (s *RestoreService) BeginRollback(
	ctx context.Context,
	jobID string,
	errorCode string,
) error {
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" || len(errorCode) > 128 {
		return ErrInvalidRequest
	}
	return s.mutate(ctx, jobID, func(
		job *backupcontract.RestoreJob,
		_ int64,
	) error {
		if job.Status == backupcontract.RestoreStatusRollingBack {
			if job.ErrorCode == errorCode {
				return nil
			}
			return ErrStateConflict
		}
		job.Status = backupcontract.RestoreStatusRollingBack
		job.ErrorCode = errorCode
		return nil
	})
}

// FinishRestore clears maintenance ownership and invalidates all Manager JWTs
// after a successful logical activation.
func (s *RestoreService) FinishRestore(
	ctx context.Context,
	jobID string,
	status backupcontract.RestoreStatus,
	errorCode string,
) error {
	switch status {
	case backupcontract.RestoreStatusSucceeded,
		backupcontract.RestoreStatusFailed,
		backupcontract.RestoreStatusCanceled:
	default:
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveRestore == nil || current.ActiveRestore.ID != jobID ||
		len(errorCode) > 128 {
		return ErrStateConflict
	}
	if status == backupcontract.RestoreStatusSucceeded {
		if current.ActiveRestore.Status != backupcontract.RestoreStatusFinalizing {
			return ErrStateConflict
		}
		for _, slot := range current.ActiveRestore.Slots {
			if slot.Status != backupcontract.RestoreSlotStatusVerified {
				return ErrStateConflict
			}
		}
	}
	now := s.now().UTC().UnixMilli()
	record := backupcontract.TaskRecord{
		ID:                  current.ActiveRestore.ID,
		Kind:                "restore",
		Initiator:           current.ActiveRestore.Initiator,
		Status:              string(status),
		StartedUnixMillis:   current.ActiveRestore.StartedUnixMillis,
		CompletedUnixMillis: now,
		ErrorCode:           errorCode,
	}
	next := current.Clone()
	next.Revision++
	next.ActiveRestore = nil
	if status == backupcontract.RestoreStatusSucceeded {
		next.ManagerSessionEpoch++
	}
	next.History = append([]backupcontract.TaskRecord{record}, next.History...)
	if len(next.History) > backupcontract.MaxTaskHistory {
		next.History = next.History[:backupcontract.MaxTaskHistory]
	}
	return s.store.CompareAndSwap(ctx, current.Revision, next)
}

func (s *RestoreService) mutate(
	ctx context.Context,
	jobID string,
	change func(*backupcontract.RestoreJob, int64) error,
) error {
	if strings.TrimSpace(jobID) == "" {
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveRestore == nil || current.ActiveRestore.ID != jobID {
		return ErrStateConflict
	}
	next := current.Clone()
	now := s.now().UTC().UnixMilli()
	if err := change(next.ActiveRestore, now); err != nil {
		return err
	}
	next.Revision++
	next.ActiveRestore.UpdatedUnixMillis = now
	return s.store.CompareAndSwap(ctx, current.Revision, next)
}
