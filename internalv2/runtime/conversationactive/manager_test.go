package conversationactive

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestAdmitActiveBatchUpdatesCacheAndSenderReadSeq(t *testing.T) {
	const activeAtMS int64 = 1781094600000
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(context.Background(), ActiveBatch{
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

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != activeAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, activeAtMS)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != activeAtMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, activeAtMS)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchCachesSenderWhenNotRecipient(t *testing.T) {
	const activeAtMS int64 = 1781094600000
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(context.Background(), ActiveBatch{
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

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != activeAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, activeAtMS)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
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

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != secondActiveAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, secondActiveAtMS)
	}
	if sender.ReadSeq != 11 {
		t.Fatalf("sender ReadSeq = %d, want 11", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
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

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if sender.ActiveAtMS != nowMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, nowMS)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
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
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  1781094600000,
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	page, err := m.ListActiveView(ctx, "alice", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}

	wantRows := []metadb.UserConversationState{{
		UID:         "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		ReadSeq:     42,
		ActiveAt:    1781094600000,
	}}
	wantCursor := metadb.UserConversationActiveCursor{ActiveAt: 1781094600000, ChannelID: "room-1", ChannelType: 2}
	if !reflect.DeepEqual(page.Rows, wantRows) || page.Cursor != wantCursor || !page.Done {
		t.Fatalf("page = %+v, want rows=%+v cursor=%+v done=true", page, wantRows, wantCursor)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d, want one DB page request", store.calls)
	}
}

func TestListActiveViewMergesCacheOverDB(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "shared", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 7, ActiveAt: 100, UpdatedAt: 101},
			{UID: "u1", ChannelID: "db-only", ChannelType: 2, ReadSeq: 1, ActiveAt: 900},
			{UID: "u1", ChannelID: "a-db-tie", ChannelType: 2, ActiveAt: 800},
			{UID: "u2", ChannelID: "other-user", ChannelType: 2, ActiveAt: 2000},
		},
	}
	m := NewManager(Options{Store: store})

	err := m.MarkActive(ctx, []ActivePatch{
		{UID: "u1", ChannelID: "shared", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 10},
		{UID: "u1", ChannelID: "b-cache-tie", ChannelType: 2, ActiveAtMS: 800},
	})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	page, err := m.ListActiveView(ctx, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}

	wantRows := []metadb.UserConversationState{
		{UID: "u1", ChannelID: "shared", ChannelType: 2, ReadSeq: 10, DeletedToSeq: 7, ActiveAt: 1000, UpdatedAt: 101},
		{UID: "u1", ChannelID: "db-only", ChannelType: 2, ReadSeq: 1, ActiveAt: 900},
		{UID: "u1", ChannelID: "a-db-tie", ChannelType: 2, ActiveAt: 800},
		{UID: "u1", ChannelID: "b-cache-tie", ChannelType: 2, ActiveAt: 800},
	}
	if !reflect.DeepEqual(page.Rows, wantRows) {
		t.Fatalf("rows = %+v, want merged rows %+v", page.Rows, wantRows)
	}
	if page.Cursor != (metadb.UserConversationActiveCursor{ActiveAt: 800, ChannelID: "b-cache-tie", ChannelType: 2}) || !page.Done {
		t.Fatalf("cursor=%+v done=%v, want final cursor and done=true", page.Cursor, page.Done)
	}
}

