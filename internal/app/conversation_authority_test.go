package app

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var _ accessnode.ConversationAuthority = (*conversationAuthority)(nil)
var _ accessnode.ConversationBatchAuthority = (*conversationAuthority)(nil)

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

func TestConversationAuthorityDrainWaitsForReservedSingleAdmission(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 10})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	authority.markActive(target)

	mutationReady := make(chan struct{})
	releaseMutation := make(chan struct{})
	authority.beforeActiveMutation = func() {
		close(mutationReady)
		<-releaseMutation
	}
	admitDone := make(chan error, 1)
	go func() {
		admitDone <- authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
			Kind:        metadb.ConversationKindNormal,
			SenderUID:   "alice",
			ChannelID:   "reserved-single",
			ChannelType: 2,
			MessageSeq:  7,
			ActiveAtMS:  100,
		})
	}()
	<-mutationReady
	authority.mu.Lock()
	reserved, hasReservation := authority.admissions[targetKey(target)]
	inFlight := 0
	if hasReservation {
		inFlight = reserved.inFlight
	}
	authority.mu.Unlock()
	if inFlight != 1 {
		close(releaseMutation)
		<-admitDone
		t.Fatalf("single-target reservations = %d, want 1", inFlight)
	}

	type drainOutcome struct {
		result conversationDrainResult
		err    error
	}
	drainDone := make(chan drainOutcome, 1)
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	go func() {
		result, err := authority.DrainAuthority(drainCtx, target)
		drainDone <- drainOutcome{result: result, err: err}
	}()

	select {
	case outcome := <-drainDone:
		close(releaseMutation)
		<-admitDone
		t.Fatalf("DrainAuthority() completed before reserved single mutation: result=%q err=%v", outcome.result, outcome.err)
	case <-time.After(20 * time.Millisecond):
		close(releaseMutation)
	}
	if err := <-admitDone; err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	outcome := <-drainDone
	if outcome.err != nil || outcome.result != conversationDrainResultDrained {
		t.Fatalf("DrainAuthority() = %q, %v, want drained after admission", outcome.result, outcome.err)
	}
	if len(store.touched) != 1 || store.touched[0].ChannelID != "reserved-single" {
		t.Fatalf("durable touches = %#v, want admitted row drained before handoff", store.touched)
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "alice", "reserved-single", 2); ok {
		t.Fatal("reserved single admission remained cached after drain")
	}
}

func TestConversationAuthorityAdmissionObserverCanReenterList(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	observer := &reentrantConversationAdmissionObserver{
		target: target,
		uid:    "alice",
		done:   make(chan error, 1),
	}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		MaxRows:     10,
		Observer:    observer,
	})
	observer.authority = authority
	authority.markActive(target)

	admitDone := make(chan error, 1)
	go func() {
		admitDone <- authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
			Kind:        metadb.ConversationKindNormal,
			SenderUID:   "alice",
			ChannelID:   "observer-reentry",
			ChannelType: 2,
			MessageSeq:  7,
			ActiveAtMS:  100,
		})
	}()
	select {
	case err := <-admitDone:
		if err != nil {
			t.Fatalf("AdmitActiveBatch() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("AdmitActiveBatch() did not return; runtime observer likely reentered while authority lock was held")
	}
	select {
	case err := <-observer.done:
		if err != nil {
			t.Fatalf("runtime observer reentrant list error = %v", err)
		}
	default:
		t.Fatal("runtime admission observer did not reenter list")
	}
}

func TestConversationAuthorityDrainWaitsForReservedPatchAdmission(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 10})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	authority.markActive(target)
	mutationReady := make(chan struct{})
	releaseMutation := make(chan struct{})
	authority.beforeActiveMutation = func() {
		close(mutationReady)
		<-releaseMutation
	}
	admitDone := make(chan error, 1)
	go func() {
		admitDone <- authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
			UID: "alice", Kind: metadb.ConversationKindNormal, ChannelID: "reserved-patch", ChannelType: 2, ActiveAt: 100, MessageSeq: 7,
		}})
	}()
	<-mutationReady

	type drainOutcome struct {
		result conversationDrainResult
		err    error
	}
	drainDone := make(chan drainOutcome, 1)
	go func() {
		result, err := authority.DrainAuthority(context.Background(), target)
		drainDone <- drainOutcome{result: result, err: err}
	}()
	select {
	case outcome := <-drainDone:
		close(releaseMutation)
		<-admitDone
		t.Fatalf("DrainAuthority() completed before reserved patch mutation: result=%q err=%v", outcome.result, outcome.err)
	case <-time.After(20 * time.Millisecond):
		close(releaseMutation)
	}
	if err := <-admitDone; err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	outcome := <-drainDone
	if outcome.err != nil || outcome.result != conversationDrainResultDrained {
		t.Fatalf("DrainAuthority() = %q, %v, want drained after patch admission", outcome.result, outcome.err)
	}
	if len(store.touched) != 1 || store.touched[0].ChannelID != "reserved-patch" {
		t.Fatalf("durable touches = %#v, want patch drained before handoff", store.touched)
	}
}

