package rpctrace

import (
	"testing"
	"unsafe"
)

// This internal representation check enforces the explicit fixed-memory budget;
// behavior is exercised through Trace publication and the public store seam.
func TestCommitCohortFixedStateFitsApprovedMemoryBudget(t *testing.T) {
	if got := unsafe.Sizeof(commitCohortState{}); got > 1024 {
		t.Fatalf("fixed cohort state=%d bytes, want <=1024", got)
	}
	if MaxSpans != 32 {
		t.Fatal("cohort observation widened existing RPC span capacity")
	}
}