func TestListActiveViewHydratesCacheOnlyDurableRow(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "db-only", ChannelType: 2, ActiveAt: 900},
		},
		primary: map[metadb.ConversationKey]metadb.UserConversationState{
			{ChannelID: "shared", ChannelType: 2}: {
				UID:          "u1",
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
		{UID: "u1", ChannelID: "shared", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 10},
	})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	page, err := m.ListActiveView(ctx, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}

	wantRows := []metadb.UserConversationState{
		{UID: "u1", ChannelID: "shared", ChannelType: 2, ReadSeq: 50, DeletedToSeq: 7, ActiveAt: 1000, UpdatedAt: 111, SparseActive: true},
		{UID: "u1", ChannelID: "db-only", ChannelType: 2, ActiveAt: 900},
	}
	if !reflect.DeepEqual(page.Rows, wantRows) {
		t.Fatalf("rows = %+v, want hydrated rows %+v", page.Rows, wantRows)
	}
	if len(store.lookups) != 1 || store.lookups[0] != (metadb.ConversationKey{ChannelID: "shared", ChannelType: 2}) {
		t.Fatalf("lookups = %+v, want shared primary lookup", store.lookups)
	}
}

func TestListActiveViewPropagatesCacheOnlyHydrationError(t *testing.T) {
	ctx := context.Background()
	lookupErr := errors.New("primary lookup failed")
	store := &recordingActiveStore{lookupErr: lookupErr}
	m := NewManager(Options{Store: store})
	err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "cache-only", ChannelType: 2, ActiveAtMS: 1000}})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	_, err = m.ListActiveView(ctx, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("ListActiveView() error = %v, want %v", err, lookupErr)
	}
}

func TestListActiveViewRequiresStore(t *testing.T) {
	m := NewManager(Options{})
	if err := m.MarkActive(context.Background(), []ActivePatch{{UID: "u1", ChannelID: "cache-only", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	page, err := m.ListActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
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
		{UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAtMS: 300},
		{UID: "u1", ChannelID: "b", ChannelType: 1, ActiveAtMS: 200},
		{UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAtMS: 200},
		{UID: "u1", ChannelID: "c", ChannelType: 2, ActiveAtMS: 100},
	})
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	first, err := m.ListActiveView(ctx, "u1", metadb.UserConversationActiveCursor{}, 2)
	if err != nil {
		t.Fatalf("ListActiveView(first) error = %v", err)
	}
	if activeChannelIDs(first.Rows) != "a,b" || first.Cursor != (metadb.UserConversationActiveCursor{ActiveAt: 200, ChannelID: "b", ChannelType: 1}) || first.Done {
		t.Fatalf("first page=%+v, want first two cache rows with done=false", first)
	}

	second, err := m.ListActiveView(ctx, "u1", first.Cursor, 10)
	if err != nil {
		t.Fatalf("ListActiveView(second) error = %v", err)
	}
	if activeChannelIDs(second.Rows) != "b,c" || second.Cursor != (metadb.UserConversationActiveCursor{ActiveAt: 100, ChannelID: "c", ChannelType: 2}) || !second.Done {
		t.Fatalf("second page=%+v, want remaining cache rows with done=true", second)
	}
}

func TestListActiveViewNonPositiveLimitReturnsEmptyDonePage(t *testing.T) {
	store := &recordingActiveStore{rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}}}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(context.Background(), []ActivePatch{{UID: "u1", ChannelID: "cache", ChannelType: 2, ActiveAtMS: 200}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	after := metadb.UserConversationActiveCursor{ActiveAt: 300, ChannelID: "before", ChannelType: 2}

	page, err := m.ListActiveView(context.Background(), "u1", after, 0)
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
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 7}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want 1", got)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Flushed != 1 {
		t.Fatalf("Flush() flushed = %d, want 1", result.Flushed)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want 0", got)
	}

	want := metadb.UserConversationActivePatch{
		UID:         "u1",
		ChannelID:   "room-1",
		ChannelType: 2,
		ReadSeq:     7,
		ActiveAt:    1000,
		UpdatedAt:   1000,
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 1 {
		t.Fatalf("touches = %+v, want one patch", store.touches)
	}
	if got := store.touches[0][0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("touch patch = %+v, want %+v", got, want)
	}
	if patch := store.touches[0][0]; patch.DeletedToSeq != 0 || patch.MessageSeq != 0 || patch.SparseActive || patch.SparseActiveSet {
		t.Fatalf("touch patch set unmanaged fields: %+v", patch)
	}
}

