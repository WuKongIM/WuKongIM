package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestMessageEventStoreAppendMapsAndClonesPayloads(t *testing.T) {
	node := &messageEventNodeFake{
		appendResult: metadb.MessageEventAppendResult{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: "cmn-1",
			EventID:     "evt-1",
			EventKey:    "main",
			MsgEventSeq: 3,
			Status:      metadb.EventStatusOpen,
			State: metadb.MessageEventState{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        "main",
				Status:          metadb.EventStatusOpen,
				LastMsgEventSeq: 3,
				SnapshotPayload: []byte("snapshot"),
			},
		},
	}
	store := NewMessageEventStore(node)
	payload := []byte("payload")

	got, err := store.AppendMessageEvent(context.Background(), message.MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   message.EventTypeStreamDelta,
		Visibility:  message.VisibilityPublic,
		OccurredAt:  10,
		Payload:     payload,
		UpdatedAt:   11,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	payload[0] = 'X'
	if string(node.appendEvent.Payload) != "payload" {
		t.Fatalf("node payload = %q, want cloned payload", node.appendEvent.Payload)
	}
	if got.MsgEventSeq != 3 || got.State.LastMsgEventSeq != 3 || string(got.State.SnapshotPayload) != "snapshot" {
		t.Fatalf("result = %#v, want mapped append result", got)
	}
	got.State.SnapshotPayload[0] = 'X'
	if string(node.appendResult.State.SnapshotPayload) != "snapshot" {
		t.Fatalf("result payload aliases node result")
	}
}

func TestMessageEventStoreGetStatesBatchMapsAndClonesRows(t *testing.T) {
	key := message.MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	nodeKey := metadb.MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	node := &messageEventNodeFake{
		states: map[metadb.MessageEventMessageKey][]metadb.MessageEventState{
			nodeKey: {{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        "main",
				Status:          metadb.EventStatusOpen,
				LastMsgEventSeq: 4,
				SnapshotPayload: []byte("snapshot"),
			}},
		},
	}
	store := NewMessageEventStore(node)

	got, err := store.GetMessageEventStatesBatch(context.Background(), []message.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch() error = %v", err)
	}
	if node.limit != 10 || !reflect.DeepEqual(node.keys, []metadb.MessageEventMessageKey{nodeKey}) {
		t.Fatalf("node call keys=%#v limit=%d, want mapped key limit=10", node.keys, node.limit)
	}
	if len(got[key]) != 1 || got[key][0].LastMsgEventSeq != 4 || string(got[key][0].SnapshotPayload) != "snapshot" {
		t.Fatalf("states = %#v, want mapped state", got)
	}
	got[key][0].SnapshotPayload[0] = 'X'
	if string(node.states[nodeKey][0].SnapshotPayload) != "snapshot" {
		t.Fatalf("state payload aliases node state")
	}
}

func TestMessageEventStoreRequiresNode(t *testing.T) {
	store := NewMessageEventStore(nil)
	_, err := store.AppendMessageEvent(context.Background(), message.MessageEventAppend{})
	if !errors.Is(err, message.ErrMessageEventStoreRequired) {
		t.Fatalf("AppendMessageEvent() error = %v, want store required", err)
	}

	_, err = store.GetMessageEventStatesBatch(context.Background(), []message.MessageEventMessageKey{{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn"}}, 10)
	if !errors.Is(err, message.ErrMessageEventStoreRequired) {
		t.Fatalf("GetMessageEventStatesBatch() error = %v, want store required", err)
	}
}

func TestMessageEventStoreMapsRouteErrors(t *testing.T) {
	store := NewMessageEventStore(&messageEventNodeFake{err: pkgcluster.ErrNoSlotLeader})

	_, err := store.AppendMessageEvent(context.Background(), message.MessageEventAppend{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn", EventID: "evt", EventType: message.EventTypeStreamDelta})
	if !errors.Is(err, message.ErrRouteNotReady) {
		t.Fatalf("AppendMessageEvent() error = %v, want route not ready", err)
	}
}

type messageEventNodeFake struct {
	appendEvent  metadb.MessageEventAppend
	appendResult metadb.MessageEventAppendResult
	keys         []metadb.MessageEventMessageKey
	limit        int
	states       map[metadb.MessageEventMessageKey][]metadb.MessageEventState
	err          error
}

func (n *messageEventNodeFake) AppendMessageEvent(_ context.Context, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	n.appendEvent = event
	if n.err != nil {
		return metadb.MessageEventAppendResult{}, n.err
	}
	return n.appendResult, nil
}

func (n *messageEventNodeFake) GetMessageEventStatesBatch(_ context.Context, keys []metadb.MessageEventMessageKey, limit int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error) {
	n.keys = append([]metadb.MessageEventMessageKey(nil), keys...)
	n.limit = limit
	if n.err != nil {
		return nil, n.err
	}
	return n.states, nil
}
