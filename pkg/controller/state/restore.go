package state

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

// RestorePartition stores bounded logical-partition recovery progress.
type RestorePartition struct {
	// HashSlot identifies the logical partition being restored.
	HashSlot uint16 `json:"hash_slot"`
	// Installed reports that this partition was durably imported.
	Installed bool `json:"installed"`
	// Verified reports that post-import semantic verification succeeded.
	Verified bool `json:"verified"`
	// PlainBytes counts installed plaintext bytes for bounded progress reporting.
	PlainBytes uint64 `json:"plain_bytes"`
	// MessageCount counts installed committed messages for bounded progress reporting.
	MessageCount uint64 `json:"message_count"`
	// MetadataSHA256 authenticates the canonical restored metadata projection.
	MetadataSHA256 string `json:"metadata_sha256,omitempty"`
	// FailureCategory is the bounded operator-facing failure class.
	FailureCategory string `json:"failure_category,omitempty"`
	// UpdatedAtUnixMillis is the UTC time of the latest partition progress update.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis,omitempty"`
}

// RestorePlan stores one immutable recovery selection and bounded progress.
type RestorePlan struct {
	// ID identifies the immutable recovery plan.
	ID string `json:"id"`
	// RestorePointID identifies the selected signed recovery point.
	RestorePointID string `json:"restore_point_id"`
	// ManifestSHA256 authenticates the selected top-level manifest bytes.
	ManifestSHA256 string `json:"manifest_sha256"`
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
