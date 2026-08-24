package conversation

import (
	"errors"
)

var (
	// ErrRouteNotReady indicates that the UID authority route cannot serve a request yet.
	ErrRouteNotReady = errors.New("internal/usecase/conversation: route not ready")
)

// Cursor resumes a sorted conversation list after one emitted row.
type Cursor struct {
	// ActiveAt is the last emitted active-index timestamp.
	ActiveAt int64
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
	// CompletedCoverage is the timestamp of the client's last fully completed pass.
	CompletedCoverage int64
}

// RetryRequest rehydrates a bounded set of unresolved channel keys without
// rewinding directory coverage.
type RetryRequest struct {
	// UID owns every membership row selected for retry.
	UID string
	// Keys is the bounded unresolved set to hydrate without moving coverage.
	Keys []ConversationKey
}

// ConversationKey identifies one channel conversation in usecase APIs.
type ConversationKey struct {
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
}

// ClearUnreadCommand advances a user's read cursor to the channel latest sequence.
type ClearUnreadCommand struct {
	// UID identifies the user whose conversation read cursor should advance.
	UID string
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
}

// SetUnreadCommand advances a user's read cursor so at most Unread messages remain unread.
type SetUnreadCommand struct {
	// UID identifies the user whose unread count should be adjusted.
	UID string
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
	// Unread is the requested unread tail size.
	Unread int
}

// DeleteConversationCommand hides a conversation through MessageSeq for one user.
type DeleteConversationCommand struct {
	// UID identifies the user that owns the hidden conversation row.
	UID string
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
}

// ActivateConversationCommand raises one channel's synchronization priority
// after an explicit user open/switch/resume action.
type ActivateConversationCommand struct {
	// UID owns the membership whose synchronization priority is raised.
	UID string
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
}

// LastMessage is the newest visible durable message for a conversation row.
type LastMessage struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the channel-local message sequence.
	MessageSeq uint64
	// FromUID identifies the sender.
	FromUID string
	// ClientMsgNo stores the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
	// Payload stores caller-owned immutable durable message bytes. Response
	// adapters may retain this slice until synchronous serialization completes.
	Payload []byte
}

// Conversation is one channel row in a user's conversation list.
type Conversation struct {
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// JoinSeq is the first channel sequence visible after the current join.
	JoinSeq uint64
	// ActiveAt is the UID-owned ordering anchor for the list.
	ActiveAt int64
	// ReadSeq is the highest message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest message sequence hidden from future reads.
	DeletedToSeq uint64
	// UpdatedAt records when the UID-owned row was last advanced.
	UpdatedAt int64
	// LastMessage is the newest visible message for display, when one exists.
	LastMessage *LastMessage
	// Unread is the first-version unread count derived from row read state and the last message sequence.
	Unread uint64
}

// ListResult contains one sorted conversation page.
type ListResult struct {
	// ScannedCandidates is the number of membership rows consumed by this page.
	ScannedCandidates int
	// Items contains the returned page.
	Items []Conversation
	// Deletes contains membership tombstones or terminal channels scanned in this page.
	Deletes []ConversationKey
	// Unresolved contains live channels whose Leader hydration should be retried.
	Unresolved []ConversationKey
	// NextCursor resumes after the last returned item when HasMore is true.
	NextCursor Cursor
	// HasMore reports whether another sorted page is available inside the scan window.
	HasMore bool
	// Done is the only authoritative signal that a directory pass is complete.
	Done bool
	// Coverage is persisted by the client only when Done is true.
	Coverage int64
	// TombstonesRetainedSince is the oldest deletion coverage still guaranteed by the server.
	TombstonesRetainedSince int64
	// ResetRequired tells a client that its completed coverage predates retained deletions.
	ResetRequired bool
}
