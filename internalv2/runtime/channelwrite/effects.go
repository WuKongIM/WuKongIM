package channelwrite

import "context"

type prepareEffect struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
	key    string
	seq    uint64
}

type prepareCompletedEvent struct {
	target   AuthorityTarget
	results  []SendBatchItemResult
	prepared []preparedSend
	future   *Future
	key      string
	seq      uint64
}

func (e prepareEffect) run(runtimeCtx context.Context, ports preparePorts) prepareCompletedEvent {
	outcome := prepareBatch(runtimeCtx, e.items, ports)
	return prepareCompletedEvent{
		target:   e.target,
		results:  outcome.results,
		prepared: outcome.prepared,
		future:   e.future,
		key:      e.key,
		seq:      e.seq,
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
	matching := make([]preparedSend, 0, len(e.prepared))
	for _, item := range e.prepared {
		if !preparedCommandMatchesTarget(e.target, item.Command) {
			e.results[item.Index] = SendBatchItemResult{Err: ErrStaleRoute}
			continue
		}
		matching = append(matching, item)
	}

	r.mu.Lock()
	if len(matching) > 0 {
		key := targetKey(e.target)
		state := r.states[key]
		if state == nil {
			state = newChannelState(e.target, r.limits)
			r.states[key] = state
		}
		state.enqueuePrepared(matching)
	}
	r.mu.Unlock()

	e.future.complete(e.results)
	r.releaseEffectSlot()
}

func preparedCommandMatchesTarget(target AuthorityTarget, cmd SendCommand) bool {
	return target.ChannelID.ID == cmd.ChannelID && target.ChannelID.Type == cmd.ChannelType
}
