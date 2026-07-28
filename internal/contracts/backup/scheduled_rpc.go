package backup

import backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"

// ChannelFence is one Channel generation selected by a stable Hash Slot cut.
type ChannelFence struct {
	ChannelID           string `json:"channel_id"`
	ChannelType         uint8  `json:"channel_type"`
	LeaderNodeID        uint64 `json:"leader_node_id"`
	ChannelEpoch        uint64 `json:"channel_epoch"`
	LeaderEpoch         uint64 `json:"leader_epoch"`
	MinISR              int64  `json:"min_isr"`
	RetentionThroughSeq uint64 `json:"retention_through_seq"`
}

// MessageShard is one bounded group of Channels sharing a source leader.
type MessageShard struct {
	ID       string         `json:"id"`
	NodeID   uint64         `json:"node_id"`
	Channels []ChannelFence `json:"channels"`
}

// SlotExportCommand asks the current physical Slot leader to export one
// logical Hash Slot directly into the configured repository.
type SlotExportCommand struct {
	Plan        Plan   `json:"plan"`
	BackupID    string `json:"backup_id"`
	HashSlot    uint16 `json:"hash_slot"`
	Attempt     uint32 `json:"attempt"`
	OwnerNodeID uint64 `json:"owner_node_id"`
	OwnerTerm   uint64 `json:"owner_term"`
	// CoordinatorNodeID and CoordinatorTerm fence work to the exact Controller
	// leadership generation that admitted it.
	CoordinatorNodeID uint64 `json:"coordinator_node_id"`
	CoordinatorTerm   uint64 `json:"coordinator_term"`
}

// SlotExportReceipt is the bounded result returned to the Controller leader.
type SlotExportReceipt struct {
	ManifestKey    string `json:"manifest_key"`
	ManifestSHA256 string `json:"manifest_sha256"`
	LogicalBytes   uint64 `json:"logical_bytes"`
	StoredBytes    uint64 `json:"stored_bytes"`
	Records        uint64 `json:"records"`
	MaxMessageID   uint64 `json:"max_message_id"`
}

// MessageExportCommand asks a Channel leader to store one portable message
// stream directly, returning references rather than message payload bytes.
type MessageExportCommand struct {
	Store             StoreConfig  `json:"store"`
	BackupID          string       `json:"backup_id"`
	HashSlot          uint16       `json:"hash_slot"`
	ArtifactPrefix    string       `json:"artifact_prefix"`
	Shard             MessageShard `json:"shard"`
	FirstSequence     uint32       `json:"first_sequence"`
	StreamNumber      uint32       `json:"stream_number"`
	RateBytesPerSec   uint64       `json:"rate_bytes_per_sec"`
	CoordinatorNodeID uint64       `json:"coordinator_node_id"`
	CoordinatorTerm   uint64       `json:"coordinator_term"`
}

// MessageExportReceipt is safe to carry over bounded node RPC.
type MessageExportReceipt struct {
	ManifestKey    string `json:"manifest_key"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ChunkCount     uint32 `json:"chunk_count"`
	LogicalBytes   uint64 `json:"logical_bytes"`
	StoredBytes    uint64 `json:"stored_bytes"`
	Records        uint64 `json:"records"`
	MaxMessageID   uint64 `json:"max_message_id"`
}

// RepositoryProbeCommand asks one active node to observe a coordinator marker
// and publish a node-specific receipt through the same repository.
type RepositoryProbeCommand struct {
	Store          StoreConfig `json:"store"`
	MarkerKey      string      `json:"marker_key"`
	MarkerSHA256   string      `json:"marker_sha256"`
	ReceiptKey     string      `json:"receipt_key"`
	ReceiptContent string      `json:"receipt_content"`
}

// RestoreNodeAction is one idempotent node-local maintenance restore step.
type RestoreNodeAction string

const (
	RestoreNodeActionPreflight RestoreNodeAction = "preflight"
	RestoreNodeActionPrepare   RestoreNodeAction = "prepare"
	RestoreNodeActionStage     RestoreNodeAction = "stage"
	RestoreNodeActionVerify    RestoreNodeAction = "verify"
	RestoreNodeActionSwitch    RestoreNodeAction = "switch"
	RestoreNodeActionActivate  RestoreNodeAction = "activate"
	RestoreNodeActionHealth    RestoreNodeAction = "health"
	RestoreNodeActionRollback  RestoreNodeAction = "rollback"
	RestoreNodeActionResume    RestoreNodeAction = "resume"
	RestoreNodeActionCleanup   RestoreNodeAction = "cleanup"
)

// RestoreNodeCommand asks one current Slot replica to operate on local staged
// files. Repository payloads never cross the cluster RPC transport.
type RestoreNodeCommand struct {
	Action   RestoreNodeAction `json:"action"`
	Store    StoreConfig       `json:"store"`
	JobID    string            `json:"job_id"`
	BackupID string            `json:"backup_id"`
	HashSlot uint16            `json:"hash_slot"`
	Attempt  uint32            `json:"attempt"`
	// SlotReference selects the exact immutable export attempt from the
	// published top-level archive manifest.
	SlotReference backupartifact.SlotReference `json:"slot_reference"`
	// ControllerRevision fences the command against an older restore mirror.
	ControllerRevision uint64 `json:"controller_revision"`
	// TargetActivation identifies the exact admitted restore activation.
	TargetActivation string `json:"target_activation"`
	// RequiredBytes is the conservative local free-space floor for preflight.
	RequiredBytes uint64 `json:"required_bytes,omitempty"`
	// MaxMessageID fences node-local allocators above restored durable IDs.
	MaxMessageID uint64 `json:"max_message_id,omitempty"`
	// CoordinatorNodeID and CoordinatorTerm reject commands from a previous
	// Controller leader even when the restore phase itself has not changed.
	CoordinatorNodeID uint64 `json:"coordinator_node_id"`
	CoordinatorTerm   uint64 `json:"coordinator_term"`
}

// RestoreNodeReceipt is bounded evidence from one replica-local restore step.
type RestoreNodeReceipt struct {
	LogicalBytes         uint64 `json:"logical_bytes,omitempty"`
	AvailableBytes       uint64 `json:"available_bytes,omitempty"`
	CurrentBusinessBytes uint64 `json:"current_business_bytes,omitempty"`
}
