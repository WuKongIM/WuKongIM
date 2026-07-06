package meta

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestMessageEventAppendTextLifecycle(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	first, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-text",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"hello"}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	if first.MsgEventSeq != 1 || first.Status != EventStatusOpen {
		t.Fatalf("first result = %#v, want seq=1 status=open", first)
	}

	second, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-text",
		EventID: "evt-2", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":" world"}`), OccurredAt: 12, UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if second.MsgEventSeq != 2 {
		t.Fatalf("second seq = %d, want 2", second.MsgEventSeq)
	}

	closed, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-text",
		EventID: "evt-3", EventKey: "main", EventType: EventTypeStreamClose,
		Payload: []byte(`{"end_reason":2}`), OccurredAt: 14, UpdatedAt: 15,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(close): %v", err)
	}
	if closed.MsgEventSeq != 3 || closed.State.EndReason != 2 {
		t.Fatalf("closed result = %#v, want seq=3 end_reason=2", closed)
	}

	state, ok, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-text", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if !ok {
		t.Fatal("GetMessageEventState(): missing state")
	}
	if state.Status != EventStatusClosed || state.LastMsgEventSeq != 3 {
		t.Fatalf("state = %#v, want closed seq=3", state)
	}
	if got, want := string(state.SnapshotPayload), `{"kind":"text","text":"hello world"}`; !jsonEqualForTest(got, want) {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

func TestMessageEventAppendStreamOpenStartsDefaultLane(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	result, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-open",
		EventID: "evt-open", EventType: EventTypeStreamOpen,
		OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(open): %v", err)
	}
	if result.MsgEventSeq != 1 || result.EventKey != EventKeyDefault || result.Status != EventStatusOpen {
		t.Fatalf("open result = %#v, want seq=1 default open", result)
	}
	if result.State.LastVisibility != VisibilityPublic {
		t.Fatalf("open visibility = %q, want public default", result.State.LastVisibility)
	}
}

func TestMessageEventAppendIdempotentByLastEventID(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	first, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"A"}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	duplicate, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"B"}`), OccurredAt: 12, UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(duplicate): %v", err)
	}
	if duplicate.MsgEventSeq != first.MsgEventSeq {
		t.Fatalf("duplicate seq = %d, want %d", duplicate.MsgEventSeq, first.MsgEventSeq)
	}
	if got, want := string(duplicate.State.SnapshotPayload), `{"kind":"text","text":"A"}`; !jsonEqualForTest(got, want) {
		t.Fatalf("duplicate snapshot = %s, want %s", got, want)
	}
}

func TestMessageEventAppendIdempotentAfterNewerEvent(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	first, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem-old",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"A"}`), UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	second, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem-old",
		EventID: "evt-2", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"B"}`), UpdatedAt: 12,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if second.MsgEventSeq != 2 {
		t.Fatalf("second seq = %d, want 2", second.MsgEventSeq)
	}
	duplicate, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-idem-old",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"A"}`), UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(duplicate old): %v", err)
	}
	if duplicate.MsgEventSeq != first.MsgEventSeq {
		t.Fatalf("duplicate old seq = %d, want original %d", duplicate.MsgEventSeq, first.MsgEventSeq)
	}
	if duplicate.State.LastMsgEventSeq != duplicate.MsgEventSeq || duplicate.State.Status != duplicate.Status {
		t.Fatalf("duplicate old result state = %#v, want seq/status consistent with result %#v", duplicate.State, duplicate)
	}
	state, ok, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-idem-old", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if !ok {
		t.Fatal("GetMessageEventState(): missing state")
	}
	if state.LastMsgEventSeq != 2 {
		t.Fatalf("state seq = %d, want 2", state.LastMsgEventSeq)
	}
	if got, want := string(state.SnapshotPayload), `{"kind":"text","text":"AB"}`; !jsonEqualForTest(got, want) {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

func TestMessageEventAppendTerminalDoesNotAdvanceSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	closed, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-terminal",
		EventID: "evt-close", EventKey: "main", EventType: EventTypeStreamClose,
		Payload: []byte(`{"end_reason":2}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(close): %v", err)
	}
	delta, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-terminal",
		EventID: "evt-after-close", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"late"}`), OccurredAt: 12, UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(delta): %v", err)
	}
	if delta.MsgEventSeq != closed.MsgEventSeq || delta.Status != EventStatusClosed {
		t.Fatalf("delta result = %#v, want close seq/status", delta)
	}
}

func TestMessageEventAppendNormalizesEmptyEventKey(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	result, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-key",
		EventID: "evt-1", EventKey: "  ", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"A"}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(): %v", err)
	}
	if result.EventKey != EventKeyDefault {
		t.Fatalf("event key = %q, want %q", result.EventKey, EventKeyDefault)
	}
	if _, ok, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-key", EventKeyDefault); err != nil {
		t.Fatalf("GetMessageEventState(default): %v", err)
	} else if !ok {
		t.Fatal("GetMessageEventState(default): missing state")
	}
}

func TestMessageEventAppendRejectsInvalidChannelType(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := store.db.HashSlot(4).AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID: "g1", ChannelType: -1, ClientMsgNo: "cmn-invalid",
		EventID: "evt-1", EventType: EventTypeStreamDelta,
	})
	if err == nil {
		t.Fatal("AppendMessageEvent() error = nil, want invalid argument")
	}
}

