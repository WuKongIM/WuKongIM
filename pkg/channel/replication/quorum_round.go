package replication

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

var errDurableQuorumUnavailable = errors.New("channel replication: durable quorum unavailable")

// durableProposal is the immutable, already-sequenced input to one durability
// round. The owning Channel sequencer must reserve capacity and assign the
// range before constructing it.
type durableProposal struct {
	first      uint64
	last       uint64
	channelKey ch.ChannelKey
	channelID  ch.ChannelID
	leader     ch.NodeID
	manifest   ch.ProposalManifest
	records    []ch.Record
	committed  uint64
}

func (p durableProposal) freeze() durableProposal {
	p.records = append([]ch.Record(nil), p.records...)
	for index := range p.records {
		p.records[index].Payload = append([]byte(nil), p.records[index].Payload...)
	}
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

	type writeResult struct {
		local      bool
		voter      ch.NodeID
		completion durabilityCompletion
	}
	workCtx := context.WithoutCancel(ctx)
	results := make(chan writeResult, len(uniqueVoters))
	for _, voter := range uniqueVoters {
		voter := voter
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
			results <- writeResult{local: localWrite, voter: voter, completion: durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: err}}
		}
	}

	result := durableRoundResult{outcome: ch.AppendOutcomeDefinitelyNotWritten}
	conflict := false
	for pending := len(uniqueVoters); pending > 0; pending-- {
		select {
		case <-ctx.Done():
			result.outcome = ch.AppendOutcomeUnknown
			return result, ctx.Err()
		case write := <-results:
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
				}
				result.outcome = ch.AppendOutcomeUnknown
			case completion.outcome == ch.AppendOutcomeUnknown:
				result.outcome = ch.AppendOutcomeUnknown
			case completion.outcome == ch.AppendOutcomeConflict:
				conflict = true
			}
			if result.localDurable && result.durableVotes >= writeQuorum {
				result.outcome = ch.AppendOutcomeDurable
				return result, nil
			}
		}
	}
	if result.outcome != ch.AppendOutcomeUnknown && conflict {
		result.outcome = ch.AppendOutcomeConflict
	}
	return result, errDurableQuorumUnavailable
}
