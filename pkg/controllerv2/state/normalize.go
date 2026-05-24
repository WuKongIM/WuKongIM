package state

import "sort"

// Normalize applies deterministic defaults and ordering to a cluster state.
func (s *ClusterState) Normalize() {
	if s == nil {
		return
	}
	s.UpdatedAt = s.UpdatedAt.UTC()
	if s.Controllers == nil {
		s.Controllers = []ControllerVoter{}
	}
	if s.Nodes == nil {
		s.Nodes = []Node{}
	}
	if s.Slots == nil {
		s.Slots = []SlotAssignment{}
	}
	if s.HashSlots.Ranges == nil {
		s.HashSlots.Ranges = []HashSlotRange{}
	}
	if s.Tasks == nil {
		s.Tasks = []ReconcileTask{}
	}
	for i := range s.Nodes {
		if s.Nodes[i].CapacityWeight == 0 {
			s.Nodes[i].CapacityWeight = 1
		}
		sort.Slice(s.Nodes[i].Roles, func(a, b int) bool { return s.Nodes[i].Roles[a] < s.Nodes[i].Roles[b] })
	}
	for i := range s.Slots {
		sort.Slice(s.Slots[i].DesiredPeers, func(a, b int) bool { return s.Slots[i].DesiredPeers[a] < s.Slots[i].DesiredPeers[b] })
	}
	for i := range s.Tasks {
		sort.Slice(s.Tasks[i].TargetPeers, func(a, b int) bool { return s.Tasks[i].TargetPeers[a] < s.Tasks[i].TargetPeers[b] })
	}
	sort.Slice(s.Controllers, func(i, j int) bool { return s.Controllers[i].NodeID < s.Controllers[j].NodeID })
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].NodeID < s.Nodes[j].NodeID })
	sort.Slice(s.Slots, func(i, j int) bool { return s.Slots[i].SlotID < s.Slots[j].SlotID })
	sort.Slice(s.HashSlots.Ranges, func(i, j int) bool {
		if s.HashSlots.Ranges[i].From == s.HashSlots.Ranges[j].From {
			return s.HashSlots.Ranges[i].To < s.HashSlots.Ranges[j].To
		}
		return s.HashSlots.Ranges[i].From < s.HashSlots.Ranges[j].From
	})
	sort.Slice(s.Tasks, func(i, j int) bool {
		if s.Tasks[i].SlotID == s.Tasks[j].SlotID {
			return s.Tasks[i].TaskID < s.Tasks[j].TaskID
		}
		return s.Tasks[i].SlotID < s.Tasks[j].SlotID
	})
}

// Clone returns a deep copy of the cluster state.
func (s ClusterState) Clone() ClusterState {
	out := s
	out.Controllers = cloneSlice(s.Controllers)
	out.Nodes = cloneSlice(s.Nodes)
	for i := range out.Nodes {
		out.Nodes[i].Roles = cloneSlice(s.Nodes[i].Roles)
	}
	out.Slots = cloneSlice(s.Slots)
	for i := range out.Slots {
		out.Slots[i].DesiredPeers = cloneUint64s(s.Slots[i].DesiredPeers)
	}
	out.HashSlots.Ranges = cloneSlice(s.HashSlots.Ranges)
	out.Tasks = cloneSlice(s.Tasks)
	for i := range out.Tasks {
		out.Tasks[i].TargetPeers = cloneUint64s(s.Tasks[i].TargetPeers)
	}
	return out
}

// HasRole reports whether the node has the requested durable capability.
func (n Node) HasRole(role NodeRole) bool {
	for _, candidate := range n.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func cloneUint64s(in []uint64) []uint64 {
	return cloneSlice(in)
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}
