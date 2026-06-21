package app

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var _ accessnode.ConversationAuthority = (*conversationAuthority)(nil)

func TestConversationAuthorityRuntimeListSeesCacheBeforeFlush(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		MaxRowsPerUID:   10,
		MaxRows:         100,
		ListDBWindowMax: 20,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)

	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID:          "u1",
		Kind:         metadb.ConversationKindNormal,
		ChannelID:    "runtime-cache",
		ChannelType:  2,
		ReadSeq:      7,
		DeletedToSeq: 99,
		ActiveAt:     300,
		UpdatedAt:    301,
		SparseActive: true,
		MessageSeq:   9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}

	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %#v, want one runtime cache row", page.Rows)
	}
	row := page.Rows[0]
	if row.ChannelID != "runtime-cache" || row.ActiveAt != 300 || row.ReadSeq != 7 {
		t.Fatalf("row = %#v, want runtime cache row with active/read fields", row)
	}
	if row.DeletedToSeq != 0 || row.SparseActive || row.UpdatedAt != 0 {
		t.Fatalf("row = %#v, want authority admission to ignore delete/sparse/update fields", row)
	}
	if len(store.touched) != 0 {
		t.Fatalf("durable patches = %#v, want no DB flush before list", store.touched)
	}
}

func TestConversationAuthorityListMergesCacheAndDB(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:          1,
		Store:                store,
		MaxRowsPerUID:        10,
		MaxRows:              100,
		ListDBWindowMax:      20,
		AdmissionBatchRows:   10,
		AdmissionConcurrency: 1,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "cache", ChannelType: 2, ActiveAt: 300, UpdatedAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 2 || page.Rows[0].ChannelID != "cache" || page.Rows[1].ChannelID != "db" {
		t.Fatalf("rows = %#v, want cache before db", page.Rows)
	}
}

func TestConversationAuthorityCachePressureRejectsOversizedBatch(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		MaxRows:         1,
		ListDBWindowMax: 1,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	})
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
	}
	if len(store.touched) != 0 {
		t.Fatalf("durable patches = %#v, want runtime to keep pressure handling out of app fallback", store.touched)
	}
}

func TestConversationAuthorityAdmitActiveBatchLazyActivatesCurrentLocalRoute(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	routeLookups := 0
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		CurrentRouteTarget: func(hashSlot uint16) (conversationusecase.RouteTarget, bool) {
			routeLookups++
			if hashSlot != target.HashSlot {
				return conversationusecase.RouteTarget{}, false
			}
			return target, true
		},
	})

	err := authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "u1",
		ChannelID:   "g-lazy",
		ChannelType: 2,
		MessageSeq:  7,
		ActiveAtMS:  100,
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v, want lazy current local route activation", err)
	}
	if routeLookups != 1 {
		t.Fatalf("current route lookups after lazy activation = %d, want 1", routeLookups)
	}

	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g-lazy" || page.Rows[0].ReadSeq != 7 {
		t.Fatalf("rows = %#v, want lazily admitted active row", page.Rows)
	}
	if routeLookups != 1 {
		t.Fatalf("current route lookups after active fast path = %d, want still 1", routeLookups)
	}
}

func TestConversationAuthorityAdmitActiveBatchActiveFastPathSkipsCurrentRouteLookup(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	routeLookups := 0
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		CurrentRouteTarget: func(uint16) (conversationusecase.RouteTarget, bool) {
			routeLookups++
			return target, true
		},
	})
	authority.markActive(target)

	err := authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "u1",
		ChannelID:   "g-fast",
		ChannelType: 2,
		MessageSeq:  7,
		ActiveAtMS:  100,
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if routeLookups != 0 {
		t.Fatalf("current route lookups on active fast path = %d, want 0", routeLookups)
	}
}

