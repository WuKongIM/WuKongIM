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
	nextCommitSeq            uint64
	commitCursor             int
	commitInflight           bool
	commitAttempts           int
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
	return len(s.pendingItems)+s.appendInflightItems+s.commitBacklog()+count <= s.pendingItemHighWatermark
}

func (s *channelState) nextAppendBatch() (uint64, []preparedSend, bool) {
	if len(s.pendingItems) == 0 {
		return 0, nil, false
	}
	// Keep same-channel append strictly single-flight until a future task
	// proves the appender can preserve durable order across concurrent batches.
	if s.appendInflight >= 1 {
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

func (s *channelState) nextCommitEffect(key string) (commitEffect, bool) {
	if s.commitInflight {
		return commitEffect{}, false
	}
	if s.commitCursor >= len(s.committed) {
		return commitEffect{}, false
	}
	event := s.committed[s.commitCursor].Clone()
	s.commitAttempts++
	effect := commitEffect{
		key:     key,
		seq:     s.nextCommitSeq,
		attempt: s.commitAttempts,
		event:   event,
	}
	s.nextCommitSeq++
	s.commitInflight = true
	return effect, true
}

func (s *channelState) finishCommitSuccess() {
	s.commitInflight = false
	s.dropCurrentCommit()
}

func (s *channelState) finishCommitFailure() {
	s.commitInflight = false
}

func (s *channelState) dropCurrentCommit() {
	if s.commitCursor >= len(s.committed) {
		s.commitAttempts = 0
		return
	}
	s.committed[s.commitCursor] = CommittedEnvelope{}
	s.commitCursor++
	s.commitAttempts = 0
	s.pruneCommittedPrefix()
}

func (s *channelState) commitBacklog() int {
	backlog := len(s.committed) - s.commitCursor
	if backlog < 0 {
		return 0
	}
	return backlog
}

func (s *channelState) pruneCommittedPrefix() {
	if s.commitCursor == 0 {
		return
	}
	if s.commitCursor >= len(s.committed) {
		s.committed = nil
		s.commitCursor = 0
		return
	}
	copy(s.committed, s.committed[s.commitCursor:])
	clear(s.committed[len(s.committed)-s.commitCursor:])
	s.committed = s.committed[:len(s.committed)-s.commitCursor]
	s.commitCursor = 0
}
