package replication

import (
	"context"
	"errors"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type roundWriteMode uint8

const (
	roundWriteDirect roundWriteMode = iota
	roundWriteHedge
	roundWriteDeferred
)

type roundWrite struct {
	voter ch.NodeID
	mode  roundWriteMode
}

// durableRound advances only on completion, cancellation or its single hedge
// timer. Callers must retain bounded admission until the terminal callback returns.
// It creates no goroutine that waits for an individual proposal's durability.
type durableRound struct {
	// mu protects protocol progress, callback ownership and cancellation fences.
	mu sync.Mutex
	// ctx controls the caller result; workCtx keeps admitted writes independent.
	ctx, workCtx          context.Context
	local                 ch.NodeID
	quorum                int
	proposal              durableProposal
	dispatcher            durabilityDispatcher
	followers             []ch.NodeID
	nextFollower, pending int
	// completed admits at most one proof per voter.
	completed             map[ch.NodeID]bool
	result                durableRoundResult
	conflict, localFailed bool
	// dispatching fences callbacks against submissions that have been planned but
	// have not yet transferred ownership to the local/peer dispatcher.
	dispatching                   int
	starting, terminal, delivered bool
	terminalErr                   error
	// complete is published outside mu after every planned dispatch returns.
	complete func(durableRoundResult, error)
	// Terminal publication stops both bounded asynchronous wakeup sources.
	hedgeStopped bool
	hedge        *time.Timer
	stopCancel   func() bool
}

// startDurableRound returns after initial dispatch, transferring exactly one
// terminal callback on nil error. Admission rejection invokes no callback.
func startDurableRound(ctx context.Context, local ch.NodeID, voters []ch.NodeID, quorum int, proposal durableProposal, dispatcher durabilityDispatcher, complete func(durableRoundResult, error)) error {
	if ctx == nil || dispatcher == nil || complete == nil || local == 0 || quorum <= 0 {
		return ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	seen := make(map[ch.NodeID]bool, len(voters))
	followers := make([]ch.NodeID, 0, len(voters))
	for _, v := range voters {
		if v == 0 || seen[v] {
			return ch.ErrInvalidConfig
		}
		seen[v] = true
		if v != local {
			followers = append(followers, v)
		}
	}
	if !seen[local] || quorum > len(seen) {
		return ch.ErrInvalidConfig
	}
	if len(followers) > 1 {
		start := preferredFollowerIndex(proposal.channelKey, len(followers))
		followers = append(append(make([]ch.NodeID, 0, len(followers)), followers[start:]...), followers[:start]...)
	}
	r := &durableRound{ctx: ctx, workCtx: context.WithoutCancel(ctx), local: local, quorum: quorum, proposal: proposal.freeze(), dispatcher: dispatcher, followers: followers, completed: make(map[ch.NodeID]bool, len(voters)), result: durableRoundResult{outcome: ch.AppendOutcomeDefinitelyNotWritten}, starting: true, complete: complete}
	diagnosticProposal("round_begin", r.proposal.manifest, r.proposal.records, 0, 0, 0)
	writes := []roundWrite{r.reserveLocked(local, roundWriteDirect)}
	for r.nextFollower < len(followers) && r.nextFollower < quorum-1 {
		writes = append(writes, r.reserveLocked(followers[r.nextFollower], roundWriteDirect))
		r.nextFollower++
	}
	r.dispatch(writes)
	r.mu.Lock()
	var hedgeNow bool
	if !r.terminal {
		r.stopCancel = context.AfterFunc(ctx, r.cancel)
		if h, ok := dispatcher.(hedgedReplicaDispatcher); ok && !r.hedgeStopped && r.nextFollower < len(followers) {
			if delay := h.replicaHedgeDelay(); delay > 0 {
				r.hedge = time.AfterFunc(delay, r.onHedge)
			} else {
				hedgeNow = true
			}
		}
	}
	r.starting = false
	finish := r.finishLocked()
	r.mu.Unlock()
	if finish != nil {
		finish()
	}
	if hedgeNow {
		r.onHedge()
	}
	return nil
}

func (r *durableRound) reserveLocked(voter ch.NodeID, mode roundWriteMode) roundWrite {
	r.pending++
	r.dispatching++
	return roundWrite{voter: voter, mode: mode}
}
func (r *durableRound) stopHedgeLocked() {
	r.hedgeStopped = true
	if r.hedge != nil {
		r.hedge.Stop()
	}
}
func (r *durableRound) remainingLocked(mode roundWriteMode) []roundWrite {
	r.stopHedgeLocked()
	writes := make([]roundWrite, 0, len(r.followers)-r.nextFollower)
	for r.nextFollower < len(r.followers) {
		writes = append(writes, r.reserveLocked(r.followers[r.nextFollower], mode))
		r.nextFollower++
	}
	return writes
}

// dispatch never holds the round mutex across dependency or terminal callbacks;
// inline completions can therefore advance the same state machine safely.
func (r *durableRound) dispatch(writes []roundWrite) {
	for _, write := range writes {
		done := func(c durabilityCompletion) { r.onResult(write.voter, c) }
		err := r.submit(write, done)
		if err != nil {
			done(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: err})
		}
		r.mu.Lock()
		r.dispatching--
		finish := r.finishLocked()
		r.mu.Unlock()
		if finish != nil {
			finish()
		}
	}
}

// submit transfers one write to its dispatcher. A dispatcher panic leaves
// ownership ambiguous, so it becomes an unknown voter result, never a proof.
func (r *durableRound) submit(write roundWrite, done func(durabilityCompletion)) (err error) {
	defer func() {
		if recover() != nil {
			err = nil
			done(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errPeerOutcomeUnknown})
		}
	}()
	{
		switch {
		case write.voter == r.local:
			err = r.dispatcher.submitLocal(r.workCtx, r.proposal, done)
		case write.mode == roundWriteHedge:
			err = r.dispatcher.(hedgedReplicaDispatcher).submitReplicaHedged(r.workCtx, write.voter, r.proposal, done)
		case write.mode == roundWriteDeferred:
			if d, ok := r.dispatcher.(deferredReplicaDispatcher); ok {
				err = d.submitReplicaDeferred(r.workCtx, write.voter, r.proposal, func(durabilityCompletion) {})
			} else {
				err = r.dispatcher.submitReplica(r.workCtx, write.voter, r.proposal, done)
			}
		default:
			err = r.dispatcher.submitReplica(r.workCtx, write.voter, r.proposal, done)
		}
	}
	return err
}

