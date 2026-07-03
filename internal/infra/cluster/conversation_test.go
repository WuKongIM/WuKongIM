package cluster

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationStoreListsActivePageAndClonesRows(t *testing.T) {
	node := &conversationNodeFake{
		rows: []metadb.ConversationState{{
			UID:         "u1",
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   "g-a",
			ChannelType: 2,
			ActiveAt:    300,
		}},
		cursor: metadb.ConversationActiveCursor{ActiveAt: 300, ChannelID: "g-a", ChannelType: 2},
		done:   true,
	}
	store := NewConversationStore(node)

	got, cursor, done, err := store.ListConversationActivePage(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{ActiveAt: 400, ChannelID: "prev", ChannelType: 2}, 10)
	if err != nil {
		t.Fatalf("ListConversationActivePage() error = %v", err)
	}
	if !done || cursor != node.cursor {
		t.Fatalf("done=%v cursor=%#v, want done true cursor %#v", done, cursor, node.cursor)
	}
	if got, want := node.activeCalls, []activePageCallFake{{kind: metadb.ConversationKindNormal, uid: "u1", after: metadb.ConversationActiveCursor{ActiveAt: 400, ChannelID: "prev", ChannelType: 2}, limit: 10}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active calls = %#v, want %#v", got, want)
	}
	got[0].ChannelID = "mutated"
	if node.rows[0].ChannelID != "g-a" {
		t.Fatalf("node row was aliased by adapter result")
	}
}

func TestConversationStoreWrapsActivePageAsView(t *testing.T) {
	node := &conversationNodeFake{
		rows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
	}
	store := NewConversationStore(node)
	page, err := store.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" || page.Done {
		t.Fatalf("page = %#v, want one row and fake done=false", page)
	}
}

func TestConversationStoreUpsertsConversationStatesThroughMutationNode(t *testing.T) {
	node := &conversationNodeFake{}
	store := NewConversationStore(node)
	states := []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 9}}

	if err := store.UpsertConversationStates(context.Background(), states); err != nil {
		t.Fatalf("UpsertConversationStates() error = %v", err)
	}
	states[0].ReadSeq = 1

	if got, want := node.upserts, []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 9}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upserts = %#v, want %#v", got, want)
	}
}

func TestConversationStoreHidesConversationsThroughMutationNode(t *testing.T) {
	node := &conversationNodeFake{}
	store := NewConversationStore(node)
	deletes := []metadb.ConversationDelete{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 12}}

	if err := store.HideConversations(context.Background(), deletes); err != nil {
		t.Fatalf("HideConversations() error = %v", err)
	}
	deletes[0].DeletedToSeq = 1

	if got, want := node.deletes, []metadb.ConversationDelete{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 12}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deletes = %#v, want %#v", got, want)
	}
}

