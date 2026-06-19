package control

import cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"

// Role describes one durable node capability in clusterv2 control state.
type Role string

const (
	// RoleController marks a node that can participate in Controller coordination.
	RoleController Role = "controller"
	// RoleData marks a node that can host physical Slot replicas and ChannelV2 data replicas.
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
type TaskKind = cv2.TaskKind

const (
	// TaskKindBootstrap creates the initial physical Slot replica group.
	TaskKindBootstrap = cv2.TaskKindBootstrap
)

// TaskStep identifies the current step inside a task workflow.
type TaskStep = cv2.TaskStep

const (
	// TaskStepCreateSlot creates or verifies a physical Slot replica group.
	TaskStepCreateSlot = cv2.TaskStepCreateSlot
)

// TaskStatus describes whether a durable reconcile task is actionable.
type TaskStatus = cv2.TaskStatus

const (
	// TaskStatusPending means the task is waiting for a worker.
	TaskStatusPending = cv2.TaskStatusPending
	// TaskStatusRunning means the task is actively being attempted.
	TaskStatusRunning = cv2.TaskStatusRunning
	// TaskStatusFailed means the task remains active after a failed attempt.
	TaskStatusFailed = cv2.TaskStatusFailed
)

// TaskCompletionPolicy describes how participant progress gates completion.
type TaskCompletionPolicy = cv2.TaskCompletionPolicy

const (
	// TaskCompletionPolicySingleObserver means one eligible observer may complete the task.
	TaskCompletionPolicySingleObserver = cv2.TaskCompletionPolicySingleObserver
	// TaskCompletionPolicyAllTargetPeers means every target peer must report done.
	TaskCompletionPolicyAllTargetPeers = cv2.TaskCompletionPolicyAllTargetPeers
)

// TaskParticipantStatus describes one node's local task progress.
type TaskParticipantStatus = cv2.TaskParticipantStatus

const (
	// TaskParticipantStatusPending means the participant is not complete.
	TaskParticipantStatusPending = cv2.TaskParticipantStatusPending
	// TaskParticipantStatusDone means the participant completed local work.
	TaskParticipantStatusDone = cv2.TaskParticipantStatusDone
	// TaskParticipantStatusFailed means the participant's latest local attempt failed.
	TaskParticipantStatusFailed = cv2.TaskParticipantStatusFailed
)

// TaskParticipantProgress describes one node's local progress.
type TaskParticipantProgress = cv2.TaskParticipantProgress

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
	// Step identifies the current workflow step.
	Step TaskStep
	// SourceNode is the optional node that currently owns source data.
	SourceNode uint64
	// TargetNode is the primary node that should execute this task when set.
	TargetNode uint64
	// TargetPeers are the peer IDs this task should converge.
	TargetPeers []uint64
	// CompletionPolicy controls how participant progress gates completion.
	CompletionPolicy TaskCompletionPolicy
	// ParticipantProgress records per-node local progress for barrier tasks.
	ParticipantProgress []TaskParticipantProgress
	// ConfigEpoch ties this task to a Slot assignment epoch.
	ConfigEpoch uint64
	// Attempt counts global task attempts, including failed attempts.
	Attempt uint32
	// Status describes whether this task is actionable.
	Status TaskStatus
	// LastError stores the bounded error from the most recent failed attempt.
	LastError string
}
