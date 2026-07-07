package fsm

import (
	"context"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestStateMachineAppliesMessageEventAppend(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	resultBytes, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		Index:    1,
		Term:     1,
		HashSlot: 11,
		Data: EncodeAppendMessageEventCommand(metadb.MessageEventAppend{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: "cmn-1",
			EventID:     "evt-1",
			EventKey:    "main",
			EventType:   metadb.EventTypeStreamDelta,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  1000,
			Payload:     []byte(`{"kind":"text","delta":"hi"}`),
			UpdatedAt:   1001,
		}),
	})
	if err != nil {
		t.Fatalf("Apply(message event) error = %v", err)
	}
	result, err := DecodeAppendMessageEventResult(resultBytes)
	if err != nil {
		t.Fatalf("DecodeAppendMessageEventResult() error = %v", err)
	}
	if result.MsgEventSeq != 1 || result.Status != metadb.EventStatusOpen || result.EventKey != "main" {
		t.Fatalf("result = %#v, want seq=1 open main", result)
	}
	if !strings.Contains(string(result.State.SnapshotPayload), "hi") {
		t.Fatalf("snapshot payload = %s, want text delta applied", result.State.SnapshotPayload)
	}

	states, err := db.ForHashSlot(11).ListMessageEventStates(ctx, "g1", 2, "cmn-1", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates() error = %v", err)
	}
	if len(states) != 1 || states[0].LastMsgEventSeq != 1 || states[0].LastEventID != "evt-1" {
		t.Fatalf("states = %#v, want one persisted event state", states)
	}
}

func TestStateMachineMessageEventAppendNormalizesOpenAndDefaultKey(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	resultBytes, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		Index:    1,
		Term:     1,
		HashSlot: 11,
		Data: EncodeAppendMessageEventCommand(metadb.MessageEventAppend{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: "cmn-open",
			EventID:     "evt-open",
			EventType:   metadb.EventTypeStreamOpen,
			OccurredAt:  1000,
			UpdatedAt:   1001,
		}),
	})
	if err != nil {
		t.Fatalf("Apply(message event open) error = %v", err)
	}
	result, err := DecodeAppendMessageEventResult(resultBytes)
	if err != nil {
		t.Fatalf("DecodeAppendMessageEventResult() error = %v", err)
	}
	if result.EventKey != metadb.EventKeyDefault || result.Status != metadb.EventStatusOpen || result.MsgEventSeq != 1 {
		t.Fatalf("result = %#v, want default key open seq=1", result)
	}
	if result.State.LastVisibility != metadb.VisibilityPublic {
		t.Fatalf("visibility = %q, want public default", result.State.LastVisibility)
	}
}

func TestStateMachineAppliesMessageEventAppendBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	resultBytes, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		Index:    1,
		Term:     1,
		HashSlot: 11,
		Data: EncodeAppendMessageEventsCommand([]metadb.MessageEventAppend{
			{
				ChannelID:   "g1",
				ChannelType: 2,
				ClientMsgNo: "cmn-batch",
				EventID:     "evt-close-main",
				EventKey:    "main",
				EventType:   metadb.EventTypeStreamClose,
				Visibility:  metadb.VisibilityPublic,
				OccurredAt:  1000,
				Payload:     []byte(`{"snapshot":{"kind":"text","text":"hi"}}`),
				UpdatedAt:   1001,
			},
			{
				ChannelID:   "g1",
				ChannelType: 2,
				ClientMsgNo: "cmn-batch",
				EventID:     "evt-finish",
				EventType:   metadb.EventTypeStreamFinish,
				Visibility:  metadb.VisibilityPublic,
				OccurredAt:  1002,
				UpdatedAt:   1003,
			},
		}),
	})
	if err != nil {
		t.Fatalf("Apply(message event batch) error = %v", err)
	}
	result, err := DecodeAppendMessageEventResult(resultBytes)
	if err != nil {
		t.Fatalf("DecodeAppendMessageEventResult() error = %v", err)
	}
	if result.EventKey != metadb.EventKeyFinish || result.MsgEventSeq != 2 || result.Status != metadb.EventStatusClosed {
		t.Fatalf("batch result = %#v, want finish seq=2 closed", result)
	}
	states, err := db.ForHashSlot(11).ListMessageEventStates(ctx, "g1", 2, "cmn-batch", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates() error = %v", err)
	}
	byKey := make(map[string]metadb.MessageEventState, len(states))
	for _, state := range states {
		byKey[state.EventKey] = state
	}
	if byKey["main"].LastMsgEventSeq != 1 || byKey["main"].Status != metadb.EventStatusClosed {
		t.Fatalf("main state = %#v, want closed seq=1", byKey["main"])
	}
	if byKey[metadb.EventKeyFinish].LastMsgEventSeq != 2 || byKey[metadb.EventKeyFinish].Status != metadb.EventStatusClosed {
		t.Fatalf("finish state = %#v, want closed seq=2", byKey[metadb.EventKeyFinish])
	}
}

