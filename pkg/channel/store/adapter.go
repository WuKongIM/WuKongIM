package store

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// Factory opens per-channel stores for channelv2 reactors.
type Factory interface {
	ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error)
}

// LeaderAppendBatcher optionally appends leader batches for multiple channels in one store call.
type LeaderAppendBatcher interface {
	AppendLeaderBatch(ctx context.Context, items []AppendLeaderBatchItem) []AppendLeaderBatchResult
}

// FollowerApplyBatcher optionally applies follower batches for multiple channels in one store call.
type FollowerApplyBatcher interface {
	ApplyFollowerBatch(ctx context.Context, items []ApplyFollowerBatchItem) []ApplyFollowerBatchResult
}

// ChannelCatalogLister pages the local message-store channel catalog.
type ChannelCatalogLister interface {
	ListChannelsPage(ctx context.Context, after ch.ChannelKey, limit int) ([]ChannelCatalogEntry, ch.ChannelKey, bool, error)
}

// ChannelStore is the narrow persistence contract used by channelv2.
type ChannelStore interface {
	Load(ctx context.Context) (InitialState, error)
	AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error)
	ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error)
	ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error)
	ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error)
	LoadRetentionState(ctx context.Context) (RetentionState, error)
	AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error)
	TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error)
	// StoreCheckpoint durably records checkpoint HW and must ignore regressive HW updates.
	StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error
	// Close releases resources owned by this store handle without deleting durable channel state.
	Close() error
}

// MessageLookup is an optional point lookup surface for rare timeout recovery paths.
type MessageLookup interface {
	// LookupMessageByID returns a durable row without applying any committed-HW check.
	LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error)
}

// IdempotencyLookup is an optional committed-message lookup by sender/client key.
type IdempotencyLookup interface {
	// LookupIdempotency returns the durable row and raw payload hash for one sender/client key.
	LookupIdempotency(ctx context.Context, fromUID string, clientMsgNo string) (IdempotencyHit, bool, error)
}

// IdempotencyHit is the durable message selected by an idempotency key.
type IdempotencyHit struct {
	// Message is the durable committed row selected by the sender/client key.
	Message ch.Message
	// PayloadHash is the FNV-64a hash persisted with the idempotency index.
	PayloadHash uint64
}

// InitialState is the durable state loaded before a channel becomes ready.
type InitialState struct {
	LEO          uint64
	HW           uint64
	CheckpointHW uint64
}

// RetentionState records local retention progress for one channel store.
type RetentionState struct {
	// LocalRetentionThroughSeq is the adopted logical retention boundary.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest physically deleted sequence.
	PhysicalRetentionThroughSeq uint64
	// RetainedMaxSeq preserves LEO when all rows at the tail are trimmed.
	RetainedMaxSeq uint64
}

// ChannelCatalogEntry describes one locally known channel in the message store.
type ChannelCatalogEntry struct {
	// Key is the stable channel partition key.
	Key ch.ChannelKey
	// ID is the user-facing channel identity.
	ID ch.ChannelID
}

// RetentionTrimOptions bounds one physical retention trim.
type RetentionTrimOptions struct {
	// MaxMessages caps deleted messages when positive.
	MaxMessages int
	// MaxBytes caps deleted payload bytes when positive.
	MaxBytes int
}

// RetentionTrimResult describes one bounded physical retention trim.
type RetentionTrimResult struct {
	// DeletedThroughSeq is the highest sequence deleted by this trim.
	DeletedThroughSeq uint64
	// Deleted is the number of message rows deleted.
	Deleted int
	// More reports whether another trim may still find rows below the boundary.
	More bool
}

// AppendLeaderRequest persists a leader-owned continuous record batch.
type AppendLeaderRequest struct {
	Records []ch.Record
	Sync    bool
}

// AppendLeaderBatchItem is one channel-scoped leader append inside a store-level batch.
type AppendLeaderBatchItem struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Request    AppendLeaderRequest
}

// AppendLeaderResult returns the durable offset range for a leader append.
type AppendLeaderResult struct {
	BaseOffset uint64
	LastOffset uint64
}

// AppendLeaderBatchResult returns the result for one AppendLeaderBatchItem.
type AppendLeaderBatchResult struct {
	BaseOffset uint64
	LastOffset uint64
	Err        error
}

// ApplyFollowerRequest persists records received from the leader.
type ApplyFollowerRequest struct {
	Records  []ch.Record
	LeaderHW uint64
}

// ApplyFollowerBatchItem is one channel-scoped follower apply inside a store-level batch.
type ApplyFollowerBatchItem struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Request    ApplyFollowerRequest
}

// ApplyFollowerResult returns the follower's durable log end offset.
type ApplyFollowerResult struct {
	LEO uint64
}

// ApplyFollowerBatchResult returns the result for one ApplyFollowerBatchItem.
type ApplyFollowerBatchResult struct {
	LEO uint64
	Err error
}

// ReadCommittedRequest reads client-visible messages up to MaxSeq.
type ReadCommittedRequest struct {
	FromSeq uint64
	MaxSeq  uint64
	// MinSeq is the lowest visible message sequence for logical compaction.
	MinSeq   uint64
	Limit    int
	MaxBytes int
	// Reverse reads messages at or before FromSeq in descending sequence order.
	Reverse bool
}

// ReadCommittedResult contains committed messages from storage.
type ReadCommittedResult struct {
	Messages []ch.Message
	NextSeq  uint64
}

// ReadLogRequest reads raw log records for replication.
type ReadLogRequest struct {
	FromOffset uint64
	MaxOffset  uint64
	MaxBytes   int
}

// ReadLogResult contains raw log records for follower catch-up.
type ReadLogResult struct {
	Records []ch.Record
}
