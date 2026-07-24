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
	// ErrVerificationJobActive reports a backup or verification blocked by an active verification task.
	ErrVerificationJobActive = backupcontract.ErrVerificationJobActive
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
	// ErrDoctorUnhealthy reports that dependency preflight evidence is not healthy.
	ErrDoctorUnhealthy = errors.New("backup usecase: doctor is not healthy")
	// ErrControllerLeaderUnavailable reports that the current coordinator cannot be reached safely.
	ErrControllerLeaderUnavailable = errors.New("backup usecase: controller leader unavailable")
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
type VerificationTaskStatus = backupcontract.VerificationTaskStatus

const (
	VerificationTaskPending   = backupcontract.VerificationTaskPending
	VerificationTaskRunning   = backupcontract.VerificationTaskRunning
	VerificationTaskSucceeded = backupcontract.VerificationTaskSucceeded
	VerificationTaskFailed    = backupcontract.VerificationTaskFailed
)

type VerificationEvidence = backupcontract.VerificationEvidence
type VerificationTask = backupcontract.VerificationTask
type ErasureLedgerRecordReference = backupcontract.ErasureLedgerRecordReference
type State = backupcontract.State
type TriggerRequest = backupcontract.TriggerRequest
type SchedulePolicy = backupcontract.SchedulePolicy
type ScheduleDecision = backupcontract.ScheduleDecision
type RetentionPolicy = backupcontract.RetentionPolicy
type RetentionDecision = backupcontract.RetentionDecision
type GarbageCollectionResult = backupcontract.GarbageCollectionResult
type DoctorReport = backupcontract.DoctorReport

func cloneRestorePoints(points []RestorePoint) []RestorePoint {
	return backupcontract.CloneRestorePoints(points)
}

const (
	// DefaultRestorePointPageSize is used when callers omit a page size.
	DefaultRestorePointPageSize = 50
	// MaxRestorePointPageSize bounds one restore-point response.
	MaxRestorePointPageSize = 200
)

// RestorePointListRequest selects one stable newest-first restore-point page.
type RestorePointListRequest struct {
	// Cursor is the opaque keyset cursor returned by the preceding page.
	Cursor string
	// Limit is clamped to MaxRestorePointPageSize and defaults when zero.
	Limit int
	// IDQuery matches a case-insensitive restore-point ID substring.
	IDQuery string
	// HeldOnly includes only operator-held restore points when true.
	HeldOnly bool
}

// RestorePointPage is one bounded restore-point inventory page.
type RestorePointPage struct {
	// Items contains detached restore points ordered newest first.
	Items []RestorePoint
	// NextCursor is empty when no later matching page exists.
	NextCursor string
	// Total is the number of restore points matching the current filter.
	Total int
}

// PolicySnapshot is the non-secret effective backup policy exposed to operators.
type PolicySnapshot struct {
	IncrementalIntervalSeconds      int64
	RestorePointIntervalSeconds     int64
	IndependentFullIntervalSeconds  int64
	MaterializedFullIntervalSeconds int64
	MonthlyRetentionMonths          int
	ObjectLockDays                  int
	MaxParallelPartitions           int
	StagingMaxBytes                 uint64
	PrimaryRegion                   string
	SecondaryRegion                 string
	KMSRegion                       string
}

// DependencySnapshot is one non-secret dependency readiness observation.
type DependencySnapshot struct {
	Health Health
	Region string
}

// DependenciesSnapshot preserves individual backup dependency evidence.
type DependenciesSnapshot struct {
	Primary             DependencySnapshot
	Secondary           DependencySnapshot
	KMS                 DependencySnapshot
	Staging             DependencySnapshot
	UTC                 DependencySnapshot
	CheckedAtUnixMillis int64
}

// CapacitySnapshot reports bounded Controller restore-point reference usage.
type CapacitySnapshot struct {
	Total      int
	Held       int
	Pending    int
	Max        int
	WarningAt  int
	CriticalAt int
	Level      string
}

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
	// Verification is the latest durable manual verification task, when present.
	Verification *VerificationTask
	// VerificationAgeSeconds is nil until one scheduled or manual full repository audit succeeds.
	VerificationAgeSeconds *int64
	// PendingGarbageCount is the number of durable retention queue entries awaiting dual-repository collection.
	PendingGarbageCount int
	// FailureCategory is the latest bounded coordinator failure category.
	FailureCategory string
	// CoordinatorNodeID is the Controller leader observed for this cluster snapshot.
	CoordinatorNodeID uint64
	// ObservedAtUnixMillis is the UTC server observation time.
	ObservedAtUnixMillis int64
	// Running reports whether the leader coordinator loop is active.
	Running bool
	// MaxRecoveryPointAgeSeconds is the server-owned RPO health threshold.
	MaxRecoveryPointAgeSeconds int64
	// MaxVerificationAgeSeconds is the server-owned repository audit threshold.
	MaxVerificationAgeSeconds int64
	// Policy contains only effective non-secret startup configuration.
	Policy PolicySnapshot
	// Dependencies contains individual bounded readiness evidence.
	Dependencies DependenciesSnapshot
	// Capacity reports Controller restore-point reference usage.
	Capacity CapacitySnapshot
}
