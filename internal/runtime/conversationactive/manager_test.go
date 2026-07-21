package conversationactive

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestAdmitActiveBatchUpdatesCacheAndSenderReadSeq(t *testing.T) {
	const activeAtMS int64 = 1781094600000
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(context.Background(), ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  activeAtMS,
		Recipients: []ActiveEntry{
			{UID: "alice", IsSender: true},
			{UID: "bob"},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	sender, ok := m.EntryForTest(metadb.ConversationKindNormal, "alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != activeAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, activeAtMS)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}
	if sender.MessageSeq != 42 {
		t.Fatalf("sender MessageSeq = %d, want 42", sender.MessageSeq)
	}

	receiver, ok := m.EntryForTest(metadb.ConversationKindNormal, "bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != activeAtMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, activeAtMS)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
	if receiver.MessageSeq != 42 {
		t.Fatalf("receiver MessageSeq = %d, want 42", receiver.MessageSeq)
	}
}

func TestAdmitActiveBatchCachesSenderWhenNotRecipient(t *testing.T) {
	const activeAtMS int64 = 1781094600000
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(context.Background(), ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  activeAtMS,
		Recipients: []ActiveEntry{
			{UID: "bob"},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	sender, ok := m.EntryForTest(metadb.ConversationKindNormal, "alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != activeAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, activeAtMS)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest(metadb.ConversationKindNormal, "bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchPreservesReceiverReadSeq(t *testing.T) {
	const firstActiveAtMS int64 = 1781094600000
	const secondActiveAtMS int64 = firstActiveAtMS + 5000
	const olderActiveAtMS int64 = firstActiveAtMS - 5000
	m := NewManager(Options{})

	for _, batch := range []ActiveBatch{
		{
			Kind:        metadb.ConversationKindNormal,
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  7,
			ActiveAtMS:  firstActiveAtMS,
			Recipients: []ActiveEntry{
				{UID: "alice", IsSender: true},
				{UID: "bob"},
			},
		},
		{
			Kind:        metadb.ConversationKindNormal,
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  11,
			ActiveAtMS:  secondActiveAtMS,
			Recipients: []ActiveEntry{
				{UID: "alice", IsSender: true},
				{UID: "bob"},
			},
		},
		{
			Kind:        metadb.ConversationKindNormal,
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  9,
			ActiveAtMS:  olderActiveAtMS,
			Recipients: []ActiveEntry{
				{UID: "alice", IsSender: true},
				{UID: "bob"},
			},
		},
	} {
		if err := m.AdmitActiveBatch(context.Background(), batch); err != nil {
			t.Fatalf("AdmitActiveBatch() error = %v", err)
		}
	}

	sender, ok := m.EntryForTest(metadb.ConversationKindNormal, "alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != secondActiveAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, secondActiveAtMS)
	}
	if sender.ReadSeq != 11 {
		t.Fatalf("sender ReadSeq = %d, want 11", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest(metadb.ConversationKindNormal, "bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != secondActiveAtMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, secondActiveAtMS)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchUsesNowMSWhenActiveAtMissing(t *testing.T) {
	const nowMS int64 = 1781094600123
	m := NewManager(Options{
		NowMS: func() int64 {
			return nowMS
		},
	})

	err := m.AdmitActiveBatch(context.Background(), ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		Recipients: []ActiveEntry{
			{UID: "bob"},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	sender, ok := m.EntryForTest(metadb.ConversationKindNormal, "alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != nowMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, nowMS)
	}

	receiver, ok := m.EntryForTest(metadb.ConversationKindNormal, "bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != nowMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, nowMS)
	}
}

func TestListActiveViewReadsCacheBeforeFlush(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})

	err := m.AdmitActiveBatch(ctx, ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  1781094600000,
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	page, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "alice", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}

	wantRows := []metadb.ConversationState{{
		UID:         "alice",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room-1",
		ChannelType: 2,
		ReadSeq:     42,
		ActiveAt:    1781094600000,
	}}
	wantCursor := metadb.ConversationActiveCursor{ActiveAt: 1781094600000, ChannelID: "room-1", ChannelType: 2}
	if !reflect.DeepEqual(page.Rows, wantRows) || page.Cursor != wantCursor || !page.Done {
		t.Fatalf("page = %+v, want rows=%+v cursor=%+v done=true", page, wantRows, wantCursor)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d, want one DB page request", store.calls)
	}
}

func TestManagerKeepsKindsIsolatedForSameChannel(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})

	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAtMS: 100, ReadSeq: 1},
		{Kind: metadb.ConversationKindCMD, UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAtMS: 200, ReadSeq: 9},
	}); err != nil {
		t.Fatalf("MarkActive(): %v", err)
	}

	normal, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView(normal): %v", err)
	}
	cmd, err := m.ListActiveView(ctx, metadb.ConversationKindCMD, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView(cmd): %v", err)
	}
	if len(normal.Rows) != 1 || normal.Rows[0].Kind != metadb.ConversationKindNormal || normal.Rows[0].ReadSeq != 1 {
		t.Fatalf("normal page = %+v", normal.Rows)
	}
	if len(cmd.Rows) != 1 || cmd.Rows[0].Kind != metadb.ConversationKindCMD || cmd.Rows[0].ReadSeq != 9 {
		t.Fatalf("cmd page = %+v", cmd.Rows)
	}
}

func TestListActiveViewMergesCacheOverDB(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{
		rows: []metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "shared", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 7, ActiveAt: 100, UpdatedAt: 101},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db-only", ChannelType: 2, ReadSeq: 1, ActiveAt: 900},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a-db-tie", ChannelType: 2, ActiveAt: 800},
			{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "other-user", ChannelType: 2, ActiveAt: 2000},
		},
	}
	m := NewManager(Options{Store: store})

	err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "shared", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 10, MessageSeq: 8},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "b-cache-tie", ChannelType: 2, ActiveAtMS: 800},
	})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	page, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}

	wantRows := []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "shared", ChannelType: 2, ReadSeq: 10, DeletedToSeq: 7, ActiveAt: 1000, UpdatedAt: 101},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db-only", ChannelType: 2, ReadSeq: 1, ActiveAt: 900},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a-db-tie", ChannelType: 2, ActiveAt: 800},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b-cache-tie", ChannelType: 2, ActiveAt: 800},
	}
	if !reflect.DeepEqual(page.Rows, wantRows) {
		t.Fatalf("rows = %+v, want merged rows %+v", page.Rows, wantRows)
	}
	if page.Cursor != (metadb.ConversationActiveCursor{ActiveAt: 800, ChannelID: "b-cache-tie", ChannelType: 2}) || !page.Done {
		t.Fatalf("cursor=%+v done=%v, want final cursor and done=true", page.Cursor, page.Done)
	}
}

func TestListActiveViewHydratesCacheOnlyDurableRow(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{
		rows: []metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db-only", ChannelType: 2, ActiveAt: 900},
		},
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "shared", ChannelType: 2}: {
				UID:          "u1",
				Kind:         metadb.ConversationKindNormal,
				ChannelID:    "shared",
				ChannelType:  2,
				ReadSeq:      50,
				DeletedToSeq: 7,
				ActiveAt:     100,
				UpdatedAt:    111,
				SparseActive: true,
			},
		},
	}
	m := NewManager(Options{Store: store})
	err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "shared", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 10, MessageSeq: 8},
	})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	page, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}

	wantRows := []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "shared", ChannelType: 2, ReadSeq: 50, DeletedToSeq: 7, ActiveAt: 1000, UpdatedAt: 111, SparseActive: true},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db-only", ChannelType: 2, ActiveAt: 900},
	}
	if !reflect.DeepEqual(page.Rows, wantRows) {
		t.Fatalf("rows = %+v, want hydrated rows %+v", page.Rows, wantRows)
	}
	if len(store.lookups) != 1 || store.lookups[0] != (metadb.ConversationStateKey{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "shared", ChannelType: 2}) {
		t.Fatalf("lookups = %+v, want shared primary lookup", store.lookups)
	}
}

func TestListActiveViewFencesStaleCacheActivityAtDurableDeleteBarrier(t *testing.T) {
	ctx := context.Background()
	key := metadb.ConversationStateKey{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "deleted",
		ChannelType: 2,
	}
	store := &recordingActiveStore{primary: map[metadb.ConversationStateKey]metadb.ConversationState{
		key: {
			UID:          key.UID,
			Kind:         key.Kind,
			ChannelID:    key.ChannelID,
			ChannelType:  key.ChannelType,
			DeletedToSeq: 10,
		},
	}}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType),
		ActiveAtMS: 2_000, MessageSeq: 10,
	}}); err != nil {
		t.Fatalf("MarkActive(stale) error = %v", err)
	}

	page, err := m.ListActiveView(ctx, key.Kind, key.UID, metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("ListActiveView() rows = %+v, want stale cache activity hidden by delete barrier", page.Rows)
	}
}

func TestFlushFencesStaleActivityWithoutListBeforeNewerActivity(t *testing.T) {
	ctx := context.Background()
	key := metadb.ConversationStateKey{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "deleted",
		ChannelType: 2,
	}
	store := &recordingActiveStore{primary: map[metadb.ConversationStateKey]metadb.ConversationState{
		key: {
			UID:          key.UID,
			Kind:         key.Kind,
			ChannelID:    key.ChannelID,
			ChannelType:  key.ChannelType,
			DeletedToSeq: 10,
		},
	}}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType),
		ActiveAtMS: 2_000, ReadSeq: 10, MessageSeq: 10,
	}}); err != nil {
		t.Fatalf("MarkActive(stale) error = %v", err)
	}
	result, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(stale) error = %v", err)
	}
	if result.Skipped != 0 || result.DeleteFenced != 1 || result.Superseded != 1 || result.Persisted != 0 || m.DirtyCountForTest() != 0 {
		t.Fatalf("Flush(stale) = %+v dirty=%d, want durable barrier to remove stale activity without a touch", result, m.DirtyCountForTest())
	}
	if _, ok := m.EntryForTest(key.Kind, key.UID, key.ChannelID, uint8(key.ChannelType)); ok {
		t.Fatal("stale activity remained cached after durable barrier filtering")
	}

	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType),
		ActiveAtMS: 2_100, MessageSeq: 11,
	}}); err != nil {
		t.Fatalf("MarkActive(newer) error = %v", err)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows after newer activity = %d, want durable reactivation without list hydration", got)
	}
	result, err = m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(newer) error = %v", err)
	}
	if result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(newer) = %+v, want sequence above barrier persisted", result)
	}
	last := store.touches[len(store.touches)-1][0]
	if last.MessageSeq != 11 || last.ActiveAt != 2_100 {
		t.Fatalf("persisted newer activity = %+v, want message_seq=11 active_at=2100", last)
	}
}

func TestListActiveViewPropagatesCacheOnlyHydrationError(t *testing.T) {
	ctx := context.Background()
	lookupErr := errors.New("primary lookup failed")
	store := &recordingActiveStore{lookupErr: lookupErr}
	m := NewManager(Options{Store: store})
	err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "cache-only", ChannelType: 2, ActiveAtMS: 1000}})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	_, err = m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("ListActiveView() error = %v, want %v", err, lookupErr)
	}
}