func TestConversationAuthorityDrainReservationWaitHonorsContext(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRows: 10})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	authority.markActive(target)
	mutationReady := make(chan struct{})
	releaseMutation := make(chan struct{})
	authority.beforeActiveMutation = func() {
		close(mutationReady)
		<-releaseMutation
	}
	admitDone := make(chan error, 1)
	go func() {
		admitDone <- authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
			UID: "alice", Kind: metadb.ConversationKindNormal, ChannelID: "bounded-wait", ChannelType: 2, ActiveAt: 100, MessageSeq: 7,
		}})
	}()
	<-mutationReady

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 20*time.Millisecond)
	result, err := authority.DrainAuthority(drainCtx, target)
	cancelDrain()
	if !errors.Is(err, context.DeadlineExceeded) || result != conversationDrainResultBusy {
		close(releaseMutation)
		<-admitDone
		t.Fatalf("DrainAuthority() = %q, %v, want busy deadline while reservation remains", result, err)
	}
	close(releaseMutation)
	if err := <-admitDone; err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if result, err := authority.finishDrainingAuthority(context.Background(), target); err != nil || result != conversationDrainResultDrained {
		t.Fatalf("finishDrainingAuthority() = %q, %v, want later bounded drain", result, err)
	}
}

func TestConversationAuthorityOldReservationDoesNotBlockNewExactTargetSameHashSlot(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRows: 10})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	newTarget := oldTarget
	newTarget.LeaderTerm++
	authority.markActive(oldTarget)
	mutationReady := make(chan struct{})
	releaseMutation := make(chan struct{})
	authority.beforeActiveMutation = func() {
		close(mutationReady)
		<-releaseMutation
	}
	admitDone := make(chan error, 1)
	go func() {
		admitDone <- authority.AdmitActiveBatch(context.Background(), oldTarget, conversationactive.ActiveBatch{
			Kind: metadb.ConversationKindNormal, SenderUID: "old", ChannelID: "old-tenure", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100,
		})
	}()
	<-mutationReady

	type beginOutcome struct {
		result conversationDrainResult
		err    error
	}
	beginDone := make(chan beginOutcome, 1)
	go func() {
		if _, err := authority.beginDrainAuthority(oldTarget); err != nil {
			beginDone <- beginOutcome{err: err}
			return
		}
		result, err := authority.flushDrainingAuthority(context.Background(), oldTarget)
		beginDone <- beginOutcome{result: result, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		authority.mu.Lock()
		draining := authority.targets[targetKey(oldTarget)] == conversationAuthorityDraining
		authority.mu.Unlock()
		if draining {
			break
		}
		if time.Now().After(deadline) {
			close(releaseMutation)
			<-admitDone
			t.Fatal("old target was not marked draining")
		}
		time.Sleep(time.Millisecond)
	}

	authority.markActive(newTarget)
	reserved, err := authority.reserveAdmissionTarget(newTarget)
	if err != nil {
		close(releaseMutation)
		<-admitDone
		t.Fatalf("reserveAdmissionTarget(newTarget) error = %v", err)
	}
	authority.releaseAdmissionTarget(reserved)
	close(releaseMutation)
	if err := <-admitDone; err != nil {
		t.Fatalf("old AdmitActiveBatch() error = %v", err)
	}
	outcome := <-beginDone
	if outcome.err != nil || outcome.result != conversationDrainResultTransferred {
		t.Fatalf("flushDrainingAuthority(oldTarget) = %q, %v, want transferred after new target publish", outcome.result, outcome.err)
	}
}