func (r *durableRound) onHedge() {
	r.mu.Lock()
	if r.terminal || r.hedgeStopped || r.nextFollower >= len(r.followers) {
		r.mu.Unlock()
		return
	}
	r.stopHedgeLocked()
	write := r.reserveLocked(r.followers[r.nextFollower], roundWriteHedge)
	r.nextFollower++
	r.mu.Unlock()
	r.dispatch([]roundWrite{write})
}
func (r *durableRound) cancel() {
	r.mu.Lock()
	if r.terminal {
		r.mu.Unlock()
		return
	}
	writes := r.remainingLocked(roundWriteDirect)
	r.result.outcome = ch.AppendOutcomeUnknown
	r.terminal = true
	r.terminalErr = r.ctx.Err()
	finish := r.finishLocked()
	r.mu.Unlock()
	r.dispatch(writes)
	if finish != nil {
		finish()
	}
}

// onResult validates each distinct voter proof before advancing quorum. Late or
// repeated callbacks cannot increase the vote count or publish another result.
func (r *durableRound) onResult(voter ch.NodeID, c durabilityCompletion) {
	r.mu.Lock()
	if r.terminal || r.completed[voter] {
		r.mu.Unlock()
		return
	}
	r.completed[voter] = true
	r.pending--
	local := voter == r.local
	repair := c.follower != 0 || c.needFrom != 0
	validRepair := repair && !local && c.follower == voter && c.needFrom > 0 && c.outcome == ch.AppendOutcomeDefinitelyNotWritten && errors.Is(c.err, errReplicaNeedsRepair)
	if !c.outcome.Valid() || (c.outcome.Durable() && c.err != nil) || (!c.outcome.Durable() && c.err == nil) || (repair && !validRepair) {
		c = durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errPeerOutcomeUnknown}
	} else if validRepair {
		r.result.repairs = append(r.result.repairs, followerRepairFor(r.proposal, c.follower, c.needFrom))
	}
	diagnosticRound("vote", r.proposal, voter, c.outcome, r.result, r.quorum, c.err)
	var writes []roundWrite
	if local && c.outcome == ch.AppendOutcomeConflict {
		r.result.outcome = ch.AppendOutcomeConflict
		r.terminal = true
		r.terminalErr = ch.ErrLogConflict
	} else {
		switch {
		case c.outcome.Durable():
			r.result.durableVotes++
			if local {
				r.result.localDurable = true
			} else {
				r.stopHedgeLocked()
			}
			r.result.outcome = ch.AppendOutcomeUnknown
		case c.outcome == ch.AppendOutcomeUnknown:
			r.result.outcome = ch.AppendOutcomeUnknown
		case c.outcome == ch.AppendOutcomeConflict:
			r.conflict = true
		}
		switch {
		case r.result.localDurable && r.result.durableVotes >= r.quorum:
			writes = r.remainingLocked(roundWriteDeferred)
			r.result.outcome = ch.AppendOutcomeDurable
			r.terminal = true
		case local && !c.outcome.Durable():
			r.localFailed = true
			writes = r.remainingLocked(roundWriteDirect)
		case !r.localFailed && !local && !c.outcome.Durable() && r.nextFollower < len(r.followers):
			r.stopHedgeLocked()
			writes = []roundWrite{r.reserveLocked(r.followers[r.nextFollower], roundWriteDirect)}
			r.nextFollower++
		}
		if !r.terminal && r.pending == 0 {
			if r.result.outcome != ch.AppendOutcomeUnknown && r.conflict {
				r.result.outcome = ch.AppendOutcomeConflict
			}
			r.terminal = true
			r.terminalErr = errDurableQuorumUnavailable
		}
	}
	finish := r.finishLocked()
	r.mu.Unlock()
	r.dispatch(writes)
	if finish != nil {
		finish()
	}
}

// finishLocked transfers terminal publication once every planned submission has
// crossed the dispatcher boundary. Dependency completions may still arrive late.
func (r *durableRound) finishLocked() func() {
	if !r.terminal || r.delivered || r.starting || r.dispatching != 0 {
		return nil
	}
	r.delivered = true
	r.stopHedgeLocked()
	if r.stopCancel != nil {
		r.stopCancel()
	}
	result, err, complete := r.result, r.terminalErr, r.complete
	return func() {
		diagnosticRound("round_terminal", r.proposal, 0, result.outcome, result, r.quorum, err)
		complete(result, err)
	}
}