func TestListActiveViewRequiresStore(t *testing.T) {
	m := NewManager(Options{})
	if err := m.MarkActive(context.Background(), []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "cache-only", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	page, err := m.ListActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("ListActiveView() error = %v, want %v", err, ErrStoreRequired)
	}
	if len(page.Rows) != 0 || page.Done {
		t.Fatalf("page=%+v, want no authoritative rows and done=false on missing store", page)
	}
}

func TestListActiveViewPaginatesCacheRowsWithCursorAndLimit(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}})
	err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAtMS: 300},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "b", ChannelType: 1, ActiveAtMS: 200},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAtMS: 200},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "c", ChannelType: 2, ActiveAtMS: 100},
	})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	first, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 2)
	if err != nil {
		t.Fatalf("ListActiveView(first) error = %v", err)
	}
	if activeChannelIDs(first.Rows) != "a,b" || first.Cursor != (metadb.ConversationActiveCursor{ActiveAt: 200, ChannelID: "b", ChannelType: 1}) || first.Done {
		t.Fatalf("first page=%+v, want first two cache rows with done=false", first)
	}

	second, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", first.Cursor, 10)
	if err != nil {
		t.Fatalf("ListActiveView(second) error = %v", err)
	}
	if activeChannelIDs(second.Rows) != "b,c" || second.Cursor != (metadb.ConversationActiveCursor{ActiveAt: 100, ChannelID: "c", ChannelType: 2}) || !second.Done {
		t.Fatalf("second page=%+v, want remaining cache rows with done=true", second)
	}
}

func TestListActiveViewNonPositiveLimitReturnsEmptyDonePage(t *testing.T) {
	store := &recordingActiveStore{rows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}}}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(context.Background(), []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "cache", ChannelType: 2, ActiveAtMS: 200}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	after := metadb.ConversationActiveCursor{ActiveAt: 300, ChannelID: "before", ChannelType: 2}

	page, err := m.ListActiveView(context.Background(), metadb.ConversationKindNormal, "u1", after, 0)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}
	if len(page.Rows) != 0 || page.Cursor != after || !page.Done {
		t.Fatalf("page=%+v, want empty done page retaining cursor", page)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want no DB call for non-positive limit", store.calls)
	}
}

func TestFlushDirtyPersistsActiveRowsAndClearsDirty(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 7, MessageSeq: 42}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	requireCacheIndexConservation(t, m)
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want 1", got)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Persisted != 1 {
		t.Fatalf("Flush() persisted = %d, want 1", result.Persisted)
	}
	if result.Cleared != 1 || result.VersionConflicts != 0 || result.Requeued != 0 {
		t.Fatalf("Flush() result = %+v, want persisted=1 cleared=1 with no requeue", result)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want 0", got)
	}
	requireCacheIndexConservation(t, m)

	want := metadb.ConversationActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room-1",
		ChannelType: 2,
		ReadSeq:     7,
		ActiveAt:    1000,
		UpdatedAt:   1000,
		MessageSeq:  42,
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 {
		t.Fatalf("touches = %+v, want one patch", store.touches)
	}
	if got := store.touches[0][0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("touch patch = %+v, want %+v", got, want)
	}
	if patch := store.touches[0][0]; patch.DeletedToSeq != 0 || patch.MessageSeq != 42 || patch.SparseActive || patch.SparseActiveSet {
		t.Fatalf("touch patch set unmanaged fields: %+v", patch)
	}
}

func TestFlushPersistsKindAwarePatches(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindCMD, UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 7}}); err != nil {
		t.Fatalf("MarkActive(): %v", err)
	}
	if _, err := m.Flush(ctx, 10); err != nil {
		t.Fatalf("Flush(): %v", err)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 {
		t.Fatalf("touches = %+v", store.touches)
	}
	if got := store.touches[0][0]; got.Kind != metadb.ConversationKindCMD || got.ChannelID != "g1____cmd" || got.ReadSeq != 7 {
		t.Fatalf("touch patch = %+v", got)
	}
}

func TestFlushSkipsReceiverActiveWithinCooldown(t *testing.T) {
	ctx := context.Background()
	const previousActiveAt int64 = 1000
	const nextActiveAt int64 = previousActiveAt + int64(time.Hour/time.Millisecond)
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room-1", ChannelType: 2}: {
				UID:         "u1",
				Kind:        metadb.ConversationKindNormal,
				ChannelID:   "room-1",
				ChannelType: 2,
				ActiveAt:    previousActiveAt,
			},
		},
	}
	m := NewManager(Options{Store: store, ActiveCooldown: 2 * time.Hour, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: nextActiveAt}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	requireCacheIndexConservation(t, m)

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Selected != 1 || result.Persisted != 0 || result.Skipped != 1 || result.Cleared != 1 {
		t.Fatalf("Flush() result = %+v, want selected=1 skipped=1 cleared=1 persisted=0", result)
	}
	if len(store.touches) != 0 {
		t.Fatalf("touches = %+v, want no durable touch inside cooldown", store.touches)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want 0", got)
	}
	entry, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "room-1", 2)
	if !ok {
		t.Fatalf("expected cache entry to remain after filtered flush")
	}
	if entry.ActiveAtMS != nextActiveAt {
		t.Fatalf("cached ActiveAtMS = %d, want latest observed active_at %d", entry.ActiveAtMS, nextActiveAt)
	}
	page, err := m.ListActiveView(ctx, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ActiveAt != nextActiveAt {
		t.Fatalf("ListActiveView() rows = %+v, want immediate latest active_at %d", page.Rows, nextActiveAt)
	}
	requireCacheIndexConservation(t, m)
}

func TestCleanReceiverWithinCooldownDoesNotRedirty(t *testing.T) {
	ctx := context.Background()
	const previousActiveAt int64 = 1_000
	cooldown := 2 * time.Hour
	key := metadb.ConversationStateKey{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room-1",
		ChannelType: 2,
	}
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			key: {
				UID:         key.UID,
				Kind:        key.Kind,
				ChannelID:   key.ChannelID,
				ChannelType: key.ChannelType,
				ActiveAt:    previousActiveAt,
			},
		},
	}
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		Store:          store,
		ActiveCooldown: cooldown,
		MaxCachedRows:  1,
		Observer:       observer,
	})
	initial := ActivePatch{
		Kind:        key.Kind,
		UID:         key.UID,
		ChannelID:   key.ChannelID,
		ChannelType: uint8(key.ChannelType),
		ActiveAtMS:  previousActiveAt + int64(time.Hour/time.Millisecond),
	}
	if err := m.MarkActive(ctx, []ActivePatch{initial}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	if result, err := m.Flush(ctx, 0); err != nil || result.Skipped != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(initial) = %+v, %v, want one cooldown skip and clear", result, err)
	}
	address := cacheAddress{uid: key.UID, key: conversationKey{kind: key.Kind, channelID: key.ChannelID, channelType: uint8(key.ChannelType)}}
	version := m.cache[key.UID][address.key].version
	lookups := len(store.batchKeys)

	withinCooldown := initial
	withinCooldown.ActiveAtMS = previousActiveAt + int64(90*time.Minute/time.Millisecond)
	if err := m.MarkActive(ctx, []ActivePatch{withinCooldown}); err != nil {
		t.Fatalf("MarkActive(within cooldown) error = %v", err)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want clean receiver row to stay clean inside cooldown", got)
	}
	entry := m.cache[key.UID][address.key]
	if entry.version <= version || entry.patch.ActiveAtMS != withinCooldown.ActiveAtMS {
		t.Fatalf("cache entry = %+v, want latest observed active_at=%d with a new version after suppression", entry, withinCooldown.ActiveAtMS)
	}
	if mutation := observer.lastMutation(t); mutation.CooldownSuppressed != 1 || mutation.Unchanged != 0 || mutation.BecameDirty != 0 || mutation.DirtyUpdated != 0 {
		t.Fatalf("receiver cooldown mutation = %+v, want one cooldown-suppressed row", mutation)
	}
	page, err := m.ListActiveView(ctx, key.Kind, key.UID, metadb.ConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListActiveView(after suppression) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ActiveAt != withinCooldown.ActiveAtMS {
		t.Fatalf("ListActiveView(after suppression) rows = %+v, want immediate active_at=%d", page.Rows, withinCooldown.ActiveAtMS)
	}
	result, err := m.Flush(ctx, 0)
	if err != nil || result.Selected != 0 {
		t.Fatalf("Flush(after suppressed receiver update) = %+v, %v, want no dirty selection", result, err)
	}
	if got := len(store.batchKeys); got != lookups {
		t.Fatalf("durable lookup keys = %d, want unchanged %d", got, lookups)
	}
	requireCacheIndexConservation(t, m)
}

func TestCleanReceiverCooldownSuppressionBoundaries(t *testing.T) {
	ctx := context.Background()
	const baselineActiveAt int64 = 1_000
	cooldown := time.Hour
	cooldownMS := int64(cooldown / time.Millisecond)
	tests := []struct {
		name       string
		cooldown   time.Duration
		activeAtMS int64
		readSeq    uint64
		hashSlot   uint16
		wantDirty  bool
	}{
		{name: "inside cooldown", cooldown: cooldown, activeAtMS: baselineActiveAt + cooldownMS - 1, hashSlot: 1},
		{name: "at cooldown", cooldown: cooldown, activeAtMS: baselineActiveAt + cooldownMS, hashSlot: 1, wantDirty: true},
		{name: "after cooldown", cooldown: cooldown, activeAtMS: baselineActiveAt + cooldownMS + 1, hashSlot: 1, wantDirty: true},
		{name: "sender read sequence advances", cooldown: cooldown, activeAtMS: baselineActiveAt + 1, readSeq: 10, hashSlot: 1, wantDirty: true},
		{name: "hash slot changes", cooldown: cooldown, activeAtMS: baselineActiveAt + 1, hashSlot: 2, wantDirty: true},
		{name: "cooldown disabled", activeAtMS: baselineActiveAt + 1, hashSlot: 1, wantDirty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager(Options{Store: &recordingActiveStore{}, ActiveCooldown: tt.cooldown, MaxCachedRows: 1})
			baseline := ActivePatch{
				Kind:        metadb.ConversationKindNormal,
				UID:         "u1",
				ChannelID:   "room-1",
				ChannelType: 2,
				ActiveAtMS:  baselineActiveAt,
				ReadSeq:     9,
			}
			if err := m.MarkActiveForHashSlot(ctx, 1, []ActivePatch{baseline}); err != nil {
				t.Fatalf("MarkActiveForHashSlot(baseline) error = %v", err)
			}
			if result, err := m.Flush(ctx, 0); err != nil || result.Persisted != 1 || result.Cleared != 1 {
				t.Fatalf("Flush(baseline) = %+v, %v, want persisted clean baseline", result, err)
			}
			incoming := baseline
			incoming.ActiveAtMS = tt.activeAtMS
			incoming.ReadSeq = tt.readSeq
			if err := m.MarkActiveForHashSlot(ctx, tt.hashSlot, []ActivePatch{incoming}); err != nil {
				t.Fatalf("MarkActiveForHashSlot(incoming) error = %v", err)
			}
			if got := m.DirtyCountForTest(); (got == 1) != tt.wantDirty {
				t.Fatalf("DirtyCountForTest() = %d, wantDirty=%v", got, tt.wantDirty)
			}
			requireCacheIndexConservation(t, m)
		})
	}
}

