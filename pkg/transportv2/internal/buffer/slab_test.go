package buffer

import "testing"

func TestSlabPoolGetUsesSmallestFittingClass(t *testing.T) {
	pool := NewSlabPool([]int{8, 16})

	buf := pool.Get(9)
	bytes := buf.Bytes()
	if len(bytes) != 9 {
		t.Fatalf("len = %d, want 9", len(bytes))
	}
	if cap(bytes) != 9 {
		t.Fatalf("cap = %d, want 9", cap(bytes))
	}

	buf.Release()
	buf.Release()
}

func TestSlabPoolGetAllocatesExactWhenLargerThanClasses(t *testing.T) {
	pool := NewSlabPool([]int{8})

	buf := pool.Get(64)
	bytes := buf.Bytes()
	if len(bytes) != 64 {
		t.Fatalf("len = %d, want 64", len(bytes))
	}
	if cap(bytes) != 64 {
		t.Fatalf("cap = %d, want 64", cap(bytes))
	}

	buf.Release()
	buf.Release()
}

func TestSlabPoolGetNormalizesSizesWithoutExposingExtraCapacity(t *testing.T) {
	pool := NewSlabPool([]int{16, 0, 8, -1, 16})

	buf := pool.Get(9)
	bytes := buf.Bytes()
	if len(bytes) != 9 {
		t.Fatalf("len = %d, want 9", len(bytes))
	}
	if cap(bytes) != 9 {
		t.Fatalf("cap = %d, want 9", cap(bytes))
	}
	if extended := append(bytes, 'x'); len(extended) != 10 || cap(extended) == 16 {
		t.Fatalf("append reused slab capacity: len=%d cap=%d", len(extended), cap(extended))
	}

	buf.Release()
}

func TestSlabPoolGetReuseKeepsPayloadCapacityBounded(t *testing.T) {
	pool := NewSlabPool([]int{16})

	first := pool.Get(9)
	firstBytes := first.Bytes()
	for i := range firstBytes {
		firstBytes[i] = byte(i + 1)
	}
	first.Release()

	second := pool.Get(9)
	defer second.Release()
	secondBytes := second.Bytes()
	if len(secondBytes) != 9 {
		t.Fatalf("len = %d, want 9", len(secondBytes))
	}
	if cap(secondBytes) != 9 {
		t.Fatalf("cap = %d, want 9", cap(secondBytes))
	}
	if extended := append(secondBytes, 'x'); len(extended) != 10 || cap(extended) == 16 {
		t.Fatalf("append reused slab capacity after release: len=%d cap=%d", len(extended), cap(extended))
	}
}