func TestStateMachineAppliesMessageEventAppendBatchAcrossMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	resultBytes, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		Index:    1,
		Term:     1,
		HashSlot: 11,
		Data: EncodeAppendMessageEventsCommand([]metadb.MessageEventAppend{
			{
				ChannelID:   "g1",
				ChannelType: 2,
				ClientMsgNo: "cmn-a",
				EventID:     "evt-finish-a",
				EventType:   metadb.EventTypeStreamFinish,
				Visibility:  metadb.VisibilityPublic,
				OccurredAt:  1000,
				Payload:     []byte(`{"snapshot":{"kind":"text","text":"a"}}`),
				UpdatedAt:   1001,
			},
			{
				ChannelID:   "g1",
				ChannelType: 2,
				ClientMsgNo: "cmn-b",
				EventID:     "evt-finish-b",
				EventType:   metadb.EventTypeStreamFinish,
				Visibility:  metadb.VisibilityPublic,
				OccurredAt:  1002,
				Payload:     []byte(`{"snapshot":{"kind":"text","text":"b"}}`),
				UpdatedAt:   1003,
			},
		}),
	})
	if err != nil {
		t.Fatalf("Apply(message event mixed-message batch) error = %v", err)
	}
	results, err := DecodeAppendMessageEventResults(resultBytes)
	if err != nil {
		t.Fatalf("DecodeAppendMessageEventResults() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("batch results = %#v, want two per-message results", results)
	}
	if results[0].ClientMsgNo != "cmn-a" || results[0].EventID != "evt-finish-a" || results[0].MsgEventSeq != 1 {
		t.Fatalf("first result = %#v, want cmn-a finish seq=1", results[0])
	}
	if results[1].ClientMsgNo != "cmn-b" || results[1].EventID != "evt-finish-b" || results[1].MsgEventSeq != 1 {
		t.Fatalf("second result = %#v, want cmn-b finish seq=1", results[1])
	}
	for _, clientMsgNo := range []string{"cmn-a", "cmn-b"} {
		states, err := db.ForHashSlot(11).ListMessageEventStates(ctx, "g1", 2, clientMsgNo, 10)
		if err != nil {
			t.Fatalf("ListMessageEventStates(%s) error = %v", clientMsgNo, err)
		}
		if len(states) != 1 || states[0].EventKey != metadb.EventKeyFinish || states[0].LastMsgEventSeq != 1 {
			t.Fatalf("states for %s = %#v, want one finish marker at seq=1", clientMsgNo, states)
		}
	}
}

func TestEncodeAppendMessageEventCommandCheckedRejectsInvalidEvent(t *testing.T) {
	_, err := EncodeAppendMessageEventCommandChecked(metadb.MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 0,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventType:   metadb.EventTypeStreamDelta,
	})
	if err == nil {
		t.Fatal("EncodeAppendMessageEventCommandChecked() error = nil, want invalid argument")
	}
}

func TestEncodeAppendMessageEventsCommandCheckedRejectsMixedChannel(t *testing.T) {
	_, err := EncodeAppendMessageEventsCommandChecked([]metadb.MessageEventAppend{
		{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: "cmn-1",
			EventID:     "evt-1",
			EventType:   metadb.EventTypeStreamClose,
		},
		{
			ChannelID:   "g2",
			ChannelType: 2,
			ClientMsgNo: "cmn-2",
			EventID:     "evt-2",
			EventType:   metadb.EventTypeStreamFinish,
		},
	})
	if err == nil {
		t.Fatal("EncodeAppendMessageEventsCommandChecked() error = nil, want invalid argument for mixed channel keys")
	}
}
