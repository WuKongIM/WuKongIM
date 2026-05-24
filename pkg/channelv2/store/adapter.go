package store

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// Factory opens per-channel stores for channelv2 reactors.
type Factory interface {
	ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error)
}

// ChannelStore is the narrow persistence contract used by channelv2.
type ChannelStore interface {
	Load(ctx context.Context) (InitialState, error)
	AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error)
	ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error)
	ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error)
	ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error)
	// StoreCheckpoint durably records checkpoint HW and must ignore regressive HW updates.
	StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error
	// Close releases resources owned by this store handle without deleting durable channel state.
	Close() error
}

// InitialState is the durable state loaded before a channel becomes ready.
type InitialState struct {
	LEO          uint64
	HW           uint64
	CheckpointHW uint64
}

// AppendLeaderRequest persists a leader-owned continuous record batch.
type AppendLeaderRequest struct {
	Records []ch.Record
	Sync    bool
}

// AppendLeaderResult returns the durable offset range for a leader append.
type AppendLeaderResult struct {
	BaseOffset uint64
	LastOffset uint64
}

// ApplyFollowerRequest persists records received from the leader.
type ApplyFollowerRequest struct {
	Records  []ch.Record
	LeaderHW uint64
}

// ApplyFollowerResult returns the follower's durable log end offset.
type ApplyFollowerResult struct {
	LEO uint64
}

// ReadCommittedRequest reads client-visible messages up to MaxSeq.
type ReadCommittedRequest struct {
	FromSeq  uint64
	MaxSeq   uint64
	Limit    int
	MaxBytes int
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
