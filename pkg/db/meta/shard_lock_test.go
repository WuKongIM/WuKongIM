package meta

import "testing"

func TestShardLocksOrderHashSlots(t *testing.T) {
	got := orderedHashSlots([]HashSlot{5, 1, 3, 1, 2})
	want := []HashSlot{1, 2, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("orderedHashSlots len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orderedHashSlots[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestShardLocksUnlockReverseOrder(t *testing.T) {
	db := NewDB(nil)
	unlock := db.lockHashSlots([]HashSlot{3, 1, 2})
	if got := db.testLockedOrder(); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("locked order = %+v, want [1 2 3]", got)
	}
	unlock()
	if got := db.testLockedOrder(); len(got) != 0 {
		t.Fatalf("locked order after unlock = %+v, want empty", got)
	}
}
