package channelwrite

import (
	"context"
	"time"
)

type prepareEffect struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
	key    string
	seq    uint64
}

type prepareCompletedEvent struct {
	target           AuthorityTarget
	results          []SendBatchItemResult
	prepared         []preparedSend
	canonicalResults []canonicalTerminalResult
	future           *Future
	key              string
	seq              uint64
}

func (e prepareEffect) run(runtimeCtx context.Context, ports preparePorts) prepareCompletedEvent {
	outcome := prepareBatch(runtimeCtx, e.items, ports)
	return prepareCompletedEvent{
		target:           e.target,
		results:          outcome.results,
		prepared:         outcome.prepared,
		canonicalResults: outcome.canonicalResults,
		future:           e.future,
		key:              e.key,
		seq:              e.seq,
	}
}

func preparePanicCompletion(effect prepareEffect, recovered any) prepareCompletedEvent {
	err := effectPanicError(effectStagePrepare, recovered)
	return prepareErrorCompletion(effect, err)
}

func prepareScheduleErrorCompletion(effect prepareEffect, scheduleErr error) prepareCompletedEvent {
	return prepareErrorCompletion(effect, effectScheduleError(effectStagePrepare, scheduleErr))
}

func prepareErrorCompletion(effect prepareEffect, err error) prepareCompletedEvent {
	results := make([]SendBatchItemResult, len(effect.items))
	for i := range results {
		results[i] = SendBatchItemResult{Err: err}
	}
	return prepareCompletedEvent{
		target:  effect.target,
		results: results,
		future:  effect.future,
		key:     effect.key,
		seq:     effect.seq,
	}
}

func (e prepareCompletedEvent) apply(r *reactor) {
	r.recordPrepareCompletion(e)
}

func (r *reactor) recordPrepareCompletion(event prepareCompletedEvent) {
	completed := r.completedPrepare[event.key]
	if completed == nil {
		completed = make(map[uint64]prepareCompletedEvent)
		r.completedPrepare[event.key] = completed
	}
	completed[event.seq] = event
	r.drainCompletedPrepare(event.key)
}

func (r *reactor) drainCompletedPrepare(key string) {
	for {
		seq := r.nextDrainSeq[key]
		completed := r.completedPrepare[key]
		event, ok := completed[seq]
		if !ok {
			return
		}
		delete(completed, seq)
		if len(completed) == 0 {
			delete(r.completedPrepare, key)
		}
		r.applyPreparedCompletion(event)
		r.nextDrainSeq[key] = seq + 1
	}
}

func (r *reactor) applyPreparedCompletion(e prepareCompletedEvent) {
	for _, item := range e.canonicalResults {
		if !preparedCommandMatchesTarget(e.target, item.command) {
			e.results[item.index] = SendBatchItemResult{Err: ErrStaleRoute}
		}
	}

	matching := make([]preparedSend, 0, len(e.prepared))
	matchingIndex := make(map[int]struct{}, len(e.prepared))
	for _, item := range e.prepared {
		if !preparedCommandMatchesTarget(e.target, item.Command) {
			e.results[item.Index] = SendBatchItemResult{Err: ErrStaleRoute}
			continue
		}
		item.future = e.future
		matching = append(matching, item)
		matchingIndex[item.Index] = struct{}{}
	}

	r.mu.Lock()
	if len(matching) > 0 {
		key := targetKey(e.target)
		state := r.states[key]
		if state == nil {
			state = newChannelState(e.target, r.limits)
			r.states[key] = state
		} else {
			state.refreshRecipientMetadata(e.target)
		}
		if state.canAdmit(len(matching)) {
			state.enqueuePrepared(matching)
			r.addPendingAppendItems(len(matching))
			r.scheduleAppendLocked(key, state)
		} else {
			for _, item := range matching {
				delete(matchingIndex, item.Index)
				e.results[item.Index] = SendBatchItemResult{Err: ErrChannelBusy}
			}
		}
	}
	r.observePressureLocked()
	r.mu.Unlock()

	e.future.completeItems(e.results, func(index int) bool {
		_, isPendingAppend := matchingIndex[index]
		return !isPendingAppend
	})
}

