package state

import backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"

// MaxBackupRestorePoints bounds restore-point references stored in Controller Raft.
const MaxBackupRestorePoints = 4096

// BackupRestorePointKind identifies how a restore point was materialized.
type BackupRestorePointKind string

const (
	// BackupRestorePointKindIncremental records a restore point built from the previous chain boundary.
	BackupRestorePointKindIncremental BackupRestorePointKind = "incremental"
	// BackupRestorePointKindSyntheticFull is reserved for a qualified independent
	// full logical point assembled without rewriting every object.
	BackupRestorePointKindSyntheticFull BackupRestorePointKind = "synthetic_full"
	// BackupRestorePointKindMaterializedFull records a full point whose reachable objects were rewritten.
	BackupRestorePointKindMaterializedFull BackupRestorePointKind = "materialized_full"
)

// BackupJobStatus identifies one durable backup coordination phase.
type BackupJobStatus string

const (
	// BackupJobStatusPreparing means the Controller leader is capturing a topology cut.
	BackupJobStatusPreparing BackupJobStatus = "preparing"
	// BackupJobStatusCapturing means partition workers may report immutable manifests.
	BackupJobStatusCapturing BackupJobStatus = "capturing"
	// BackupJobStatusPublishing means repository publication is in progress.
	BackupJobStatusPublishing BackupJobStatus = "publishing"
	// BackupJobStatusDegraded means publication may be retried after an infrastructure failure.
	BackupJobStatusDegraded BackupJobStatus = "degraded"
	// BackupJobStatusFailed means the active job reached a terminal failure.
	BackupJobStatusFailed BackupJobStatus = "failed"
)

// BackupPartitionReport is the bounded completion summary for one logical hash slot.
type BackupPartitionReport struct {
	// JobID fences the report to one active backup job.
	JobID string `json:"job_id"`
	// BackupEpoch fences stale reports from an older job incarnation.
	BackupEpoch uint64 `json:"backup_epoch"`
	// HashSlot identifies the completed logical partition.
	HashSlot uint16 `json:"hash_slot"`
	// RaftIndex is the committed Slot boundary represented by the partition manifest.
	RaftIndex uint64 `json:"raft_index"`
	// CommittedAtUnixMillis is the UTC commit watermark represented by the report.
	CommittedAtUnixMillis int64 `json:"committed_at_unix_millis"`
	// ManifestKey points to the immutable partition manifest in backup repositories.
	ManifestKey string `json:"manifest_key"`
	// ManifestSHA256 authenticates the partition manifest bytes.
	ManifestSHA256 string `json:"manifest_sha256"`
	// ObjectCount is the number of encrypted objects referenced by the partition manifest.
	ObjectCount uint64 `json:"object_count"`
	// CiphertextBytes is the total encrypted payload size for the partition.
	CiphertextBytes uint64 `json:"ciphertext_bytes"`
}

// BackupJob is one bounded cluster-coordinated backup attempt.
type BackupJob struct {
	// ID uniquely identifies the job.
	ID string `json:"id"`
	// Epoch monotonically fences backup attempts in this cluster generation.
	Epoch uint64 `json:"epoch"`
	// Kind identifies the restore-point creation strategy.
	Kind BackupRestorePointKind `json:"kind"`
	// Status is the current job lifecycle state.
	Status BackupJobStatus `json:"status"`
	// HashSlotCount is the required logical partition count.
	HashSlotCount uint16 `json:"hash_slot_count"`
	// ConfigFingerprint proves non-secret backup configuration agreement.
	ConfigFingerprint string `json:"config_fingerprint"`
	// RestorePointID is allocated before capture for idempotent publication retries.
	RestorePointID string `json:"restore_point_id"`
	// BaseRestorePointID identifies the previous complete point, when required.
	BaseRestorePointID string `json:"base_restore_point_id,omitempty"`
	// StartedAtUnixMillis is the UTC job creation timestamp.
	StartedAtUnixMillis int64 `json:"started_at_unix_millis"`
	// UpdatedAtUnixMillis is the UTC timestamp of the latest state transition.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
	// Partitions contains sorted logical completion summaries, never backup payloads.
	Partitions []BackupPartitionReport `json:"partitions"`
	// FailureCategory is a bounded operator-facing failure class.
	FailureCategory string `json:"failure_category,omitempty"`
}

