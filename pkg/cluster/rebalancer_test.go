package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func applyMigrationPlan(t *testing.T, table *HashSlotTable, plan []MigrationPlan) map[multiraft.SlotID]int {
	t.Helper()

	counts := make(map[multiraft.SlotID]int)
	for _, slotID := range activeSlotIDsForTest(table) {
		counts[slotID] = len(table.HashSlotsOf(slotID))
	}
	for _, step := range plan {
		if got := table.Lookup(step.HashSlot); got != step.From {
			t.Fatalf("hash slot %d owner = %d, want %d", step.HashSlot, got, step.From)
		}
		counts[step.From]--
		counts[step.To]++
	}
	return counts
}

func activeSlotIDsForTest(table *HashSlotTable) []multiraft.SlotID {
	seen := make(map[multiraft.SlotID]struct{})
	slots := make([]multiraft.SlotID, 0)
	for hashSlot := uint16(0); hashSlot < table.HashSlotCount(); hashSlot++ {
		slotID := table.Lookup(hashSlot)
		if slotID == 0 {
			continue
		}
		if _, ok := seen[slotID]; ok {
			continue
		}
		seen[slotID] = struct{}{}
		slots = append(slots, slotID)
	}
	return slots
}

func TestComputeAddSlotPlan(t *testing.T) {
	table := NewHashSlotTable(256, 4)

	plan := ComputeAddSlotPlan(table, 5)
	if got := len(plan); got < 51 || got > 52 {
		t.Fatalf("len(plan) = %d, want 51 or 52", got)
	}

	counts := applyMigrationPlan(t, table, plan)
	for _, slotID := range []multiraft.SlotID{1, 2, 3, 4, 5} {
		got := counts[slotID]
		if got < 51 || got > 52 {
			t.Fatalf("slot %d count after add plan = %d, want [51,52]", slotID, got)
		}
	}
}

func TestComputeRemoveSlotPlan(t *testing.T) {
	table := NewHashSlotTable(256, 4)

	plan := ComputeRemoveSlotPlan(table, 3)
	if got := len(plan); got != 64 {
		t.Fatalf("len(plan) = %d, want 64", got)
	}

	counts := applyMigrationPlan(t, table, plan)
	if got := counts[3]; got != 0 {
		t.Fatalf("removed slot count = %d, want 0", got)
	}
	for _, slotID := range []multiraft.SlotID{1, 2, 4} {
		got := counts[slotID]
		if got < 85 || got > 86 {
			t.Fatalf("slot %d count after remove plan = %d, want [85,86]", slotID, got)
		}
	}
}

func TestComputeRebalancePlan(t *testing.T) {
	table := NewHashSlotTable(12, 3)
	table.Reassign(4, 1)
	table.Reassign(5, 1)
	table.Reassign(8, 1)
	table.Reassign(9, 1)

	plan := ComputeRebalancePlan(table)
	if got := len(plan); got != 4 {
		t.Fatalf("len(plan) = %d, want 4", got)
	}

	counts := applyMigrationPlan(t, table, plan)
	for _, slotID := range []multiraft.SlotID{1, 2, 3} {
		if got := counts[slotID]; got != 4 {
			t.Fatalf("slot %d count after rebalance = %d, want 4", slotID, got)
		}
	}
}
