package transfer

import "errors"

const (
	bundleFormat  = "wkdb-import-bundle"
	bundleVersion = 1
)

var (
	// ErrInvalidBundle reports that an import bundle cannot be loaded or trusted.
	ErrInvalidBundle = errors.New("invalid import bundle")
	// ErrValidation reports that a decoded import bundle failed semantic validation.
	ErrValidation = errors.New("import bundle validation failed")
)

// FileKind identifies the logical WKDB data set stored in a bundle file.
type FileKind string

const (
	// FileKindMetaUsers stores user metadata rows.
	FileKindMetaUsers FileKind = "meta.users"
	// FileKindMetaDevices stores user device metadata rows.
	FileKindMetaDevices FileKind = "meta.devices"
	// FileKindMetaChannels stores channel metadata rows.
	FileKindMetaChannels FileKind = "meta.channels"
	// FileKindMetaSubscribers stores channel subscriber rows.
	FileKindMetaSubscribers FileKind = "meta.subscribers"
	// FileKindMetaUserChannelMemberships stores user-to-channel membership rows.
	FileKindMetaUserChannelMemberships FileKind = "meta.user_channel_memberships"
	// FileKindMetaConversations stores conversation projection rows.
	FileKindMetaConversations FileKind = "meta.conversations"
	// FileKindMetaChannelLatest stores latest channel message projection rows.
	FileKindMetaChannelLatest FileKind = "meta.channel_latest"
	// FileKindMessageChannels stores message channel index rows.
	FileKindMessageChannels FileKind = "message.channels"
	// FileKindMessageMessages stores message log rows.
	FileKindMessageMessages FileKind = "message.messages"
)

// Manifest describes the files and invariants for a WKDB import bundle.
type Manifest struct {
	// Format is the bundle format identifier and must equal wkdb-import-bundle.
	Format string `json:"format"`
	// Version is the manifest schema version and must equal 1.
	Version int `json:"version"`
	// HashSlotCount is the number of hash slots used when the bundle was exported.
	HashSlotCount int `json:"hash_slot_count"`
	// Files lists the data files included in the bundle.
	Files []FileEntry `json:"files"`
}

// FileEntry describes one verified data file inside an import bundle.
type FileEntry struct {
	// Path is the clean relative path to the file inside the bundle root.
	Path string `json:"path"`
	// Kind identifies the logical WKDB data set contained by the file.
	Kind FileKind `json:"kind"`
	// Rows is the number of JSONL rows expected in the file.
	Rows int64 `json:"rows"`
	// SHA256 is the lowercase hex-encoded SHA256 checksum of the file bytes.
	SHA256 string `json:"sha256"`
}

// ImportOptions controls how a verified WKDB import bundle is applied.
type ImportOptions struct{}

// ImportStats summarizes the outcome of applying a WKDB import bundle.
type ImportStats struct{}

// UserRecord represents one imported user metadata row.
type UserRecord struct {
	// HashSlot is the hash slot that owns this user.
	HashSlot uint16 `json:"hash_slot"`
	// UID is the stable user identifier.
	UID string `json:"uid"`
	// Token is the user's authentication token.
	Token string `json:"token"`
	// DeviceFlag identifies the device platform for legacy-compatible metadata.
	DeviceFlag int64 `json:"device_flag"`
	// DeviceLevel describes the device trust or login level.
	DeviceLevel int64 `json:"device_level"`
}

// DeviceRecord represents one imported user device metadata row.
type DeviceRecord struct {
	// HashSlot is the hash slot that owns this user device.
	HashSlot uint16 `json:"hash_slot"`
	// UID is the stable user identifier.
	UID string `json:"uid"`
	// DeviceFlag identifies the device platform.
	DeviceFlag int64 `json:"device_flag"`
	// Token is the device authentication token.
	Token string `json:"token"`
	// DeviceLevel describes the device trust or login level.
	DeviceLevel int64 `json:"device_level"`
}

// ChannelRecord represents one imported channel metadata row.
type ChannelRecord struct {
	// HashSlot is the hash slot that owns this channel metadata.
	HashSlot uint16 `json:"hash_slot"`
	// ChannelID is the stable channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the numeric channel type.
	ChannelType int64 `json:"channel_type"`
	// Ban is the numeric 0/1 flag that marks whether the channel is banned.
	Ban int64 `json:"ban"`
	// Disband is the numeric 0/1 flag that marks whether the channel has been disbanded.
	Disband int64 `json:"disband"`
	// SendBan is the numeric 0/1 flag that marks whether sending is disabled for the channel.
	SendBan int64 `json:"send_ban"`
	// AllowStranger is the numeric 0/1 flag that marks whether non-subscribers may interact with the channel.
	AllowStranger int64 `json:"allow_stranger"`
	// Large is the numeric 0/1 flag that marks whether the channel should use large-channel behavior.
	Large int64 `json:"large"`
	// SubscriberMutationVersion is the exact subscriber mutation version.
	SubscriberMutationVersion Uint64 `json:"subscriber_mutation_version"`
}

