package conversation

// Cursor resumes a sorted conversation list after one emitted row.
type Cursor struct {
	// LastAt is the last emitted conversation timestamp.
	LastAt int64
	// LastMessageSeq is the last emitted channel message sequence.
	LastMessageSeq uint64
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

// Conversation is one channel row in a user's conversation list.
type Conversation struct {
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// LastMessageID is the newest durable message id observed for the channel.
	LastMessageID uint64
	// LastMessageSeq is the newest durable channel sequence observed.
	LastMessageSeq uint64
	// LastAt is the timestamp used for conversation list sorting.
	LastAt int64
	// FromUID identifies the newest message sender.
	FromUID string
	// ClientMsgNo stores the newest message client idempotency key.
	ClientMsgNo string
	// Payload stores the newest message payload or preview bytes.
	Payload []byte
	// UpdatedAt records when the projection row was last advanced.
	UpdatedAt int64
}

// ListResult contains one sorted conversation page.
type ListResult struct {
	// Items contains the returned page.
	Items []Conversation
	// NextCursor resumes after the last returned item when HasMore is true.
	NextCursor Cursor
	// HasMore reports whether another sorted page is available inside the scan window.
	HasMore bool
	// Truncated reports whether MaxMembershipScan stopped the membership scan early.
	Truncated bool
	// ScannedMemberships reports how many UID membership rows were joined.
	ScannedMemberships int
}
