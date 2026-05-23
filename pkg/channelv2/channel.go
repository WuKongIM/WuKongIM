package channelv2

import (
	"context"
	"time"
)

// Cluster is the experimental channelv2 facade.
type Cluster interface {
	ApplyMeta(Meta) error
	Append(context.Context, AppendRequest) (AppendResult, error)
	AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
	Fetch(context.Context, FetchRequest) (FetchResult, error)
	Tick(context.Context) error
	Close() error
}

// MetaResolver loads authoritative metadata for channels that are not yet active in a reactor.
type MetaResolver interface {
	ResolveChannelMeta(context.Context, ChannelID) (Meta, error)
}

// Config contains construction dependencies for the v0 service facade.
type Config struct {
	LocalNode    NodeID
	ReactorCount int
	MailboxSize  int
	Store        any
	Transport    any
	// MetaResolver lazily loads metadata when Append reaches an unloaded channel.
	MetaResolver any
	// Observer carries a reactor metrics observer for adapters that construct the service facade.
	Observer any
	// AppendBatchMaxRecords is the queued record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxBytes is the queued payload byte budget that triggers a store append flush.
	AppendBatchMaxBytes int
	// AppendBatchMaxWait is the maximum age of the oldest queued append before flushing.
	AppendBatchMaxWait time.Duration
	// AppendQueueMaxRequests bounds accepted append requests waiting per channel.
	AppendQueueMaxRequests int
	// AppendQueueMaxBytes bounds accepted append payload bytes waiting per channel.
	AppendQueueMaxBytes int
	// AppendStoreRetryBackoff delays retry after the store append worker pool rejects a batch.
	AppendStoreRetryBackoff time.Duration
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 10ms.
	ReplicationIdlePollInterval time.Duration
	// ReplicationMinBackoff is the first retry delay after pull, apply, or ack failures; defaults to 1ms.
	ReplicationMinBackoff time.Duration
	// ReplicationMaxBackoff caps follower replication retry delays after repeated failures; defaults to 100ms.
	ReplicationMaxBackoff time.Duration
	// PullMaxBytes bounds one follower pull response requested from the leader; defaults to 64 KiB.
	PullMaxBytes int
}
