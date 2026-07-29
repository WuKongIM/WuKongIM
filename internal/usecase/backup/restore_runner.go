package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

// RestoreStageResult proves one Hash Slot was staged on every current replica.
type RestoreStageResult struct {
	ReplicaNodeIDs []uint64
	LogicalBytes   uint64
}

// RestoreExecutor owns maintenance, all-replica staging, verification,
// logical activation, and rollback.
type RestoreExecutor interface {
	VerifyArchive(
		context.Context,
		backupcontract.RestoreJob,
	) error
	EnterMaintenance(
		context.Context,
		backupcontract.RestoreJob,
	) (string, error)
	StageSlot(
		context.Context,
		backupcontract.RestoreJob,
		uint16,
		uint32,
	) (RestoreStageResult, error)
	VerifySlot(
		context.Context,
		backupcontract.RestoreJob,
		uint16,
		uint32,
	) error
	ActivateRestore(context.Context, backupcontract.RestoreJob) error
	Rollback(context.Context, backupcontract.RestoreJob) error
	ExitMaintenance(context.Context, backupcontract.RestoreJob, bool) error
}

// RestoreRunner advances at most one bounded restore transition per call.
type RestoreRunner struct {
	scheduled *ScheduledService
	restore   *RestoreService
	executor  RestoreExecutor
	now       func() time.Time
}

// NewRestoreRunner creates the resumable restore worker.
func NewRestoreRunner(
	scheduled *ScheduledService,
	restore *RestoreService,
	executor RestoreExecutor,
	now func() time.Time,
) (*RestoreRunner, error) {
	if scheduled == nil || restore == nil || executor == nil || now == nil {
		return nil, fmt.Errorf("%w: restore runner dependencies", ErrInvalidRequest)
	}
	return &RestoreRunner{
		scheduled: scheduled, restore: restore, executor: executor, now: now,
	}, nil
}