func TestApplyConversationDeletesUsesLatestMessageSequenceBarrier(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour, MaxCachedRows: 8})
	for _, patch := range []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 10},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "new", ChannelType: 2, ActiveAtMS: 2_000, MessageSeq: 20},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "unknown", ChannelType: 2, ActiveAtMS: 3_000},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "other", ChannelType: 2, ActiveAtMS: 4_000, MessageSeq: 40},
	} {
		if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{patch}); err != nil {
			t.Fatalf("MarkActiveForHashSlot(%s) error = %v", patch.ChannelID, err)
		}
	}
	if result, err := m.Flush(ctx, 0); err != nil || result.Cleared != 4 {
		t.Fatalf("Flush(initial) = %+v, %v, want four clean rows", result, err)
	}

	deletes := []metadb.ConversationDelete{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "old", ChannelType: 2, DeletedToSeq: 10},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new", ChannelType: 2, DeletedToSeq: 15},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "unknown", ChannelType: 2, DeletedToSeq: 15},
	}
	m.ApplyConversationDeletes(deletes)

	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "old", 2); ok {
		t.Fatal("message at the delete barrier remained cached")
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "unknown", 2); ok {
		t.Fatal("message with an unknown sequence remained cached")
	}
	newer, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "new", 2)
	if !ok || newer.ActiveAtMS != 2_000 || newer.MessageSeq != 20 {
		t.Fatalf("newer row = %+v present=%t, want latest view retained", newer, ok)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "other", 2); !ok {
		t.Fatal("unrelated row was removed")
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows = %d, want only the newer row forced dirty", got)
	}
	entry := m.cache["u1"][conversationKey{kind: metadb.ConversationKindNormal, channelID: "new", channelType: 2}]
	if entry.durableActiveAtMS != 0 || !entry.dirty {
		t.Fatalf("newer cache entry = %+v, want invalid durable baseline and dirty retry", entry)
	}
	m.ApplyConversationDeletes(deletes)
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows after duplicate apply = %d, want idempotent one", got)
	}
	repeated := m.cache["u1"][conversationKey{kind: metadb.ConversationKindNormal, channelID: "new", channelType: 2}]
	if repeated.version != entry.version {
		t.Fatalf("duplicate delete advanced version from %d to %d", entry.version, repeated.version)
	}
	m.ApplyConversationDeletes([]metadb.ConversationDelete{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new", ChannelType: 2, DeletedToSeq: 14,
	}})
	lower := m.cache["u1"][conversationKey{kind: metadb.ConversationKindNormal, channelID: "new", channelType: 2}]
	if lower.version != entry.version || lower.deleteBarrier != 15 {
		t.Fatalf("lower delete barrier changed cache entry from %+v to %+v", entry, lower)
	}
	requireCacheIndexConservation(t, m)

	store.primary = map[metadb.ConversationStateKey]metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new", ChannelType: 2}: {
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new", ChannelType: 2, DeletedToSeq: 15,
		},
	}
	if result, err := m.Flush(ctx, 1); err != nil || result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(newer) = %+v, %v, want newer sequence persisted", result, err)
	}
	last := store.touches[len(store.touches)-1][0]
	if last.ChannelID != "new" || last.MessageSeq != 20 || last.ActiveAt != 2_000 {
		t.Fatalf("persisted newer patch = %+v", last)
	}
	requireCacheIndexConservation(t, m)
}

func TestEqualDeleteBarrierRetryRedirtiesCleanNewerActivity(t *testing.T) {
	ctx := context.Background()
	key := metadb.ConversationStateKey{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room",
		ChannelType: 2,
	}
	store := &recordingActiveStore{primary: map[metadb.ConversationStateKey]metadb.ConversationState{
		key: {
			UID:         key.UID,
			Kind:        key.Kind,
			ChannelID:   key.ChannelID,
			ChannelType: key.ChannelType,
			ActiveAt:    1_000,
		},
	}}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType),
		ActiveAtMS: 1_100, MessageSeq: 11,
	}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	deletion := metadb.ConversationDelete{
		UID: key.UID, Kind: key.Kind, ChannelID: key.ChannelID, ChannelType: key.ChannelType, DeletedToSeq: 10,
	}

	// A confirmed callback can race a stale durable read snapshot that does not
	// yet expose the barrier, allowing one cooldown skip to leave the row clean.
	m.ApplyConversationDeletes([]metadb.ConversationDelete{deletion})
	result, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(before durable hide) error = %v", err)
	}
	if result.Skipped != 1 || result.Cleared != 1 || m.DirtyCountForTest() != 0 {
		t.Fatalf("Flush(before durable hide) = %+v dirty=%d, want cooldown skip to leave the newer row clean", result, m.DirtyCountForTest())
	}

	// Later hydration rediscovers the same confirmed barrier. Equal-barrier
	// reconciliation must re-invalidate the clean row while durable ActiveAt is
	// still cleared.
	store.primary[key] = metadb.ConversationState{
		UID: key.UID, Kind: key.Kind, ChannelID: key.ChannelID, ChannelType: key.ChannelType, DeletedToSeq: 10,
	}
	m.ApplyConversationDeletes([]metadb.ConversationDelete{deletion})
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows after equal confirmed retry = %d, want newer activity forced dirty", got)
	}
	result, err = m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush(after confirmed retry) error = %v", err)
	}
	if result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(after confirmed retry) = %+v, want newer activity reactivated durably", result)
	}
	last := store.touches[len(store.touches)-1][0]
	if last.MessageSeq != 11 || last.ActiveAt != 1_100 {
		t.Fatalf("reactivation patch = %+v, want message_seq=11 active_at=1100", last)
	}
	requireCacheIndexConservation(t, m)
}

func TestSuppressedReceiverAfterDeleteBecomesDirtyAndPersists(t *testing.T) {
	ctx := context.Background()
	key := metadb.ConversationStateKey{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2}
	store := &recordingActiveStore{primary: map[metadb.ConversationStateKey]metadb.ConversationState{
		key: {UID: key.UID, Kind: key.Kind, ChannelID: key.ChannelID, ChannelType: key.ChannelType, ActiveAt: 1_000},
	}}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour, MaxCachedRows: 1})
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType), ActiveAtMS: 1_500, MessageSeq: 10,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(initial) error = %v", err)
	}
	if result, err := m.Flush(ctx, 1); err != nil || result.Skipped != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(initial) = %+v, %v, want cooldown skip", result, err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType), ActiveAtMS: 1_600, MessageSeq: 11,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(suppressed) error = %v", err)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("dirty rows before delete = %d, want suppressed clean row", got)
	}

	m.ApplyConversationDeletes([]metadb.ConversationDelete{{
		UID: key.UID, Kind: key.Kind, ChannelID: key.ChannelID, ChannelType: key.ChannelType, DeletedToSeq: 11,
	}})
	if _, ok := m.EntryForTest(key.Kind, key.UID, key.ChannelID, uint8(key.ChannelType)); ok {
		t.Fatal("suppressed row at delete barrier remained cached")
	}
	store.primary[key] = metadb.ConversationState{
		UID: key.UID, Kind: key.Kind, ChannelID: key.ChannelID, ChannelType: key.ChannelType, DeletedToSeq: 11,
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: key.Kind, UID: key.UID, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType), ActiveAtMS: 1_700, MessageSeq: 12,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(after delete) error = %v", err)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows after delete = %d, want newer receiver dirty", got)
	}
	if result, err := m.Flush(ctx, 1); err != nil || result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(after delete) = %+v, %v, want newer receiver persisted", result, err)
	}
	last := store.touches[len(store.touches)-1][0]
	if last.MessageSeq != 12 || last.ActiveAt != 1_700 {
		t.Fatalf("persisted after-delete patch = %+v, want message_seq=12 active_at=1700", last)
	}
	requireCacheIndexConservation(t, m)
}

func TestInvalidateConversationDeleteAttemptsPreservesUnconfirmedRows(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 4})
	patches := []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 5},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "new", ChannelType: 2, ActiveAtMS: 2_000, MessageSeq: 20},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "unknown", ChannelType: 2, ActiveAtMS: 3_000},
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, patches); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	if result, err := m.Flush(ctx, 0); err != nil || result.Cleared != len(patches) {
		t.Fatalf("Flush(initial) = %+v, %v, want clean cached rows", result, err)
	}

	deletes := []metadb.ConversationDelete{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "old", ChannelType: 2, DeletedToSeq: 10},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new", ChannelType: 2, DeletedToSeq: 10},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "unknown", ChannelType: 2, DeletedToSeq: 10},
	}
	m.InvalidateConversationDeleteAttempts(deletes)
	for _, patch := range patches {
		got, ok := m.EntryForTest(patch.Kind, patch.UID, patch.ChannelID, patch.ChannelType)
		if !ok || got.ActiveAtMS != patch.ActiveAtMS || got.MessageSeq != patch.MessageSeq {
			t.Fatalf("unconfirmed row %s = %+v present=%t, want preserved cache projection %+v", patch.ChannelID, got, ok, patch)
		}
		entry := m.cache[patch.UID][conversationKey{kind: patch.Kind, channelID: patch.ChannelID, channelType: patch.ChannelType}]
		if !entry.dirty || entry.durableActiveAtMS != 0 || entry.deleteBarrier != 0 {
			t.Fatalf("unconfirmed cache entry %s = %+v, want dirty invalid baseline without confirmed barrier", patch.ChannelID, entry)
		}
	}
	if got := m.DirtyCountForTest(); got != len(patches) {
		t.Fatalf("dirty rows = %d, want all %d uncertain rows retried", got, len(patches))
	}
	if result, err := m.Flush(ctx, 0); err != nil || result.Persisted != len(patches) || result.Cleared != len(patches) {
		t.Fatalf("Flush(unconfirmed) = %+v, %v, want rows restored when durable barriers were not committed", result, err)
	}
	for _, patch := range patches {
		if _, ok := m.EntryForTest(patch.Kind, patch.UID, patch.ChannelID, patch.ChannelType); !ok {
			t.Fatalf("unconfirmed row %s disappeared after successful retry flush", patch.ChannelID)
		}
	}
	requireCacheIndexConservation(t, m)
}

func TestApplyConversationDeletesRemovesDirtyOldRowAndPreservesUnrelatedDirty(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 4})
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "deleted", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 5},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "unrelated", ChannelType: 2, ActiveAtMS: 2_000, MessageSeq: 6},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	m.ApplyConversationDeletes([]metadb.ConversationDelete{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "deleted", ChannelType: 2, DeletedToSeq: 5,
	}})
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "deleted", 2); ok {
		t.Fatal("dirty row at the delete barrier remained cached")
	}
	if row, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "unrelated", 2); !ok || row.MessageSeq != 6 {
		t.Fatalf("unrelated dirty row = %+v present=%t", row, ok)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows = %d, want one unrelated row", got)
	}
	requireCacheIndexConservation(t, m)
}