func TestConversationStoreReadsLastVisibleMessages(t *testing.T) {
	node := &conversationNodeFake{
		read: map[metadb.ConversationKey]lastVisibleResultFake{
			{ChannelID: "g-a", ChannelType: 2}: {msg: channelruntime.Message{
				MessageID:         12,
				MessageSeq:        12,
				ChannelID:         "g-a",
				ChannelType:       2,
				FromUID:           "u2",
				ClientMsgNo:       "client-12",
				ServerTimestampMS: 900,
				Payload:           []byte("visible"),
			}, ok: true},
			{ChannelID: "g-b", ChannelType: 2}: {msg: channelruntime.Message{
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
		{channelID: channelruntime.ChannelID{ID: "g-a", Type: 2}, visibleAfterSeq: 10},
		{channelID: channelruntime.ChannelID{ID: "g-b", Type: 2}, visibleAfterSeq: 5},
		{channelID: channelruntime.ChannelID{ID: "missing", Type: 2}, visibleAfterSeq: 0},
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

func TestConversationStoreSkipsSyncOnceTailForLastVisibleMessage(t *testing.T) {
	node := &conversationNodeFake{
		read: map[metadb.ConversationKey]lastVisibleResultFake{
			{ChannelID: "g-a", ChannelType: 2}: {msg: channelruntime.Message{
				MessageID:   13,
				MessageSeq:  13,
				ChannelID:   "g-a",
				ChannelType: 2,
				ClientMsgNo: "cmd-13",
				SyncOnce:    true,
			}, ok: true},
		},
		committed: map[metadb.ConversationKey][]channelruntime.Message{
			{ChannelID: "g-a", ChannelType: 2}: {{
				MessageID:         12,
				MessageSeq:        12,
				ChannelID:         "g-a",
				ChannelType:       2,
				FromUID:           "u2",
				ClientMsgNo:       "normal-12",
				ServerTimestampMS: 900,
				Payload:           []byte("normal"),
			}},
		},
	}
	store := NewConversationStore(node)

	got, err := store.GetLastVisibleMessages(context.Background(), []conversationusecase.LastVisibleMessageRequest{{ChannelID: "g-a", ChannelType: 2}})
	if err != nil {
		t.Fatalf("GetLastVisibleMessages() error = %v", err)
	}
	key := metadb.ConversationKey{ChannelID: "g-a", ChannelType: 2}
	msg, ok := got[key]
	if !ok || msg.ClientMsgNo != "normal-12" || msg.MessageSeq != 12 {
		t.Fatalf("last message = %#v ok=%v, want latest ordinary message", msg, ok)
	}
	if got, want := node.committedCalls, []committedCallFake{{
		channelID: channelruntime.ChannelID{ID: "g-a", Type: 2},
		req:       channelstore.ReadCommittedRequest{FromSeq: 12, MaxSeq: maxUint64(), Limit: conversationReadMinPageLimit, MaxBytes: maxInt(), Reverse: true},
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed calls = %#v, want %#v", got, want)
	}
}

func TestConversationStoreReadsDurableStateAndRecentMessages(t *testing.T) {
	node := &conversationNodeFake{
		states: map[metadb.ConversationKey]metadb.ConversationState{
			{ChannelID: "g-a", ChannelType: 2}: {UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g-a", ChannelType: 2, ReadSeq: 7},
		},
		committed: map[metadb.ConversationKey][]channelruntime.Message{
			{ChannelID: "g-a", ChannelType: 2}: {
				{
					MessageID:   13,
					MessageSeq:  13,
					ChannelID:   "g-a",
					ChannelType: 2,
					ClientMsgNo: "cmd-13",
					SyncOnce:    true,
				},
				{
					MessageID:         12,
					MessageSeq:        12,
					ChannelID:         "g-a",
					ChannelType:       2,
					FromUID:           "u2",
					ClientMsgNo:       "client-12",
					ServerTimestampMS: 900,
					Payload:           []byte("recent"),
				},
			},
		},
	}
	store := NewConversationStore(node)

	state, ok, err := store.GetConversationState(context.Background(), metadb.ConversationKindNormal, "u1", "g-a", 2)
	if err != nil || !ok || state.ReadSeq != 7 {
		t.Fatalf("GetConversationState() state=%#v ok=%v err=%v, want read seq 7", state, ok, err)
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
		{channelID: channelruntime.ChannelID{ID: "g-a", Type: 2}, req: channelstore.ReadCommittedRequest{FromSeq: maxUint64(), MaxSeq: maxUint64(), Limit: conversationReadMinPageLimit, MaxBytes: maxInt(), Reverse: true}},
		{channelID: channelruntime.ChannelID{ID: "g-a", Type: 2}, req: channelstore.ReadCommittedRequest{FromSeq: maxUint64(), MaxSeq: maxUint64(), Limit: conversationReadMinPageLimit, MaxBytes: maxInt(), Reverse: true}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed calls = %#v, want %#v", got, want)
	}
}

func TestConversationStoreBoundsLastVisibleMessageConcurrency(t *testing.T) {
	node := &conversationNodeFake{
		read: map[metadb.ConversationKey]lastVisibleResultFake{
			{ChannelID: "g-a", ChannelType: 2}: {msg: channelruntime.Message{MessageID: 1}, ok: true},
			{ChannelID: "g-b", ChannelType: 2}: {msg: channelruntime.Message{MessageID: 2}, ok: true},
			{ChannelID: "g-c", ChannelType: 2}: {msg: channelruntime.Message{MessageID: 3}, ok: true},
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
	kind  metadb.ConversationKind
	uid   string
	after metadb.ConversationActiveCursor
	limit int
}

type readCallFake struct {
	channelID       channelruntime.ChannelID
	visibleAfterSeq uint64
}

type committedCallFake struct {
	channelID channelruntime.ChannelID
	req       channelstore.ReadCommittedRequest
}

type lastVisibleResultFake struct {
	msg channelruntime.Message
	ok  bool
}

type conversationNodeFake struct {
	mu          sync.Mutex
	rows        []metadb.ConversationState
	cursor      metadb.ConversationActiveCursor
	done        bool
	err         error
	activeCalls []activePageCallFake
	states      map[metadb.ConversationKey]metadb.ConversationState
	stateCalls  []metadb.ConversationStateKey

	read           map[metadb.ConversationKey]lastVisibleResultFake
	readErr        map[metadb.ConversationKey]error
	readCalls      []readCallFake
	readGate       chan struct{}
	readStarted    chan struct{}
	activeReads    int
	maxActiveReads int

	committed      map[metadb.ConversationKey][]channelruntime.Message
	committedErr   map[metadb.ConversationKey]error
	committedCalls []committedCallFake
	upserts        []metadb.ConversationState
	deletes        []metadb.ConversationDelete
}

func (n *conversationNodeFake) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	n.activeCalls = append(n.activeCalls, activePageCallFake{kind: kind, uid: uid, after: after, limit: limit})
	if n.err != nil {
		return nil, metadb.ConversationActiveCursor{}, false, n.err
	}
	return n.rows, n.cursor, n.done, nil
}

func (n *conversationNodeFake) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid string, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	key := metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}
	n.stateCalls = append(n.stateCalls, metadb.ConversationStateKey{Kind: kind, UID: uid, ChannelID: channelID, ChannelType: channelType})
	state, ok := n.states[key]
	if ok && state.Kind != kind {
		return metadb.ConversationState{}, false, nil
	}
	return state, ok, nil
}

func (n *conversationNodeFake) UpsertConversationStatesBatch(_ context.Context, states []metadb.ConversationState) error {
	n.upserts = append(n.upserts, states...)
	return nil
}

func (n *conversationNodeFake) HideConversationsBatch(_ context.Context, reqs []metadb.ConversationDelete) error {
	n.deletes = append(n.deletes, reqs...)
	return nil
}

func (n *conversationNodeFake) ReadChannelLastVisible(_ context.Context, id channelruntime.ChannelID, visibleAfterSeq uint64) (channelruntime.Message, bool, error) {
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
		return channelruntime.Message{}, false, err
	}
	result, ok := n.read[key]
	if !ok {
		return channelruntime.Message{}, false, channelruntime.ErrChannelNotFound
	}
	return result.msg, result.ok, nil
}

func (n *conversationNodeFake) ReadChannelCommitted(_ context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	n.committedCalls = append(n.committedCalls, committedCallFake{channelID: id, req: req})
	if err := n.committedErr[key]; err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	messages, ok := n.committed[key]
	if !ok {
		return channelstore.ReadCommittedResult{}, channelruntime.ErrChannelNotFound
	}
	out := append([]channelruntime.Message(nil), messages...)
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