func TestMessageEventAppendFinishUsesFinishKey(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	result, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-finish",
		EventID: "evt-finish", EventKey: "main", EventType: EventTypeStreamFinish,
		Payload: []byte(`{"ok":true}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(finish): %v", err)
	}
	if result.EventKey != EventKeyFinish || result.Status != EventStatusClosed {
		t.Fatalf("finish result = %#v, want finish key and closed status", result)
	}
	state, ok, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-finish", EventKeyFinish)
	if err != nil {
		t.Fatalf("GetMessageEventState(finish): %v", err)
	}
	if !ok {
		t.Fatal("GetMessageEventState(finish): missing state")
	}
	if state.EventKey != EventKeyFinish || state.Status != EventStatusClosed {
		t.Fatalf("finish state = %#v, want finish key and closed status", state)
	}
}

func TestMessageEventAppendBatchOverlayAllocatesDistinctSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	first, err := batch.AppendMessageEvent(4, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-batch",
		EventID: "evt-1", EventKey: "left", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"A"}`), OccurredAt: 10, UpdatedAt: 11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	second, err := batch.AppendMessageEvent(4, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-batch",
		EventID: "evt-2", EventKey: "right", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"B"}`), OccurredAt: 12, UpdatedAt: 13,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(second): %v", err)
	}
	if first.MsgEventSeq != 1 || second.MsgEventSeq != 2 {
		t.Fatalf("batch seqs = %d,%d want 1,2", first.MsgEventSeq, second.MsgEventSeq)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	states, err := store.db.HashSlot(4).ListMessageEventStates(ctx, "g1", 2, "cmn-batch", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(): %v", err)
	}
	if len(states) != 2 || states[0].EventKey != "left" || states[1].EventKey != "right" {
		t.Fatalf("states = %#v, want left/right", states)
	}
}

func TestMessageEventAppendBatchDetectsExternalCursorDrift(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	if _, err := batch.AppendMessageEvent(4, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-drift",
		EventID: "evt-batch", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"batch"}`), UpdatedAt: 10,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(batch): %v", err)
	}
	if _, err := store.db.HashSlot(4).AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-drift",
		EventID: "evt-direct", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: []byte(`{"kind":"text","delta":"direct"}`), UpdatedAt: 11,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(direct): %v", err)
	}
	if err := batch.Commit(ctx); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("Commit() error = %v, want ErrStaleMeta", err)
	}
	state, ok, err := store.db.HashSlot(4).GetMessageEventState(ctx, "g1", 2, "cmn-drift", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if !ok {
		t.Fatal("GetMessageEventState(): missing state")
	}
	if state.LastMsgEventSeq != 1 || state.LastEventID != "evt-direct" {
		t.Fatalf("state after conflict = %#v, want direct seq=1", state)
	}
}

func TestMessageEventAppendClonesPayloadAndReturnedState(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	payload := []byte(`{"kind":"text","delta":"A"}`)
	result, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
		ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-clone",
		EventID: "evt-1", EventKey: "main", EventType: EventTypeStreamDelta,
		Payload: payload, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(): %v", err)
	}
	copy(payload, []byte(`{"kind":"text","delta":"B"}`))
	if len(result.State.SnapshotPayload) > 0 {
		result.State.SnapshotPayload[0] = 'X'
	}
	state, ok, err := shard.GetMessageEventState(ctx, "g1", 2, "cmn-clone", "main")
	if err != nil {
		t.Fatalf("GetMessageEventState(): %v", err)
	}
	if !ok {
		t.Fatal("GetMessageEventState(): missing state")
	}
	if got, want := string(state.SnapshotPayload), `{"kind":"text","text":"A"}`; !jsonEqualForTest(got, want) {
		t.Fatalf("snapshot = %s, want %s", got, want)
	}
}

func TestMessageEventListStatesByClientMsgNo(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	events := []MessageEventAppend{
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-list", EventID: "evt-a", EventKey: "a", EventType: EventTypeStreamDelta, Payload: []byte(`{"kind":"text","delta":"A"}`)},
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-list", EventID: "evt-b", EventKey: "b", EventType: EventTypeStreamDelta, Payload: []byte(`{"kind":"text","delta":"B"}`)},
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-other", EventID: "evt-c", EventKey: "c", EventType: EventTypeStreamDelta, Payload: []byte(`{"kind":"text","delta":"C"}`)},
	}
	for _, event := range events {
		if _, err := shard.AppendMessageEvent(ctx, event); err != nil {
			t.Fatalf("AppendMessageEvent(%s): %v", event.EventID, err)
		}
	}

	states, err := shard.ListMessageEventStates(ctx, "g1", 2, "cmn-list", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(): %v", err)
	}
	if len(states) != 2 || states[0].EventKey != "a" || states[1].EventKey != "b" {
		t.Fatalf("states = %#v, want a/b only", states)
	}
}

func jsonEqualForTest(left, right string) bool {
	var leftValue any
	var rightValue any
	if err := json.Unmarshal([]byte(left), &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(right), &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
