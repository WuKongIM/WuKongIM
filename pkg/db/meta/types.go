package meta

// HashSlot identifies one metadata hash-slot partition.
type HashSlot = uint16

// ChannelID identifies a channel in metadata rows and indexes.
type ChannelID struct {
	// ID is the channel identifier.
	ID string
	// Type is the channel type.
	Type int64
}

// MessageEventAppend describes one incoming message event projection update.
type MessageEventAppend struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ClientMsgNo identifies the message inside the channel.
	ClientMsgNo string
	// EventID is the idempotency key for this event lane.
	EventID string
	// EventKey identifies the projected event lane for this message.
	EventKey string
	// EventType identifies the reducer transition to apply.
	EventType string
	// Visibility describes who can read the projected event state.
	Visibility string
	// OccurredAt records when the source event happened.
	OccurredAt int64
	// Payload stores the reducer payload for this event.
	Payload []byte
	// UpdatedAt records when the projection update was created.
	UpdatedAt int64
}

// MessageEventAppendResult reports the durable state after applying an event.
type MessageEventAppendResult struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ClientMsgNo identifies the message inside the channel.
	ClientMsgNo string
	// EventID is the applied or idempotently observed event id.
	EventID string
	// EventKey identifies the projected event lane for this message.
	EventKey string
	// MsgEventSeq is the per-message event sequence after the append.
	MsgEventSeq uint64
	// Status is the projected lane status after the append.
	Status string
	// State is the full projected lane state after the append.
	State MessageEventState
}

// MessageEventState stores one message event lane projection state.
type MessageEventState struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ClientMsgNo identifies the message inside the channel.
	ClientMsgNo string
	// EventKey identifies the projected event lane for this message.
	EventKey string
	// Status records whether the event lane is open or terminal.
	Status string
	// LastMsgEventSeq is the latest per-message event sequence applied to this lane.
	LastMsgEventSeq uint64
	// LastEventID is the latest idempotency key applied to this lane.
	LastEventID string
	// LastEventType is the latest event type applied to this lane.
	LastEventType string
	// LastVisibility is the latest visibility associated with this lane.
	LastVisibility string
	// LastOccurredAt records when the latest source event happened.
	LastOccurredAt int64
	// SnapshotPayload stores the compact projected lane payload.
	SnapshotPayload []byte
	// EndReason stores the terminal close reason when provided.
	EndReason uint8
	// Error stores the terminal error message when provided.
	Error string
	// UpdatedAt records when this projection row was last updated.
	UpdatedAt int64
}

// MessageEventCursor stores the latest event sequence for one message.
type MessageEventCursor struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ClientMsgNo identifies the message inside the channel.
	ClientMsgNo string
	// LastMsgEventSeq is the latest event sequence allocated for the message.
	LastMsgEventSeq uint64
	// UpdatedAt records when this cursor row was last advanced.
	UpdatedAt int64
}

// MessageEventMessageKey identifies all event lanes for one message.
type MessageEventMessageKey struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ClientMsgNo identifies the message inside the channel.
	ClientMsgNo string
}

// ConversationKind identifies one logical UID-owned conversation projection view.
type ConversationKind uint8

const (
	// ConversationKindNormal stores ordinary chat conversation cursors.
	ConversationKindNormal ConversationKind = 1
	// ConversationKindCMD stores command-channel sync cursors.
	ConversationKindCMD ConversationKind = 2
)

const (
	// TableIDUser stores user token and device defaults.
	TableIDUser uint32 = 1
	// TableIDChannel stores channel flags and subscriber version.
	TableIDChannel uint32 = 2
	// TableIDChannelRuntimeMeta stores channel runtime routing metadata.
	TableIDChannelRuntimeMeta uint32 = 3
	// TableIDDevice stores per-device token state.
	TableIDDevice uint32 = 4
	// TableIDSubscriber stores channel subscribers.
	TableIDSubscriber uint32 = 5
	// TableIDConversation stores user conversation state.
	TableIDConversation uint32 = 6
	// TableIDCMDConversation is reserved by the development-era split CMD table and must not be reused.
	TableIDCMDConversation uint32 = 7
	// TableIDPluginBinding stores plugin user bindings.
	TableIDPluginBinding uint32 = 8
	// TableIDChannelMigration stores channel migration tasks.
	TableIDChannelMigration uint32 = 9
	// TableIDHashSlotMigration stores hash-slot migration tasks.
	TableIDHashSlotMigration uint32 = 10
	// TableIDUserChannelMembership stores UID-owned channel membership rows.
	TableIDUserChannelMembership uint32 = 11
	// TableIDChannelLatest stores channel-owned latest message projections.
	TableIDChannelLatest uint32 = 12
	// TableIDMessageEventState stores message event lane projection state.
	TableIDMessageEventState uint32 = 13
	// TableIDMessageEventCursor stores the latest event sequence per message.
	TableIDMessageEventCursor uint32 = 14
)

const (
	userPrimaryFamilyID uint16 = 0
	userPrimaryIndexID  uint16 = 1

	devicePrimaryFamilyID uint16 = 0
	devicePrimaryIndexID  uint16 = 1

	channelPrimaryFamilyID    uint16 = 0
	channelPrimaryIndexID     uint16 = 1
	channelIDIndexID          uint16 = 2
	channelActiveIndexID      uint16 = 3
	subscriberPrimaryFamilyID uint16 = 0
	subscriberPrimaryIndexID  uint16 = 1

	userChannelMembershipPrimaryFamilyID uint16 = 0
	userChannelMembershipPrimaryIndexID  uint16 = 1

	channelLatestPrimaryFamilyID uint16 = 0
	channelLatestPrimaryIndexID  uint16 = 1

	messageEventStatePrimaryFamilyID uint16 = 0
	messageEventStatePrimaryIndexID  uint16 = 1

	messageEventCursorPrimaryFamilyID uint16 = 0
	messageEventCursorPrimaryIndexID  uint16 = 1

	conversationPrimaryIndexID uint16 = 1
	conversationActiveIndexID  uint16 = 2

	systemIDSnapshot uint16 = 1
)
