package replication

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

var errDurableQuorumUnavailable = errors.New("channel replication: durable quorum unavailable")

// durableProposal is the immutable, already-sequenced input to one durability
// round. The owning Channel sequencer must reserve capacity and assign the
// range before constructing it.
type durableProposal struct {
	first                     uint64
	last                      uint64
	channelKey                ch.ChannelKey
	channelID                 ch.ChannelID
	leader                    ch.NodeID
	manifest                  ch.ProposalManifest
	records                   []ch.Record
	committed                 uint64
	payloadsImmutable         bool
	serverAllocatedMessageIDs bool
}

func (p durableProposal) freeze() durableProposal {
	if p.payloadsImmutable {
		return p
	}
	p.records = append([]ch.Record(nil), p.records...)
	for index := range p.records {
		p.records[index].Payload = append([]byte(nil), p.records[index].Payload...)
	}
	p.payloadsImmutable = true
	return p
}

type durabilityCompletion struct {
	outcome  ch.AppendOutcome
	err      error
	follower ch.NodeID
	needFrom uint64
}

// followerRepair is bounded exact evidence for one replica gap; it deliberately
// carries no record payload.
type followerRepair struct {
	channelKey ch.ChannelKey
	channelID  ch.ChannelID
	leader     ch.NodeID
	manifest   ch.ProposalManifest
	follower   ch.NodeID
	needFrom   uint64
	// committed is independently quorum-proven, even before local HW checkpointing.
	committed uint64
}

func followerRepairFor(proposal durableProposal, follower ch.NodeID, needFrom uint64) followerRepair {
	return followerRepair{
		channelKey: proposal.channelKey,
		channelID:  proposal.channelID,
		leader:     proposal.leader,
		manifest:   proposal.manifest,
		follower:   follower,
		needFrom:   needFrom,
	}
}

// durabilityDispatcher is the internal adapter seam for bounded local storage
// and owned peer worker admission. A nil submit error transfers exactly one
// completion callback to the dispatcher; a non-nil error transfers no work.
type durabilityDispatcher interface {
	submitLocal(context.Context, durableProposal, func(durabilityCompletion)) error
	submitReplica(context.Context, ch.NodeID, durableProposal, func(durabilityCompletion)) error
}

// hedgedReplicaDispatcher admits the trailing follower on the foreground path
// only after the preferred follower exceeds the configured hedge delay.
// Implementations own that completion after the quorum round returns and must
// retain repair evidence for a late non-durable outcome instead of relying on
// the round's result channel.
type hedgedReplicaDispatcher interface {
	replicaHedgeDelay() time.Duration
	submitReplicaHedged(context.Context, ch.NodeID, durableProposal, func(durabilityCompletion)) error
}

// deferredReplicaDispatcher owns non-quorum follower convergence after the
// foreground write quorum is durable. Admission remains bounded and the
// dispatcher must arrange repair evidence for any asynchronous failure.
type deferredReplicaDispatcher interface {
	submitReplicaDeferred(context.Context, ch.NodeID, durableProposal, func(durabilityCompletion)) error
}

type durableRoundResult struct {
	localDurable bool
	durableVotes int
	outcome      ch.AppendOutcome
	repairs      []followerRepair
}

// runDurableRound persists one immutable proposal locally and on a write
// quorum. The caller owns bounded admission before entering this function.
func runDurableRound(ctx context.Context, local ch.NodeID, voters []ch.NodeID, writeQuorum int, proposal durableProposal, dispatcher durabilityDispatcher) (durableRoundResult, error) {
	type completion struct {
		result durableRoundResult
		err    error
	}
	done := make(chan completion, 1)
	if err := startDurableRound(ctx, local, voters, writeQuorum, proposal, dispatcher, func(result durableRoundResult, err error) { done <- completion{result, err} }); err != nil {
		result := durableRoundResult{}
		if ctx != nil && ctx.Err() != nil && err == ctx.Err() {
			result.outcome = ch.AppendOutcomeDefinitelyNotWritten
		}
		return result, err
	}
	result := <-done
	return result.result, result.err
}

func preferredFollowerIndex(key ch.ChannelKey, followers int) int {
	if key == "" || followers <= 1 {
		return 0
	}
	const offset32 = uint32(2166136261)
	const prime32 = uint32(16777619)
	hash := offset32
	for index := 0; index < len(key); index++ {
		hash ^= uint32(key[index])
		hash *= prime32
	}
	return int(hash % uint32(followers))
}
