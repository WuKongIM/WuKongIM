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
		}
		if state.canAdmit(len(matching)) {
			state.enqueuePrepared(matching)
			r.scheduleAppendLocked(key, state)
		} else {
			for _, item := range matching {
				delete(matchingIndex, item.Index)
				e.results[item.Index] = SendBatchItemResult{Err: ErrChannelBusy}
			}
		}
	}
	r.mu.Unlock()

	e.future.completeItems(e.results, func(index int) bool {
		_, isPendingAppend := matchingIndex[index]
		return !isPendingAppend
	})
}

func (e appendCompletedEvent) apply(r *reactor) {
	r.recordAppendCompletion(e)
}

func (r *reactor) recordAppendCompletion(event appendCompletedEvent) {
	var dispatch []appendCompletionDispatch
	r.mu.Lock()
	state := r.states[event.key]
	if state == nil {
		r.mu.Unlock()
		for _, completion := range event.items {
			completion.result = SendBatchItemResult{Err: ErrStaleRoute}
			completion.traceErr = ErrStaleRoute
			dispatch = append(dispatch, appendCompletionDispatch{completion: completion, duration: event.duration})
		}
		for _, item := range dispatch {
			r.dispatchAppendItemCompletion(item.completion, item.duration)
		}
		return
	}
	state.recordAppendCompletion(event)
	for {
		next, ok := state.popNextAppendCompletion()
		if !ok {
			break
		}
		state.finishAppend(len(next.items))
		for _, completion := range next.items {
			if completion.traceErr == nil && completion.result.Err == nil && completion.result.Result.Reason == ReasonSuccess {
				state.enqueueCommitted(committedEnvelopeForAppend(completion.item, completion.appended))
			}
			dispatch = append(dispatch, appendCompletionDispatch{completion: completion, duration: next.duration})
		}
		r.scheduleAppendLocked(event.key, state)
	}
	r.mu.Unlock()
	for _, item := range dispatch {
		r.dispatchAppendItemCompletion(item.completion, item.duration)
	}
}

type appendCompletionDispatch struct {
	completion appendItemCompletion
	duration   time.Duration
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
	}
}

func preparedCommandMatchesTarget(target AuthorityTarget, cmd SendCommand) bool {
	return target.ChannelID.ID == cmd.ChannelID && target.ChannelID.Type == cmd.ChannelType
}
