package online_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	presence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

func generationClaim(g uint64) presence.GenerationClaim {
	return presence.GenerationClaim{Target: presence.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 9, LeaderTerm: 4, ConfigEpoch: 2}, OwnerSetEpoch: 11, ServingBoot: 19, Generation: g, PreviousGeneration: g - 1, AppliedRaftIndex: g}
}
func ownerSession(id uint64) online.LocalSession {
	return online.LocalSession{Route: online.OwnerRoute{UID: "u", HashSlot: 37, OwnerNodeID: 7, OwnerBootID: 17, OwnerSeq: id, SessionID: id, DeviceID: "device", ConnectedUnix: 10, LastActivityUnix: 20}}
}
func mustPending(t *testing.T, r *online.Registry, id uint64) {
	t.Helper()
	if err := r.RegisterPending(ownerSession(id)); err != nil {
		t.Fatal(err)
	}
}
func receipt(claim presence.GenerationClaim, cut presence.OwnerCut, id uint64) presence.ActivationReceipt {
	return presence.ActivationReceipt{Claim: claim, Owner: cut, Identity: presence.RouteIdentity{UID: "u", OwnerNodeID: 7, OwnerBootID: 17, SessionID: id}, OwnerSeq: id}
}
func pageRequest(claim presence.GenerationClaim, cut presence.OwnerCut) presence.SlotPageRequest {
	return presence.SlotPageRequest{Claim: claim, Owner: cut, Sequence: 1, MaxRows: 1, MaxBytes: 4096}
}

func TestSlotBarrierOrdersActivationWithoutWaitingForPending(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	mustPending(t, r, 1)
	if err := r.MarkActive(1); err != nil {
		t.Fatal(err)
	}
	mustPending(t, r, 2)
	claim := generationClaim(100)
	cut, err := r.BeginSlot(claim, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.BeginSlot(claim, 7, 17)
	if err != nil || again != cut {
		t.Fatalf("non-idempotent Begin: %+v %v", again, err)
	}
	if err = r.MarkActive(2); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatalf("legacy activation after barrier: %v", err)
	}
	if err = r.MarkActiveWithReceipt(2, receipt(claim, cut, 2)); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatalf("activation before End: %v", err)
	}
	old := receipt(generationClaim(99), cut, 2)
	if err = r.MarkActiveWithReceipt(2, old); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatalf("old activation after barrier: %v", err)
	}
	page, err := r.ReadSlotPage(pageRequest(claim, cut))
	if err != nil {
		t.Fatal(err)
	}
	defer page.Release()
	if len(page.Page.Routes) != 1 || page.Page.Routes[0].SessionID != 1 || !page.Page.Exhausted {
		t.Fatalf("active before Begin absent or pending included: %+v", page.Page)
	}
	end := presence.SlotEndRequest{Claim: claim, Owner: cut, Sequence: page.Page.Sequence, LastSessionID: page.Page.NextSessionID, RowsTotal: page.Page.RowsTotal}
	if _, err = r.EndSlot(end); err != nil {
		t.Fatal(err)
	}
	if err = r.MarkActiveWithReceipt(2, receipt(claim, cut, 2)); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.LocalSession(2); !ok || got.State != online.RouteStateActive {
		t.Fatalf("current receipt not active: %+v", got)
	}
	next, err := r.BeginSlot(generationClaim(101), 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	if next.Cut <= cut.Cut {
		t.Fatal("new generation did not advance Cut")
	}
	if err = r.AbortSlot(claim, cut); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatalf("stale Abort affected successor: %v", err)
	}
}

