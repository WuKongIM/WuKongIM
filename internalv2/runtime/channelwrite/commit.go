package channelwrite

import (
	"context"
	"fmt"
	"time"
)

type commitPorts struct {
	subscribers                  SubscriberSource
	recipientAuthorityResolver   RecipientAuthorityResolver
	recipientRouter              RecipientAuthorityRouter
	subscriberPageSize           int
	recipientBatchSize           int
	recipientDispatchConcurrency int
	observer                     AppendObserver
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
	result        string
	event         CommittedEnvelope
	detail        PostCommitFailureDetail
	checkpointSeq uint64
}

func (e commitEffect) run(runtimeCtx context.Context, ports commitPorts) commitCompletedEvent {
	startedAt := time.Now()
	result := channelWriteResultOK
	defer func() {
		observeEffect(ports.observer, EffectObservation{Stage: "post_commit", Result: result, Items: 1, Duration: elapsedSince(startedAt)})
	}()
	err := dispatchCommittedRecipients(runtimeCtx, e.event, ports)
	if err != nil {
		result = errorClass(err)
		detail := postCommitFailureDetailFromError(err)
		err = fmt.Errorf("%w: %w", ErrCommitEffectFailed, err)
		return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, err: err, result: result, event: e.event.Clone(), detail: detail}
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
		backlogBefore := state.commitBacklog()
		state.finishCommitSuccess(event.checkpointSeq)
		r.addPostCommitBacklog(state.commitBacklog() - backlogBefore)
		r.scheduleCommitLocked(event.key, state)
		r.observePressureLocked()
		return
	}
	state.finishCommitFailure()
	if r.stopCtx.Err() != nil {
		return
	}
	observePostCommitFailure(r.appendPorts.observer, PostCommitFailureObservation{
		ReactorID:             r.id,
		ChannelID:             event.event.ChannelID,
		ChannelType:           event.event.ChannelType,
		MessageID:             event.event.MessageID,
		MessageSeq:            event.event.MessageSeq,
		Attempt:               event.attempt,
		Result:                event.result,
		Phase:                 event.detail.Phase,
		UID:                   event.detail.UID,
		UIDCount:              event.detail.UIDCount,
		RecipientCount:        event.detail.RecipientCount,
		TargetHashSlot:        event.detail.TargetHashSlot,
		TargetSlotID:          event.detail.TargetSlotID,
		TargetLeaderNodeID:    event.detail.TargetLeaderNodeID,
		TargetRouteRevision:   event.detail.TargetRouteRevision,
		TargetAuthorityEpoch:  event.detail.TargetAuthorityEpoch,
		DispatchTargetCount:   event.detail.DispatchTargetCount,
		DispatchBatchSize:     event.detail.DispatchBatchSize,
		DispatchOwnerNodeID:   event.detail.DispatchOwnerNodeID,
		DispatchOwnerRouteNum: event.detail.DispatchOwnerRouteNum,
		Err:                   event.err,
	})
	backlogBefore := state.commitBacklog()
	state.dropCurrentCommit()
	r.addPostCommitBacklog(state.commitBacklog() - backlogBefore)
	r.scheduleCommitLocked(event.key, state)
	r.observePressureLocked()
}