func TestConversationAuthorityObservesAdmitResults(t *testing.T) {
	observer := &recordingConversationAuthorityObserver{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           &recordingConversationAuthorityStore{},
		MaxRowsPerUID:   10,
		MaxRows:         10,
		ListDBWindowMax: 20,
		Observer:        observer,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	staleTarget := target
	staleTarget.LeaderTerm = 1
	err := authority.AdmitPatches(context.Background(), staleTarget, []conversationusecase.ActivePatch{{
		UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(stale) error = %v, want ErrStaleRoute", err)
	}
	if got := observer.admitResults(); len(got) != 2 || got[0] != "ok" || got[1] != "stale_route" {
		t.Fatalf("admit observations = %#v, want ok then stale_route", got)
	}
}

func TestConversationAuthorityObservesCachePressureInAdmit(t *testing.T) {
	admitObserver := &recordingConversationAuthorityObserver{}
	admitAuthority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           &recordingConversationAuthorityStore{},
		MaxRows:         1,
		ListDBWindowMax: 20,
		Observer:        admitObserver,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	admitAuthority.markActive(target)
	err := admitAuthority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	})
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
	}
	if got := admitObserver.cachePressureLabels(); len(got) != 1 || got[0] != "admit:cache_pressure" {
		t.Fatalf("cache pressure observations = %#v, want admit cache pressure", got)
	}
}

func TestConversationAuthorityAdmitCachePressureObserverRunsAfterUnlock(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	observer := &reentrantConversationAuthorityObserver{
		target: target,
		uid:    "u1",
		done:   make(chan error, 1),
	}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           &recordingConversationAuthorityStore{},
		MaxRows:         1,
		ListDBWindowMax: 20,
		Observer:        observer,
	})
	observer.authority = authority
	authority.markActive(target)

	errCh := make(chan error, 1)
	go func() {
		errCh <- authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
		})
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, conversationusecase.ErrCachePressure) {
			t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("AdmitPatches() did not return; cache-pressure observer likely ran while authority lock was held")
	}
	select {
	case err := <-observer.done:
		if err != nil {
			t.Fatalf("observer reentrant list error = %v", err)
		}
	default:
		t.Fatal("cache-pressure observer did not run")
	}
	if got := observer.cachePressureLabels(); len(got) != 1 || got[0] != "admit:cache_pressure" {
		t.Fatalf("cache pressure observations = %#v, want admit cache pressure exactly once", got)
	}
}

func TestConversationAuthorityObservesListSuccessAndError(t *testing.T) {
	observer := &recordingConversationAuthorityObserver{}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		MaxRowsPerUID:   10,
		MaxRows:         100,
		ListDBWindowMax: 20,
		Observer:        observer,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if _, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10); err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}

	store.listErr = errors.New("store list failed")
	if _, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10); err == nil {
		t.Fatal("ListConversationActiveViewForTarget() error = nil, want store error")
	}
	if got := observer.listResults(); len(got) != 2 || got[0] != "ok" || got[1] != "error" {
		t.Fatalf("list observations = %#v, want ok then error", got)
	}
}

func TestConversationAuthorityAdmissionPressureDoesNotPartiallyCache(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           &recordingConversationAuthorityStore{},
		MaxRowsPerUID:   1,
		MaxRows:         10,
		ListDBWindowMax: 20,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 2 || page.Rows[0].ChannelID != "a" || page.Rows[1].ChannelID != "b" {
		t.Fatalf("rows = %#v, want durable fallback rows without partial cache admission", page.Rows)
	}
}

func TestConversationAuthorityCachePressureDoesNotDurablyFallback(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		MaxRows:         1,
		ListDBWindowMax: 20,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)

	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ReadSeq: 6, DeletedToSeq: 6, ActiveAt: 300, UpdatedAt: 300, MessageSeq: 7},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, UpdatedAt: 200, MessageSeq: 5},
	})
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
	}
	if len(store.touched) != 0 {
		t.Fatalf("durable patches = %#v, want no app durable fallback after cache pressure", store.touched)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want failed oversized batch not partially cached", page.Rows)
	}
}

func TestConversationAuthorityFlushPersistsRuntimeReadFloor(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		MaxRowsPerUID:   10,
		MaxRows:         100,
		ListDBWindowMax: 20,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	patch := conversationusecase.ActivePatch{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "join-floor", ChannelType: 2, ReadSeq: 8, DeletedToSeq: 8, ActiveAt: 300, UpdatedAt: 300, MessageSeq: 9,
	}
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{patch}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}

	if err := authority.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(store.touched) != 1 || store.touched[0].ReadSeq != 8 || store.touched[0].DeletedToSeq != 0 || store.touched[0].MessageSeq != 0 {
		t.Fatalf("durable patches = %#v, want flushed runtime read floor only", store.touched)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ReadSeq != 8 || page.Rows[0].DeletedToSeq != 0 {
		t.Fatalf("rows = %#v, want DB row to keep runtime read floor after cache flush", page.Rows)
	}
}

