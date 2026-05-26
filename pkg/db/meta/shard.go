package meta

import "sort"

// Shard is a typed metadata handle for one hash slot.
type Shard struct {
	db       *MetaDB
	hashSlot HashSlot
}

// HashSlot returns the shard hash slot.
func (s *Shard) HashSlot() HashSlot {
	if s == nil {
		return 0
	}
	return s.hashSlot
}

func orderedHashSlots(hashSlots []HashSlot) []HashSlot {
	if len(hashSlots) == 0 {
		return nil
	}
	seen := make(map[HashSlot]struct{}, len(hashSlots))
	ordered := make([]HashSlot, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		if _, ok := seen[hashSlot]; ok {
			continue
		}
		seen[hashSlot] = struct{}{}
		ordered = append(ordered, hashSlot)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered
}
