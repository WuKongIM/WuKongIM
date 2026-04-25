package plane

import (
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

type CommandKind uint8

const (
	CommandKindUnknown CommandKind = iota
	CommandKindNodeHeartbeat
	CommandKindOperatorRequest
	CommandKindEvaluateTimeouts
	CommandKindTaskResult
	CommandKindAssignmentTaskUpdate
	CommandKindStartMigration
	CommandKindAdvanceMigration
	CommandKindFinalizeMigration
	CommandKindAbortMigration
	CommandKindAddSlot          CommandKind = 14
	CommandKindRemoveSlot       CommandKind = 15
	CommandKindNodeStatusUpdate CommandKind = 16
)

type OperatorKind uint8

const (
	OperatorKindUnknown OperatorKind = iota
	OperatorMarkNodeDraining
	OperatorResumeNode
)

type AgentReport struct {
	NodeID               uint64
	Addr                 string
	ObservedAt           time.Time
	CapacityWeight       int
	HashSlotTableVersion uint64
	Runtime              *controllermeta.SlotRuntimeView
}

type OperatorRequest struct {
	Kind   OperatorKind
	NodeID uint64
}

type TaskAdvance struct {
	SlotID  uint32
	Attempt uint32
	Now     time.Time
	Err     error
}

type MigrationRequest struct {
	HashSlot uint16
	Source   uint64
	Target   uint64
	Phase    uint8
}

type AddSlotRequest struct {
	NewSlotID uint64
	Peers     []uint64
}

type RemoveSlotRequest struct {
	SlotID uint64
}

type NodeStatusTransition struct {
	NodeID         uint64
	NewStatus      controllermeta.NodeStatus
	ExpectedStatus *controllermeta.NodeStatus
	EvaluatedAt    time.Time
	Addr           string
	CapacityWeight int
}

type NodeStatusUpdate struct {
	Transitions []NodeStatusTransition
}

type Command struct {
	Kind             CommandKind
	Report           *AgentReport
	Op               *OperatorRequest
	Advance          *TaskAdvance
	Assignment       *controllermeta.SlotAssignment
	Task             *controllermeta.ReconcileTask
	Migration        *MigrationRequest
	AddSlot          *AddSlotRequest
	RemoveSlot       *RemoveSlotRequest
	NodeStatusUpdate *NodeStatusUpdate
}
