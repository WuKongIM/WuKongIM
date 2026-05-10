package deliverytag

// PartitionTopologyVersion describes the full partition authority shape used by a tag.
type PartitionTopologyVersion struct {
	HashSlotTableVersion uint64
	SlotAuthorityRefs    []SlotAuthorityRef
}

// Equal reports whether two topology versions have the same full shape.
func (v PartitionTopologyVersion) Equal(other PartitionTopologyVersion) bool {
	if v.HashSlotTableVersion != other.HashSlotTableVersion {
		return false
	}
	if len(v.SlotAuthorityRefs) != len(other.SlotAuthorityRefs) {
		return false
	}
	for i := range v.SlotAuthorityRefs {
		if v.SlotAuthorityRefs[i] != other.SlotAuthorityRefs[i] {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of the topology version.
func (v PartitionTopologyVersion) Clone() PartitionTopologyVersion {
	out := v
	out.SlotAuthorityRefs = append([]SlotAuthorityRef(nil), v.SlotAuthorityRefs...)
	return out
}

// SlotAuthorityRef records the authoritative owner/version for one subscriber slot.
type SlotAuthorityRef struct {
	SlotID         uint32
	LeaderNodeID   uint64
	ConfigEpoch    uint64
	BalanceVersion uint64
}
