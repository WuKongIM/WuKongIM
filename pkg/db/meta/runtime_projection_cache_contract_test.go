package meta

import (
	"context"
	"reflect"
	"strconv"
	"testing"
)

func TestRuntimeMetaReducerIsMonotonicAndIdempotent(t *testing.T) {
	existing := contractRuntimeMeta("runtime-policy")

	t.Run("stale channel epoch cannot replace current state", func(t *testing.T) {
		candidate := existing
		candidate.ChannelEpoch--
		candidate.Features++
		candidate.LeaseUntilMS++

		got, result := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
		if result != MonotonicIgnoredStale {
			t.Fatalf("result = %v, want %v", result, MonotonicIgnoredStale)
		}
		if !reflect.DeepEqual(got, existing) {
			t.Fatalf("stale candidate changed state:\n got  %+v\n want %+v", got, existing)
		}
	})

	t.Run("same epochs with a different leader conflicts", func(t *testing.T) {
		candidate := existing
		candidate.Leader = 2

		got, result := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
		if result != MonotonicConflict {
			t.Fatalf("result = %v, want %v", result, MonotonicConflict)
		}
		if !reflect.DeepEqual(got, existing) {
			t.Fatalf("conflicting candidate changed state:\n got  %+v\n want %+v", got, existing)
		}
	})

	t.Run("same route cannot regress its leader lease", func(t *testing.T) {
		candidate := existing
		candidate.RouteGeneration = 0
		candidate.LeaseUntilMS--
		candidate.Features++

		got, result := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
		if result != MonotonicApplied {
			t.Fatalf("result = %v, want %v", result, MonotonicApplied)
		}
		if got.LeaseUntilMS != existing.LeaseUntilMS {
			t.Fatalf("lease = %d, want preserved %d", got.LeaseUntilMS, existing.LeaseUntilMS)
		}
		if got.RouteGeneration != existing.RouteGeneration {
			t.Fatalf("route generation = %d, want unchanged %d", got.RouteGeneration, existing.RouteGeneration)
		}
	})

	t.Run("new leader epoch preserves independent fences and bumps route once", func(t *testing.T) {
		candidate := existing
		candidate.LeaderEpoch++
		candidate.Leader = 2
		candidate.RouteGeneration = 0
		candidate.Features++
		candidate.LeaseUntilMS--
		candidate.RetentionThroughSeq--
		candidate.RetentionUpdatedAtMS--
		candidate.WriteFenceToken = "older-task"
		candidate.WriteFenceVersion--
		candidate.WriteFenceReason = 1
		candidate.WriteFenceUntilMS--
		candidate.DirectoryGeneration--

		got, result := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
		if result != MonotonicApplied {
			t.Fatalf("result = %v, want %v", result, MonotonicApplied)
		}
		if got.LeaderEpoch != existing.LeaderEpoch+1 || got.Leader != 2 {
			t.Fatalf("leader route = (epoch=%d leader=%d), want (%d,2)", got.LeaderEpoch, got.Leader, existing.LeaderEpoch+1)
		}
		if got.RouteGeneration != existing.RouteGeneration+1 {
			t.Fatalf("route generation = %d, want %d", got.RouteGeneration, existing.RouteGeneration+1)
		}
		if got.LeaseUntilMS != candidate.LeaseUntilMS {
			t.Fatalf("new-epoch lease = %d, want candidate %d", got.LeaseUntilMS, candidate.LeaseUntilMS)
		}
		if got.RetentionThroughSeq != existing.RetentionThroughSeq || got.RetentionUpdatedAtMS != existing.RetentionUpdatedAtMS {
			t.Fatalf("retention = (%d,%d), want preserved (%d,%d)", got.RetentionThroughSeq, got.RetentionUpdatedAtMS, existing.RetentionThroughSeq, existing.RetentionUpdatedAtMS)
		}
		if got.WriteFenceToken != existing.WriteFenceToken ||
			got.WriteFenceVersion != existing.WriteFenceVersion ||
			got.WriteFenceReason != existing.WriteFenceReason ||
			got.WriteFenceUntilMS != existing.WriteFenceUntilMS {
			t.Fatalf("write fence = (%q,%d,%d,%d), want preserved (%q,%d,%d,%d)",
				got.WriteFenceToken, got.WriteFenceVersion, got.WriteFenceReason, got.WriteFenceUntilMS,
				existing.WriteFenceToken, existing.WriteFenceVersion, existing.WriteFenceReason, existing.WriteFenceUntilMS)
		}
		if got.DirectoryGeneration != existing.DirectoryGeneration {
			t.Fatalf("directory generation = %d, want preserved %d", got.DirectoryGeneration, existing.DirectoryGeneration)
		}
		if got.Features != candidate.Features {
			t.Fatalf("features = %d, want candidate %d", got.Features, candidate.Features)
		}

		replayed, replayResult := resolveMonotonicChannelRuntimeMeta(got, true, got)
		if replayResult != MonotonicApplied {
			t.Fatalf("replay result = %v, want %v", replayResult, MonotonicApplied)
		}
		if !reflect.DeepEqual(replayed, got) {
			t.Fatalf("replay changed state:\n got  %+v\n want %+v", replayed, got)
		}
	})
}