func TestConversationAuthorityAdmitActiveBatchRejectsInvalidKind(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
	})
	authority.markActive(target)

	err := authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
		SenderUID:   "u1",
		ChannelID:   "invalid-kind",
		ChannelType: 2,
		MessageSeq:  7,
		ActiveAtMS:  100,
	})
	if err == nil {
		t.Fatal("AdmitActiveBatch() error = nil, want invalid kind rejection")
	}

	page, listErr := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if listErr != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", listErr)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want invalid batch kept out of cache", page.Rows)
	}
}

func TestConversationAuthorityAdmitActiveBatchesKeepsAlignedTargetResults(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		MaxRows:     10,
	})
	first := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	second := conversationusecase.RouteTarget{HashSlot: 2, SlotID: 12, LeaderNodeID: 1, LeaderTerm: 8, ConfigEpoch: 4, RouteRevision: 11, AuthorityEpoch: 21}
	staleSecond := second
	staleSecond.LeaderTerm--
	authority.markActive(first)
	authority.markActive(second)

	results := authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{
		{Target: first, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "alice", ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{Target: staleSecond, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "stale"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{Target: second, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "bob"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
	})
	if len(results) != 3 {
		t.Fatalf("AdmitActiveBatches() result count = %d, want 3", len(results))
	}
	if results[0].Err != nil || !errors.Is(results[1].Err, conversationusecase.ErrStaleRoute) || results[2].Err != nil {
		t.Fatalf("AdmitActiveBatches() results = %+v, want [ok stale_route ok]", results)
	}

	for _, uid := range []string{"alice", "bob"} {
		if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, uid, "room", 2); !ok {
			t.Fatalf("valid sibling %q was not admitted", uid)
		}
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "stale", "room", 2); ok {
		t.Fatal("stale sibling was admitted")
	}
}

func TestConversationAuthorityDrainWaitsForReservedBulkAdmission(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 10})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	authority.markActive(target)

	mutationReady := make(chan struct{})
	releaseMutation := make(chan struct{})
	authority.beforeActiveMutation = func() {
		close(mutationReady)
		<-releaseMutation
	}
	admitDone := make(chan []accessnode.ConversationActiveBatchResult, 1)
	go func() {
		admitDone <- authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{
			{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "alice", ChannelID: "reserved-bulk-a", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
			{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "bob"}}, ChannelID: "reserved-bulk-b", ChannelType: 2, MessageSeq: 8, ActiveAtMS: 110}},
		})
	}()
	<-mutationReady
	authority.mu.Lock()
	reserved, hasReservation := authority.admissions[targetKey(target)]
	inFlight := 0
	if hasReservation {
		inFlight = reserved.inFlight
	}
	authority.mu.Unlock()
	if inFlight != 1 {
		close(releaseMutation)
		<-admitDone
		t.Fatalf("same-target bulk reservations = %d, want 1", inFlight)
	}

	type drainOutcome struct {
		result conversationDrainResult
		err    error
	}
	drainDone := make(chan drainOutcome, 1)
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	go func() {
		result, err := authority.DrainAuthority(drainCtx, target)
		drainDone <- drainOutcome{result: result, err: err}
	}()

	select {
	case outcome := <-drainDone:
		close(releaseMutation)
		<-admitDone
		t.Fatalf("DrainAuthority() completed before reserved bulk mutation: result=%q err=%v", outcome.result, outcome.err)
	case <-time.After(20 * time.Millisecond):
		close(releaseMutation)
	}
	results := <-admitDone
	if len(results) != 2 || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("AdmitActiveBatches() results = %+v, want both admitted", results)
	}
	outcome := <-drainDone
	if outcome.err != nil || outcome.result != conversationDrainResultDrained {
		t.Fatalf("DrainAuthority() = %q, %v, want drained after bulk admission", outcome.result, outcome.err)
	}
	if len(store.touched) != 2 {
		t.Fatalf("durable touches = %#v, want both bulk rows drained before handoff", store.touched)
	}
	for _, row := range []struct {
		uid       string
		channelID string
	}{{uid: "alice", channelID: "reserved-bulk-a"}, {uid: "bob", channelID: "reserved-bulk-b"}} {
		if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, row.uid, row.channelID, 2); ok {
			t.Fatalf("reserved bulk admission %s/%s remained cached after drain", row.uid, row.channelID)
		}
	}
}

