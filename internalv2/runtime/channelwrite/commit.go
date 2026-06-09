package channelwrite

import (
	"context"
	"fmt"
)

type commitPorts struct {
	subscribers                SubscriberSource
	recipientAuthorityResolver RecipientAuthorityResolver
	recipientRouter            RecipientAuthorityRouter
	subscriberPageSize         int
	recipientBatchSize         int
	retryMaxAttempts           int
}

type commitEffect struct {
	key     string
	seq     uint64
	attempt int
	event   CommittedEnvelope
}

type commitCompletedEvent struct {
	key     string
	seq     uint64
	attempt int
	err     error
}

func (e commitEffect) run(runtimeCtx context.Context, ports commitPorts) commitCompletedEvent {
	err := dispatchCommittedRecipients(runtimeCtx, e.event, ports)
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrCommitEffectFailed, err)
	}
	return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, err: err}
}

func (e commitCompletedEvent) apply(r *reactor) {
	r.recordCommitCompletion(e)
}

func (r *reactor) recordCommitCompletion(event commitCompletedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.states[event.key]
	if state == nil {
		return
	}
	if event.err == nil {
		state.finishCommitSuccess()
		r.scheduleCommitLocked(event.key, state)
		return
	}
	state.finishCommitFailure()
	if r.stopCtx.Err() != nil {
		return
	}
	if event.attempt >= boundedPositive(r.commitPorts.retryMaxAttempts, defaultCommitRetryMaxAttempts) {
		state.dropCurrentCommit()
	}
	r.scheduleCommitLocked(event.key, state)
}
