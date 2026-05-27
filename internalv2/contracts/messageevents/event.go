package messageevents

// MessageCommitted is emitted after a durable channel append succeeds.
type MessageCommitted struct {
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence assigned by channelv2.
	MessageSeq uint64
	// ChannelID is the client-visible channel identifier.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// FromUID is the sender user id.
	FromUID string
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// Payload is a copy of the committed payload.
	Payload []byte
}
