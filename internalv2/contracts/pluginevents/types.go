package pluginevents

// PersistAfterCommitted carries one durable committed message into plugin hooks.
type PersistAfterCommitted struct {
	// MessageID is the durable server message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence number.
	MessageSeq uint64
	// ChannelID is the target channel identifier.
	ChannelID string
	// ChannelType is the target channel type.
	ChannelType uint8
	// FromUID is the sender user identifier.
	FromUID string
	// SenderNodeID is the node identifier that accepted the sender session.
	SenderNodeID uint64
	// SenderSessionID is the gateway session identifier that submitted the message.
	SenderSessionID uint64
	// ClientMsgNo is the client-provided idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server commit timestamp in milliseconds.
	ServerTimestampMS int64
	// Payload is the committed message body.
	Payload []byte
	// RedDot reports whether this message should affect unread counters.
	RedDot bool
	// SyncOnce reports whether the message should only sync once to recipients.
	SyncOnce bool
	// MessageScopedUIDs limits plugin-visible recipient scope for this message.
	MessageScopedUIDs []string
}

// Clone returns an independent event copy safe for asynchronous plugin workers.
func (e PersistAfterCommitted) Clone() PersistAfterCommitted {
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}
