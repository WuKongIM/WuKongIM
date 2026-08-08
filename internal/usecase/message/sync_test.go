package message

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestSyncChannelMessagesNormalizesPersonChannelAndCapsLimit(t *testing.T) {
	reader := &recordingChannelMessageReader{
		page: ChannelMessagePage{
			Messages: []SyncedMessage{{MessageID: 88, MessageSeq: 9}},
			HasMore:  true,
		},
	}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore()})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:        "u1",
		ChannelID:       "u2",
		ChannelType:     channelTypePerson,
		StartMessageSeq: 1,
		EndMessageSeq:   10,
		Limit:           20000,
		PullMode:        PullModeUp,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if !result.More {
		t.Fatalf("More = false, want true")
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != 88 {
		t.Fatalf("messages = %#v, want message 88", result.Messages)
	}
	if len(reader.queries) != 1 {
		t.Fatalf("queries = %#v, want one query", reader.queries)
	}
	wantChannel := ChannelID{ID: runtimechannelid.EncodePersonChannel("u1", "u2"), Type: channelTypePerson}
	if got := reader.queries[0]; got.ChannelID != wantChannel || got.StartSeq != 1 || got.EndSeq != 10 || got.Limit != 10000 || got.PullMode != PullModeUp {
		t.Fatalf("query = %#v, want normalized person capped-limit query", got)
	}
}

func TestSyncChannelMessagesReturnsEmptyForMissingChannelRuntime(t *testing.T) {
	app := New(Options{Reader: &recordingChannelMessageReader{err: metadb.ErrNotFound}, Memberships: liveSyncMembershipStore()})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Limit:       30,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if result.More || len(result.Messages) != 0 {
		t.Fatalf("result = %#v, want empty page", result)
	}
}

func TestSyncChannelMessagesRequiresLiveMembershipAndClampsVisibilityFloor(t *testing.T) {
	reader := &recordingChannelMessageReader{}
	memberships := &recordingSyncMembershipStore{row: metadb.UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 8, DeletedToSeq: 10,
	}, ok: true}
	app := New(Options{Reader: reader, Memberships: memberships})
	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID: "u1", ChannelID: "g1", ChannelType: 2, StartMessageSeq: 1, PullMode: PullModeUp,
	})
	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(reader.queries) != 1 || reader.queries[0].StartSeq != 11 || reader.queries[0].MinSeq != 11 {
		t.Fatalf("queries = %#v, want start/min seq 11", reader.queries)
	}

	memberships.ok = false
	_, err = app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{LoginUID: "u1", ChannelID: "g1", ChannelType: 2})
	if !errors.Is(err, ErrSyncMembershipRequired) {
		t.Fatalf("missing membership error = %v, want %v", err, ErrSyncMembershipRequired)
	}
}

func TestSyncChannelMessagesRequiresMembershipStore(t *testing.T) {
	app := New(Options{Reader: &recordingChannelMessageReader{}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID: "u1", ChannelID: "g1", ChannelType: 2,
	})

	if !errors.Is(err, ErrSyncMembershipRequired) {
		t.Fatalf("error = %v, want membership required", err)
	}
}

func TestSyncChannelMessagesBatchValidatesMembershipsBeforeOneGroupedRead(t *testing.T) {
	reader := &recordingChannelMessageReader{batchResults: []ChannelMessageReadResult{
		{Page: ChannelMessagePage{Messages: []SyncedMessage{{MessageSeq: 12, ChannelID: "g1", ChannelType: 2}}}},
		{Page: ChannelMessagePage{Messages: []SyncedMessage{{MessageSeq: 7, ChannelType: channelTypePerson}}}},
	}}
	personID := runtimechannelid.EncodePersonChannel("u1", "u2")
	memberships := &multiSyncMembershipStore{rows: map[ChannelID]metadb.UserChannelMembership{
		{ID: "g1", Type: 2}:     {UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 10, DeletedToSeq: 11},
		{ID: personID, Type: 1}: {UID: "u1", ChannelID: personID, ChannelType: 1, JoinSeq: 5},
	}}
	app := New(Options{Reader: reader, Memberships: memberships})

	result, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{
		LoginUID: "u1",
		Items: []SyncChannelMessagesQuery{
			{ChannelID: "g1", ChannelType: 2, StartMessageSeq: 1, PullMode: PullModeUp},
			{ChannelID: "u2", ChannelType: channelTypePerson, StartMessageSeq: 1, PullMode: PullModeUp},
		},
	})
	if err != nil {
		t.Fatalf("SyncChannelMessagesBatch() error = %v", err)
	}
	if memberships.calls != 2 || reader.batchCalls != 1 {
		t.Fatalf("membership calls=%d batch calls=%d, want 2 then 1", memberships.calls, reader.batchCalls)
	}
	if got := reader.batchQueries[0].StartSeq; got != 12 {
		t.Fatalf("first start seq=%d, want delete floor 12", got)
	}
	if got := reader.batchQueries[1].ChannelID.ID; got != personID {
		t.Fatalf("person channel=%q, want %q", got, personID)
	}
	if len(result.Items) != 2 || result.Items[0].Err != nil || result.Items[1].Err != nil {
		t.Fatalf("result=%+v, want two aligned successes", result)
	}
}

