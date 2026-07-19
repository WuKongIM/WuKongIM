package channelappend

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
	persistAfter                 PersistAfterEnqueuer
	subscriberPageSize           int
	recipientBatchSize           int
	recipientDispatchConcurrency int
	observer                     AppendObserver
}

func (p commitPorts) hasPostCommitWork() bool {
	return p.persistAfter != nil || p.activeAdmitter != nil || effectiveRecipientDeliveryEnqueuer(p) != nil
}

func (p commitPorts) hasRecipientWork() bool {
	return p.activeAdmitter != nil || effectiveRecipientDeliveryEnqueuer(p) != nil
}

type commitEffect struct {
	key             string
	seq             uint64
	attempt         int
	events          []CommittedEnvelope
	target          AuthorityTarget
	subscriberCache subscriberCache
}

type commitCompletedEvent struct {
	key             string
	seq             uint64
	attempt         int
	items           []commitCompletedItem
	subscriberCache subscriberCache
}

type commitCompletedItem struct {
	err           error
	result        string
	event         CommittedEnvelope
	detail        PostCommitFailureDetail
	checkpointSeq uint64
}

func (e commitEffect) run(runtimeCtx context.Context, ports commitPorts) commitCompletedEvent {
	startedAt := time.Now()
	result := channelAppendResultOK
	defer func() {
		observeEffect(ports.observer, EffectObservation{Stage: "post_commit", Result: result, Items: len(e.events), Duration: elapsedSince(startedAt)})
	}()
	completion := commitCompletedEvent{
		key:             e.key,
		seq:             e.seq,
		attempt:         e.attempt,
		items:           make([]commitCompletedItem, 0, len(e.events)),
		subscriberCache: e.subscriberCache,
	}
	cache := e.subscriberCache
	for _, event := range e.events {
		enqueuePersistAfterBestEffort(runtimeCtx, ports.persistAfter, event)
		if !ports.hasRecipientWork() {
			completion.items = append(completion.items, commitCompletedItem{event: event, checkpointSeq: event.MessageSeq})
			continue
		}
		dispatch, err := dispatchCommittedRecipientsForTarget(runtimeCtx, e.target, event, cache, ports)
		if err != nil {
			itemResult := errorClass(err)
			if result == channelAppendResultOK {
				result = itemResult
			} else if result != itemResult {
				result = channelAppendResultMixed
			}
			detail := postCommitFailureDetailFromError(err)
			completion.items = append(completion.items, commitCompletedItem{
				err:    fmt.Errorf("%w: %w", ErrCommitEffectFailed, err),
				result: itemResult,
				event:  event,
				detail: detail,
			})
			continue
		}
		cache = dispatch.subscriberCache
		completion.subscriberCache = cache
		completion.items = append(completion.items, commitCompletedItem{event: event, checkpointSeq: event.MessageSeq})
	}
	return completion
}

func enqueuePersistAfterBestEffort(ctx context.Context, enqueuer PersistAfterEnqueuer, event CommittedEnvelope) {
	if enqueuer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	enqueuer.EnqueuePersistAfter(ctx, event)
}

func commitPanicCompletion(effect commitEffect, recovered any) commitCompletedEvent {
	return commitErrorCompletion(effect, effectPanicError(effectStagePostCommit, recovered), PostCommitFailureDetail{Phase: "panic"})
}

func commitScheduleErrorCompletion(effect commitEffect, scheduleErr error) commitCompletedEvent {
	return commitErrorCompletion(effect, effectScheduleError(effectStagePostCommit, scheduleErr), PostCommitFailureDetail{Phase: "scheduler"})
}

// postCommitAdmissionFailure records an already-durable envelope that could
// not enter the separately bounded best-effort backlog.
func postCommitAdmissionFailure(event CommittedEnvelope) PostCommitFailureObservation {
	err := fmt.Errorf("%w: post-commit backlog admission: %w", ErrCommitEffectFailed, ErrBackpressured)
	return PostCommitFailureDetail{Phase: "admission"}.toObservation(event, 0, errorClass(err), err)
}

func commitErrorCompletion(effect commitEffect, err error, detail PostCommitFailureDetail) commitCompletedEvent {
	completion := commitCompletedEvent{
		key:     effect.key,
		seq:     effect.seq,
		attempt: effect.attempt,
		items:   make([]commitCompletedItem, 0, len(effect.events)),
	}
	itemErr := fmt.Errorf("%w: %w", ErrCommitEffectFailed, err)
	result := errorClass(err)
	for _, event := range effect.events {
		completion.items = append(completion.items, commitCompletedItem{
			err:    itemErr,
			result: result,
			event:  event,
			detail: detail,
		})
	}
	return completion
}

// postCommitFailureFromEvent maps a failed commit completion to its observation.
func postCommitFailureFromItem(event commitCompletedEvent, item commitCompletedItem) PostCommitFailureObservation {
	return item.detail.toObservation(item.event, event.attempt, item.result, item.err)
}
