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