func TestSyncChannelMessagesBatchRejectsInvalidMembershipBeforeReadingAnyChannel(t *testing.T) {
	reader := &recordingChannelMessageReader{}
	memberships := &multiSyncMembershipStore{rows: map[ChannelID]metadb.UserChannelMembership{
		{ID: "g1", Type: 2}: {UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1},
	}}
	app := New(Options{Reader: reader, Memberships: memberships})

	_, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{
		LoginUID: "u1",
		Items: []SyncChannelMessagesQuery{
			{ChannelID: "g1", ChannelType: 2},
			{ChannelID: "g2", ChannelType: 2},
		},
	})
	if !errors.Is(err, ErrSyncMembershipRequired) {
		t.Fatalf("error=%v, want membership required", err)
	}
	if reader.batchCalls != 0 {
		t.Fatalf("batch calls=%d, want zero", reader.batchCalls)
	}
}

func TestSyncChannelMessagesRejectsDisbandedChannelBeforeRead(t *testing.T) {
	reader := &recordingChannelMessageReader{}
	memberships := &recordingSyncMembershipStore{row: metadb.UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1,
	}, ok: true}
	app := New(Options{
		Reader: reader, Memberships: memberships,
		ChannelState: staticSyncChannelStateStore{channel: metadb.Channel{ChannelID: "g1", ChannelType: 2, Disband: 1}},
	})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{LoginUID: "u1", ChannelID: "g1", ChannelType: 2})
	if !errors.Is(err, ErrSyncChannelDisbanded) || len(reader.queries) != 0 {
		t.Fatalf("SyncChannelMessages() error=%v queries=%+v", err, reader.queries)
	}
}

func TestSyncChannelMessagesRejectsMissingRequiredFields(t *testing.T) {
	app := New(Options{Reader: &recordingChannelMessageReader{}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		ChannelID:   "g1",
		ChannelType: 2,
	})

	if !errors.Is(err, ErrSyncLoginUIDRequired) {
		t.Fatalf("error = %v, want login uid required", err)
	}
}

func TestSyncChannelMessagesPropagatesReaderError(t *testing.T) {
	readerErr := errors.New("reader failed")
	app := New(Options{Reader: &recordingChannelMessageReader{err: readerErr}, Memberships: liveSyncMembershipStore()})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Limit:       30,
	})

	if !errors.Is(err, readerErr) {
		t.Fatalf("error = %v, want reader error", err)
	}
}

func TestSyncChannelMessagesEnrichesFullEventMeta(t *testing.T) {
	key := MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	store := &recordingMessageEventStore{stateRows: map[MessageEventMessageKey][]MessageEventState{
		key: {
			{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        EventKeyDefault,
				Status:          EventStatusClosed,
				LastMsgEventSeq: 2,
				SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`),
				EndReason:       3,
			},
			{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        "tool",
				Status:          EventStatusOpen,
				LastMsgEventSeq: 3,
				SnapshotPayload: []byte(`{"kind":"text","text":"tool"}`),
			},
			{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        EventKeyFinish,
				Status:          EventStatusClosed,
				LastMsgEventSeq: 4,
			},
		},
	}}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{Setting: legacySettingStream, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1", MessageID: 9, Payload: []byte("base")},
	}}}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), EventStore: store})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "full",
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(store.stateCalls) != 1 {
		t.Fatalf("state calls = %#v, want one call", store.stateCalls)
	}
	if store.stateCalls[0].limit != maxMessageEventSummaryLanes || len(store.stateCalls[0].keys) != 1 || store.stateCalls[0].keys[0] != key {
		t.Fatalf("state call = %#v, want message event key with lane cap", store.stateCalls[0])
	}
	if len(result.Messages) != 1 {
		t.Fatalf("messages = %#v, want one message", result.Messages)
	}
	msg := result.Messages[0]
	if string(msg.Payload) != "base" || string(msg.StreamData) != "hello" || msg.End != 1 || msg.EndReason != 3 || msg.Error != "" {
		t.Fatalf("stream fields = payload %q stream %q end %d reason %d error %q", msg.Payload, msg.StreamData, msg.End, msg.EndReason, msg.Error)
	}
	if msg.EventHint == nil || msg.EventHint.ClientMsgNo != "cmn-1" || msg.EventHint.FromMsgEventSeq != 0 {
		t.Fatalf("event hint = %#v, want cmn-1/0", msg.EventHint)
	}
	if msg.EventMeta == nil {
		t.Fatal("EventMeta = nil, want event summary")
	}
	if !msg.EventMeta.HasEvents || !msg.EventMeta.Completed || msg.EventMeta.EventVersion != 3 || msg.EventMeta.LastMsgEventSeq != 3 || msg.EventMeta.EventCount != 2 || msg.EventMeta.OpenEventCount != 1 {
		t.Fatalf("event meta = %#v, want completed two-lane summary", msg.EventMeta)
	}
	if len(msg.EventMeta.Events) != 2 || msg.EventMeta.Events[0].EventKey != EventKeyDefault || msg.EventMeta.Events[1].EventKey != "tool" {
		t.Fatalf("event lanes = %#v, want main/tool order", msg.EventMeta.Events)
	}
	if msg.EventMeta.Events[0].Snapshot == nil || msg.EventMeta.Events[1].Snapshot == nil {
		t.Fatalf("snapshots = %#v, want decoded snapshots in full mode", msg.EventMeta.Events)
	}
}

func TestSyncChannelMessagesBasicEventMetaOmitsSnapshots(t *testing.T) {
	key := MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	store := &recordingMessageEventStore{stateRows: map[MessageEventMessageKey][]MessageEventState{
		key: {{EventKey: EventKeyDefault, Status: EventStatusOpen, LastMsgEventSeq: 1, SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`)}},
	}}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{Setting: legacySettingStream, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), EventStore: store})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "basic",
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if got := result.Messages[0].EventMeta.Events[0].Snapshot; got != nil {
		t.Fatalf("snapshot = %#v, want nil in basic mode", got)
	}
}

