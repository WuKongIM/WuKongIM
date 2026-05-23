package transport

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// Client sends v0 replication RPCs to peer nodes.
type Client interface {
	Pull(ctx context.Context, node ch.NodeID, req PullRequest) (PullResponse, error)
	Ack(ctx context.Context, node ch.NodeID, req AckRequest) error
	Notify(ctx context.Context, node ch.NodeID, req NotifyRequest) error
}

// Server handles v0 replication RPCs on a node.
type Server interface {
	HandlePull(ctx context.Context, req PullRequest) (PullResponse, error)
	HandleAck(ctx context.Context, req AckRequest) error
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
	ChannelKey  ch.ChannelKey
	Epoch       uint64
	LeaderEpoch uint64
	LeaderHW    uint64
	LeaderLEO   uint64
	Records     []ch.Record
}

// AckRequest reports follower match progress to the leader.
type AckRequest struct {
	ChannelKey  ch.ChannelKey
	Epoch       uint64
	LeaderEpoch uint64
	Follower    ch.NodeID
	MatchOffset uint64
}

// NotifyRequest nudges a follower to pull a channel after leader append progress.
type NotifyRequest struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Leader      ch.NodeID
	LeaderLEO   uint64
}
