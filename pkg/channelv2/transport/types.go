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
	ChannelKey      ch.ChannelKey
	Epoch           uint64
	LeaderEpoch     uint64
	LeaderHW        uint64
	LeaderLEO       uint64
	ActivityVersion uint64
	NextPullAfter   time.Duration
	Control         PullControl
	Records         []ch.Record
}

// AckRequest reports follower match progress to the leader.
type AckRequest struct {
	ChannelKey      ch.ChannelKey
	Epoch           uint64
	LeaderEpoch     uint64
	Follower        ch.NodeID
	MatchOffset     uint64
	ActivityVersion uint64
	Stopped         bool
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
	ChannelKey      ch.ChannelKey
	ChannelID       ch.ChannelID
	Epoch           uint64
	LeaderEpoch     uint64
	Leader          ch.NodeID
	LeaderLEO       uint64
	ActivityVersion uint64
	Reason          PullHintReason
}

// PullControl lets the leader control follower pull lifecycle.
type PullControl uint8

const (
	// PullControlContinue keeps the follower runtime loaded and pulling later.
	PullControlContinue PullControl = iota + 1
	// PullControlStop allows a caught-up follower to checkpoint and unload runtime state.
	PullControlStop
)

// NotifyRequest nudges a follower to pull a channel after leader append progress.
type NotifyRequest struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Leader      ch.NodeID
	LeaderLEO   uint64
}
