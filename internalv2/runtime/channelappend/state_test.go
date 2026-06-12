package channelappend

import "testing"

func TestNewChannelStateCapturesAuthorityIdentityAndLimits(t *testing.T) {
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "room", Type: 2},
		ChannelKey:   "2:room",
		LeaderNodeID: 1,
		Epoch:        10,
		LeaderEpoch:  3,
	}

	state := newChannelState(target, channelStateLimits{
		pendingItemHighWatermark: 8,
		appendInflightLimit:      2,
	})

	if state.target != target {
		t.Fatalf("target = %+v, want %+v", state.target, target)
	}
	if state.pendingItemHighWatermark != 8 {
		t.Fatalf("pendingItemHighWatermark = %d, want 8", state.pendingItemHighWatermark)
	}
	if state.appendInflightLimit != 2 {
		t.Fatalf("appendInflightLimit = %d, want 2", state.appendInflightLimit)
	}
}

func TestChannelStateRecordsInOrderAppendCompletionWithoutMap(t *testing.T) {
	state := newChannelState(AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}}, channelStateLimits{})
	state.recordAppendCompletion(appendCompletedEvent{seq: 0})

	if state.completedAppends != nil {
		t.Fatalf("completedAppends allocated for in-order completion")
	}

	event, ok := state.popNextAppendCompletion()
	if !ok {
		t.Fatalf("popNextAppendCompletion() ok = false, want true")
	}
	if event.seq != 0 {
		t.Fatalf("popped seq = %d, want 0", event.seq)
	}
	if state.nextAppendDrainSeq != 1 {
		t.Fatalf("nextAppendDrainSeq = %d, want 1", state.nextAppendDrainSeq)
	}
}

func TestChannelStateIgnoresStaleAppendCompletion(t *testing.T) {
	state := newChannelState(AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}}, channelStateLimits{})
	payload := []byte("payload")
	completion := appendCompletedEvent{
		seq: 0,
		items: []appendItemCompletion{{
			item: preparedSend{Command: SendCommand{Payload: payload}},
		}},
	}
	state.recordAppendCompletion(completion)
	if _, ok := state.popNextAppendCompletion(); !ok {
		t.Fatalf("popNextAppendCompletion() ok = false, want true")
	}

	state.recordAppendCompletion(completion)

	if state.completedAppends != nil {
		t.Fatalf("completedAppends retained stale append completion: %#v", state.completedAppends)
	}
}

func TestChannelStateNextAppendBatchDoesNotAllocatePendingCopy(t *testing.T) {
	state := newChannelState(AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}}, channelStateLimits{})
	pending := make([]preparedSend, 16)

	allocs := testing.AllocsPerRun(100, func() {
		state.pendingItems = pending
		state.appendInflight = 0
		state.appendInflightItems = 0

		_, items, ok := state.nextAppendBatch()
		if !ok {
			t.Fatalf("nextAppendBatch() ok = false, want true")
		}
		if len(items) != len(pending) {
			t.Fatalf("items = %d, want %d", len(items), len(pending))
		}
	})
	if allocs != 0 {
		t.Fatalf("nextAppendBatch allocations = %.1f, want 0", allocs)
	}
}

func TestChannelStateDropCurrentCommitAdvancesCursorWithoutMovingBacklog(t *testing.T) {
	state := newChannelState(AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}}, channelStateLimits{})
	state.enqueueCommitted(CommittedEnvelope{MessageID: 1})
	state.enqueueCommitted(CommittedEnvelope{MessageID: 2})
	state.enqueueCommitted(CommittedEnvelope{MessageID: 3})

	state.dropCurrentCommit()

	if state.commitCursor != 1 {
		t.Fatalf("commitCursor = %d, want 1 without compacting every committed item", state.commitCursor)
	}
	if len(state.committed) != 3 {
		t.Fatalf("committed len = %d, want original backing queue retained after one drop", len(state.committed))
	}
	if state.committed[0].MessageID != 0 || state.committed[1].MessageID != 2 || state.committed[2].MessageID != 3 {
		t.Fatalf("committed queue = %#v, want first slot cleared and remaining slots unmoved", state.committed)
	}
	if backlog := state.commitBacklog(); backlog != 2 {
		t.Fatalf("commitBacklog() = %d, want 2", backlog)
	}
}

func TestChannelStateCommitEffectSharesQueuedImmutablePayload(t *testing.T) {
	payload := []byte("payload")
	state := newChannelState(AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}}, channelStateLimits{})
	state.enqueueCommitted(CommittedEnvelope{MessageID: 1, Payload: payload})

	var effect commitEffect
	ok := state.nextCommitEffect("2:room", &effect)
	if !ok {
		t.Fatalf("nextCommitEffect() ok = false, want true")
	}
	if len(effect.events) != 1 {
		t.Fatalf("commit effect events = %d, want 1", len(effect.events))
	}
	if len(effect.events[0].Payload) == 0 || &effect.events[0].Payload[0] != &payload[0] {
		t.Fatalf("commit effect payload did not share queued immutable payload")
	}
}

func TestChannelStateCommitEffectReusesReadySubscriberCache(t *testing.T) {
	recipients := []Recipient{{UID: "u1"}, {UID: "u2"}}
	state := newChannelState(AuthorityTarget{
		ChannelID:                 ChannelID{ID: "room", Type: 2},
		SubscriberMutationVersion: 7,
	}, channelStateLimits{})
	state.subscriberCache = subscriberCache{
		ready:           true,
		mutationVersion: 7,
		recipients:      recipients,
	}
	state.enqueueCommitted(CommittedEnvelope{MessageID: 1})

	var effect commitEffect
	ok := state.nextCommitEffect("2:room", &effect)
	if !ok {
		t.Fatalf("nextCommitEffect() ok = false, want true")
	}
	if len(effect.subscriberCache.recipients) == 0 {
		t.Fatalf("commit effect subscriber cache is empty")
	}
	if &effect.subscriberCache.recipients[0] != &state.subscriberCache.recipients[0] {
		t.Fatalf("commit effect cloned ready subscriber cache; hot path should reuse immutable backing")
	}
}

func TestChannelStateRecordSubscriberCacheNoopsWhenAlreadyCurrent(t *testing.T) {
	recipients := []Recipient{{UID: "u1"}, {UID: "u2"}}
	state := newChannelState(AuthorityTarget{
		ChannelID:                 ChannelID{ID: "room", Type: 2},
		SubscriberMutationVersion: 7,
	}, channelStateLimits{})
	state.subscriberCache = subscriberCache{
		ready:           true,
		mutationVersion: 7,
		recipients:      recipients,
	}

	state.recordSubscriberCache(state.subscriberCache)

	if len(state.subscriberCache.recipients) == 0 {
		t.Fatalf("subscriber cache is empty")
	}
	if &state.subscriberCache.recipients[0] != &recipients[0] {
		t.Fatalf("recordSubscriberCache cloned an already-current subscriber cache")
	}
}
