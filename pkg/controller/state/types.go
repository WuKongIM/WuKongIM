package state

import "time"

const (
	// CurrentSchemaVersion is the only cluster-state schema version supported by Controller v1.
	CurrentSchemaVersion uint32 = 1
	// CurrentHashSlotTableVersion is the hash-slot routing table schema version.
	CurrentHashSlotTableVersion uint32 = 1
)

// NodeRole describes a durable node capability.
type NodeRole string

const (
	// NodeRoleControllerVoter marks a node that participates in Controller Raft voting.
	NodeRoleControllerVoter NodeRole = "controller_voter"
	// NodeRoleData marks a node that can host physical slot replicas.
	NodeRoleData NodeRole = "data"
)

// NodeJoinState describes whether a node is allowed to serve desired assignments.
type NodeJoinState string

const (
	// NodeJoinStateActive means the node is an active cluster member.
	NodeJoinStateActive NodeJoinState = "active"
	// NodeJoinStateJoining means the node is being introduced but is not yet assignment-ready.
	NodeJoinStateJoining NodeJoinState = "joining"
	// NodeJoinStateLeaving means the node is being drained from assignments.
	NodeJoinStateLeaving NodeJoinState = "leaving"
	// NodeJoinStateRemoved means the node identity is retained as a tombstone and must not receive assignments.
	NodeJoinStateRemoved NodeJoinState = "removed"
)

// NodeStatus describes durable node health as last written through Controller Raft.
type NodeStatus string

const (
	// NodeStatusAlive means the node is considered available by durable control-plane state.
	NodeStatusAlive NodeStatus = "alive"
	// NodeStatusSuspect means the node may be unavailable.
	NodeStatusSuspect NodeStatus = "suspect"
	// NodeStatusDown means the node is considered unavailable.
	NodeStatusDown NodeStatus = "down"
)

// NodeHealthReport stores one bounded low-frequency runtime health report.
type NodeHealthReport struct {
	// NodeID is the reporting node identity.
	NodeID uint64 `json:"node_id"`
	// Status is the reported runtime health status.
	Status NodeStatus `json:"status"`
	// RuntimeReady reports whether the node can serve foreground cluster traffic.
	RuntimeReady bool `json:"runtime_ready"`
	// ObservedControlRevision is the latest logical Controller revision observed by the node.
	ObservedControlRevision uint64 `json:"observed_control_revision"`
	// ObservedSlotRevision is the latest local Slot runtime revision observed by the node.
	ObservedSlotRevision uint64 `json:"observed_slot_revision,omitempty"`
	// ReportSeq is a node-local sequence used for diagnostics.
	ReportSeq uint64 `json:"report_seq"`
	// ReportedAtUnixMilli is filled by the Controller leader when the report is proposed.
	ReportedAtUnixMilli int64 `json:"reported_at_unix_milli"`
	// AppliedRaftIndex is the Controller Raft index that stored this report.
	AppliedRaftIndex uint64 `json:"applied_raft_index,omitempty"`
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string `json:"error_code,omitempty"`
}

// ControllerRole describes a Controller Raft membership role.
type ControllerRole string

const (
	// ControllerRoleVoter is a Controller Raft voting member.
	ControllerRoleVoter ControllerRole = "voter"
)

// TaskKind describes the reconcile workflow represented by a durable task.
type TaskKind string

const (
	// TaskKindBootstrap converges an initial physical slot assignment.
	TaskKindBootstrap TaskKind = "bootstrap"
	// TaskKindLeaderTransfer records an operator-requested Slot Raft leadership transfer.
	TaskKindLeaderTransfer TaskKind = "leader_transfer"
	// TaskKindSlotReplicaMove moves one physical Slot voter from SourceNode to TargetNode.
	TaskKindSlotReplicaMove TaskKind = "slot_replica_move"
)

// TaskStep describes the current step inside a reconcile workflow.
type TaskStep string

const (
	// TaskStepCreateSlot creates or verifies the slot replica group for an assignment.
	TaskStepCreateSlot TaskStep = "create_slot"
	// TaskStepTransferLeader asks Slot Raft to move leadership away from the observed source.
	TaskStepTransferLeader TaskStep = "transfer_leader"
	// TaskStepOpenLearner opens the target replica as a non-voting Slot learner.
	TaskStepOpenLearner TaskStep = "open_learner"
	// TaskStepAddLearner adds the target node to the Slot Raft learner set.
	TaskStepAddLearner TaskStep = "add_learner"
	// TaskStepPromoteLearner promotes the target learner into the Slot Raft voter set.
	TaskStepPromoteLearner TaskStep = "promote_learner"
	// TaskStepRemoveVoter removes the source node from the Slot Raft voter set.
	TaskStepRemoveVoter TaskStep = "remove_voter"
	// TaskStepCommitAssignment commits the durable Slot assignment after Slot Raft converges.
	TaskStepCommitAssignment TaskStep = "commit_assignment"
)

