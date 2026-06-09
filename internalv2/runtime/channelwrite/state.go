package channelwrite

// channelState holds in-memory state for one locally authoritative channel.
type channelState struct {
	target AuthorityTarget

	pendingItemHighWatermark int
	appendInflightLimit      int
	pendingItems             []preparedSend
	appendInflight           int
	appendInflightItems      int
	nextAppendSeq            uint64
	nextAppendDrainSeq       uint64
	completedAppends         map[uint64]appendCompletedEvent
	committed                []CommittedEnvelope
}

type channelStateLimits struct {
	pendingItemHighWatermark int
	appendInflightLimit      int
}

func newChannelState(target AuthorityTarget, limits channelStateLimits) *channelState {
	return &channelState{
		target:                   target,
		pendingItemHighWatermark: limits.pendingItemHighWatermark,
		appendInflightLimit:      limits.appendInflightLimit,
	}
}

func (s *channelState) enqueuePrepared(items []preparedSend) {
	if len(items) == 0 {
		return
	}
	s.pendingItems = append(s.pendingItems, items...)
}

func (s *channelState) canAdmit(count int) bool {
	if count <= 0 {
		return true
	}
	if s.pendingItemHighWatermark <= 0 {
		return true
	}
	return len(s.pendingItems)+s.appendInflightItems+count <= s.pendingItemHighWatermark
}

func (s *channelState) nextAppendBatch() (uint64, []preparedSend, bool) {
	if len(s.pendingItems) == 0 {
		return 0, nil, false
	}
	limit := s.appendInflightLimit
	if limit <= 0 {
		limit = 1
	}
	if s.appendInflight >= limit {
		return 0, nil, false
	}
	items := append([]preparedSend(nil), s.pendingItems...)
	s.pendingItems = nil
	seq := s.nextAppendSeq
	s.nextAppendSeq++
	s.appendInflight++
	s.appendInflightItems += len(items)
	return seq, items, true
}

func (s *channelState) finishAppend(items int) {
	if s.appendInflight > 0 {
		s.appendInflight--
	}
	s.appendInflightItems -= items
	if s.appendInflightItems < 0 {
		s.appendInflightItems = 0
	}
}

func (s *channelState) recordAppendCompletion(event appendCompletedEvent) {
	if s.completedAppends == nil {
		s.completedAppends = make(map[uint64]appendCompletedEvent)
	}
	s.completedAppends[event.seq] = event
}

func (s *channelState) popNextAppendCompletion() (appendCompletedEvent, bool) {
	if s.completedAppends == nil {
		return appendCompletedEvent{}, false
	}
	event, ok := s.completedAppends[s.nextAppendDrainSeq]
	if !ok {
		return appendCompletedEvent{}, false
	}
	delete(s.completedAppends, s.nextAppendDrainSeq)
	if len(s.completedAppends) == 0 {
		s.completedAppends = nil
	}
	s.nextAppendDrainSeq++
	return event, true
}

func (s *channelState) enqueueCommitted(event CommittedEnvelope) {
	s.committed = append(s.committed, event.Clone())
}