func TestSlotPageReplayAndCursorPreserveOriginalActivity(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	for _, id := range []uint64{3, 1, 2} {
		mustPending(t, r, id)
		if err := r.MarkActive(id); err != nil {
			t.Fatal(err)
		}
	}
	claim := generationClaim(100)
	cut, err := r.BeginSlot(claim, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	req := pageRequest(claim, cut)
	first, err := r.ReadSlotPage(req)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if first.Page.Routes[0].SessionID != 1 || first.Page.Exhausted {
		t.Fatalf("unordered page: %+v", first.Page)
	}
	r.MarkTouched(1, 90)
	r.MarkClosingAndUnregister(2)
	repeat, err := r.ReadSlotPage(req)
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.Release()
	if !reflect.DeepEqual(first.Page, repeat.Page) || repeat.Page.Routes[0].LastSeenUnix != 20 {
		t.Fatal("same page changed or refreshed original activity")
	}
	wrong := req
	wrong.Sequence = 2
	wrong.AfterSessionID = 2
	if _, err = r.ReadSlotPage(wrong); err == nil {
		t.Fatal("accepted skipped cursor")
	}
	req.Sequence = 2
	req.AfterSessionID = first.Page.NextSessionID
	last, err := r.ReadSlotPage(req)
	if err != nil {
		t.Fatal(err)
	}
	defer last.Release()
	if len(last.Page.Routes) != 1 || last.Page.Routes[0].SessionID != 3 || !last.Page.Exhausted || last.Page.RowsTotal != 2 {
		t.Fatalf("close/paging result: %+v", last.Page)
	}
	if _, err = r.ReadSlotPage(pageRequest(claim, cut)); err == nil {
		t.Fatal("reconstructed already retired older page")
	}
	end := presence.SlotEndRequest{Claim: claim, Owner: cut, Sequence: 2, LastSessionID: 3, RowsTotal: 2}
	bad := end
	bad.RowsTotal++
	if _, err = r.EndSlot(bad); err == nil {
		t.Fatal("accepted forged End")
	}
	if _, err = r.EndSlot(end); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadSlotPage(req); err == nil {
		t.Fatal("page after End accepted")
	}
}

func TestSlotPagesChargeRetainedReferencesAcrossAbort(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{SlotPageMaxRows: 1, SlotPageMaxBytes: 4096})
	for _, id := range []uint64{1, 2} {
		mustPending(t, r, id)
		if err := r.MarkActive(id); err != nil {
			t.Fatal(err)
		}
	}
	claim := generationClaim(100)
	cut, err := r.BeginSlot(claim, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	req := pageRequest(claim, cut)
	first, err := r.ReadSlotPage(req)
	if err != nil {
		t.Fatal(err)
	}
	if stats := r.SlotPageBudget(); stats.Rows != 1 || stats.Bytes == 0 {
		t.Fatalf("unaccounted reply: %+v", stats)
	}
	req.Sequence = 2
	req.AfterSessionID = 1
	if _, err = r.ReadSlotPage(req); !errors.Is(err, online.ErrSlotPageCapacity) {
		t.Fatalf("retained old reply overbooked capacity: %v", err)
	}
	if err = r.AbortSlot(claim, cut); err != nil {
		t.Fatal(err)
	}
	if stats := r.SlotPageBudget(); stats.Rows != 1 {
		t.Fatalf("Abort released live reply credit: %+v", stats)
	}
	first.Release()
	first.Release()
	if stats := r.SlotPageBudget(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("reply release leaked: %+v", stats)
	}
	if _, err = r.BeginSlot(claim, 7, 17); err == nil {
		t.Fatal("aborted generation restarted without greater G")
	}
	cut, err = r.BeginSlot(generationClaim(101), 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	invalid := pageRequest(generationClaim(101), cut)
	invalid.MaxRows = 4097
	if _, err = r.ReadSlotPage(invalid); err == nil {
		t.Fatal("oversized page accepted")
	}
}

func TestSlotReceiptBindsBootIdentityAndGeneration(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	mustPending(t, r, 1)
	claim := generationClaim(100)
	cut, err := r.BeginSlot(claim, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*presence.ActivationReceipt){
		func(x *presence.ActivationReceipt) { x.Owner.BootID++ }, func(x *presence.ActivationReceipt) { x.Claim.Target.LeaderTerm++ },
		func(x *presence.ActivationReceipt) { x.Claim.OwnerSetEpoch++ }, func(x *presence.ActivationReceipt) { x.OwnerSeq++ },
		func(x *presence.ActivationReceipt) { x.Identity.UID = "other" }, func(x *presence.ActivationReceipt) { x.Owner.Cut++ },
	}
	for i, mutate := range mutations {
		bad := receipt(claim, cut, 1)
		mutate(&bad)
		if err = r.MarkActiveWithReceipt(1, bad); !errors.Is(err, online.ErrStaleActivation) {
			t.Fatalf("mutation %d admitted: %v", i, err)
		}
	}
	sameG := claim
	sameG.ServingBoot++
	if _, err = r.BeginSlot(sameG, 7, 17); err == nil {
		t.Fatal("same G accepted a different claim")
	}
	if _, err = r.BeginSlot(generationClaim(99), 7, 17); err == nil {
		t.Fatal("older G superseded owner gate")
	}
	fresh := online.NewRegistry(online.RegistryOptions{})
	newSession := ownerSession(1)
	newSession.Route.OwnerBootID = 18
	if err = fresh.RegisterPending(newSession); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.BeginSlot(claim, 7, 18); err != nil {
		t.Fatal(err)
	}
	if err = fresh.MarkActiveWithReceipt(1, receipt(claim, cut, 1)); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatalf("restart accepted old boot: %v", err)
	}
}

func TestSlotIndexRemovesReplacedSessionAndAllowsOtherSlots(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	mustPending(t, r, 1)
	if err := r.MarkActive(1); err != nil {
		t.Fatal(err)
	}
	replacement := ownerSession(1)
	replacement.Route.UID = "replacement"
	replacement.Route.HashSlot = 38
	if err := r.RegisterPending(replacement); err != nil {
		t.Fatal(err)
	}
	claim := generationClaim(100)
	cut, err := r.BeginSlot(claim, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.ReadSlotPage(pageRequest(claim, cut))
	if err != nil {
		t.Fatal(err)
	}
	defer page.Release()
	if len(page.Page.Routes) != 0 || !page.Page.Exhausted {
		t.Fatalf("replacement left stale active index: %+v", page.Page)
	}
	if err = r.MarkActive(1); err != nil {
		t.Fatal("unrelated slot blocked", err)
	}
	if rows, ok := r.ActiveRoutesByUIDs([]string{"u", "replacement"}, 10); !ok || len(rows) != 1 || rows[0].UID != "replacement" {
		t.Fatalf("UID index inconsistent: %+v", rows)
	}
}

func TestSlotCutValidationRetainsEndAndRejectsAbortOrSuccessor(t *testing.T) {
	r := online.NewRegistry(online.RegistryOptions{})
	claim := generationClaim(100)
	cut, err := r.BeginSlot(claim, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateSlotCut(claim, cut); err != nil {
		t.Fatal(err)
	}
	page, err := r.ReadSlotPage(pageRequest(claim, cut))
	if err != nil {
		t.Fatal(err)
	}
	end := presence.SlotEndRequest{Claim: claim, Owner: cut, Sequence: 1, RowsTotal: 0}
	if _, err = r.EndSlot(end); err != nil {
		t.Fatal(err)
	}
	page.Release()
	if err = r.ValidateSlotCut(claim, cut); err != nil {
		t.Fatal("End retired valid activation cut", err)
	}
	next := generationClaim(101)
	nextCut, err := r.BeginSlot(next, 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateSlotCut(claim, cut); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatal("accepted prior cut", err)
	}
	if err = r.AbortSlot(next, nextCut); err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateSlotCut(next, nextCut); !errors.Is(err, online.ErrStaleActivation) {
		t.Fatal("accepted aborted cut", err)
	}
}
