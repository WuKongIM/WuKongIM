package control

import (
	"time"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// Role describes one durable node capability in cluster control state.
type Role string

const (
	// RoleController marks a node that can participate in Controller coordination.
	RoleController Role = "controller"
	// RoleData marks a node that can host physical Slot replicas and Channel data replicas.
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

// NodeHealthFreshness describes whether a health report is usable for decisions.
type NodeHealthFreshness string

const (
	// NodeHealthFresh means the latest durable report is within its TTL.
	NodeHealthFresh NodeHealthFreshness = "fresh"
	// NodeHealthStale means the latest durable report exists but exceeded its TTL.
	NodeHealthStale NodeHealthFreshness = "stale"
	// NodeHealthMissing means no durable report exists for the node.
	NodeHealthMissing NodeHealthFreshness = "missing"
)

// NodeHealth describes durable low-frequency health evidence for one node.
type NodeHealth struct {
	// Status is the reported runtime health state.
	Status NodeStatus
	// Freshness reports whether the durable health evidence is usable.
	Freshness NodeHealthFreshness
	// RuntimeReady reports whether the node can serve foreground cluster traffic.
	RuntimeReady bool
	// ObservedControlRevision is the latest Controller revision observed by the node.
	ObservedControlRevision uint64
	// ObservedSlotRevision is the latest local Slot runtime revision observed by the node.
	ObservedSlotRevision uint64
	// ReportSeq is a node-local sequence used for diagnostics.
	ReportSeq uint64
	// ReportedAt is the Controller leader timestamp for the report.
	ReportedAt time.Time
	// ReportAge is the age of the report at snapshot build time.
	ReportAge time.Duration
	// ReportTTL is the TTL used to classify freshness.
	ReportTTL time.Duration
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string
}

// ChannelDataPlaneLease describes this node's local Channel append visibility lease.
type ChannelDataPlaneLease struct {
	// LastVisibleAt is the last successful control visibility timestamp for local append admission.
	LastVisibleAt time.Time
	// TTL is the maximum age accepted for local append admission.
	TTL time.Duration
	// Ready reports whether LastVisibleAt is currently within TTL.
	Ready bool
}

// NodeJoinState describes durable node lifecycle in the cluster control read model.
type NodeJoinState string

const (
	// NodeJoinStateActive means the node may receive new placement.
	NodeJoinStateActive NodeJoinState = "active"
	// NodeJoinStateJoining means the node is visible but not assignment-ready.
	NodeJoinStateJoining NodeJoinState = "joining"
	// NodeJoinStateLeaving means the node is draining and must not receive new placement.
	NodeJoinStateLeaving NodeJoinState = "leaving"
	// NodeJoinStateRemoved means the node identity is retained as a tombstone.
	NodeJoinStateRemoved NodeJoinState = "removed"
)

// TaskKind identifies one reconcile workflow kind.
type TaskKind = controller.TaskKind

const (
	// TaskKindBootstrap creates the initial physical Slot replica group.
	TaskKindBootstrap = controller.TaskKindBootstrap
	// TaskKindLeaderTransfer records an operator-requested Slot Raft leadership transfer.
	TaskKindLeaderTransfer = controller.TaskKindLeaderTransfer
	// TaskKindSlotReplicaMove moves one physical Slot voter from SourceNode to TargetNode.
	TaskKindSlotReplicaMove = controller.TaskKindSlotReplicaMove
)

// TaskStep identifies the current step inside a task workflow.
type TaskStep = controller.TaskStep

const (
	// TaskStepCreateSlot creates or verifies a physical Slot replica group.
	TaskStepCreateSlot = controller.TaskStepCreateSlot
	// TaskStepTransferLeader asks Slot Raft to move leadership away from the observed source.
	TaskStepTransferLeader = controller.TaskStepTransferLeader
	// TaskStepOpenLearner opens the target replica as a non-voting Slot learner.
	TaskStepOpenLearner = controller.TaskStepOpenLearner
	// TaskStepAddLearner adds the target node to the Slot Raft learner set.
	TaskStepAddLearner = controller.TaskStepAddLearner
	// TaskStepPromoteLearner promotes the target learner into the Slot Raft voter set.
	TaskStepPromoteLearner = controller.TaskStepPromoteLearner
	// TaskStepRemoveVoter removes the source node from the Slot Raft voter set.
	TaskStepRemoveVoter = controller.TaskStepRemoveVoter
	// TaskStepCommitAssignment commits the durable Slot assignment after Slot Raft converges.
	TaskStepCommitAssignment = controller.TaskStepCommitAssignment
)

// TaskStatus describes whether a durable reconcile task is actionable.
type TaskStatus = controller.TaskStatus

const (
	// TaskStatusPending means the task is waiting for a worker.
	TaskStatusPending = controller.TaskStatusPending
	// TaskStatusRunning means the task is actively being attempted.
	TaskStatusRunning = controller.TaskStatusRunning
	// TaskStatusFailed means the task remains active after a failed attempt.
	TaskStatusFailed = controller.TaskStatusFailed
)

// TaskCompletionPolicy describes how participant progress gates completion.
type TaskCompletionPolicy = controller.TaskCompletionPolicy

const (
	// TaskCompletionPolicySingleObserver means one eligible observer may complete the task.
	TaskCompletionPolicySingleObserver = controller.TaskCompletionPolicySingleObserver
	// TaskCompletionPolicyAllTargetPeers means every target peer must report done.
	TaskCompletionPolicyAllTargetPeers = controller.TaskCompletionPolicyAllTargetPeers
)

// TaskParticipantStatus describes one node's local task progress.
type TaskParticipantStatus = controller.TaskParticipantStatus

const (
	// TaskParticipantStatusPending means the participant is not complete.
	TaskParticipantStatusPending = controller.TaskParticipantStatusPending
	// TaskParticipantStatusDone means the participant completed local work.
	TaskParticipantStatusDone = controller.TaskParticipantStatusDone
	// TaskParticipantStatusFailed means the participant's latest local attempt failed.
	TaskParticipantStatusFailed = controller.TaskParticipantStatusFailed
)

// TaskParticipantProgress describes one node's local progress.
type TaskParticipantProgress = controller.TaskParticipantProgress

// OpsMCPState is the bounded desired state for the embedded operations MCP.
type OpsMCPState struct {
	// Enabled reports whether Manager listeners should accept MCP requests.
	Enabled bool
	// OwnerNodeID is the single cluster node that executes MCP tools.
	OwnerNodeID uint64
	// ProfileFenceUntilUnixMillis prevents profiles during an owner-generation transition.
	ProfileFenceUntilUnixMillis int64
	// Credentials contains token identifiers and one-way digests.
	Credentials []OpsMCPCredential
}

// OpsMCPCredential is one opaque bearer token verifier.
type OpsMCPCredential struct {
	// ID is the non-secret identifier encoded in the token.
	ID string
	// DigestSHA256 is the lowercase digest of the complete token.
	DigestSHA256 string
	// CreatedAtUnixMillis records when the token was created.
	CreatedAtUnixMillis int64
}

// Snapshot is the cluster control read model consumed by data-plane modules.
type Snapshot struct {
	// ClusterID is the stable Controller cluster identity carried by this snapshot.
	ClusterID string
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
	// OpsMCP contains desired state for the embedded operations MCP.
	OpsMCP *OpsMCPState
	// ChannelDataPlaneLease is this node's local append-admission visibility lease.
	ChannelDataPlaneLease ChannelDataPlaneLease
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
	// Health contains durable low-frequency runtime health evidence.
	Health NodeHealth
	// JoinState is the durable membership lifecycle state.
	JoinState NodeJoinState
	// CapacityWeight is the relative planner capacity for future placement decisions.
	CapacityWeight uint32
}

// NodeSchedulableForPlacement reports whether a node can receive new data placement.
// A schedulable node must be data-role, effectively active, fresh alive, and runtime-ready.
func NodeSchedulableForPlacement(node Node) bool {
	if !hasRole(node.Roles, RoleData) {
		return false
	}
	if effectiveJoinState(node.JoinState) != NodeJoinStateActive {
		return false
	}
	return node.Health.Freshness == NodeHealthFresh &&
		node.Health.Status == NodeAlive &&
		node.Health.RuntimeReady
}

// SlotAssignment describes desired replicas for one physical Slot.
type SlotAssignment struct {
	// SlotID is the non-zero physical Slot ID.
	SlotID uint32
	// DesiredPeers are active or leaving data node IDs that should host this Slot.
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
	TaskID string `json:"task_id"`
	// SlotID is the affected physical Slot.
	SlotID uint32 `json:"slot_id"`
	// Kind identifies the reconcile workflow kind.
	Kind TaskKind `json:"kind"`
	// Step identifies the current workflow step.
	Step TaskStep `json:"step"`
	// SourceNode is the optional node that currently owns source data.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the primary node that should execute this task when set.
	TargetNode uint64 `json:"target_node,omitempty"`
	// TargetPeers are the peer IDs this task should converge.
	TargetPeers []uint64 `json:"target_peers,omitempty"`
	// CompletionPolicy controls how participant progress gates completion.
	CompletionPolicy TaskCompletionPolicy `json:"completion_policy,omitempty"`
	// ParticipantProgress records per-node local progress for barrier tasks.
	ParticipantProgress []TaskParticipantProgress `json:"participant_progress,omitempty"`
	// ConfigEpoch ties this task to a Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch,omitempty"`
	// Attempt counts global task attempts, including failed attempts.
	Attempt uint32 `json:"attempt"`
	// Status describes whether this task is actionable.
	Status TaskStatus `json:"status"`
	// LastError stores the bounded error from the most recent failed attempt.
	LastError string `json:"last_error,omitempty"`
	// PhaseIndex advances after each observed Slot Raft config step.
	PhaseIndex uint32 `json:"phase_index,omitempty"`
	// ObservedConfigIndex is the Slot Raft applied index that proved the current phase.
	ObservedConfigIndex uint64 `json:"observed_config_index,omitempty"`
	// ObservedVoters stores the voter set observed for the current phase.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
	// ObservedLearners stores the learner set observed for the current phase.
	ObservedLearners []uint64 `json:"observed_learners,omitempty"`
}

// BuildNodeHealth maps durable Controller health evidence into the control snapshot read model.
func BuildNodeHealth(report controller.NodeHealthReport, exists bool, now time.Time, ttl time.Duration) NodeHealth {
	if !exists {
		return NodeHealth{Freshness: NodeHealthMissing, ReportTTL: ttl}
	}
	reportedAt := time.UnixMilli(report.ReportedAtUnixMilli).UTC()
	age := now.Sub(reportedAt)
	freshness := NodeHealthFresh
	if age < 0 {
		age = 0
		freshness = NodeHealthStale
	} else if ttl <= 0 || age > ttl {
		freshness = NodeHealthStale
	}
	return NodeHealth{
		Status:                  NodeStatus(report.Status),
		Freshness:               freshness,
		RuntimeReady:            report.RuntimeReady,
		ObservedControlRevision: report.ObservedControlRevision,
		ObservedSlotRevision:    report.ObservedSlotRevision,
		ReportSeq:               report.ReportSeq,
		ReportedAt:              reportedAt,
		ReportAge:               age,
		ReportTTL:               ttl,
		ErrorCode:               report.ErrorCode,
	}
}
