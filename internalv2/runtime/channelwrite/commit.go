package channelwrite

import (
	"context"
)

type commitPorts struct {
	subscribers                SubscriberSource
	recipientAuthorityResolver RecipientAuthorityResolver
	recipientRouter            RecipientAuthorityRouter
	subscriberPageSize         int
	recipientBatchSize         int
}

type commitEffect struct {
	key   string
	seq   uint64
	event CommittedEnvelope
}

type commitCompletedEvent struct {
	key string
	seq uint64
	err error
}

func (e commitEffect) run(runtimeCtx context.Context, ports commitPorts) commitCompletedEvent {
	err := dispatchCommittedRecipients(runtimeCtx, e.event, ports)
	return commitCompletedEvent{key: e.key, seq: e.seq, err: err}
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
	state.finishCommit()
	r.scheduleCommitLocked(event.key, state)
}