// RunOnce resumes the active restore after process or Controller failover.
func (r *RestoreRunner) RunOnce(ctx context.Context) (bool, error) {
	state, err := r.scheduled.State(ctx)
	if err != nil {
		return false, err
	}
	if state.ActiveRestore == nil {
		return false, nil
	}
	job := *state.ActiveRestore
	canceled := job.CancelRequested
	expired := !r.now().UTC().Before(
		time.UnixMilli(job.DeadlineUnixMillis),
	)
	if job.Status != backupcontract.RestoreStatusFinalizing &&
		(canceled || expired) && !job.MaintenanceEntered {
		status := backupcontract.RestoreStatusFailed
		errorCode := "deadline_exceeded"
		if canceled {
			status = backupcontract.RestoreStatusCanceled
			errorCode = ""
		}
		return true, r.restore.FinishRestore(
			ctx, job.ID, status, errorCode,
		)
	}
	if job.Status != backupcontract.RestoreStatusFinalizing &&
		(canceled || expired) {
		return true, r.rollback(ctx, job)
	}
	executionContext := ctx
	cancelExecution := func() {}
	if job.Status != backupcontract.RestoreStatusFinalizing &&
		job.Status != backupcontract.RestoreStatusRollingBack {
		executionContext, cancelExecution = context.WithDeadline(
			ctx, time.UnixMilli(job.DeadlineUnixMillis),
		)
	}
	defer cancelExecution()
	switch job.Status {
	case backupcontract.RestoreStatusPreparing:
		if err := r.executor.VerifyArchive(executionContext, job); err != nil {
			finishErr := r.restore.FinishRestore(
				ctx, job.ID, backupcontract.RestoreStatusFailed,
				"archive_verification_failed",
			)
			return true, errors.Join(err, finishErr)
		}
		return true, r.restore.SetRestorePhase(
			ctx, job.ID, backupcontract.RestoreStatusValidated,
		)
	case backupcontract.RestoreStatusValidated:
		return true, r.restore.BeginMaintenance(ctx, job.ID)
	case backupcontract.RestoreStatusMaintenance,
		backupcontract.RestoreStatusStaging:
		if job.Status == backupcontract.RestoreStatusMaintenance &&
			job.PreviousActivation == "" {
			previous, err := r.executor.EnterMaintenance(executionContext, job)
			if err != nil {
				return r.beginRollback(ctx, job, "maintenance_failed", err)
			}
			return true, r.restore.MarkMaintenance(ctx, job.ID, previous)
		}
		for _, slot := range job.Slots {
			if slot.Status == backupcontract.RestoreSlotStatusStaged ||
				slot.Status == backupcontract.RestoreSlotStatusVerified {
				continue
			}
			claimed, err := r.restore.ClaimRestoreSlot(
				ctx, job.ID, slot.HashSlot,
			)
			if err != nil {
				return true, err
			}
			result, err := r.executor.StageSlot(
				executionContext, job, slot.HashSlot, claimed.Attempt,
			)
			if err != nil {
				return r.beginRollback(ctx, job, "staging_failed", err)
			}
			return true, r.restore.CompleteRestoreSlot(
				ctx, job.ID, slot.HashSlot, claimed.Attempt,
				result.ReplicaNodeIDs, result.LogicalBytes,
			)
		}
		return true, r.restore.SetRestorePhase(
			ctx, job.ID, backupcontract.RestoreStatusVerifying,
		)
	case backupcontract.RestoreStatusVerifying:
		for _, slot := range job.Slots {
			if slot.Status == backupcontract.RestoreSlotStatusVerified {
				continue
			}
			if slot.Status != backupcontract.RestoreSlotStatusStaged {
				return true, ErrStateConflict
			}
			if err := r.executor.VerifySlot(
				executionContext, job, slot.HashSlot, slot.Attempt,
			); err != nil {
				return r.beginRollback(ctx, job, "verification_failed", err)
			}
			return true, r.restore.VerifyRestoreSlot(
				ctx, job.ID, slot.HashSlot, slot.Attempt,
			)
		}
		return true, r.restore.SetRestorePhase(
			ctx, job.ID, backupcontract.RestoreStatusSwitching,
		)
	case backupcontract.RestoreStatusSwitching:
		if err := r.executor.ActivateRestore(executionContext, job); err != nil {
			return r.beginRollback(ctx, job, "switch_failed", err)
		}
		return true, r.restore.SetRestorePhase(
			ctx, job.ID, backupcontract.RestoreStatusFinalizing,
		)
	case backupcontract.RestoreStatusFinalizing:
		if err := r.executor.ExitMaintenance(ctx, job, true); err != nil {
			return true, err
		}
		return true, r.restore.FinishRestore(
			ctx, job.ID, backupcontract.RestoreStatusSucceeded, "",
		)
	case backupcontract.RestoreStatusRollingBack:
		return true, r.rollback(ctx, job)
	default:
		return true, ErrStateConflict
	}
}

func (r *RestoreRunner) rollback(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	if job.Status != backupcontract.RestoreStatusRollingBack {
		errorCode := "deadline_exceeded"
		if job.CancelRequested {
			errorCode = "canceled"
		}
		if err := r.restore.BeginRollback(ctx, job.ID, errorCode); err != nil {
			return err
		}
		job.Status = backupcontract.RestoreStatusRollingBack
		job.ErrorCode = errorCode
	}
	if err := r.executor.Rollback(ctx, job); err != nil {
		return err
	}
	if err := r.executor.ExitMaintenance(ctx, job, false); err != nil {
		return err
	}
	status := backupcontract.RestoreStatusFailed
	errorCode := job.ErrorCode
	if errorCode == "" {
		errorCode = "restore_failed"
	}
	if job.CancelRequested {
		status = backupcontract.RestoreStatusCanceled
		errorCode = ""
	}
	return r.restore.FinishRestore(ctx, job.ID, status, errorCode)
}

func (r *RestoreRunner) beginRollback(
	ctx context.Context,
	job backupcontract.RestoreJob,
	errorCode string,
	cause error,
) (bool, error) {
	err := r.restore.BeginRollback(ctx, job.ID, errorCode)
	return true, errors.Join(cause, err)
}
