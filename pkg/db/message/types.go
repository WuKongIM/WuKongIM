package message

// ChannelKey is the stable partition key for one channel log.
type ChannelKey string

// ChannelID identifies the user-facing channel identity.
type ChannelID struct {
	// ID is the channel identifier.
	ID string
	// Type is the channel type.
	Type uint8
}

// Record is one append input record.
type Record struct {
	// ID is the stable message ID.
	ID uint64
	// ClientMsgNo is the optional client-provided message number.
	ClientMsgNo string
	// FromUID is the optional sender UID used with ClientMsgNo for idempotency.
	FromUID string
	// Payload stores the encoded message payload.
	Payload []byte
	// SizeBytes optionally stores the caller-known payload size.
	SizeBytes int
}

// AppendResult describes the durable sequence range assigned by an append.
type AppendResult struct {
	// BaseSeq is the first assigned sequence, or zero for an empty append.
	BaseSeq uint64
	// LastSeq is the last assigned sequence, or zero for an empty append.
	LastSeq uint64
	// Count is the number of records written.
	Count int
}

// Message is one materialized persisted message.
type Message struct {
	// MessageSeq is the durable channel sequence.
	MessageSeq uint64
	// MessageID is the stable message ID.
	MessageID uint64
	// ClientMsgNo is the optional client-provided message number.
	ClientMsgNo string
	// FromUID is the optional sender UID used with ClientMsgNo for idempotency.
	FromUID string
	// PayloadHash is the persisted payload hash.
	PayloadHash uint64
	// Payload stores the message payload.
	Payload []byte
}

// IdempotencyKey identifies one sender/client-message pair in a channel.
type IdempotencyKey struct {
	// FromUID is the sender UID.
	FromUID string
	// ClientMsgNo is the client-provided message number.
	ClientMsgNo string
}

// IdempotencyHit is the durable message selected by an idempotency key.
type IdempotencyHit struct {
	// MessageSeq is the durable channel sequence.
	MessageSeq uint64
	// MessageID is the stable message ID.
	MessageID uint64
	// Offset is the zero-based channel offset matching MessageSeq-1.
	Offset uint64
	// PayloadHash is the payload hash recorded with the idempotency entry.
	PayloadHash uint64
}

// Checkpoint records durable committed progress for one channel log.
type Checkpoint struct {
	// Epoch is the channel epoch of the checkpoint.
	Epoch uint64
	// LogStartOffset is the first retained durable offset.
	LogStartOffset uint64
	// HW is the committed high watermark.
	HW uint64
}

// EpochPoint records the first offset owned by a channel epoch.
type EpochPoint struct {
	// Epoch is the channel epoch.
	Epoch uint64
	// StartOffset is the first offset for Epoch.
	StartOffset uint64
}

// Snapshot stores a durable channel snapshot payload and boundary.
type Snapshot struct {
	// Epoch is the channel epoch captured by the snapshot.
	Epoch uint64
	// EndOffset is the last offset included by the snapshot.
	EndOffset uint64
	// Payload stores the encoded snapshot bytes.
	Payload []byte
}

// ApplyFetchRequest applies a follower fetch batch and optional system state.
type ApplyFetchRequest struct {
	// BaseSeq explicitly pins the first record sequence.
	BaseSeq uint64
	// Records contains fetched records in sequence order.
	Records []Record
	// Checkpoint optionally stores committed progress atomically with records.
	Checkpoint *Checkpoint
	// EpochPoint optionally stores an epoch boundary atomically with records.
	EpochPoint *EpochPoint
}

// RetentionState records local message retention progress for one channel.
type RetentionState struct {
	// LocalRetentionThroughSeq is the adopted logical retention boundary.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest physically deleted sequence.
	PhysicalRetentionThroughSeq uint64
	// RetainedMaxSeq preserves LEO when all rows at the tail are trimmed.
	RetainedMaxSeq uint64
}

// RetentionTrimResult describes one prefix retention trim.
type RetentionTrimResult struct {
	// DeletedThroughSeq is the highest sequence deleted by this trim.
	DeletedThroughSeq uint64
	// Deleted is the number of message rows deleted.
	Deleted int
}

// ChannelCatalogEntry describes one known channel in the message domain.
type ChannelCatalogEntry struct {
	// Key is the stable channel partition key.
	Key ChannelKey
	// ID is the user-facing channel identity.
	ID ChannelID
}

// AppendMode controls append validation work.
type AppendMode uint8

const (
	// AppendStrict checks existing unique indexes before writing.
	AppendStrict AppendMode = iota
	// AppendTrustedContiguous skips existing index reads for caller-validated rows.
	AppendTrustedContiguous
)

// AppendOptions configures one append call.
type AppendOptions struct {
	// Mode controls duplicate validation.
	Mode AppendMode
	// BaseSeq explicitly pins the first assigned sequence when non-zero.
	BaseSeq uint64
}

// ReadOptions configures log scans.
type ReadOptions struct {
	// Limit caps returned messages when positive.
	Limit int
	// MaxBytes caps returned payload bytes when positive.
	MaxBytes int
}

// MessagePage is one paged message result.
type MessagePage struct {
	// Messages contains the returned messages.
	Messages []Message
	// NextBeforeSeq is the cursor for the next page.
	NextBeforeSeq uint64
	// HasMore reports whether another page may exist.
	HasMore bool
}
