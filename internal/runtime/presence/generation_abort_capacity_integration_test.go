//go:build integration

package presence_test

import (
	"fmt"
	"testing"
	"time"

	p "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// A failed unpublished scan must release its rows, but cancellation cannot
// erase a published generation, even after revocation, or its successor.
func TestGenerationAbortRetiresOnlyUnpublishedRows(t *testing.T) {
	t.Run("overflow_closed_scan", func(t *testing.T) {
		d, c, _ := overflowClosedGeneration(t, false)
		err := d.AbortGeneration(c)
		got := d.Snapshot()
		if err != nil || got.Active != 0 || got.ExpiryIndexRoutes != 0 {
			t.Fatalf("exact failed scan retained unpublished backing: abort=%v active=%d expiry=%d", err, got.Active, got.ExpiryIndexRoutes)
		}
		if _, err := d.EndpointsByGeneration(c, []string{"never-observed"}); err == nil {
			t.Fatal("aborted scan manufactured an empty proof")
		}
	})
	t.Run("complete", func(t *testing.T) {
		d, c, o := generationDirectory(t)
		t.Cleanup(func() { d.LoseAuthority(c.Target.HashSlot) })
		finishGeneration(t, d, c, o, generationRoute(1))
		if err := d.AbortGeneration(c); err == nil {
			t.Fatal("late Abort accepted a published generation")
		}
		if rows, err := d.EndpointsByGeneration(c, []string{"user"}); err != nil || len(rows) != 1 {
			t.Fatalf("late Abort erased Complete: rows=%v err=%v", rows, err)
		}
	})
	t.Run("overflow_revoked_complete", func(t *testing.T) {
		d, c, _ := overflowClosedGeneration(t, true)
		err := d.AbortGeneration(c)
		got := d.Snapshot()
		if err == nil || got.Active != 3 || got.ExpiryIndexRoutes != 3 {
			t.Fatalf("published revoked rows treated as staging: abort=%v active=%d expiry=%d", err, got.Active, got.ExpiryIndexRoutes)
		}
	})
	t.Run("successor", func(t *testing.T) {
		d, old, o := overflowClosedGeneration(t, false)
		next := old
		next.PreviousGeneration = old.Generation
		next.Generation++
		next.AppliedRaftIndex++
		o.Cut++
		if err := d.BeginGeneration(next, []p.OwnerCut{o}); err != nil {
			t.Fatal(err)
		}
		finishGeneration(t, d, next, o, generationRoute(100))
		if err := d.AbortGeneration(old); err == nil {
			t.Fatal("old Abort accepted a successor generation")
		}
		if rows, err := d.EndpointsByGeneration(next, []string{"user"}); err != nil || len(rows) != 1 || rows[0].SessionID != 100 {
			t.Fatalf("old Abort erased successor: rows=%v err=%v", rows, err)
		}
	})
}

// Fill the node-wide uncertainty budget through another Complete slot, then
// overflow it while the target still owns three unexpired routes.
func overflowClosedGeneration(t *testing.T, complete bool) (*p.Directory, p.GenerationClaim, p.OwnerCut) {
	t.Helper()
	d, c, o := generationDirectory(t)
	other := c
	other.Target.HashSlot = 38
	d.BecomeAuthority(other.Target)
	t.Cleanup(func() {
		d.LoseAuthority(c.Target.HashSlot)
		d.LoseAuthority(other.Target.HashSlot)
		if got := d.Snapshot(); got.Active != 0 || got.ExpiryIndexRoutes != 0 {
			t.Fatalf("authority cleanup retained rows: %+v", got)
		}
	})
	if err := d.BeginGeneration(other, []p.OwnerCut{o}); err != nil {
		t.Fatal(err)
	}
	for page := uint64(1); page <= 2; page++ {
		rows := make([]p.Route, 4096)
		for i := range rows {
			id := (page-1)*4096 + uint64(i) + 1
			rows[i] = generationRoute(id)
			rows[i].UID = fmt.Sprintf("other-%d", id)
		}
		if err := d.InstallSlotPage(p.SlotPage{Claim: other, Owner: o, Sequence: page, AfterSessionID: (page - 1) * 4096, NextSessionID: page * 4096, RowsTotal: page * 4096, Exhausted: page == 2, Routes: rows}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.EndGenerationOwner(p.SlotEnd{Claim: other, Owner: o, Sequence: 2, LastSessionID: 8192, RowsTotal: 8192}); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteGeneration(other); err != nil {
		t.Fatal(err)
	}
	if n := d.ExpireRoutes(time.Unix(151, 0), 50*time.Second); n != 8192 {
		t.Fatalf("other slot expired=%d", n)
	}
	rows := make([]p.Route, 4)
	for i := range rows {
		rows[i] = generationRoute(uint64(i + 1))
		rows[i].UID = fmt.Sprintf("target-%d", i)
		if i != 0 {
			rows[i].LastSeenUnix = 500
		}
	}
	page := generationPage(c, o, rows...)
	page.Exhausted = complete
	if err := d.InstallSlotPage(page); err != nil {
		t.Fatal(err)
	}
	if complete {
		if err := d.EndGenerationOwner(p.SlotEnd{Claim: c, Owner: o, Sequence: 1, LastSessionID: 4, RowsTotal: 4}); err != nil {
			t.Fatal(err)
		}
		if err := d.CompleteGeneration(c); err != nil {
			t.Fatal(err)
		}
	}
	if n := d.ExpireRoutes(time.Unix(151, 0), 50*time.Second); n != 1 {
		t.Fatalf("target expired=%d", n)
	}
	if _, _, err := d.CurrentGeneration(c.Target); err == nil {
		t.Fatal("overflow left target generation open")
	}
	if got := d.Snapshot(); got.Active != 3 || got.ExpiryIndexRoutes != 3 {
		t.Fatalf("fixture lost unexpired target rows: %+v", got)
	}
	return d, c, o
}