func TestDeleteInvalidationFencesInFlightDurableBaseline(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 1})
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 2_000, MessageSeq: 20,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	store.touchHook = func() {
		store.touchHook = nil
		m.ApplyConversationDeletes([]metadb.ConversationDelete{{
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, DeletedToSeq: 15,
		}})
	}
	result, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Persisted != 1 || result.Cleared != 0 || result.VersionConflicts != 1 || result.Requeued != 1 {
		t.Fatalf("Flush() = %+v, want persisted stale snapshot fenced by delete invalidation", result)
	}
	address := cacheAddress{uid: "u1", key: conversationKey{kind: metadb.ConversationKindNormal, channelID: "room", channelType: 2}}
	entry := m.cache[address.uid][address.key]
	if !entry.dirty || entry.durableActiveAtMS != 0 || entry.patch.MessageSeq != 20 {
		t.Fatalf("cache entry after in-flight delete = %+v, want dirty newer view with invalid baseline", entry)
	}
	requireCacheIndexConservation(t, m)
}

func TestPurgeCleanHashSlotPreservesDirtyAndOtherSlots(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 4})
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "clean-7", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 1,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(clean 7) error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 7, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(7) = %+v, %v", result, err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "dirty-7", ChannelID: "room", ChannelType: 2, ActiveAtMS: 2_000, MessageSeq: 2,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(dirty 7) error = %v", err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 9, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "clean-9", ChannelID: "room", ChannelType: 2, ActiveAtMS: 3_000, MessageSeq: 3,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(9) error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 9, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(9) = %+v, %v", result, err)
	}

	if purged := m.PurgeCleanHashSlot(7); purged != 1 {
		t.Fatalf("PurgeCleanHashSlot(7) = %d, want 1", purged)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "clean-7", "room", 2); ok {
		t.Fatal("clean target-slot row remained cached")
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "dirty-7", "room", 2); !ok {
		t.Fatal("dirty target-slot row was purged")
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "clean-9", "room", 2); !ok {
		t.Fatal("clean other-slot row was purged")
	}
	if purged := m.PurgeCleanHashSlot(7); purged != 0 {
		t.Fatalf("duplicate PurgeCleanHashSlot(7) = %d, want 0", purged)
	}
	requireCacheIndexConservation(t, m)
}

func TestPurgeCleanHashSlotWorksWithoutBoundedCleanIndex(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}})
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "clean-7", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 1,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 7, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot() = %+v, %v", result, err)
	}
	if purged := m.PurgeCleanHashSlot(7); purged != 1 {
		t.Fatalf("PurgeCleanHashSlot(7) = %d, want 1", purged)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "clean-7", "room", 2); ok {
		t.Fatal("unbounded manager retained the clean target-slot row")
	}
	requireCacheIndexConservation(t, m)
}

func TestPurgeCleanHashSlotTracksCleanDirtyAndSlotTransitions(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 2})
	patch := ActivePatch{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 1,
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{patch}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(slot 7) error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 7, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(7) = %+v, %v", result, err)
	}
	requireCacheIndexConservation(t, m)

	patch.ActiveAtMS = 2_000
	patch.MessageSeq = 2
	if err := m.MarkActiveForHashSlot(ctx, 9, []ActivePatch{patch}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(slot 9) error = %v", err)
	}
	if purged := m.PurgeCleanHashSlot(7); purged != 0 {
		t.Fatalf("PurgeCleanHashSlot(7) = %d, want moved dirty row preserved", purged)
	}
	if purged := m.PurgeCleanHashSlot(9); purged != 0 {
		t.Fatalf("PurgeCleanHashSlot(9) = %d, want dirty row preserved", purged)
	}
	requireCacheIndexConservation(t, m)

	if result, err := m.FlushHashSlot(ctx, 9, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(9) = %+v, %v", result, err)
	}
	if purged := m.PurgeCleanHashSlot(9); purged != 1 {
		t.Fatalf("PurgeCleanHashSlot(9) = %d, want clean moved row purged", purged)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2); ok {
		t.Fatal("clean moved row remained cached")
	}
	requireCacheIndexConservation(t, m)
}

func TestPurgeCleanHashSlotStateOnlyDefersCacheObservation(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 1, Observer: observer})
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000, MessageSeq: 1,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 7, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot() = %+v, %v", result, err)
	}
	observationsBeforePurge := len(observer.cache)

	if purged := m.PurgeCleanHashSlotStateOnly(7); purged != 1 {
		t.Fatalf("PurgeCleanHashSlotStateOnly() = %d, want 1", purged)
	}
	if len(observer.cache) != observationsBeforePurge {
		t.Fatalf("state-only purge emitted %d cache observations, want none", len(observer.cache)-observationsBeforePurge)
	}
	m.ObserveCacheState()
	if len(observer.cache) != observationsBeforePurge+1 {
		t.Fatalf("ObserveCacheState() cache observations = %d, want %d", len(observer.cache), observationsBeforePurge+1)
	}
	if got := observer.lastCache(t); got.Rows != 0 || got.DirtyRows != 0 {
		t.Fatalf("observed cache after purge = %+v, want empty", got)
	}
	if err := m.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u2", ChannelID: "room", ChannelType: 2, ActiveAtMS: 2_000, MessageSeq: 2,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(second row) error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 7, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(second row) = %+v, %v", result, err)
	}
	observationsBeforeNormalPurge := len(observer.cache)
	if purged := m.PurgeCleanHashSlot(7); purged != 1 {
		t.Fatalf("PurgeCleanHashSlot() = %d, want 1", purged)
	}
	if len(observer.cache) != observationsBeforeNormalPurge+1 {
		t.Fatalf("normal purge cache observations = %d, want %d", len(observer.cache), observationsBeforeNormalPurge+1)
	}
	requireCacheIndexConservation(t, m)
}

func TestFlushCooldownSkipRetainsConcurrentNewerVersion(t *testing.T) {
	ctx := context.Background()
	key := metadb.ConversationStateKey{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room-1", ChannelType: 2}
	lookupStarted := make(chan struct{})
	releaseLookup := make(chan struct{})
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			key: {UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room-1", ChannelType: 2, ActiveAt: 1_000},
		},
		batchLookupHook: func() {
			close(lookupStarted)
			<-releaseLookup
		},
	}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1_100, MessageSeq: 10}}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}

	type flushOutcome struct {
		result FlushResult
		err    error
	}
	finished := make(chan flushOutcome, 1)
	go func() {
		result, err := m.Flush(ctx, 1)
		finished <- flushOutcome{result: result, err: err}
	}()
	<-lookupStarted
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1_200, MessageSeq: 11}}); err != nil {
		t.Fatalf("MarkActive(concurrent) error = %v", err)
	}
	close(releaseLookup)
	outcome := <-finished
	if outcome.err != nil {
		t.Fatalf("Flush() error = %v", outcome.err)
	}
	if got := outcome.result; got.Selected != 1 || got.Persisted != 0 || got.Skipped != 1 || got.Cleared != 0 || got.VersionConflicts != 1 || got.Requeued != 1 || got.Superseded != 0 {
		t.Fatalf("Flush() result = %+v, want selected=skipped=requeued=1 with one version conflict", got)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows = %d, want concurrent newer version retained", got)
	}
	address := cacheAddress{uid: "u1", key: conversationKey{kind: metadb.ConversationKindNormal, channelID: "room-1", channelType: 2}}
	entry := m.cache[address.uid][address.key]
	if entry.durableActiveAtMS != 1_000 || entry.patch.ActiveAtMS != 1_200 || entry.patch.MessageSeq != 11 {
		t.Fatalf("cache after version conflict = %+v, want confirmed baseline 1000 and latest view 1200/seq11", entry)
	}
	store.batchLookupHook = nil
	if result, err := m.Flush(ctx, 1); err != nil || result.Skipped != 1 || result.Cleared != 1 {
		t.Fatalf("Flush(retry) = %+v, %v, want cooldown skip and clear", result, err)
	}
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1_300, MessageSeq: 12,
	}}); err != nil {
		t.Fatalf("MarkActive(after confirmed baseline) error = %v", err)
	}
	entry = m.cache[address.uid][address.key]
	if entry.dirty || entry.durableActiveAtMS != 1_000 || entry.patch.ActiveAtMS != 1_300 || entry.patch.MessageSeq != 12 {
		t.Fatalf("cache after clean suppression = %+v, want clean latest view on confirmed baseline", entry)
	}
}

func TestFlushKeepsSenderActiveWithinCooldown(t *testing.T) {
	ctx := context.Background()
	const previousActiveAt int64 = 1000
	const nextActiveAt int64 = previousActiveAt + int64(time.Hour/time.Millisecond)
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room-1", ChannelType: 2}: {
				UID:         "u1",
				Kind:        metadb.ConversationKindNormal,
				ChannelID:   "room-1",
				ChannelType: 2,
				ActiveAt:    previousActiveAt,
				ReadSeq:     7,
			},
		},
	}
	m := NewManager(Options{Store: store, ActiveCooldown: 2 * time.Hour})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: nextActiveAt, ReadSeq: 9}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Selected != 1 || result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush() result = %+v, want selected=1 persisted=1 cleared=1", result)
	}
	want := metadb.ConversationActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "room-1",
		ChannelType: 2,
		ReadSeq:     9,
		ActiveAt:    nextActiveAt,
		UpdatedAt:   nextActiveAt,
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 {
		t.Fatalf("touches = %+v, want one sender patch", store.touches)
	}
	if got := store.touches[0][0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("touch patch = %+v, want %+v", got, want)
	}
}

func TestFlushReportsFilterFailureAsRequeued(t *testing.T) {
	ctx := context.Background()
	lookupErr := errors.New("durable state lookup failed")
	observer := &recordingConversationActiveObserver{}
	store := &recordingActiveStore{lookupErr: lookupErr}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour, Observer: observer})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	result, err := m.Flush(ctx, 1)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("Flush() error = %v, want %v", err, lookupErr)
	}
	if result.Selected != 1 || result.Persisted != 0 || result.Requeued != 1 {
		t.Fatalf("Flush() result = %+v, want selected=1 persisted=0 requeued=1", result)
	}
	observation := observer.lastFlush(t)
	if observation.Result != "error" || observation.FailureStage != "filter" || observation.Requeued != 1 {
		t.Fatalf("flush observation = %+v, want filter error with one requeued row", observation)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows = %d, want failed filter row retained", got)
	}
}