// TaskStatus describes whether a durable reconcile task is still actionable.
type TaskStatus string

const (
	// TaskStatusPending means the task has not started or is waiting for a worker.
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusRunning means the task is actively being attempted.
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusFailed means the task remains active but its latest attempt failed.
	TaskStatusFailed TaskStatus = "failed"
)

// TaskCompletionPolicy describes how participant progress becomes task completion.
type TaskCompletionPolicy string

const (
	// TaskCompletionPolicySingleObserver means one eligible executor can complete the task.
	TaskCompletionPolicySingleObserver TaskCompletionPolicy = "single_observer"
	// TaskCompletionPolicyAllTargetPeers requires every target peer to report done.
	TaskCompletionPolicyAllTargetPeers TaskCompletionPolicy = "all_target_peers"
)

// TaskParticipantStatus describes one node's local task progress.
type TaskParticipantStatus string

const (
	// TaskParticipantStatusPending means the participant has not completed its local work.
	TaskParticipantStatusPending TaskParticipantStatus = "pending"
	// TaskParticipantStatusDone means the participant completed its local work.
	TaskParticipantStatusDone TaskParticipantStatus = "done"
	// TaskParticipantStatusFailed means the participant's latest local attempt failed.
	TaskParticipantStatusFailed TaskParticipantStatus = "failed"
)

// TaskParticipantProgress stores one participant's progress for the current global task attempt.
type TaskParticipantProgress struct {
	// NodeID is the participant node identity.
	NodeID uint64 `json:"node_id"`
	// Attempt counts this participant's local attempts within the current global task attempt.
	Attempt uint32 `json:"attempt"`
	// Status is the participant's current local progress.
	Status TaskParticipantStatus `json:"status"`
	// LastError stores the bounded error from the latest failed local attempt.
	LastError string `json:"last_error,omitempty"`
}

// ClusterState is the canonical durable Controller cluster-state document.
type ClusterState struct {
	// SchemaVersion selects the durable JSON schema used by this file.
	SchemaVersion uint32 `json:"schema_version"`
	// ClusterID is the stable identity shared by all nodes in the cluster.
	ClusterID string `json:"cluster_id"`
	// Revision is the logical state version advanced by successful state changes.
	Revision uint64 `json:"revision"`
	// AppliedRaftIndex is the Controller Raft log index that produced this file.
	AppliedRaftIndex uint64 `json:"applied_raft_index"`
	// UpdatedAt is the UTC timestamp of the last logical state update.
	UpdatedAt time.Time `json:"updated_at"`
	// Config stores durable cluster sizing and placement defaults.
	Config ClusterConfig `json:"config"`
	// Controllers lists the desired Controller Raft voters.
	Controllers []ControllerVoter `json:"controllers"`
	// Nodes lists durable cluster members.
	Nodes []Node `json:"nodes"`
	// Slots lists desired physical slot assignments.
	Slots []SlotAssignment `json:"slots"`
	// NodeHealthReports stores one compact health report per node.
	NodeHealthReports []NodeHealthReport `json:"node_health_reports,omitempty"`
	// HashSlots maps hash-slot ranges to physical slot IDs.
	HashSlots HashSlotTable `json:"hash_slots"`
	// Tasks lists active reconcile tasks required to converge the desired state.
	Tasks []ReconcileTask `json:"tasks"`
	// Backup stores bounded cluster backup coordination metadata when configured.
	Backup *BackupCoordinationState `json:"backup,omitempty"`
	// Restore stores the explicit fresh-cluster recovery plan when configured.
	Restore *RestoreCoordinationState `json:"restore,omitempty"`
	// Checksum protects the canonical JSON payload excluding this field.
	Checksum string `json:"checksum"`
}

// ClusterConfig stores durable cluster sizing and placement defaults.
type ClusterConfig struct {
	// SlotCount is the number of physical slots managed by the cluster.
	SlotCount uint32 `json:"slot_count"`
	// HashSlotCount is the number of hash slots in the routing table.
	HashSlotCount uint16 `json:"hash_slot_count"`
	// ReplicaCount is the desired replica count for each physical slot.
	ReplicaCount uint16 `json:"replica_count"`
	// DefaultCapacityWeight is used by planners when a node omits its own weight.
	DefaultCapacityWeight uint32 `json:"default_capacity_weight,omitempty"`
}

