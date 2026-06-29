package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerControllerTaskAuditsResponse is the retained Controller task history list response.
type ManagerControllerTaskAuditsResponse struct {
	// Total is the number of matching retained histories before limit.
	Total int `json:"total"`
	// Limit is the normalized response limit.
	Limit int `json:"limit"`
	// Truncated reports whether matching histories exceeded Limit.
	Truncated bool `json:"truncated"`
	// Items contains retained task history rows.
	Items []ManagerControllerTaskAuditSnapshot `json:"items"`
}

// ManagerControllerTaskAuditSnapshot is one retained Controller task history row.
type ManagerControllerTaskAuditSnapshot struct {
	// TaskID identifies the ControllerV2 reconcile task.
	TaskID string `json:"task_id"`
	// Kind is the durable task workflow kind.
	Kind string `json:"kind"`
	// Status is the latest retained task status.
	Status string `json:"status"`
	// SlotID identifies the physical Slot affected by this task.
	SlotID uint32 `json:"slot_id"`
	// LeaderID is the preferred or observed leader associated with the task.
	LeaderID uint64 `json:"leader_id"`
	// SourceNode is the source node for move-like tasks.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the primary target node for the task.
	TargetNode uint64 `json:"target_node"`
	// FirstAppliedRaftIndex is the oldest retained event index for this task.
	FirstAppliedRaftIndex uint64 `json:"first_applied_raft_index"`
	// LastAppliedRaftIndex is the newest retained event index for this task.
	LastAppliedRaftIndex uint64 `json:"last_applied_raft_index"`
	// StartedAt is the timestamp of the oldest retained event for this task.
	StartedAt *time.Time `json:"started_at"`
	// CompletedAt is the completion timestamp when retained.
	CompletedAt *time.Time `json:"completed_at"`
	// EventCount is the number of retained events for this task.
	EventCount int `json:"event_count"`
	// Truncated reports whether older events were dropped.
	Truncated bool `json:"truncated"`
	// Summary is the latest retained compact event summary.
	Summary string `json:"summary"`
	// LastReason is the latest retained failure or no-op reason.
	LastReason string `json:"last_reason"`
}

// ManagerControllerTaskAuditEventsResponse is one retained Controller task timeline.
type ManagerControllerTaskAuditEventsResponse struct {
	// Task is the retained task summary.
	Task ManagerControllerTaskAuditSnapshot `json:"task"`
	// Events contains oldest-first retained events.
	Events []ManagerControllerTaskAuditEvent `json:"events"`
	// Truncated reports whether older task events were dropped.
	Truncated bool `json:"truncated"`
}

// ManagerControllerTaskAuditEvent is one retained Controller task audit event.
type ManagerControllerTaskAuditEvent struct {
	// EventID is the stable idempotency key for this event.
	EventID string `json:"event_id"`
	// TaskID identifies the ControllerV2 reconcile task.
	TaskID string `json:"task_id"`
	// Type classifies the task audit edge.
	Type string `json:"type"`
	// Kind is the durable task workflow kind.
	Kind string `json:"kind"`
	// Status is the task status after this event when known.
	Status string `json:"status"`
	// SlotID identifies the physical Slot affected by this task.
	SlotID uint32 `json:"slot_id"`
	// LeaderID is the preferred or observed leader associated with the task.
	LeaderID uint64 `json:"leader_id"`
	// SourceNode is the source node for move-like tasks.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the primary target node for the task.
	TargetNode uint64 `json:"target_node"`
	// AppliedRaftIndex is the ControllerV2 Raft index that produced this event.
	AppliedRaftIndex uint64 `json:"applied_raft_index"`
	// AppliedRaftTerm is the ControllerV2 Raft term that produced this event.
	AppliedRaftTerm uint64 `json:"applied_raft_term"`
	// CommandKind is the ControllerV2 command kind that produced this event.
	CommandKind string `json:"command_kind"`
	// ParticipantNode is the reporting node for participant progress events.
	ParticipantNode uint64 `json:"participant_node"`
	// OccurredAt is the local timestamp attached to this event.
	OccurredAt time.Time `json:"occurred_at"`
	// Summary is a compact human-readable event summary.
	Summary string `json:"summary"`
	// Reason is a compact failure or no-op reason when available.
	Reason string `json:"reason"`
	// Details stores bounded structured context for operator drill-down.
	Details map[string]any `json:"details,omitempty"`
}

func (s *Server) handleControllerTaskAudits(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseControllerTaskAuditsRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task audit query")
		return
	}
	page, err := s.management.ListControllerTaskAudits(c.Request.Context(), req)
	if err != nil {
		writeControllerTaskAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, controllerTaskAuditsDTO(page))
}

