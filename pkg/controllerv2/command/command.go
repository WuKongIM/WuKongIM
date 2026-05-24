package command

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// Kind identifies a durable ControllerV2 Raft command.
type Kind string

const (
	// KindInitClusterState creates the first durable cluster-state file.
	KindInitClusterState Kind = "init_cluster_state"
	// KindUpsertNode inserts or replaces a durable node record.
	KindUpsertNode Kind = "upsert_node"
	// KindUpdateControllerVoters replaces the desired Controller voter set.
	KindUpdateControllerVoters Kind = "update_controller_voters"
	// KindUpsertSlotAssignmentAndTask atomically writes a slot assignment and reconcile task.
	KindUpsertSlotAssignmentAndTask Kind = "upsert_slot_assignment_and_task"
	// KindCompleteTask removes a completed active reconcile task.
	KindCompleteTask Kind = "complete_task"
	// KindFailTask records a failed attempt while keeping the task active.
	KindFailTask Kind = "fail_task"
	// KindReplaceHashSlotTable replaces the hash-slot routing table.
	KindReplaceHashSlotTable Kind = "replace_hash_slot_table"
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
	// Assignment contains the desired slot assignment to upsert.
	Assignment *state.SlotAssignment `json:"assignment,omitempty"`
	// Task contains the active reconcile task to upsert.
	Task *state.ReconcileTask `json:"task,omitempty"`
	// TaskResult identifies a completed or failed task attempt.
	TaskResult *TaskResult `json:"task_result,omitempty"`
	// HashSlots contains a replacement hash-slot table.
	HashSlots *state.HashSlotTable `json:"hash_slots,omitempty"`
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
	// Err contains the task failure reason for KindFailTask.
	Err string `json:"err,omitempty"`
	// FinishedAt records when the worker observed the task result.
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
