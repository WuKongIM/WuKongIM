//go:build !race

package meta

import (
	"context"
	"testing"
)

// Race instrumentation changes compiler escape decisions and sync.Pool reuse;
// allocation budgets describe the production build, while correctness tests
// remain enabled under race instrumentation.
func TestRuntimeMetadataBatchAllocationBudget(t *testing.T) {
	db, keys := runtimeMetadataBatchFixture(t)
	allocations := testing.AllocsPerRun(20, func() {
		rows, err := db.GetChannelRuntimeMetaBatch(context.Background(), keys)
		if err != nil || len(rows) != len(keys) {
			t.Fatalf("rows=%d err=%v", len(rows), err)
		}
	})
	// The original loop allocates 901 objects for these 100 exact-key reads.
	if allocations > 850 {
		t.Fatalf("runtime batch allocations = %.0f, want <= 850", allocations)
	}
}
