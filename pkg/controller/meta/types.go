package meta

import (
	"errors"
	"time"
)

var (
	ErrClosed           = errors.New("controllermeta: closed")
	ErrNotFound         = errors.New("controllermeta: not found")
	ErrChecksumMismatch = errors.New("controllermeta: checksum mismatch")
	ErrCorruptValue     = errors.New("controllermeta: corrupt value")
	ErrInvalidArgument  = errors.New("controllermeta: invalid argument")
)

type NodeStatus uint8

const (
	NodeStatusUnknown NodeStatus = iota
	NodeStatusAlive
	NodeStatusSuspect
	NodeStatusDead
	NodeStatusDraining
)

// NodeRole identifies the durable role a node serves in the cluster.
type NodeRole uint8

const (
	// NodeRoleUnknown is the zero value and is normalized before persistence.
	NodeRoleUnknown NodeRole = iota
	// NodeRoleData marks a node that owns data-plane replicas.
	NodeRoleData
	// NodeRoleControllerVoter marks a node that votes in the controller Raft group.
	NodeRoleControllerVoter
)

// NodeJoinState records the durable membership lifecycle state for a node.
type NodeJoinState uint8

const (
	// NodeJoinStateUnknown is the zero value and is normalized before persistence.
	NodeJoinStateUnknown NodeJoinState = iota
	// NodeJoinStateJoining marks a node whose membership is not active yet.
	NodeJoinStateJoining
	// NodeJoinStateActive marks a node that is allowed to participate.
	NodeJoinStateActive
	// NodeJoinStateRejected marks a node that was denied membership.
	NodeJoinStateRejected
)

type TaskKind uint8

const (
	TaskKindUnknown TaskKind = iota
	TaskKindBootstrap
	TaskKindRepair
	TaskKindRebalance
)

type TaskStep uint8

const (
	TaskStepUnknown TaskStep = iota
	TaskStepAddLearner
	TaskStepCatchUp
	TaskStepPromote
	TaskStepTransferLeader
	TaskStepRemoveOld
)

type TaskStatus uint8

const (
	TaskStatusUnknown TaskStatus = iota
	TaskStatusPending
	TaskStatusRetrying
	TaskStatusFailed
)

// ClusterNode is the durable controller metadata for one cluster node.
type ClusterNode struct {
	// NodeID is the stable non-zero cluster identity for the node.
	NodeID uint64
	// Name is the operator-facing node name persisted with membership.
	Name string
	// Addr is the node RPC address used by controller metadata readers.
	Addr string
	// Role records the explicit cluster membership role for the node.
	Role NodeRole
	// JoinState records the explicit membership lifecycle state for the node.
	JoinState NodeJoinState
	// Status records the observed health state used by planner decisions.
	Status NodeStatus
	// JoinedAt records when the node first entered explicit membership.
	JoinedAt time.Time
	// LastHeartbeatAt records the latest durable heartbeat observation.
	LastHeartbeatAt time.Time
	// CapacityWeight is the positive relative planner capacity for the node.
	CapacityWeight int
}

type SlotAssignment struct {
	// SlotID is the non-zero physical slot identity controlled by this assignment.
	SlotID uint32
	// DesiredPeers is the canonical voter set the data plane should converge to.
	DesiredPeers []uint64
	// ConfigEpoch increments whenever the desired peer set changes.
	ConfigEpoch uint64
	// BalanceVersion increments on opportunistic rebalance moves to preserve fair ordering.
	BalanceVersion uint64
}

type SlotRuntimeView struct {
	// SlotID is the physical slot identity described by this observation.
	SlotID uint32
	// CurrentPeers is the observed voter or learner set currently known to the slot runtime.
	CurrentPeers []uint64
	// LeaderID is the observed slot Raft leader, or zero when no stable leader is known.
	LeaderID uint64
	// HealthyVoters is the number of observed voters that are healthy enough for quorum checks.
	HealthyVoters uint32
	// HasQuorum reports whether the current runtime view can safely accept automatic changes.
	HasQuorum bool
	// ObservedConfigEpoch is the assignment epoch observed by the reporting runtime.
	ObservedConfigEpoch uint64
	// LastReportAt records when the controller leader received this runtime observation.
	LastReportAt time.Time
}

type ControllerMembership struct {
	Peers []uint64
}

type ReconcileTask struct {
	// SlotID is the physical slot this task reconciles.
	SlotID uint32
	// Kind identifies why the reconciliation is running.
	Kind TaskKind
	// Step records the next data-plane operation to execute.
	Step TaskStep
	// SourceNode is the node being replaced or moved away from, when applicable.
	SourceNode uint64
	// TargetNode is the node being added or promoted by this task.
	TargetNode uint64
	// Attempt is incremented after failed task result reports.
	Attempt uint32
	// NextRunAt is the earliest time a retrying task may be executed again.
	NextRunAt time.Time
	// Status records whether the task is pending, retrying, or failed.
	Status TaskStatus
	// LastError stores the latest executor error for operator inspection.
	LastError string
}
