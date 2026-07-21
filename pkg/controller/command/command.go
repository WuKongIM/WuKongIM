package command

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// Kind identifies a durable Controller Raft command.
type Kind string

const (
	// KindInitClusterState creates the first durable cluster-state file.
	KindInitClusterState Kind = "init_cluster_state"
	// KindUpsertNode inserts or replaces a durable node record.
	KindUpsertNode Kind = "upsert_node"
	// KindUpdateControllerVoters replaces the desired Controller voter set.
	KindUpdateControllerVoters Kind = "update_controller_voters"
	// KindPromoteControllerVoter atomically declares a live Controller Raft voter in durable cluster state.
	KindPromoteControllerVoter Kind = "promote_controller_voter"
	// KindUpsertSlotAssignmentAndTask atomically writes a slot assignment and reconcile task.
	KindUpsertSlotAssignmentAndTask Kind = "upsert_slot_assignment_and_task"
	// KindUpsertSlotReplicaMoveTask writes a staged Slot replica move task without changing assignment peers.
	KindUpsertSlotReplicaMoveTask Kind = "upsert_slot_replica_move_task"
	// KindAdvanceSlotReplicaMovePhase records the next safe Slot replica move phase.
	KindAdvanceSlotReplicaMovePhase Kind = "advance_slot_replica_move_phase"
	// KindCommitSlotReplicaMove atomically replaces the Slot assignment and completes the move task.
	KindCommitSlotReplicaMove Kind = "commit_slot_replica_move"
	// KindCompleteTask removes a completed active reconcile task.
	KindCompleteTask Kind = "complete_task"
	// KindFailTask records a failed attempt while keeping the task active.
	KindFailTask Kind = "fail_task"
	// KindReportTaskProgress records one participant's local progress.
	KindReportTaskProgress Kind = "report_task_progress"
	// KindReportNodeHealth stores a bounded low-frequency node health report.
	KindReportNodeHealth Kind = "report_node_health"
	// KindReplaceHashSlotTable replaces the hash-slot routing table.
	KindReplaceHashSlotTable Kind = "replace_hash_slot_table"
	// KindReplaceBackupCoordinationState replaces bounded backup coordination metadata.
	KindReplaceBackupCoordinationState Kind = "replace_backup_coordination_state"
	// KindReplaceRestoreCoordinationState replaces bounded explicit recovery metadata.
	KindReplaceRestoreCoordinationState Kind = "replace_restore_coordination_state"
)

// Command is the versioned payload replicated through Controller Raft.
type Command struct {
	// Kind selects which mutation handler applies this command.
	Kind Kind `json:"kind"`
	// IssuedAt records when the proposer created the command.
	IssuedAt time.Time `json:"issued_at"`
	// ExpectedRevision is an optional compare-and-set guard for planner commands.
	ExpectedRevision *uint64 `json:"expected_revision,omitempty"`
	// Init contains the initial cluster-state payload for bootstrap.
	Init *InitClusterState `json:"init,omitempty"`
	// Node contains a node record for KindUpsertNode.
	Node *state.Node `json:"node,omitempty"`
	// Controllers contains the replacement Controller voter set.
	Controllers []state.ControllerVoter `json:"controllers,omitempty"`
	// ControllerVoterPromotion carries a proven Controller Raft membership promotion.
	ControllerVoterPromotion *ControllerVoterPromotion `json:"controller_voter_promotion,omitempty"`
	// Assignment contains the desired slot assignment to upsert.
	Assignment *state.SlotAssignment `json:"assignment,omitempty"`
	// Task contains the active reconcile task to upsert.
	Task *state.ReconcileTask `json:"task,omitempty"`
	// SlotReplicaMovePhase carries a fenced Slot replica move phase update.
	SlotReplicaMovePhase *SlotReplicaMovePhaseAdvance `json:"slot_replica_move_phase,omitempty"`
	// SlotReplicaMoveCommit carries a fenced Slot replica move assignment commit.
	SlotReplicaMoveCommit *SlotReplicaMoveCommit `json:"slot_replica_move_commit,omitempty"`
	// TaskResult identifies a completed or failed task attempt.
	TaskResult *TaskResult `json:"task_result,omitempty"`
	// TaskProgress records one participant's local progress for barrier tasks.
	TaskProgress *TaskProgress `json:"task_progress,omitempty"`
	// NodeHealth contains a health report for KindReportNodeHealth.
	NodeHealth *state.NodeHealthReport `json:"node_health,omitempty"`
	// HashSlots contains a replacement hash-slot table.
	HashSlots *state.HashSlotTable `json:"hash_slots,omitempty"`
	// Backup contains replacement bounded backup coordination metadata.
	Backup *state.BackupCoordinationState `json:"backup,omitempty"`
	// Restore contains replacement bounded explicit recovery metadata.
	Restore *state.RestoreCoordinationState `json:"restore,omitempty"`
}

