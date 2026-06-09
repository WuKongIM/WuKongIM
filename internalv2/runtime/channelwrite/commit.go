package channelwrite

import (
	"context"
	"fmt"
)

type commitPorts struct {
	subscribers                SubscriberSource
	recipientAuthorityResolver RecipientAuthorityResolver
	recipientRouter            RecipientAuthorityRouter
	cursorStore                CursorStore
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
	key           string
	seq           uint64
	attempt       int
	err           error
	checkpointSeq uint64
}

func (e commitEffect) run(runtimeCtx context.Context, ports commitPorts) commitCompletedEvent {
	err := dispatchCommittedRecipients(runtimeCtx, e.event, ports)
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrCommitEffectFailed, err)
		return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, err: err}
	}
	if ports.cursorStore != nil {
		err = ports.cursorStore.StorePostCommitCursor(runtimeCtx, ChannelID{ID: e.event.ChannelID, Type: e.event.ChannelType}, e.event.MessageSeq)
		if err != nil {
			err = fmt.Errorf("%w: store post-commit cursor: %w", ErrCommitEffectFailed, err)
			return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, err: err}
		}
	}
	return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, checkpointSeq: e.event.MessageSeq}
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
		state.finishCommitSuccess(event.checkpointSeq)
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
