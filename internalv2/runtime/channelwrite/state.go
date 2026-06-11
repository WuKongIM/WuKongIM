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
	// subscriberCache stores the full non-large subscriber snapshot for post-commit fanout.
	subscriberCache subscriberCache
}

type channelStateLimits struct {
	pendingItemHighWatermark int
	appendInflightLimit      int
}

// subscriberCache is a versioned non-large channel subscriber snapshot.
type subscriberCache struct {
	// ready reports whether recipients contains a complete subscriber snapshot.
	ready bool
	// mutationVersion is the channel subscriber version used to invalidate recipients.
	mutationVersion uint64
	// recipients are cloned durable channel subscribers for non-large fanout.
	recipients []Recipient
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

func (s *channelState) refreshRecipientMetadata(target AuthorityTarget) {
	if s.target.Large != target.Large || s.target.SubscriberMutationVersion != target.SubscriberMutationVersion {
		s.subscriberCache = subscriberCache{}
	}
	s.target.Large = target.Large
	s.target.SubscriberMutationVersion = target.SubscriberMutationVersion
}

func (s *channelState) applySubscriberMutation(update SubscriberMutationUpdate) {
	s.target.Large = update.Large
	s.target.SubscriberMutationVersion = update.SubscriberMutationVersion
	if update.Large {
		s.subscriberCache = subscriberCache{}
		return
	}
	if update.Reset {
		s.subscriberCache = subscriberCache{
			ready:           true,
			mutationVersion: update.SubscriberMutationVersion,
			recipients:      recipientsFromUIDs(update.AddedUIDs),
		}
		return
	}
	if !s.subscriberCache.ready {
		return
	}
	s.subscriberCache.remove(update.RemovedUIDs)
	s.subscriberCache.add(update.AddedUIDs)
	s.subscriberCache.mutationVersion = update.SubscriberMutationVersion
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
	if !s.canStartAppend() {
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

func (s *channelState) canStartAppend() bool {
	if len(s.pendingItems) == 0 {
		return false
	}
	limit := s.appendInflightLimit
	if limit <= 0 {
		limit = 1
	}
	return s.appendInflight < limit
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
	if s.commitInflight {
		return commitEffect{}, false
	}
	if s.commitCursor >= len(s.committed) {
		return commitEffect{}, false
	}
	event := s.committed[s.commitCursor].Clone()
	s.commitAttempts++
	effect := commitEffect{
		key:             key,
		seq:             s.nextCommitSeq,
		attempt:         s.commitAttempts,
		event:           event,
		target:          s.target,
		subscriberCache: s.subscriberCache.clone(),
	}
	s.nextCommitSeq++
	s.commitInflight = true
	return effect, true
}

func (s *channelState) recordSubscriberCache(cache subscriberCache) {
	if s.target.Large || !cache.ready || cache.mutationVersion != s.target.SubscriberMutationVersion {
		return
	}
	s.subscriberCache = cache.clone()
}

func (s *channelState) finishCommitSuccess(checkpointSeq uint64) {
	s.commitInflight = false
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

func (s *channelState) dropCommitBacklog() int {
	backlog := s.commitBacklog()
	if backlog == 0 {
		s.commitInflight = false
		s.commitAttempts = 0
		return 0
	}
	for i := s.commitCursor; i < len(s.committed); i++ {
		s.committed[i] = CommittedEnvelope{}
	}
	s.committed = nil
	s.commitCursor = 0
	s.commitInflight = false
	s.commitAttempts = 0
	return backlog
}

func (s *channelState) commitBacklog() int {
	backlog := len(s.committed) - s.commitCursor
	if backlog < 0 {
		return 0
	}
	return backlog
}

// hasPendingWork reports whether the channel has prepared items, in-flight
// appends, or committed envelopes still needing post-commit dispatch.
func (s *channelState) hasPendingWork() bool {
	return len(s.pendingItems) > 0 ||
		s.appendInflight > 0 ||
		s.commitBacklog() > 0 ||
		s.hasReadyAppendCompletion ||
		len(s.completedAppends) > 0
}

// hasRunnableWork reports whether advance can make progress without waiting
// for an in-flight append or commit to complete.
func (s *channelState) hasRunnableWork(includeCommit bool) bool {
	if s.canStartAppend() || s.hasReadyAppendCompletion || len(s.completedAppends) > 0 {
		return true
	}
	return includeCommit && !s.commitInflight && s.commitBacklog() > 0
}

func (s *channelState) pruneCommittedPrefixIfNeeded() {
	if s.commitCursor == 0 {
		return
	}
	if s.commitCursor >= len(s.committed) {
		s.committed = nil
		s.commitCursor = 0
		return
	}
	if s.commitCursor < committedCompactMinPrefix || s.commitCursor*2 < len(s.committed) {
		return
	}
	copy(s.committed, s.committed[s.commitCursor:])
	clear(s.committed[len(s.committed)-s.commitCursor:])
	s.committed = s.committed[:len(s.committed)-s.commitCursor]
	s.commitCursor = 0
}

func (c subscriberCache) clone() subscriberCache {
	c.recipients = append([]Recipient(nil), c.recipients...)
	return c
}

func (c subscriberCache) matches(target AuthorityTarget) bool {
	return !target.Large && c.ready && c.mutationVersion == target.SubscriberMutationVersion
}

func (c *subscriberCache) remove(uids []string) {
	if c == nil || len(uids) == 0 || len(c.recipients) == 0 {
		return
	}
	removed := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		if uid != "" {
			removed[uid] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return
	}
	out := c.recipients[:0]
	for _, recipient := range c.recipients {
		if _, ok := removed[recipient.UID]; ok {
			continue
		}
		out = append(out, recipient)
	}
	clear(c.recipients[len(out):])
	c.recipients = out
}

func (c *subscriberCache) add(uids []string) {
	if c == nil || len(uids) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(c.recipients)+len(uids))
	for _, recipient := range c.recipients {
		if recipient.UID != "" {
			seen[recipient.UID] = struct{}{}
		}
	}
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		c.recipients = append(c.recipients, Recipient{UID: uid})
	}
}
