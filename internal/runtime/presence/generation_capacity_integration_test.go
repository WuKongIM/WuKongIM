//go:build integration

package presence_test

import (
	"fmt"
	p "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"testing"
	"time"
)

// This integration contract audits the implemented persistent uncertainty cap;
// it is additional coverage, not retroactively claimed as a baseline RED.
func TestGenerationSuspectCapacityRevokesCompleteness(t *testing.T) {
	d, c, o := generationDirectory(t)
	seq, last, total := uint64(1), uint64(0), uint64(0)
	for start := 1; start <= 8193; start += 4096 {
		end := min(start+4096, 8194)
		rows := make([]p.Route, 0, end-start)
		for id := start; id < end; id++ {
			r := generationRoute(uint64(id))
			r.UID = fmt.Sprintf("user-%d", id)
			rows = append(rows, r)
		}
		total += uint64(len(rows))
		page := p.SlotPage{Claim: c, Owner: o, Sequence: seq, AfterSessionID: last, NextSessionID: uint64(end - 1), RowsTotal: total, Exhausted: end == 8194, Routes: rows}
		if err := d.InstallSlotPage(page); err != nil {
			t.Fatal(err)
		}
		last = page.NextSessionID
		if page.Exhausted {
			if err := d.EndGenerationOwner(p.SlotEnd{Claim: c, Owner: o, Sequence: seq, LastSessionID: last, RowsTotal: total}); err != nil {
				t.Fatal(err)
			}
		}
		seq++
	}
	if err := d.CompleteGeneration(c); err != nil {
		t.Fatal(err)
	}
	if n := d.ExpireRoutes(time.Unix(151, 0), 50*time.Second); n != 8193 {
		t.Fatalf("expired=%d", n)
	}
	if _, err := d.EndpointsByGeneration(c, []string{"never-observed"}); err == nil {
		t.Fatal("overflow discarded uncertainty while retaining Complete")
	}
	d.LoseAuthority(37)
	if got := d.Snapshot(); got.Active != 0 || got.ExpiryIndexRoutes != 0 {
		t.Fatalf("discarded authority retained routes: %+v", got)
	}
}
