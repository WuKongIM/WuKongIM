package channelwrite

type prepareEffect struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
}

type prepareCompletedEvent struct {
	target   AuthorityTarget
	results  []SendBatchItemResult
	prepared []preparedSend
	future   *Future
}

func (e prepareEffect) run(ports preparePorts) prepareCompletedEvent {
	outcome := prepareBatch(e.items, ports)
	return prepareCompletedEvent{
		target:   e.target,
		results:  outcome.results,
		prepared: outcome.prepared,
		future:   e.future,
	}
}

func (e prepareCompletedEvent) apply(r *reactor) {
	r.mu.Lock()
	for _, item := range e.prepared {
		target := targetForPreparedCommand(e.target, item.Command)
		key := targetKey(target)
		state := r.states[key]
		if state == nil {
			state = newChannelState(target, r.limits)
			r.states[key] = state
		}
		state.enqueuePrepared([]preparedSend{item})
	}
	r.mu.Unlock()

	e.future.complete(e.results)
}

func targetForPreparedCommand(base AuthorityTarget, cmd SendCommand) AuthorityTarget {
	target := base
	target.ChannelID = ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}
	if cmd.ChannelKey != "" {
		target.ChannelKey = cmd.ChannelKey
	} else {
		target.ChannelKey = channelKey(target.ChannelID)
	}
	return target
}
