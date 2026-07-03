package channelplane

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

// RouteEpoch identifies one authoritative routing version for a channel.
type RouteEpoch struct {
	// RouteGeneration is the authoritative version of the full write route.
	RouteGeneration uint64
	// ChannelEpoch fences writes to the channel replica-group epoch.
	ChannelEpoch uint64
	// LeaderEpoch fences writes to the current channel leader epoch.
	LeaderEpoch uint64
}

// ChannelRoute is the channel plane's write-routing view for one channel.
type ChannelRoute struct {
	// ChannelID identifies the channel this route belongs to.
	ChannelID channel.ChannelID
	// Leader is the current authoritative channel leader node.
	Leader channel.NodeID
	// RouteGeneration is the authoritative version of the full write route.
	RouteGeneration uint64
	// ChannelEpoch fences writes to the channel replica-group epoch.
	ChannelEpoch uint64
	// LeaderEpoch fences writes to the current channel leader epoch.
	LeaderEpoch uint64
	// Replicas is the current replica set.
	Replicas []channel.NodeID
	// ISR is the current in-sync replica set.
	ISR []channel.NodeID
	// LeaseUntil is the leader lease deadline observed with this route.
	LeaseUntil time.Time
	// Status is the authoritative channel lifecycle status.
	Status channel.Status
}

// Epoch returns the compact route epoch carried on append envelopes.
func (r ChannelRoute) Epoch() RouteEpoch {
	return RouteEpoch{RouteGeneration: r.RouteGeneration, ChannelEpoch: r.ChannelEpoch, LeaderEpoch: r.LeaderEpoch}
}

// IsLocal reports whether this node owns the current channel leader append effect.
func (r ChannelRoute) IsLocal(localNode channel.NodeID) bool {
	return r.Leader != 0 && r.Leader == localNode
}

func (r ChannelRoute) applyTo(req channel.AppendBatchRequest) channel.AppendBatchRequest {
	req.ExpectedChannelEpoch = r.ChannelEpoch
	req.ExpectedLeaderEpoch = r.LeaderEpoch
	return req
}
