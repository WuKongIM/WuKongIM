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

// followerRepair is bounded exact evidence for one voter gap; it deliberately
// carries no record payload.
type followerRepair struct {
	channelKey ch.ChannelKey
	channelID  ch.ChannelID
	leader     ch.NodeID
	manifest   ch.ProposalManifest
	follower   ch.NodeID
	needFrom   uint64
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
	if ctx == nil || dispatcher == nil || local == 0 || writeQuorum <= 0 {
		return durableRoundResult{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return durableRoundResult{outcome: ch.AppendOutcomeDefinitelyNotWritten}, err
	}
	uniqueVoters := make([]ch.NodeID, 0, len(voters))
	seen := make(map[ch.NodeID]struct{}, len(voters))
	localMember := false
	for _, voter := range voters {
		if voter == 0 {
			return durableRoundResult{}, ch.ErrInvalidConfig
		}
		if _, exists := seen[voter]; exists {
			return durableRoundResult{}, ch.ErrInvalidConfig
		}
		seen[voter] = struct{}{}
		uniqueVoters = append(uniqueVoters, voter)
		if voter == local {
			localMember = true
		}
	}
	if !localMember || writeQuorum > len(uniqueVoters) {
		return durableRoundResult{}, ch.ErrInvalidConfig
	}
	proposal = proposal.freeze()
	followers := make([]ch.NodeID, 0, len(uniqueVoters)-1)
	for _, voter := range uniqueVoters {
		if voter != local {
			followers = append(followers, voter)
		}
	}
	if len(followers) > 1 {
		start := preferredFollowerIndex(proposal.channelKey, len(followers))
		followers = append(append(make([]ch.NodeID, 0, len(followers)), followers[start:]...), followers[:start]...)
	}

	type writeResult struct {
		local      bool
		voter      ch.NodeID
		completion durabilityCompletion
	}
	workCtx := context.WithoutCancel(ctx)
	results := make(chan writeResult, len(uniqueVoters))
	pending := 0
	submit := func(voter ch.NodeID) {
		localWrite := voter == local
		complete := func(completion durabilityCompletion) {
			results <- writeResult{local: localWrite, voter: voter, completion: completion}
		}
		var err error
		if localWrite {
			err = dispatcher.submitLocal(workCtx, proposal, complete)
		} else {
			err = dispatcher.submitReplica(workCtx, voter, proposal, complete)
		}
		if err != nil {
			completion := durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: err}
			results <- writeResult{local: localWrite, voter: voter, completion: completion}
		}
		pending++
	}
	submit(local)
	nextFollower := 0
	for nextFollower < len(followers) && nextFollower < writeQuorum-1 {
		submit(followers[nextFollower])
		nextFollower++
	}
	var hedgeTimer *time.Timer
	var hedgeReady <-chan time.Time
	hedged, hedgeAvailable := dispatcher.(hedgedReplicaDispatcher)
	stopHedge := func() {
		if hedgeTimer != nil {
			hedgeTimer.Stop()
		}
		hedgeReady = nil
	}
	submitHedge := func() {
		if !hedgeAvailable || nextFollower >= len(followers) {
			stopHedge()
			return
		}
		stopHedge()
		follower := followers[nextFollower]
		nextFollower++
		complete := func(completion durabilityCompletion) {
			results <- writeResult{voter: follower, completion: completion}
		}
		if err := hedged.submitReplicaHedged(workCtx, follower, proposal, complete); err != nil {
			results <- writeResult{voter: follower, completion: durabilityCompletion{
				outcome: ch.AppendOutcomeDefinitelyNotWritten, err: err,
			}}
		}
		pending++
	}
	if hedgeAvailable && nextFollower < len(followers) {
		delay := hedged.replicaHedgeDelay()
		if delay <= 0 {
			submitHedge()
		} else {
			hedgeTimer = time.NewTimer(delay)
			hedgeReady = hedgeTimer.C
			defer stopHedge()
		}
	}
	submitRemaining := func() {
		stopHedge()
		for nextFollower < len(followers) {
			submit(followers[nextFollower])
			nextFollower++
		}
	}
	submitRemainingDeferred := func() {
		stopHedge()
		deferred, ok := dispatcher.(deferredReplicaDispatcher)
		if !ok {
			submitRemaining()
			return
		}
		for nextFollower < len(followers) {
			follower := followers[nextFollower]
			nextFollower++
			_ = deferred.submitReplicaDeferred(workCtx, follower, proposal, func(durabilityCompletion) {})
		}
	}

	result := durableRoundResult{outcome: ch.AppendOutcomeDefinitelyNotWritten}
	conflict := false
	localFailed := false
	for pending > 0 {
		select {
		case <-ctx.Done():
			submitRemaining()
			result.outcome = ch.AppendOutcomeUnknown
			return result, ctx.Err()
		case <-hedgeReady:
			submitHedge()
		case write := <-results:
			pending--
			completion := write.completion
			repair := completion.follower != 0 || completion.needFrom != 0
			validRepair := repair && !write.local && completion.follower == write.voter && completion.needFrom > 0 &&
				completion.outcome == ch.AppendOutcomeDefinitelyNotWritten && errors.Is(completion.err, errReplicaNeedsRepair)
			if !completion.outcome.Valid() || (completion.outcome.Durable() && completion.err != nil) || (!completion.outcome.Durable() && completion.err == nil) || (repair && !validRepair) {
				completion = durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errPeerOutcomeUnknown}
			} else if validRepair {
				result.repairs = append(result.repairs, followerRepairFor(proposal, completion.follower, completion.needFrom))
			}
			switch {
			case completion.outcome.Durable():
				result.durableVotes++
				if write.local {
					result.localDurable = true
				} else {
					stopHedge()
				}
				result.outcome = ch.AppendOutcomeUnknown
			case completion.outcome == ch.AppendOutcomeUnknown:
				result.outcome = ch.AppendOutcomeUnknown
			case completion.outcome == ch.AppendOutcomeConflict:
				conflict = true
			}
			if result.localDurable && result.durableVotes >= writeQuorum {
				submitRemainingDeferred()
				result.outcome = ch.AppendOutcomeDurable
				return result, nil
			}
			if write.local && !completion.outcome.Durable() {
				localFailed = true
				submitRemaining()
				continue
			}
			if !localFailed && !write.local && !completion.outcome.Durable() && nextFollower < len(followers) {
				stopHedge()
				submit(followers[nextFollower])
				nextFollower++
			}
		}
	}
	if result.outcome != ch.AppendOutcomeUnknown && conflict {
		result.outcome = ch.AppendOutcomeConflict
	}
	return result, errDurableQuorumUnavailable
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