// BackupRestorePoint is one bounded reference to a published signed manifest.
type BackupRestorePoint struct {
	// ID is the globally unique restore-point identity.
	ID string `json:"id"`
	// JobID identifies the job that produced the restore point.
	JobID string `json:"job_id"`
	// BackupEpoch is the source job epoch.
	BackupEpoch uint64 `json:"backup_epoch"`
	// Kind identifies the restore-point creation strategy.
	Kind BackupRestorePointKind `json:"kind"`
	// EffectiveAtUnixMillis is the oldest included logical partition watermark.
	EffectiveAtUnixMillis int64 `json:"effective_at_unix_millis"`
	// CreatedAtUnixMillis is the UTC publication timestamp.
	CreatedAtUnixMillis int64 `json:"created_at_unix_millis"`
	// ManifestSHA256 authenticates the signed top-level manifest.
	ManifestSHA256 string `json:"manifest_sha256"`
	// PrimaryVerified reports verification in the primary repository.
	PrimaryVerified bool `json:"primary_verified"`
	// SecondaryVerified reports verification in the secondary repository.
	SecondaryVerified bool `json:"secondary_verified"`
	// Held prevents retention collection while true.
	Held bool `json:"held,omitempty"`
	// LastVerification is later audit evidence, separate from publication verification.
	LastVerification *BackupVerificationEvidence `json:"last_verification,omitempty"`
}

// BackupVerificationTaskStatus identifies one durable manual verification lifecycle state.
type BackupVerificationTaskStatus string

const (
	BackupVerificationTaskStatusPending   BackupVerificationTaskStatus = "pending"
	BackupVerificationTaskStatusRunning   BackupVerificationTaskStatus = "running"
	BackupVerificationTaskStatusSucceeded BackupVerificationTaskStatus = "succeeded"
	BackupVerificationTaskStatusFailed    BackupVerificationTaskStatus = "failed"
)

// BackupVerificationEvidence is bounded evidence from one later repository audit.
type BackupVerificationEvidence struct {
	// Status identifies the current or terminal audit phase.
	Status BackupVerificationTaskStatus `json:"status"`
	// StartedAtUnixMillis and CompletedAtUnixMillis are UTC lifecycle timestamps.
	StartedAtUnixMillis   int64 `json:"started_at_unix_millis"`
	CompletedAtUnixMillis int64 `json:"completed_at_unix_millis,omitempty"`
	// PrimaryVerified and SecondaryVerified report independent repository results.
	PrimaryVerified   bool `json:"primary_verified"`
	SecondaryVerified bool `json:"secondary_verified"`
	// ManifestSHA256 authenticates the audited top-level bytes on success.
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	// FailureCategory is a bounded non-sensitive failure class.
	FailureCategory string `json:"failure_category,omitempty"`
}

// BackupVerificationTask is the one cluster-wide durable manual audit task.
type BackupVerificationTask struct {
	// ID uniquely identifies this resumable task.
	ID string `json:"id"`
	// RestorePointID identifies the exact audited recovery point.
	RestorePointID string `json:"restore_point_id"`
	// BackupVerificationEvidence contains the current bounded task result.
	BackupVerificationEvidence
}

