package buffer

import "testing"

func TestSlabPoolGetUsesSmallestFittingClass(t *testing.T) {
	pool := NewSlabPool([]int{8, 16})

	buf := pool.Get(9)
	bytes := buf.Bytes()
	if len(bytes) != 9 {
		t.Fatalf("len = %d, want 9", len(bytes))
	}
	if cap(bytes) != 16 {
		t.Fatalf("cap = %d, want 16", cap(bytes))
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