func TestFlushPersistFailureAccountsDeleteFencedRowsWithoutRequeueingThem(t *testing.T) {
	ctx := context.Background()
	deleteKey := metadb.ConversationStateKey{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "deleted", ChannelType: 2,
	}
	liveKey := metadb.ConversationStateKey{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "live", ChannelType: 2,
	}
	touchErr := errors.New("touch failed")
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			deleteKey: {
				UID: deleteKey.UID, Kind: deleteKey.Kind, ChannelID: deleteKey.ChannelID,
				ChannelType: deleteKey.ChannelType, DeletedToSeq: 10,
			},
		},
		touchErr: touchErr,
	}
	observer := &recordingConversationActiveObserver{}
	manager := NewManager(Options{Store: store, ActiveCooldown: time.Hour, Observer: observer})
	if err := manager.MarkActiveForHashSlot(ctx, 7, []ActivePatch{
		{UID: deleteKey.UID, Kind: deleteKey.Kind, ChannelID: deleteKey.ChannelID, ChannelType: uint8(deleteKey.ChannelType), ActiveAtMS: 1_000, MessageSeq: 10},
		{UID: liveKey.UID, Kind: liveKey.Kind, ChannelID: liveKey.ChannelID, ChannelType: uint8(liveKey.ChannelType), ActiveAtMS: 2_000, MessageSeq: 11},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}

	result, err := manager.FlushHashSlot(ctx, 7, 2)
	if !errors.Is(err, touchErr) {
		t.Fatalf("FlushHashSlot() error = %v, want %v", err, touchErr)
	}
	if result.Selected != 2 || result.Persisted != 0 || result.Skipped != 0 || result.DeleteFenced != 1 || result.Requeued != 1 {
		t.Fatalf("FlushHashSlot() = %+v, want selected=2 delete_fenced=1 requeued=1", result)
	}
	if _, ok := manager.EntryForTest(deleteKey.Kind, deleteKey.UID, deleteKey.ChannelID, uint8(deleteKey.ChannelType)); ok {
		t.Fatal("delete-fenced row remained cached after durable reconciliation")
	}
	if _, ok := manager.EntryForTest(liveKey.Kind, liveKey.UID, liveKey.ChannelID, uint8(liveKey.ChannelType)); !ok {
		t.Fatal("persist-failed live row disappeared instead of remaining dirty")
	}
	if got := manager.DirtyCountForTest(); got != 1 {
		t.Fatalf("dirty rows = %d, want only the persist-failed live row", got)
	}
	observation := observer.lastFlush(t)
	if observation.Result != "error" || observation.FailureStage != "persist" || observation.Selected != 2 ||
		observation.DeleteFenced != 1 || observation.Requeued != 1 {
		t.Fatalf("flush observation = %+v, want persist error with delete_fenced=1 requeued=1", observation)
	}
	requireCacheIndexConservation(t, manager)
}

func TestFlushPersistFailureCountsNewerActivityAfterDeleteFenceAsRequeued(t *testing.T) {
	ctx := context.Background()
	deleteKey := metadb.ConversationStateKey{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "deleted", ChannelType: 2,
	}
	liveKey := metadb.ConversationStateKey{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "live", ChannelType: 2,
	}
	touchErr := errors.New("touch failed")
	store := &recordingActiveStore{
		primary: map[metadb.ConversationStateKey]metadb.ConversationState{
			deleteKey: {
				UID: deleteKey.UID, Kind: deleteKey.Kind, ChannelID: deleteKey.ChannelID,
				ChannelType: deleteKey.ChannelType, DeletedToSeq: 10,
			},
		},
		touchErr: touchErr,
	}
	observer := &recordingConversationActiveObserver{}
	manager := NewManager(Options{Store: store, ActiveCooldown: time.Hour, Observer: observer})
	if err := manager.MarkActiveForHashSlot(ctx, 7, []ActivePatch{
		{UID: deleteKey.UID, Kind: deleteKey.Kind, ChannelID: deleteKey.ChannelID, ChannelType: uint8(deleteKey.ChannelType), ActiveAtMS: 1_000, MessageSeq: 10},
		{UID: liveKey.UID, Kind: liveKey.Kind, ChannelID: liveKey.ChannelID, ChannelType: uint8(liveKey.ChannelType), ActiveAtMS: 2_000, MessageSeq: 11},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot() error = %v", err)
	}
	var concurrentErr error
	store.touchHook = func() {
		store.touchHook = nil
		concurrentErr = manager.MarkActiveForHashSlot(ctx, 7, []ActivePatch{{
			UID: deleteKey.UID, Kind: deleteKey.Kind, ChannelID: deleteKey.ChannelID,
			ChannelType: uint8(deleteKey.ChannelType), ActiveAtMS: 3_000, MessageSeq: 11,
		}})
	}

	result, err := manager.FlushHashSlot(ctx, 7, 2)
	if concurrentErr != nil {
		t.Fatalf("concurrent MarkActiveForHashSlot() error = %v", concurrentErr)
	}
	if !errors.Is(err, touchErr) {
		t.Fatalf("FlushHashSlot() error = %v, want %v", err, touchErr)
	}
	if result.Selected != 2 || result.DeleteFenced != 1 || result.Requeued != 2 {
		t.Fatalf("FlushHashSlot() = %+v, want selected=2 delete_fenced=1 requeued=2 after newer activity", result)
	}
	newer, ok := manager.EntryForTest(deleteKey.Kind, deleteKey.UID, deleteKey.ChannelID, uint8(deleteKey.ChannelType))
	if !ok || newer.MessageSeq != 11 || newer.ActiveAtMS != 3_000 {
		t.Fatalf("newer delete-fenced activity = %+v present=%t, want seq=11 active_at=3000", newer, ok)
	}
	if got := manager.DirtyCountForTest(); got != 2 {
		t.Fatalf("dirty rows = %d, want newer activity and persist-failed live row", got)
	}
	observation := observer.lastFlush(t)
	if observation.DeleteFenced != 1 || observation.Requeued != 2 {
		t.Fatalf("flush observation = %+v, want delete_fenced=1 requeued=2", observation)
	}
	requireCacheIndexConservation(t, manager)
}

func TestManagerObservesCacheRowsAndDirtyLag(t *testing.T) {
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		NowMS:    func() int64 { return 2500 },
		Observer: observer,
	})

	if err := m.MarkActive(context.Background(), []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAtMS: 1000},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "new", ChannelType: 2, ActiveAtMS: 2000},
	}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	got := observer.lastCache(t)
	if got.Rows != 2 || got.DirtyRows != 2 {
		t.Fatalf("cache observation rows=%d dirty=%d, want 2/2", got.Rows, got.DirtyRows)
	}
	if got.OldestDirtyAge != 1500*time.Millisecond {
		t.Fatalf("oldest dirty age = %s, want 1.5s", got.OldestDirtyAge)
	}
}

func TestManagerObservesCacheRowsByKind(t *testing.T) {
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{Observer: observer})

	if err := m.MarkActive(context.Background(), []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "normal-a", ChannelType: 2, ActiveAtMS: 1000},
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "normal-b", ChannelType: 2, ActiveAtMS: 1001},
		{Kind: metadb.ConversationKindCMD, UID: "u1", ChannelID: "cmd-a", ChannelType: 2, ActiveAtMS: 1002},
	}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	got := observer.lastCache(t)
	if got.RowsByKind[metadb.ConversationKindNormal] != 2 || got.RowsByKind[metadb.ConversationKindCMD] != 1 {
		t.Fatalf("RowsByKind = %+v, want normal=2 cmd=1", got.RowsByKind)
	}
	if got.DirtyRowsByKind[metadb.ConversationKindNormal] != 2 || got.DirtyRowsByKind[metadb.ConversationKindCMD] != 1 {
		t.Fatalf("DirtyRowsByKind = %+v, want normal=2 cmd=1", got.DirtyRowsByKind)
	}
}

func TestFlushZeroLimitFlushesAllDirtyRows(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000},
		{Kind: metadb.ConversationKindNormal, UID: "u2", ChannelID: "room-2", ChannelType: 1, ActiveAtMS: 2000, ReadSeq: 5},
	}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if got := m.DirtyCountForTest(); got != 2 {
		t.Fatalf("DirtyCountForTest() = %d, want 2", got)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Selected != 2 || result.Persisted != 2 {
		t.Fatalf("Flush() result = %+v, want selected=2 persisted=2", result)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 2 {
		t.Fatalf("touches = %+v, want one batch with two patches", store.touches)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want 0", got)
	}
}

func TestFlushHashSlotFlushesOnlyTargetDirtyRows(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})
	if err := m.MarkActiveForHashSlot(ctx, 1, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u-slot-1", ChannelID: "slot-1", ChannelType: 2, ActiveAtMS: 1000},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(slot 1) error = %v", err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 9, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u-slot-9", ChannelID: "slot-9", ChannelType: 2, ActiveAtMS: 2000},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(slot 9) error = %v", err)
	}

	result, err := m.FlushHashSlot(ctx, 1, 0)
	if err != nil {
		t.Fatalf("FlushHashSlot(slot 1) error = %v", err)
	}
	if result.Selected != 1 || result.Persisted != 1 {
		t.Fatalf("FlushHashSlot(slot 1) result = %+v, want selected=1 persisted=1", result)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 || store.touches[0][0].ChannelID != "slot-1" {
		t.Fatalf("touches = %+v, want only slot-1 flushed", store.touches)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want slot-9 dirty row left", got)
	}

	result, err = m.FlushHashSlot(ctx, 9, 0)
	if err != nil {
		t.Fatalf("FlushHashSlot(slot 9) error = %v", err)
	}
	if result.Selected != 1 || result.Persisted != 1 {
		t.Fatalf("FlushHashSlot(slot 9) result = %+v, want selected=1 persisted=1", result)
	}
	if len(store.touches) != 2 || len(store.touches[1]) != 1 || store.touches[1][0].ChannelID != "slot-9" {
		t.Fatalf("touches = %+v, want slot-9 flushed second", store.touches)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want all dirty rows flushed", got)
	}
}

func TestFlushHashSlotFailureDoesNotSelectOtherSlots(t *testing.T) {
	ctx := context.Background()
	touchErr := errors.New("touch failed")
	store := &recordingActiveStore{touchErr: touchErr}
	m := NewManager(Options{Store: store})
	if err := m.MarkActiveForHashSlot(ctx, 1, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u-slot-1", ChannelID: "slot-1", ChannelType: 2, ActiveAtMS: 1000},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(slot 1) error = %v", err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 9, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u-slot-9", ChannelID: "slot-9", ChannelType: 2, ActiveAtMS: 2000},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(slot 9) error = %v", err)
	}

	result, err := m.FlushHashSlot(ctx, 1, 0)
	if !errors.Is(err, touchErr) {
		t.Fatalf("FlushHashSlot(slot 1) error = %v, want %v", err, touchErr)
	}
	if result.Selected != 1 || result.Persisted != 0 {
		t.Fatalf("FlushHashSlot(slot 1) result = %+v, want selected=1 persisted=0", result)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 || store.touches[0][0].ChannelID != "slot-1" {
		t.Fatalf("touches = %+v, want failed attempt to include only slot-1", store.touches)
	}
	if got := m.DirtyCountForTest(); got != 2 {
		t.Fatalf("DirtyCountForTest() = %d, want both rows still dirty after failure", got)
	}
}