func TestManagerObservesCacheRowsAndDirtyLag(t *testing.T) {
	observer := &recordingConversationActiveObserver{}
	m := NewManager(Options{
		NowMS:    func() int64 { return 2500 },
		Observer: observer,
	})

	if err := m.MarkActive(context.Background(), []ActivePatch{
		{UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAtMS: 1000},
		{UID: "u1", ChannelID: "new", ChannelType: 2, ActiveAtMS: 2000},
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

func TestFlushZeroLimitFlushesAllDirtyRows(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{
		{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000},
		{UID: "u2", ChannelID: "room-2", ChannelType: 1, ActiveAtMS: 2000, ReadSeq: 5},
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
	if result.Selected != 2 || result.Flushed != 2 {
		t.Fatalf("Flush() result = %+v, want selected=2 flushed=2", result)
	}
	if len(store.touches) != 1 || len(store.touches[0]) != 2 {
		t.Fatalf("touches = %+v, want one batch with two patches", store.touches)
	}
	if got := m.DirtyCountForTest(); got != 0 {
		t.Fatalf("DirtyCountForTest() = %d, want 0", got)
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
	if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	result, err := m.Flush(ctx, 0)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Selected != 1 || result.Flushed != 1 {
		t.Fatalf("Flush() result = %+v, want selected=1 flushed=1", result)
	}

	flush := observer.lastFlush(t)
	if flush.Result != "ok" || flush.Selected != 1 || flush.Flushed != 1 {
		t.Fatalf("flush observation = %+v, want ok selected=1 flushed=1", flush)
	}
	if flush.Duration <= 0 {
		t.Fatalf("flush duration = %s, want positive", flush.Duration)
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
	if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	if _, err := m.Flush(ctx, 0); !errors.Is(err, touchErr) {
		t.Fatalf("Flush(error) = %v, want %v", err, touchErr)
	}
	failed := observer.lastFlush(t)
	if failed.Result != "error" || failed.Selected != 1 || failed.Flushed != 0 {
		t.Fatalf("failed flush observation = %+v, want error selected=1 flushed=0", failed)
	}

	store.touchErr = nil
	if _, err := m.Flush(ctx, 0); err != nil {
		t.Fatalf("Flush(cleanup) error = %v", err)
	}
	if _, err := m.Flush(ctx, 0); err != nil {
		t.Fatalf("Flush(no dirty) error = %v", err)
	}
	noDirty := observer.lastFlush(t)
	if noDirty.Result != "no_dirty" || noDirty.Selected != 0 || noDirty.Flushed != 0 {
		t.Fatalf("no-dirty flush observation = %+v, want no_dirty selected=0 flushed=0", noDirty)
	}
}

func TestFlushFailureKeepsDirty(t *testing.T) {
	ctx := context.Background()
	touchErr := errors.New("touch failed")
	store := &recordingActiveStore{touchErr: touchErr}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	result, err := m.Flush(ctx, 0)
	if !errors.Is(err, touchErr) {
		t.Fatalf("Flush() error = %v, want %v", err, touchErr)
	}
	if result.Flushed != 0 {
		t.Fatalf("Flush() flushed = %d, want 0 on failure", result.Flushed)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want 1", got)
	}
}

func TestFlushDoesNotClearConcurrentDirtyUpdate(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store})
	if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 1000, ReadSeq: 7}}); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	store.touchHook = func() {
		if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "room-1", ChannelType: 2, ActiveAtMS: 2000, ReadSeq: 9}}); err != nil {
			t.Fatalf("MarkActive(concurrent) error = %v", err)
		}
	}

	result, err := m.Flush(ctx, 1)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if result.Flushed != 1 {
		t.Fatalf("Flush() flushed = %d, want 1", result.Flushed)
	}
	if got := m.DirtyCountForTest(); got != 1 {
		t.Fatalf("DirtyCountForTest() = %d, want 1 after concurrent update", got)
	}
	entry, ok := m.EntryForTest("u1", "room-1", 2)
	if !ok {
		t.Fatalf("entry was removed")
	}
	if entry.ActiveAtMS != 2000 || entry.ReadSeq != 9 {
		t.Fatalf("entry = %+v, want newer active/read values", entry)
	}
	if got := store.touches[0][0].ActiveAt; got != 1000 {
		t.Fatalf("flushed ActiveAt = %d, want original snapshot 1000", got)
	}
}

