package cluster

import (
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestRouterSlotForKeyDeterministic(t *testing.T) {
	r := NewRouter(NewHashSlotTable(256, 4), 1, nil)
	a := r.SlotForKey("test-channel")
	b := r.SlotForKey("test-channel")
	if a != b {
		t.Fatalf("non-deterministic: %d != %d", a, b)
	}
}

func TestRouterSlotForKeyUsesHashSlotTable(t *testing.T) {
	table := NewHashSlotTable(256, 4)
	r := NewRouter(table, 1, nil)

	for _, id := range []string{"a", "b", "c", "d", "e", "channel-123", "xyz"} {
		hashSlot := r.HashSlotForKey(id)
		slot := r.SlotForKey(id)
		if hashSlot >= 256 {
			t.Fatalf("hash slot %d out of range [0,255] for key=%s", hashSlot, id)
		}
		if want := table.Lookup(hashSlot); slot != want {
			t.Fatalf("SlotForKey(%q) = %d, want %d", id, slot, want)
		}
	}
}

func TestRouterUpdateHashSlotTable(t *testing.T) {
	table := NewHashSlotTable(256, 4)
	r := NewRouter(table, 1, nil)

	key := ""
	for i := 0; i < 1024; i++ {
		candidate := fmt.Sprintf("channel-%d", i)
		if table.Lookup(r.HashSlotForKey(candidate)) != 4 {
			key = candidate
			break
		}
	}
	if key == "" {
		t.Fatal("failed to find key outside slot 4")
	}
	hashSlot := r.HashSlotForKey(key)
	original := r.SlotForKey(key)

	next := table.Clone()
	next.Reassign(hashSlot, 4)
	r.UpdateHashSlotTable(next)

	if updated := r.SlotForKey(key); updated != 4 {
		t.Fatalf("SlotForKey(%q) after update = %d, want 4", key, updated)
	}
	if original == 4 {
		t.Fatalf("test key %q did not move slots", key)
	}
}

func TestRouterSlotForKeyDistribution(t *testing.T) {
	r := NewRouter(NewHashSlotTable(256, 4), 1, nil)
	counts := make(map[multiraft.SlotID]int)
	for i := 0; i < 1000; i++ {
		slot := r.SlotForKey(fmt.Sprintf("channel-%d", i))
		counts[slot]++
	}
	for g := multiraft.SlotID(1); g <= 4; g++ {
		if counts[g] == 0 {
			t.Fatalf("slot %d has 0 channels — bad distribution", g)
		}
	}
}

func TestIsLocal(t *testing.T) {
	r := &Router{localNode: 1}
	if !r.IsLocal(1) {
		t.Fatal("expected local")
	}
	if r.IsLocal(2) {
		t.Fatal("expected not local")
	}
}
