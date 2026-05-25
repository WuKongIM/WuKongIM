package cmdsync

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestAppSyncLoadsCMDStatesFetchesFromReadCursorStripsSuffixAndRecordsReturnedSeqs(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{active: []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 1, DeletedToSeq: 3, ActiveAt: 100},
		{UID: "u1", ChannelID: "g2____cmd", ChannelType: 2, ReadSeq: 5, ActiveAt: 200},
		{UID: "u1", ChannelID: "empty____cmd", ChannelType: 2, ReadSeq: 9, ActiveAt: 50},
	}}
	messages := &fakeMessageStore{byKey: map[CommandChannelKey][]channel.Message{
		{ChannelID: "g1____cmd", ChannelType: 2}: {
			{MessageID: 11, MessageSeq: 4, Timestamp: 20, ChannelID: "g1____cmd", ChannelType: 2},
			{MessageID: 12, MessageSeq: 5, Timestamp: 10, ChannelID: "g1____cmd", ChannelType: 2},
		},
		{ChannelID: "g2____cmd", ChannelType: 2}: {
			{MessageID: 21, MessageSeq: 6, Timestamp: 15, ChannelID: "g2____cmd", ChannelType: 2},
		},
	}}
	records := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10})
	app := New(Options{States: states, Messages: messages, Records: records, ActiveScanLimit: 3, DefaultLimit: 2, MaxLimit: 10})

	result, err := app.Sync(ctx, SyncQuery{UID: " u1 ", Limit: 2, MessageSeq: 999})
	require.NoError(t, err)
	require.Equal(t, []messageLoadCall{
		{key: CommandChannelKey{ChannelID: "g2____cmd", ChannelType: 2}, fromSeq: 6, limit: 2},
		{key: CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, fromSeq: 4, limit: 2},
		{key: CommandChannelKey{ChannelID: "empty____cmd", ChannelType: 2}, fromSeq: 10, limit: 2},
	}, messages.calls)
	require.Equal(t, []channel.Message{
		{MessageID: 12, MessageSeq: 5, Timestamp: 10, ChannelID: "g1", ChannelType: 2},
		{MessageID: 21, MessageSeq: 6, Timestamp: 15, ChannelID: "g2", ChannelType: 2},
	}, result.Messages)
	require.Equal(t, []SyncRecord{
		{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 5},
		{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 6},
	}, records.Pop("u1"))
}

