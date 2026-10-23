package multiraft

import (
	"testing"
	"time"
)

// Deterministic observer matrix: each scope counts once per positive phase;
// mutually exclusive children partition their own parent; caller clipping
// closes both at one instant, and late completion cannot add duration. A caller
// registering inside an older scope starts at its own enqueue, not scope start.
func TestReadBarrierOwnerCoveragePartitionsAndClips(t *testing.T) {
	start := time.Unix(900, 0)
	o := &readBarrierTiming{}
	for i := range o.points {
		o.points[i] = start.Add(time.Duration(i) * 10 * time.Nanosecond)
	}
	o.openOwner(true, 1, start, ownerStageDrain, start)
	o.switchOwnerStage(start.Add(15*time.Nanosecond), ownerStagePermit)
	o.switchOwnerStage(start.Add(25*time.Nanosecond), ownerStageWork)
	o.closeOwner(start.Add(42*time.Nanosecond), true)
	o.terminal = start.Add(42 * time.Nanosecond)
	before := o.ownerCoverage
	o.closeOwner(start.Add(100*time.Nanosecond), false)
	o.switchOwnerStage(start.Add(100*time.Nanosecond), ownerStageDrain)
	if o.ownerCoverage != before {
		t.Fatal("late scope changed closed owner coverage")
	}
	for i, p := range o.ownerCoverage.Phases {
		want := 10 * time.Nanosecond
		if i == 4 {
			want = 2 * time.Nanosecond
		}
		if p.Budget.Duration != want || p.Budget.Count != 1 || !p.Budget.Unfinished || p.Budget.RejectMask != 1 || p.Other.Duration != 0 {
			t.Fatalf("phase %d scope: %+v", i, p)
		}
		if p.Budget.DrainWait+p.Budget.SavePermit+p.Budget.SyncWork != want {
			t.Fatalf("phase %d children did not partition: %+v", i, p.Budget)
		}
	}
	if o.ownerCoverage.Phases[1].Budget.DrainWait != 5*time.Nanosecond || o.ownerCoverage.Phases[1].Budget.SavePermit != 5*time.Nanosecond {
		t.Fatal("stage crossing phase was attributed incorrectly")
	}
}
func TestReadBarrierOwnerCoveragePriorScopeAndUnknownPhase(t *testing.T) {
	start := time.Unix(901, 0)
	o := &readBarrierTiming{}
	o.points[0] = start
	o.openOwner(false, 1, start.Add(-time.Second), ownerStageNone, time.Time{})
	o.closeOwner(start.Add(17*time.Nanosecond), true)
	p := o.ownerCoverage.Phases[0]
	if p.Other.Duration != 17*time.Nanosecond || p.Other.Count != 1 || !p.Other.Unfinished || p.Other.CauseMask != 1 || p.Budget.Duration != 0 {
		t.Fatalf("prior scope not clipped to caller: %+v", p)
	}
	for _, p := range o.ownerCoverage.Phases[1:] {
		if p != o.ownerCoverage.Phases[4] {
			t.Fatal("unreached phases became observed")
		}
		if p.Other.Duration != 0 || p.Budget.Duration != 0 {
			t.Fatal("unreached phase owner work invented")
		}
	}
}

// Reasons are a snapshot of the same failed locked admission, never a second
// admission decision. Oversize is structural and must not become budget busy.
func TestStorageOwnerRejectReasonsPreserveAdmission(t *testing.T) {
	b, e := newStorageBudget(2, 1)
	if e != nil {
		t.Fatal(e)
	}
	g := &slot{}
	first, ok := b.reserve(g, storageSlotBytes, storageAppendPart)
	if !ok {
		t.Fatal("setup")
	}
	defer first.retire()
	var mask uint8
	if _, ok = b.reserveWithReason(g, 1, storageAppendPart, &mask); ok || mask != 5 {
		t.Fatalf("slot rejection mask=%d accepted=%v", mask, ok)
	}
	mask = 0
	if _, ok = b.reserveWithReason(&slot{}, storageSlotBytes+1, storageAppendPart, &mask); ok || mask != 0 {
		t.Fatalf("oversize became budget reason=%d accepted=%v", mask, ok)
	}
	other, ok := b.reserveWithReason(&slot{}, 1, storageAppendPart, &mask)
	if !ok || mask != 0 {
		t.Fatal("observation changed unrelated admission")
	}
	other.retire()
}
