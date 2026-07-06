package cluster

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterMessageEventFacadeAppendsThroughChannelHashSlot(t *testing.T) {
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
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"hello"}`),
		UpdatedAt:   1001,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	if result.MsgEventSeq != 1 || result.Status != metadb.EventStatusOpen {
		t.Fatalf("append result = %#v, want seq=1 open", result)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-event-1", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(channel hash slot) error = %v", err)
	}
	if len(states) != 1 || states[0].LastMsgEventSeq != 1 || states[0].LastEventID != "evt-1" {
		t.Fatalf("stored states = %#v, want one state at seq 1", states)
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
		EventType:   metadb.EventTypeStreamSnapshot,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","text":"a"}`),
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
		EventType:   metadb.EventTypeStreamSnapshot,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  2000,
		Payload:     []byte(`{"kind":"text","text":"b"}`),
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
