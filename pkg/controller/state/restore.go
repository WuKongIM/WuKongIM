package state

import backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"

// RestoreStatus identifies one explicit fresh-cluster recovery phase.
type RestoreStatus string

const (
	RestoreStatusPlanned    RestoreStatus = "planned"
	RestoreStatusInstalling RestoreStatus = "installing"
	RestoreStatusInstalled  RestoreStatus = "installed"
	RestoreStatusVerified   RestoreStatus = "verified"
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

// RestorePartition stores bounded logical-partition recovery progress.
type RestorePartition struct {
	// HashSlot identifies the logical partition being restored.
	HashSlot uint16 `json:"hash_slot"`
	// Status is the durable Leader-import and replica-convergence phase.
	Status RestorePartitionStatus `json:"status,omitempty"`
	// TargetSlotID and leader fencing bind the current idempotent attempt.
	TargetSlotID   uint32 `json:"target_slot_id,omitempty"`
	LeaderNodeID   uint64 `json:"leader_node_id,omitempty"`
	LeaderTerm     uint64 `json:"leader_term,omitempty"`
	ConfigEpoch    uint64 `json:"config_epoch,omitempty"`
	InstallAttempt uint64 `json:"install_attempt,omitempty"`
	// EvidenceVersion distinguishes explicit empty evidence from a legacy missing report.
	EvidenceVersion uint32 `json:"evidence_version"`
	// Installed reports that this partition was durably imported.
	Installed bool `json:"installed"`
	// Verified reports that post-import semantic verification succeeded.
	Verified bool `json:"verified"`
	// PlainBytes counts installed plaintext bytes for bounded progress reporting.
	PlainBytes uint64 `json:"plain_bytes"`
	// MetadataRecordCount counts installed semantic metadata records.
	MetadataRecordCount uint64 `json:"metadata_record_count"`
	// MessageCount counts installed committed messages for bounded progress reporting.
	MessageCount uint64 `json:"message_count"`
	// MaxMessageID is the restored message-ID allocator fence.
	MaxMessageID uint64 `json:"max_message_id"`
	// MetadataSHA256 authenticates the canonical restored metadata projection.
	MetadataSHA256 string `json:"metadata_sha256,omitempty"`
	// ContentSHA256 and MessageMerkleSHA256 are single-pass import evidence.
	ContentSHA256       string `json:"content_sha256,omitempty"`
	MessageMerkleSHA256 string `json:"message_merkle_sha256,omitempty"`
	// ChannelBoundaryCount counts exact restored Channel sequence boundaries.
	ChannelBoundaryCount uint64 `json:"channel_boundary_count,omitempty"`
	// DownloadedBytes and ReplicatedBytes drive throughput and ETA projections.
	DownloadedBytes uint64 `json:"downloaded_bytes,omitempty"`
	ReplicatedBytes uint64 `json:"replicated_bytes,omitempty"`
	// ReplicaCount and ConvergedReplicas expose the target convergence gate.
	ReplicaCount      uint32 `json:"replica_count,omitempty"`
	ConvergedReplicas uint32 `json:"converged_replicas,omitempty"`
	// FailureCategory is the bounded operator-facing failure class.
	FailureCategory string `json:"failure_category,omitempty"`
	// UpdatedAtUnixMillis is the UTC time of the latest partition progress update.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis,omitempty"`
	// StartedAtUnixMillis and InstalledAtUnixMillis are durable phase timestamps.
	StartedAtUnixMillis   int64 `json:"started_at_unix_millis,omitempty"`
	InstalledAtUnixMillis int64 `json:"installed_at_unix_millis,omitempty"`
}

// RestorePlan stores one immutable recovery selection and bounded progress.
type RestorePlan struct {
	// ID identifies the immutable recovery plan.
	ID string `json:"id"`
	// RestorePointID identifies the selected signed recovery point.
	RestorePointID string `json:"restore_point_id"`
	// ManifestSHA256 authenticates the selected top-level manifest bytes.
	ManifestSHA256 string `json:"manifest_sha256"`
	// CatalogProof pins the checkpoint's original membership under an immutable catalog head.
	CatalogProof *backupartifact.CheckpointCatalogProof `json:"catalog_proof,omitempty"`
	// CheckpointVersion and timestamps are authenticated immutable checkpoint identity.
	CheckpointVersion               uint16 `json:"checkpoint_version,omitempty"`
	CheckpointCreatedAtUnixMillis   int64  `json:"checkpoint_created_at_unix_millis,omitempty"`
	CheckpointEffectiveAtUnixMillis int64  `json:"checkpoint_effective_at_unix_millis,omitempty"`
	// Repository selects the primary or secondary source copy.
	Repository string `json:"repository"`
	// SourceClusterID identifies the backed-up cluster.
	SourceClusterID string `json:"source_cluster_id"`
	// SourceGeneration fences the backed-up cluster incarnation.
	SourceGeneration string `json:"source_generation"`
	// TargetClusterID identifies the fresh successor cluster.
	TargetClusterID string `json:"target_cluster_id"`
	// TargetGeneration fences the fresh successor incarnation.
	TargetGeneration string `json:"target_generation"`
	// HashSlotCount is the immutable logical partition count shared by source and target.
	HashSlotCount uint16 `json:"hash_slot_count"`
	// ErasureLedgerVersion identifies the authenticated restore ledger snapshot schema.
	ErasureLedgerVersion uint32 `json:"erasure_ledger_version"`
	// ErasureEventCount is the total number of events selected by ErasureHeads.
	ErasureEventCount uint64 `json:"erasure_event_count"`
	// ErasureHeads authenticate the exact selected prefix of each Hash Slot stream.
	ErasureHeads []backupartifact.ErasureStreamHead `json:"erasure_heads,omitempty"`
	// ErasureLedgerSHA256 authenticates that exact ledger prefix.
	ErasureLedgerSHA256 string `json:"erasure_ledger_sha256"`
	// InvalidateTokens records the explicit restore-time credential transform.
	InvalidateTokens bool `json:"invalidate_tokens,omitempty"`
	// EstimatedPlainBytes preserves the authenticated plaintext size estimate; nil is unknown.
	EstimatedPlainBytes *uint64 `json:"estimated_plain_bytes,omitempty"`
	// EstimatedCipherBytes preserves the authenticated download size estimate; nil is unknown.
	EstimatedCipherBytes *uint64 `json:"estimated_cipher_bytes,omitempty"`
	// Status is the explicit recovery lifecycle phase.
	Status RestoreStatus `json:"status"`
	// CreatedAtUnixMillis is the UTC plan creation time.
	CreatedAtUnixMillis int64 `json:"created_at_unix_millis"`
	// UpdatedAtUnixMillis is the UTC time of the latest plan mutation.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
	// VerifiedAtUnixMillis is the UTC time of successful semantic verification.
	VerifiedAtUnixMillis int64 `json:"verified_at_unix_millis,omitempty"`
	// ActivatedAtUnixMillis is the UTC time of explicit successor activation.
	ActivatedAtUnixMillis int64 `json:"activated_at_unix_millis,omitempty"`
	// ActivationFenceDigest authenticates reviewed old-cluster fencing evidence.
	ActivationFenceDigest string `json:"activation_fence_digest,omitempty"`
	// Partitions contains one bounded progress record per hash slot.
	Partitions []RestorePartition `json:"partitions"`
}

// RestoreCoordinationState contains the only explicit recovery plan.
type RestoreCoordinationState struct {
	// Plan is the only active or completed explicit recovery plan.
	Plan *RestorePlan `json:"plan,omitempty"`
}

// Clone returns a detached recovery state.
func (s RestoreCoordinationState) Clone() RestoreCoordinationState {
	out := s
	if s.Plan != nil {
		plan := *s.Plan
		if s.Plan.CatalogProof != nil {
			proof := *s.Plan.CatalogProof
			plan.CatalogProof = &proof
		}
		plan.ErasureHeads = cloneSlice(s.Plan.ErasureHeads)
		if s.Plan.EstimatedPlainBytes != nil {
			value := *s.Plan.EstimatedPlainBytes
			plan.EstimatedPlainBytes = &value
		}
		if s.Plan.EstimatedCipherBytes != nil {
			value := *s.Plan.EstimatedCipherBytes
			plan.EstimatedCipherBytes = &value
		}
		plan.Partitions = cloneSlice(s.Plan.Partitions)
		out.Plan = &plan
	}
	return out
}
