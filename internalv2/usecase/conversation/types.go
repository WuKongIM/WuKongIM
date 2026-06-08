package conversation

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrRouteNotReady indicates that the UID authority route cannot serve a request yet.
	ErrRouteNotReady = errors.New("internalv2/usecase/conversation: route not ready")
	// ErrStaleRoute indicates that a request was sent to an outdated UID authority target.
	ErrStaleRoute = errors.New("internalv2/usecase/conversation: stale route")
	// ErrNotLeader indicates that the target node is no longer the UID authority leader.
	ErrNotLeader = errors.New("internalv2/usecase/conversation: not leader")
	// ErrCachePressure indicates that authority cache pressure prevents a complete successful List.
	ErrCachePressure = errors.New("internalv2/usecase/conversation: authority cache pressure")
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
	// UpdatedAt records when the projection row was last advanced.
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
	// RouteRevision is the route-table revision used to resolve this target.
	RouteRevision uint64
	// AuthorityEpoch fences leadership changes for this hash slot.
	AuthorityEpoch uint64
}

// ActivePatch is an unflushed conversation activity candidate owned by a UID authority.
type ActivePatch struct {
	// UID identifies the user that owns the conversation row.
	UID string
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
	// UpdatedAt records when the projection advanced this row in memory.
	UpdatedAt int64
	// SparseActive is the projected sparse-active mode.
	SparseActive bool
	// MessageSeq fences stale activity after user delete barriers.
	MessageSeq uint64
}

// ToMetaPatch converts the activity candidate to the durable DB patch command.
func (p ActivePatch) ToMetaPatch() metadb.UserConversationActivePatch {
	return metadb.UserConversationActivePatch{
		UID:             p.UID,
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
	Rows []metadb.UserConversationState
	// Cursor is the active index cursor after the last returned row.
	Cursor metadb.UserConversationActiveCursor
	// Done reports that no further rows are available in the authoritative view.
	Done bool
}

// MemberSource classifies channel membership for conversation projection.
type MemberSource interface {
	ClassifyMembers(ctx context.Context, channelID string, channelType int64, limit int) (MemberClass, error)
}

// MemberClass describes whether a channel can be safely fanned out synchronously by the projector.
type MemberClass struct {
	// IsSmall reports that Members contains the complete fanout target set.
	IsSmall bool
	// Members contains the bounded member snapshot used for small-channel fanout.
	Members []Member
}

// Member describes one channel member and its first visible sequence.
type Member struct {
	// UID identifies the member user.
	UID string
	// JoinSeq is the first channel sequence visible to this member.
	JoinSeq uint64
}

// ConversationBatchStore persists UID-owned conversation state rows.
type ConversationBatchStore interface {
	UpsertUserConversationStatesBatch(ctx context.Context, states []metadb.UserConversationState) error
}
