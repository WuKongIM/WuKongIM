package conversationactive

import "testing"

func TestDirtyAgeIndexCountsAndRemovesArbitraryBuckets(t *testing.T) {
	var index dirtyAgeIndex
	index.Add(3_000)
	index.Add(1_000)
	index.Add(1_000)
	index.Add(2_000)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.Oldest(); got != 1_000 {
		t.Fatalf("Oldest() = %d, want 1000", got)
	}

	index.Remove(1_000)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.Oldest(); got != 1_000 {
		t.Fatalf("Oldest() after first duplicate remove = %d, want 1000", got)
	}
	index.Remove(1_000)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.Oldest(); got != 2_000 {
		t.Fatalf("Oldest() after final duplicate remove = %d, want 2000", got)
	}

	index.Remove(3_000)
	index.Add(500)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.Oldest(); got != 500 {
		t.Fatalf("Oldest() after arbitrary removal and re-add = %d, want 500", got)
	}
	index.Remove(500)
	index.Remove(2_000)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.Oldest(); got != 0 || index.Len() != 0 {
		t.Fatalf("empty index oldest=%d len=%d, want 0/0", got, index.Len())
	}
}

func TestDirtyAgeIndexIgnoresNonpositiveAndMissingValues(t *testing.T) {
	var index dirtyAgeIndex
	index.Add(0)
	index.Add(-1)
	index.Remove(42)
	assertDirtyAgeIndexInvariant(t, &index)
	if index.Len() != 0 || index.Oldest() != 0 {
		t.Fatalf("index len=%d oldest=%d, want empty", index.Len(), index.Oldest())
	}
}

func TestDirtyAgeIndexRemoveMovesReplacementUpward(t *testing.T) {
	var index dirtyAgeIndex
	for _, activeAtMS := range []int64{1, 10, 2, 11, 12, 3, 4} {
		index.Add(activeAtMS)
	}
	assertDirtyAgeIndexInvariant(t, &index)

	index.Remove(12)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.Oldest(); got != 1 {
		t.Fatalf("Oldest() = %d, want 1 after upward replacement", got)
	}
	if _, ok := index.positions[4]; !ok {
		t.Fatalf("replacement bucket 4 missing after removing 12: %+v", index.heap)
	}
	if _, ok := index.positions[12]; ok {
		t.Fatalf("removed bucket 12 remains indexed: %+v", index.heap)
	}
}

func TestDirtyAgeIndexMoveUpdatesUniqueAndSharedBuckets(t *testing.T) {
	var index dirtyAgeIndex
	for _, activeAtMS := range []int64{1, 3, 5} {
		index.Add(activeAtMS)
	}
	index.Move(3, 6)
	assertDirtyAgeIndexInvariant(t, &index)
	if _, ok := index.positions[3]; ok {
		t.Fatalf("old unique bucket 3 remains after move: %+v", index.heap)
	}
	if _, ok := index.positions[6]; !ok {
		t.Fatalf("new unique bucket 6 missing after move: %+v", index.heap)
	}

	index.Move(6, 2)
	index.Add(2)
	index.Add(10)
	index.Move(2, 10)
	assertDirtyAgeIndexInvariant(t, &index)
	if got := index.heap[index.positions[2]].count; got != 1 {
		t.Fatalf("source shared bucket count = %d, want 1", got)
	}
	if got := index.heap[index.positions[10]].count; got != 2 {
		t.Fatalf("destination shared bucket count = %d, want 2", got)
	}
	if got := index.Oldest(); got != 1 {
		t.Fatalf("Oldest() = %d, want 1 after moves", got)
	}
}

func assertDirtyAgeIndexInvariant(t *testing.T, index *dirtyAgeIndex) {
	t.Helper()
	if len(index.heap) != len(index.positions) {
		t.Fatalf("heap len=%d positions len=%d", len(index.heap), len(index.positions))
	}
	for position, bucket := range index.heap {
		if bucket.count <= 0 {
			t.Fatalf("bucket[%d] count=%d, want positive", position, bucket.count)
		}
		if got := index.positions[bucket.activeAtMS]; got != position {
			t.Fatalf("positions[%d]=%d, want %d", bucket.activeAtMS, got, position)
		}
		if position > 0 {
			parent := (position - 1) / 2
			if index.heap[parent].activeAtMS > bucket.activeAtMS {
				t.Fatalf("heap parent[%d]=%d > child[%d]=%d", parent, index.heap[parent].activeAtMS, position, bucket.activeAtMS)
			}
		}
	}
}