// BackupErasureLedgerReference is the only bounded pending permanent-erasure
// ledger record stored in Controller state.
type BackupErasureLedgerReference struct {
	// HashSlot identifies the independently sequenced erasure stream.
	HashSlot uint16 `json:"hash_slot"`
	// Sequence is the next contiguous position within HashSlot.
	Sequence uint64 `json:"sequence"`
	// EventID is the deterministic permanent-erasure event identity.
	EventID string `json:"event_id"`
	// RecordKey points to the immutable signed event record.
	RecordKey string `json:"record_key"`
	// RecordSHA256 authenticates the exact signed record bytes.
	RecordSHA256 string `json:"record_sha256"`
}

// BackupErasureStreamState is the bounded coordination state for one Hash Slot
// permanent-erasure stream.
type BackupErasureStreamState struct {
	// HashSlot identifies the independently sequenced stream.
	HashSlot uint16 `json:"hash_slot"`
	// Head authenticates the latest dual-repository commit.
	Head *backupartifact.ErasureStreamHead `json:"head,omitempty"`
	// Pending is the one reserved record whose commit marker may need repair.
	Pending *BackupErasureLedgerReference `json:"pending,omitempty"`
	// LastCommitted preserves constant-space retry recognition.
	LastCommitted *BackupErasureLedgerReference `json:"last_committed,omitempty"`
}

// BackupSegmentReference binds a Controller frontier to one immutable dual-repository commit.
type BackupSegmentReference struct {
	// SegmentID is the canonical segment-header SHA-256.
	SegmentID string `json:"segment_id"`
	// CommitKey locates the immutable signed commit record.
	CommitKey string `json:"commit_key"`
	// CommitSHA256 authenticates the exact signed commit bytes.
	CommitSHA256 string `json:"commit_sha256"`
	// PlaintextBytes is the authenticated decompressed segment size.
	PlaintextBytes int64 `json:"plaintext_bytes"`
}

// BackupCatalogPageReference is the bounded Controller-visible head of the
// immutable checkpoint catalog.
type BackupCatalogPageReference struct {
	// Sequence is the monotonically increasing catalog page position.
	Sequence uint64 `json:"sequence"`
	// Key locates the immutable signed catalog page.
	Key string `json:"key"`
	// SHA256 authenticates the exact signed page bytes.
	SHA256 string `json:"sha256"`
	// Bytes is the exact signed page size.
	Bytes int64 `json:"bytes"`
	// LatestCheckpointID identifies the newest checkpoint on this page.
	LatestCheckpointID string `json:"latest_checkpoint_id"`
}

// BackupStreamFrontier is the compact durable head of one continuous Slot stream.
type BackupStreamFrontier struct {
	// Sequence is the latest committed segment sequence in the current Generation.
	Sequence uint64 `json:"sequence"`
	// Head authenticates the latest segment; nil means the stream has emitted no data.
	Head *BackupSegmentReference `json:"head,omitempty"`
	// CursorHead authenticates the latest cursor-only message sidecar.
	CursorHead *BackupSegmentReference `json:"cursor_head,omitempty"`
	// BaselineCursorHead authenticates the complete materialized Channel index.
	BaselineCursorHead *BackupSegmentReference `json:"baseline_cursor_head,omitempty"`
	// SourceCursor is the bounded opaque reconciliation cursor.
	SourceCursor string `json:"source_cursor,omitempty"`
	// SourceHighWatermark is the greatest fully reconciled committed source position.
	SourceHighWatermark uint64 `json:"source_high_watermark"`
	// WatermarkAtUnixMillis is the UTC source time represented by the high watermark.
	WatermarkAtUnixMillis int64 `json:"watermark_at_unix_millis"`
}

