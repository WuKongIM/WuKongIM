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

// Envelope carries the committed message data needed by delivery fanout.
type Envelope struct {
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the channel-local sequence assigned to the committed message.
	MessageSeq uint64
	// ChannelID identifies the channel that produced the committed message.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// FromUID is the sender user ID.
	FromUID string
	// SenderNodeID is the sender owner node ID used for same-connection echo suppression.
	SenderNodeID uint64
	// SenderSessionID is the sender owner-local gateway session used for same-connection echo suppression.
	SenderSessionID uint64
	// ClientMsgNo is the client idempotency key associated with the send request.
	ClientMsgNo string
	// RedDot carries the client red-dot flag for delivery side effects.
	RedDot bool
	// Payload is the committed message payload.
	Payload []byte
	// MessageScopedUIDs are request-scoped one-shot delivery targets.
	MessageScopedUIDs []string
}

// Partition identifies one delivery authority partition planned for fanout.
type Partition struct {
	// ID is the stable delivery partition identifier.
	ID uint32
	// LeaderNodeID is the node currently authoritative for this partition.
	LeaderNodeID uint64
	// HashSlotStart is the inclusive first hash slot owned by this partition.
	HashSlotStart uint16
	// HashSlotEnd is the inclusive last hash slot owned by this partition.
	HashSlotEnd uint16
}

// FanoutTask binds a committed message envelope to one authority partition.
type FanoutTask struct {
	// Envelope is an independent copy of the committed message data.
	Envelope Envelope
	// Partition is the authority partition to scan or target.
	Partition Partition
	// Cursor resumes subscriber scanning within the partition.
	Cursor string
	// Attempt is the one-based fanout attempt number.
	Attempt int
}

// Route describes one online recipient endpoint resolved by presence.
type Route struct {
	// UID is the recipient user ID for this endpoint.
	UID string
	// OwnerNodeID is the node that owns the recipient gateway session.
	OwnerNodeID uint64
	// OwnerBootID fences stale owner-node process incarnations.
	OwnerBootID uint64
	// OwnerSeq fences stale owner-session authority observations.
	OwnerSeq uint64
	// SessionID is the recipient owner-local gateway session identifier.
	SessionID uint64
	// DeviceID identifies the recipient client device.
	DeviceID string
	// DeviceFlag carries protocol device category metadata.
	DeviceFlag uint8
	// DeviceLevel carries protocol device priority metadata.
	DeviceLevel uint8
}

// PushCommand groups recipient routes owned by the same node for one envelope.
type PushCommand struct {
	// OwnerNodeID is the recipient owner node that should accept the push.
	OwnerNodeID uint64
	// Envelope is an independent copy of the message being pushed.
	Envelope Envelope
	// Routes are the recipient endpoints owned by OwnerNodeID.
	Routes []Route
}

// PushResult reports how an owner node classified pushed recipient routes.
type PushResult struct {
	// Accepted routes were accepted for delivery by the owner node.
	Accepted []Route
	// Retryable routes should be retried by a later runtime stage.
	Retryable []Route
	// Dropped routes should not be retried.
	Dropped []Route
}