func (s *Server) handleControllerTaskAuditEvents(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	taskID := c.Param("task_id")
	if strings.TrimSpace(taskID) == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task audit request")
		return
	}
	events, err := s.management.ControllerTaskAuditEvents(c.Request.Context(), taskID)
	if err != nil {
		writeControllerTaskAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, controllerTaskAuditEventsDTO(events))
}

func parseControllerTaskAuditsRequest(c *gin.Context) (managementusecase.ControllerTaskAuditListRequest, error) {
	kind := c.Query("kind")
	if !validControllerTaskKind(kind) {
		return managementusecase.ControllerTaskAuditListRequest{}, errors.New("invalid kind")
	}
	status := c.Query("status")
	if !validControllerTaskAuditStatus(status) {
		return managementusecase.ControllerTaskAuditListRequest{}, errors.New("invalid status")
	}
	slotID, err := parseOptionalControllerTaskSlotID(c.Query("slot_id"))
	if err != nil {
		return managementusecase.ControllerTaskAuditListRequest{}, err
	}
	nodeID, err := parseOptionalControllerTaskNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ControllerTaskAuditListRequest{}, err
	}
	limit, err := parseControllerTaskAuditLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ControllerTaskAuditListRequest{}, err
	}
	return managementusecase.ControllerTaskAuditListRequest{
		Kind:    kind,
		Status:  status,
		Keyword: c.Query("keyword"),
		SlotID:  slotID,
		NodeID:  nodeID,
		Limit:   limit,
	}, nil
}

func validControllerTaskAuditStatus(status string) bool {
	switch status {
	case "", string(control.TaskStatusPending), string(control.TaskStatusRunning), string(control.TaskStatusFailed), "completed":
		return true
	default:
		return false
	}
}

func parseControllerTaskAuditLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > managementusecase.MaxControllerTaskAuditLimit {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func controllerTaskAuditsDTO(page managementusecase.ControllerTaskAuditListResponse) ManagerControllerTaskAuditsResponse {
	items := make([]ManagerControllerTaskAuditSnapshot, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, controllerTaskAuditSnapshotDTO(item))
	}
	return ManagerControllerTaskAuditsResponse{Total: page.Total, Limit: page.Limit, Truncated: page.Truncated, Items: items}
}

func controllerTaskAuditEventsDTO(resp managementusecase.ControllerTaskAuditEventsResponse) ManagerControllerTaskAuditEventsResponse {
	events := make([]ManagerControllerTaskAuditEvent, 0, len(resp.Events))
	for _, event := range resp.Events {
		events = append(events, controllerTaskAuditEventDTO(event))
	}
	return ManagerControllerTaskAuditEventsResponse{Task: controllerTaskAuditSnapshotDTO(resp.Task), Events: events, Truncated: resp.Truncated}
}

func controllerTaskAuditSnapshotDTO(item managementusecase.ControllerTaskAuditSnapshot) ManagerControllerTaskAuditSnapshot {
	return ManagerControllerTaskAuditSnapshot{
		TaskID:                item.TaskID,
		Kind:                  item.Kind,
		Status:                item.Status,
		SlotID:                item.SlotID,
		LeaderID:              item.LeaderID,
		SourceNode:            item.SourceNode,
		TargetNode:            item.TargetNode,
		FirstAppliedRaftIndex: item.FirstAppliedRaftIndex,
		LastAppliedRaftIndex:  item.LastAppliedRaftIndex,
		StartedAt:             timePtr(item.StartedAt),
		CompletedAt:           timePtr(item.CompletedAt),
		EventCount:            item.EventCount,
		Truncated:             item.Truncated,
		Summary:               item.Summary,
		LastReason:            item.LastReason,
	}
}

func controllerTaskAuditEventDTO(event managementusecase.ControllerTaskAuditEvent) ManagerControllerTaskAuditEvent {
	return ManagerControllerTaskAuditEvent{
		EventID:          event.EventID,
		TaskID:           event.TaskID,
		Type:             event.Type,
		Kind:             event.Kind,
		Status:           event.Status,
		SlotID:           event.SlotID,
		LeaderID:         event.LeaderID,
		SourceNode:       event.SourceNode,
		TargetNode:       event.TargetNode,
		AppliedRaftIndex: event.AppliedRaftIndex,
		AppliedRaftTerm:  event.AppliedRaftTerm,
		CommandKind:      event.CommandKind,
		ParticipantNode:  event.ParticipantNode,
		OccurredAt:       event.OccurredAt,
		Summary:          event.Summary,
		Reason:           event.Reason,
		Details:          event.Details,
	}
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	v := value
	return &v
}

func writeControllerTaskAuditError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task audit request")
	case errors.Is(err, managementusecase.ErrControllerTaskAuditNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "controller task audit not found")
	case errors.Is(err, managementusecase.ErrControllerTaskAuditUnavailable), controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller task audit unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
