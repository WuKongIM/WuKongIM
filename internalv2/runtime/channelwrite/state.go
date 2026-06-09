package channelwrite

const committedCompactMinPrefix = 1024

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
	// readyAppendCompletion stores the next in-order append completion without allocating the out-of-order map.
	readyAppendCompletion appendCompletedEvent
	// hasReadyAppendCompletion reports whether readyAppendCompletion is populated.
	hasReadyAppendCompletion bool
	completedAppends         map[uint64]appendCompletedEvent
	committed                []CommittedEnvelope
	nextCommitSeq            uint64
	commitCursor             int
	commitInflight           bool
	commitAttempts           int
	cursorSeq                uint64
	replayEnabled            bool
	replayDone               bool
	replayInflight           bool
	replayNextSeq            uint64
	replayInsertIndex        int
	replayAttempts           int
}

type channelStateLimits struct {
	pendingItemHighWatermark int
	appendInflightLimit      int
}

func newChannelState(target AuthorityTarget, limits channelStateLimits, replayEnabled ...bool) *channelState {
	enabled := false
	if len(replayEnabled) > 0 {
		enabled = replayEnabled[0]
	}
	state := &channelState{
		target:                   target,
		pendingItemHighWatermark: limits.pendingItemHighWatermark,
		appendInflightLimit:      limits.appendInflightLimit,
	}
	if !enabled {
		state.replayDone = true
	}
	state.replayEnabled = enabled
	return state
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
	limit := s.appendInflightLimit
	if limit <= 0 {
		limit = 1
	}
	if s.appendInflight >= limit {
		return 0, nil, false
	}
	items := s.pendingItems
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
	if event.seq == s.nextAppendDrainSeq && !s.hasReadyAppendCompletion {
		s.readyAppendCompletion = event
		s.hasReadyAppendCompletion = true
		return
	}
	if s.completedAppends == nil {
		s.completedAppends = make(map[uint64]appendCompletedEvent)
	}
	s.completedAppends[event.seq] = event
}

func (s *channelState) popNextAppendCompletion() (appendCompletedEvent, bool) {
	if s.hasReadyAppendCompletion && s.readyAppendCompletion.seq == s.nextAppendDrainSeq {
		event := s.readyAppendCompletion
		s.readyAppendCompletion = appendCompletedEvent{}
		s.hasReadyAppendCompletion = false
		s.nextAppendDrainSeq++
		return event, true
	}
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
	if !s.replayDone {
		return commitEffect{}, false
	}
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

func (s *channelState) finishCommitSuccess(checkpointSeq uint64) {
	s.commitInflight = false
	if checkpointSeq > s.cursorSeq {
		s.cursorSeq = checkpointSeq
	}
	s.dropCurrentCommit()
}

func (s *channelState) finishCommitFailure() {
	s.commitInflight = false
}

func (s *channelState) cancelCommitDispatch() {
	s.commitInflight = false
	if s.commitAttempts > 0 {
		s.commitAttempts--
	}
}

func (s *channelState) dropCurrentCommit() {
	if s.commitCursor >= len(s.committed) {
		s.commitAttempts = 0
		return
	}
	s.committed[s.commitCursor] = CommittedEnvelope{}
	s.commitCursor++
	s.commitAttempts = 0
	s.pruneCommittedPrefixIfNeeded()
}

func (s *channelState) nextCursorEffect(key string, limit int) (cursorEffect, bool) {
	if !s.replayEnabled || s.replayDone || s.replayInflight {
		return cursorEffect{}, false
	}
	effect := cursorEffect{
		key:       key,
		channelID: s.target.ChannelID,
		fromSeq:   s.replayNextSeq,
		limit:     limit,
		load:      s.replayNextSeq == 0,
	}
	s.replayInflight = true
	s.replayAttempts++
	return effect, true
}

func (s *channelState) finishCursorReplaySuccess(loadedCursor uint64, messages []CommittedMessage, nextSeq uint64, done bool) {
	s.replayInflight = false
	s.replayAttempts = 0
	if loadedCursor > s.cursorSeq {
		s.cursorSeq = loadedCursor
	}
	if s.replayNextSeq == 0 {
		s.replayNextSeq = nextPostCommitSeq(s.cursorSeq)
	}
	if nextSeq > 0 {
		s.replayNextSeq = nextSeq
	}
	s.enqueueReplayCommitted(messages)
	if done {
		s.replayDone = true
	}
}

func (s *channelState) finishCursorReplayFailure() {
	s.replayInflight = false
}

func (s *channelState) cancelCursorReplayDispatch() {
	s.replayInflight = false
}

func (s *channelState) enqueueReplayCommitted(messages []CommittedMessage) {
	if len(messages) == 0 {
		return
	}
	replay := make([]CommittedEnvelope, 0, len(messages))
	for _, msg := range messages {
		if msg.MessageSeq <= s.cursorSeq {
			continue
		}
		replay = append(replay, msg.Clone())
	}
	if len(replay) == 0 {
		return
	}
	insertAt := s.replayInsertIndex
	if insertAt > len(s.committed) {
		insertAt = len(s.committed)
	}
	s.committed = append(s.committed, make([]CommittedEnvelope, len(replay))...)
	copy(s.committed[insertAt+len(replay):], s.committed[insertAt:])
	copy(s.committed[insertAt:], replay)
	s.replayInsertIndex = insertAt + len(replay)
}

func (s *channelState) commitBacklog() int {
	backlog := len(s.committed) - s.commitCursor
	if backlog < 0 {
		return 0
	}
	return backlog
}

func (s *channelState) pruneCommittedPrefixIfNeeded() {
	if s.commitCursor == 0 {
		return
	}
	if s.commitCursor >= len(s.committed) {
		s.committed = nil
		s.commitCursor = 0
		s.replayInsertIndex = 0
		return
	}
	if s.commitCursor < committedCompactMinPrefix || s.commitCursor*2 < len(s.committed) {
		return
	}
	copy(s.committed, s.committed[s.commitCursor:])
	clear(s.committed[len(s.committed)-s.commitCursor:])
	s.committed = s.committed[:len(s.committed)-s.commitCursor]
	if s.replayInsertIndex >= s.commitCursor {
		s.replayInsertIndex -= s.commitCursor
	} else {
		s.replayInsertIndex = 0
	}
	s.commitCursor = 0
}
