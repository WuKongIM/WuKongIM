package conversationactive

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrStoreRequired reports that durable active-row operations need a store.
var ErrStoreRequired = errors.New("conversationactive: store required")

// ErrCachePressure reports that admitting new active rows would exceed the cache bound.
var ErrCachePressure = errors.New("conversationactive: cache pressure")

// Options configures the conversation active admission manager.
type Options struct {
	// NowMS returns the current Unix millisecond when a batch does not carry ActiveAtMS.
	NowMS func() int64
	// Store reads and persists durable active conversation rows that have already flushed to DB.
	Store ActiveStore
	// MaxCachedRows bounds in-memory active rows across all users; zero disables the bound.
	MaxCachedRows int
}

// ActiveStore reads and persists durable active conversation rows for cache/DB view merging.
type ActiveStore interface {
	// ListUserConversationActivePage returns active rows after the active-index cursor.
	ListUserConversationActivePage(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
	// GetUserConversationState returns one durable primary row for cache-only hydration.
	GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, bool, error)
	// TouchUserConversationActiveAt persists active-row patches produced by the runtime cache.
	TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error
}

// FlushResult reports the outcome of one dirty active-row flush attempt.
type FlushResult struct {
	// Selected is the number of dirty rows selected from cache for this flush.
	Selected int
	// Flushed is the number of selected rows durably written by the store.
	Flushed int
}

// ActiveViewPage is a merged cache plus durable active-row page.
type ActiveViewPage struct {
	// Rows contains active conversations ordered by active-index order.
	Rows []metadb.UserConversationState
	// Cursor identifies the last returned row for the next page request.
	Cursor metadb.UserConversationActiveCursor
	// Done reports that there are no more active rows after Cursor.
	Done bool
}

// ActiveBatch is the channelwrite output consumed by the active cache.
type ActiveBatch struct {
	// SenderUID identifies the user who sent the committed message.
	SenderUID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the conversation channel type.
	ChannelType uint8
	// MessageSeq is the latest committed message sequence for the batch.
	MessageSeq uint64
	// ActiveAtMS is the Unix millisecond activity timestamp shared by all recipients.
	ActiveAtMS int64
	// Recipients contains the users whose active conversation cache should be touched.
	Recipients []ActiveEntry
}

// ActiveEntry identifies one user touched by an active batch.
type ActiveEntry struct {
	// UID identifies the user that should see the conversation as active.
	UID string
	// IsSender marks the sender's own conversation row so ReadSeq can advance.
	IsSender bool
}

// ActivePatch is the cached active conversation projection for one user/channel.
type ActivePatch struct {
	// UID identifies the owner of the cached conversation row.
	UID string
	// ChannelID identifies the active conversation channel.
	ChannelID string
	// ChannelType identifies the active conversation channel type.
	ChannelType uint8
	// ActiveAtMS is the maximum observed Unix millisecond activity timestamp.
	ActiveAtMS int64
	// ReadSeq is advanced only for the sender's own conversation row.
	ReadSeq uint64
}
