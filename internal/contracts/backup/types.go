// Package backup contains lightweight backup coordination contracts shared by
// use cases, node-local runtimes, and infrastructure adapters.
package backup

import (
	"errors"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

var (
	// ErrJobActive reports a trigger attempted while another backup job is active.
	ErrJobActive = errors.New("backup usecase: job already active")
	// ErrStateConflict reports an optimistic coordination-state conflict.
	ErrStateConflict = errors.New("backup usecase: state conflict")
)

// JobStatus identifies one cluster backup job lifecycle state.
type JobStatus string

const (
	JobStatusPreparing  JobStatus = "preparing"
	JobStatusCapturing  JobStatus = "capturing"
	JobStatusPublishing JobStatus = "publishing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusDegraded   JobStatus = "degraded"
	JobStatusFailed     JobStatus = "failed"
	JobStatusCanceled   JobStatus = "canceled"
)

// Health identifies the current backup SLO state without collapsing missing evidence to healthy.
type Health string

const (
	HealthDisabled Health = "disabled"
	HealthUnknown  Health = "unknown"
	HealthHealthy  Health = "healthy"
	HealthDegraded Health = "degraded"
	HealthFailed   Health = "failed"
)

// PartitionReport is the bounded completion summary for one logical hash slot.
type PartitionReport struct {
	// JobID and BackupEpoch fence the active capture attempt.
	JobID       string
	BackupEpoch uint64
	// HashSlot and RaftIndex identify the committed logical cut.
	HashSlot  uint16
	RaftIndex uint64
	// CommittedAtUnixMillis is the UTC partition watermark.
	CommittedAtUnixMillis int64
	// ManifestKey and ManifestSHA256 authenticate the external partition index.
	ManifestKey    string
	ManifestSHA256 string
	// ObjectCount and CiphertextBytes are bounded publication summaries.
	ObjectCount     uint64
	CiphertextBytes uint64
}

// Job is one bounded cluster-coordinated backup attempt.
type Job struct {
	// ID and Epoch identify and fence this cluster backup attempt.
	ID    string
	Epoch uint64
	// Kind and Status describe the creation strategy and lifecycle phase.
	Kind   backupartifact.RestorePointKind
	Status JobStatus
	// HashSlotCount is the immutable number of required logical partitions.
	HashSlotCount uint16
	// ConfigFingerprint proves non-secret policy agreement across retries.
	ConfigFingerprint string
	// RestorePointID is allocated before publication for idempotent retries.
	RestorePointID string
	// BaseRestorePointID identifies the authenticated incremental base.
	BaseRestorePointID string
	// StartedAtUnixMillis and UpdatedAtUnixMillis are UTC lifecycle timestamps.
	StartedAtUnixMillis int64
	UpdatedAtUnixMillis int64
	// Partitions contains at most one sorted report per hash slot.
	Partitions []PartitionReport
	// FailureCategory is a bounded operator-facing error class.
	FailureCategory string
}

// RestorePoint is the bounded published restore-point reference retained in control state.
type RestorePoint struct {
	// ID identifies the discoverable signed recovery point.
	ID string
	// JobID and BackupEpoch identify its source capture.
	JobID       string
	BackupEpoch uint64
	// Kind identifies incremental, synthetic, or materialized creation.
	Kind backupartifact.RestorePointKind
	// EffectiveAtUnixMillis is the oldest included cut watermark.
	EffectiveAtUnixMillis int64
	// CreatedAtUnixMillis is the UTC manifest publication time.
	CreatedAtUnixMillis int64
	// ManifestSHA256 authenticates the exact signed top-level bytes.
	ManifestSHA256 string
	// PrimaryVerified and SecondaryVerified record dual-copy evidence.
	PrimaryVerified   bool
	SecondaryVerified bool
	// Held prevents retention collection while true.
	Held bool
}

// Verification is bounded evidence from an explicit dual-repository audit.
type Verification struct {
	// RestorePointID identifies the audited recovery point.
	RestorePointID string
	// VerifiedAtUnixMillis is the UTC audit completion time.
	VerifiedAtUnixMillis int64
	// PrimaryVerified and SecondaryVerified require full graph verification.
	PrimaryVerified   bool
	SecondaryVerified bool
	// ManifestSHA256 authenticates the audited top-level bytes.
	ManifestSHA256 string
}

// State is the bounded Controller-persisted backup coordination state.
type State struct {
	// Revision is the Controller compare-and-swap revision.
	Revision uint64
	// LastEpoch is the latest allocated backup fence.
	LastEpoch uint64
	// Active contains the only in-progress job.
	Active *Job
	// RestorePoints contains selectable bounded recovery references.
	RestorePoints []RestorePoint
	// PendingGarbage contains expired references awaiting external collection.
	PendingGarbage []RestorePoint
}

// Clone returns a deep copy safe for mutation by a caller.
func (s State) Clone() State {
	out := s
	if s.Active != nil {
		job := *s.Active
		job.Partitions = append([]PartitionReport(nil), s.Active.Partitions...)
		out.Active = &job
	}
	out.RestorePoints = append([]RestorePoint(nil), s.RestorePoints...)
	out.PendingGarbage = append([]RestorePoint(nil), s.PendingGarbage...)
	return out
}

// TriggerRequest describes one manual or scheduled backup trigger.
type TriggerRequest struct {
	// Kind selects incremental, synthetic, or materialized capture.
	Kind backupartifact.RestorePointKind
	// ConfigFingerprint proves non-secret policy agreement.
	ConfigFingerprint string
}

// SchedulePolicy controls pure automatic backup scheduling decisions.
type SchedulePolicy struct {
	// RestorePointInterval is the configured RPO publication cadence.
	RestorePointInterval time.Duration
	// SyntheticFullInterval is the target independent-manifest cadence. Until
	// object-reuse flattening is qualified, the scheduler emits a safe
	// materialized-full fallback at this cadence.
	SyntheticFullInterval time.Duration
	// MaterializedFullInterval is the source reread cadence.
	MaterializedFullInterval time.Duration
}

// ScheduleDecision is one side-effect-free scheduler outcome.
type ScheduleDecision struct {
	// Due reports whether a new job should be created.
	Due bool
	// Kind is meaningful only when Due is true.
	Kind backupartifact.RestorePointKind
	// Reason is a bounded audit/debug explanation.
	Reason string
}

// RetentionPolicy controls fixed UTC restore-point tiers.
type RetentionPolicy struct {
	// MonthlyMonths enables the optional materialized monthly tier.
	MonthlyMonths int
}

// RetentionDecision is a deterministic reference-level retention plan.
type RetentionDecision struct {
	// Retain contains references that remain selectable.
	Retain []RestorePoint
	// Collect contains references moved to durable pending garbage.
	Collect []RestorePoint
}

// GarbageCollectionResult is bounded evidence from one dual-repository sweep.
type GarbageCollectionResult struct {
	// DeletedObjects counts exact versions removed across repositories.
	DeletedObjects int
	// CompletedRestorePointIDs identifies fully collected pending entries.
	CompletedRestorePointIDs []string
}

// RestoreStatus identifies the explicit recovery lifecycle state.
type RestoreStatus string

const (
	RestoreStatusPlanned    RestoreStatus = "planned"
	RestoreStatusInstalling RestoreStatus = "installing"
	RestoreStatusInstalled  RestoreStatus = "installed"
	RestoreStatusVerified   RestoreStatus = "verified"
	RestoreStatusActivated  RestoreStatus = "activated"
	RestoreStatusAbandoned  RestoreStatus = "abandoned"
)

// RestorePartition records one idempotent logical-partition installation result.
type RestorePartition struct {
	// HashSlot identifies the restored logical partition.
	HashSlot uint16
	// EvidenceVersion distinguishes explicit empty evidence from missing evidence.
	EvidenceVersion uint32
	// Installed and Verified record durable lifecycle progress.
	Installed bool
	Verified  bool
	// PlainBytes, MetadataRecordCount, and MessageCount are bounded progress summaries.
	PlainBytes          uint64
	MetadataRecordCount uint64
	MessageCount        uint64
	// MaxMessageID is the restored node-independent allocator fence.
	MaxMessageID uint64
	// MetadataSHA256 authenticates the canonical post-transform metadata view.
	MetadataSHA256 string
	// FailureCategory is a bounded operator-facing error class.
	FailureCategory string
	// UpdatedAtUnixMillis is the UTC progress timestamp.
	UpdatedAtUnixMillis int64
}

// RestorePlan is the immutable selection plus mutable bounded recovery progress.
type RestorePlan struct {
	// ID identifies the immutable restore plan.
	ID string
	// RestorePointID and ManifestSHA256 select exact signed source bytes.
	RestorePointID string
	ManifestSHA256 string
	// Repository selects the primary or secondary installation copy.
	Repository string
	// SourceClusterID and SourceGeneration identify the backed-up incarnation.
	SourceClusterID  string
	SourceGeneration string
	// TargetClusterID and TargetGeneration identify the fresh successor.
	TargetClusterID  string
	TargetGeneration string
	// HashSlotCount must match source and target.
	HashSlotCount uint16
	// InvalidateTokens applies the explicit restore-time credential transform.
	InvalidateTokens bool
	// EstimatedPlainBytes and EstimatedCipherBytes preserve unknown as nil.
	EstimatedPlainBytes  *uint64
	EstimatedCipherBytes *uint64
	// Status is the explicit restore lifecycle phase.
	Status RestoreStatus
	// CreatedAtUnixMillis and UpdatedAtUnixMillis are UTC lifecycle times.
	CreatedAtUnixMillis int64
	UpdatedAtUnixMillis int64
	// VerifiedAtUnixMillis records successful full semantic verification.
	VerifiedAtUnixMillis int64
	// ActivatedAtUnixMillis records explicit operator activation.
	ActivatedAtUnixMillis int64
	// ActivationFenceDigest authenticates reviewed old-cluster fencing evidence.
	ActivationFenceDigest string
	// Partitions contains exactly one progress record per hash slot.
	Partitions []RestorePartition
}