func TestManagerObservesFlushResults(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	store := &recordingActiveStore{}
	m := NewManager(Options{
		Store:    store,
		NowMS:    func() int64 { return 3000 },
		Observer: observer,
	})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Selected != 1 || result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("Flush() result = %+v, want selected=1 persisted=1 cleared=1", result)
	}

	flush := observer.lastFlush(t)
	if flush.Result != "ok" || flush.Selected != 1 || flush.Persisted != 1 || flush.Cleared != 1 || flush.Requeued != 0 {
		t.Fatalf("flush observation = %+v, want ok selected=1 persisted=1 cleared=1 requeued=0", flush)
	}
	if flush.Duration <= 0 {
		t.Fatalf("flush duration = %s, want positive", flush.Duration)
	}
	mutation := observer.lastMutation(t)
	if mutation.BecameDirty != 1 || mutation.DirtyUpdated != 0 || mutation.Unchanged != 0 {
		t.Fatalf("mutation observation = %+v, want one became_dirty row", mutation)
	}
	cache := observer.lastCache(t)
	if cache.Rows != 1 || cache.DirtyRows != 0 || cache.OldestDirtyAge != 0 {
		t.Fatalf("post-flush cache observation = %+v, want rows=1 dirty=0 age=0", cache)
	}
}

func TestManagerObservesFlushFailureAndNoDirty(t *testing.T) {
	ctx := context.Background()
	touchErr := errors.New("touch failed")
	observer := &recordingConversationActiveObserver{}
	store := &recordingActiveStore{touchErr: touchErr}
	m := NewManager(Options{
		Store:    store,
		NowMS:    func() int64 { return 3000 },
		Observer: observer,
	})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	if _, err := m.Flush(ctx, 0); !errors.Is(err, touchErr) {
		t.Fatalf("Flush(error) = %v, want %v", err, touchErr)
	}
	failed := observer.lastFlush(t)
	if failed.Result != "error" || failed.FailureStage != "persist" || failed.Selected != 1 || failed.Persisted != 0 || failed.Requeued != 1 {
		t.Fatalf("failed flush observation = %+v, want persist error selected=1 persisted=0 requeued=1", failed)
	}

	store.touchErr = nil
	if _, err := m.Flush(ctx, 0); err != nil {
		t.Fatalf("Flush(cleanup) error = %v", err)
	}
	if _, err := m.Flush(ctx, 0); err != nil {
		t.Fatalf("Flush(no dirty) error = %v", err)
	}
	noDirty := observer.lastFlush(t)
	if noDirty.Result != "no_dirty" || noDirty.Selected != 0 || noDirty.Persisted != 0 {
		t.Fatalf("no-dirty flush observation = %+v, want no_dirty selected=0 persisted=0", noDirty)
	}
}

func TestManagerObservesFlushDeadlineAsTimeout(t *testing.T) {
	ctx := context.Background()
	observer := &recordingConversationActiveObserver{}
	store := &recordingActiveStore{touchErr: context.DeadlineExceeded}
	m := NewManager(Options{Store: store, Observer: observer})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u1",
		ChannelID:   "room-1",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	if _, err := m.Flush(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Flush() error = %v, want deadline exceeded", err)
	}
	if flush := observer.lastFlush(t); flush.Result != "timeout" || flush.Selected != 1 {
		t.Fatalf("flush observation = %+v, want timeout selected=1", flush)
	}
}

func TestFlushFailureKeepsDirty(t *testing.T) {
	ctx := context.Background()
	touchErr := errors.New("touch failed")
	store := &recordingActiveStore{touchErr: touchErr}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	result, err := m.Flush(ctx, 0)
	if !errors.Is(err, touchErr) {
		t.Fatalf("Flush() error = %v, want %v", err, touchErr)
	}
	if result.Persisted != 0 || result.Requeued != 1 {
		t.Fatalf("Flush() result = %+v, want persisted=0 requeued=1 on failure", result)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want 1", got)
	}
}

func TestFlushDoesNotClearConcurrentDirtyUpdate(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 7}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	store.touchHook = func() {
		if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 2000, ReadSeq: 9}}); err != nil {
			t.Fatalf("MarkActive(concurrent) error = %v", err)
		}
	}

	result, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Persisted != 1 {
		t.Fatalf("Flush() persisted = %d, want 1", result.Persisted)
	}
	if result.Cleared != 0 || result.VersionConflicts != 1 || result.Requeued != 1 {
		t.Fatalf("Flush() result = %+v, want persisted=1 cleared=0 version_conflicts=1 requeued=1", result)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want 1 after concurrent update", got)
	}
	entry, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "room-1", 2)
	if !ok {
		t.Fatalf("entry was removed")
	}
	if entry.ActiveAtMS != 2000 || entry.ReadSeq != 9 {
		t.Fatalf("entry = %+v, want newer active/read values", entry)
	}
	if got := store.touches[0][0].ActiveAt; got != 1000 {
		t.Fatalf("flushed ActiveAt = %d, want original snapshot 1000", got)
	}
	requireCacheIndexConservation(t, m)
}

func TestClearFlushedDirtyClassifiesAlreadyCleanSnapshotAsSuperseded(t *testing.T) {
	m := NewManager(Options{Store: &recordingActiveStore{}})
	if err := m.MarkActive(context.Background(), []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1000,
	}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	entries := m.dirtyFlushEntries(1)
	if len(entries) != 1 {
		t.Fatalf("dirty entries = %d, want 1", len(entries))
	}
	if got := m.clearFlushedDirty(entries); got.cleared != 1 || got.versionConflicts != 0 || got.staleSnapshots != 0 {
		t.Fatalf("first clear = %+v, want one cleared row", got)
	}
	if got := m.clearFlushedDirty(entries); got.cleared != 0 || got.versionConflicts != 0 || got.staleSnapshots != 1 {
		t.Fatalf("second clear = %+v, want one superseded snapshot", got)
	}
}

func TestFlushReportsPersistedRowsSeparateFromConcurrentVersionConflicts(t *testing.T) {
	const (
		cacheRows = 64
		batchRows = 16
		attempts  = 4
	)
	ctx := context.Background()
	store := &concurrentVersionUpdateStore{
		persisted: make(chan []metadb.ConversationActivePatch),
		updated:   make(chan error),
	}
	m := NewManager(Options{Store: store, MaxCachedRows: cacheRows})
	initial := make([]ActivePatch, 0, cacheRows)
	for i := 0; i < cacheRows; i++ {
		initial = append(initial, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("u-%02d", i),
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  1_000,
		})
	}
	if err := m.MarkActive(ctx, initial); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}

	updaterDone := make(chan struct{})
	go func() {
		defer close(updaterDone)
		for attempt := 0; attempt < attempts; attempt++ {
			persisted := <-store.persisted
			updates := make([]ActivePatch, 0, len(persisted))
			for _, patch := range persisted {
				updates = append(updates, ActivePatch{
					Kind:        patch.Kind,
					UID:         patch.UID,
					ChannelID:   patch.ChannelID,
					ChannelType: uint8(patch.ChannelType),
					ActiveAtMS:  patch.ActiveAt + int64(attempt) + 1,
				})
			}
			store.updated <- m.MarkActive(ctx, updates)
		}
	}()

	var durablyWritten, actuallyCleared, versionConflicts, requeued int
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := m.Flush(ctx, batchRows)
		if err != nil {
			t.Fatalf("Flush(attempt=%d) error = %v", attempt, err)
		}
		durablyWritten += result.Persisted
		actuallyCleared += result.Cleared
		versionConflicts += result.VersionConflicts
		requeued += result.Requeued
	}
	<-updaterDone

	if durablyWritten != cacheRows || actuallyCleared != 0 || versionConflicts != cacheRows || requeued != cacheRows {
		t.Fatalf("flush accounting persisted=%d cleared=%d conflicts=%d requeued=%d, want %d/0/%d/%d", durablyWritten, actuallyCleared, versionConflicts, requeued, cacheRows, cacheRows, cacheRows)
	}
	if remainingDirty := m.DirtyCountForTest(); remainingDirty != cacheRows {
		t.Fatalf("dirty rows = %d, want all %d concurrent versions retained", remainingDirty, cacheRows)
	}
}

func TestCachePressureAdmissionDoesNotPerformDurableIO(t *testing.T) {
	ctx := context.Background()
	storeErr := errors.New("durable store must not be called by admission")
	store := &recordingActiveStore{touchErr: storeErr}
	m := NewManager(Options{Store: store, ActiveCooldown: time.Hour, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u1",
		ChannelID:   "old",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}); err != nil {
		t.Fatalf("MarkActive(old) error = %v", err)
	}

	err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u2",
		ChannelID:   "new",
		ChannelType: 2,
		ActiveAtMS:  2000,
	}})
	if !errors.Is(err, ErrCachePressure) {
		t.Fatalf("MarkActive(pressure) error = %v, want %v", err, ErrCachePressure)
	}
	if len(store.touches) != 0 {
		t.Fatalf("admission durable writes = %d, want 0", len(store.touches))
	}
	if len(store.batchKeys) != 0 {
		t.Fatalf("admission durable reads = %d, want 0", len(store.batchKeys))
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "old", 2); !ok {
		t.Fatal("existing dirty row was removed after rejected pressure admission")
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u2", "new", 2); ok {
		t.Fatal("rejected pressure row was partially cached")
	}
}

func TestCachePressureAdmissionSignalsAsyncFlush(t *testing.T) {
	ctx := context.Background()
	pressureSignals := make(chan PressureSignal, 1)
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 1, PressureNotify: pressureSignals})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u1",
		ChannelID:   "old",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}); err != nil {
		t.Fatalf("MarkActive(old) error = %v", err)
	}

	err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u2",
		ChannelID:   "new",
		ChannelType: 2,
		ActiveAtMS:  2000,
	}})
	if !errors.Is(err, ErrCachePressure) {
		t.Fatalf("MarkActive(pressure) error = %v, want %v", err, ErrCachePressure)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("cache pressure admission did not signal the async flush worker")
	}
}

func TestCachePressureAdmissionDoesNotWaitForInFlightFlush(t *testing.T) {
	ctx := context.Background()
	store := &blockingActiveStore{
		entered: make(chan int, 1),
		release: make(chan struct{}),
	}
	releaseStore := sync.OnceFunc(func() { close(store.release) })
	defer releaseStore()
	m := NewManager(Options{Store: store, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u1",
		ChannelID:   "old",
		ChannelType: 2,
		ActiveAtMS:  1000,
	}}); err != nil {
		t.Fatalf("MarkActive(old) error = %v", err)
	}

	flushDone := make(chan error, 1)
	go func() {
		_, err := m.Flush(ctx, 1)
		flushDone <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("flush did not reach the blocking durable store")
	}

	admissionDone := make(chan error, 1)
	go func() {
		admissionDone <- m.MarkActive(ctx, []ActivePatch{{
			Kind:        metadb.ConversationKindNormal,
			UID:         "u2",
			ChannelID:   "new",
			ChannelType: 2,
			ActiveAtMS:  2000,
		}})
	}()
	select {
	case err := <-admissionDone:
		if !errors.Is(err, ErrCachePressure) {
			t.Fatalf("MarkActive(pressure) error = %v, want %v", err, ErrCachePressure)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cache-pressure admission waited for the in-flight durable flush")
	}

	releaseStore()
	if err := <-flushDone; err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
}

