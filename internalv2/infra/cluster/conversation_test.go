package cluster

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
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

func TestConversationStoreWrapsActivePageAsView(t *testing.T) {
	node := &conversationNodeFake{
		rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
	}
	store := NewConversationStore(node)
	page, err := store.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" || page.Done {
		t.Fatalf("page = %#v, want one row and fake done=false", page)
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

func TestConversationStoreReadsDurableStateAndRecentMessages(t *testing.T) {
	node := &conversationNodeFake{
		states: map[metadb.ConversationKey]metadb.UserConversationState{
			{ChannelID: "g-a", ChannelType: 2}: {UID: "u1", ChannelID: "g-a", ChannelType: 2, ReadSeq: 7},
		},
		committed: map[metadb.ConversationKey][]channelv2.Message{
			{ChannelID: "g-a", ChannelType: 2}: {{
				MessageID:         12,
				MessageSeq:        12,
				ChannelID:         "g-a",
				ChannelType:       2,
				FromUID:           "u2",
				ClientMsgNo:       "client-12",
				ServerTimestampMS: 900,
				Payload:           []byte("recent"),
			}},
		},
	}
	store := NewConversationStore(node)

	state, ok, err := store.GetUserConversationState(context.Background(), "u1", "g-a", 2)
	if err != nil || !ok || state.ReadSeq != 7 {
		t.Fatalf("GetUserConversationState() state=%#v ok=%v err=%v, want read seq 7", state, ok, err)
	}
	recent, err := store.GetRecentMessages(context.Background(), []conversationusecase.ConversationKey{{ChannelID: "g-a", ChannelType: 2}}, 2)
	if err != nil {
		t.Fatalf("GetRecentMessages() error = %v", err)
	}
	key := conversationusecase.ConversationKey{ChannelID: "g-a", ChannelType: 2}
	if len(recent[key]) != 1 || recent[key][0].MessageID != 12 || string(recent[key][0].Payload) != "recent" {
		t.Fatalf("recent messages = %#v, want durable fields", recent[key])
	}
	recent[key][0].Payload[0] = 'X'
	again, err := store.GetRecentMessages(context.Background(), []conversationusecase.ConversationKey{key}, 2)
	if err != nil {
		t.Fatalf("GetRecentMessages(again) error = %v", err)
	}
	if string(again[key][0].Payload) != "recent" {
		t.Fatalf("recent message payload aliases node storage: %q", again[key][0].Payload)
	}
	if got, want := node.committedCalls, []committedCallFake{
		{channelID: channelv2.ChannelID{ID: "g-a", Type: 2}, req: channelstore.ReadCommittedRequest{FromSeq: maxUint64(), MaxSeq: maxUint64(), Limit: 2, MaxBytes: maxInt(), Reverse: true}},
		{channelID: channelv2.ChannelID{ID: "g-a", Type: 2}, req: channelstore.ReadCommittedRequest{FromSeq: maxUint64(), MaxSeq: maxUint64(), Limit: 2, MaxBytes: maxInt(), Reverse: true}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed calls = %#v, want %#v", got, want)
	}
}

func TestConversationStoreBoundsLastVisibleMessageConcurrency(t *testing.T) {
	node := &conversationNodeFake{
		read: map[metadb.ConversationKey]lastVisibleResultFake{
			{ChannelID: "g-a", ChannelType: 2}: {msg: channelv2.Message{MessageID: 1}, ok: true},
			{ChannelID: "g-b", ChannelType: 2}: {msg: channelv2.Message{MessageID: 2}, ok: true},
			{ChannelID: "g-c", ChannelType: 2}: {msg: channelv2.Message{MessageID: 3}, ok: true},
		},
		readGate:    make(chan struct{}),
		readStarted: make(chan struct{}, 3),
	}
	store := NewConversationStore(node, ConversationStoreOptions{MaxLastMessageConcurrency: 2})
	done := make(chan error, 1)

	go func() {
		_, err := store.GetLastVisibleMessages(context.Background(), []conversationusecase.LastVisibleMessageRequest{
			{ChannelID: "g-a", ChannelType: 2},
			{ChannelID: "g-b", ChannelType: 2},
			{ChannelID: "g-c", ChannelType: 2},
		})
		done <- err
	}()

	<-node.readStarted
	<-node.readStarted
	select {
	case <-node.readStarted:
		t.Fatal("third tail read started before a bounded worker was released")
	case <-time.After(20 * time.Millisecond):
	}
	close(node.readGate)
	if err := <-done; err != nil {
		t.Fatalf("GetLastVisibleMessages() error = %v", err)
	}
	if node.maxActiveReads != 2 {
		t.Fatalf("max active tail reads = %d, want 2", node.maxActiveReads)
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

type committedCallFake struct {
	channelID channelv2.ChannelID
	req       channelstore.ReadCommittedRequest
}

type lastVisibleResultFake struct {
	msg channelv2.Message
	ok  bool
}

type conversationNodeFake struct {
	mu          sync.Mutex
	rows        []metadb.UserConversationState
	cursor      metadb.UserConversationActiveCursor
	done        bool
	err         error
	activeCalls []activePageCallFake
	states      map[metadb.ConversationKey]metadb.UserConversationState
	stateCalls  []metadb.ConversationKey

	read           map[metadb.ConversationKey]lastVisibleResultFake
	readErr        map[metadb.ConversationKey]error
	readCalls      []readCallFake
	readGate       chan struct{}
	readStarted    chan struct{}
	activeReads    int
	maxActiveReads int

	committed      map[metadb.ConversationKey][]channelv2.Message
	committedErr   map[metadb.ConversationKey]error
	committedCalls []committedCallFake
}

func (n *conversationNodeFake) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	n.activeCalls = append(n.activeCalls, activePageCallFake{uid: uid, after: after, limit: limit})
	if n.err != nil {
		return nil, metadb.UserConversationActiveCursor{}, false, n.err
	}
	return n.rows, n.cursor, n.done, nil
}

func (n *conversationNodeFake) GetUserConversationState(_ context.Context, _ string, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	key := metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}
	n.stateCalls = append(n.stateCalls, key)
	state, ok := n.states[key]
	return state, ok, nil
}

func (n *conversationNodeFake) ReadChannelLastVisible(_ context.Context, id channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	n.mu.Lock()
	n.readCalls = append(n.readCalls, readCallFake{channelID: id, visibleAfterSeq: visibleAfterSeq})
	n.activeReads++
	if n.activeReads > n.maxActiveReads {
		n.maxActiveReads = n.activeReads
	}
	started := n.readStarted
	gate := n.readGate
	n.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if gate != nil {
		<-gate
	}
	defer func() {
		n.mu.Lock()
		n.activeReads--
		n.mu.Unlock()
	}()
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

func (n *conversationNodeFake) ReadChannelCommitted(_ context.Context, id channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	n.committedCalls = append(n.committedCalls, committedCallFake{channelID: id, req: req})
	if err := n.committedErr[key]; err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	messages, ok := n.committed[key]
	if !ok {
		return channelstore.ReadCommittedResult{}, channelv2.ErrChannelNotFound
	}
	out := append([]channelv2.Message(nil), messages...)
	for i := range out {
		out[i].Payload = append([]byte(nil), out[i].Payload...)
	}
	return channelstore.ReadCommittedResult{Messages: out}, nil
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