func TestConversationAuthorityAdmitActiveBatchesPreservesLazyActivation(t *testing.T) {
	first := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	second := conversationusecase.RouteTarget{HashSlot: 2, SlotID: 12, LeaderNodeID: 1, LeaderTerm: 8, ConfigEpoch: 4, RouteRevision: 11, AuthorityEpoch: 21}
	lookups := 0
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		MaxRows:     10,
		CurrentRouteTarget: func(hashSlot uint16) (conversationusecase.RouteTarget, bool) {
			lookups++
			switch hashSlot {
			case first.HashSlot:
				return first, true
			case second.HashSlot:
				return second, true
			default:
				return conversationusecase.RouteTarget{}, false
			}
		},
	})
	results := authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{
		{Target: first, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "alice", ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{Target: second, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "bob"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
	})
	if len(results) != 2 || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("AdmitActiveBatches() results = %+v, want both lazily active", results)
	}
	if lookups != 2 {
		t.Fatalf("current route lookups = %d, want one per initially stale hash slot", lookups)
	}
}

func TestConversationAuthorityAdmitActiveBatchesRevalidatesSiblingAfterLazyActivation(t *testing.T) {
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	newTarget := oldTarget
	newTarget.LeaderTerm++
	newTarget.RouteRevision++
	newTarget.AuthorityEpoch++
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		MaxRows:     10,
		CurrentRouteTarget: func(hashSlot uint16) (conversationusecase.RouteTarget, bool) {
			if hashSlot != newTarget.HashSlot {
				return conversationusecase.RouteTarget{}, false
			}
			return newTarget, true
		},
	})
	authority.markActive(oldTarget)

	results := authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{
		{Target: oldTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "old", ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{Target: newTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "new"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
	})
	if len(results) != 2 || !errors.Is(results[0].Err, conversationusecase.ErrStaleRoute) || results[1].Err != nil {
		t.Fatalf("AdmitActiveBatches() results = %+v, want [stale_route ok]", results)
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "old", "room", 2); ok {
		t.Fatal("old target sibling was admitted after replacement")
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "new", "room", 2); !ok {
		t.Fatal("current lazily activated sibling was not admitted")
	}
}

func TestConversationAuthorityAdmitActiveBatchesCachePressureIsAtomic(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRows: 1})
	first := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 3}
	second := conversationusecase.RouteTarget{HashSlot: 2, SlotID: 12, LeaderNodeID: 1, LeaderTerm: 8, ConfigEpoch: 4}
	authority.markActive(first)
	authority.markActive(second)
	results := authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{
		{Target: first, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "alice", ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{Target: second, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "bob"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
	})
	if len(results) != 2 || !errors.Is(results[0].Err, conversationusecase.ErrCachePressure) || !errors.Is(results[1].Err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitActiveBatches() results = %+v, want cache pressure for every valid sibling", results)
	}
	for _, uid := range []string{"alice", "bob"} {
		if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, uid, "room", 2); ok {
			t.Fatalf("cache contains %q after atomic pressure rejection", uid)
		}
	}
}

func TestConversationAuthorityAdmitActiveBatchesRejectsCrossHashSlotAddress(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRows: 10})
	first := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 3}
	second := conversationusecase.RouteTarget{HashSlot: 2, SlotID: 12, LeaderNodeID: 1, LeaderTerm: 8, ConfigEpoch: 4}
	authority.markActive(first)
	authority.markActive(second)
	results := authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{
		{Target: first, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "same"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100}},
		{Target: second, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: []conversationactive.ActiveEntry{{UID: "same"}}, ChannelID: "room", ChannelType: 2, MessageSeq: 8, ActiveAtMS: 110}},
	})
	if len(results) != 2 || !errors.Is(results[0].Err, conversationactive.ErrHashSlotConflict) || !errors.Is(results[1].Err, conversationactive.ErrHashSlotConflict) {
		t.Fatalf("AdmitActiveBatches() results = %+v, want hash-slot conflict for both valid siblings", results)
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "same", "room", 2); ok {
		t.Fatal("cache contains conflicted address after atomic rejection")
	}
}