func TestCreateChannelRuntimeMetaDoesNotOverwriteExistingOverlay(t *testing.T) {
	db := NewDB(nil)
	batch := db.NewBatch()
	defer batch.Close()

	existing := contractRuntimeMeta("runtime-create-only")
	candidate := existing
	candidate.ChannelEpoch++
	candidate.LeaderEpoch++
	candidate.Leader = 2
	candidate.RouteGeneration++
	result, err := batch.CreateChannelRuntimeMeta(7, candidate)
	if err != nil {
		t.Fatalf("CreateChannelRuntimeMeta(): %v", err)
	}
	if result.Created {
		t.Fatal("Created before commit operation = true, want false")
	}
	if len(batch.ops) != 1 {
		t.Fatalf("staged operations = %d, want 1", len(batch.ops))
	}

	key := encodeChannelRuntimeMetaRowKey(7, existing.ChannelID, existing.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	state := &batchCommitState{
		db: db,
		runtimeMeta: map[string]runtimeMetaOverlay{
			string(key): {meta: existing, exists: true},
		},
	}
	if err := batch.ops[0].apply(context.Background(), state, nil); err != nil {
		t.Fatalf("apply duplicate create: %v", err)
	}
	if result.Created {
		t.Fatal("Created after observing an existing row = true, want false")
	}
	if got := state.runtimeMeta[string(key)].meta; !reflect.DeepEqual(got, existing) {
		t.Fatalf("duplicate create replaced row:\n got  %+v\n want %+v", got, existing)
	}
}

func TestChannelLatestReducerKeepsHighestSequenceAndOwnsAcceptedPayload(t *testing.T) {
	existing := ChannelLatest{
		ChannelID:      "latest-policy",
		ChannelType:    2,
		LastMessageID:  100,
		LastMessageSeq: 10,
		LastAt:         1000,
		FromUID:        "u1",
		ClientMsgNo:    "m10",
		Payload:        []byte("current"),
		UpdatedAt:      1001,
	}

	for _, candidate := range []ChannelLatest{
		{ChannelID: "latest-policy", ChannelType: 2, LastMessageID: 99, LastMessageSeq: 9, Payload: []byte("stale")},
		{ChannelID: "latest-policy", ChannelType: 2, LastMessageID: 101, LastMessageSeq: 10, Payload: []byte("same-sequence-replay")},
	} {
		got := resolveChannelLatest(existing, true, candidate)
		if !reflect.DeepEqual(got, existing) {
			t.Fatalf("candidate at sequence %d changed latest:\n got  %+v\n want %+v", candidate.LastMessageSeq, got, existing)
		}
	}

	fresh := ChannelLatest{
		ChannelID:      "latest-policy",
		ChannelType:    2,
		LastMessageID:  110,
		LastMessageSeq: 11,
		LastAt:         1100,
		FromUID:        "u2",
		ClientMsgNo:    "m11",
		Payload:        []byte("fresh"),
		UpdatedAt:      1101,
	}
	got := resolveChannelLatest(existing, true, fresh)
	if !reflect.DeepEqual(got, fresh) {
		t.Fatalf("fresh candidate = %+v, want %+v", got, fresh)
	}
	fresh.Payload[0] = 'X'
	if string(got.Payload) != "fresh" {
		t.Fatalf("accepted payload changed through caller alias: %q", got.Payload)
	}

	replayed := resolveChannelLatest(got, true, ChannelLatest{
		ChannelID:      got.ChannelID,
		ChannelType:    got.ChannelType,
		LastMessageID:  got.LastMessageID,
		LastMessageSeq: got.LastMessageSeq,
		Payload:        []byte("different replay payload"),
	})
	if !reflect.DeepEqual(replayed, got) {
		t.Fatalf("same-sequence replay changed latest:\n got  %+v\n want %+v", replayed, got)
	}
}

func TestMessageEventReducerAllocatesMonotonicSequenceAndFreezesTerminalLane(t *testing.T) {
	firstEvent := contractMessageEvent("event-1", "main", EventTypeStreamDelta, []byte(`{"kind":"text","delta":"A"}`))
	mainState, cursor, applied, firstResult := reduceMessageEventAppend(
		MessageEventState{}, false,
		MessageEventCursor{}, false,
		firstEvent,
	)
	if !applied || mainState.LastMsgEventSeq != 1 || cursor.LastMsgEventSeq != 1 || firstResult.MsgEventSeq != 1 {
		t.Fatalf("first reduction = applied:%v state:%+v cursor:%+v result:%+v, want sequence 1", applied, mainState, cursor, firstResult)
	}
	if got, want := string(mainState.SnapshotPayload), `{"kind":"text","text":"A"}`; got != want {
		t.Fatalf("first snapshot = %s, want %s", got, want)
	}

	otherEvent := contractMessageEvent("event-2", "other", EventTypeStreamDelta, []byte(`{"kind":"text","delta":"B"}`))
	otherState, cursor, applied, otherResult := reduceMessageEventAppend(
		MessageEventState{}, false,
		cursor, true,
		otherEvent,
	)
	if !applied || otherState.LastMsgEventSeq != 2 || cursor.LastMsgEventSeq != 2 || otherResult.MsgEventSeq != 2 {
		t.Fatalf("second lane reduction = applied:%v state:%+v cursor:%+v result:%+v, want sequence 2", applied, otherState, cursor, otherResult)
	}

	replay := otherEvent
	replay.Payload = []byte(`{"kind":"text","delta":"must-not-apply"}`)
	replayedState, replayedCursor, applied, replayResult := reduceMessageEventAppend(otherState, true, cursor, true, replay)
	if applied {
		t.Fatal("same event id applied twice")
	}
	if !reflect.DeepEqual(replayedState, otherState) || replayedCursor != cursor {
		t.Fatalf("replay changed reducer state: state=%+v cursor=%+v", replayedState, replayedCursor)
	}
	if replayResult.MsgEventSeq != 2 || replayResult.Status != EventStatusOpen {
		t.Fatalf("replay result = %+v, want original sequence/status", replayResult)
	}

	closeEvent := contractMessageEvent("event-3", "main", EventTypeStreamClose, []byte(`{"snapshot":{"kind":"text","text":"final"},"end_reason":2}`))
	closedState, closedCursor, applied, closedResult := reduceMessageEventAppend(mainState, true, cursor, true, closeEvent)
	if !applied || closedState.Status != EventStatusClosed || closedState.LastMsgEventSeq != 3 || closedCursor.LastMsgEventSeq != 3 {
		t.Fatalf("close reduction = applied:%v state:%+v cursor:%+v, want closed sequence 3", applied, closedState, closedCursor)
	}
	if closedState.EndReason != 2 || string(closedState.SnapshotPayload) != `{"kind":"text","text":"final"}` {
		t.Fatalf("closed payload = reason:%d snapshot:%s", closedState.EndReason, closedState.SnapshotPayload)
	}
	if closedResult.Status != EventStatusClosed || closedResult.MsgEventSeq != 3 {
		t.Fatalf("close result = %+v, want closed sequence 3", closedResult)
	}

	lateEvent := contractMessageEvent("event-4", "main", EventTypeStreamDelta, []byte(`{"kind":"text","delta":"late"}`))
	lateState, lateCursor, applied, lateResult := reduceMessageEventAppend(closedState, true, closedCursor, true, lateEvent)
	if applied {
		t.Fatal("event after terminal state applied")
	}
	if !reflect.DeepEqual(lateState, closedState) || lateCursor != closedCursor {
		t.Fatalf("event after terminal state changed reducer: state=%+v cursor=%+v", lateState, lateCursor)
	}
	if lateResult.Status != EventStatusClosed || lateResult.MsgEventSeq != 3 {
		t.Fatalf("late result = %+v, want frozen terminal result", lateResult)
	}
}

func TestChannelReadCacheHonorsLRUCapacityAndExplicitInvalidation(t *testing.T) {
	db := &MetaDB{channelCache: newChannelReadCache(2)}
	keyA := []byte("channel-a")
	keyB := []byte("channel-b")
	keyC := []byte("channel-c")

	db.rememberChannel(keyA, Channel{ChannelID: "a", ChannelType: 2})
	db.rememberChannel(keyB, Channel{ChannelID: "b", ChannelType: 2})
	if got := db.channelCacheSize(); got != 2 {
		t.Fatalf("cache size = %d, want 2", got)
	}
	if _, ok := db.cachedChannel(keyA); !ok {
		t.Fatal("recently used channel a is missing")
	}

	db.rememberChannel(keyC, Channel{ChannelID: "c", ChannelType: 2})
	if got := db.channelCacheSize(); got != 2 {
		t.Fatalf("cache size after overflow = %d, want bounded at 2", got)
	}
	if _, ok := db.cachedChannel(keyB); ok {
		t.Fatal("least-recently-used channel b survived capacity eviction")
	}
	if _, ok := db.cachedChannel(keyA); !ok {
		t.Fatal("promoted channel a was evicted")
	}
	if _, ok := db.cachedChannel(keyC); !ok {
		t.Fatal("newest channel c is missing")
	}

	db.forgetChannel(keyA)
	if _, ok := db.cachedChannel(keyA); ok {
		t.Fatal("explicitly invalidated channel a is still cached")
	}
	if got := db.channelCacheSize(); got != 1 {
		t.Fatalf("cache size after one invalidation = %d, want 1", got)
	}

	db.clearChannelCache()
	if _, ok := db.cachedChannel(keyC); ok {
		t.Fatal("channel c survived full cache invalidation")
	}
	if got := db.channelCacheSize(); got != 0 {
		t.Fatalf("cache size after clear = %d, want 0", got)
	}
}

func TestMetaDBChannelReadCacheUsesDocumentedFixedCapacity(t *testing.T) {
	db := NewDB(nil)
	for i := 0; i <= channelCacheCapacity; i++ {
		key := []byte(strconv.Itoa(i))
		db.rememberChannel(key, Channel{ChannelID: string(key), ChannelType: 2})
	}
	if got := db.channelCacheSize(); got != 8192 {
		t.Fatalf("configured cache size after overflow = %d, want fixed bound 8192", got)
	}
	if _, ok := db.cachedChannel([]byte("0")); ok {
		t.Fatal("oldest channel survived fixed-capacity eviction")
	}
	if _, ok := db.cachedChannel([]byte(strconv.Itoa(channelCacheCapacity))); !ok {
		t.Fatal("newest channel is missing after fixed-capacity eviction")
	}
}

func contractRuntimeMeta(channelID string) ChannelRuntimeMeta {
	return normalizeChannelRuntimeMeta(ChannelRuntimeMeta{
		ChannelID:            channelID,
		ChannelType:          2,
		ChannelEpoch:         5,
		LeaderEpoch:          8,
		RouteGeneration:      20,
		Replicas:             []uint64{3, 1, 2, 2},
		ISR:                  []uint64{2, 1, 2},
		Leader:               1,
		MinISR:               2,
		Status:               2,
		Features:             4,
		LeaseUntilMS:         1000,
		RetentionThroughSeq:  100,
		RetentionUpdatedAtMS: 900,
		WriteFenceToken:      "current-task",
		WriteFenceVersion:    7,
		WriteFenceReason:     2,
		WriteFenceUntilMS:    1200,
		DirectoryGeneration:  11,
	})
}

func contractMessageEvent(eventID string, eventKey string, eventType string, payload []byte) MessageEventAppend {
	return MessageEventAppend{
		ChannelID:   "event-policy",
		ChannelType: 2,
		ClientMsgNo: "client-message",
		EventID:     eventID,
		EventKey:    eventKey,
		EventType:   eventType,
		Visibility:  VisibilityPublic,
		OccurredAt:  100,
		Payload:     payload,
		UpdatedAt:   101,
	}
}
