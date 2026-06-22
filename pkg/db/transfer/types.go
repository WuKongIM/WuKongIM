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
