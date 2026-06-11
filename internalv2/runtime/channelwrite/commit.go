package channelwrite

import (
	"context"
	"fmt"
	"time"
)

type commitPorts struct {
	subscribers                  SubscriberSource
	activeAdmitter               ConversationActiveAdmitter
	recipientAuthorityResolver   RecipientAuthorityResolver
	deliveryEnqueuer             RecipientDeliveryEnqueuer
	subscriberPageSize           int
	recipientBatchSize           int
	recipientDispatchConcurrency int
	observer                     AppendObserver
}

func (p commitPorts) hasPostCommitWork() bool {
	return p.activeAdmitter != nil || effectiveRecipientDeliveryEnqueuer(p) != nil
}

type commitEffect struct {
	key             string
	seq             uint64
	attempt         int
	event           CommittedEnvelope
	target          AuthorityTarget
	subscriberCache subscriberCache
}

type commitCompletedEvent struct {
	key             string
	seq             uint64
	attempt         int
	err             error
	result          string
	event           CommittedEnvelope
	detail          PostCommitFailureDetail
	checkpointSeq   uint64
	subscriberCache subscriberCache
}

func (e commitEffect) run(runtimeCtx context.Context, ports commitPorts) commitCompletedEvent {
	startedAt := time.Now()
	result := channelWriteResultOK
	defer func() {
		observeEffect(ports.observer, EffectObservation{Stage: "post_commit", Result: result, Items: 1, Duration: elapsedSince(startedAt)})
	}()
	dispatch, err := dispatchCommittedRecipientsForTarget(runtimeCtx, e.target, e.event, e.subscriberCache, ports)
	if err != nil {
		result = errorClass(err)
		detail := postCommitFailureDetailFromError(err)
		err = fmt.Errorf("%w: %w", ErrCommitEffectFailed, err)
		return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, err: err, result: result, event: e.event.Clone(), detail: detail}
	}
	return commitCompletedEvent{key: e.key, seq: e.seq, attempt: e.attempt, checkpointSeq: e.event.MessageSeq, subscriberCache: dispatch.subscriberCache}
}

func commitPanicCompletion(effect commitEffect, recovered any) commitCompletedEvent {
	return commitErrorCompletion(effect, effectPanicError(effectStagePostCommit, recovered), PostCommitFailureDetail{Phase: "panic"})
}

func commitScheduleErrorCompletion(effect commitEffect, scheduleErr error) commitCompletedEvent {
	return commitErrorCompletion(effect, effectScheduleError(effectStagePostCommit, scheduleErr), PostCommitFailureDetail{Phase: "scheduler"})
}

func commitErrorCompletion(effect commitEffect, err error, detail PostCommitFailureDetail) commitCompletedEvent {
	return commitCompletedEvent{
		key:     effect.key,
		seq:     effect.seq,
		attempt: effect.attempt,
		err:     fmt.Errorf("%w: %w", ErrCommitEffectFailed, err),
		result:  errorClass(err),
		event:   effect.event.Clone(),
		detail:  detail,
	}
}

// postCommitFailureFromEvent maps a failed commit completion to its observation.
func postCommitFailureFromEvent(event commitCompletedEvent) PostCommitFailureObservation {
	return PostCommitFailureObservation{
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
	}
}
