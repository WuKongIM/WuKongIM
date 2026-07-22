package state

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
}

// BackupErasureLedgerReference is the only bounded pending permanent-erasure
// ledger record stored in Controller state.
type BackupErasureLedgerReference struct {
	// Sequence is the next contiguous ledger commit sequence.
	Sequence uint64 `json:"sequence"`
	// EventID is the deterministic permanent-erasure event identity.
	EventID string `json:"event_id"`
	// RecordKey points to the immutable signed event record.
	RecordKey string `json:"record_key"`
	// RecordSHA256 authenticates the exact signed record bytes.
	RecordSHA256 string `json:"record_sha256"`
}

// BackupCoordinationState stores only bounded backup coordination metadata in Controller Raft.
type BackupCoordinationState struct {
	// LastEpoch is the latest allocated backup epoch.
	LastEpoch uint64 `json:"last_epoch"`
	// Active contains the only active job, when present.
	Active *BackupJob `json:"active,omitempty"`
	// RestorePoints contains bounded published restore-point references.
	RestorePoints []BackupRestorePoint `json:"restore_points"`
	// PendingGarbage contains expired restore-point graphs awaiting reference-safe repository collection.
	PendingGarbage []BackupRestorePoint `json:"pending_garbage,omitempty"`
	// ErasureLedgerBoundary is the highest durably committed contiguous ledger sequence.
	ErasureLedgerBoundary uint64 `json:"erasure_ledger_boundary,omitempty"`
	// PendingErasureLedger contains at most one record awaiting commit-marker publication.
	PendingErasureLedger *BackupErasureLedgerReference `json:"pending_erasure_ledger,omitempty"`
	// LastCommittedErasureLedger preserves bounded idempotency for the latest accepted request.
	LastCommittedErasureLedger *BackupErasureLedgerReference `json:"last_committed_erasure_ledger,omitempty"`
}

// Clone returns a deep copy safe for normalization and mutation.
func (s BackupCoordinationState) Clone() BackupCoordinationState {
	out := s
	if s.Active != nil {
		job := *s.Active
		job.Partitions = cloneSlice(s.Active.Partitions)
		out.Active = &job
	}
	out.RestorePoints = cloneSlice(s.RestorePoints)
	out.PendingGarbage = cloneSlice(s.PendingGarbage)
	if s.PendingErasureLedger != nil {
		pending := *s.PendingErasureLedger
		out.PendingErasureLedger = &pending
	}
	if s.LastCommittedErasureLedger != nil {
		committed := *s.LastCommittedErasureLedger
		out.LastCommittedErasureLedger = &committed
	}
	return out
}
