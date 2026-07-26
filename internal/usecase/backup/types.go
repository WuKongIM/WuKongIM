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
	// LocalCaptureStatuses is the bounded node-local worker projection used to
	// diagnose capture progress independently from durable frontier authority.
	LocalCaptureStatuses []backupcontract.SlotCaptureStatus
	// ErasureStreams exposes only per-Slot sequence and pending progress.
	ErasureStreams []ErasureStreamProgress
}