func TestAdmitUnderCachePressureSpillsDirtyRows(t *testing.T) {
	ctx := context.Background()
	store := &recordingActiveStore{}
	m := NewManager(Options{Store: store, MaxCachedRows: 1})
	if err := m.MarkActive(ctx, []ActivePatch{{UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAtMS: 1000}}); err != nil {
		t.Fatalf("MarkActive(old) error = %v", err)
	}

	err := m.AdmitActiveBatch(ctx, ActiveBatch{
		SenderUID:   "u1",
		ChannelID:   "new",
		ChannelType: 2,
		MessageSeq:  10,
		ActiveAtMS:  2000,
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	if _, ok := m.EntryForTest("u1", "old", 2); ok {
		t.Fatalf("old flushed row is still cached under pressure")
	}
	newEntry, ok := m.EntryForTest("u1", "new", 2)
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

type recordingActiveStore struct {
	rows      []metadb.UserConversationState
	primary   map[metadb.ConversationKey]metadb.UserConversationState
	calls     int
	lastAfter metadb.UserConversationActiveCursor
	lastLimit int
	lookupErr error
	lookups   []metadb.ConversationKey
	touchErr  error
	touchHook func()
	touches   [][]metadb.UserConversationActivePatch
}

type recordingConversationActiveObserver struct {
	cache []CacheObservation
	flush []FlushObservation
}

func (o *recordingConversationActiveObserver) ObserveConversationActiveCache(event CacheObservation) {
	o.cache = append(o.cache, event)
}

func (o *recordingConversationActiveObserver) ObserveConversationActiveFlush(event FlushObservation) {
	o.flush = append(o.flush, event)
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

func (s *recordingActiveStore) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	s.calls++
	s.lastAfter = after
	s.lastLimit = limit

	rows := append([]metadb.UserConversationState(nil), s.rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ActiveAt != rows[j].ActiveAt {
			return rows[i].ActiveAt > rows[j].ActiveAt
		}
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].ChannelType < rows[j].ChannelType
	})

	candidates := make([]metadb.UserConversationState, 0, len(rows))
	for _, row := range rows {
		if row.UID != uid || !testActiveRowAfter(row, after) {
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
		cursor = metadb.UserConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return candidates, cursor, done, nil
}

func (s *recordingActiveStore) GetUserConversationState(_ context.Context, _ string, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	key := metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}
	s.lookups = append(s.lookups, key)
	if s.lookupErr != nil {
		return metadb.UserConversationState{}, false, s.lookupErr
	}
	row, ok := s.primary[key]
	return row, ok, nil
}

func (s *recordingActiveStore) TouchUserConversationActiveAt(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	batch := append([]metadb.UserConversationActivePatch(nil), patches...)
	s.touches = append(s.touches, batch)
	if s.touchHook != nil {
		s.touchHook()
	}
	if s.touchErr != nil {
		return s.touchErr
	}
	return nil
}

func testActiveRowAfter(row metadb.UserConversationState, after metadb.UserConversationActiveCursor) bool {
	if after == (metadb.UserConversationActiveCursor{}) {
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

func activeChannelIDs(rows []metadb.UserConversationState) string {
	var out string
	for _, row := range rows {
		if out != "" {
			out += ","
		}
		out += row.ChannelID
	}
	return out
}