// SubscriberRecord represents one imported channel subscriber row.
type SubscriberRecord struct {
	// HashSlot is the hash slot that owns this subscriber row.
	HashSlot uint16 `json:"hash_slot"`
	// ChannelID is the stable channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the numeric channel type.
	ChannelType int64 `json:"channel_type"`
	// UID is the stable subscriber user identifier.
	UID string `json:"uid"`
}

// UserChannelMembershipRecord represents one imported user-to-channel membership row.
type UserChannelMembershipRecord struct {
	// HashSlot is the hash slot that owns this membership row.
	HashSlot uint16 `json:"hash_slot"`
	// UID is the stable user identifier.
	UID string `json:"uid"`
	// ChannelID is the stable channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the numeric channel type.
	ChannelType int64 `json:"channel_type"`
	// JoinSeq is the exact message sequence visible when the user joined.
	JoinSeq Uint64 `json:"join_seq"`
	// UpdatedAtMS is the last update time in milliseconds.
	UpdatedAtMS int64 `json:"updated_at_ms"`
}

// ConversationRecord represents one imported user conversation projection row.
type ConversationRecord struct {
	// HashSlot is the hash slot that owns this conversation row.
	HashSlot uint16 `json:"hash_slot"`
	// UID is the stable user identifier.
	UID string `json:"uid"`
	// Kind is the conversation kind and must be normal or cmd.
	Kind string `json:"kind"`
	// ChannelID is the stable channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the numeric channel type.
	ChannelType int64 `json:"channel_type"`
	// ReadSeq is the exact latest read message sequence.
	ReadSeq Uint64 `json:"read_seq"`
	// DeletedToSeq is the exact highest message sequence deleted for this conversation.
	DeletedToSeq Uint64 `json:"deleted_to_seq"`
	// ActiveAt is the conversation active timestamp.
	ActiveAt int64 `json:"active_at"`
	// UpdatedAt is the conversation update timestamp.
	UpdatedAt int64 `json:"updated_at"`
	// SparseActive marks rows imported from sparse active conversation state.
	SparseActive bool `json:"sparse_active"`
}

// ChannelLatestRecord represents one imported latest-message projection row for a channel.
type ChannelLatestRecord struct {
	// HashSlot is the hash slot that owns this projection row.
	HashSlot uint16 `json:"hash_slot"`
	// ChannelID is the stable channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the numeric channel type.
	ChannelType int64 `json:"channel_type"`
	// LastMessageID is the exact latest message identifier.
	LastMessageID Uint64 `json:"last_message_id"`
	// LastMessageSeq is the exact latest message sequence.
	LastMessageSeq Uint64 `json:"last_message_seq"`
	// LastAt is the latest message timestamp.
	LastAt int64 `json:"last_at"`
	// FromUID is the sender of the latest message.
	FromUID string `json:"from_uid"`
	// ClientMsgNo is the client message identifier for the latest message.
	ClientMsgNo string `json:"client_msg_no"`
	// LastPayloadB64 is the base64-encoded latest message payload.
	LastPayloadB64 string `json:"last_payload_b64"`
	// Payload is the decoded latest message payload.
	Payload []byte `json:"-"`
	// UpdatedAt is the projection update timestamp.
	UpdatedAt int64 `json:"updated_at"`
}

// MessageChannelRecord represents one imported message channel index row.
type MessageChannelRecord struct {
	// ChannelKey is the encoded message channel key.
	ChannelKey string `json:"channel_key"`
	// ChannelID is the stable channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the compact channel type.
	ChannelType uint8 `json:"channel_type"`
}

// MessageRecord represents one imported channel message log row.
type MessageRecord struct {
	// ChannelKey is the encoded message channel key.
	ChannelKey string `json:"channel_key"`
	// MessageSeq is the exact message sequence within the channel.
	MessageSeq Uint64 `json:"message_seq"`
	// MessageID is the exact globally unique message identifier.
	MessageID Uint64 `json:"message_id"`
	// ClientMsgNo is the client-provided message identifier.
	ClientMsgNo string `json:"client_msg_no"`
	// FromUID is the sender user identifier.
	FromUID string `json:"from_uid"`
	// ServerTimestampMS is the server timestamp in milliseconds.
	ServerTimestampMS int64 `json:"server_timestamp_ms"`
	// PayloadB64 is the base64-encoded message payload.
	PayloadB64 string `json:"payload_b64"`
	// Payload is the decoded message payload.
	Payload []byte `json:"-"`
}
