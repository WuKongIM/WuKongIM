// Package backup contains lightweight backup coordination contracts shared by
// use cases, node-local runtimes, and infrastructure adapters.
package backup

import (
	"errors"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

var (
	// ErrStateConflict reports an optimistic coordination-state conflict.
	ErrStateConflict = errors.New("backup usecase: state conflict")
)

// CheckpointReplicaAction identifies one bounded target-snapshot transfer step.
type CheckpointReplicaAction string

const (
	CheckpointReplicaBegin   CheckpointReplicaAction = "begin"
	CheckpointReplicaChunk   CheckpointReplicaAction = "chunk"
	CheckpointReplicaCommit  CheckpointReplicaAction = "commit"
	CheckpointReplicaStatus  CheckpointReplicaAction = "status"
	CheckpointReplicaCleanup CheckpointReplicaAction = "cleanup"
)

// CheckpointReplicaFileKind identifies one plaintext target snapshot stream.
type CheckpointReplicaFileKind string

const (
	CheckpointReplicaMetadata CheckpointReplicaFileKind = "metadata"
	CheckpointReplicaMessages CheckpointReplicaFileKind = "messages"
	CheckpointReplicaErasures CheckpointReplicaFileKind = "erasures"
)

// CheckpointReplicaFence binds one replica transfer to Controller and Slot
// authority without exposing repository or key-authority credentials.
type CheckpointReplicaFence struct {
	PlanID           string
	CheckpointID     string
	CheckpointSHA256 string
	TargetGeneration string
	HashSlot         uint16
	TargetSlotID     uint32
	ReplicaCount     uint32
	LeaderNodeID     uint64
	LeaderTerm       uint64
	ConfigEpoch      uint64
	Attempt          uint64
	InvalidateTokens bool
}

// CheckpointReplicaFile authenticates one bounded plaintext target stream.
type CheckpointReplicaFile struct {
	Kind    CheckpointReplicaFileKind
	Ordinal uint32
	Size    int64
	SHA256  string
}

// CheckpointReplicaRequest carries one begin, chunk, commit, or status step.
type CheckpointReplicaRequest struct {
	Action CheckpointReplicaAction
	Fence  CheckpointReplicaFence
	Files  []CheckpointReplicaFile
	File   CheckpointReplicaFile
	Offset int64
	Data   []byte
	// Evidence and progress are copied into a durable follower receipt at begin.
	Evidence backupartifact.RestoreEvidence
	// FinalMessageCount and FinalMaxMessageID describe live rows after
	// permanent erasure has been applied to the exported target snapshot.
	FinalMessageCount     uint64
	FinalMaxMessageID     uint64
	DownloadedBytes       uint64
	InstalledAtUnixMillis int64
}

// CheckpointReplicaResponse reports idempotent staging or completed install.
type CheckpointReplicaResponse struct {
	AcceptedOffset int64
	Completed      bool
	MetadataSHA256 string
	InstalledBytes uint64
}

// Health identifies the current backup SLO state without collapsing missing evidence to healthy.
type Health string

const (
	HealthDisabled Health = "disabled"
	HealthUnknown  Health = "unknown"
	HealthHealthy  Health = "healthy"
	HealthDegraded Health = "degraded"
	HealthFailed   Health = "failed"
)

// DoctorReport is one bounded dependency readiness observation.
type DoctorReport struct {
	// Primary, Secondary, KeyAuthority, Staging, and UTC preserve readiness.
	Primary      Health
	Secondary    Health
	KeyAuthority Health
	Staging      Health
	UTC          Health
	// CheckedAtUnixMillis is the UTC server observation time.
	CheckedAtUnixMillis int64
	// FailureCategory identifies the first failed dependency without raw details.
	FailureCategory string
}

// ErasureLedgerRecordReference is the bounded Controller coordination fence for
// one dual-repository permanent-erasure ledger record.
type ErasureLedgerRecordReference struct {
	// HashSlot identifies the independently sequenced erasure stream.
	HashSlot uint16
	// Sequence is the next contiguous position within HashSlot.
	Sequence uint64
	// EventID is the deterministic permanent-erasure event identity.
	EventID string
	// RecordKey points to the immutable signed event record.
	RecordKey string
	// RecordSHA256 authenticates the exact signed record bytes.
	RecordSHA256 string
}

// ErasureStreamState is the bounded Controller coordination state for one
// Hash Slot permanent-erasure stream.
type ErasureStreamState struct {
	// HashSlot identifies the independently sequenced stream.
	HashSlot uint16
	// Head authenticates the latest dual-repository commit, or is nil before the first commit.
	Head *backupartifact.ErasureStreamHead
	// Pending is the one reserved record whose commit marker may need repair.
	Pending *ErasureLedgerRecordReference
	// LastCommitted preserves constant-space retry recognition for the latest event.
	LastCommitted *ErasureLedgerRecordReference
}

// PermanentMessageErasure identifies one accepted permanent Channel message-prefix deletion.
type PermanentMessageErasure struct {
	// ChannelID identifies the permanently erased Channel log.
	ChannelID string
	// ChannelType identifies the Channel namespace.
	ChannelType uint8
	// ThroughSeq is the inclusive highest permanently erased message sequence.
	ThroughSeq uint64
	// RequestedAtUnixMillis is the accepted operator request time in UTC.
	RequestedAtUnixMillis int64
}

// ErasureLedgerReceipt identifies one durable dual-repository ledger commit.
type ErasureLedgerReceipt struct {
	// HashSlot identifies the independently sequenced erasure stream.
	HashSlot uint16
	// Sequence is the contiguous committed position within HashSlot.
	Sequence uint64
	// EventID is the deterministic permanent-erasure identity.
	EventID string
}

// GenerationGCCursor is one bounded durable repository sweep position.
type GenerationGCCursor struct {
	// Repository identifies one explicit failure-domain copy.
	Repository string
	// Revision fences independent updates to this repository cursor.
	Revision uint64
	// CycleID identifies one retryable protection/cutoff decision.
	CycleID string
	// CatalogRetentionRevision is the hold/release revision fixed by this cycle.
	CatalogRetentionRevision uint64
	// AfterKey is the last fully processed lexicographic repository key.
	AfterKey string
	// CutoffUnixMillis freezes Object Lock plus safety-window eligibility.
	CutoffUnixMillis int64
	// Complete prevents a healthy repository from rescanning while its peer retries.
	Complete bool
	// UpdatedAtUnixMillis is the latest durable progress time.
	UpdatedAtUnixMillis int64
}

// State is the bounded Controller-persisted backup coordination state.
type State struct {
	// Revision is the Controller compare-and-swap revision.
	Revision uint64
	// SourceFence is the irreversible generation-level ordinary-write fence.
	SourceFence *backupartifact.SourceFenceRecord
	// SlotFrontiers contains at most one sorted compact continuous-capture record per Hash Slot.
	SlotFrontiers []SlotFrontier
	// CatalogHead is the only Controller-resident pointer into immutable checkpoint history.
	CatalogHead *backupartifact.CatalogPageReference
	// CatalogAuditRootSequence is the oldest page that may contain a checkpoint
	// in the current sparse retention decision. Exact references stay external.
	CatalogAuditRootSequence uint64
	// CatalogRetentionRevision increments only after a hold/release catalog
	// decision becomes the durable head and fences concurrent Generation GC.
	CatalogRetentionRevision uint64
	// ErasureStreams contains at most one sorted bounded state per Hash Slot.
	ErasureStreams []ErasureStreamState
	// GenerationGCCursors contains at most one independent durable cursor per repository.
	GenerationGCCursors []GenerationGCCursor
	// IntegrityAudit contains one durable full-scan cursor and bounded per-Slot health.
	IntegrityAudit IntegrityAuditState
}

// Clone returns a deep copy safe for mutation by a caller.
func (s State) Clone() State {
	out := s
	if s.SourceFence != nil {
		sourceFence := *s.SourceFence
		out.SourceFence = &sourceFence
	}
	out.SlotFrontiers = make([]SlotFrontier, len(s.SlotFrontiers))
	for index := range s.SlotFrontiers {
		out.SlotFrontiers[index] = CloneSlotFrontier(s.SlotFrontiers[index])
	}
	if s.CatalogHead != nil {
		head := *s.CatalogHead
		out.CatalogHead = &head
	}
	out.ErasureStreams = make([]ErasureStreamState, len(s.ErasureStreams))
	for index, stream := range s.ErasureStreams {
		out.ErasureStreams[index] = stream
		if stream.Head != nil {
			head := *stream.Head
			out.ErasureStreams[index].Head = &head
		}
		if stream.Pending != nil {
			pending := *stream.Pending
			out.ErasureStreams[index].Pending = &pending
		}
		if stream.LastCommitted != nil {
			committed := *stream.LastCommitted
			out.ErasureStreams[index].LastCommitted = &committed
		}
	}
	out.GenerationGCCursors = append([]GenerationGCCursor(nil), s.GenerationGCCursors...)
	out.IntegrityAudit = CloneIntegrityAuditState(s.IntegrityAudit)
	return out
}

// RestoreStatus identifies the explicit recovery lifecycle state.
type RestoreStatus string

const (
	RestoreStatusPlanned    RestoreStatus = "planned"
	RestoreStatusInstalling RestoreStatus = "installing"
	RestoreStatusInstalled  RestoreStatus = "installed"
	RestoreStatusVerified   RestoreStatus = "verified"
	RestoreStatusActivating RestoreStatus = "activating"
	RestoreStatusActivated  RestoreStatus = "activated"
	RestoreStatusAbandoned  RestoreStatus = "abandoned"
)

// RestorePartitionStatus identifies one durable Slot restore phase.
type RestorePartitionStatus string

const (
	RestorePartitionPending    RestorePartitionStatus = "pending"
	RestorePartitionInstalling RestorePartitionStatus = "installing"
	RestorePartitionInstalled  RestorePartitionStatus = "installed"
	RestorePartitionConverging RestorePartitionStatus = "converging"
	RestorePartitionConverged  RestorePartitionStatus = "converged"
	RestorePartitionFailed     RestorePartitionStatus = "failed"
)

// RestorePartition records one idempotent logical-partition installation result.
type RestorePartition struct {
	// HashSlot identifies the restored logical partition.
	HashSlot uint16
	// Status is the durable Leader-import and replica-convergence phase.
	Status RestorePartitionStatus
	// TargetSlotID and leader fencing bind one attempt to the current target authority.
	TargetSlotID   uint32
	LeaderNodeID   uint64
	LeaderTerm     uint64
	ConfigEpoch    uint64
	InstallAttempt uint64
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
	// ContentSHA256 and MessageMerkleSHA256 are single-pass import evidence.
	ContentSHA256       string
	MessageMerkleSHA256 string
	// ChannelBoundaryCount counts exact restored Channel sequence boundaries.
	ChannelBoundaryCount uint64
	// DownloadedBytes and ReplicatedBytes drive throughput and ETA projections.
	DownloadedBytes uint64
	ReplicatedBytes uint64
	// ReplicaCount and ConvergedReplicas expose the target convergence gate.
	ReplicaCount      uint32
	ConvergedReplicas uint32
	// FailureCategory is a bounded operator-facing error class.
	FailureCategory string
	// UpdatedAtUnixMillis is the UTC progress timestamp.
	UpdatedAtUnixMillis int64
	// StartedAtUnixMillis and InstalledAtUnixMillis are durable phase timestamps.
	StartedAtUnixMillis   int64
	InstalledAtUnixMillis int64
}

// RestorePartitionAssignment fences one import attempt to the current target
// Slot Leader and replica configuration.
type RestorePartitionAssignment struct {
	// HashSlot identifies the logical partition.
	HashSlot uint16
	// TargetSlotID identifies the current physical Slot.
	TargetSlotID uint32
	// LeaderNodeID and LeaderTerm identify the current Slot write authority.
	LeaderNodeID uint64
	LeaderTerm   uint64
	// ConfigEpoch fences the desired replica set.
	ConfigEpoch uint64
	// ReplicaCount is the exact desired replica convergence target.
	ReplicaCount uint32
}

// RestorePlan is the immutable selection plus mutable bounded recovery progress.
type RestorePlan struct {
	// ID identifies the immutable restore plan.
	ID string
	// CheckpointID and CheckpointSHA256 select exact signed source bytes.
	CheckpointID     string
	CheckpointSHA256 string
	// CatalogProof pins the exact checkpoint membership under the catalog head
	// observed at restore admission. It is mandatory for every restore plan.
	CatalogProof *backupartifact.CheckpointCatalogProof
	// CheckpointVersion and timestamps are authenticated immutable checkpoint identity.
	CheckpointVersion               uint16
	CheckpointCreatedAtUnixMillis   int64
	CheckpointEffectiveAtUnixMillis int64
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
	// ErasureLedgerVersion identifies the authenticated restore ledger snapshot schema.
	ErasureLedgerVersion uint32
	// ErasureEventCount is the total number of events selected by ErasureHeads.
	ErasureEventCount uint64
	// ErasureHeads authenticate the exact selected prefix of each Hash Slot stream.
	ErasureHeads []backupartifact.ErasureStreamHead
	// ErasureLedgerSHA256 authenticates that exact ledger prefix, including encrypted event objects.
	ErasureLedgerSHA256 string
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
	// StagingCleanupCompletedAtUnixMillis proves every target replica removed
	// the plan-bound plaintext staging before ordinary service may start.
	StagingCleanupCompletedAtUnixMillis int64
	// Activation contains the immutable normal or break-glass audit evidence.
	Activation *backupartifact.RestoreActivationEvidence
	// Partitions contains exactly one progress record per hash slot.
	Partitions []RestorePartition
}
