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
	CommandKindAddSlot                 CommandKind = 14
	CommandKindRemoveSlot              CommandKind = 15
	CommandKindNodeStatusUpdate        CommandKind = 16
	CommandKindNodeJoin                CommandKind = 17
	CommandKindNodeJoinActivate        CommandKind = 18
	CommandKindNodeOnboardingJobUpdate CommandKind = 19
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
	// PreferredLeader is the non-zero soft leader target chosen before crossing the Raft command boundary.
	PreferredLeader uint64
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

// NodeJoinRequest records the durable metadata for a data node entering membership.
type NodeJoinRequest struct {
	// NodeID is the stable non-zero cluster identity being admitted.
	NodeID uint64
	// Name is the optional operator-facing display name for the node.
	Name string
	// Addr is the unique node RPC address used by controller readers.
	Addr string
	// CapacityWeight is normalized to a positive planner capacity before persistence.
	CapacityWeight int
	// JoinedAt is the controller time used for initial join and heartbeat metadata.
	JoinedAt time.Time
}

// NodeJoinActivateRequest promotes a joined node after runtime full sync is observed.
type NodeJoinActivateRequest struct {
	// NodeID is the joined node to activate.
	NodeID uint64
	// ActivatedAt is the controller observation time for the full-sync activation edge.
	ActivatedAt time.Time
}

// NodeOnboardingJobUpdate persists a job transition and optional Slot task mutation atomically.
type NodeOnboardingJobUpdate struct {
	// Job is the full durable job state to upsert.
	Job *controllermeta.NodeOnboardingJob
	// ExpectedStatus is optional; when set, the state machine no-ops if the stored job has moved elsewhere.
	ExpectedStatus *controllermeta.OnboardingJobStatus
	// Assignment is written with Task when starting a move; nil means no assignment mutation.
	Assignment *controllermeta.SlotAssignment
	// Task is written with Assignment when starting a move; nil means no task mutation.
	Task *controllermeta.ReconcileTask
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
	NodeJoin         *NodeJoinRequest
	NodeJoinActivate *NodeJoinActivateRequest
	NodeOnboarding   *NodeOnboardingJobUpdate
}
