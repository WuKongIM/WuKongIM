//go:build !race

package message

import (
	"context"
	"testing"
)

// A zero-floor preview still verifies the durable index and retained ordinal,
// but should not allocate a predecessor search key below the first sequence.
func TestOrdinaryCountZeroFloorAllocationBudget(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	appendBadgeRows(t, log, 1, false, true, false, false, false)
	assertBadgeCount(t, log, 0, 4, 3)
	allocations := testing.AllocsPerRun(100, func() {
		got, err := log.CountOrdinaryMessages(context.Background(), 0, 4)
		if err != nil || got != 3 {
			t.Fatalf("count = %d, %v", got, err)
		}
	})
	if allocations > 16 {
		t.Fatalf("zero-floor count allocations = %.0f, want <= 16", allocations)
	}
}