// ControllerVoterPromotion records a proven promotion of one node into Controller Raft voting membership.
type ControllerVoterPromotion struct {
	// TargetNodeID is the active cluster node that has been promoted in live Controller Raft.
	TargetNodeID uint64 `json:"target_node_id"`
	// TargetAddr is the stable control-plane address expected in durable node state.
	TargetAddr string `json:"target_addr"`
	// ExpectedPreviousVoters fences the state command to the voter set observed before the promotion.
	ExpectedPreviousVoters []uint64 `json:"expected_previous_voters"`
	// ObservedConfigIndex is the Controller Raft config index that proved the target voter.
	ObservedConfigIndex uint64 `json:"observed_config_index"`
	// ObservedVoters is the live Controller Raft voter set observed after learner promotion.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
}

// SlotReplicaMovePhaseAdvance records an observed Slot Raft config phase.
type SlotReplicaMovePhaseAdvance struct {
	// TaskID fences the phase update to the active move task.
	TaskID string `json:"task_id"`
	// SlotID fences the phase update to the physical Slot.
	SlotID uint32 `json:"slot_id"`
	// ConfigEpoch fences the phase update to the assignment epoch being moved.
	ConfigEpoch uint64 `json:"config_epoch"`
	// Attempt fences the phase update to the active global task attempt.
	Attempt uint32 `json:"attempt"`
	// ExpectedPhaseIndex rejects stale phase observations.
	ExpectedPhaseIndex uint32 `json:"expected_phase_index"`
	// NextStep is the next durable move workflow step.
	NextStep state.TaskStep `json:"next_step"`
	// ObservedConfigIndex is the Slot Raft applied index proving this phase.
	ObservedConfigIndex uint64 `json:"observed_config_index"`
	// ObservedVoters is the Slot Raft voter set observed for this phase.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
	// ObservedLearners is the Slot Raft learner set observed for this phase.
	ObservedLearners []uint64 `json:"observed_learners,omitempty"`
}

// SlotReplicaMoveCommit fences the final durable assignment replacement.
type SlotReplicaMoveCommit struct {
	// TaskID fences the commit to the active move task.
	TaskID string `json:"task_id"`
	// SlotID fences the commit to the physical Slot.
	SlotID uint32 `json:"slot_id"`
	// ConfigEpoch fences the commit to the assignment epoch being moved.
	ConfigEpoch uint64 `json:"config_epoch"`
	// Attempt fences the commit to the active global task attempt.
	Attempt uint32 `json:"attempt"`
	// ObservedConfigIndex is the Slot Raft applied index proving the final voter set.
	ObservedConfigIndex uint64 `json:"observed_config_index"`
	// ObservedVoters is the Slot Raft voter set observed by the committing executor.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
}

// InitClusterState is the committed payload that creates the first cluster state.
type InitClusterState struct {
	// ClusterID is the stable identity shared by every node in the cluster.
	ClusterID string `json:"cluster_id"`
	// Config stores durable cluster sizing and placement defaults.
	Config state.ClusterConfig `json:"config"`
	// Controllers lists the initial Controller Raft voters.
	Controllers []state.ControllerVoter `json:"controllers"`
	// Nodes lists the initial durable cluster members.
	Nodes []state.Node `json:"nodes"`
}

// TaskResult describes the result of a data-plane reconcile task attempt.
type TaskResult struct {
	// TaskID identifies the active task being completed or failed.
	TaskID string `json:"task_id"`
	// SlotID optionally confirms the slot affected by the result.
	SlotID uint32 `json:"slot_id,omitempty"`
	// TaskKind fences the result to the active task kind.
	TaskKind state.TaskKind `json:"task_kind,omitempty"`
	// ConfigEpoch fences the result to the active assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch,omitempty"`
	// Attempt fences the result to the active global task attempt.
	Attempt uint32 `json:"attempt"`
	// Err contains the task failure reason for KindFailTask.
	Err string `json:"err,omitempty"`
	// FinishedAt records when the worker observed the task result.
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// TaskProgress describes one participant's local progress for a barrier task.
type TaskProgress struct {
	// TaskID identifies the active task.
	TaskID string `json:"task_id"`
	// SlotID confirms the slot affected by the progress report.
	SlotID uint32 `json:"slot_id"`
	// TaskKind fences the report to the active task kind.
	TaskKind state.TaskKind `json:"task_kind"`
	// ConfigEpoch fences the report to the active assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// TaskAttempt fences the report to the active global task attempt.
	TaskAttempt uint32 `json:"task_attempt"`
	// ParticipantNodeID is the reporting participant.
	ParticipantNodeID uint64 `json:"participant_node_id"`
	// ParticipantAttempt fences the report to the participant's local attempt.
	ParticipantAttempt uint32 `json:"participant_attempt"`
	// Status is the participant's new progress status.
	Status state.TaskParticipantStatus `json:"status"`
	// Err contains the participant failure reason when Status is failed.
	Err string `json:"err,omitempty"`
	// FinishedAt records when the participant observed this progress state.
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
