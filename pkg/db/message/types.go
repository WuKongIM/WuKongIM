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
	// Payload stores the encoded message payload.
	Payload []byte
	// SizeBytes optionally stores the caller-known payload size.
	SizeBytes int
}

// Message is one materialized persisted message.
type Message struct {
	// MessageSeq is the durable channel sequence.
	MessageSeq uint64
	// MessageID is the stable message ID.
	MessageID uint64
	// Payload stores the message payload.
	Payload []byte
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
