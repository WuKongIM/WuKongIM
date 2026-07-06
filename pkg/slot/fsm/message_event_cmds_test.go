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
