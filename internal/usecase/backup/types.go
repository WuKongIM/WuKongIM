package backup

import (
	"errors"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

var (
	// ErrDisabled reports a backup operation requested while backup is disabled.
	ErrDisabled = errors.New("backup usecase: disabled")
	// ErrInvalidRequest reports an invalid backup usecase request.
	ErrInvalidRequest = errors.New("backup usecase: invalid request")
	// ErrStateConflict reports an optimistic coordination-state conflict.
	ErrStateConflict = backupcontract.ErrStateConflict
	// ErrPartitionsIncomplete reports a publish attempt missing logical partitions.
	ErrPartitionsIncomplete = errors.New("backup usecase: logical partitions incomplete")
	// ErrCheckpointUnhealthy reports a Slot capture that cannot enter a recovery cut.
	ErrCheckpointUnhealthy = errors.New("backup usecase: checkpoint Slot unhealthy")
	// ErrRestorePlanNotFound reports an unknown immutable restore plan.
	ErrRestorePlanNotFound = errors.New("backup usecase: restore plan not found")
	// ErrCheckpointNotFound reports an unknown immutable catalog checkpoint.
	ErrCheckpointNotFound = errors.New("backup usecase: checkpoint not found")
	// ErrErasureLedgerPending reports a different record already reserved the next commit sequence.
	ErrErasureLedgerPending = errors.New("backup usecase: erasure ledger commit pending")
	// ErrDoctorUnhealthy reports that dependency preflight evidence is not healthy.
	ErrDoctorUnhealthy = errors.New("backup usecase: doctor is not healthy")
	// ErrControllerLeaderUnavailable reports that the current coordinator cannot be reached safely.
	ErrControllerLeaderUnavailable = errors.New("backup usecase: controller leader unavailable")
	// ErrSourceFenceExists reports a conflicting request after the generation was irreversibly fenced.
	ErrSourceFenceExists = errors.New("backup usecase: source generation is already fenced")
	// ErrInvalidRestoreArtifact reports an invalid authenticated recovery artifact.
	ErrInvalidRestoreArtifact = backupartifact.ErrInvalidManifest
	// ErrRestoreArtifactNotFound reports a missing immutable recovery artifact.
	ErrRestoreArtifactNotFound = backupartifact.ErrObjectNotFound
)

type SourceFenceRecord = backupartifact.SourceFenceRecord
type SourceFenceReceipt = backupartifact.SourceFenceReceipt
type RestoreActivationEvidence = backupartifact.RestoreActivationEvidence
type ErasureStreamHead = backupartifact.ErasureStreamHead

// CloneRestoreActivationEvidence returns a detached recovery evidence value.
func CloneRestoreActivationEvidence(
	evidence *RestoreActivationEvidence,
) *RestoreActivationEvidence {
	return backupartifact.CloneRestoreActivationEvidence(evidence)
}

type Health = backupcontract.Health

const (
	HealthDisabled = backupcontract.HealthDisabled
	HealthUnknown  = backupcontract.HealthUnknown
	HealthHealthy  = backupcontract.HealthHealthy
	HealthDegraded = backupcontract.HealthDegraded
	HealthFailed   = backupcontract.HealthFailed
)

type ErasureLedgerRecordReference = backupcontract.ErasureLedgerRecordReference
type ErasureStreamState = backupcontract.ErasureStreamState
type GenerationGCCursor = backupcontract.GenerationGCCursor
type StreamFrontier = backupcontract.StreamFrontier
type SlotCaptureLease = backupcontract.SlotCaptureLease
type SlotFrontier = backupcontract.SlotFrontier
type State = backupcontract.State
type DoctorReport = backupcontract.DoctorReport
type SlotCaptureStatus = backupcontract.SlotCaptureStatus

// PolicySnapshot is the non-secret effective backup policy exposed to operators.
type PolicySnapshot struct {
	CaptureReconcileIntervalSeconds int64
	CheckpointIntervalSeconds       int64
	CaptureWorkerCount              int
	TargetSegmentBytes              uint64
	MaxSegmentBytes                 uint64
	MaxSegmentOpenDurationSeconds   int64
	StagingMaxBytes                 uint64
	SourcePinMaxAgeSeconds          int64
	MaxSourcePinnedBytes            uint64
}

// CheckpointRetentionPolicy controls sparse immutable checkpoint retention.
type CheckpointRetentionPolicy struct {
	// MonthlyMonths enables the optional monthly recovery tier.
	MonthlyMonths int
}

// CaptureLeaseSnapshot is one sanitized durable Slot takeover observation.
type CaptureLeaseSnapshot struct {
	// HashSlot and SlotID identify the logical capture partition and its Raft Group.
	HashSlot uint16
	SlotID   uint32
	// SourceSlotID identifies the physical Raft index space used by the durable
	// metadata frontier. It differs from SlotID while a remapped Slot rebases.
	SourceSlotID uint32
	// HolderNodeID, LeaderTerm, and ConfigEpoch identify exact Slot authority.
	HolderNodeID uint64
	LeaderTerm   uint64
	ConfigEpoch  uint64
	// Generation identifies the immutable segment graph protected by this lease.
	Generation string
	// LeaseSequence exposes monotonic takeover order.
	LeaseSequence uint64
	// FrontierRevision exposes monotonic lease and stream-head commits.
	FrontierRevision uint64
	// LastPromotionPreviousGeneration and LastPromotionReason prove why the
	// current Generation durably replaced its immediate predecessor.
	LastPromotionPreviousGeneration string
	LastPromotionReason             string
	// LastPromotionAtUnixMillis is the durable promotion commit time.
	LastPromotionAtUnixMillis int64
	// MetadataSourceWatermark and MessageSourceWatermark are durable source positions.
	MetadataSourceWatermark uint64
	MessageSourceWatermark  uint64
	// AcquiredAtUnixMillis is the latest durable takeover time.
	AcquiredAtUnixMillis int64
	// SourcePinStartedAtUnixMillis is the durable age origin of the retained
	// metadata source floor and is not reset by lease takeover.
	SourcePinStartedAtUnixMillis int64
	// FrontierUpdatedUnixMillis is the latest lease or stream-head commit time.
	FrontierUpdatedUnixMillis int64
}

// ErasureStreamProgress is a sanitized permanent-erasure stream observation.
type ErasureStreamProgress struct {
	// HashSlot identifies the independently sequenced stream.
	HashSlot uint16
	// Sequence is the latest durably committed position.
	Sequence uint64
	// Pending reports whether one later record is reserved for repair.
	Pending bool
}

// IntegrityAuditCursorSnapshot is the non-secret durable audit position
// exposed to operators. Opaque repository object coordinates stay internal.
type IntegrityAuditCursorSnapshot struct {
	// CycleID identifies one fixed catalog audit decision.
	CycleID string
	// ScrubEpoch identifies the periodic latent-damage pass.
	ScrubEpoch uint64
	// CatalogSequence is the immutable catalog head covered by this cycle.
	CatalogSequence uint64
	// HashSlot and Generation identify the currently inspected isolation boundary.
	HashSlot   uint16
	Generation string
	// Phase is inspect, repair, revalidate, rebase, or complete.
	Phase backupcontract.IntegrityAuditPhase
	// Repository and Category contain only bounded failure classifications.
	Repository string
	Category   backupcontract.IntegrityCorruptionCategory
	// UpdatedAtUnixMillis is the latest durable cursor transition time.
	UpdatedAtUnixMillis int64
}

// SlotIntegrityAuditSnapshot is one bounded operator-facing Slot health record.
type SlotIntegrityAuditSnapshot struct {
	// HashSlot identifies the independently isolated logical partition.
	HashSlot uint16
	// Generation is the affected immutable Slot graph.
	Generation string
	// Health is healthy, degraded, rebase_required, or failed.
	Health backupcontract.SlotAuditHealth
	// Repository and Category describe only the bounded repair reason.
	Repository string
	Category   backupcontract.IntegrityCorruptionCategory
	// LastSuccessAtUnixMillis is the latest complete artifact validation.
	LastSuccessAtUnixMillis int64
	// UpdatedAtUnixMillis is the latest durable health transition time.
	UpdatedAtUnixMillis int64
}

// IntegrityAuditSnapshot is the sanitized durable integrity-audit projection.
type IntegrityAuditSnapshot struct {
	// Revision fences audit transitions independently from unrelated state.
	Revision uint64
	// Cursor is nil before the first integrity-audit cycle.
	Cursor *IntegrityAuditCursorSnapshot
	// Slots contains at most one sorted health record per Hash Slot.
	Slots []SlotIntegrityAuditSnapshot
	// DebtObjects is the bounded remaining-artifact estimate.
	DebtObjects uint64
	// LastSuccessAtUnixMillis is the latest successful full validation.
	LastSuccessAtUnixMillis int64
	// UpdatedAtUnixMillis is the latest durable audit progress time.
	UpdatedAtUnixMillis int64
}

// CompactionSlotSnapshot is one bounded pending Generation replacement.
type CompactionSlotSnapshot struct {
	// HashSlot identifies the independently replaceable partition.
	HashSlot uint16
	// Generation remains authoritative until TargetGeneration is validated.
	Generation       string
	TargetGeneration string
	// Reason is the bounded replacement trigger.
	Reason string
	// StartedAtUnixMillis is preserved across retries.
	StartedAtUnixMillis int64
}

// CompactionSnapshot is the cluster-wide pending Generation projection.
type CompactionSnapshot struct {
	// DebtSlots is the number of pending Slot replacements.
	DebtSlots int
	// Slots contains at most one sorted record per Hash Slot.
	Slots []CompactionSlotSnapshot
}

// GenerationGCCursorSnapshot is one sanitized repository sweep projection.
type GenerationGCCursorSnapshot struct {
	// Repository identifies the explicit failure-domain copy.
	Repository string
	// Revision and CycleID identify the current bounded sweep.
	Revision uint64
	CycleID  string
	// Complete reports whether this repository reached the fixed sweep cut.
	Complete bool
	// UpdatedAtUnixMillis is the latest durable cursor transition time.
	UpdatedAtUnixMillis int64
}

// GarbageCollectionSnapshot is the bounded dual-repository GC projection.
type GarbageCollectionSnapshot struct {
	// DebtRepositories counts current repository sweeps that are incomplete.
	DebtRepositories int
	// Cursors contains no repository object keys or credentials.
	Cursors []GenerationGCCursorSnapshot
}

// StatusSnapshot is the read-only backup status exposed to access adapters.
type StatusSnapshot struct {
	// Enabled reports whether backup coordination is configured.
	Enabled bool
	// Health reports healthy, degraded, failed, unknown, or disabled.
	Health Health
	// CheckpointAgeSeconds is nil until the first immutable checkpoint exists.
	CheckpointAgeSeconds *int64
	// LatestCheckpoint is the newest immutable continuous-capture checkpoint.
	LatestCheckpoint *CheckpointSummary
	// FailureCategory is the latest bounded coordinator failure category.
	FailureCategory string
	// CoordinatorNodeID is the Controller leader observed for this cluster snapshot.
	CoordinatorNodeID uint64
	// ObservedAtUnixMillis is the UTC server observation time.
	ObservedAtUnixMillis int64
	// Running reports whether the leader coordinator loop is active.
	Running bool
	// MaxCheckpointAgeSeconds is the server-owned checkpoint health threshold.
	MaxCheckpointAgeSeconds int64
	// Policy contains only effective non-secret startup configuration.
	Policy PolicySnapshot
	// CaptureLeases is the bounded sanitized durable lease view for every initialized Hash Slot.
	CaptureLeases []CaptureLeaseSnapshot
	// CaptureStatuses is the bounded cluster-wide per-Slot worker projection
	// assembled at the Manager boundary without entering Controller state.
	CaptureStatuses []backupcontract.SlotCaptureStatus
	// CaptureStatusComplete reports whether every durable lease holder returned
	// one current observation for each Slot it owns.
	CaptureStatusComplete bool
	// CaptureStatusMissingNodeIDs identifies unreachable lease holders.
	CaptureStatusMissingNodeIDs []uint64
	// CaptureStatusMissingSlots identifies durable leases without an observation.
	CaptureStatusMissingSlots []uint16
	// IntegrityAudit is the bounded durable cursor and per-Slot health projection.
	IntegrityAudit IntegrityAuditSnapshot
	// Compaction exposes pending immutable Generation replacements.
	Compaction CompactionSnapshot
	// GarbageCollection exposes bounded per-repository sweep progress.
	GarbageCollection GarbageCollectionSnapshot
	// ErasureStreams exposes only per-Slot sequence and pending progress.
	ErasureStreams []ErasureStreamProgress
	// Restore is the active recovery projection when this process is in restore mode.
	Restore *RestoreProgress
}