func TestConversationAuthorityAdmitActiveBatchesIgnoresDiagnosticTargetFields(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}, MaxRows: 10})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	authority.markActive(target)
	diagnosticOnly := target
	diagnosticOnly.RouteRevision = 99
	diagnosticOnly.AuthorityEpoch = 100
	results := authority.AdmitActiveBatches(context.Background(), []accessnode.ConversationActiveBatchGroup{{
		Target: diagnosticOnly,
		Batch:  conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, SenderUID: "alice", ChannelID: "room", ChannelType: 2, MessageSeq: 7, ActiveAtMS: 100},
	}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("AdmitActiveBatches() results = %+v, want diagnostic-only differences accepted", results)
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
	if len(store.touched) != 1 || store.touched[0].ReadSeq != 8 || store.touched[0].DeletedToSeq != 0 || store.touched[0].MessageSeq != 9 {
		t.Fatalf("durable patches = %#v, want flushed runtime read floor and message fence", store.touched)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ReadSeq != 8 || page.Rows[0].DeletedToSeq != 0 {
		t.Fatalf("rows = %#v, want DB row to keep runtime read floor after cache flush", page.Rows)
	}
}

func TestConversationAuthorityHideReconcilesCooldownCacheAndAllowsNewerMessage(t *testing.T) {
	const baselineActiveAt int64 = 1_000
	key := metadb.ConversationKey{ChannelID: "room", ChannelType: 2}
	baseline := metadb.ConversationState{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: key.ChannelID, ChannelType: key.ChannelType, ActiveAt: baselineActiveAt}
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.ConversationState{baseline},
		primary:    map[metadb.ConversationKey]metadb.ConversationState{key: baseline},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:    1,
		Store:          store,
		MaxRows:        100,
		ActiveCooldown: 2 * time.Hour,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	authority.markActive(target)
	insideCooldown := baselineActiveAt + int64(time.Hour/time.Millisecond)
	if err := authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
		Kind: metadb.ConversationKindNormal, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType), MessageSeq: 9, ActiveAtMS: insideCooldown,
		Recipients: []conversationactive.ActiveEntry{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch(before hide) error = %v", err)
	}
	if result, err := authority.FlushActiveRows(context.Background(), 0); err != nil || result.Skipped != 1 || result.Cleared != 1 {
		t.Fatalf("FlushActiveRows(before hide) = %+v, %v, want cooldown skip", result, err)
	}

	deleteReq := metadb.ConversationDelete{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: key.ChannelID, ChannelType: key.ChannelType, DeletedToSeq: 9, UpdatedAt: 200}
	if err := authority.HideConversationsForTarget(context.Background(), target, []metadb.ConversationDelete{deleteReq}); err != nil {
		t.Fatalf("HideConversationsForTarget() error = %v", err)
	}
	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget(after hide) error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows after hide = %#v, want hidden conversation", page.Rows)
	}

	newActiveAt := baselineActiveAt + int64(90*time.Minute/time.Millisecond)
	if err := authority.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
		Kind: metadb.ConversationKindNormal, ChannelID: key.ChannelID, ChannelType: uint8(key.ChannelType), MessageSeq: 10, ActiveAtMS: newActiveAt,
		Recipients: []conversationactive.ActiveEntry{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch(after hide) error = %v", err)
	}
	if result, err := authority.FlushActiveRows(context.Background(), 0); err != nil || result.Persisted != 1 || result.Cleared != 1 {
		t.Fatalf("FlushActiveRows(after hide) = %+v, %v, want newer message persisted", result, err)
	}
	state := store.primary[key]
	if state.ActiveAt != newActiveAt || state.DeletedToSeq != 9 {
		t.Fatalf("durable state = %+v, want newer activity above delete barrier", state)
	}
}

func TestConversationAuthorityHideReturnsStaleAfterAuthorityMoves(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 100})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	freshTarget := oldTarget
	freshTarget.LeaderTerm++
	freshTarget.RouteRevision++
	authority.markActive(oldTarget)
	store.beforeHide = func() { authority.markActive(freshTarget) }

	err := authority.HideConversationsForTarget(context.Background(), oldTarget, []metadb.ConversationDelete{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, DeletedToSeq: 9,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("HideConversationsForTarget() error = %v, want ErrStaleRoute", err)
	}
	if len(store.hidden) != 1 {
		t.Fatalf("durable hides = %#v, want committed idempotent barrier before stale retry", store.hidden)
	}
}

