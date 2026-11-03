package presence_test

import (
	"errors"
	"testing"
	"time"

	p "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// Failure matrix precedes implementation: a partial owner/page/End cannot
// prove absence; old G/boot/M or repeated terminal work cannot publish; legacy
// writes cannot bypass Full; receipts come only from actual final success;
// tombstones beat late pages; TTL never converts stale activity to absence.
func generationClaim() p.GenerationClaim {
	return p.GenerationClaim{Target: p.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}, OwnerSetEpoch: 2, ServingBoot: 22, Generation: 1, AppliedRaftIndex: 100}
}
func generationOwner() p.OwnerCut { return p.OwnerCut{NodeID: 1, BootID: 11, Cut: 1} }
func generationRoute(id uint64) p.Route {
	return p.Route{UID: "user", OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: id, SessionID: id, DeviceID: "device", DeviceFlag: 1, DeviceLevel: 1, LastSeenUnix: 100}
}
func generationDirectory(t *testing.T) (*p.Directory, p.GenerationClaim, p.OwnerCut) {
	t.Helper()
	d := p.NewDirectory(p.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	c, o := generationClaim(), generationOwner()
	d.BecomeAuthority(c.Target)
	if err := d.BeginGeneration(c, []p.OwnerCut{o}); err != nil {
		t.Fatal(err)
	}
	return d, c, o
}
func generationPage(c p.GenerationClaim, o p.OwnerCut, rows ...p.Route) p.SlotPage {
	next := uint64(0)
	if len(rows) > 0 {
		next = rows[len(rows)-1].SessionID
	}
	return p.SlotPage{Claim: c, Owner: o, Sequence: 1, NextSessionID: next, RowsTotal: uint64(len(rows)), Exhausted: true, Routes: rows}
}
func finishGeneration(t *testing.T, d *p.Directory, c p.GenerationClaim, o p.OwnerCut, rows ...p.Route) {
	t.Helper()
	page := generationPage(c, o, rows...)
	if err := d.InstallSlotPage(page); err != nil {
		t.Fatal(err)
	}
	end := p.SlotEnd{Claim: c, Owner: o, Sequence: page.Sequence, LastSessionID: page.NextSessionID, RowsTotal: page.RowsTotal}
	if err := d.EndGenerationOwner(end); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteGeneration(c); err != nil {
		t.Fatal(err)
	}
}
func TestGenerationCannotPublishPartialOrLegacyProof(t *testing.T) {
	d, c, o := generationDirectory(t)
	if err := d.CompleteGeneration(c); err == nil {
		t.Fatal("missing pages/End accepted")
	}
	if err := d.InstallRecoveredRoutes(c.Target, []string{"missing"}, nil); err == nil {
		t.Fatal("legacy UID page manufactured Full proof")
	}
	if _, err := d.RegisterRoute(c.Target, generationRoute(1)); err == nil {
		t.Fatal("legacy register bypassed activation barrier")
	}
	if _, err := d.EndpointsByUID(c.Target, "missing"); err == nil {
		t.Fatal("partial snapshot proved offline")
	}
	page := generationPage(c, o)
	page.Exhausted = false
	if err := d.InstallSlotPage(page); err == nil {
		t.Fatal("nonprogressing nonterminal page accepted")
	}
	finishGeneration(t, d, c, o)
	if routes, err := d.EndpointsByGeneration(c, []string{"never-queried"}); err != nil || len(routes) != 0 {
		t.Fatalf("completed slot cannot prove unseen UID: %v %v", routes, err)
	}
}
func TestGenerationReceiptComesFromFinalCommit(t *testing.T) {
	d, c, o := generationDirectory(t)
	finishGeneration(t, d, c, o, generationRoute(1))
	result, err := d.RegisterRouteWithReceipt(c, generationRoute(2))
	if err != nil {
		t.Fatal(err)
	}
	if result.PendingToken == "" || result.Receipt != nil {
		t.Fatalf("pending candidate got activation receipt: %+v", result)
	}
	if result.PendingClaim == nil || !result.PendingClaim.Equal(c) {
		t.Fatal("pending response lost actual authority claim")
	}
	receipt, err := d.CommitRouteWithReceipt(c, result.PendingToken)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Valid() || receipt.Identity != generationRoute(2).Identity() || receipt.Owner != o || !receipt.Claim.Equal(c) {
		t.Fatalf("wrong final receipt: %+v", receipt)
	}
	if err := d.CommitRoute(c.Target, result.PendingToken); err == nil {
		t.Fatal("legacy commit admitted Full slot")
	}
}
func TestGenerationRejectsABAAndModifiedClaims(t *testing.T) {
	d, c, o := generationDirectory(t)
	finishGeneration(t, d, c, o)
	for _, change := range []func(*p.GenerationClaim){func(x *p.GenerationClaim) { x.OwnerSetEpoch++ }, func(x *p.GenerationClaim) { x.ServingBoot++ }, func(x *p.GenerationClaim) { x.AppliedRaftIndex++ }} {
		stale := c
		change(&stale)
		if _, err := d.EndpointsByGeneration(stale, nil); err == nil {
			t.Fatal("modified same-G claim accepted")
		}
	}
	d.LoseAuthority(37)
	d.BecomeAuthority(c.Target)
	if err := d.BeginGeneration(c, []p.OwnerCut{o}); err == nil {
		t.Fatal("same-generation ABA accepted")
	}
	c.PreviousGeneration = c.Generation
	c.Generation++
	c.AppliedRaftIndex++
	o.Cut++
	if err := d.BeginGeneration(c, []p.OwnerCut{o}); err != nil {
		t.Fatal(err)
	}
	if err := d.AbortGeneration(c); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteGeneration(c); err == nil {
		t.Fatal("aborted generation published")
	}
	if err := d.BeginGeneration(c, []p.OwnerCut{o}); err == nil {
		t.Fatal("aborted generation silently restarted")
	}
}
func TestGenerationPageOrderTombstoneAndOwnerBoot(t *testing.T) {
	d, c, o := generationDirectory(t)
	route := generationRoute(1)
	if err := d.UnregisterRoute(c.Target, route.Identity(), route.OwnerSeq); err != nil {
		t.Fatal(err)
	}
	bad := generationPage(c, o, route)
	bad.Owner.BootID++
	if err := d.InstallSlotPage(bad); err == nil {
		t.Fatal("wrong boot page accepted")
	}
	bad = generationPage(c, o, route)
	bad.Sequence = 2
	if err := d.InstallSlotPage(bad); err == nil {
		t.Fatal("skipped page accepted")
	}
	finishGeneration(t, d, c, o, route)
	if rows, err := d.EndpointsByGeneration(c, []string{"user"}); err != nil || len(rows) != 0 {
		t.Fatalf("late page revived tombstone: %v %v", rows, err)
	}
	if err := d.InstallSlotPage(generationPage(c, o, route)); err == nil {
		t.Fatal("page after publication accepted")
	}
}
func TestGenerationTTLRequiresRealOwnerEvidence(t *testing.T) {
	d, c, o := generationDirectory(t)
	route := generationRoute(1)
	finishGeneration(t, d, c, o, route)
	if n := d.ExpireRoutes(time.Unix(151, 0), 50*time.Second); n != 1 {
		t.Fatalf("expired %d", n)
	}
	if _, err := d.EndpointsByGeneration(c, []string{"user"}); !errors.Is(err, p.ErrRouteNotReady) {
		t.Fatalf("expired known identity became offline: %v", err)
	}
	if rows, err := d.EndpointsByGeneration(c, []string{"unrelated"}); err != nil || len(rows) != 0 {
		t.Fatalf("one suspect blocked unrelated UID: %v %v", rows, err)
	}
	if err := d.ReconcileSuspect(c, []string{"user"}, []p.Route{route}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.EndpointsByGeneration(c, []string{"user"}); !errors.Is(err, p.ErrRouteNotReady) {
		t.Fatalf("stale activity renewed by recheck: %v", err)
	}
	route.LastSeenUnix = 200
	if err := d.ReconcileSuspect(c, []string{"user"}, []p.Route{route}); err != nil {
		t.Fatal(err)
	}
	if rows, err := d.EndpointsByGeneration(c, []string{"user"}); err != nil || len(rows) != 1 || rows[0].LastSeenUnix != 200 {
		t.Fatalf("fresh activity not restored: %v %v", rows, err)
	}
	d.ExpireRoutes(time.Unix(251, 0), 50*time.Second)
	if err := d.ReconcileSuspect(c, []string{"user"}, nil); err != nil {
		t.Fatal(err)
	}
	if rows, err := d.EndpointsByGeneration(c, []string{"user"}); err != nil || len(rows) != 0 {
		t.Fatalf("all-owner absence cannot clear suspect: %v %v", rows, err)
	}
}

// Failure-first: a valid new owner/boot cannot register under an obsolete
// Complete vector. Rejection must atomically revoke the absence certificate,
// whereas malformed input or an old claim must not revoke a current generation.
func TestGenerationOwnerVectorChangeRevokesCompleteness(t *testing.T) {
	for _, kind := range []string{"boot", "new-owner"} {
		t.Run(kind, func(t *testing.T) {
			d, c, o := generationDirectory(t)
			finishGeneration(t, d, c, o)
			route := generationRoute(2)
			if kind == "boot" {
				route.OwnerBootID++
			} else {
				route.OwnerNodeID++
			}
			if _, err := d.RegisterRouteWithReceipt(c, route); err == nil {
				t.Fatal("mismatched owner vector admitted registration")
			}
			if rows, err := d.EndpointsByGeneration(c, []string{"unseen"}); err == nil {
				t.Fatalf("owner vector rejection left stale Complete negative proof: %+v", rows)
			}
			if err := d.BeginGeneration(c, []p.OwnerCut{o}); err == nil {
				t.Fatal("revoked generation was reused")
			}
		})
	}
	t.Run("malformed-is-not-evidence", func(t *testing.T) {
		d, c, o := generationDirectory(t)
		finishGeneration(t, d, c, o)
		route := generationRoute(2)
		route.OwnerNodeID++
		route.OwnerSeq = 0
		if _, err := d.RegisterRouteWithReceipt(c, route); err == nil {
			t.Fatal("malformed route admitted")
		}
		if _, err := d.EndpointsByGeneration(c, []string{"unseen"}); err != nil {
			t.Fatalf("malformed route revoked current certificate: %v", err)
		}
	})
}
