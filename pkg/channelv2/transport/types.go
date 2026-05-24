package transport

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// Client sends v0 replication RPCs to peer nodes.
type Client interface {
	Pull(ctx context.Context, node ch.NodeID, req PullRequest) (PullResponse, error)
	Ack(ctx context.Context, node ch.NodeID, req AckRequest) error
	PullHint(ctx context.Context, node ch.NodeID, req PullHintRequest) error
	// Notify is a compatibility wrapper for older leader nudge callers.
	Notify(ctx context.Context, node ch.NodeID, req NotifyRequest) error
}

// Server handles v0 replication RPCs on a node.
type Server interface {
	HandlePull(ctx context.Context, req PullRequest) (PullResponse, error)
	HandleAck(ctx context.Context, req AckRequest) error
	HandlePullHint(ctx context.Context, req PullHintRequest) error
	// HandleNotify is a compatibility wrapper for older leader nudge callers.
	HandleNotify(ctx context.Context, req NotifyRequest) error
}

// PullRequest asks a leader for records starting at NextOffset.
type PullRequest struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Follower    ch.NodeID
	NextOffset  uint64
	MaxBytes    int
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
	// Records are the log entries returned to the follower.
	Records []ch.Record
}

// AckRequest reports follower match progress to the leader.
type AckRequest struct {
	// ChannelKey identifies the replicated channel.
	ChannelKey ch.ChannelKey
	// Epoch is the channel metadata epoch observed by the follower.
	Epoch uint64
	// LeaderEpoch fences acknowledgements from an older leader term.
	LeaderEpoch uint64
	// Follower is the node reporting progress.
	Follower ch.NodeID
	// MatchOffset is the highest follower offset known to match the leader.
	MatchOffset uint64
	// ActivityVersion fences stopped acknowledgements for idle eviction.
	ActivityVersion uint64
	// Stopped reports that the follower has checkpointed and unloaded runtime state.
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
