package management

import (
	"context"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultControllerTaskAuditLimit is the manager default retained task audit list size.
	DefaultControllerTaskAuditLimit = 200
	// MaxControllerTaskAuditLimit is the largest accepted retained task audit list size.
	MaxControllerTaskAuditLimit = 200
)

// ErrControllerTaskAuditUnavailable reports that retained task audit history is not wired.
var ErrControllerTaskAuditUnavailable = errors.New("internalv2/usecase/management: controller task audit unavailable")

// ErrControllerTaskAuditNotFound reports that retained history for a task is absent.
var ErrControllerTaskAuditNotFound = errors.New("internalv2/usecase/management: controller task audit not found")

// ControllerTaskAuditReader reads retained ControllerV2 task audit history.
type ControllerTaskAuditReader interface {
	// ListControllerTaskAudits returns retained task audit snapshots.
	ListControllerTaskAudits(context.Context, ControllerTaskAuditListRequest) (ControllerTaskAuditListResponse, error)
	// ControllerTaskAuditEvents returns retained events for one task id.
	ControllerTaskAuditEvents(context.Context, string) (ControllerTaskAuditEventsResponse, error)
}

// ControllerTaskAuditListRequest contains manager filters for retained task history.
type ControllerTaskAuditListRequest struct {
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
	// Limit bounds returned task histories.
	Limit int
}

// ControllerTaskAuditListResponse is a retained task history list.
type ControllerTaskAuditListResponse struct {
	// Total is the number of matching retained histories before limit.
	Total int
	// Limit is the normalized response limit.
	Limit int
	// Truncated reports whether matching histories exceeded Limit.
	Truncated bool
	// Items contains newest-first retained task histories.
	Items []ControllerTaskAuditSnapshot
}

// ControllerTaskAuditSnapshot is the manager-facing retained task projection.
type ControllerTaskAuditSnapshot struct {
	// TaskID identifies the ControllerV2 reconcile task.
	TaskID string
	// Kind is the durable task workflow kind.
	Kind string
	// Status is the latest retained task status.
	Status string
	// Step is the latest retained workflow step.
	Step string
	// SlotID identifies the physical Slot affected by this task.
	SlotID uint32
	// LeaderID is the preferred or observed leader associated with the task.
	LeaderID uint64
	// SourceNode is the source node for move-like tasks.
	SourceNode uint64
	// TargetNode is the primary target node for the task.
	TargetNode uint64
	// FirstAppliedRaftIndex is the oldest retained event index for this task.
	FirstAppliedRaftIndex uint64
	// LastAppliedRaftIndex is the newest retained event index for this task.
	LastAppliedRaftIndex uint64
	// StartedAt is the timestamp of the oldest retained event for this task.
	StartedAt time.Time
	// CompletedAt is the completion timestamp when retained.
	CompletedAt time.Time
	// EventCount is the number of retained events for this task.
	EventCount int
	// Truncated reports whether older events were dropped.
	Truncated bool
	// Summary is the latest retained compact event summary.
	Summary string
	// LastReason is the latest retained failure or no-op reason.
	LastReason string
}

// ControllerTaskAuditEvent is one manager-facing retained task event.
type ControllerTaskAuditEvent struct {
	// EventID is the stable idempotency key for this persisted event.
	EventID string
	// TaskID identifies the ControllerV2 reconcile task.
	TaskID string
	// Type classifies the task audit edge.
	Type string
	// Kind is the durable task workflow kind.
	Kind string
	// Status is the task status after this event when known.
	Status string
	// SlotID identifies the physical Slot affected by this task.
	SlotID uint32
	// LeaderID is the preferred or observed leader associated with the task.
	LeaderID uint64
	// SourceNode is the source node for move-like tasks.
	SourceNode uint64
	// TargetNode is the primary target node for the task.
	TargetNode uint64
	// AppliedRaftIndex is the ControllerV2 Raft index that produced this event.
	AppliedRaftIndex uint64
	// AppliedRaftTerm is the ControllerV2 Raft term that produced this event.
	AppliedRaftTerm uint64
	// CommandKind is the ControllerV2 command kind that produced this event.
	CommandKind string
	// ParticipantNode is the reporting node for participant progress events.
	ParticipantNode uint64
	// OccurredAt is the local timestamp attached to this event.
	OccurredAt time.Time
	// Summary is a compact human-readable event summary.
	Summary string
	// Reason is a compact failure or no-op reason when available.
	Reason string
	// Details stores bounded structured context for operator drill-down.
	Details map[string]any
}

// ControllerTaskAuditEventsResponse is one retained task event timeline.
type ControllerTaskAuditEventsResponse struct {
	// Task is the retained task summary.
	Task ControllerTaskAuditSnapshot
	// Events contains oldest-first retained events.
	Events []ControllerTaskAuditEvent
	// Truncated reports whether older events were dropped.
	Truncated bool
}

// ListControllerTaskAudits returns retained ControllerV2 task histories.
func (a *App) ListControllerTaskAudits(ctx context.Context, req ControllerTaskAuditListRequest) (ControllerTaskAuditListResponse, error) {
	limit, err := normalizeControllerTaskAuditLimit(req.Limit)
	if err != nil {
		return ControllerTaskAuditListResponse{}, err
	}
	if a == nil || a.controllerTaskAudit == nil {
		return ControllerTaskAuditListResponse{}, ErrControllerTaskAuditUnavailable
	}
	req.Limit = limit
	return a.controllerTaskAudit.ListControllerTaskAudits(ctx, req)
}

// ControllerTaskAuditEvents returns retained events for one task id.
func (a *App) ControllerTaskAuditEvents(ctx context.Context, taskID string) (ControllerTaskAuditEventsResponse, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ControllerTaskAuditEventsResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.controllerTaskAudit == nil {
		return ControllerTaskAuditEventsResponse{}, ErrControllerTaskAuditUnavailable
	}
	return a.controllerTaskAudit.ControllerTaskAuditEvents(ctx, taskID)
}

func normalizeControllerTaskAuditLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxControllerTaskAuditLimit {
		return 0, metadb.ErrInvalidArgument
	}
	if limit == 0 {
		return DefaultControllerTaskAuditLimit, nil
	}
	return limit, nil
}
