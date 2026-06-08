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

func TestConversationProjectionStoreClassifiesSubscribers(t *testing.T) {
	node := &conversationNodeFake{
		subscribers:    []string{"u1", "u2"},
		subscriberDone: true,
	}
	store := NewConversationProjectionStore(node)

	got, err := store.ClassifyMembers(context.Background(), "g-small", 2, 3)
	if err != nil {
		t.Fatalf("ClassifyMembers() error = %v", err)
	}
	if !got.IsSmall || len(got.Members) != 2 || got.Members[0].UID != "u1" || got.Members[1].UID != "u2" {
		t.Fatalf("member class = %#v, want complete small member snapshot", got)
	}
	if got, want := node.subscriberCalls, []subscriberPageCallFake{{channelID: "g-small", channelType: 2, afterUID: "", limit: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("subscriber calls = %#v, want %#v", got, want)
	}

	node = &conversationNodeFake{
		subscribers:    []string{"u1", "u2", "u3"},
		subscriberDone: false,
	}
	store = NewConversationProjectionStore(node)
	got, err = store.ClassifyMembers(context.Background(), "g-large", 2, 2)
	if err != nil {
		t.Fatalf("ClassifyMembers(large) error = %v", err)
	}
	if got.IsSmall || len(got.Members) != 2 {
		t.Fatalf("large member class = %#v, want bounded non-small page", got)
	}

	node = &conversationNodeFake{subscriberDone: true}
	store = NewConversationProjectionStore(node)
	got, err = store.ClassifyMembers(context.Background(), "g-empty", 2, 2)
	if err != nil {
		t.Fatalf("ClassifyMembers(empty) error = %v", err)
	}
	if got.IsSmall || len(got.Members) != 0 {
		t.Fatalf("empty member class = %#v, want sparse fallback", got)
	}
}

func TestConversationProjectionStoreClonesStateBatch(t *testing.T) {
	node := &conversationNodeFake{}
	store := NewConversationProjectionStore(node)
	states := []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10}}

	if err := store.UpsertUserConversationStatesBatch(context.Background(), states); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}
	states[0].ChannelID = "mutated"

	if got := node.stateBatches; len(got) != 1 || len(got[0]) != 1 || got[0][0].ChannelID != "g1" {
		t.Fatalf("state batches = %#v, want cloned original row", got)
	}
}

func TestConversationProjectionStoreTouchesActiveAtBatch(t *testing.T) {
	node := &conversationNodeFake{}
	store := NewConversationProjectionStore(node)
	patches := []metadb.UserConversationActivePatch{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 10}}

	if err := store.TouchUserConversationActiveAtBatch(context.Background(), patches); err != nil {
		t.Fatalf("TouchUserConversationActiveAtBatch() error = %v", err)
	}
	patches[0].ChannelID = "mutated"

	if got := node.activePatchBatches; len(got) != 1 || len(got[0]) != 1 || got[0][0].ChannelID != "g1" {
		t.Fatalf("active patch batches = %#v, want cloned original patch", got)
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

type subscriberPageCallFake struct {
	channelID   string
	channelType int64
	afterUID    string
	limit       int
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

	read           map[metadb.ConversationKey]lastVisibleResultFake
	readErr        map[metadb.ConversationKey]error
	readCalls      []readCallFake
	readGate       chan struct{}
	readStarted    chan struct{}
	activeReads    int
	maxActiveReads int

	subscribers     []string
	subscriberDone  bool
	subscriberCalls []subscriberPageCallFake
	stateBatches    [][]metadb.UserConversationState

	activePatchBatches [][]metadb.UserConversationActivePatch
}

func (n *conversationNodeFake) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	n.activeCalls = append(n.activeCalls, activePageCallFake{uid: uid, after: after, limit: limit})
	if n.err != nil {
		return nil, metadb.UserConversationActiveCursor{}, false, n.err
	}
	return n.rows, n.cursor, n.done, nil
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

func (n *conversationNodeFake) ListChannelSubscribersPage(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	n.subscriberCalls = append(n.subscriberCalls, subscriberPageCallFake{channelID: channelID, channelType: channelType, afterUID: afterUID, limit: limit})
	if limit <= 0 || len(n.subscribers) == 0 {
		return nil, "", true, nil
	}
	end := limit
	if end > len(n.subscribers) {
		end = len(n.subscribers)
	}
	page := append([]string(nil), n.subscribers[:end]...)
	cursor := ""
	if !n.subscriberDone && len(page) > 0 {
		cursor = page[len(page)-1]
	}
	return page, cursor, n.subscriberDone, nil
}

func (n *conversationNodeFake) UpsertUserConversationStatesBatch(_ context.Context, states []metadb.UserConversationState) error {
	n.stateBatches = append(n.stateBatches, append([]metadb.UserConversationState(nil), states...))
	return nil
}

func (n *conversationNodeFake) TouchUserConversationActiveAtBatch(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	n.activePatchBatches = append(n.activePatchBatches, append([]metadb.UserConversationActivePatch(nil), patches...))
	return nil
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