// BackupSlotCaptureLease fences one Hash Slot capture worker to one Raft authority.
type BackupSlotCaptureLease struct {
	// SlotID identifies the logical Slot Raft Group that owns the Hash Slot.
	SlotID uint32 `json:"slot_id"`
	// LeaderTerm is the Slot Raft term observed with HolderNodeID.
	LeaderTerm uint64 `json:"leader_term"`
	// ConfigEpoch is the control-plane configuration epoch for SlotID.
	ConfigEpoch uint64 `json:"config_epoch"`
	// HolderNodeID is the only node allowed to advance this frontier.
	HolderNodeID uint64 `json:"holder_node_id"`
	// Generation binds the lease to one immutable Slot segment graph.
	Generation string `json:"generation"`
	// Sequence increases on every authority takeover.
	Sequence uint64 `json:"sequence"`
	// AcquiredAtUnixMillis is the UTC time of the latest durable takeover.
	AcquiredAtUnixMillis int64 `json:"acquired_at_unix_millis"`
}

// BackupPartitionReference authenticates one immutable materialized Slot manifest.
type BackupPartitionReference struct {
	HashSlot        uint16                           `json:"hash_slot"`
	Key             string                           `json:"key"`
	SHA256          string                           `json:"sha256"`
	Bytes           int64                            `json:"bytes"`
	ObjectCount     uint64                           `json:"object_count"`
	CiphertextBytes uint64                           `json:"ciphertext_bytes"`
	Evidence        backupartifact.PartitionEvidence `json:"evidence"`
}

// BackupSlotBaselineReference authenticates the materialized root and cursor index.
type BackupSlotBaselineReference struct {
	Partition BackupPartitionReference `json:"partition"`
}

// BackupSlotRebase preserves a pending replacement while the current generation
// remains the published restore source.
type BackupSlotRebase struct {
	TargetGeneration    string `json:"target_generation"`
	Epoch               uint64 `json:"epoch"`
	Reason              string `json:"reason"`
	StartedAtUnixMillis int64  `json:"started_at_unix_millis"`
}

// BackupSlotFrontier atomically binds metadata and message stream heads for one Hash Slot.
type BackupSlotFrontier struct {
	// Revision fences compare-and-swap updates to this Slot record.
	Revision uint64 `json:"revision"`
	// HashSlot identifies the logical cluster partition.
	HashSlot uint16 `json:"hash_slot"`
	// Generation identifies the independently replaceable immutable segment graph.
	Generation string `json:"generation"`
	// Lease fences frontier commits to one exact current Slot authority.
	Lease BackupSlotCaptureLease `json:"lease"`
	// SourceSlotID identifies the physical Slot index space used by the
	// metadata source cursor. It may differ from Lease only while rebasing.
	SourceSlotID uint32 `json:"source_slot_id"`
	// SourcePinStartedAtUnixMillis is the durable age origin of the retained
	// metadata log floor and survives lease takeover.
	SourcePinStartedAtUnixMillis int64 `json:"source_pin_started_at_unix_millis"`
	// Baseline is the optional materialized root of Generation.
	Baseline *BackupSlotBaselineReference `json:"baseline,omitempty"`
	// Rebase records a retryable pending generation replacement.
	Rebase *BackupSlotRebase `json:"rebase,omitempty"`
	// Metadata and Messages are separate streams advanced through this one record.
	Metadata BackupStreamFrontier `json:"metadata"`
	Messages BackupStreamFrontier `json:"messages"`
	// WatermarkAtUnixMillis is the older fully reconciled stream time.
	WatermarkAtUnixMillis int64 `json:"watermark_at_unix_millis"`
	// UpdatedAtUnixMillis is the UTC time of the latest frontier commit.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
}

