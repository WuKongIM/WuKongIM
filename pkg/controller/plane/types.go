package plane

import (
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

type NodeStatus = controllermeta.NodeStatus
type TaskStatus = controllermeta.TaskStatus

const (
	NodeStatusAlive    = controllermeta.NodeStatusAlive
	NodeStatusSuspect  = controllermeta.NodeStatusSuspect
	NodeStatusDead     = controllermeta.NodeStatusDead
	NodeStatusDraining = controllermeta.NodeStatusDraining

	TaskStatusPending  = controllermeta.TaskStatusPending
	TaskStatusRetrying = controllermeta.TaskStatusRetrying
	TaskStatusFailed   = controllermeta.TaskStatusFailed
)

type PlannerConfig struct {
	// SlotCount is the initial physical slot fallback used before a hash-slot table exists.
	SlotCount uint32
	// ReplicaN is the desired replica count for bootstrap peer selection.
	ReplicaN int
	// RebalanceSkewThreshold is the minimum max-min slot replica skew before rebalancing.
	RebalanceSkewThreshold int
	// MaxTaskAttempts is reserved for task retry policies owned by the state machine.
	MaxTaskAttempts int
	// RetryBackoffBase is reserved for task retry policies owned by the state machine.
	RetryBackoffBase time.Duration
}

type PlannerState struct {
	Now         time.Time
	Nodes       map[uint64]controllermeta.ClusterNode
	Assignments map[uint32]controllermeta.SlotAssignment
	Runtime     map[uint32]controllermeta.SlotRuntimeView
	Tasks       map[uint32]controllermeta.ReconcileTask
	// PhysicalSlots is the authoritative physical slot set from the hash-slot table.
	PhysicalSlots  map[uint32]struct{}
	MigratingSlots map[uint32]struct{}
	// PauseRebalance disables opportunistic automatic Rebalance decisions while keeping Bootstrap/Repair active.
	PauseRebalance bool
	// LockedSlots prevents ordinary planner decisions for slots owned by an external coordinator.
	LockedSlots map[uint32]struct{}
}

type Decision struct {
	SlotID     uint32
	Assignment controllermeta.SlotAssignment
	Task       *controllermeta.ReconcileTask
	Degraded   bool
}
