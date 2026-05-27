package control

import "fmt"

// Validate checks structural invariants required by clusterv2 consumers.
func (s Snapshot) Validate() error {
	if s.HashSlots.Count == 0 {
		return fmt.Errorf("control snapshot: hash slot count must be > 0")
	}
	dataNodes := make(map[uint64]struct{}, len(s.Nodes))
	seenNodes := make(map[uint64]struct{}, len(s.Nodes))
	for _, node := range s.Nodes {
		if node.NodeID == 0 {
			return fmt.Errorf("control snapshot: node id must be > 0")
		}
		if _, ok := seenNodes[node.NodeID]; ok {
			return fmt.Errorf("control snapshot: duplicate node %d", node.NodeID)
		}
		seenNodes[node.NodeID] = struct{}{}
		if hasRole(node.Roles, RoleData) {
			dataNodes[node.NodeID] = struct{}{}
		}
	}
	seenSlots := make(map[uint32]struct{}, len(s.Slots))
	for _, slot := range s.Slots {
		if slot.SlotID == 0 {
			return fmt.Errorf("control snapshot: slot id must be > 0")
		}
		if _, ok := seenSlots[slot.SlotID]; ok {
			return fmt.Errorf("control snapshot: duplicate slot %d", slot.SlotID)
		}
		seenSlots[slot.SlotID] = struct{}{}
		if len(slot.DesiredPeers) == 0 {
			return fmt.Errorf("control snapshot: slot %d has no desired peers", slot.SlotID)
		}
		seenPeers := make(map[uint64]struct{}, len(slot.DesiredPeers))
		for _, peer := range slot.DesiredPeers {
			if _, ok := dataNodes[peer]; !ok {
				return fmt.Errorf("control snapshot: slot %d peer %d is not a data node", slot.SlotID, peer)
			}
			if _, ok := seenPeers[peer]; ok {
				return fmt.Errorf("control snapshot: slot %d duplicate peer %d", slot.SlotID, peer)
			}
			seenPeers[peer] = struct{}{}
		}
		if slot.PreferredLeader != 0 {
			if _, ok := seenPeers[slot.PreferredLeader]; !ok {
				return fmt.Errorf("control snapshot: slot %d preferred leader %d is not a desired peer", slot.SlotID, slot.PreferredLeader)
			}
		}
	}
	if err := validateHashSlotRanges(s.HashSlots, seenSlots); err != nil {
		return err
	}
	for _, task := range s.Tasks {
		if task.TaskID == "" || task.SlotID == 0 {
			return fmt.Errorf("control snapshot: invalid task")
		}
		if _, ok := seenSlots[task.SlotID]; !ok {
			return fmt.Errorf("control snapshot: task %q references unknown slot %d", task.TaskID, task.SlotID)
		}
	}
	return nil
}

func validateHashSlotRanges(table HashSlotTable, slots map[uint32]struct{}) error {
	if len(table.Ranges) == 0 {
		return fmt.Errorf("control snapshot: hash slot ranges must not be empty")
	}
	expectedFrom := uint16(0)
	for i, r := range table.Ranges {
		if r.SlotID == 0 {
			return fmt.Errorf("control snapshot: hash slot range %d has zero slot", i)
		}
		if _, ok := slots[r.SlotID]; !ok {
			return fmt.Errorf("control snapshot: hash slot range %d references unknown slot %d", i, r.SlotID)
		}
		if r.From != expectedFrom || r.To < r.From {
			return fmt.Errorf("control snapshot: hash slot range %d is not contiguous", i)
		}
		if r.To == ^uint16(0) {
			expectedFrom = r.To
		} else {
			expectedFrom = r.To + 1
		}
	}
	if expectedFrom != table.Count {
		return fmt.Errorf("control snapshot: hash slot ranges cover %d slots, want %d", expectedFrom, table.Count)
	}
	return nil
}

func hasRole(roles []Role, role Role) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}
