package cmdsync

import (
	"context"
	"reflect"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestSyncReadsCMDMembershipRowsAndStripsCommandSuffix(t *testing.T) {
	store := newCmdSyncStore()
	store.memberships = []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, StartSeq: 1,
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

func TestSyncScansEveryCMDMembershipPage(t *testing.T) {
	store := newCmdSyncStore()
	store.memberships = []metadb.UserCMDChannelMembership{
		{UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, StartSeq: 1},
		{UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g2"), ChannelType: 2, StartSeq: 1},
	}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g2"), ChannelType: 2}] = []SyncedMessage{{
		MessageID: 2, MessageSeq: 1, ChannelID: runtimechannelid.ToCommandChannel("g2"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store, ActiveScanLimit: 1})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if gotIDs := syncMessageChannelIDs(got.Messages); !reflect.DeepEqual(gotIDs, []string{"g2"}) {
		t.Fatalf("message channel IDs = %#v, want row from second membership page", gotIDs)
	}
}

func TestSyncAckAdvancesCMDMembershipAckOnlyFromLatestGeneration(t *testing.T) {
	store := newCmdSyncStore()
	store.memberships = []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, StartSeq: 1,
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
	if len(store.acks) != 1 || store.acks[0].CommandChannelID != runtimechannelid.ToCommandChannel("g1") || store.acks[0].AckSeq != 5 {
		t.Fatalf("acks = %+v", store.acks)
	}
}

func TestSyncSkipsTombstonedCMDMemberships(t *testing.T) {
	store := newCmdSyncStore()
	store.memberships = []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, StartSeq: 1, Tombstone: true,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 6, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if len(got.Messages) != 0 || len(store.messageCalls) != 0 {
		t.Fatalf("Sync() messages=%+v calls=%+v, want tombstone skipped", got.Messages, store.messageCalls)
	}
}

func TestSyncUsesStartAndAckFloorAndSortsDeterministically(t *testing.T) {
	store := newCmdSyncStore()
	store.memberships = []metadb.UserCMDChannelMembership{
		{UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, StartSeq: 2, AckSeq: 3},
		{UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2, StartSeq: 1},
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
		{key: CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2}, fromSeq: 1, limit: 10},
		{key: CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2}, fromSeq: 4, limit: 10},
	}; !reflect.DeepEqual(store.messageCalls, want) {
		t.Fatalf("message calls = %#v, want %#v", store.messageCalls, want)
	}
	if gotIDs := syncMessageChannelIDs(got.Messages); !reflect.DeepEqual(gotIDs, []string{"b", "a", "b"}) {
		t.Fatalf("message channel IDs = %#v, want sorted stripped ids", gotIDs)
	}
}

func TestSyncRecordsLatestGenerationOnly(t *testing.T) {
	store := newCmdSyncStore()
	store.memberships = []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2, StartSeq: 1,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 2, ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})
	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("first Sync(): %v", err)
	}

	store.memberships = []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2, StartSeq: 1,
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
	if len(store.acks) != 1 || store.acks[0].CommandChannelID != runtimechannelid.ToCommandChannel("new") || store.acks[0].AckSeq != 9 {
		t.Fatalf("acks = %+v, want latest generation only", store.acks)
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

func TestBindCapturesCommandTailAndWritesOneMembership(t *testing.T) {
	store := newCmdSyncStore()
	store.tails[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2}] = 41
	app := New(Options{States: store, Messages: store, Now: func() time.Time { return time.Unix(0, 99) }})

	if err := app.Bind(context.Background(), BindCommand{UID: " u1 ", ChannelID: " g1 ", ChannelType: 2}); err != nil {
		t.Fatalf("Bind(): %v", err)
	}
	if got, want := store.upserts, []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2,
		StartSeq: 42, UpdatedAt: 99,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upserts = %#v, want %#v", got, want)
	}
}

func TestUnbindWritesCommandMembershipTombstone(t *testing.T) {
	store := newCmdSyncStore()
	app := New(Options{States: store, Messages: store, Now: func() time.Time { return time.Unix(0, 101) }})

	if err := app.Unbind(context.Background(), UnbindCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("Unbind(): %v", err)
	}
	if got, want := store.tombstones, []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2,
		Tombstone: true, TombstoneAt: 101, UpdatedAt: 101,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tombstones = %#v, want %#v", got, want)
	}
	if len(store.tailCalls) != 0 {
		t.Fatalf("tail calls = %#v, want none", store.tailCalls)
	}
}

type cmdSyncStore struct {
	memberships  []metadb.UserCMDChannelMembership
	acks         []metadb.UserCMDChannelMembership
	upserts      []metadb.UserCMDChannelMembership
	tombstones   []metadb.UserCMDChannelMembership
	messages     map[CommandChannelKey][]SyncedMessage
	tails        map[CommandChannelKey]uint64
	tailCalls    []CommandChannelKey
	messageCalls []messageLoadCall
}

func newCmdSyncStore() *cmdSyncStore {
	return &cmdSyncStore{messages: make(map[CommandChannelKey][]SyncedMessage), tails: make(map[CommandChannelKey]uint64)}
}

func (s *cmdSyncStore) ListUserCMDChannelMembershipPage(_ context.Context, uid string, cursor metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error) {
	rows := make([]metadb.UserCMDChannelMembership, 0, len(s.memberships))
	for _, row := range s.memberships {
		if row.UID == uid {
			rows = append(rows, row)
		}
	}
	start := 0
	if cursor != (metadb.UserCMDChannelMembershipCursor{}) {
		for index, row := range rows {
			if row.CommandChannelID == cursor.CommandChannelID && row.ChannelType == cursor.ChannelType {
				start = index + 1
				break
			}
		}
	}
	end := len(rows)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	page := append([]metadb.UserCMDChannelMembership(nil), rows[start:end]...)
	next := cursor
	if len(page) > 0 {
		last := page[len(page)-1]
		next = metadb.UserCMDChannelMembershipCursor{CommandChannelID: last.CommandChannelID, ChannelType: last.ChannelType}
	}
	return page, next, end == len(rows), nil
}

func (s *cmdSyncStore) AdvanceUserCMDChannelMembershipAcks(_ context.Context, memberships []metadb.UserCMDChannelMembership) error {
	s.acks = append(s.acks, memberships...)
	return nil
}

func (s *cmdSyncStore) UpsertUserCMDChannelMemberships(_ context.Context, memberships []metadb.UserCMDChannelMembership) error {
	s.upserts = append(s.upserts, memberships...)
	return nil
}

func (s *cmdSyncStore) TombstoneUserCMDChannelMemberships(_ context.Context, memberships []metadb.UserCMDChannelMembership) error {
	s.tombstones = append(s.tombstones, memberships...)
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

func (s *cmdSyncStore) CommandChannelTail(_ context.Context, key CommandChannelKey) (uint64, error) {
	s.tailCalls = append(s.tailCalls, key)
	return s.tails[key], nil
}

func syncMessageChannelIDs(messages []SyncedMessage) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ChannelID)
	}
	return out
}
