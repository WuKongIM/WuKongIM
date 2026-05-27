package control

// Clone returns a deep copy that callers may mutate independently.
func (s Snapshot) Clone() Snapshot {
	out := s
	out.Nodes = append([]Node(nil), s.Nodes...)
	for i := range out.Nodes {
		out.Nodes[i].Roles = append([]Role(nil), s.Nodes[i].Roles...)
	}
	out.Slots = append([]SlotAssignment(nil), s.Slots...)
	for i := range out.Slots {
		out.Slots[i].DesiredPeers = append([]uint64(nil), s.Slots[i].DesiredPeers...)
	}
	out.HashSlots.Ranges = append([]HashSlotRange(nil), s.HashSlots.Ranges...)
	out.Tasks = append([]ReconcileTask(nil), s.Tasks...)
	for i := range out.Tasks {
		out.Tasks[i].TargetPeers = append([]uint64(nil), s.Tasks[i].TargetPeers...)
	}
	return out
}
