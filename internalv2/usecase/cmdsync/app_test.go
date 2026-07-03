package cmdsync

import (
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestSyncReadsCMDKindRowsAndStripsCommandSuffix(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2}] = []SyncedMessage{{
		MessageID: 1, MessageSeq: 3, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, FromUID: "u2", Payload: []byte("cmd"),
	}}
	app := New(Options{States: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ChannelID != "g1" || got.Messages[0].MessageSeq != 3 {
		t.Fatalf("messages = %+v", got.Messages)
	}
}

func TestSyncAckAdvancesCMDKindReadSeqOnlyFromLatestGeneration(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 5, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})

	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1", LastMessageSeq: 5}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Kind != metadb.ConversationKindCMD || store.upserts[0].ReadSeq != 5 {
		t.Fatalf("upserts = %+v", store.upserts)
	}
}

func TestSyncAckAdvancesSyncOnceSourceChannelRows(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: "g1", ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 6, ChannelID: "g1", ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})

	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1"}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].ChannelID != "g1" || store.upserts[0].ReadSeq != 6 {
		t.Fatalf("upserts = %+v, want sync_once source channel read progress", store.upserts)
	}
}

func TestSyncUsesReadDeleteFloorAndSortsDeterministically(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, ReadSeq: 1, DeletedToSeq: 3, ActiveAt: 200},
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2, ReadSeq: 0, ActiveAt: 100},
	}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2}] = []SyncedMessage{
		{MessageID: 12, MessageSeq: 4, ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, ServerTimestampMS: 10},
		{MessageID: 13, MessageSeq: 5, ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, ServerTimestampMS: 20},
	}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2}] = []SyncedMessage{{
		MessageID: 11, MessageSeq: 1, ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2, ServerTimestampMS: 20,
	}}
	app := New(Options{States: store, Messages: store, DefaultLimit: 10, MaxLimit: 10})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if want := []messageLoadCall{
		{key: CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2}, fromSeq: 4, limit: 10},
		{key: CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2}, fromSeq: 1, limit: 10},
	}; !reflect.DeepEqual(store.messageCalls, want) {
		t.Fatalf("message calls = %#v, want %#v", store.messageCalls, want)
	}
	if gotIDs := syncMessageChannelIDs(got.Messages); !reflect.DeepEqual(gotIDs, []string{"b", "a", "b"}) {
		t.Fatalf("message channel IDs = %#v, want sorted stripped ids", gotIDs)
	}
}

func TestSyncRecordsLatestGenerationOnly(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 2, ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})
	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("first Sync(): %v", err)
	}

	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2, ActiveAt: 200,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 9, ChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2,
	}}
	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("second Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1"}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].ChannelID != runtimechannelid.ToCommandChannel("new") || store.upserts[0].ReadSeq != 9 {
		t.Fatalf("upserts = %+v, want latest generation only", store.upserts)
	}
}

func TestSyncRejectsMissingDependencies(t *testing.T) {
	store := newCmdSyncStore()
	if _, err := New(Options{States: store, Messages: store}).Sync(context.Background(), SyncQuery{}); err != ErrUIDRequired {
		t.Fatalf("Sync() error = %v, want %v", err, ErrUIDRequired)
	}
	if err := New(Options{States: store}).SyncAck(context.Background(), SyncAckCommand{}); err != ErrUIDRequired {
		t.Fatalf("SyncAck() error = %v, want %v", err, ErrUIDRequired)
	}
	if _, err := New(Options{Messages: store}).Sync(context.Background(), SyncQuery{UID: "u1"}); err != ErrStateStoreRequired {
		t.Fatalf("Sync() error = %v, want %v", err, ErrStateStoreRequired)
	}
	if _, err := New(Options{States: store}).Sync(context.Background(), SyncQuery{UID: "u1"}); err != ErrMessageStoreRequired {
		t.Fatalf("Sync() error = %v, want %v", err, ErrMessageStoreRequired)
	}
	if err := New(Options{}).SyncAck(context.Background(), SyncAckCommand{UID: "u1"}); err != ErrStateStoreRequired {
		t.Fatalf("SyncAck() error = %v, want %v", err, ErrStateStoreRequired)
	}
}

type cmdSyncStore struct {
	active       []metadb.ConversationState
	upserts      []metadb.ConversationState
	messages     map[CommandChannelKey][]SyncedMessage
	messageCalls []messageLoadCall
}

func newCmdSyncStore() *cmdSyncStore {
	return &cmdSyncStore{messages: make(map[CommandChannelKey][]SyncedMessage)}
}

func (s *cmdSyncStore) ListConversationActiveView(_ context.Context, uid string, limit int) ([]metadb.ConversationState, error) {
	rows := make([]metadb.ConversationState, 0, len(s.active))
	for _, row := range s.active {
		if row.UID == uid && row.Kind == metadb.ConversationKindCMD {
			rows = append(rows, row)
		}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *cmdSyncStore) UpsertConversationStates(_ context.Context, states []metadb.ConversationState) error {
	s.upserts = append(s.upserts, states...)
	return nil
}

type messageLoadCall struct {
	key     CommandChannelKey
	fromSeq uint64
	limit   int
}

func (s *cmdSyncStore) LoadCommandMessages(_ context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]SyncedMessage, error) {
	s.messageCalls = append(s.messageCalls, messageLoadCall{key: key, fromSeq: fromSeq, limit: limit})
	msgs := s.messages[key]
	out := make([]SyncedMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.MessageSeq < fromSeq {
			continue
		}
		if msg.ChannelID == "" {
			msg.ChannelID = key.ChannelID
		}
		if msg.ChannelType == 0 {
			msg.ChannelType = key.ChannelType
		}
		out = append(out, msg)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func syncMessageChannelIDs(messages []SyncedMessage) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ChannelID)
	}
	return out
}
