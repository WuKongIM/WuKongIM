package channelappend

import (
	"context"
	"fmt"
	"time"
)

type commitPorts struct {
	subscribers                SubscriberSource
	recipientAuthorityResolver RecipientAuthorityResolver
	deliveryEnqueuer           OnlineDeliveryEnqueuer
	persistAfter               PersistAfterEnqueuer
	subscriberPageSize         int
	recipientBatchSize         int
	observer                   AppendObserver
}

func (p commitPorts) hasPostCommitWork() bool {
	return p.persistAfter != nil || p.deliveryEnqueuer != nil
}

func (p commitPorts) hasRecipientWork() bool {
	return p.deliveryEnqueuer != nil
}

type commitEffect struct {
	key             string
	seq             uint64
	attempt         int
	events          []committedPostCommit
	target          AuthorityTarget
	subscriberCache subscriberCache
}

type commitCompletedEvent struct {
	key      string
	seq      uint64
	attempt  int
	duration time.Duration
	items    []commitCompletedItem
	// committed aliases the immutable effect records and carries exact
	// reservation ownership into completion validation without another copy.
	committed []committedPostCommit
	// failures contains observation-only side-effect failures that must not change item completion.
	failures        []commitCompletedItem
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
	completion := commitCompletedEvent{
		key:             e.key,
		seq:             e.seq,
		attempt:         e.attempt,
		items:           make([]commitCompletedItem, 0, len(e.events)),
		committed:       e.events,
		failures:        make([]commitCompletedItem, 0, len(e.events)),
		subscriberCache: e.subscriberCache,
	}
	cache := e.subscriberCache
	for _, committed := range e.events {
		event := committed.envelope
		enqueuePersistAfterBestEffort(runtimeCtx, ports.persistAfter, event)
		if !ports.hasRecipientWork() {
			completion.items = append(completion.items, commitCompletedItem{event: event, checkpointSeq: event.MessageSeq})
			continue
		}
		dispatch, err := dispatchCommittedRecipientsForTarget(runtimeCtx, e.target, event, cache, ports)
		if err != nil {
			itemResult := errorClass(err)
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

func commitErrorCompletion(effect commitEffect, err error, detail PostCommitFailureDetail) commitCompletedEvent {
	completion := commitCompletedEvent{
		key:       effect.key,
		seq:       effect.seq,
		attempt:   effect.attempt,
		items:     make([]commitCompletedItem, 0, len(effect.events)),
		committed: effect.events,
	}
	itemErr := fmt.Errorf("%w: %w", ErrCommitEffectFailed, err)
	result := errorClass(itemErr)
	for _, committed := range effect.events {
		completion.items = append(completion.items, commitCompletedItem{
			err:    itemErr,
			result: result,
			event:  committed.envelope,
			detail: detail,
		})
	}
	return completion
}

func commitCompletionResult(event commitCompletedEvent) string {
	result := ""
	for _, item := range event.items {
		itemResult := item.result
		if itemResult == "" {
			itemResult = errorClass(item.err)
		}
		if result == "" {
			result = itemResult
		} else if result != itemResult {
			return channelAppendResultMixed
		}
	}
	if result == "" {
		return channelAppendResultOther
	}
	return result
}

// postCommitFailureFromEvent maps a failed commit completion to its observation.
func postCommitFailureFromItem(event commitCompletedEvent, item commitCompletedItem) PostCommitFailureObservation {
	return item.detail.toObservation(item.event, event.attempt, item.result, item.err)
}