// ControllerVoter identifies a Controller Raft voting member.
type ControllerVoter struct {
	// NodeID references a node with the controller_voter role.
	NodeID uint64 `json:"node_id"`
	// Addr is the stable Controller RPC address for this voter.
	Addr string `json:"addr"`
	// Role is the Controller Raft role for this member.
	Role ControllerRole `json:"role"`
}

// Node is a durable cluster membership record.
type Node struct {
	// NodeID is the non-zero stable node identity.
	NodeID uint64 `json:"node_id"`
	// Name is a human-readable node label.
	Name string `json:"name,omitempty"`
	// Addr is the stable node RPC address used by control-plane components.
	Addr string `json:"addr"`
	// Roles is the durable capability set for this node.
	Roles []NodeRole `json:"roles"`
	// JoinState controls whether planners may place new assignments on this node.
	JoinState NodeJoinState `json:"join_state"`
	// Status is durable node health as written by Controller Raft.
	Status NodeStatus `json:"status"`
	// CapacityWeight is the planner placement weight; zero normalizes to one.
	CapacityWeight uint32 `json:"capacity_weight"`
}

// SlotAssignment describes the desired replica placement for one physical slot.
type SlotAssignment struct {
	// SlotID is the physical slot ID in the range 1..ClusterConfig.SlotCount.
	SlotID uint32 `json:"slot_id"`
	// DesiredPeers are unique data-capable node IDs that are active or being drained.
	DesiredPeers []uint64 `json:"desired_peers"`
	// ConfigEpoch changes whenever the desired assignment changes.
	ConfigEpoch uint64 `json:"config_epoch"`
	// PreferredLeader is the voter preferred for the initial Slot Raft election.
	PreferredLeader uint64 `json:"preferred_leader,omitempty"`
}

// HashSlotTable maps hash slots to physical slot IDs.
type HashSlotTable struct {
	// Version selects the durable hash-slot table schema.
	Version uint32 `json:"version"`
	// SlotCount is the number of hash slots covered by the ranges.
	SlotCount uint16 `json:"slot_count"`
	// Ranges is a contiguous, non-overlapping list ordered by From.
	Ranges []HashSlotRange `json:"ranges"`
}

// HashSlotRange maps an inclusive hash-slot range to one physical slot.
type HashSlotRange struct {
	// From is the inclusive lower hash-slot bound.
	From uint16 `json:"from"`
	// To is the inclusive upper hash-slot bound.
	To uint16 `json:"to"`
	// SlotID is the physical slot target for this hash range.
	SlotID uint32 `json:"slot_id"`
}

// ReconcileTask is an active durable task needed to converge data-plane state.
type ReconcileTask struct {
	// TaskID is the unique durable task identity.
	TaskID string `json:"task_id"`
	// SlotID is the physical slot affected by this task.
	SlotID uint32 `json:"slot_id"`
	// Kind identifies the reconcile workflow.
	Kind TaskKind `json:"kind"`
	// Step identifies the current workflow step.
	Step TaskStep `json:"step"`
	// SourceNode is the optional node that currently owns data for move-like tasks.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the primary node targeted by this task.
	TargetNode uint64 `json:"target_node,omitempty"`
	// TargetPeers is the desired peer set this task must converge.
	TargetPeers []uint64 `json:"target_peers,omitempty"`
	// CompletionPolicy controls how participant progress gates global completion.
	CompletionPolicy TaskCompletionPolicy `json:"completion_policy,omitempty"`
	// ParticipantProgress records per-node local progress for barrier-style tasks.
	ParticipantProgress []TaskParticipantProgress `json:"participant_progress,omitempty"`
	// ConfigEpoch is the assignment epoch this task is tied to.
	ConfigEpoch uint64 `json:"config_epoch,omitempty"`
	// Attempt counts task attempts, including failed attempts.
	Attempt uint32 `json:"attempt"`
	// Status is the active task status.
	Status TaskStatus `json:"status"`
	// LastError stores the bounded error from the most recent failed attempt.
	LastError string `json:"last_error,omitempty"`
	// PhaseIndex advances after each externally observed Slot Raft config step.
	PhaseIndex uint32 `json:"phase_index,omitempty"`
	// ObservedConfigIndex is the Slot Raft applied index that proved the current phase.
	ObservedConfigIndex uint64 `json:"observed_config_index,omitempty"`
	// ObservedVoters stores the voter set observed for the current phase.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
	// ObservedLearners stores the learner set observed for the current phase.
	ObservedLearners []uint64 `json:"observed_learners,omitempty"`
}
