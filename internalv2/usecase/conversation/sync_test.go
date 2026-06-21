package conversation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSyncUsesActiveRowsClientKnownOverlayAndRecents(t *testing.T) {
	store := newConversationSyncStore()
	personChannel, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel() error = %v", err)
	}
	store.active = []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 5, ActiveAt: 300, UpdatedAt: 50},
	}
	store.states[metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}] = store.active[0]
	store.latest[ConversationKey{ChannelID: "g1", ChannelType: 2}] = LastMessage{
		MessageID: 11, MessageSeq: 12, FromUID: "u3", ClientMsgNo: "g1-last", ServerTimestampMS: 150000, Payload: []byte("g1-last"),
	}
	store.latest[ConversationKey{ChannelID: personChannel, ChannelType: 1}] = LastMessage{
		MessageID: 22, MessageSeq: 8, FromUID: "u2", ClientMsgNo: "p-last", ServerTimestampMS: 200000, Payload: []byte("p-last"),
	}
	store.recents[ConversationKey{ChannelID: personChannel, ChannelType: 1}] = []SyncMessage{
		{MessageID: 22, MessageSeq: 8, FromUID: "u2", ChannelID: personChannel, ChannelType: 1, ClientMsgNo: "p-last", ServerTimestampMS: 200000, Payload: []byte("p-last")},
		{MessageID: 21, MessageSeq: 7, FromUID: "u1", ChannelID: personChannel, ChannelType: 1, ClientMsgNo: "p-prev", ServerTimestampMS: 199000, Payload: []byte("p-prev")},
	}
	store.recents[ConversationKey{ChannelID: "g1", ChannelType: 2}] = []SyncMessage{
		{MessageID: 11, MessageSeq: 12, FromUID: "u3", ChannelID: "g1", ChannelType: 2, ClientMsgNo: "g1-last", ServerTimestampMS: 150000, Payload: []byte("g1-last")},
	}
	app := New(Options{Store: store, StateStore: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{
		UID:         "u1",
		LastMsgSeqs: map[ConversationKey]uint64{{ChannelID: personChannel, ChannelType: 1}: 3},
		MsgCount:    2,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	want := []SyncConversation{
		{ChannelID: personChannel, ChannelType: 1, Unread: 8, Timestamp: 200, LastMsgSeq: 8, LastClientMsgNo: "p-last", ReadToMsgSeq: 0, Version: 200000000000, Recents: []SyncMessage{
			{MessageID: 22, MessageSeq: 8, FromUID: "u2", ChannelID: personChannel, ChannelType: 1, ClientMsgNo: "p-last", ServerTimestampMS: 200000, Payload: []byte("p-last")},
			{MessageID: 21, MessageSeq: 7, FromUID: "u1", ChannelID: personChannel, ChannelType: 1, ClientMsgNo: "p-prev", ServerTimestampMS: 199000, Payload: []byte("p-prev")},
		}},
		{ChannelID: "g1", ChannelType: 2, Unread: 7, Timestamp: 150, LastMsgSeq: 12, LastClientMsgNo: "g1-last", ReadToMsgSeq: 5, Version: 150000000000, Recents: []SyncMessage{
			{MessageID: 11, MessageSeq: 12, FromUID: "u3", ChannelID: "g1", ChannelType: 2, ClientMsgNo: "g1-last", ServerTimestampMS: 150000, Payload: []byte("g1-last")},
		}},
	}
	if !reflect.DeepEqual(got.Conversations, want) {
		t.Fatalf("conversations = %#v, want %#v", got.Conversations, want)
	}
	if got, want := store.activeCalls, []activeViewCall{{kind: metadb.ConversationKindNormal, uid: "u1", after: metadb.ConversationActiveCursor{}, limit: defaultSyncActiveScanLimit}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active calls = %#v, want %#v", got, want)
	}
	if got, want := store.stateCalls, []stateCall{{kind: metadb.ConversationKindNormal, uid: "u1", channelID: personChannel, channelType: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("state calls = %#v, want %#v", got, want)
	}
	if got, want := store.recentBatchCalls, [][]ConversationKey{{{ChannelID: personChannel, ChannelType: 1}, {ChannelID: "g1", ChannelType: 2}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent batch calls = %#v, want %#v", got, want)
	}
}

func TestSyncFiltersUnreadDeletedAndExcludedConversations(t *testing.T) {
	store := newConversationSyncStore()
	store.active = []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "unread", ChannelType: 2, ReadSeq: 3, ActiveAt: 400},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "read", ChannelType: 2, ReadSeq: 10, ActiveAt: 300},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "deleted", ChannelType: 2, DeletedToSeq: 9, ActiveAt: 200},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "excluded", ChannelType: 3, ActiveAt: 100},
	}
	for _, row := range store.active {
		store.states[metadb.ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}] = row
		messageSeq := uint64(10)
		if row.ChannelID == "deleted" {
			messageSeq = 9
		}
		store.latest[ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}] = LastMessage{
			MessageID: uint64(row.ActiveAt), MessageSeq: messageSeq, FromUID: "u2", ClientMsgNo: row.ChannelID, ServerTimestampMS: row.ActiveAt * 1000,
		}
	}
	app := New(Options{Store: store, StateStore: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{
		UID:                 "u1",
		OnlyUnread:          true,
		ExcludeChannelTypes: []uint8{3},
		Limit:               10,
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if gotIDs := syncConversationIDs(got.Conversations); !reflect.DeepEqual(gotIDs, []string{"unread"}) {
		t.Fatalf("conversation IDs = %#v, want unread only", gotIDs)
	}
}

func TestSyncReadsOnlyNormalKindRows(t *testing.T) {
	store := newConversationSyncStore()
	store.active = []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "chat", ChannelType: 2, ReadSeq: 1, ActiveAt: 100},
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("chat"), ChannelType: 2, ReadSeq: 1, ActiveAt: 200},
	}
	store.latest[ConversationKey{ChannelID: "chat", ChannelType: 2}] = LastMessage{MessageSeq: 3, ServerTimestampMS: 1000}

	app := New(Options{Store: store, StateStore: store, Messages: store})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1"})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if gotIDs := syncConversationIDs(got.Conversations); !reflect.DeepEqual(gotIDs, []string{"chat"}) {
		t.Fatalf("conversation IDs = %#v, want normal kind only", gotIDs)
	}
}

