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

// Config contains construction dependencies for the v0 service facade.
type Config struct {
	LocalNode    NodeID
	ReactorCount int
	MailboxSize  int
	Store        any
	Transport    any
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
}
