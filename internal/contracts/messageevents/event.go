package messageevents

// MessageCommitted is emitted after a durable channel append succeeds.
type MessageCommitted struct {
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence assigned by the channel runtime.
	MessageSeq uint64
	// ChannelID is the client-visible channel identifier.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// FromUID is the sender user id.
	FromUID string
	// SenderNodeID is the sender owner node id used to suppress same-connection echo.
	SenderNodeID uint64
	// SenderSessionID is the owner-local sender session id used to suppress same-connection echo.
	SenderSessionID uint64
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp used for conversation ordering.
	ServerTimestampMS int64
	// Payload is a copy of the committed payload.
	Payload []byte
	// RedDot carries the client red-dot flag for delivery side effects.
	RedDot bool
	// MessageScopedUIDs are request-scoped one-shot delivery targets.
	MessageScopedUIDs []string
}

// Clone returns an independent copy of the committed-message event.
func (e MessageCommitted) Clone() MessageCommitted {
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}