func TestAppSyncDoesNotReplaceRecordsWhenMessageLoadFails(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{active: []metadb.CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100}}}
	loadErr := errors.New("load failed")
	messages := &fakeMessageStore{err: loadErr}
	records := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "old____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})
	app := New(Options{States: states, Messages: messages, Records: records})

	_, err := app.Sync(ctx, SyncQuery{UID: "u1", Limit: 10})
	require.ErrorIs(t, err, loadErr)
	require.Equal(t, []SyncRecord{{CommandChannelID: "old____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}}, records.Pop("u1"))
}

func TestAppSyncMergesPendingCMDConversationOverlay(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{}
	pending := &fakePendingStore{views: map[string][]PendingConversationView{
		"u1": {
			{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 1, ActiveAt: 10, ReadSeq: 0},
			{CommandChannelID: "nested____cmd____cmd", ChannelType: 2, LastMsgSeq: 2, ActiveAt: 20, ReadSeq: 0},
		},
	}}
	messages := &fakeMessageStore{byKey: map[CommandChannelKey][]channel.Message{
		{ChannelID: "g1____cmd", ChannelType: 2}: {
			{MessageID: 11, MessageSeq: 1, Timestamp: 20, ChannelID: "g1____cmd", ChannelType: 2},
		},
		{ChannelID: "nested____cmd____cmd", ChannelType: 2}: {
			{MessageID: 12, MessageSeq: 2, Timestamp: 21, ChannelID: "nested____cmd____cmd", ChannelType: 2},
		},
	}}
	app := New(Options{States: states, Messages: messages, Pending: pending, Records: NewSyncRecordCache(SyncRecordCacheOptions{})})

	result, err := app.Sync(ctx, SyncQuery{UID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []channel.Message{
		{MessageID: 11, MessageSeq: 1, Timestamp: 20, ChannelID: "g1", ChannelType: 2},
		{MessageID: 12, MessageSeq: 2, Timestamp: 21, ChannelID: "nested____cmd", ChannelType: 2},
	}, result.Messages)
}

func TestAppSyncUsesMaxPersistedAndPendingReadSeq(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{active: []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 5, ActiveAt: 100},
	}}
	pending := &fakePendingStore{views: map[string][]PendingConversationView{
		"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 4, ActiveAt: 200, ReadSeq: 0}},
	}}
	messages := &fakeMessageStore{}
	app := New(Options{States: states, Messages: messages, Pending: pending, Records: NewSyncRecordCache(SyncRecordCacheOptions{})})

	_, err := app.Sync(ctx, SyncQuery{UID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []messageLoadCall{{key: CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, fromSeq: 6, limit: 10}}, messages.calls)
}

func TestAppSyncAckAdvancesOnlyLatestGenerationAndIgnoresCompatibilitySeq(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 12, 12, 0, 0, 123, time.UTC)
	states := &fakeStateStore{}
	records := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "old____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})
	records.Replace("u1", []SyncRecord{
		{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 7},
		{CommandChannelID: "not-cmd", ChannelType: 2, LastReturnedMsgSeq: 8},
		{CommandChannelID: "zero____cmd", ChannelType: 2, LastReturnedMsgSeq: 0},
	})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Now: func() time.Time { return now }})

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: " u1 ", LastMessageSeq: 99999}))
	require.Equal(t, []metadb.CMDConversationReadPatch{{
		UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 7, UpdatedAt: now.UnixNano(),
	}}, states.patches)

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1", LastMessageSeq: 99999}))
	require.Len(t, states.patches, 1)
}

func TestAppRejectsMissingUIDAndDependencies(t *testing.T) {
	ctx := context.Background()
	app := New(Options{States: &fakeStateStore{}, Messages: &fakeMessageStore{}})
	_, err := app.Sync(ctx, SyncQuery{UID: "   ", Limit: 10})
	require.ErrorIs(t, err, ErrUIDRequired)
	require.ErrorIs(t, app.SyncAck(ctx, SyncAckCommand{UID: ""}), ErrUIDRequired)

	_, err = New(Options{Messages: &fakeMessageStore{}}).Sync(ctx, SyncQuery{UID: "u1", Limit: 10})
	require.ErrorIs(t, err, ErrStateStoreRequired)
	_, err = New(Options{States: &fakeStateStore{}}).Sync(ctx, SyncQuery{UID: "u1", Limit: 10})
	require.ErrorIs(t, err, ErrMessageStoreRequired)
	require.ErrorIs(t, New(Options{}).SyncAck(ctx, SyncAckCommand{UID: "u1"}), ErrStateStoreRequired)
}

func TestAppSyncSortsDeterministicallyWhenTimestampsTie(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{active: []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "b____cmd", ChannelType: 2, ActiveAt: 100},
		{UID: "u1", ChannelID: "a____cmd", ChannelType: 2, ActiveAt: 100},
	}}
	messages := &fakeMessageStore{byKey: map[CommandChannelKey][]channel.Message{
		{ChannelID: "b____cmd", ChannelType: 2}: {{MessageID: 2, MessageSeq: 1, Timestamp: 10, ChannelID: "b____cmd", ChannelType: 2}},
		{ChannelID: "a____cmd", ChannelType: 2}: {{MessageID: 1, MessageSeq: 1, Timestamp: 10, ChannelID: "a____cmd", ChannelType: 2}},
	}}
	app := New(Options{States: states, Messages: messages, Records: NewSyncRecordCache(SyncRecordCacheOptions{})})

	result, err := app.Sync(ctx, SyncQuery{UID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, []string{result.Messages[0].ChannelID, result.Messages[1].ChannelID})
}

func TestAppSyncAckPropagatesStoreErrors(t *testing.T) {
	ctx := context.Background()
	storeErr := errors.New("advance failed")
	states := &fakeStateStore{err: storeErr}
	records := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 7}})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records})

	require.ErrorIs(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}), storeErr)
	require.Equal(t, []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 7}}, records.Pop("u1"))
}

