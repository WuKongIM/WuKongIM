package delivery

// PendingRecvAck is one owner-local delivered message awaiting client
// acknowledgement. Its identity is UID, SessionID, and MessageID.
type PendingRecvAck struct {
	// UID identifies the recipient whose client must acknowledge.
	UID string
	// SessionID fences cleanup to one owner-local gateway session.
	SessionID uint64
	// MessageID is the protocol-visible ACK identity within the session.
	MessageID uint64
	// MessageSeq preserves delivery metadata for diagnostics.
	MessageSeq uint64
	// ChannelID preserves the delivered channel identity for diagnostics.
	ChannelID string
	// ChannelType preserves the delivered channel kind for diagnostics.
	ChannelType uint8
	// DeliveredAt is the Unix second used by bounded TTL expiry.
	DeliveredAt int64
}

// Recvack is exact client feedback for one delivered message.
type Recvack struct {
	// UID identifies the acknowledging recipient.
	UID string
	// SessionID identifies the exact owner-local gateway session.
	SessionID uint64
	// MessageID identifies the pending delivery.
	MessageID uint64
	// MessageSeq is protocol feedback metadata and is not part of ACK identity.
	MessageSeq uint64
}

// SessionClosed identifies an owner-local session whose pending ACK state must
// be removed.
type SessionClosed struct {
	// UID identifies the disconnected recipient.
	UID string
	// SessionID identifies the exact owner-local session to clear.
	SessionID uint64
}
