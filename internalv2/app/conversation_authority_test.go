package app

import (
	"context"
	"errors"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityListMergesCacheAndDB(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
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
		UID: "u1", ChannelID: "cache", ChannelType: 2, ActiveAt: 300, UpdatedAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 2 || page.Rows[0].ChannelID != "cache" || page.Rows[1].ChannelID != "db" {
		t.Fatalf("rows = %#v, want cache before db", page.Rows)
	}
}

func TestConversationAuthorityCachePressureFailsList(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           &recordingConversationAuthorityStore{},
		MaxRowsPerUID:   1,
		MaxRows:         1,
		ListDBWindowMax: 1,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	})
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
	}
}

func TestConversationAuthorityAdmissionPressureDoesNotPartiallyApply(t *testing.T) {
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
		{UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	})
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want no partial admission rows", page.Rows)
	}
}

func TestConversationAuthorityCacheOnlyRowChecksPrimaryDeleteBarrier(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.UserConversationState{
			{ChannelID: "hidden", ChannelType: 2}: {UID: "u1", ChannelID: "hidden", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want hidden cache row blocked", page.Rows)
	}
}

func TestConversationAuthorityDBActiveRowDeleteBarrierBlocksSameKeyCacheOverlay(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{
			UID: "u1", ChannelID: "same", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 100,
		}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "same", ChannelType: 2, ActiveAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ActiveAt != 100 {
		t.Fatalf("rows = %#v, want DB row unchanged by stale cache overlay", page.Rows)
	}
}

func TestConversationAuthorityCachedDeleteBarrierSkipsRow(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, DeletedToSeq: 10, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want cached delete barrier to block row", page.Rows)
	}
}

func TestConversationAuthorityCoalescedRowKeepsActiveMessageSeqFence(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.UserConversationState{
			{ChannelID: "hidden", ChannelType: 2}: {UID: "u1", ChannelID: "hidden", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, MessageSeq: 9},
		{UID: "u1", ChannelID: "hidden", ChannelType: 2, ActiveAt: 200, MessageSeq: 11},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want coalesced active row blocked by original message seq", page.Rows)
	}
}

func TestConversationAuthoritySparseModeFollowsNewestPatch(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 300, MessageSeq: 30, SparseActive: true},
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 20, SparseActive: false},
	}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || !page.Rows[0].SparseActive {
		t.Fatalf("rows = %#v, want sparse mode from newest patch", page.Rows)
	}
}

func TestConversationAuthoritySparseModeFollowsNewerDBRow(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 300, SparseActive: true}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 20, SparseActive: false,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || !page.Rows[0].SparseActive {
		t.Fatalf("rows = %#v, want sparse mode from newer DB row", page.Rows)
	}
}

func TestConversationAuthorityListDoesNotDropLowerCacheRowsAfterDeleteBarrier(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.UserConversationState{
			{ChannelID: "hidden-a", ChannelType: 2}: {UID: "u1", ChannelID: "hidden-a", ChannelType: 2, DeletedToSeq: 10},
			{ChannelID: "hidden-b", ChannelType: 2}: {UID: "u1", ChannelID: "hidden-b", ChannelType: 2, DeletedToSeq: 20},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "hidden-a", ChannelType: 2, ActiveAt: 300, MessageSeq: 9},
		{UID: "u1", ChannelID: "hidden-b", ChannelType: 2, ActiveAt: 200, MessageSeq: 19},
		{UID: "u1", ChannelID: "visible", ChannelType: 2, ActiveAt: 100, MessageSeq: 30},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "visible" {
		t.Fatalf("rows = %#v, want lower-ranked eligible cache row", page.Rows)
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
			ChannelID:   string(rune('a' + i)),
			ChannelType: 2,
			ActiveAt:    int64(900 - i),
			MessageSeq:  uint64(90 - i),
		})
	}
	if err := authority.AdmitPatches(context.Background(), target, patches); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "a" {
		t.Fatalf("rows = %#v, want top cache row without pressure", page.Rows)
	}
}