func TestConversationAuthorityCacheOnlyRowHydratesPrimaryDeleteBarrier(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.ConversationState{
			{ChannelID: "hidden", ChannelType: 2}: {UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "hidden" || page.Rows[0].DeletedToSeq != 10 || page.Rows[0].ActiveAt != 300 {
		t.Fatalf("rows = %#v, want cache row hydrated with durable delete barrier", page.Rows)
	}
}

func TestConversationAuthorityDBActiveRowKeepsDeleteBarrierDuringCacheOverlay(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "same", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 100,
		}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "same", ChannelType: 2, ActiveAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ActiveAt != 300 || page.Rows[0].DeletedToSeq != 10 {
		t.Fatalf("rows = %#v, want cache active time over durable delete barrier", page.Rows)
	}
}

func TestConversationAuthorityCachedDeleteBarrierIsIgnoredByRuntimeAdmission(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, DeletedToSeq: 10, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "hidden" || page.Rows[0].DeletedToSeq != 0 {
		t.Fatalf("rows = %#v, want runtime admission to ignore cached delete barrier", page.Rows)
	}
}

func TestConversationAuthorityCoalescedRowIgnoresMessageSeqFence(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.ConversationState{
			{ChannelID: "hidden", ChannelType: 2}: {UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, MessageSeq: 9},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden", ChannelType: 2, ActiveAt: 200, MessageSeq: 11},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "hidden" || page.Rows[0].ActiveAt != 300 || page.Rows[0].DeletedToSeq != 10 {
		t.Fatalf("rows = %#v, want coalesced runtime row hydrated from durable state", page.Rows)
	}
}

func TestConversationAuthoritySparseModeIsNotAdmittedFromRuntimePatch(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, MessageSeq: 30, SparseActive: true},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 20, SparseActive: false},
	}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].SparseActive {
		t.Fatalf("rows = %#v, want runtime admission to ignore sparse mode", page.Rows)
	}
}

func TestConversationAuthoritySparseModeFollowsNewerDBRow(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, SparseActive: true}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 20, SparseActive: false,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || !page.Rows[0].SparseActive {
		t.Fatalf("rows = %#v, want sparse mode from newer DB row", page.Rows)
	}
}

func TestConversationAuthorityListHydratesDurableDeleteBarriers(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.ConversationState{
			{ChannelID: "hidden-a", ChannelType: 2}: {UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden-a", ChannelType: 2, DeletedToSeq: 10},
			{ChannelID: "hidden-b", ChannelType: 2}: {UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden-b", ChannelType: 2, DeletedToSeq: 20},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden-a", ChannelType: 2, ActiveAt: 300, MessageSeq: 9},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "hidden-b", ChannelType: 2, ActiveAt: 200, MessageSeq: 19},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "visible", ChannelType: 2, ActiveAt: 100, MessageSeq: 30},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "hidden-a" || page.Rows[0].DeletedToSeq != 10 {
		t.Fatalf("rows = %#v, want highest runtime row hydrated with durable delete barrier", page.Rows)
	}
}

func TestConversationAuthorityManyCacheRowsSmallLimitDoesNotPressure(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 20, MaxRows: 100, ListDBWindowMax: 10})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	patches := make([]conversationusecase.ActivePatch, 0, 9)
	for i := 0; i < 9; i++ {
		patches = append(patches, conversationusecase.ActivePatch{
			UID:         "u1",
			Kind:        metadb.ConversationKindNormal,
			ChannelID:   string(rune('a' + i)),
			ChannelType: 2,
			ActiveAt:    int64(900 - i),
			MessageSeq:  uint64(90 - i),
		})
	}
	if err := authority.AdmitPatches(context.Background(), target, patches); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "a" {
		t.Fatalf("rows = %#v, want top cache row without pressure", page.Rows)
	}
}

func TestConversationAuthorityFlushUsesRuntimeTouchPatch(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, MessageSeq: 30, SparseActive: true,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if err := authority.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(store.touched) != 1 || store.touched[0].MessageSeq != 0 || store.touched[0].SparseActiveSet {
		t.Fatalf("touched = %#v, want one runtime active touch patch", store.touched)
	}
}

