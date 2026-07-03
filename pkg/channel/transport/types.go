package transport

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// Client sends v0 replication RPCs to peer nodes.
type Client interface {
	Pull(ctx context.Context, node ch.NodeID, req PullRequest) (PullResponse, error)
	Ack(ctx context.Context, node ch.NodeID, req AckRequest) error
	PullHint(ctx context.Context, node ch.NodeID, req PullHintRequest) error
	// Notify is a compatibility wrapper for older leader nudge callers.
	Notify(ctx context.Context, node ch.NodeID, req NotifyRequest) error
}

// BatchClient sends grouped v0 replication RPCs to peer nodes.
type BatchClient interface {
	PullBatch(ctx context.Context, node ch.NodeID, req PullBatchRequest) (PullBatchResponse, error)
	PullHintBatch(ctx context.Context, node ch.NodeID, req PullHintBatchRequest) (PullHintBatchResponse, error)
}

// Server handles v0 replication RPCs on a node.
type Server interface {
	HandlePull(ctx context.Context, req PullRequest) (PullResponse, error)
	HandleAck(ctx context.Context, req AckRequest) error
	HandlePullHint(ctx context.Context, req PullHintRequest) error
	// HandleNotify is a compatibility wrapper for older leader nudge callers.
	HandleNotify(ctx context.Context, req NotifyRequest) error
}

// BatchServer handles grouped v0 replication RPCs on a node.
type BatchServer interface {
	HandlePullBatch(ctx context.Context, req PullBatchRequest) (PullBatchResponse, error)
	HandlePullHintBatch(ctx context.Context, req PullHintBatchRequest) (PullHintBatchResponse, error)
}

// PullRequest asks a leader for records starting at NextOffset.
type PullRequest struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Follower    ch.NodeID
	NextOffset  uint64
	// AckOffset is the highest offset the follower has durably applied before this pull.
	AckOffset uint64
	MaxBytes  int
	// NeedMeta asks the leader to include authoritative metadata with this pull response.
	NeedMeta bool
}

// PullResponse returns leader records and the leader committed frontier.
type PullResponse struct {
	// ChannelKey identifies the replicated channel.
	ChannelKey ch.ChannelKey
	// Epoch is the channel metadata epoch observed by the leader.
	Epoch uint64
	// LeaderEpoch fences responses from an older leader term.
	LeaderEpoch uint64
	// LeaderHW is the leader committed high watermark.
	LeaderHW uint64
	// LeaderLEO is the leader log end offset.
	LeaderLEO uint64
	// ActivityVersion fences idle lifecycle decisions for this leader state.
	ActivityVersion uint64
	// NextPullAfter tells a caught-up follower when to pull again.
	NextPullAfter time.Duration
	// Control tells the follower whether to continue pulling or stop.
	Control PullControl
	// Meta carries authoritative channel metadata only for NeedMeta pull responses.
	Meta *ch.Meta
	// Records are the log entries returned to the follower.
	Records []ch.Record
}

// PullBatchRequest groups pull requests that target the same leader node.
type PullBatchRequest struct {
	Items []PullRequest
}

// PullBatchItemResult is the per-request result inside a pull batch response.
type PullBatchItemResult struct {
	Response PullResponse
	Err      error
}

// PullBatchResponse returns one item for each pull request in order.
type PullBatchResponse struct {
	Items []PullBatchItemResult
}

// AckRequest reports follower progress to the leader.
type AckRequest struct {
	// ChannelKey identifies the replicated channel.
	ChannelKey ch.ChannelKey
	// Epoch is the channel metadata epoch observed by the follower.
	Epoch uint64
	// LeaderEpoch fences acknowledgements from an older leader term.
	LeaderEpoch uint64
	// Follower is the node reporting stopped lifecycle progress.
	Follower ch.NodeID
	// MatchOffset is the highest offset the follower has durably applied.
	MatchOffset uint64
	// ActivityVersion fences stopped acknowledgements for idle eviction.
	ActivityVersion uint64
	// Stopped reports that the follower has checkpointed and unloaded runtime state.
	// When false, the request is an ordinary durable progress acknowledgement.
	Stopped bool
}

// PullHintReason explains why a leader is asking a follower to pull promptly.
type PullHintReason uint8

const (
	// PullHintReasonAppend means leader append progress should interrupt follower parking.
	PullHintReasonAppend PullHintReason = iota + 1
	// PullHintReasonResume means the leader is retrying or resuming follower pull work.
	PullHintReasonResume
)

// PullHintRequest nudges a follower to pull current leader progress without carrying records.
type PullHintRequest struct {
	// ChannelKey identifies the follower channel to wake.
	ChannelKey ch.ChannelKey
	// ChannelID is the stable channel identity for lazy activation.
	ChannelID ch.ChannelID
	// Epoch is the channel metadata epoch observed by the leader.
	Epoch uint64
	// LeaderEpoch fences hints from an older leader term.
	LeaderEpoch uint64
	// Leader is the node that should serve the next pull.
	Leader ch.NodeID
	// LeaderLEO is the leader log end offset when the hint was sent.
	LeaderLEO uint64
	// ActivityVersion fences stale hints after newer leader activity.
	ActivityVersion uint64
	// Reason explains why the hint was sent.
	Reason PullHintReason
}

// PullHintBatchRequest groups pull hints that target the same follower node.
type PullHintBatchRequest struct {
	Items []PullHintRequest
}

// PullHintBatchItemResult is the per-hint result inside a pull hint batch response.
type PullHintBatchItemResult struct {
	Err error
}

// PullHintBatchResponse returns one item for each pull hint request in order.
type PullHintBatchResponse struct {
	Items []PullHintBatchItemResult
}

// PullControl lets the leader control follower pull lifecycle.
type PullControl uint8

const (
	// PullControlContinue keeps the follower runtime loaded and pulling later.
	PullControlContinue PullControl = iota + 1
	// PullControlStop allows a caught-up follower to checkpoint and unload runtime state.
	PullControlStop
)

// NotifyRequest is the legacy compatibility form of a PullHint request.
type NotifyRequest struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Leader      ch.NodeID
	LeaderLEO   uint64
}
