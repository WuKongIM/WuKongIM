package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterMessageEventFacadeAppendsTerminalThroughChannelHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-channel"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-event-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"snapshot":{"kind":"text","text":"hello"}}`),
		UpdatedAt:   1001,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	if result.MsgEventSeq != 1 || result.Status != metadb.EventStatusClosed {
		t.Fatalf("append result = %#v, want seq=1 closed", result)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-event-1", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(channel hash slot) error = %v", err)
	}
	if len(states) != 1 || states[0].LastMsgEventSeq != 1 || states[0].LastEventID != "evt-1" {
		t.Fatalf("stored states = %#v, want one state at seq 1", states)
	}
}

func TestClusterMessageEventFacadeCachesStreamUntilTerminalEvent(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-stream-cache"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-cache",
		EventID:     "evt-delta-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"hello "}`),
		UpdatedAt:   1001,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(delta 1) error = %v", err)
	}
	if result.MsgEventSeq != 0 || result.Status != metadb.EventStatusOpen {
		t.Fatalf("delta result = %#v, want cache-only open seq 0", result)
	}

	result, err = node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-cache",
		EventID:     "evt-delta-2",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"kind":"text","delta":"world"}`),
		UpdatedAt:   1003,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(delta 2) error = %v", err)
	}
	if !strings.Contains(string(result.State.SnapshotPayload), "hello world") {
		t.Fatalf("cached delta snapshot = %s, want accumulated text", result.State.SnapshotPayload)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-stream-cache", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(before terminal) error = %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("states before terminal = %#v, want no durable event state", states)
	}
	key := metadb.MessageEventMessageKey{ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-stream-cache"}
	summary, err := node.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(before terminal) error = %v", err)
	}
	if len(summary[key]) != 1 || summary[key][0].LastMsgEventSeq != 0 || summary[key][0].Status != metadb.EventStatusOpen {
		t.Fatalf("summary before terminal = %#v, want one cached open state", summary[key])
	}
	if !strings.Contains(string(summary[key][0].SnapshotPayload), "hello world") {
		t.Fatalf("summary snapshot before terminal = %s, want cached text", summary[key][0].SnapshotPayload)
	}

	closed, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-cache",
		EventID:     "evt-close",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1004,
		Payload:     []byte(`{"end_reason":2}`),
		UpdatedAt:   1005,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(close) error = %v", err)
	}
	if closed.MsgEventSeq != 1 || closed.Status != metadb.EventStatusClosed || closed.State.EndReason != 2 {
		t.Fatalf("close result = %#v, want one durable closed event", closed)
	}
	if !strings.Contains(string(closed.State.SnapshotPayload), "hello world") {
		t.Fatalf("closed snapshot = %s, want cached text merged", closed.State.SnapshotPayload)
	}

	states, err = node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-stream-cache", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(after terminal) error = %v", err)
	}
	if len(states) != 1 || states[0].LastMsgEventSeq != 1 || states[0].LastEventID != "evt-close" {
		t.Fatalf("states after terminal = %#v, want one closed durable state", states)
	}
	summary, err = node.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(after terminal) error = %v", err)
	}
	if len(summary[key]) != 1 || summary[key][0].LastMsgEventSeq != 1 || summary[key][0].Status != metadb.EventStatusClosed {
		t.Fatalf("summary after terminal = %#v, want one durable closed state", summary[key])
	}
}

func TestClusterMessageEventFacadeFinishFlushesCachedLanes(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-stream-finish"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for _, tc := range []struct {
		eventID string
		key     string
		delta   string
	}{
		{eventID: "evt-main-delta", key: "main", delta: "answer"},
		{eventID: "evt-tool-delta", key: "tool", delta: "lookup"},
	} {
		if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: "cmn-stream-finish",
			EventID:     tc.eventID,
			EventKey:    tc.key,
			EventType:   metadb.EventTypeStreamDelta,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  1000,
			Payload:     []byte(`{"kind":"text","delta":"` + tc.delta + `"}`),
			UpdatedAt:   1001,
		}); err != nil {
			t.Fatalf("AppendMessageEvent(%s delta) error = %v", tc.key, err)
		}
	}

	result, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-finish",
		EventID:     "evt-finish",
		EventType:   metadb.EventTypeStreamFinish,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"end_reason":3}`),
		UpdatedAt:   1003,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(finish) error = %v", err)
	}
	if result.EventKey != metadb.EventKeyFinish || result.Status != metadb.EventStatusClosed {
		t.Fatalf("finish result = %#v, want closed finish marker", result)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-stream-finish", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(after finish) error = %v", err)
	}
	byKey := make(map[string]metadb.MessageEventState, len(states))
	for _, state := range states {
		byKey[state.EventKey] = state
	}
	for _, want := range []struct {
		key  string
		text string
	}{
		{key: "main", text: "answer"},
		{key: "tool", text: "lookup"},
	} {
		state, ok := byKey[want.key]
		if !ok {
			t.Fatalf("durable states after finish = %#v, missing flushed %s lane", states, want.key)
		}
		if state.Status != metadb.EventStatusClosed || state.LastMsgEventSeq == 0 {
			t.Fatalf("%s lane after finish = %#v, want durable closed state", want.key, state)
		}
		if !strings.Contains(string(state.SnapshotPayload), want.text) {
			t.Fatalf("%s lane snapshot = %s, want cached text %q", want.key, state.SnapshotPayload, want.text)
		}
	}
	if _, ok := byKey[metadb.EventKeyFinish]; !ok {
		t.Fatalf("durable states after finish = %#v, missing finish marker", states)
	}
}

