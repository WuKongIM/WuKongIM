package migration

import "context"

// ChannelAuthority is decoded source ownership, never target cluster state.
type ChannelAuthority struct {
	Channel  ChannelIdentity `json:"channel"`
	Leader   uint64          `json:"leader"`
	Replicas []uint64        `json:"replicas"`
	Term     uint32          `json:"term"`
	// Version preserves the original configuration counter, including zero.
	// Authority requires the owner Slot and replica comparison, not positivity.
	Version uint64 `json:"version"`
}

// RecordDescription gives a logical comparison key and complete source value.
// Comparable excludes physical row placement and proven replica-local metadata.
// Narrow archival projections require explicit policy; original rows are always
// retained separately for lossless provenance.
type RecordDescription struct {
	// DerivedChannelCounters has no channel body or business policy. Preserve
	// its exact raw columns without creating a target channel from stale counts.
	ArchivedEmptyChannel   bool
	DerivedChannelCounters bool
	Key                    string
	Comparable             []byte
	Authority              *ChannelAuthority
	Plugin                 *PluginDescription
	// DeviceUIDIndex is the exact original cold-login lookup entry. Its row
	// order is the physical Device ID; nil means no proven device read rule.
	DeviceUIDIndex     []byte
	ConversationLookup *ConversationLookup
	// UserWithoutTimestamps excludes only the old manager's creation/update
	// fields. Selection may use it only under explicit archival policy.
	UserWithoutTimestamps []byte
}

// ConversationLookup separates the original exact state from display version.
// State excludes only CreatedAt/UpdatedAt; all other source fields stay bound.
type ConversationLookup struct {
	IndexKey     []byte
	State        []byte
	UpdatedAt    int64
	HasUpdatedAt bool
	Type         uint8
}

// PluginDescription separates obsolete registration text from methods and
// configuration that can affect business behavior without any user binding.
type PluginDescription struct {
	// CompatibilityProfile is set only by a use-case decorator after exact
	// source program, registration and configuration mapping checks.
	CompatibilityProfile string
	Methods              []string
	HasConfig            bool
	// Evidence compares node-local settings without exposing config or identity.
	Evidence PluginDiagnosticEvidence
}

// PluginDiagnosticEvidence is an inventory, never a verified runtime mapping.
// ConfigJSONSHA256 normalizes JSON object order and whitespace only; a different
// digest requires investigation, not automatic newest-node selection.
type PluginDiagnosticEvidence struct {
	NoSHA256             string `json:"no_sha256"`
	ConfigJSONSHA256     string `json:"config_json_sha256"`
	ConfigTemplateSHA256 string `json:"config_template_sha256"`
	MethodsSHA256        string `json:"methods_sha256"`
	FieldsSHA256         string `json:"fields_sha256"`
	MethodCount          int    `json:"method_count"`
	ConfigFieldCount     int    `json:"config_field_count"`
}

type RecordDecoder interface {
	IdentityDecoder
	Describe(Row, RecordIdentity) (RecordDescription, error)
}

// SelectedRecord preserves the exact authoritative source row. A pending
// conversation retains its recovery intent instead of inventing an old row ID.
type SelectedRecord struct {
	NodeID     uint64         `json:"node_id"`
	Row        Row            `json:"row"`
	Identity   RecordIdentity `json:"identity"`
	LogicalKey string         `json:"logical_key"`
}

type SourceSelection struct {
	Quarantine      *QuarantineReport       `json:"quarantine,omitempty"`
	HistoryPrefixes *HistoryPrefixSelection `json:"history_prefixes,omitempty"`
	PluginArtifacts *PluginArtifactsReport  `json:"plugin_artifacts,omitempty"`
	UserTimestamps  *UserTimestampArchive   `json:"archived_user_timestamps,omitempty"`
	EmptyChannels   *EmptyChannelProof      `json:"empty_channels,omitempty"`
	// ReplicaComparisonComplete is diagnostic evidence only. Compatibility
	// blockers still prevent a selection digest and a PREPARED checkpoint.
	ReplicaComparisonComplete bool `json:"replica_comparison_complete,omitempty"`
	// PluginBusinessRows counts captured physical registrations that still
	// require a verified executable/runtime mapping. It contains no config.
	PluginBusinessRows                 uint64 `json:"plugin_business_rows,omitempty"`
	PluginArtifactCompatibilityPending uint64 `json:"plugin_artifact_compatibility_pending,omitempty"`
	// AuthorityDigest binds a fresh transition proof to the complete capture.
	AuthorityDigest string             `json:"authority_digest,omitempty"`
	Messages        *MessagePolicy     `json:"messages,omitempty"`
	Metadata        *MetadataSelection `json:"metadata,omitempty"`
	Tables          map[string]uint64  `json:"selected_primary_rows_by_table"`
	Preserved       map[string]uint64  `json:"preserved_physical_rows_by_reason"`
	Digest          string             `json:"digest"`
	Excluded        *ExclusionReport   `json:"excluded_from_target,omitempty"`
}

// UserTimestampArchive binds per-node original values without choosing a new
// creation/update time or claiming that source timestamp replicas agree.
type UserTimestampArchive struct {
	Rows   uint64 `json:"physical_rows"`
	Fields uint64 `json:"timestamp_fields"`
	SHA256 string `json:"sha256"`
}

// Exclusions deliberately uses named, narrow rules instead of arbitrary table
// filters. An exclusion never authorizes skipping malformed or unknown data.
type Exclusions struct {
	// LegacyStreamStorage excludes only historical Stream/StreamMeta storage,
	// removed before SourceCommit. It retains Message rows and their sequences.
	LegacyStreamStorage bool `json:"legacy_stream_storage,omitempty"`
}

// ExclusionReport counts physical rows across source copies, not distinct
// business messages. SHA256 binds their node identities and complete raw rows.
type ExclusionReport struct {
	Policy       Exclusions        `json:"policy"`
	PhysicalRows map[string]uint64 `json:"physical_rows_by_table"`
	SHA256       string            `json:"sha256"`
}

// WalkSelectedSources streams only the source records selected by the workflow.
// Callers must require a successful SelectSources result before using its data.
func WalkSelectedSources(ctx context.Context, workspace Workspace, visit func(SelectedRecord) error) error {
	return walkSelected(ctx, workspace, []byte("selected/"), visit)
}
