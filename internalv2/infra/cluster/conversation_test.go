package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationStoreListsActivePageAndClonesRows(t *testing.T) {
	node := &conversationNodeFake{
		rows: []metadb.UserConversationState{{
			UID:         "u1",
			ChannelID:   "g-a",
			ChannelType: 2,
			ActiveAt:    300,
		}},
		cursor: metadb.UserConversationActiveCursor{ActiveAt: 300, ChannelID: "g-a", ChannelType: 2},
		done:   true,
	}
	store := NewConversationStore(node)

	got, cursor, done, err := store.ListUserConversationActivePage(context.Background(), "u1", metadb.UserConversationActiveCursor{ActiveAt: 400, ChannelID: "prev", ChannelType: 2}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage() error = %v", err)
	}
	if !done || cursor != node.cursor {
		t.Fatalf("done=%v cursor=%#v, want done true cursor %#v", done, cursor, node.cursor)
	}
	if got, want := node.activeCalls, []activePageCallFake{{uid: "u1", after: metadb.UserConversationActiveCursor{ActiveAt: 400, ChannelID: "prev", ChannelType: 2}, limit: 10}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active calls = %#v, want %#v", got, want)
	}
	got[0].ChannelID = "mutated"
	if node.rows[0].ChannelID != "g-a" {
		t.Fatalf("node row was aliased by adapter result")
	}
}

func TestConversationStoreReadsLastVisibleMessages(t *testing.T) {
	node := &conversationNodeFake{
		read: map[metadb.ConversationKey]lastVisibleResultFake{
			{ChannelID: "g-a", ChannelType: 2}: {msg: channelv2.Message{
				MessageID:         12,
				MessageSeq:        12,
				ChannelID:         "g-a",
				ChannelType:       2,
				FromUID:           "u2",
				ClientMsgNo:       "client-12",
				ServerTimestampMS: 900,
				Payload:           []byte("visible"),
			}, ok: true},
			{ChannelID: "g-b", ChannelType: 2}: {msg: channelv2.Message{
				MessageID:  5,
				MessageSeq: 5,
				ChannelID:  "g-b",
			}, ok: false},
		},
	}
	store := NewConversationStore(node)

	got, err := store.GetLastVisibleMessages(context.Background(), []conversationusecase.LastVisibleMessageRequest{
		{ChannelID: "g-a", ChannelType: 2, VisibleAfterSeq: 10},
		{ChannelID: "g-b", ChannelType: 2, VisibleAfterSeq: 5},
		{ChannelID: "missing", ChannelType: 2},
	})
	if err != nil {
		t.Fatalf("GetLastVisibleMessages() error = %v", err)
	}
	keyA := metadb.ConversationKey{ChannelID: "g-a", ChannelType: 2}
	msg, ok := got[keyA]
	if !ok {
		t.Fatalf("missing visible message for %#v", keyA)
	}
	if msg.MessageID != 12 || msg.MessageSeq != 12 || msg.FromUID != "u2" || msg.ClientMsgNo != "client-12" || msg.ServerTimestampMS != 900 || string(msg.Payload) != "visible" {
		t.Fatalf("last message = %#v, want durable channel message fields", msg)
	}
	if _, ok := got[metadb.ConversationKey{ChannelID: "g-b", ChannelType: 2}]; ok {
		t.Fatalf("g-b message at visible floor was returned")
	}
	if _, ok := got[metadb.ConversationKey{ChannelID: "missing", ChannelType: 2}]; ok {
		t.Fatalf("missing channel returned a last message")
	}
	if got, want := node.readCalls, []readCallFake{
		{channelID: channelv2.ChannelID{ID: "g-a", Type: 2}, visibleAfterSeq: 10},
		{channelID: channelv2.ChannelID{ID: "g-b", Type: 2}, visibleAfterSeq: 5},
		{channelID: channelv2.ChannelID{ID: "missing", Type: 2}, visibleAfterSeq: 0},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("read calls = %#v, want %#v", got, want)
	}

	msg.Payload[0] = 'X'
	again, err := store.GetLastVisibleMessages(context.Background(), []conversationusecase.LastVisibleMessageRequest{{ChannelID: "g-a", ChannelType: 2}})
	if err != nil {
		t.Fatalf("GetLastVisibleMessages(again) error = %v", err)
	}
	if string(again[keyA].Payload) != "visible" {
		t.Fatalf("adapter result payload aliases node storage: %q", again[keyA].Payload)
	}
}

type activePageCallFake struct {
	uid   string
	after metadb.UserConversationActiveCursor
	limit int
}

type readCallFake struct {
	channelID       channelv2.ChannelID
	visibleAfterSeq uint64
}

type lastVisibleResultFake struct {
	msg channelv2.Message
	ok  bool
}

type conversationNodeFake struct {
	rows        []metadb.UserConversationState
	cursor      metadb.UserConversationActiveCursor
	done        bool
	err         error
	activeCalls []activePageCallFake

	read      map[metadb.ConversationKey]lastVisibleResultFake
	readErr   map[metadb.ConversationKey]error
	readCalls []readCallFake
}

func (n *conversationNodeFake) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	n.activeCalls = append(n.activeCalls, activePageCallFake{uid: uid, after: after, limit: limit})
	if n.err != nil {
		return nil, metadb.UserConversationActiveCursor{}, false, n.err
	}
	return n.rows, n.cursor, n.done, nil
}

func (n *conversationNodeFake) ReadChannelLastVisible(_ context.Context, id channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	n.readCalls = append(n.readCalls, readCallFake{channelID: id, visibleAfterSeq: visibleAfterSeq})
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	if err := n.readErr[key]; err != nil {
		return channelv2.Message{}, false, err
	}
	result, ok := n.read[key]
	if !ok {
		return channelv2.Message{}, false, channelv2.ErrChannelNotFound
	}
	return result.msg, result.ok, nil
}

func TestConversationStorePropagatesLastMessageRouteErrors(t *testing.T) {
	routeErr := errors.New("route unavailable")
	node := &conversationNodeFake{
		read:    map[metadb.ConversationKey]lastVisibleResultFake{},
		readErr: map[metadb.ConversationKey]error{{ChannelID: "g-a", ChannelType: 2}: routeErr},
	}
	store := NewConversationStore(node)

	_, err := store.GetLastVisibleMessages(context.Background(), []conversationusecase.LastVisibleMessageRequest{{ChannelID: "g-a", ChannelType: 2}})
	if !errors.Is(err, routeErr) {
		t.Fatalf("GetLastVisibleMessages() error = %v, want route error", err)
	}
}