func TestAppSyncAckCleansPendingAfterPersistedAdvance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 12, 12, 0, 0, 123, time.UTC)
	states := &fakeStateStore{}
	pending := &fakePendingStore{views: map[string][]PendingConversationView{
		"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 100, ReadSeq: 0}},
	}}
	records := NewSyncRecordCache(SyncRecordCacheOptions{})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending, Now: func() time.Time { return now }})

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}))
	require.Equal(t, []metadb.CMDConversationReadPatch{{
		UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: now.UnixNano(),
	}}, states.patches)
	require.Equal(t, []markSyncedCall{{uid: "u1", key: CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, throughSeq: 9}}, pending.markCalls)
}

func TestAppSyncAckTreatsPendingCleanupFailureAsBestEffort(t *testing.T) {
	ctx := context.Background()
	markErr := errors.New("mark synced failed")
	states := &fakeStateStore{}
	pending := &fakePendingStore{
		views:   map[string][]PendingConversationView{"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 100, ReadSeq: 0}}},
		markErr: markErr,
	}
	records := NewSyncRecordCache(SyncRecordCacheOptions{})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending})

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}))
	require.Len(t, states.patches, 1)
	require.Equal(t, uint64(9), states.patches[0].ReadSeq)
	require.Len(t, pending.markCalls, 1)
}

func TestAppSyncAckPersistsPendingOnlyReadProgressBeforeCleanup(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 12, 12, 0, 0, 456, time.UTC)
	states := &fakeStateStore{}
	pending := &fakePendingStore{views: map[string][]PendingConversationView{
		"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 100, ReadSeq: 0}},
	}}
	records := NewSyncRecordCache(SyncRecordCacheOptions{})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending, Now: func() time.Time { return now }})

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}))
	require.Equal(t, []metadb.CMDConversationState{{
		UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, ActiveAt: 100, UpdatedAt: now.UnixNano(),
	}}, states.upserts)
	require.Equal(t, []markSyncedCall{{uid: "u1", key: CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, throughSeq: 9}}, pending.markCalls)
}

func TestAppSyncAckPendingOnlyProgressUpsertFailureSkipsCleanup(t *testing.T) {
	ctx := context.Background()
	upsertErr := errors.New("upsert failed")
	states := &fakeStateStore{upsertErr: upsertErr}
	pending := &fakePendingStore{views: map[string][]PendingConversationView{
		"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 100, ReadSeq: 0}},
	}}
	records := NewSyncRecordCache(SyncRecordCacheOptions{})
	records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending})

	require.ErrorIs(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}), upsertErr)
	require.Empty(t, pending.markCalls)
	require.Equal(t, []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}}, records.Pop("u1"))
}

func TestAppSyncAckPendingCleanupOnlyRunsForValidRecords(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{}
	pending := &fakePendingStore{views: map[string][]PendingConversationView{
		"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 100, ReadSeq: 0}},
	}}
	records := NewSyncRecordCache(SyncRecordCacheOptions{})
	records.Replace("u1", []SyncRecord{
		{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9},
		{CommandChannelID: "zero____cmd", ChannelType: 2, LastReturnedMsgSeq: 0},
		{CommandChannelID: "not-cmd", ChannelType: 2, LastReturnedMsgSeq: 10},
	})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending})

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}))
	require.Equal(t, []markSyncedCall{{uid: "u1", key: CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, throughSeq: 9}}, pending.markCalls)
}

func TestAppSyncAckUsesPendingRecordMetadataWhenPendingListNoLongerContainsView(t *testing.T) {
	ctx := context.Background()
	states := &fakeStateStore{}
	pending := &fakePendingStore{}
	records := NewSyncRecordCache(SyncRecordCacheOptions{})
	records.Replace("u1", []SyncRecord{{
		CommandChannelID:   "g1____cmd",
		ChannelType:        2,
		LastReturnedMsgSeq: 9,
		Pending:            true,
		PendingActiveAt:    100,
	}})
	app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending})

	require.NoError(t, app.SyncAck(ctx, SyncAckCommand{UID: "u1"}))
	require.Equal(t, []metadb.CMDConversationState{{
		UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, ActiveAt: 100, UpdatedAt: states.upserts[0].UpdatedAt,
	}}, states.upserts)
	require.Equal(t, []markSyncedCall{{uid: "u1", key: CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, throughSeq: 9}}, pending.markCalls)
}