func TestConversationAuthorityHideMapsProposalNotLeader(t *testing.T) {
	store := &recordingConversationAuthorityStore{hideErr: propose.ErrNotLeader}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 100})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	authority.markActive(target)

	err := authority.HideConversationsForTarget(context.Background(), target, []metadb.ConversationDelete{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, DeletedToSeq: 9,
	}})
	if !errors.Is(err, conversationusecase.ErrNotLeader) {
		t.Fatalf("HideConversationsForTarget() error = %v, want ErrNotLeader", err)
	}
}

func TestConversationAuthorityHideErrorPreservesUncommittedTail(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 100})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	authority.markActive(target)
	patches := []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "committed-prefix", ChannelType: 2, ActiveAt: 1_000, MessageSeq: 10},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "uncommitted-tail", ChannelType: 2, ActiveAt: 2_000, MessageSeq: 20},
	}
	if err := authority.AdmitPatches(context.Background(), target, patches); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if err := authority.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	prefixKey := metadb.ConversationKey{ChannelID: "committed-prefix", ChannelType: 2}
	tailKey := metadb.ConversationKey{ChannelID: "uncommitted-tail", ChannelType: 2}
	store.beforeHide = func() {
		prefix := store.primary[prefixKey]
		prefix.DeletedToSeq = 10
		prefix.ActiveAt = 0
		store.primary[prefixKey] = prefix
		store.activeRows = []metadb.ConversationState{store.primary[tailKey]}
	}
	store.hideErr = context.DeadlineExceeded
	deletes := []metadb.ConversationDelete{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: prefixKey.ChannelID, ChannelType: prefixKey.ChannelType, DeletedToSeq: 10},
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: tailKey.ChannelID, ChannelType: tailKey.ChannelType, DeletedToSeq: 20},
	}

	err := authority.HideConversationsForTarget(context.Background(), target, deletes)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HideConversationsForTarget() error = %v, want DeadlineExceeded", err)
	}
	for _, channelID := range []string{prefixKey.ChannelID, tailKey.ChannelID} {
		if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", channelID, 2); !ok {
			t.Fatalf("uncertain delete removed cache row %q before durable reconciliation", channelID)
		}
	}
	if got := authority.active.DirtyCountForTest(); got != 2 {
		t.Fatalf("dirty rows after unknown-prefix error = %d, want 2", got)
	}

	page, err := authority.ListConversationActiveViewForTarget(context.Background(), target, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveViewForTarget() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != tailKey.ChannelID {
		t.Fatalf("rows after partial commit = %#v, want only uncommitted tail visible", page.Rows)
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", prefixKey.ChannelID, 2); ok {
		t.Fatal("durably committed prefix survived hydration fence")
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", tailKey.ChannelID, 2); !ok {
		t.Fatal("uncommitted tail disappeared after hydration")
	}
}

func TestConversationAuthorityHideValidatesBeforeLazyActivation(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	routeLookups := 0
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       &recordingConversationAuthorityStore{},
		MaxRows:     100,
		CurrentRouteTarget: func(uint16) (conversationusecase.RouteTarget, bool) {
			routeLookups++
			return target, true
		},
	})

	err := authority.HideConversationsForTarget(context.Background(), target, []metadb.ConversationDelete{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 256, DeletedToSeq: 9,
	}})
	if err == nil {
		t.Fatal("HideConversationsForTarget() error = nil, want invalid channel type")
	}
	if routeLookups != 0 || len(authority.targets) != 0 {
		t.Fatalf("invalid hide routeLookups=%d targets=%#v, want no authority mutation", routeLookups, authority.targets)
	}
}

