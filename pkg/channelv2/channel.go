package channelv2

import "context"

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
}
