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
	SlotCount              uint32
	ReplicaN               int
	RebalanceSkewThreshold int
	MaxTaskAttempts        int
	RetryBackoffBase       time.Duration
}

type PlannerState struct {
	Now            time.Time
	Nodes          map[uint64]controllermeta.ClusterNode
	Assignments    map[uint32]controllermeta.SlotAssignment
	Runtime        map[uint32]controllermeta.SlotRuntimeView
	Tasks          map[uint32]controllermeta.ReconcileTask
	MigratingSlots map[uint32]struct{}
}

type Decision struct {
	SlotID     uint32
	Assignment controllermeta.SlotAssignment
	Task       *controllermeta.ReconcileTask
	Degraded   bool
}