func TestConversationAuthorityWarmingRejectsList(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markWarming(target)
	_, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("ListConversationActiveView() error = %v, want ErrRouteNotReady", err)
	}
}

func TestConversationAuthorityListForWarmingTargetRejectsEvenWithActiveTarget(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	warming := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(active)
	authority.markWarming(warming)
	_, err := authority.ListConversationActiveViewForTarget(context.Background(), warming, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v, want ErrRouteNotReady", err)
	}
}

func TestConversationAuthorityListForStaleTargetRejects(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	stale := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(active)
	_, err := authority.ListConversationActiveViewForTarget(context.Background(), stale, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityListForActiveTargetSucceeds(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(active)
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), active, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "db" {
		t.Fatalf("rows = %#v, want active target DB row", page.Rows)
	}
}

func TestConversationAuthorityAcceptsSameDistributedIdentityWithDifferentRouteRevisionAndAuthorityEpoch(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := oldTarget
	newTarget.RouteRevision = 5
	newTarget.AuthorityEpoch = 6
	authority.markActive(oldTarget)
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "old", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}})
	if err != nil {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want same distributed identity accepted", err)
	}
	_, err = authority.ListConversationActiveViewForTarget(context.Background(), oldTarget, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget(oldTarget) error = %v, want same distributed identity accepted", err)
	}
}

