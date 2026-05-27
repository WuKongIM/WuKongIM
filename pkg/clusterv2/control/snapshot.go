package control

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