func TestConversationAuthorityActivationPurgesFormerCleanRows(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRows: 100})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	authority.markActive(oldTarget)
	if err := authority.AdmitActiveBatch(context.Background(), oldTarget, conversationactive.ActiveBatch{
		Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, MessageSeq: 9, ActiveAtMS: 100,
		Recipients: []conversationactive.ActiveEntry{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if _, err := authority.FlushActiveRows(context.Background(), 0); err != nil {
		t.Fatalf("FlushActiveRows() error = %v", err)
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2); !ok {
		t.Fatal("clean cache row missing before authority refresh")
	}

	freshTarget := oldTarget
	freshTarget.LeaderTerm++
	freshTarget.RouteRevision++
	authority.markActive(freshTarget)
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2); ok {
		t.Fatal("former clean cache row survived authority activation")
	}
}

func TestConversationAuthorityActivationObservesPurgeAfterPublishAndUnlock(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	observer := &reentrantConversationActiveCacheObserver{done: make(chan error, 1)}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID: 1,
		Store:       store,
		MaxRows:     100,
		Observer:    observer,
	})
	observer.authority = authority
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 5}
	authority.markActive(oldTarget)
	if err := authority.AdmitActiveBatch(context.Background(), oldTarget, conversationactive.ActiveBatch{
		Kind: metadb.ConversationKindNormal, ChannelID: "room", ChannelType: 2, MessageSeq: 9, ActiveAtMS: 100,
		Recipients: []conversationactive.ActiveEntry{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if _, err := authority.FlushActiveRows(context.Background(), 0); err != nil {
		t.Fatalf("FlushActiveRows() error = %v", err)
	}

	freshTarget := oldTarget
	freshTarget.LeaderTerm++
	freshTarget.RouteRevision++
	observer.target = freshTarget
	observer.reenter = true
	activated := make(chan struct{})
	go func() {
		authority.markActive(freshTarget)
		close(activated)
	}()

	select {
	case <-activated:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("markActive() did not return; cache observer likely ran while authority lock was held")
	}
	select {
	case err := <-observer.done:
		if err != nil {
			t.Fatalf("cache observer saw target before active publish: %v", err)
		}
	default:
		t.Fatal("cache observer did not run after clean-row purge")
	}
	if _, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", "room", 2); ok {
		t.Fatal("former clean cache row survived authority activation")
	}
}

func TestConversationAuthorityCacheOnlyRowRespectsPrimaryDeleteBarrier(t *testing.T) {
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
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want stale cache activity fenced by durable delete barrier", page.Rows)
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
	if len(page.Rows) != 1 || page.Rows[0].ActiveAt != 100 || page.Rows[0].DeletedToSeq != 10 {
		t.Fatalf("rows = %#v, want durable active row without stale cache overlay", page.Rows)
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
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "visible" || page.Rows[0].DeletedToSeq != 0 {
		t.Fatalf("rows = %#v, want stale hidden rows fenced before pagination", page.Rows)
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
	if len(store.touched) != 1 || store.touched[0].MessageSeq != 30 || store.touched[0].SparseActiveSet {
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
	authority.mu.Lock()
	_, retained := authority.targets[targetKey(target)]
	authority.mu.Unlock()
	if retained {
		t.Fatal("successful drain retained its exact target state")
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

func TestConversationAuthorityObsoleteDrainDoesNotPurgeNewTenureCleanRow(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           store,
		ActiveCooldown:  time.Hour,
		MaxRowsPerUID:   10,
		MaxRows:         100,
		ListDBWindowMax: 20,
	})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3, RouteRevision: 3, AuthorityEpoch: 4}
	newTarget := oldTarget
	newTarget.LeaderTerm = 4
	newTarget.RouteRevision = 5
	newTarget.AuthorityEpoch = 6
	authority.markActive(oldTarget)
	if _, err := authority.beginDrainAuthority(oldTarget); err != nil {
		t.Fatalf("beginDrainAuthority(oldTarget) error = %v", err)
	}
	authority.markActive(newTarget)
	if err := authority.AdmitPatches(context.Background(), newTarget, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new-tenure", ChannelType: 2, ActiveAt: 1_000, MessageSeq: 10,
	}}); err != nil {
		t.Fatalf("AdmitPatches(newTarget initial) error = %v", err)
	}
	if err := authority.Flush(context.Background()); err != nil {
		t.Fatalf("Flush(newTarget initial) error = %v", err)
	}
	if err := authority.AdmitPatches(context.Background(), newTarget, []conversationusecase.ActivePatch{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "new-tenure", ChannelType: 2, ActiveAt: 1_100, MessageSeq: 11,
	}}); err != nil {
		t.Fatalf("AdmitPatches(newTarget cooldown) error = %v", err)
	}
	if got := authority.active.DirtyCountForTest(); got != 0 {
		t.Fatalf("new-tenure dirty rows = %d, want cooldown-suppressed clean row", got)
	}

	result, err := authority.finishDrainingAuthority(context.Background(), oldTarget)
	if err != nil {
		t.Fatalf("finishDrainingAuthority(oldTarget) error = %v", err)
	}
	if result != conversationDrainResultTransferred {
		t.Fatalf("finishDrainingAuthority(oldTarget) result = %q, want %q", result, conversationDrainResultTransferred)
	}
	patch, ok := authority.active.EntryForTest(metadb.ConversationKindNormal, "u1", "new-tenure", 2)
	if !ok || patch.MessageSeq != 11 || patch.ActiveAtMS != 1_100 {
		t.Fatalf("new-tenure cache patch = %+v ok=%v, want preserved latest clean view", patch, ok)
	}
	if len(store.touched) != 1 {
		t.Fatalf("durable touches = %#v, want no obsolete-drain touch", store.touched)
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
	beforeHide func()
	hidden     []metadb.ConversationDelete
	hideErr    error
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

func (s *recordingConversationAuthorityStore) HideConversationsBatch(ctx context.Context, deletes []metadb.ConversationDelete) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.beforeHide != nil {
		s.beforeHide()
	}
	s.hidden = append(s.hidden, deletes...)
	if s.hideErr != nil {
		return s.hideErr
	}
	for _, req := range deletes {
		key := metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		state := s.primary[key]
		if state.UID == "" {
			state = metadb.ConversationState{UID: req.UID, Kind: req.Kind, ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		}
		if req.DeletedToSeq <= state.DeletedToSeq {
			continue
		}
		state.DeletedToSeq = req.DeletedToSeq
		state.ActiveAt = 0
		if req.UpdatedAt > state.UpdatedAt {
			state.UpdatedAt = req.UpdatedAt
		}
		if s.primary == nil {
			s.primary = make(map[metadb.ConversationKey]metadb.ConversationState)
		}
		s.primary[key] = state
		filtered := s.activeRows[:0]
		for _, row := range s.activeRows {
			if row.UID == req.UID && row.Kind == req.Kind && row.ChannelID == req.ChannelID && row.ChannelType == req.ChannelType {
				continue
			}
			filtered = append(filtered, row)
		}
		s.activeRows = filtered
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

type reentrantConversationActiveCacheObserver struct {
	recordingConversationAuthorityObserver
	authority *conversationAuthority
	target    conversationusecase.RouteTarget
	reenter   bool
	done      chan error
}

type reentrantConversationAdmissionObserver struct {
	recordingConversationAuthorityObserver
	authority *conversationAuthority
	target    conversationusecase.RouteTarget
	uid       string
	done      chan error
}

func (*reentrantConversationAdmissionObserver) ObserveConversationActiveCache(conversationactive.CacheObservation) {
}

func (o *reentrantConversationAdmissionObserver) ObserveConversationActiveMutation(conversationactive.MutationObservation) {
	_, err := o.authority.ListConversationActiveViewForTarget(context.Background(), o.target, metadb.ConversationKindNormal, o.uid, metadb.ConversationActiveCursor{}, 10)
	o.done <- err
}

func (*reentrantConversationAdmissionObserver) ObserveConversationActiveFlush(conversationactive.FlushObservation) {
}

func (*reentrantConversationAdmissionObserver) ObserveConversationActivePressure(conversationactive.PressureObservation) {
}

func (o *reentrantConversationActiveCacheObserver) ObserveConversationActiveCache(conversationactive.CacheObservation) {
	if !o.reenter {
		return
	}
	o.done <- o.authority.ensureTarget(o.target)
}

func (*reentrantConversationActiveCacheObserver) ObserveConversationActiveMutation(conversationactive.MutationObservation) {
}

func (*reentrantConversationActiveCacheObserver) ObserveConversationActiveFlush(conversationactive.FlushObservation) {
}

func (*reentrantConversationActiveCacheObserver) ObserveConversationActivePressure(conversationactive.PressureObservation) {
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
