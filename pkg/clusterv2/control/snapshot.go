package control

import "fmt"

// Role describes one durable node capability in clusterv2 control state.
type Role string

const (
	// RoleController marks a node that can participate in Controller coordination.
	RoleController Role = "controller"
	// RoleData marks a node that can host physical Slot replicas.
	RoleData Role = "data"
)

// NodeStatus describes the latest durable control-plane health state for a node.
type NodeStatus string

const (
	// NodeAlive means the node is considered available.
	NodeAlive NodeStatus = "alive"
	// NodeSuspect means the node may be unavailable.
	NodeSuspect NodeStatus = "suspect"
	// NodeDown means the node is considered unavailable.
	NodeDown NodeStatus = "down"
)

// TaskKind identifies one reconcile workflow kind.
type TaskKind string

const (
	// TaskKindBootstrap creates the initial physical Slot replica group.
	TaskKindBootstrap TaskKind = "bootstrap"
)

// Snapshot is the clusterv2 control read model consumed by data-plane modules.
type Snapshot struct {
	// Revision is the monotonically increasing control state revision.
	Revision uint64
	// ControllerID is the best-known Controller leader or owner node ID.
	ControllerID uint64
	// Nodes lists known cluster members.
	Nodes []Node
	// Slots lists desired physical Slot assignments.
	Slots []SlotAssignment
	// HashSlots maps logical hash-slot ranges to physical Slots.
	HashSlots HashSlotTable
	// Tasks lists active reconcile tasks.
	Tasks []ReconcileTask
}

// Node describes one cluster member in the control snapshot.
type Node struct {
	// NodeID is the stable non-zero node identity.
	NodeID uint64
	// Addr is the cluster RPC address for this node.
	Addr string
	// Roles lists durable node capabilities.
	Roles []Role
	// Status is the durable control-plane health state.
	Status NodeStatus
}

// SlotAssignment describes desired replicas for one physical Slot.
type SlotAssignment struct {
	// SlotID is the non-zero physical Slot ID.
	SlotID uint32
	// DesiredPeers are node IDs that should host this Slot.
	DesiredPeers []uint64
	// ConfigEpoch changes when DesiredPeers changes.
	ConfigEpoch uint64
	// PreferredLeader is the desired bootstrap or leadership target when set.
	PreferredLeader uint64
}

// HashSlotTable maps logical hash slots to physical Slot IDs.
type HashSlotTable struct {
	// Revision is the routing table revision.
	Revision uint64
	// Count is the total number of logical hash slots.
	Count uint16
	// Ranges is a contiguous, non-overlapping list ordered by From.
	Ranges []HashSlotRange
}

// HashSlotRange maps an inclusive hash-slot range to one physical Slot.
type HashSlotRange struct {
	// From is the inclusive lower hash-slot bound.
	From uint16
	// To is the inclusive upper hash-slot bound.
	To uint16
	// SlotID is the physical Slot target for this range.
	SlotID uint32
}

// ReconcileTask describes one active Slot convergence task.
type ReconcileTask struct {
	// TaskID is the stable task identity.
	TaskID string
	// SlotID is the affected physical Slot.
	SlotID uint32
	// Kind identifies the reconcile workflow kind.
	Kind TaskKind
	// TargetNode is the primary node that should execute this task when set.
	TargetNode uint64
	// TargetPeers are the peer IDs this task should converge.
	TargetPeers []uint64
	// ConfigEpoch ties this task to a Slot assignment epoch.
	ConfigEpoch uint64
}

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
