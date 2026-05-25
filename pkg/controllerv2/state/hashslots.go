package state

import "fmt"

// BuildInitialHashSlotTable creates a deterministic contiguous hash-slot routing table.
func BuildInitialHashSlotTable(slotCount uint32, hashSlotCount uint16) (HashSlotTable, error) {
	if slotCount == 0 || hashSlotCount == 0 || slotCount > uint32(hashSlotCount) {
		return HashSlotTable{}, fmt.Errorf("%w: invalid hash slot counts", ErrInvalidState)
	}

	ranges := make([]HashSlotRange, 0, slotCount)
	base := uint32(hashSlotCount) / slotCount
	remainder := uint32(hashSlotCount) % slotCount
	next := uint32(0)
	for slotID := uint32(1); slotID <= slotCount; slotID++ {
		width := base
		if slotID <= remainder {
			width++
		}
		from := next
		to := next + width - 1
		ranges = append(ranges, HashSlotRange{From: uint16(from), To: uint16(to), SlotID: slotID})
		next = to + 1
	}
	return HashSlotTable{Version: CurrentHashSlotTableVersion, SlotCount: hashSlotCount, Ranges: ranges}, nil
}