func TestAppSyncDefaultRecordCacheRetainsAllReturnedChannels(t *testing.T) {
	ctx := context.Background()
	limit := defaultSyncRecordMaxPerUID + 1
	states := &fakeStateStore{active: make([]metadb.CMDConversationState, 0, limit)}
	messages := &fakeMessageStore{byKey: make(map[CommandChannelKey][]channel.Message, limit)}
	for i := 0; i < limit; i++ {
		channelID := channelid.ToCommandChannel("g-record-" + strconv.Itoa(i))
		state := metadb.CMDConversationState{UID: "u1", ChannelID: channelID, ChannelType: 2, ActiveAt: int64(limit - i)}
		states.active = append(states.active, state)
		key := CommandChannelKey{ChannelID: channelID, ChannelType: 2}
		messages.byKey[key] = []channel.Message{{MessageID: uint64(i + 1), MessageSeq: 1, ChannelID: channelID, ChannelType: 2}}
	}
	app := New(Options{States: states, Messages: messages, ActiveScanLimit: limit, DefaultLimit: limit, MaxLimit: limit})

	result, err := app.Sync(ctx, SyncQuery{UID: "u1", Limit: limit})
	require.NoError(t, err)
	require.Len(t, result.Messages, limit)
	require.Len(t, app.records.Pop("u1"), limit)
}

type fakeStateStore struct {
	active    []metadb.CMDConversationState
	patches   []metadb.CMDConversationReadPatch
	upserts   []metadb.CMDConversationState
	err       error
	upsertErr error
}

func (f *fakeStateStore) ListCMDConversationActive(_ context.Context, uid string, limit int) ([]metadb.CMDConversationState, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]metadb.CMDConversationState, 0, len(f.active))
	for _, state := range f.active {
		if state.UID == uid {
			out = append(out, state)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStateStore) AdvanceCMDConversationReadSeq(_ context.Context, patches []metadb.CMDConversationReadPatch) error {
	if f.err != nil {
		return f.err
	}
	f.patches = append(f.patches, patches...)
	return nil
}

func (f *fakeStateStore) UpsertCMDConversationStates(_ context.Context, states []metadb.CMDConversationState) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, states...)
	return nil
}

type messageLoadCall struct {
	key     CommandChannelKey
	fromSeq uint64
	limit   int
}

type fakeMessageStore struct {
	byKey map[CommandChannelKey][]channel.Message
	calls []messageLoadCall
	err   error
}

func (f *fakeMessageStore) LoadCommandMessages(_ context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error) {
	f.calls = append(f.calls, messageLoadCall{key: key, fromSeq: fromSeq, limit: limit})
	if f.err != nil {
		return nil, f.err
	}
	var out []channel.Message
	for _, msg := range f.byKey[key] {
		if msg.MessageSeq < fromSeq {
			continue
		}
		if !channelid.IsCommandChannel(msg.ChannelID) {
			msg.ChannelID = key.ChannelID
		}
		out = append(out, msg)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

type markSyncedCall struct {
	uid        string
	key        CommandChannelKey
	throughSeq uint64
}

type fakePendingStore struct {
	views     map[string][]PendingConversationView
	markCalls []markSyncedCall
	markErr   error
}

func (f *fakePendingStore) ListPending(_ context.Context, uid string, limit int) []PendingConversationView {
	views := append([]PendingConversationView(nil), f.views[uid]...)
	if limit > 0 && len(views) > limit {
		views = views[:limit]
	}
	return views
}

func (f *fakePendingStore) MarkSynced(_ context.Context, uid string, key CommandChannelKey, throughSeq uint64) error {
	f.markCalls = append(f.markCalls, markSyncedCall{uid: uid, key: key, throughSeq: throughSeq})
	return f.markErr
}
