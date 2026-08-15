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
	first uint64
	last  uint64
}

// durabilityDispatcher is the internal adapter seam for bounded local storage
// and owned peer worker admission. A nil submit error transfers exactly one
// completion callback to the dispatcher; a non-nil error transfers no work.
type durabilityDispatcher interface {
	submitLocal(context.Context, durableProposal, func(error)) error
	submitReplica(context.Context, ch.NodeID, durableProposal, func(error)) error
}

type durableRoundResult struct {
	localDurable bool
	durableVotes int
}

// runDurableRound persists one immutable proposal locally and on a write
// quorum. The caller owns bounded admission before entering this function.
func runDurableRound(ctx context.Context, local ch.NodeID, voters []ch.NodeID, writeQuorum int, proposal durableProposal, dispatcher durabilityDispatcher) (durableRoundResult, error) {
	if dispatcher == nil || local == 0 || writeQuorum <= 0 {
		return durableRoundResult{}, ch.ErrInvalidConfig
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

	type writeResult struct {
		local bool
		err   error
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan writeResult, len(uniqueVoters))
	for _, voter := range uniqueVoters {
		localWrite := voter == local
		complete := func(err error) {
			results <- writeResult{local: localWrite, err: err}
		}
		var err error
		if localWrite {
			err = dispatcher.submitLocal(workCtx, proposal, complete)
		} else {
			err = dispatcher.submitReplica(workCtx, voter, proposal, complete)
		}
		if err != nil {
			results <- writeResult{local: localWrite, err: err}
		}
	}

	result := durableRoundResult{}
	for pending := len(uniqueVoters); pending > 0; pending-- {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case write := <-results:
			if write.err != nil {
				continue
			}
			result.durableVotes++
			if write.local {
				result.localDurable = true
			}
			if result.localDurable && result.durableVotes >= writeQuorum {
				return result, nil
			}
		}
	}
	return result, errDurableQuorumUnavailable
}