func TestCachePressureAdmissionRecoversAfterAsyncFlush(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive(old) error = %v", err)
	}

	batch := ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "u1",
		ChannelID:   "new",
		ChannelType: 2,
		MessageSeq:  10,
		ActiveAtMS:  2000,
	}
	if err := m.AdmitActiveBatch(ctx, batch); !errors.Is(err, ErrCachePressure) {
		t.Fatalf("AdmitActiveBatch(before flush) error = %v, want %v", err, ErrCachePressure)
	}
	if len(store.touches) != 0 {
		t.Fatalf("admission durable writes = %d, want 0", len(store.touches))
	}
	if result, err := m.Flush(ctx, 1); err != nil || result.Persisted != 1 {
		t.Fatalf("Flush() = %+v, %v, want one persisted row", result, err)
	}
	if err := m.AdmitActiveBatch(ctx, batch); err != nil {
		t.Fatalf("AdmitActiveBatch(after flush) error = %v", err)
	}

	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "old", 2); ok {
		t.Fatalf("old flushed row is still cached under pressure")
	}
	newEntry, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "new", 2)
	if !ok {
		t.Fatalf("new row was not cached")
	}
	if newEntry.ActiveAtMS != 2000 || newEntry.ReadSeq != 10 {
		t.Fatalf("new entry = %+v, want active 2000 read 10", newEntry)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want only new dirty row", got)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 {
		t.Fatalf("touches = %+v, want old row flushed once", store.touches)
	}
	if got := store.touches[0][0]; got.ChannelID != "old" || got.ActiveAt != 1000 {
		t.Fatalf("flushed patch = %+v, want old active row", got)
	}
}

func TestAdmitUnderCachePressureEvictsCleanRowsWithoutFlush(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{Store: store, MaxCachedRows: 2, Observer: observer})
	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "old-1", ChannelType: 2, ActiveAtMS: 1000},
		{Kind: metadb.ConversationKindNormal, UID: "u2", ChannelID: "old-2", ChannelType: 2, ActiveAtMS: 1000},
	}); err != nil {
		t.Fatalf("MarkActive(old) error = %v", err)
	}
	if _, err := m.Flush(ctx, 0); err != nil {
		t.Fatalf("Flush(old) error = %v", err)
	}
	requireCacheIndexConservation(t, m)
	flushObservations := len(observer.flush)
	store.touches = nil

	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u3",
		ChannelID:   "new",
		ChannelType: 2,
		ActiveAtMS:  2000,
	}}); err != nil {
		t.Fatalf("MarkActive(new) error = %v", err)
	}

	if len(observer.flush) != flushObservations {
		t.Fatalf("flush observations = %d, want unchanged %d when clean rows can be evicted", len(observer.flush), flushObservations)
	}
	if len(store.touches) != 0 {
		t.Fatalf("touches = %+v, want no durable flush for clean-row eviction", store.touches)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u3", "new", 2); !ok {
		t.Fatal("new row was not cached")
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want only new dirty row", got)
	}
	cache := observer.lastCache(t)
	if cache.Rows != 2 || cache.DirtyRows != 1 {
		t.Fatalf("cache observation = %+v, want rows=2 dirty=1", cache)
	}
	requireCacheIndexConservation(t, m)
}

func TestSparseCleanIndexProtectsBatchRowAndEvictsOnlyAvailableVictim(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 4})
	if err := m.MarkActiveForHashSlot(ctx, 1, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "clean-victim", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(clean victim) error = %v", err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 2, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "dirty-1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000},
		{Kind: metadb.ConversationKindNormal, UID: "dirty-2", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000},
	}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(dirty rows) error = %v", err)
	}
	if err := m.MarkActiveForHashSlot(ctx, 3, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "protected-clean", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_500,
	}}); err != nil {
		t.Fatalf("MarkActiveForHashSlot(protected clean) error = %v", err)
	}
	if result, err := m.FlushHashSlot(ctx, 1, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(clean victim) = %+v, %v", result, err)
	}
	if result, err := m.FlushHashSlot(ctx, 3, 1); err != nil || result.Cleared != 1 {
		t.Fatalf("FlushHashSlot(protected clean) = %+v, %v", result, err)
	}
	requireCacheIndexConservation(t, m)

	patches := make([]ActivePatch, 0, 9)
	for index := 0; index < cap(patches); index++ {
		uid := "incoming"
		activeAtMS := int64(2_000 + index)
		if index%2 == 0 {
			uid = "protected-clean"
			activeAtMS = 1_500
		}
		patches = append(patches, ActivePatch{
			Kind: metadb.ConversationKindNormal, UID: uid, ChannelID: "room", ChannelType: 2, ActiveAtMS: activeAtMS,
		})
	}
	if err := m.MarkActiveForHashSlot(ctx, 3, patches); err != nil {
		t.Fatalf("MarkActiveForHashSlot(incoming) error = %v", err)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "clean-victim", "room", 2); ok {
		t.Fatal("the only clean victim remained cached after successful admission")
	}
	for _, uid := range []string{"protected-clean", "dirty-1", "dirty-2", "incoming"} {
		if _, ok := m.EntryForTest(metadb.ConversationKindNormal, uid, "room", 2); !ok {
			t.Fatalf("protected or dirty row %q was lost during clean eviction", uid)
		}
	}
	if got := m.DirtyCountForTest(); got != 3 {
		t.Fatalf("dirty rows = %d, want two retained rows plus incoming", got)
	}
	requireCacheIndexConservation(t, m)
}

func TestCachePressureDoesNotEvictCleanRowUpdatedBySameBatch(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 2})
	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "protected", ChannelType: 2, ActiveAtMS: 3000, ReadSeq: 30},
		{Kind: metadb.ConversationKindNormal, UID: "u2", ChannelID: "dirty", ChannelType: 2, ActiveAtMS: 1000},
	}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	if _, err := m.Flush(ctx, 0); err != nil {
		t.Fatalf("Flush(initial) error = %v", err)
	}
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind:        metadb.ConversationKindNormal,
		UID:         "u2",
		ChannelID:   "dirty",
		ChannelType: 2,
		ActiveAtMS:  2000,
	}}); err != nil {
		t.Fatalf("MarkActive(dirty) error = %v", err)
	}
	requireCacheIndexConservation(t, m)

	err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "u1", ChannelID: "protected", ChannelType: 2, ActiveAtMS: 2000, ReadSeq: 20},
		{Kind: metadb.ConversationKindNormal, UID: "u3", ChannelID: "new", ChannelType: 2, ActiveAtMS: 4000},
	})
	if !errors.Is(err, ErrCachePressure) {
		t.Fatalf("MarkActive(protected batch) error = %v, want %v", err, ErrCachePressure)
	}
	protected, ok := m.EntryForTest(metadb.ConversationKindNormal, "u1", "protected", 2)
	if !ok || protected.ActiveAtMS != 3000 || protected.ReadSeq != 30 {
		t.Fatalf("protected entry = %+v, %t, want retained active=3000 read=30", protected, ok)
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u2", "dirty", 2); !ok {
		t.Fatal("existing dirty row was removed after rejected protected batch")
	}
	if _, ok := m.EntryForTest(metadb.ConversationKindNormal, "u3", "new", 2); ok {
		t.Fatal("rejected new row was partially cached")
	}
	cache := m.cacheObservation()
	if cache.Revision == 0 || cache.Rows != 2 || cache.DirtyRows != 1 ||
		cache.RowsByKind[metadb.ConversationKindNormal] != 2 ||
		cache.DirtyRowsByKind[metadb.ConversationKindNormal] != 1 {
		t.Fatalf("cache observation = %+v, want rows=2 dirty=1 with matching normal-kind counts", cache)
	}
	requireCacheIndexConservation(t, m)
}

func TestCachePressureRejectsAtomicallyWhenOnlyPartOfVictimSetIsAvailable(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: 3})
	if err := m.MarkActive(ctx, []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "victim", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_000},
		{Kind: metadb.ConversationKindNormal, UID: "protected", ChannelID: "room", ChannelType: 2, ActiveAtMS: 2_000},
		{Kind: metadb.ConversationKindNormal, UID: "dirty", ChannelID: "room", ChannelType: 2, ActiveAtMS: 3_000},
	}); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	if result, err := m.Flush(ctx, 0); err != nil || result.Cleared != 3 {
		t.Fatalf("Flush(initial) = %+v, %v", result, err)
	}
	if err := m.MarkActive(ctx, []ActivePatch{{
		Kind: metadb.ConversationKindNormal, UID: "dirty", ChannelID: "room", ChannelType: 2, ActiveAtMS: 4_000,
	}}); err != nil {
		t.Fatalf("MarkActive(dirty) error = %v", err)
	}
	requireCacheIndexConservation(t, m)

	patches := []ActivePatch{
		{Kind: metadb.ConversationKindNormal, UID: "protected", ChannelID: "room", ChannelType: 2, ActiveAtMS: 2_000},
		{Kind: metadb.ConversationKindNormal, UID: "new-1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 5_000},
		{Kind: metadb.ConversationKindNormal, UID: "new-2", ChannelID: "room", ChannelType: 2, ActiveAtMS: 6_000},
		{Kind: metadb.ConversationKindNormal, UID: "protected", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_900},
		{Kind: metadb.ConversationKindNormal, UID: "new-1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 4_900},
		{Kind: metadb.ConversationKindNormal, UID: "new-2", ChannelID: "room", ChannelType: 2, ActiveAtMS: 5_900},
		{Kind: metadb.ConversationKindNormal, UID: "protected", ChannelID: "room", ChannelType: 2, ActiveAtMS: 1_800},
		{Kind: metadb.ConversationKindNormal, UID: "new-1", ChannelID: "room", ChannelType: 2, ActiveAtMS: 4_800},
		{Kind: metadb.ConversationKindNormal, UID: "new-2", ChannelID: "room", ChannelType: 2, ActiveAtMS: 5_800},
	}
	if err := m.MarkActive(ctx, patches); !errors.Is(err, ErrCachePressure) {
		t.Fatalf("MarkActive(partial victims) error = %v, want %v", err, ErrCachePressure)
	}
	for _, uid := range []string{"victim", "protected", "dirty"} {
		if _, ok := m.EntryForTest(metadb.ConversationKindNormal, uid, "room", 2); !ok {
			t.Fatalf("existing row %q was partially evicted by rejected admission", uid)
		}
	}
	for _, uid := range []string{"new-1", "new-2"} {
		if _, ok := m.EntryForTest(metadb.ConversationKindNormal, uid, "room", 2); ok {
			t.Fatalf("rejected new row %q was partially admitted", uid)
		}
	}
	protected, _ := m.EntryForTest(metadb.ConversationKindNormal, "protected", "room", 2)
	if protected.ActiveAtMS != 2_000 {
		t.Fatalf("protected row active_at=%d, want unchanged 2000", protected.ActiveAtMS)
	}
	requireCacheIndexConservation(t, m)
}

func TestCachePressureFlushContinuesUntilDirtyLowWatermark(t *testing.T) {
	const (
		maxRows = 10
	)
	ctx := context.Background()
	store := &recordingActiveStore{}
	pressureSignals := make(chan PressureSignal, 1)
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		Store:          store,
		MaxCachedRows:  maxRows,
		PressureNotify: pressureSignals,
		Observer:       observer,
	})
	initial := make([]ActivePatch, 0, maxRows)
	for i := 0; i < maxRows; i++ {
		initial = append(initial, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("existing-%d", i),
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  1000,
		})
	}
	if err := m.MarkActive(ctx, initial); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("high watermark did not start an asynchronous pressure flush cycle")
	}

	if result, err := m.Flush(ctx, 1); err != nil || result.Persisted != 1 {
		t.Fatalf("Flush(first) = %+v, %v, want one bounded row", result, err)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("dirty rows above the low watermark did not continue the pressure flush cycle")
	}
	if result, err := m.Flush(ctx, 2); err != nil || result.Persisted != 2 {
		t.Fatalf("Flush(to low watermark) = %+v, %v, want two bounded rows", result, err)
	}
	select {
	case <-pressureSignals:
		t.Fatal("pressure flush cycle continued after dirty rows reached the 70 percent low watermark")
	default:
	}
	for _, event := range []string{"start_high_watermark", "signal_sent", "requeue_progress", "stop_low_watermark"} {
		if observer.pressureEventCount(event) == 0 {
			t.Fatalf("pressure events = %+v, want %q", observer.pressure, event)
		}
	}
}

