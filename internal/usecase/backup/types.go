package backup

import (
	"errors"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

var (
	// ErrDisabled reports a backup operation requested while backup is disabled.
	ErrDisabled = errors.New("backup usecase: disabled")
	// ErrInvalidRequest reports an invalid backup usecase request.
	ErrInvalidRequest = errors.New("backup usecase: invalid request")
	// ErrJobActive reports a trigger attempted while another backup job is active.
	ErrJobActive = backupcontract.ErrJobActive
	// ErrJobNotFound reports a job fence that does not match the active job.
	ErrJobNotFound = errors.New("backup usecase: active job not found")
	// ErrStateConflict reports an optimistic coordination-state conflict.
	ErrStateConflict = backupcontract.ErrStateConflict
	// ErrPartitionsIncomplete reports a publish attempt missing logical partitions.
	ErrPartitionsIncomplete = errors.New("backup usecase: logical partitions incomplete")
	// ErrRestorePointNotFound reports an unknown published restore-point identity.
	ErrRestorePointNotFound = errors.New("backup usecase: restore point not found")
	// ErrErasureLedgerPending reports a different record already reserved the next commit sequence.
	ErrErasureLedgerPending = errors.New("backup usecase: erasure ledger commit pending")
)

type JobStatus = backupcontract.JobStatus

const (
	JobStatusPreparing  = backupcontract.JobStatusPreparing
	JobStatusCapturing  = backupcontract.JobStatusCapturing
	JobStatusPublishing = backupcontract.JobStatusPublishing
	JobStatusCompleted  = backupcontract.JobStatusCompleted
	JobStatusDegraded   = backupcontract.JobStatusDegraded
	JobStatusFailed     = backupcontract.JobStatusFailed
	JobStatusCanceled   = backupcontract.JobStatusCanceled
)

type Health = backupcontract.Health

const (
	HealthDisabled = backupcontract.HealthDisabled
	HealthUnknown  = backupcontract.HealthUnknown
	HealthHealthy  = backupcontract.HealthHealthy
	HealthDegraded = backupcontract.HealthDegraded
	HealthFailed   = backupcontract.HealthFailed
)

type PartitionReport = backupcontract.PartitionReport
type Job = backupcontract.Job
type RestorePoint = backupcontract.RestorePoint
type Verification = backupcontract.Verification
type ErasureLedgerRecordReference = backupcontract.ErasureLedgerRecordReference
type State = backupcontract.State
type TriggerRequest = backupcontract.TriggerRequest
type SchedulePolicy = backupcontract.SchedulePolicy
type ScheduleDecision = backupcontract.ScheduleDecision
type RetentionPolicy = backupcontract.RetentionPolicy
type RetentionDecision = backupcontract.RetentionDecision
type GarbageCollectionResult = backupcontract.GarbageCollectionResult

// StatusSnapshot is the read-only backup status exposed to access adapters.
type StatusSnapshot struct {
	// Enabled reports whether backup coordination is configured.
	Enabled bool
	// Health reports healthy, degraded, failed, unknown, or disabled.
	Health Health
	// RecoveryPointAgeSeconds is nil when no verified restore point exists.
	RecoveryPointAgeSeconds *int64
	// Active is a detached copy of the active job, when present.
	Active *Job
	// Latest is the newest published restore point, when present.
	Latest *RestorePoint
	// VerificationAgeSeconds is nil until one scheduled or manual full repository audit succeeds.
	VerificationAgeSeconds *int64
	// PendingGarbageCount is the number of durable retention queue entries awaiting dual-repository collection.
	PendingGarbageCount int
	// FailureCategory is the latest bounded coordinator failure category.
	FailureCategory string
}
