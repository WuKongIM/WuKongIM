package message

import metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"

const (
	// EventTypeStreamOpen starts an open event lane.
	EventTypeStreamOpen = metadb.EventTypeStreamOpen
	// EventTypeStreamDelta appends a delta to an open event lane.
	EventTypeStreamDelta = metadb.EventTypeStreamDelta
	// EventTypeStreamClose closes an event lane successfully.
	EventTypeStreamClose = metadb.EventTypeStreamClose
	// EventTypeStreamError closes an event lane with an error.
	EventTypeStreamError = metadb.EventTypeStreamError
	// EventTypeStreamCancel closes an event lane by cancellation.
	EventTypeStreamCancel = metadb.EventTypeStreamCancel
	// EventTypeStreamSnapshot replaces the compact event lane snapshot.
	EventTypeStreamSnapshot = metadb.EventTypeStreamSnapshot
	// EventTypeStreamFinish marks the message-level finish lane.
	EventTypeStreamFinish = metadb.EventTypeStreamFinish

	// EventStatusOpen reports an active event lane.
	EventStatusOpen = metadb.EventStatusOpen
	// EventStatusClosed reports a completed event lane.
	EventStatusClosed = metadb.EventStatusClosed
	// EventStatusError reports an errored event lane.
	EventStatusError = metadb.EventStatusError
	// EventStatusCancelled reports a cancelled event lane.
	EventStatusCancelled = metadb.EventStatusCancelled

	// EventKeyDefault is the default event lane key.
	EventKeyDefault = metadb.EventKeyDefault
	// EventKeyFinish is the reserved finish lane key.
	EventKeyFinish = metadb.EventKeyFinish

	// VisibilityPublic exposes event state to ordinary sync readers.
	VisibilityPublic = metadb.VisibilityPublic
	// VisibilityPrivate keeps event state scoped to the sender/owner.
	VisibilityPrivate = metadb.VisibilityPrivate
	// VisibilityRestricted keeps event state behind entry-specific policy.
	VisibilityRestricted = metadb.VisibilityRestricted
)

// MessageEventAppend describes one message event projection update.
type MessageEventAppend struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// FromUID identifies the sender used for person/agent channel normalization.
	FromUID string
	// MessageID optionally carries the base message id for entry responses.
	MessageID uint64
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
	// FromUID identifies the sender carried by the append command.
	FromUID string
	// MessageID optionally carries the base message id from the append command.
	MessageID uint64
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

// MessageEventState stores one compact message event lane projection.
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

// MessageEventMessageKey identifies all event lanes for one message.
type MessageEventMessageKey struct {
	// ChannelID identifies the channel that owns the message.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ClientMsgNo identifies the message inside the channel.
	ClientMsgNo string
}

// MessageEventMeta is the compact message event summary attached to a synced message.
type MessageEventMeta struct {
	// HasEvents reports whether the message has compact event lane states.
	HasEvents bool
	// Completed reports whether the reserved finish lane has been observed.
	Completed bool
	// EventVersion mirrors LastMsgEventSeq for compatible clients.
	EventVersion uint64
	// LastMsgEventSeq is the greatest message-level event sequence in returned lanes.
	LastMsgEventSeq uint64
	// EventCount is the number of non-finish lanes in Events.
	EventCount int
	// OpenEventCount is the number of non-finish lanes still open.
	OpenEventCount int
	// Events contains compact per-lane state in event-key order.
	Events []MessageEventKeyMeta
}

// MessageEventKeyMeta is one compact event lane summary.
type MessageEventKeyMeta struct {
	// EventKey identifies the lane.
	EventKey string
	// Status is the lane reducer status.
	Status string
	// LastMsgEventSeq is the latest message-level event sequence applied to this lane.
	LastMsgEventSeq uint64
	// EndReason stores the terminal close reason when provided.
	EndReason uint8
	// Error stores the terminal error message when provided.
	Error string
	// Snapshot optionally contains the decoded or raw snapshot in full summary mode.
	Snapshot any
}

// MessageEventSyncHint tells clients where fine-grained event sync would begin.
type MessageEventSyncHint struct {
	// ClientMsgNo identifies the message for a later event sync request.
	ClientMsgNo string
	// FromMsgEventSeq is the starting message-level event sequence.
	FromMsgEventSeq uint64
}