func TestConcurrentCachePressureCoalescesAsyncFlushSignal(t *testing.T) {
	const (
		maxRows = 64
		workers = 8
	)
	ctx := context.Background()
	store := &recordingActiveStore{}
	pressureSignals := make(chan PressureSignal, 1)
	m := NewManager(Options{Store: store, MaxCachedRows: maxRows, PressureNotify: pressureSignals})
	initial := make([]ActivePatch, 0, maxRows)
	for i := 0; i < maxRows; i++ {
		initial = append(initial, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("existing-%d", i),
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  1000,
		})
	}
	if err := m.MarkActive(ctx, initial); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}
	select {
	case <-pressureSignals:
	default:
		t.Fatal("initial high watermark did not signal pressure")
	}

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(worker int) {
			ready.Done()
			<-start
			errs <- m.MarkActive(ctx, []ActivePatch{{
				Kind:        metadb.ConversationKindNormal,
				UID:         fmt.Sprintf("new-%d", worker),
				ChannelID:   "room",
				ChannelType: 2,
				ActiveAtMS:  2000,
			}})
		}(i)
	}
	ready.Wait()
	close(start)

	for i := 0; i < workers; i++ {
		if err := <-errs; !errors.Is(err, ErrCachePressure) {
			t.Fatalf("MarkActive(concurrent pressure) error = %v, want %v", err, ErrCachePressure)
		}
	}
	if len(store.touches) != 0 || len(store.batchKeys) != 0 {
		t.Fatalf("concurrent admission performed durable IO: writes=%d reads=%d", len(store.touches), len(store.batchKeys))
	}
	select {
	case <-pressureSignals:
		t.Fatal("rejected admissions duplicated the in-progress pressure-cycle signal")
	default:
	}
	if got := m.DirtyCountForTest(); got != maxRows {
		t.Fatalf("dirty rows = %d, want unchanged %d after rejected admissions", got, maxRows)
	}
}

func TestFullDirtyCachePressureAdmissionRemainsBounded(t *testing.T) {
	const (
		maxRows = 100_000
		workers = 512
	)
	ctx := context.Background()
	m := NewManager(Options{Store: &recordingActiveStore{}, MaxCachedRows: maxRows})
	initial := make([]ActivePatch, 0, maxRows)
	for i := 0; i < maxRows; i++ {
		initial = append(initial, ActivePatch{
			Kind:        metadb.ConversationKindNormal,
			UID:         fmt.Sprintf("existing-%d", i),
			ChannelID:   "room",
			ChannelType: 2,
			ActiveAtMS:  1000,
		})
	}
	if err := m.MarkActive(ctx, initial); err != nil {
		t.Fatalf("MarkActive(initial) error = %v", err)
	}

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(worker int) {
			ready.Done()
			<-start
			errs <- m.MarkActive(ctx, []ActivePatch{{
				Kind:        metadb.ConversationKindNormal,
				UID:         fmt.Sprintf("new-%d", worker),
				ChannelID:   "room",
				ChannelType: 2,
				ActiveAtMS:  2000,
			}})
		}(i)
	}
	ready.Wait()
	startedAt := time.Now()
	close(start)
	for i := 0; i < workers; i++ {
		if err := <-errs; !errors.Is(err, ErrCachePressure) {
			t.Fatalf("MarkActive(full dirty cache) error = %v, want %v", err, ErrCachePressure)
		}
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("full dirty cache pressure admissions took %s, want <= 1s", elapsed)
	}
}

type recordingActiveStore struct {
	rows            []metadb.ConversationState
	primary         map[metadb.ConversationStateKey]metadb.ConversationState
	calls           int
	lastKind        metadb.ConversationKind
	lastAfter       metadb.ConversationActiveCursor
	lastLimit       int
	lookupErr       error
	lookups         []metadb.ConversationStateKey
	batchKeys       []metadb.ConversationStateKey
	batchLookupHook func()
	touchErr        error
	touchHook       func()
	touches         [][]metadb.ConversationActivePatch
}

type blockingActiveStore struct {
	recordingActiveStore
	mu            sync.Mutex
	entered       chan int
	release       chan struct{}
	concurrent    int
	maxConcurrent int
}

type concurrentVersionUpdateStore struct {
	recordingActiveStore
	persisted chan []metadb.ConversationActivePatch
	updated   chan error
}

func (s *concurrentVersionUpdateStore) TouchConversationActiveAt(_ context.Context, patches []metadb.ConversationActivePatch) error {
	persisted := append([]metadb.ConversationActivePatch(nil), patches...)
	s.persisted <- persisted
	return <-s.updated
}

func (s *blockingActiveStore) TouchConversationActiveAt(_ context.Context, patches []metadb.ConversationActivePatch) error {
	s.mu.Lock()
	s.concurrent++
	if s.concurrent > s.maxConcurrent {
		s.maxConcurrent = s.concurrent
	}
	s.mu.Unlock()

	s.entered <- len(patches)
	<-s.release

	s.mu.Lock()
	s.concurrent--
	s.mu.Unlock()
	return nil
}

func (s *blockingActiveStore) maxConcurrentCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxConcurrent
}

type recordingConversationActiveObserver struct {
	cache    []CacheObservation
	mutation []MutationObservation
	flush    []FlushObservation
	pressure []PressureObservation
}

func (o *recordingConversationActiveObserver) ObserveConversationActiveCache(event CacheObservation) {
	o.cache = append(o.cache, event)
}

func (o *recordingConversationActiveObserver) ObserveConversationActiveMutation(event MutationObservation) {
	o.mutation = append(o.mutation, event)
}

func (o *recordingConversationActiveObserver) ObserveConversationActiveFlush(event FlushObservation) {
	o.flush = append(o.flush, event)
}

func (o *recordingConversationActiveObserver) ObserveConversationActivePressure(event PressureObservation) {
	o.pressure = append(o.pressure, event)
}

func (o *recordingConversationActiveObserver) lastCache(t *testing.T) CacheObservation {
	t.Helper()
	if len(o.cache) == 0 {
		t.Fatalf("no cache observations")
	}
	return o.cache[len(o.cache)-1]
}

func (o *recordingConversationActiveObserver) lastFlush(t *testing.T) FlushObservation {
	t.Helper()
	if len(o.flush) == 0 {
		t.Fatalf("no flush observations")
	}
	return o.flush[len(o.flush)-1]
}

func (o *recordingConversationActiveObserver) lastMutation(t *testing.T) MutationObservation {
	t.Helper()
	if len(o.mutation) == 0 {
		t.Fatalf("no mutation observations")
	}
	return o.mutation[len(o.mutation)-1]
}

func (o *recordingConversationActiveObserver) pressureEventCount(event string) int {
	var count int
	for _, observed := range o.pressure {
		if observed.Event == event {
			count++
		}
	}
	return count
}

func (s *recordingActiveStore) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	s.calls++
	s.lastKind = kind
	s.lastAfter = after
	s.lastLimit = limit

	rows := append([]metadb.ConversationState(nil), s.rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ActiveAt != rows[j].ActiveAt {
			return rows[i].ActiveAt > rows[j].ActiveAt
		}
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].ChannelType < rows[j].ChannelType
	})

	candidates := make([]metadb.ConversationState, 0, len(rows))
	for _, row := range rows {
		if row.Kind != kind || row.UID != uid || !testActiveRowAfter(row, after) {
			continue
		}
		candidates = append(candidates, row)
	}
	done := len(candidates) <= limit
	if limit <= 0 {
		candidates = nil
		done = true
	} else if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	cursor := after
	if len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		cursor = metadb.ConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return candidates, cursor, done, nil
}

func (s *recordingActiveStore) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid string, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	key := metadb.ConversationStateKey{UID: uid, Kind: kind, ChannelID: channelID, ChannelType: channelType}
	s.lookups = append(s.lookups, key)
	if s.lookupErr != nil {
		return metadb.ConversationState{}, false, s.lookupErr
	}
	row, ok := s.primary[key]
	return row, ok, nil
}

func (s *recordingActiveStore) GetConversationStates(_ context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	s.batchKeys = append(s.batchKeys, keys...)
	if s.batchLookupHook != nil {
		s.batchLookupHook()
	}
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	for _, key := range keys {
		row, ok := s.primary[key]
		if !ok {
			continue
		}
		states[key] = row
	}
	return states, nil
}

func (s *recordingActiveStore) TouchConversationActiveAt(_ context.Context, patches []metadb.ConversationActivePatch) error {
	batch := append([]metadb.ConversationActivePatch(nil), patches...)
	s.touches = append(s.touches, batch)
	if s.touchHook != nil {
		s.touchHook()
	}
	if s.touchErr != nil {
		return s.touchErr
	}
	return nil
}

func testActiveRowAfter(row metadb.ConversationState, after metadb.ConversationActiveCursor) bool {
	if after == (metadb.ConversationActiveCursor{}) {
		return true
	}
	if row.ActiveAt != after.ActiveAt {
		return row.ActiveAt < after.ActiveAt
	}
	if row.ChannelID != after.ChannelID {
		return row.ChannelID > after.ChannelID
	}
	return row.ChannelType > after.ChannelType
}

func activeChannelIDs(rows []metadb.ConversationState) string {
	var out string
	for _, row := range rows {
		if out != "" {
			out += ","
		}
		out += row.ChannelID
	}
	return out
}
