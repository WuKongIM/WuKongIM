package taskaudit

import (
	"errors"
	"time"
)

const (
	// DefaultMaxTasks bounds retained task histories.
	DefaultMaxTasks = 200
	// DefaultMaxEventsPerTask bounds retained events for one task.
	DefaultMaxEventsPerTask = 50
	// DefaultDetailLimitBytes bounds JSON details stored with one event.
	DefaultDetailLimitBytes = 16 << 10
)

var (
	// ErrUnavailable indicates the local audit store cannot serve reads or writes.
	ErrUnavailable = errors.New("taskaudit: unavailable")
	// ErrTaskNotFound indicates that no retained task history matches the id.
	ErrTaskNotFound = errors.New("taskaudit: task not found")
)

// EventType describes one stable task audit event kind.
type EventType string

const (
	// EventCreated records creation of a durable task.
	EventCreated EventType = "created"
	// EventRunning records a task entering active work.
	EventRunning EventType = "running"
	// EventParticipantProgress records one participant's progress.
	EventParticipantProgress EventType = "participant_progress"
	// EventFailed records a failed task attempt.
	EventFailed EventType = "failed"
	// EventCompleted records task completion.
	EventCompleted EventType = "completed"
	// EventSnapshot records a startup snapshot/backfill event.
	EventSnapshot EventType = "snapshot"
)

// Options controls the bounded JSONL audit store.
type Options struct {
	// MaxTasks bounds retained task histories.
	MaxTasks int
	// MaxEventsPerTask bounds retained events per task.
	MaxEventsPerTask int
	// DetailLimitBytes bounds JSON details stored with one event.
	DetailLimitBytes int
	// Now supplies timestamps for events without OccurredAt.
	Now func() time.Time
}

// Event is one persisted ControllerV2 task audit fact.
type Event struct {
	// EventID is the stable idempotency key for this persisted event.
	EventID string `json:"event_id"`
	// TaskID identifies the ControllerV2 reconcile task.
	TaskID string `json:"task_id"`
	// Type classifies the task audit edge.
	Type EventType `json:"type"`
	// Kind is the durable task workflow kind.
	Kind string `json:"kind,omitempty"`
	// Status is the task status after this event when known.
	Status string `json:"status,omitempty"`
	// SlotID identifies the physical Slot affected by this task.
	SlotID uint32 `json:"slot_id,omitempty"`
	// LeaderID is the preferred or observed leader associated with the task.
	LeaderID uint64 `json:"leader_id,omitempty"`
	// SourceNode is the source node for move-like tasks.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the primary target node for the task.
	TargetNode uint64 `json:"target_node,omitempty"`
	// AppliedRaftIndex is the ControllerV2 Raft index that produced this event.
	AppliedRaftIndex uint64 `json:"applied_raft_index"`
	// AppliedRaftTerm is the ControllerV2 Raft term that produced this event.
	AppliedRaftTerm uint64 `json:"applied_raft_term,omitempty"`
	// CommandKind is the ControllerV2 command kind that produced this event.
	CommandKind string `json:"command_kind,omitempty"`
	// ParticipantNode is the reporting node for participant progress events.
	ParticipantNode uint64 `json:"participant_node,omitempty"`
	// OccurredAt is the local timestamp attached to this event.
	OccurredAt time.Time `json:"occurred_at"`
	// Summary is a compact human-readable event summary.
	Summary string `json:"summary,omitempty"`
	// Reason is a compact failure or no-op reason when available.
	Reason string `json:"reason,omitempty"`
	// Details stores bounded structured context for operator drill-down.
	Details map[string]any `json:"details,omitempty"`
}

// Snapshot is the query projection for one retained task history.
type Snapshot struct {
	// TaskID identifies the ControllerV2 reconcile task.
	TaskID string `json:"task_id"`
	// Kind is the durable task workflow kind.
	Kind string `json:"kind,omitempty"`
	// Status is the latest retained task status.
	Status string `json:"status,omitempty"`
	// SlotID identifies the physical Slot affected by this task.
	SlotID uint32 `json:"slot_id,omitempty"`
	// LeaderID is the preferred or observed leader associated with the task.
	LeaderID uint64 `json:"leader_id,omitempty"`
	// SourceNode is the source node for move-like tasks.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the primary target node for the task.
	TargetNode uint64 `json:"target_node,omitempty"`
	// FirstAppliedRaftIndex is the oldest retained event index for this task.
	FirstAppliedRaftIndex uint64 `json:"first_applied_raft_index"`
	// LastAppliedRaftIndex is the newest retained event index for this task.
	LastAppliedRaftIndex uint64 `json:"last_applied_raft_index"`
	// StartedAt is the timestamp of the oldest retained event for this task.
	StartedAt time.Time `json:"started_at,omitempty"`
	// CompletedAt is the completion timestamp when a retained completion event exists.
	CompletedAt time.Time `json:"completed_at,omitempty"`
	// EventCount is the number of retained events for this task.
	EventCount int `json:"event_count"`
	// Truncated reports whether older events for this task were dropped.
	Truncated bool `json:"truncated"`
	// Summary is the latest retained compact event summary.
	Summary string `json:"summary,omitempty"`
	// LastReason is the latest retained failure or no-op reason.
	LastReason string `json:"last_reason,omitempty"`
}

// ListRequest filters retained task audit snapshots.
type ListRequest struct {
	// Kind filters by durable task workflow kind.
	Kind string
	// Status filters by latest retained task status.
	Status string
	// Keyword filters task id, summary, and latest reason case-insensitively.
	Keyword string
	// SlotID filters by physical Slot id.
	SlotID uint32
	// NodeID filters by leader, source, or target node id.
	NodeID uint64
	// Limit bounds the number of snapshots returned.
	Limit int
}

// ListResponse contains retained task audit snapshots sorted newest first.
type ListResponse struct {
	// Total is the number of retained snapshots matching the filter before limit.
	Total int
	// Limit is the normalized response item limit.
	Limit int
	// Truncated reports whether matching snapshots exceeded Limit.
	Truncated bool
	// Items contains newest-first retained task snapshots.
	Items []Snapshot
}

// EventsResponse contains one retained task timeline sorted oldest first.
type EventsResponse struct {
	// Task is the retained snapshot for the requested task id.
	Task Snapshot
	// Events contains retained task events sorted oldest first.
	Events []Event
	// Truncated reports whether older task events were dropped.
	Truncated bool
}
