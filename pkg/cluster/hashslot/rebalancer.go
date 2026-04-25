package hashslot

import (
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type MigrationPlan struct {
	HashSlot uint16
	From     multiraft.SlotID
	To       multiraft.SlotID
}

func ComputeAddSlotPlan(table *HashSlotTable, newSlotID multiraft.SlotID) []MigrationPlan {
	if table == nil || newSlotID == 0 {
		return nil
	}

	existingSlots := tableActiveSlotIDs(table)
	if containsSlotID(existingSlots, newSlotID) {
		return nil
	}
	slots := append(append([]multiraft.SlotID(nil), existingSlots...), newSlotID)
	sortSlotIDs(slots)

	current := slotCounts(table, slots)
	target := idealSlotCounts(int(table.HashSlotCount()), slots)
	owned := slotHashSlots(table, existingSlots)

	plan := make([]MigrationPlan, 0, target[newSlotID])
	for current[newSlotID] < target[newSlotID] {
		donor := selectLargestSurplusSlot(current, target, existingSlots)
		if donor == 0 {
			break
		}
		hashSlot, ok := popOwnedHashSlot(owned, donor)
		if !ok {
			break
		}
		plan = append(plan, MigrationPlan{
			HashSlot: hashSlot,
			From:     donor,
			To:       newSlotID,
		})
		current[donor]--
		current[newSlotID]++
	}
	return plan
}

func ComputeRemoveSlotPlan(table *HashSlotTable, removeSlotID multiraft.SlotID) []MigrationPlan {
	if table == nil || removeSlotID == 0 {
		return nil
	}

	if len(table.HashSlotsOf(removeSlotID)) == 0 {
		return nil
	}

	remaining := tableActiveSlotIDsExcluding(table, removeSlotID)
	if len(remaining) == 0 {
		return nil
	}

	current := slotCounts(table, append(append([]multiraft.SlotID(nil), remaining...), removeSlotID))
	target := idealSlotCounts(int(table.HashSlotCount()), remaining)
	owned := slotHashSlots(table, []multiraft.SlotID{removeSlotID})

	plan := make([]MigrationPlan, 0, current[removeSlotID])
	for current[removeSlotID] > 0 {
		receiver := selectSmallestDeficitSlot(current, target, remaining)
		if receiver == 0 {
			break
		}
		hashSlot, ok := popOwnedHashSlot(owned, removeSlotID)
		if !ok {
			break
		}
		plan = append(plan, MigrationPlan{
			HashSlot: hashSlot,
			From:     removeSlotID,
			To:       receiver,
		})
		current[removeSlotID]--
		current[receiver]++
	}
	return plan
}

func ComputeRebalancePlan(table *HashSlotTable) []MigrationPlan {
	if table == nil {
		return nil
	}

	slots := tableActiveSlotIDs(table)
	if len(slots) <= 1 {
		return nil
	}

	current := slotCounts(table, slots)
	target := idealSlotCounts(int(table.HashSlotCount()), slots)
	owned := slotHashSlots(table, slots)

	plan := make([]MigrationPlan, 0)
	for {
		donor := selectLargestSurplusSlot(current, target, slots)
		receiver := selectSmallestDeficitSlot(current, target, slots)
		if donor == 0 || receiver == 0 {
			break
		}
		hashSlot, ok := popOwnedHashSlot(owned, donor)
		if !ok {
			break
		}
		plan = append(plan, MigrationPlan{
			HashSlot: hashSlot,
			From:     donor,
			To:       receiver,
		})
		current[donor]--
		current[receiver]++
	}
	return plan
}

func tableActiveSlotIDs(table *HashSlotTable) []multiraft.SlotID {
	if table == nil {
		return nil
	}
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
	sortSlotIDs(slots)
	return slots
}

func tableActiveSlotIDsExcluding(table *HashSlotTable, exclude multiraft.SlotID) []multiraft.SlotID {
	slots := tableActiveSlotIDs(table)
	filtered := slots[:0]
	for _, slotID := range slots {
		if slotID == exclude {
			continue
		}
		filtered = append(filtered, slotID)
	}
	return append([]multiraft.SlotID(nil), filtered...)
}

func slotCounts(table *HashSlotTable, slots []multiraft.SlotID) map[multiraft.SlotID]int {
	counts := make(map[multiraft.SlotID]int, len(slots))
	for _, slotID := range slots {
		counts[slotID] = len(table.HashSlotsOf(slotID))
	}
	return counts
}

func slotHashSlots(table *HashSlotTable, slots []multiraft.SlotID) map[multiraft.SlotID][]uint16 {
	owned := make(map[multiraft.SlotID][]uint16, len(slots))
	for _, slotID := range slots {
		hashSlots := table.HashSlotsOf(slotID)
		owned[slotID] = append([]uint16(nil), hashSlots...)
	}
	return owned
}

func idealSlotCounts(totalHashSlots int, slots []multiraft.SlotID) map[multiraft.SlotID]int {
	target := make(map[multiraft.SlotID]int, len(slots))
	if len(slots) == 0 {
		return target
	}

	sorted := append([]multiraft.SlotID(nil), slots...)
	sortSlotIDs(sorted)

	base := totalHashSlots / len(sorted)
	remainder := totalHashSlots % len(sorted)
	for i, slotID := range sorted {
		target[slotID] = base
		if i < remainder {
			target[slotID]++
		}
	}
	return target
}

func selectLargestSurplusSlot(current, target map[multiraft.SlotID]int, candidates []multiraft.SlotID) multiraft.SlotID {
	var chosen multiraft.SlotID
	bestSurplus := 0
	bestCount := 0
	for _, slotID := range candidates {
		surplus := current[slotID] - target[slotID]
		if surplus <= 0 {
			continue
		}
		if chosen == 0 || surplus > bestSurplus || (surplus == bestSurplus && current[slotID] > bestCount) || (surplus == bestSurplus && current[slotID] == bestCount && slotID < chosen) {
			chosen = slotID
			bestSurplus = surplus
			bestCount = current[slotID]
		}
	}
	return chosen
}

func selectSmallestDeficitSlot(current, target map[multiraft.SlotID]int, candidates []multiraft.SlotID) multiraft.SlotID {
	var chosen multiraft.SlotID
	bestDeficit := 0
	bestCount := 0
	for _, slotID := range candidates {
		deficit := target[slotID] - current[slotID]
		if deficit <= 0 {
			continue
		}
		if chosen == 0 || deficit > bestDeficit || (deficit == bestDeficit && current[slotID] < bestCount) || (deficit == bestDeficit && current[slotID] == bestCount && slotID < chosen) {
			chosen = slotID
			bestDeficit = deficit
			bestCount = current[slotID]
		}
	}
	return chosen
}

func popOwnedHashSlot(owned map[multiraft.SlotID][]uint16, slotID multiraft.SlotID) (uint16, bool) {
	hashSlots := owned[slotID]
	if len(hashSlots) == 0 {
		return 0, false
	}
	last := hashSlots[len(hashSlots)-1]
	owned[slotID] = hashSlots[:len(hashSlots)-1]
	return last, true
}

func containsSlotID(slots []multiraft.SlotID, want multiraft.SlotID) bool {
	for _, slotID := range slots {
		if slotID == want {
			return true
		}
	}
	return false
}

func sortSlotIDs(slots []multiraft.SlotID) {
	sort.Slice(slots, func(i, j int) bool {
		return slots[i] < slots[j]
	})
}
