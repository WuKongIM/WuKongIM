package delivery

// PendingRecvAck records one owner-local delivery waiting for a client recvack.
type PendingRecvAck struct {
	// UID is the recipient user ID that owns the gateway session.
	UID string
	// SessionID is the recipient-owner gateway session identifier.
	SessionID uint64
	// MessageID is the globally unique message identifier waiting for recvack.
	MessageID uint64
	// MessageSeq is the channel-local sequence assigned to the delivered message.
	MessageSeq uint64
	// ChannelID identifies the channel that produced the delivered message.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// DeliveredAt is the Unix second when the recipient owner accepted delivery.
	DeliveredAt int64
}

// Recvack identifies a client recvack for one delivered message.
type Recvack struct {
	// UID is the recipient user ID that owns the gateway session.
	UID string
	// SessionID is the recipient-owner gateway session identifier.
	SessionID uint64
	// MessageID is the globally unique message identifier acknowledged by the client.
	MessageID uint64
	// MessageSeq is the channel-local sequence acknowledged by the client.
	MessageSeq uint64
}

// SessionClosed identifies a recipient-owner gateway session that closed locally.
type SessionClosed struct {
	// UID is the recipient user ID that owns the closed gateway session.
	UID string
	// SessionID is the recipient-owner gateway session identifier that closed.
	SessionID uint64
}
