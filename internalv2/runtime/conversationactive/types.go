package conversationactive

// Options configures the conversation active admission manager.
type Options struct {
	// NowMS returns the current Unix millisecond when a batch does not carry ActiveAtMS.
	NowMS func() int64
}

// ActiveBatch is the channelwrite output consumed by the active cache.
type ActiveBatch struct {
	// SenderUID identifies the user who sent the committed message.
	SenderUID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the conversation channel type.
	ChannelType uint8
	// MessageSeq is the latest committed message sequence for the batch.
	MessageSeq uint64
	// ActiveAtMS is the Unix millisecond activity timestamp shared by all recipients.
	ActiveAtMS int64
	// Recipients contains the users whose active conversation cache should be touched.
	Recipients []ActiveEntry
}

// ActiveEntry identifies one user touched by an active batch.
type ActiveEntry struct {
	// UID identifies the user that should see the conversation as active.
	UID string
	// IsSender marks the sender's own conversation row so ReadSeq can advance.
	IsSender bool
}

// ActivePatch is the cached active conversation projection for one user/channel.
type ActivePatch struct {
	// UID identifies the owner of the cached conversation row.
	UID string
	// ChannelID identifies the active conversation channel.
	ChannelID string
	// ChannelType identifies the active conversation channel type.
	ChannelType uint8
	// ActiveAtMS is the maximum observed Unix millisecond activity timestamp.
	ActiveAtMS int64
	// ReadSeq is advanced only for the sender's own conversation row.
	ReadSeq uint64
}