func TestClusterGetMessageEventStatesBatchRoutesEachMessage(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelA := channelLatestKeyForHashSlot(t, 1, 4)
	channelB := channelLatestKeyForHashSlot(t, 3, 4)
	waitRouteKeyLeaderReady(t, node, channelA)
	waitRouteKeyLeaderReady(t, node, channelB)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelA,
		ChannelType: 2,
		ClientMsgNo: "cmn-a",
		EventID:     "evt-a",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"snapshot":{"kind":"text","text":"a"}}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(channelA) error = %v", err)
	}
	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelB,
		ChannelType: 2,
		ClientMsgNo: "cmn-b",
		EventID:     "evt-b",
		EventKey:    "tool",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  2000,
		Payload:     []byte(`{"snapshot":{"kind":"text","text":"b"}}`),
		UpdatedAt:   2001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(channelB) error = %v", err)
	}

	keyA := metadb.MessageEventMessageKey{ChannelID: channelA, ChannelType: 2, ClientMsgNo: "cmn-a"}
	keyB := metadb.MessageEventMessageKey{ChannelID: channelB, ChannelType: 2, ClientMsgNo: "cmn-b"}
	missing := metadb.MessageEventMessageKey{ChannelID: channelA, ChannelType: 2, ClientMsgNo: "missing"}
	got, err := node.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{keyA, missing, keyB, keyA}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetMessageEventStatesBatch() returned %d keys, want 2: %#v", len(got), got)
	}
	if len(got[keyA]) != 1 || got[keyA][0].EventKey != "main" {
		t.Fatalf("states for keyA = %#v, want main lane", got[keyA])
	}
	if len(got[keyB]) != 1 || got[keyB][0].EventKey != "tool" {
		t.Fatalf("states for keyB = %#v, want tool lane", got[keyB])
	}
	if _, ok := got[missing]; ok {
		t.Fatalf("missing key returned states: %#v", got[missing])
	}
}

func TestClusterGetMessageEventStatesBatchReadsLeaderCacheFromFollower(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	channelID := "message-event-remote-cache"
	route := waitRouteKeyLeaderReady(t, nodes[0], channelID)
	follower := firstNonLeaderNode(t, nodes, route.Leader)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := follower.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-remote-cache",
		EventID:     "evt-remote-delta",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"remote"}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(follower delta) error = %v", err)
	}

	key := metadb.MessageEventMessageKey{ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-remote-cache"}
	got, err := follower.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(follower) error = %v", err)
	}
	if len(got[key]) != 1 || got[key][0].Status != metadb.EventStatusOpen || got[key][0].LastMsgEventSeq != 0 {
		t.Fatalf("follower state batch = %#v, want leader cached open state", got[key])
	}
	if !strings.Contains(string(got[key][0].SnapshotPayload), "remote") {
		t.Fatalf("follower snapshot = %s, want leader cached text", got[key][0].SnapshotPayload)
	}
}

func TestMergeMessageEventTerminalPayloadKeepsCachedSnapshot(t *testing.T) {
	snapshot := []byte(`{"kind":"text","text":"cached"}`)

	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "missing snapshot", payload: []byte(`{"end_reason":2}`)},
		{name: "null snapshot", payload: []byte(`{"snapshot":null,"end_reason":2}`)},
		{name: "invalid json", payload: []byte(`not-json`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeMessageEventTerminalPayload(tc.payload, snapshot)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(got, &body); err != nil {
				t.Fatalf("merged payload is invalid JSON: %s: %v", got, err)
			}
			if !strings.Contains(string(body["snapshot"]), "cached") {
				t.Fatalf("snapshot = %s, want cached snapshot in payload %s", body["snapshot"], got)
			}
			if tc.name == "invalid json" && !strings.Contains(string(body["raw_payload"]), "not-json") {
				t.Fatalf("raw_payload = %s, want original invalid payload preserved", body["raw_payload"])
			}
		})
	}
}

func TestMessageEventStreamCacheRejectsNewActiveSessionWhenFull(t *testing.T) {
	cache := newMessageEventStreamCache(1)
	first := metadb.MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		Payload:     []byte(`{"kind":"text","delta":"a"}`),
	}
	if _, err := cache.appendCached(first); err != nil {
		t.Fatalf("appendCached(first) error = %v", err)
	}
	second := first
	second.ClientMsgNo = "cmn-2"
	second.EventID = "evt-2"
	if _, err := cache.appendCached(second); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("appendCached(second) error = %v, want ErrBackpressured", err)
	}

	closeResult := metadb.MessageEventAppendResult{
		ChannelID:   first.ChannelID,
		ChannelType: first.ChannelType,
		ClientMsgNo: first.ClientMsgNo,
		EventID:     "evt-close",
		EventKey:    "main",
		MsgEventSeq: 1,
		Status:      metadb.EventStatusClosed,
		State: metadb.MessageEventState{
			ChannelID:   first.ChannelID,
			ChannelType: first.ChannelType,
			ClientMsgNo: first.ClientMsgNo,
			EventKey:    "main",
			Status:      metadb.EventStatusClosed,
		},
	}
	cache.markTerminalPersisted(metadb.MessageEventAppend{
		ChannelID:   first.ChannelID,
		ChannelType: first.ChannelType,
		ClientMsgNo: first.ClientMsgNo,
		EventID:     "evt-close",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
	}, closeResult)
	if _, err := cache.appendCached(second); err != nil {
		t.Fatalf("appendCached(second after terminal eviction) error = %v", err)
	}
}