func TestConversationAuthorityFlushUsesTouchPatch(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 300, MessageSeq: 30, SparseActive: true,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if err := authority.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(store.touched) != 1 || store.touched[0].MessageSeq != 30 || !store.touched[0].SparseActiveSet {
		t.Fatalf("touched = %#v, want one fenced sparse active patch", store.touched)
	}
}

func TestConversationAuthorityWarmingRejectsList(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markWarming(target)
	_, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("ListUserConversationActiveView() error = %v, want ErrRouteNotReady", err)
	}
}

func TestConversationAuthorityListForWarmingTargetRejectsEvenWithActiveTarget(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	warming := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(active)
	authority.markWarming(warming)
	_, err := authority.ListUserConversationActiveViewForTarget(context.Background(), warming, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v, want ErrRouteNotReady", err)
	}
}

func TestConversationAuthorityListForStaleTargetRejects(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	stale := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(active)
	_, err := authority.ListUserConversationActiveViewForTarget(context.Background(), stale, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityListForActiveTargetSucceeds(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(active)
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), active, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "db" {
		t.Fatalf("rows = %#v, want active target DB row", page.Rows)
	}
}

func TestConversationAuthoritySupersededTargetRejectsOldRoute(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(oldTarget)
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want ErrStaleRoute", err)
	}
	_, err = authority.ListUserConversationActiveViewForTarget(context.Background(), oldTarget, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveViewForTarget(oldTarget) error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthoritySupersededRevisionRetagsDirtyRows(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(oldTarget)
	if err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "dirty", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}}); err != nil {
		t.Fatalf("AdmitPatches(oldTarget) error = %v", err)
	}
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want ErrStaleRoute", err)
	}
	_, err = authority.ListUserConversationActiveViewForTarget(context.Background(), oldTarget, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveViewForTarget(oldTarget) error = %v, want ErrStaleRoute", err)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), newTarget, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget(newTarget) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "dirty" {
		t.Fatalf("rows = %#v, want dirty row retagged to new target", page.Rows)
	}
}

func TestConversationAuthoritySupersededSlotMoveRejectsOldTarget(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 7, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(oldTarget)
	authority.markActive(newTarget)
	err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "old", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches(oldTarget) error = %v, want ErrStaleRoute", err)
	}
	_, err = authority.ListUserConversationActiveViewForTarget(context.Background(), oldTarget, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveViewForTarget(oldTarget) error = %v, want ErrStaleRoute", err)
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
	_, err = authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityListForTargetRechecksDrainingAfterStoreRead(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
		beforeList: func() {
			_, _ = authority.DrainAuthority(context.Background(), target)
		},
	}
	authority.store = store
	authority.markActive(target)
	_, err := authority.ListUserConversationActiveViewForTarget(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveViewForTarget() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityUnscopedListRechecksDrainingAfterStoreRead(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
		beforeList: func() {
			_, _ = authority.DrainAuthority(context.Background(), target)
		},
	}
	authority.store = store
	authority.markActive(target)
	_, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveView() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityUnscopedListRejectsSecondActiveAfterStoreRead(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
		beforeList: func() {
			authority.markActive(targetB)
		},
	}
	authority.store = store
	authority.markActive(targetA)
	_, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("ListUserConversationActiveView() error = %v, want ErrStaleRoute", err)
	}
}

func TestConversationAuthorityDrainFlushesOnlyTargetRows(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(targetA)
	authority.markActive(targetB)
	if err := authority.AdmitPatches(context.Background(), targetA, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 30,
	}}); err != nil {
		t.Fatalf("AdmitPatches(targetA) error = %v", err)
	}
	if err := authority.AdmitPatches(context.Background(), targetB, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
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
		t.Fatalf("touched = %#v, want only target A row flushed", store.touched)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), targetB, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget(targetB) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "b" {
		t.Fatalf("target B rows = %#v, want target B cache row still visible", page.Rows)
	}
}

