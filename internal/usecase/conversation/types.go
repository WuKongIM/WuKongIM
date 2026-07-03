package conversation

import (
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrRouteNotReady indicates that the UID authority route cannot serve a request yet.
	ErrRouteNotReady = errors.New("internal/usecase/conversation: route not ready")
	// ErrStaleRoute indicates that a request was sent to an outdated UID authority target.
	ErrStaleRoute = errors.New("internal/usecase/conversation: stale route")
	// ErrNotLeader indicates that the target node is no longer the UID authority leader.
	ErrNotLeader = errors.New("internal/usecase/conversation: not leader")
	// ErrCachePressure indicates that authority cache pressure prevents a complete successful List.
	ErrCachePressure = errors.New("internal/usecase/conversation: authority cache pressure")
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
}

// ConversationKey identifies one channel conversation in usecase APIs.
type ConversationKey struct {
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
}

// SyncQuery describes a legacy-compatible conversation sync request after adapter mapping.
type SyncQuery struct {
	// UID identifies the user whose conversations should be synchronized.
	UID string
	// Version is accepted for legacy clients; sync uses active rows plus client-known overlays.
	Version int64
	// LastMsgSeqs contains client-known channel sequence floors keyed by normalized conversation.
	LastMsgSeqs map[ConversationKey]uint64
	// MsgCount bounds recent messages loaded for each returned conversation.
	MsgCount int
	// OnlyUnread keeps only conversations with unread messages when true.
	OnlyUnread bool
	// ExcludeChannelTypes skips conversations whose channel type appears in this list.
	ExcludeChannelTypes []uint8
	// Limit bounds returned conversations. Zero uses the legacy-compatible default.
	Limit int
}

// ClearUnreadCommand advances a user's read cursor to the channel latest sequence.
type ClearUnreadCommand struct {
	// UID identifies the user whose conversation read cursor should advance.
	UID string
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
	// MessageSeq is a legacy client-supplied latest sequence fallback.
	MessageSeq uint64
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
	// MessageSeq is the delete barrier. When zero, the latest channel sequence is loaded.
	MessageSeq uint64
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
	// Payload stores the durable message payload.
	Payload []byte
}

// SyncMessage is one recent message returned by legacy-compatible conversation sync.
type SyncMessage struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the channel-local message sequence.
	MessageSeq uint64
	// FromUID identifies the sender.
	FromUID string
	// ChannelID identifies the normalized channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
	// ClientMsgNo stores the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
	// Payload stores the durable message payload.
	Payload []byte
}

// SyncConversation is one conversation returned by legacy-compatible sync.
type SyncConversation struct {
	// ChannelID identifies the normalized conversation channel.
	ChannelID string
	// ChannelType identifies the protocol channel category.
	ChannelType uint8
	// Unread is the unread message count derived from read/delete floors.
	Unread int
	// Timestamp is the latest message timestamp in Unix seconds.
	Timestamp int64
	// LastMsgSeq is the newest visible channel sequence.
	LastMsgSeq uint32
	// LastClientMsgNo is the client idempotency key of the newest visible message.
	LastClientMsgNo string
	// ReadToMsgSeq is the read floor returned to legacy clients.
	ReadToMsgSeq uint32
	// Version is a compatibility timestamp for this conversation row.
	Version int64
	// Recents contains recent messages for this conversation when requested.
	Recents []SyncMessage
}

// SyncResult contains the conversations selected for one sync response.
type SyncResult struct {
	// Conversations contains legacy-compatible synchronized conversations.
	Conversations []SyncConversation
	// OverlayItems is the number of client-known overlay candidates before sync filtering.
	OverlayItems int
	// RecentLoadDuration records how long recent-message loading took when requested.
	RecentLoadDuration time.Duration
}

// Conversation is one channel row in a user's conversation list.
type Conversation struct {
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ActiveAt is the UID-owned ordering anchor for the list.
	ActiveAt int64
	// ReadSeq is the highest message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest message sequence hidden from future reads.
	DeletedToSeq uint64
	// SparseActive reports that ActiveAt is a low-frequency ordering anchor.
	SparseActive bool
	// UpdatedAt records when the UID-owned row was last advanced.
	UpdatedAt int64
	// LastMessage is the newest visible message for display, when one exists.
	LastMessage *LastMessage
	// Unread is the first-version unread count derived from row read state and the last message sequence.
	Unread uint64
}

// ListResult contains one sorted conversation page.
type ListResult struct {
	// Items contains the returned page.
	Items []Conversation
	// NextCursor resumes after the last returned item when HasMore is true.
	NextCursor Cursor
	// HasMore reports whether another sorted page is available inside the scan window.
	HasMore bool
}

// RouteTarget identifies the fenced UID authority that should serve a conversation request.
type RouteTarget struct {
	// HashSlot is the logical UID hash slot.
	HashSlot uint16
	// SlotID is the physical Slot that owns HashSlot.
	SlotID uint32
	// LeaderNodeID is the authority leader node for this target.
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	// RouteRevision is the route-table revision used to resolve this target.
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence kept for diagnostics only.
	AuthorityEpoch uint64
}

// ActivePatch is an unflushed conversation activity candidate owned by a UID authority.
type ActivePatch struct {
	// UID identifies the user that owns the conversation row.
	UID string
	// Kind identifies the logical conversation projection view.
	Kind metadb.ConversationKind
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the minimum read floor derived from membership visibility.
	ReadSeq uint64
	// DeletedToSeq is the minimum delete floor derived from membership visibility.
	DeletedToSeq uint64
	// ActiveAt is the candidate active-list ordering timestamp.
	ActiveAt int64
	// UpdatedAt records when this active candidate was produced.
	UpdatedAt int64
	// SparseActive is the requested sparse-active mode.
	SparseActive bool
	// MessageSeq fences stale activity after user delete barriers.
	MessageSeq uint64
}

// ToMetaPatch converts the activity candidate to the durable DB patch command.
func (p ActivePatch) ToMetaPatch() metadb.ConversationActivePatch {
	return metadb.ConversationActivePatch{
		UID:             p.UID,
		Kind:            p.Kind,
		ChannelID:       p.ChannelID,
		ChannelType:     p.ChannelType,
		ReadSeq:         p.ReadSeq,
		DeletedToSeq:    p.DeletedToSeq,
		ActiveAt:        p.ActiveAt,
		UpdatedAt:       p.UpdatedAt,
		MessageSeq:      p.MessageSeq,
		SparseActive:    p.SparseActive,
		SparseActiveSet: true,
	}
}

// ActiveViewPage is one authoritative active-row page before last-message hydration.
type ActiveViewPage struct {
	// Rows contains DB rows merged with unflushed authority cache rows.
	Rows []metadb.ConversationState
	// Cursor is the active index cursor after the last returned row.
	Cursor metadb.ConversationActiveCursor
	// Done reports that no further rows are available in the authoritative view.
	Done bool
}