func TestConversationAuthorityDifferentLeaderTermRejectsOldTarget(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := oldTarget
	newTarget.LeaderTerm = 10
	newTarget.RouteRevision = 5
	newTarget.AuthorityEpoch = 6
	authority.markActive(oldTarget)
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "old", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want ErrStaleRoute", err)
	}
	_, err = authority.ListConversationActiveViewForTarget(context.Background(), oldTarget, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveViewForTarget(oldTarget) error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityDifferentConfigEpochRejectsOldTarget(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := oldTarget
	newTarget.ConfigEpoch = 4
	newTarget.RouteRevision = 5
	newTarget.AuthorityEpoch = 6
	authority.markActive(oldTarget)
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "old", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthoritySupersededSlotMoveRejectsOldTarget(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 7, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(oldTarget)
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "old", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want ErrStaleRoute", err)
	}
	_, err = authority.ListConversationActiveViewForTarget(context.Background(), oldTarget, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveViewForTarget(oldTarget) error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityListForDrainedTargetRejects(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	result, err := authority.DrainAuthority(context.Background(), target)
	if err != nil {
		t.Fatalf("DrainAuthority() error = %v", err)
	}
	if result != conversationDrainResultNoDirty {
		t.Fatalf("DrainAuthority() result = %q, want %q", result, conversationDrainResultNoDirty)
	}
	_, err = authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityListForTargetRechecksDrainingAfterStoreRead(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
		beforeList: func() {
			_, _ = authority.DrainAuthority(context.Background(), target)
		},
	}
	authority.store = store
	authority.markActive(target)
	_, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityUnscopedListRechecksDrainingAfterStoreRead(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
		beforeList: func() {
			_, _ = authority.DrainAuthority(context.Background(), target)
		},
	}
	authority.store = store
	authority.markActive(target)
	_, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveView() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityUnscopedListRejectsSecondActiveAfterStoreRead(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
		beforeList: func() {
			authority.markActive(targetB)
		},
	}
	authority.store = store
	authority.markActive(targetA)
	_, err := authority.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListConversationActiveView() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityDrainFlushesTargetDirtyRowsBeforeHandoff(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(targetA)
	authority.markActive(targetB)
	if err := authority.AdmitPatches(context.Background(), targetA, []conversationusecase.ActivePatch{{
		UID: "u-slot-1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}}); err != nil {
		t.Fatalf("AdmitPatches(targetA) error = %v", err)
	}
	if err := authority.AdmitPatches(context.Background(), targetB, []conversationusecase.ActivePatch{{
		UID: "u-slot-9", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
	}}); err != nil {
		t.Fatalf("AdmitPatches(targetB) error = %v", err)
	}
	result, err := authority.DrainAuthority(context.Background(), targetA)
	if err != nil {
		t.Fatalf("DrainAuthority() error = %v", err)
	}
	if result != conversationDrainResultDrained {
		t.Fatalf("DrainAuthority() result = %q, want %q", result, conversationDrainResultDrained)
	}
	if len(store.touched) != 1 || store.touched[0].ChannelID != "a" {
		t.Fatalf("touched = %#v, want only target A runtime dirty row flushed before handoff", store.touched)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), targetB, metadb.ConversationKindNormal, "u-slot-9", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget(targetB) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "b" {
		t.Fatalf("target B rows = %#v, want target B cache row untouched by target A drain", page.Rows)
	}
}

func TestConversationAuthorityDrainFlushesOnlyTargetHashSlot(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(targetA)
	authority.markActive(targetB)
	if err := authority.AdmitPatches(context.Background(), targetA, []conversationusecase.ActivePatch{{
		UID: "u-slot-1", Kind: metadb.ConversationKindNormal, ChannelID: "target-a", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}}); err != nil {
		t.Fatalf("AdmitPatches(targetA) error = %v", err)
	}
	if err := authority.AdmitPatches(context.Background(), targetB, []conversationusecase.ActivePatch{{
		UID: "u-slot-9", Kind: metadb.ConversationKindNormal, ChannelID: "target-b", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
	}}); err != nil {
		t.Fatalf("AdmitPatches(targetB) error = %v", err)
	}

	result, err := authority.DrainAuthority(context.Background(), targetA)
	if err != nil {
		t.Fatalf("DrainAuthority(targetA) error = %v", err)
	}
	if result != conversationDrainResultDrained {
		t.Fatalf("DrainAuthority(targetA) result = %q, want %q", result, conversationDrainResultDrained)
	}
	if len(store.touched) != 1 || store.touched[0].ChannelID != "target-a" {
		t.Fatalf("touched = %#v, want only target-a flushed during targetA drain", store.touched)
	}

	page, err := authority.ListConversationActiveViewForTarget(context.Background(), targetB, metadb.ConversationKindNormal, "u-slot-9", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget(targetB) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "target-b" {
		t.Fatalf("target B rows = %#v, want target-b still served from cache", page.Rows)
	}
}

func TestConversationAuthorityDrainLeavesOtherTargetDirtyRowsAlone(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(targetA)
	authority.markActive(targetB)
	if err := authority.AdmitPatches(context.Background(), targetB, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
	}}); err != nil {
		t.Fatalf("AdmitPatches(targetB) error = %v", err)
	}
	result, err := authority.DrainAuthority(context.Background(), targetA)
	if err != nil {
		t.Fatalf("DrainAuthority() error = %v", err)
	}
	if result != conversationDrainResultNoDirty {
		t.Fatalf("DrainAuthority() result = %q, want %q", result, conversationDrainResultNoDirty)
	}
	if len(store.touched) != 0 {
		t.Fatalf("touched = %#v, want target B dirty row left alone", store.touched)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), targetB, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget(targetB) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "b" {
		t.Fatalf("target B rows = %#v, want target B cache row still visible", page.Rows)
	}
}

func TestConversationAuthorityObservesHandoffTimeout(t *testing.T) {
	observer := &recordingConversationAuthorityObserver{}
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		MaxRowsPerUID:   10,
		MaxRows:         100,
		ListDBWindowMax: 20,
		Observer:        observer,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "dirty", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err := authority.DrainAuthority(ctx, target)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DrainAuthority() error = %v, want DeadlineExceeded", err)
	}
	if result != conversationDrainResultBusy {
		t.Fatalf("DrainAuthority() result = %q, want busy", result)
	}
	if got := observer.handoffResults(); len(got) != 1 || got[0] != "timeout" {
		t.Fatalf("handoff observations = %#v, want timeout", got)
	}
}

func TestConversationAuthorityDrainUnknownTargetDoesNotPoisonActiveList(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	unknown := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, ConfigEpoch: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(active)
	result, err := authority.DrainAuthority(context.Background(), unknown)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("DrainAuthority(unknown) error = %v, want ErrStaleRoute", err)
	}
	if result != conversationDrainResultNoDirty {
		t.Fatalf("DrainAuthority(unknown) result = %q, want %q", result, conversationDrainResultNoDirty)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), active, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget(active) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "db" {
		t.Fatalf("rows = %#v, want active target unaffected", page.Rows)
	}
}

func TestConversationAuthorityStoreFakeHonorsCursorLimitAndOrdering(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{
			{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "other", ChannelType: 2, ActiveAt: 900},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "c", ChannelType: 2, ActiveAt: 100},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "a", ChannelType: 2, ActiveAt: 300},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 1, ActiveAt: 200},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "b", ChannelType: 2, ActiveAt: 200},
		},
	}
	first, cursor, done, err := store.ListConversationActivePage(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 2)
	if err != nil {
		t.Fatalf("ListConversationActivePage(first) error = %v", err)
	}
	if done || cursor != (metadb.ConversationActiveCursor{ActiveAt: 200, ChannelID: "b", ChannelType: 1}) || conversationAuthorityChannelIDs(first) != "a,b" {
		t.Fatalf("first rows=%#v cursor=%+v done=%v, want first ordered page", first, cursor, done)
	}
	second, cursor, done, err := store.ListConversationActivePage(context.Background(), metadb.ConversationKindNormal, "u1", cursor, 2)
	if err != nil {
		t.Fatalf("ListConversationActivePage(second) error = %v", err)
	}
	if !done || cursor != (metadb.ConversationActiveCursor{ActiveAt: 100, ChannelID: "c", ChannelType: 2}) || conversationAuthorityChannelIDs(second) != "b,c" {
		t.Fatalf("second rows=%#v cursor=%+v done=%v, want second ordered page", second, cursor, done)
	}
}

