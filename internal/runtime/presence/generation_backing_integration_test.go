//go:build integration

package presence

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// All mutations use Directory's public seam. The read-only internal view is
// limited to ownership accounting: no runtime map capacity or heap estimate.
func TestGenerationSuspectBackingRetainsItsReservation(t *testing.T) {
	t.Run("partial_and_empty", func(t *testing.T) {
		d := NewDirectory(DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
		claim, routes := installBackingSuspects(t, d, 37, 8192)
		t.Cleanup(func() { d.LoseAuthority(37) })
		initial := readSuspectBacking(t, d, claim)
		payload := uint64(0)
		for _, route := range routes {
			payload += backingPayloadBytes(route)
		}
		if initial.bytes <= payload || initial.rows != 8192 {
			t.Fatalf("fixture lacks prepaid structure: rows=%d bytes=%d payload=%d", initial.rows, initial.bytes, payload)
		}
		structure := initial.bytes - payload
		for _, route := range routes[:len(routes)-1] {
			if err := d.UnregisterRoute(claim.Target, route.Identity(), route.OwnerSeq); err != nil {
				t.Fatal(err)
			}
		}
		partial := readSuspectBacking(t, d, claim)
		if partial.rows != 1 || partial.suspectsLen != 1 || partial.uidsLen != 1 {
			t.Fatalf("unexpected partial ownership: %+v", partial)
		}
		if sameSuspectTables(initial, partial) && partial.bytes < structure+backingPayloadBytes(routes[len(routes)-1]) {
			t.Errorf("retained partial tables lost their structure reservation: initial_structure=%d retained_bytes=%d", structure, partial.bytes)
		}
		last := routes[len(routes)-1]
		if err := d.UnregisterRoute(claim.Target, last.Identity(), last.OwnerSeq); err != nil {
			t.Fatal(err)
		}
		empty := readSuspectBacking(t, d, claim)
		if empty.rows != 0 || empty.suspectsLen != 0 || empty.uidsLen != 0 {
			t.Fatalf("last close retained identities: %+v", empty)
		}
		if sameSuspectTables(initial, empty) && empty.bytes < structure {
			t.Errorf("retained empty tables lost their structure reservation: initial_structure=%d retained_bytes=%d", structure, empty.bytes)
		}
		if rows, err := d.EndpointsByGeneration(claim, []string{last.UID}); err != nil || len(rows) != 0 {
			t.Fatalf("exact close failed to discharge uncertainty: rows=%v err=%v", rows, err)
		}
		d.LoseAuthority(37)
		if rows, bytes := readSuspectLedger(d); rows != 0 || bytes != 0 {
			t.Fatalf("whole retirement retained ledger: rows=%d bytes=%d", rows, bytes)
		}
	})
	t.Run("cross_slot_and_successor", func(t *testing.T) {
		d := NewDirectory(DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
		first, firstRoutes := installBackingSuspects(t, d, 37, 4096)
		a := readSuspectBacking(t, d, first)
		second, secondRoutes := installBackingSuspects(t, d, 38, 4096)
		b := readSuspectBacking(t, d, second)
		t.Cleanup(func() { d.LoseAuthority(37); d.LoseAuthority(38) })
		payload := uint64(0)
		for _, group := range []struct {
			claim  GenerationClaim
			routes []Route
		}{{first, firstRoutes}, {second, secondRoutes}} {
			for _, route := range group.routes {
				payload += backingPayloadBytes(route)
			}
			for _, route := range group.routes[:len(group.routes)-1] {
				if err := d.UnregisterRoute(group.claim.Target, route.Identity(), route.OwnerSeq); err != nil {
					t.Fatal(err)
				}
			}
		}
		left, right := readSuspectBacking(t, d, first), readSuspectBacking(t, d, second)
		structure := b.bytes - payload
		if sameSuspectTables(a, left) && sameSuspectTables(b, right) && right.bytes < structure {
			t.Errorf("two retained tables only charge live identities: original_structure=%d current_bytes=%d", structure, right.bytes)
		}
		next := first
		next.PreviousGeneration, next.Generation, next.AppliedRaftIndex = first.Generation, first.Generation+1, first.AppliedRaftIndex+1
		owner := OwnerCut{NodeID: 1, BootID: 11, Cut: 2}
		if err := d.BeginGeneration(next, []OwnerCut{owner}); err != nil {
			t.Fatal(err)
		}
		row := Route{UID: "successor", OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: 9000, SessionID: 9000, DeviceID: "device", LastSeenUnix: 100}
		if err := d.InstallSlotPage(SlotPage{Claim: next, Owner: owner, Sequence: 1, NextSessionID: row.SessionID, RowsTotal: 1, Exhausted: true, Routes: []Route{row}}); err != nil {
			t.Fatal(err)
		}
		if err := d.EndGenerationOwner(SlotEnd{Claim: next, Owner: owner, Sequence: 1, LastSessionID: row.SessionID, RowsTotal: 1}); err != nil {
			t.Fatal(err)
		}
		if err := d.CompleteGeneration(next); err != nil {
			t.Fatal(err)
		}
		if n := d.ExpireRoutes(time.Unix(151, 0), 50*time.Second); n != 1 {
			t.Fatalf("successor expired=%d", n)
		}
		rowsBefore, bytesBefore := readSuspectLedger(d)
		if err := d.AbortGeneration(first); err == nil {
			t.Fatal("old Abort accepted successor")
		}
		if rows, bytes := readSuspectLedger(d); rows != rowsBefore || bytes != bytesBefore {
			t.Fatalf("old Abort refunded successor ownership: before=(%d,%d) after=(%d,%d)", rowsBefore, bytesBefore, rows, bytes)
		}
		d.LoseAuthority(37)
		d.LoseAuthority(38)
		if rows, bytes := readSuspectLedger(d); rows != 0 || bytes != 0 {
			t.Fatalf("retirement retained ledger: rows=%d bytes=%d", rows, bytes)
		}
	})
}

type suspectBackingView struct {
	// Holding the test's references prevents pointer reuse during comparison;
	// they are outside production ownership and are never used as a heap metric.
	suspects             map[identityKey]Route
	uids                 map[string]int
	suspectsLen, uidsLen int
	rows, bytes          uint64
}

func readSuspectLedger(d *Directory) (uint64, uint64) {
	d.suspects.mu.Lock()
	defer d.suspects.mu.Unlock()
	return d.suspects.rows, d.suspects.bytes
}

func readSuspectBacking(t *testing.T, d *Directory, claim GenerationClaim) suspectBackingView {
	t.Helper()
	s := d.shard(claim.Target.HashSlot)
	s.mu.RLock()
	defer s.mu.RUnlock()
	g := s.slots[claim.Target.HashSlot].generation
	if !g.claim.Equal(claim) {
		t.Fatal("unexpected generation")
	}
	rows, bytes := readSuspectLedger(d)
	return suspectBackingView{g.suspects, g.suspectUIDs, len(g.suspects), len(g.suspectUIDs), rows, bytes}
}

func sameSuspectTables(a, b suspectBackingView) bool {
	return reflect.ValueOf(a.suspects).Pointer() == reflect.ValueOf(b.suspects).Pointer() && reflect.ValueOf(a.uids).Pointer() == reflect.ValueOf(b.uids).Pointer()
}

func backingPayloadBytes(route Route) uint64 {
	return uint64(len(route.UID) + len(route.DeviceID) + len(route.Listener))
}

func installBackingSuspects(t *testing.T, d *Directory, hash uint16, count int) (GenerationClaim, []Route) {
	t.Helper()
	claim := GenerationClaim{Target: RouteTarget{HashSlot: hash, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}, OwnerSetEpoch: 2, ServingBoot: 22, Generation: 1, AppliedRaftIndex: 100}
	owner := OwnerCut{NodeID: 1, BootID: 11, Cut: 1}
	d.BecomeAuthority(claim.Target)
	if err := d.BeginGeneration(claim, []OwnerCut{owner}); err != nil {
		t.Fatal(err)
	}
	all := make([]Route, count)
	for i := range all {
		all[i] = Route{UID: fmt.Sprintf("slot-%d-user-%d", hash, i), OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: uint64(i + 1), SessionID: uint64(i + 1), DeviceID: "device", LastSeenUnix: 100}
	}
	seq := uint64(0)
	for start := 0; start < len(all); start += 4096 {
		end := min(start+4096, len(all))
		seq++
		page := SlotPage{Claim: claim, Owner: owner, Sequence: seq, AfterSessionID: uint64(start), NextSessionID: uint64(end), RowsTotal: uint64(end), Exhausted: end == len(all), Routes: all[start:end]}
		if err := d.InstallSlotPage(page); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.EndGenerationOwner(SlotEnd{Claim: claim, Owner: owner, Sequence: seq, LastSessionID: uint64(count), RowsTotal: uint64(count)}); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteGeneration(claim); err != nil {
		t.Fatal(err)
	}
	if n := d.ExpireRoutes(time.Unix(151, 0), 50*time.Second); n != count {
		t.Fatalf("expired=%d want=%d", n, count)
	}
	return claim, all
}