// BackupCoordinationState stores only bounded backup coordination metadata in Controller Raft.
type BackupCoordinationState struct {
	// LastEpoch is the latest allocated backup epoch.
	LastEpoch uint64 `json:"last_epoch"`
	// Active contains the only active job, when present.
	Active *BackupJob `json:"active,omitempty"`
	// Verification contains the latest cluster-wide manual verification task.
	Verification *BackupVerificationTask `json:"verification,omitempty"`
	// RestorePoints contains bounded published restore-point references.
	RestorePoints []BackupRestorePoint `json:"restore_points"`
	// PendingGarbage contains expired restore-point graphs awaiting reference-safe repository collection.
	PendingGarbage []BackupRestorePoint `json:"pending_garbage,omitempty"`
	// SlotFrontiers contains at most one compact sorted record per configured Hash Slot.
	SlotFrontiers []BackupSlotFrontier `json:"slot_frontiers,omitempty"`
	// CatalogHead is the only Controller-resident pointer into immutable checkpoint history.
	CatalogHead *BackupCatalogPageReference `json:"catalog_head,omitempty"`
	// ErasureStreams contains at most one sorted bounded state per Hash Slot.
	ErasureStreams []BackupErasureStreamState `json:"erasure_streams,omitempty"`
}

// Clone returns a deep copy safe for normalization and mutation.
func (s BackupCoordinationState) Clone() BackupCoordinationState {
	out := s
	if s.Active != nil {
		job := *s.Active
		job.Partitions = cloneSlice(s.Active.Partitions)
		out.Active = &job
	}
	if s.Verification != nil {
		verification := *s.Verification
		out.Verification = &verification
	}
	out.RestorePoints = cloneBackupRestorePoints(s.RestorePoints)
	out.PendingGarbage = cloneBackupRestorePoints(s.PendingGarbage)
	out.SlotFrontiers = cloneBackupSlotFrontiers(s.SlotFrontiers)
	if s.CatalogHead != nil {
		head := *s.CatalogHead
		out.CatalogHead = &head
	}
	out.ErasureStreams = cloneBackupErasureStreams(s.ErasureStreams)
	return out
}

func cloneBackupErasureStreams(streams []BackupErasureStreamState) []BackupErasureStreamState {
	out := cloneSlice(streams)
	for index, stream := range streams {
		if stream.Head != nil {
			head := *stream.Head
			out[index].Head = &head
		}
		if stream.Pending != nil {
			pending := *stream.Pending
			out[index].Pending = &pending
		}
		if stream.LastCommitted != nil {
			committed := *stream.LastCommitted
			out[index].LastCommitted = &committed
		}
	}
	return out
}

func cloneBackupSlotFrontiers(frontiers []BackupSlotFrontier) []BackupSlotFrontier {
	out := cloneSlice(frontiers)
	for index := range out {
		if frontiers[index].Baseline != nil {
			baseline := *frontiers[index].Baseline
			out[index].Baseline = &baseline
		}
		if frontiers[index].Rebase != nil {
			rebase := *frontiers[index].Rebase
			out[index].Rebase = &rebase
		}
		if frontiers[index].Metadata.Head != nil {
			head := *frontiers[index].Metadata.Head
			out[index].Metadata.Head = &head
		}
		if frontiers[index].Metadata.CursorHead != nil {
			head := *frontiers[index].Metadata.CursorHead
			out[index].Metadata.CursorHead = &head
		}
		if frontiers[index].Metadata.BaselineCursorHead != nil {
			head := *frontiers[index].Metadata.BaselineCursorHead
			out[index].Metadata.BaselineCursorHead = &head
		}
		if frontiers[index].Messages.Head != nil {
			head := *frontiers[index].Messages.Head
			out[index].Messages.Head = &head
		}
		if frontiers[index].Messages.CursorHead != nil {
			head := *frontiers[index].Messages.CursorHead
			out[index].Messages.CursorHead = &head
		}
		if frontiers[index].Messages.BaselineCursorHead != nil {
			head := *frontiers[index].Messages.BaselineCursorHead
			out[index].Messages.BaselineCursorHead = &head
		}
	}
	return out
}

func cloneBackupRestorePoints(points []BackupRestorePoint) []BackupRestorePoint {
	out := cloneSlice(points)
	for index := range out {
		if points[index].LastVerification != nil {
			evidence := *points[index].LastVerification
			out[index].LastVerification = &evidence
		}
	}
	return out
}