type recordingConversationAuthorityStore struct {
	activeRows []metadb.ConversationState
	primary    map[metadb.ConversationKey]metadb.ConversationState
	touched    []metadb.ConversationActivePatch
	listErr    error
	beforeList func()
}

func (s *recordingConversationAuthorityStore) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	if s.beforeList != nil {
		s.beforeList()
	}
	if s.listErr != nil {
		return nil, after, false, s.listErr
	}
	rows := append([]metadb.ConversationState(nil), s.activeRows...)
	sortConversationRows(rows)
	candidates := make([]metadb.ConversationState, 0, len(rows))
	for _, row := range rows {
		if row.UID != uid || row.Kind != kind || !conversationRowAfter(row, after) {
			continue
		}
		candidates = append(candidates, row)
	}
	done := len(candidates) <= limit
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	cursor := after
	if len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		cursor = metadb.ConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return candidates, cursor, done, nil
}

func (s *recordingConversationAuthorityStore) GetConversationState(_ context.Context, kind metadb.ConversationKind, _ string, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	if s.primary == nil {
		return metadb.ConversationState{}, false, nil
	}
	row, ok := s.primary[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if ok && row.Kind != kind {
		return metadb.ConversationState{}, false, nil
	}
	return row, ok, nil
}

func (s *recordingConversationAuthorityStore) GetConversationStates(_ context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	if s.primary == nil {
		return states, nil
	}
	for _, key := range keys {
		row, ok := s.primary[metadb.ConversationKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}]
		if !ok || row.UID != key.UID || row.Kind != key.Kind {
			continue
		}
		states[key] = row
	}
	return states, nil
}

func (s *recordingConversationAuthorityStore) TouchConversationActiveAtBatch(ctx context.Context, patches []metadb.ConversationActivePatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.touched = append(s.touched, patches...)
	if s.primary == nil {
		s.primary = make(map[metadb.ConversationKey]metadb.ConversationState)
	}
	for _, patch := range patches {
		key := metadb.ConversationKey{ChannelID: patch.ChannelID, ChannelType: patch.ChannelType}
		state, ok := s.primary[key]
		if !ok {
			state = metadb.ConversationState{
				UID:         patch.UID,
				Kind:        patch.Kind,
				ChannelID:   patch.ChannelID,
				ChannelType: patch.ChannelType,
			}
		}
		deleteBarrier := state.DeletedToSeq
		if patch.DeletedToSeq > deleteBarrier {
			deleteBarrier = patch.DeletedToSeq
		}
		if patch.MessageSeq > 0 {
			if deleteBarrier >= patch.MessageSeq {
				patch.ActiveAt = 0
			}
		}
		if patch.ActiveAt > state.ActiveAt {
			state.ActiveAt = patch.ActiveAt
		}
		if patch.ReadSeq > state.ReadSeq {
			state.ReadSeq = patch.ReadSeq
		}
		if patch.DeletedToSeq > state.DeletedToSeq {
			state.DeletedToSeq = patch.DeletedToSeq
		}
		if patch.UpdatedAt > state.UpdatedAt {
			state.UpdatedAt = patch.UpdatedAt
		}
		if patch.SparseActiveSet {
			state.SparseActive = patch.SparseActive
		}
		s.upsertActiveRow(state)
	}
	return nil
}

