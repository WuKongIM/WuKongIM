package conversationactive

import (
	"context"
	"errors"
	"time"

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
	// ActiveCooldown skips receiver-only active_at flushes newer than the durable row by less than this duration.
	ActiveCooldown time.Duration
	// MaxCachedRows bounds in-memory active rows across all users; zero disables the bound.
	MaxCachedRows int
	// PressureNotify receives nonblocking coalesced wakeups for proactive or hard-bound dirty cache pressure.
	PressureNotify chan<- PressureSignal
	// Observer receives low-cardinality cache and flush observations.
	Observer Observer
}

// Observer receives conversation active cache and flush observations.
// Implementations must be safe for concurrent manager and app-worker calls.
type Observer interface {
	// ObserveConversationActiveCache records a current cache pressure snapshot.
	ObserveConversationActiveCache(CacheObservation)
	// ObserveConversationActiveMutation records one cache mutation batch.
	ObserveConversationActiveMutation(MutationObservation)
	// ObserveConversationActiveFlush records one dirty-row flush attempt.
	ObserveConversationActiveFlush(FlushObservation)
	// ObserveConversationActivePressure records one bounded pressure-drain event.
	ObserveConversationActivePressure(PressureObservation)
}

// PressureSignal wakes the app-owned flush worker and preserves queue wait evidence.
type PressureSignal struct {
	// EnqueuedAt is the local process time when the coalesced wakeup entered the channel.
	EnqueuedAt time.Time
}

// CacheObservation reports current in-memory active cache pressure.
type CacheObservation struct {
	// Revision is a manager-local monotonic snapshot order used to reject delayed gauge writes.
	Revision uint64
	// Rows is the number of cached active rows across all users.
	Rows int
	// DirtyRows is the number of cached rows still waiting to flush.
	DirtyRows int
	// RowsByKind counts cached rows by conversation kind.
	RowsByKind map[metadb.ConversationKind]int
	// DirtyRowsByKind counts dirty cached rows by conversation kind.
	DirtyRowsByKind map[metadb.ConversationKind]int
	// OldestDirtyAge is the age of the oldest dirty row by ActiveAtMS.
	OldestDirtyAge time.Duration
	// PressureDraining reports whether the manager is actively draining toward the low watermark.
	PressureDraining bool
}

// MutationObservation reports how one admission batch changed dirty cache work.
type MutationObservation struct {
	// BecameDirty is the number of new or previously clean rows that became dirty.
	BecameDirty int
	// DirtyUpdated is the number of already-dirty rows advanced to a newer version.
	DirtyUpdated int
	// Unchanged is the number of existing rows whose projection and ownership were unchanged.
	Unchanged int
}

// FlushObservation reports one active dirty-row flush attempt.
type FlushObservation struct {
	// Result is a low-cardinality outcome such as ok, error, timeout, or no_dirty.
	Result string
	// FailureStage is filter or persist when Result is error or timeout.
	FailureStage string
	// Selected is the number of dirty rows selected for the attempt.
	Selected int
	// Persisted is the number of selected rows acknowledged durable after a successful whole-store call.
	// It is zero and durability is unknown when Result is error or timeout.
	Persisted int
	// Skipped is the number of selected rows routed through the cooldown no-write path after durable-state comparison.
	Skipped int
	// Cleared is the number of dirty markers actually cleared after version fencing.
	Cleared int
	// VersionConflicts is the number of dirty markers retained because a concurrent update advanced the version.
	VersionConflicts int
	// Superseded is the number of selected snapshots no longer present or dirty at clear time.
	Superseded int
	// Requeued is the number of selected dirty rows retained for retry after a version conflict or failed attempt.
	Requeued int
	// LaneWaitDuration is time spent waiting for the serialized flush lane.
	LaneWaitDuration time.Duration
	// SelectDuration is time spent selecting a bounded dirty snapshot.
	SelectDuration time.Duration
	// FilterDuration is time spent comparing receiver-only rows with durable state.
	FilterDuration time.Duration
	// PersistDuration is time spent durably writing selected patches.
	PersistDuration time.Duration
	// ClearDuration is time spent applying version-fenced dirty clears.
	ClearDuration time.Duration
	// Duration is the flush attempt latency.
	Duration time.Duration
}

// PressureObservation reports one low-cardinality pressure-drain lifecycle event.
type PressureObservation struct {
	// Event is a bounded event such as start_high_watermark, signal_sent, signal_received, requeue_progress, or stop_low_watermark.
	Event string
	// WakeupWaitDuration is populated for signal_received events.
	WakeupWaitDuration time.Duration
}

// ActiveStore reads and persists durable active conversation rows for cache/DB view merging.
type ActiveStore interface {
	// ListConversationActivePage returns active rows after the active-index cursor for one conversation kind.
	ListConversationActivePage(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	// GetConversationState returns one durable primary row for cache-only hydration.
	GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error)
	// GetConversationStates returns durable primary rows for flush-time active_at filtering.
	GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error)
	// TouchConversationActiveAt persists active-row patches produced by the runtime cache.
	// Implementations may span multiple atomic proposals; an error can leave an unknown committed prefix.
	TouchConversationActiveAt(ctx context.Context, patches []metadb.ConversationActivePatch) error
}

// FlushResult reports the outcome of one dirty active-row flush attempt.
type FlushResult struct {
	// Selected is the number of dirty rows selected from cache for this flush.
	Selected int
	// Persisted is the number of selected rows acknowledged durable after a successful whole-store call.
	// It is zero and durability is unknown when the call returns an error.
	Persisted int
	// Skipped is the number of rows routed through the cooldown no-write path.
	Skipped int
	// Cleared is the number of dirty markers actually cleared after version fencing.
	Cleared int
	// VersionConflicts is the number of dirty markers retained after concurrent updates.
	VersionConflicts int
	// Superseded is the number of selected snapshots no longer present or dirty at clear time.
	Superseded int
	// Requeued is the number of selected dirty rows retained for retry.
	Requeued int
}

// ActiveViewPage is a merged cache plus durable active-row page.
type ActiveViewPage struct {
	// Rows contains active conversations ordered by active-index order.
	Rows []metadb.ConversationState
	// Cursor identifies the last returned row for the next page request.
	Cursor metadb.ConversationActiveCursor
	// Done reports that there are no more active rows after Cursor.
	Done bool
}

// ActiveBatch is the channelappend output consumed by the active cache.
type ActiveBatch struct {
	// Kind identifies the logical conversation projection view being touched.
	Kind metadb.ConversationKind
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
	// Kind identifies the logical conversation projection view being touched.
	Kind metadb.ConversationKind
	// ChannelID identifies the active conversation channel.
	ChannelID string
	// ChannelType identifies the active conversation channel type.
	ChannelType uint8
	// ActiveAtMS is the maximum observed Unix millisecond activity timestamp.
	ActiveAtMS int64
	// ReadSeq is advanced only for the sender's own conversation row.
	ReadSeq uint64
}