func (r *reactor) applySubscriberMutation(event subscriberMutationEvent) {
	key := channelKey(event.update.ChannelID)
	r.mu.Lock()
	state := r.states[key]
	if state != nil {
		state.applySubscriberMutation(event.update)
	}
	r.mu.Unlock()
	select {
	case event.ack <- nil:
	default:
	}
}

func (e appendCompletedEvent) apply(r *reactor) {
	r.recordAppendCompletion(e)
}

func (r *reactor) recordAppendCompletion(event appendCompletedEvent) {
	var dispatch appendCompletionDispatchBuffer
	r.mu.Lock()
	state := r.states[event.key]
	if state == nil {
		r.mu.Unlock()
		for _, completion := range event.items {
			completion.result = SendBatchItemResult{Err: ErrStaleRoute}
			completion.traceErr = ErrStaleRoute
			dispatch.add(appendCompletionDispatch{completion: completion, duration: event.duration})
		}
		dispatch.run(r)
		return
	}
	state.recordAppendCompletion(event)
	for {
		next, ok := state.popNextAppendCompletion()
		if !ok {
			break
		}
		state.finishAppend(len(next.items))
		r.addAppendInflightItems(-len(next.items))
		for _, completion := range next.items {
			if completion.traceErr == nil && completion.result.Err == nil && completion.result.Result.Reason == ReasonSuccess {
				state.enqueueCommitted(committedEnvelopeForAppend(completion.item, completion.appended))
				r.addPostCommitBacklog(1)
				r.scheduleCommitLocked(event.key, state)
			}
			dispatch.add(appendCompletionDispatch{completion: completion, duration: next.duration})
		}
		r.scheduleAppendLocked(event.key, state)
	}
	r.observePressureLocked()
	r.mu.Unlock()
	dispatch.run(r)
}

type appendCompletionDispatch struct {
	completion appendItemCompletion
	duration   time.Duration
}

type appendCompletionDispatchBuffer struct {
	first appendCompletionDispatch
	rest  []appendCompletionDispatch
	count int
}

func (b *appendCompletionDispatchBuffer) add(item appendCompletionDispatch) {
	if b.count == 0 {
		b.first = item
		b.count = 1
		return
	}
	if b.count == 1 {
		b.rest = append(b.rest, b.first)
	}
	b.rest = append(b.rest, item)
	b.count++
}

func (b *appendCompletionDispatchBuffer) run(r *reactor) {
	if b.count == 0 {
		return
	}
	if b.count == 1 {
		r.dispatchAppendItemCompletion(b.first.completion, b.first.duration)
		return
	}
	for _, item := range b.rest {
		r.dispatchAppendItemCompletion(item.completion, item.duration)
	}
}

func (r *reactor) dispatchAppendItemCompletion(completion appendItemCompletion, dur time.Duration) {
	observeAppendCompletion(r.appendPorts.observer, completion, dur)
	recordAppendDurableTrace(completion.item, appendTraceMessageSeq(completion), completion.traceErr, dur)
	completion.item.future.completeItem(completion.item.Index, completion.result)
}

func appendTraceMessageSeq(completion appendItemCompletion) uint64 {
	if completion.appended.MessageSeq != 0 {
		return completion.appended.MessageSeq
	}
	return completion.result.Result.MessageSeq
}

func (r *reactor) scheduleAppendLocked(key string, state *channelState) {
	for {
		seq, items, ok := state.nextAppendBatch()
		if !ok {
			return
		}
		r.pendingAppend = append(r.pendingAppend, appendEffect{
			target: state.target,
			key:    key,
			seq:    seq,
			items:  items,
		})
		r.addEffectQueueDepth(effectStageAppend, 1)
		r.addPendingAppendItems(-len(items))
		r.addAppendInflightItems(len(items))
	}
}

func (r *reactor) scheduleCommitLocked(key string, state *channelState) {
	if r.stopCtx.Err() != nil {
		return
	}
	effect, ok := state.nextCommitEffect(key)
	if !ok {
		return
	}
	r.pendingCommit = append(r.pendingCommit, effect)
	r.addEffectQueueDepth(effectStagePostCommit, 1)
}

func preparedCommandMatchesTarget(target AuthorityTarget, cmd SendCommand) bool {
	return target.ChannelID.ID == cmd.ChannelID && target.ChannelID.Type == cmd.ChannelType
}