func (s *recordingConversationAuthorityStore) upsertActiveRow(state metadb.ConversationState) {
	if s.primary == nil {
		s.primary = make(map[metadb.ConversationKey]metadb.ConversationState)
	}
	key := metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}
	if existing, ok := s.primary[key]; ok && existing.UID == state.UID {
		state = mergeConversationState(existing, state)
	}
	s.primary[key] = state
	if state.ActiveAt <= 0 {
		return
	}
	for idx, row := range s.activeRows {
		if row.UID == state.UID && row.ChannelID == state.ChannelID && row.ChannelType == state.ChannelType {
			s.activeRows[idx] = state
			return
		}
	}
	s.activeRows = append(s.activeRows, state)
}

func conversationAuthorityRowsContain(rows []metadb.ConversationState, channelID string) bool {
	for _, row := range rows {
		if row.ChannelID == channelID {
			return true
		}
	}
	return false
}

func conversationAuthorityPatchesContain(patches []metadb.ConversationActivePatch, channelID string) bool {
	for _, patch := range patches {
		if patch.ChannelID == channelID {
			return true
		}
	}
	return false
}

type recordingConversationAuthorityObserver struct {
	admit         []conversationAuthorityAdmitEvent
	cachePressure []conversationAuthorityCachePressureEvent
	lists         []conversationAuthorityListEvent
	handoffs      []conversationAuthorityHandoffEvent
}

func (o *recordingConversationAuthorityObserver) ObserveConversationAuthorityAdmit(event conversationAuthorityAdmitEvent) {
	o.admit = append(o.admit, event)
}

func (o *recordingConversationAuthorityObserver) ObserveConversationAuthorityCachePressure(event conversationAuthorityCachePressureEvent) {
	o.cachePressure = append(o.cachePressure, event)
}

func (o *recordingConversationAuthorityObserver) ObserveConversationAuthorityList(event conversationAuthorityListEvent) {
	o.lists = append(o.lists, event)
}

func (o *recordingConversationAuthorityObserver) ObserveConversationAuthorityHandoff(event conversationAuthorityHandoffEvent) {
	o.handoffs = append(o.handoffs, event)
}

func (o *recordingConversationAuthorityObserver) admitResults() []string {
	out := make([]string, 0, len(o.admit))
	for _, event := range o.admit {
		out = append(out, event.Result)
	}
	return out
}

func (o *recordingConversationAuthorityObserver) listResults() []string {
	out := make([]string, 0, len(o.lists))
	for _, event := range o.lists {
		out = append(out, event.Result)
	}
	return out
}

func (o *recordingConversationAuthorityObserver) handoffResults() []string {
	out := make([]string, 0, len(o.handoffs))
	for _, event := range o.handoffs {
		out = append(out, event.Result)
	}
	return out
}

func (o *recordingConversationAuthorityObserver) cachePressureLabels() []string {
	out := make([]string, 0, len(o.cachePressure))
	for _, event := range o.cachePressure {
		out = append(out, event.Phase+":"+event.Result)
	}
	return out
}

type reentrantConversationAuthorityObserver struct {
	recordingConversationAuthorityObserver
	authority *conversationAuthority
	target    conversationusecase.RouteTarget
	uid       string
	done      chan error
}

func (o *reentrantConversationAuthorityObserver) ObserveConversationAuthorityCachePressure(event conversationAuthorityCachePressureEvent) {
	o.recordingConversationAuthorityObserver.ObserveConversationAuthorityCachePressure(event)
	_, err := o.authority.ListConversationActiveViewForTarget(context.Background(), o.target, metadb.ConversationKindNormal, o.uid, metadb.ConversationActiveCursor{}, 10)
	o.done <- err
}

func conversationAuthorityChannelIDs(rows []metadb.ConversationState) string {
	var out string
	for i, row := range rows {
		if i > 0 {
			out += ","
		}
		out += row.ChannelID
	}
	return out
}