func TestSyncReportsOverlayAndRecentLoadShape(t *testing.T) {
	store := newConversationSyncStore()
	store.active = []metadb.ConversationState{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "active",
		ChannelType: 2,
		ActiveAt:    20,
	}}
	store.latest[ConversationKey{ChannelID: "active", ChannelType: 2}] = LastMessage{MessageSeq: 4, ServerTimestampMS: 4000}
	store.latest[ConversationKey{ChannelID: "overlay", ChannelType: 2}] = LastMessage{MessageSeq: 9, ServerTimestampMS: 9000}
	store.states[metadb.ConversationKey{ChannelID: "durable-overlay", ChannelType: 2}] = metadb.ConversationState{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "durable-overlay",
		ChannelType: 2,
	}
	store.latest[ConversationKey{ChannelID: "durable-overlay", ChannelType: 2}] = LastMessage{MessageSeq: 8, ServerTimestampMS: 8000}
	store.recents[ConversationKey{ChannelID: "overlay", ChannelType: 2}] = []SyncMessage{{
		MessageSeq:        9,
		ChannelID:         "overlay",
		ChannelType:       2,
		ServerTimestampMS: 9000,
	}}
	app := New(Options{Store: store, StateStore: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{
		UID:      "u1",
		MsgCount: 1,
		LastMsgSeqs: map[ConversationKey]uint64{
			{ChannelID: "overlay", ChannelType: 2}:         7,
			{ChannelID: "durable-overlay", ChannelType: 2}: 7,
		},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got.OverlayItems != 2 {
		t.Fatalf("OverlayItems = %d, want 2", got.OverlayItems)
	}
	if got.RecentLoadDuration <= 0 {
		t.Fatalf("RecentLoadDuration = %v, want positive duration", got.RecentLoadDuration)
	}
}

type conversationSyncStore struct {
	active           []metadb.ConversationState
	states           map[metadb.ConversationKey]metadb.ConversationState
	latest           map[ConversationKey]LastMessage
	recents          map[ConversationKey][]SyncMessage
	activeCalls      []activeViewCall
	stateCalls       []stateCall
	lastMessageCalls [][]LastVisibleMessageRequest
	recentBatchCalls [][]ConversationKey
}

type stateCall struct {
	kind        metadb.ConversationKind
	uid         string
	channelID   string
	channelType int64
}

func newConversationSyncStore() *conversationSyncStore {
	return &conversationSyncStore{
		states:  make(map[metadb.ConversationKey]metadb.ConversationState),
		latest:  make(map[ConversationKey]LastMessage),
		recents: make(map[ConversationKey][]SyncMessage),
	}
}

func (s *conversationSyncStore) ListConversationActiveView(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (ActiveViewPage, error) {
	s.activeCalls = append(s.activeCalls, activeViewCall{kind: kind, uid: uid, after: after, limit: limit})
	rows := make([]metadb.ConversationState, 0, len(s.active))
	for _, row := range s.active {
		if row.Kind == kind && row.UID == uid {
			rows = append(rows, row)
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return ActiveViewPage{Rows: rows, Done: true}, nil
}

func (s *conversationSyncStore) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	s.stateCalls = append(s.stateCalls, stateCall{kind: kind, uid: uid, channelID: channelID, channelType: channelType})
	state, ok := s.states[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok || state.Kind != kind {
		return metadb.ConversationState{}, false, nil
	}
	return state, true, nil
}

func (s *conversationSyncStore) GetLastVisibleMessages(_ context.Context, requests []LastVisibleMessageRequest) (map[metadb.ConversationKey]LastMessage, error) {
	s.lastMessageCalls = append(s.lastMessageCalls, append([]LastVisibleMessageRequest(nil), requests...))
	out := make(map[metadb.ConversationKey]LastMessage)
	for _, req := range requests {
		key := ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		msg, ok := s.latest[key]
		if !ok || msg.MessageSeq <= req.VisibleAfterSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		out[metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}] = msg
	}
	return out, nil
}

func (s *conversationSyncStore) GetRecentMessages(_ context.Context, keys []ConversationKey, limit int) (map[ConversationKey][]SyncMessage, error) {
	if limit < 0 {
		return nil, errors.New("negative limit")
	}
	s.recentBatchCalls = append(s.recentBatchCalls, append([]ConversationKey(nil), keys...))
	out := make(map[ConversationKey][]SyncMessage)
	for _, key := range keys {
		msgs := append([]SyncMessage(nil), s.recents[key]...)
		if len(msgs) > limit {
			msgs = msgs[:limit]
		}
		out[key] = msgs
	}
	return out, nil
}

func syncConversationIDs(items []SyncConversation) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ChannelID)
	}
	return ids
}
