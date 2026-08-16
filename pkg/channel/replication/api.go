package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// AuthorityID is the complete durable write authority for one Channel
// generation. Every component is non-zero and participates in entry identity.
type AuthorityID struct {
	ChannelEpoch uint64
	LeaderTerm   uint64
	FenceVersion uint64
}

// Authority is the authoritative voter configuration installed before a
// Channel becomes writable through the durable quorum log.
type Authority struct {
	Key         ch.ChannelKey
	ChannelID   ch.ChannelID
	ID          AuthorityID
	Leader      ch.NodeID
	Voters      []ch.NodeID
	WriteQuorum int
	WriteFence  ch.WriteFence
}

// Proposal is one retry-stable immutable business append. The durable quorum
// log assigns its exact contiguous range before physical I/O.
type Proposal struct {
	Key       ch.ChannelKey
	Expected  AuthorityID
	CommandID ch.CommandID
	Records   []ch.Record
}

// Receipt proves that one exact proposal is durable on the local leader and
// an intersecting current-voter quorum.
type Receipt struct {
	Authority AuthorityID
	CommandID ch.CommandID
	First     uint64
	Last      uint64
	HW        uint64
}

// Installed is the ready frontier after quorum recovery. A non-empty frontier
// from another authority includes a quorum-durable current-term barrier; an
// empty frontier defers that proof into its first business proposal.
type Installed struct {
	Authority AuthorityID
	LEO       uint64
	HW        uint64
}

// DurableQuorumLog is the complete external Channel durability surface.
// Implementations hide sequencing, recovery, peer batching, and quorum math.
type DurableQuorumLog interface {
	Install(context.Context, Authority) (Installed, error)
	Commit(context.Context, Proposal) (Receipt, error)
}