func TestConversationAuthorityDrainCleanTargetReturnsNoDirty(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	targetA := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	targetB := conversationusecase.RouteTarget{HashSlot: 9, SlotID: 10, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(targetA)
	authority.markActive(targetB)
	if err := authority.AdmitPatches(context.Background(), targetB, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 20,
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
		t.Fatalf("touched = %#v, want no target A flush", store.touched)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), targetB, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget(targetB) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "b" {
		t.Fatalf("target B rows = %#v, want target B cache row still visible", page.Rows)
	}
}

func TestConversationAuthorityDrainUnknownTargetDoesNotPoisonActiveList(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	active := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	unknown := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 5, AuthorityEpoch: 6}
	authority.markActive(active)
	result, err := authority.DrainAuthority(context.Background(), unknown)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("DrainAuthority(unknown) error = %v, want ErrStaleRoute", err)
	}
	if result != conversationDrainResultNoDirty {
		t.Fatalf("DrainAuthority(unknown) result = %q, want %q", result, conversationDrainResultNoDirty)
	}
	page, err := authority.ListUserConversationActiveViewForTarget(context.Background(), active, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveViewForTarget(active) error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "db" {
		t.Fatalf("rows = %#v, want active target unaffected", page.Rows)
	}
}

func TestConversationAuthorityStoreFakeHonorsCursorLimitAndOrdering(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{
			{UID: "u2", ChannelID: "other", ChannelType: 2, ActiveAt: 900},
			{UID: "u1", ChannelID: "c", ChannelType: 2, ActiveAt: 100},
			{UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAt: 300},
			{UID: "u1", ChannelID: "b", ChannelType: 1, ActiveAt: 200},
			{UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAt: 200},
		},
	}
	first, cursor, done, err := store.ListUserConversationActivePage(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 2)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage(first) error = %v", err)
	}
	if done || cursor != (metadb.UserConversationActiveCursor{ActiveAt: 200, ChannelID: "b", ChannelType: 1}) || conversationAuthorityChannelIDs(first) != "a,b" {
		t.Fatalf("first rows=%#v cursor=%+v done=%v, want first ordered page", first, cursor, done)
	}
	second, cursor, done, err := store.ListUserConversationActivePage(context.Background(), "u1", cursor, 2)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage(second) error = %v", err)
	}
	if !done || cursor != (metadb.UserConversationActiveCursor{ActiveAt: 100, ChannelID: "c", ChannelType: 2}) || conversationAuthorityChannelIDs(second) != "b,c" {
		t.Fatalf("second rows=%#v cursor=%+v done=%v, want second ordered page", second, cursor, done)
	}
}

type recordingConversationAuthorityStore struct {
	activeRows []metadb.UserConversationState
	primary    map[metadb.ConversationKey]metadb.UserConversationState
	touched    []metadb.UserConversationActivePatch
	beforeList func()
}

func (s *recordingConversationAuthorityStore) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	if s.beforeList != nil {
		s.beforeList()
	}
	rows := append([]metadb.UserConversationState(nil), s.activeRows...)
	sortConversationRows(rows)
	candidates := make([]metadb.UserConversationState, 0, len(rows))
	for _, row := range rows {
		if row.UID != uid || !conversationRowAfter(row, after) {
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
		cursor = metadb.UserConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return candidates, cursor, done, nil
}

func (s *recordingConversationAuthorityStore) GetUserConversationState(_ context.Context, _ string, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	if s.primary == nil {
		return metadb.UserConversationState{}, false, nil
	}
	row, ok := s.primary[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	return row, ok, nil
}

func (s *recordingConversationAuthorityStore) TouchUserConversationActiveAtBatch(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.touched = append(s.touched, patches...)
	return nil
}

func conversationAuthorityChannelIDs(rows []metadb.UserConversationState) string {
	var out string
	for i, row := range rows {
		if i > 0 {
			out += ","
		}
		out += row.ChannelID
	}
	return out
}
