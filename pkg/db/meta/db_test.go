package meta

import "testing"

func TestMetaDBShardHandlesAreStable(t *testing.T) {
	db := NewDB(nil)
	a := db.HashSlot(12)
	b := db.HashSlot(12)
	if a == nil || b == nil {
		t.Fatal("HashSlot() returned nil")
	}
	if a != b {
		t.Fatal("HashSlot() returned different handles for the same hash slot")
	}
	if a.HashSlot() != 12 {
		t.Fatalf("HashSlot() = %d, want 12", a.HashSlot())
	}
}
