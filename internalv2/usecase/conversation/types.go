package conversation

// Cursor resumes a sorted conversation list after one emitted row.
type Cursor struct {
	// ActiveAt is the last emitted active-index timestamp.
	ActiveAt int64
	// ChannelID is the last emitted channel id.
	ChannelID string
	// ChannelType is the last emitted channel type.
	ChannelType int64
}

// ListRequest configures one conversation list read.
type ListRequest struct {
	// UID identifies the user whose conversation list should be read.
	UID string
	// Cursor resumes after the previous page's last item.
	Cursor Cursor
	// Limit bounds returned conversations. Zero uses the default limit.
	Limit int
}

// LastMessage is the newest visible durable message for a conversation row.
type LastMessage struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the channel-local message sequence.
	MessageSeq uint64
	// FromUID identifies the sender.
	FromUID string
	// ClientMsgNo stores the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
	// Payload stores the durable message payload.
	Payload []byte
}

// Conversation is one channel row in a user's conversation list.
type Conversation struct {
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ActiveAt is the UID-owned ordering anchor for the list.
	ActiveAt int64
	// ReadSeq is the highest message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest message sequence hidden from future reads.
	DeletedToSeq uint64
	// SparseActive reports that ActiveAt is a low-frequency ordering anchor.
	SparseActive bool
	// UpdatedAt records when the projection row was last advanced.
	UpdatedAt int64
	// LastMessage is the newest visible message for display, when one exists.
	LastMessage *LastMessage
	// Unread is the first-version unread count derived from row read state and the last message sequence.
	Unread uint64
}

// ListResult contains one sorted conversation page.
type ListResult struct {
	// Items contains the returned page.
	Items []Conversation
	// NextCursor resumes after the last returned item when HasMore is true.
	NextCursor Cursor
	// HasMore reports whether another sorted page is available inside the scan window.
	HasMore bool
}