func TestSyncChannelMessagesIncludeEventMetaDefaultsToFull(t *testing.T) {
	key := MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	store := &recordingMessageEventStore{stateRows: map[MessageEventMessageKey][]MessageEventState{
		key: {{EventKey: EventKeyDefault, Status: EventStatusOpen, LastMsgEventSeq: 1, SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`)}},
	}}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{Setting: legacySettingStream, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), EventStore: store})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		IncludeEventMeta: true,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(store.stateCalls) != 1 {
		t.Fatalf("state calls = %#v, want one call", store.stateCalls)
	}
	if got := result.Messages[0].EventMeta.Events[0].Snapshot; got == nil {
		t.Fatalf("snapshot = nil, want full-mode snapshot from include_event_meta")
	}
}

func TestSyncChannelMessagesSkipsNonStreamMessagesDuringEventEnrichment(t *testing.T) {
	store := &recordingMessageEventStore{}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-ordinary"},
	}}}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), EventStore: store})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "full",
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(store.stateCalls) != 0 {
		t.Fatalf("state calls = %#v, want none for ordinary messages", store.stateCalls)
	}
}

func TestSyncChannelMessagesSkipsEventStoreWhenSummaryModeEmpty(t *testing.T) {
	store := &recordingMessageEventStore{}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{Setting: legacySettingStream, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), EventStore: store})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(store.stateCalls) != 0 {
		t.Fatalf("state calls = %#v, want none", store.stateCalls)
	}
}

func TestSyncChannelMessagesPropagatesEventStoreError(t *testing.T) {
	storeErr := errors.New("event store failed")
	store := &recordingMessageEventStore{stateErr: storeErr}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{Setting: legacySettingStream, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), EventStore: store})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "basic",
	})

	if !errors.Is(err, storeErr) {
		t.Fatalf("error = %v, want event store error", err)
	}
}

type recordingChannelMessageReader struct {
	queries      []ChannelMessageQuery
	page         ChannelMessagePage
	err          error
	batchQueries []ChannelMessageQuery
	batchResults []ChannelMessageReadResult
	batchCalls   int
}

type recordingSyncMembershipStore struct {
	row metadb.UserChannelMembership
	ok  bool
}

func liveSyncMembershipStore() *recordingSyncMembershipStore {
	return &recordingSyncMembershipStore{row: metadb.UserChannelMembership{JoinSeq: 1}, ok: true}
}

type staticSyncChannelStateStore struct {
	channel metadb.Channel
	err     error
}

func (s staticSyncChannelStateStore) GetChannelForMessagePull(context.Context, string, int64) (metadb.Channel, error) {
	return s.channel, s.err
}

func (s *recordingSyncMembershipStore) GetUserChannelMembership(context.Context, string, string, int64) (metadb.UserChannelMembership, bool, error) {
	return s.row, s.ok, nil
}

func (r *recordingChannelMessageReader) SyncMessages(_ context.Context, query ChannelMessageQuery) (ChannelMessagePage, error) {
	r.queries = append(r.queries, query)
	return r.page, r.err
}

func (r *recordingChannelMessageReader) SyncMessagesBatch(_ context.Context, queries []ChannelMessageQuery) ([]ChannelMessageReadResult, error) {
	r.batchCalls++
	r.batchQueries = append([]ChannelMessageQuery(nil), queries...)
	return r.batchResults, r.err
}

type multiSyncMembershipStore struct {
	rows  map[ChannelID]metadb.UserChannelMembership
	calls int
}

func (s *multiSyncMembershipStore) GetUserChannelMembership(_ context.Context, _ string, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	s.calls++
	row, ok := s.rows[ChannelID{ID: channelID, Type: uint8(channelType)}]
	return row, ok, nil
}
