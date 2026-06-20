package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerControllerTasksResponse is the manager Controller task list response body.
type ManagerControllerTasksResponse struct {
	// Total is the number of matching active tasks before the limit is applied.
	Total int `json:"total"`
	// Items contains active Controller task rows.
	Items []ManagerControllerTask `json:"items"`
}

// ManagerControllerTask is one manager-facing active Controller task row.
type ManagerControllerTask struct {
	// TaskID is the durable task identity.
	TaskID string `json:"task_id"`
	// SlotID is the affected physical Slot.
	SlotID uint32 `json:"slot_id"`
	// Kind is the reconcile workflow kind.
	Kind string `json:"kind"`
	// Step is the current workflow step.
	Step string `json:"step"`
	// Status is the task state.
	Status string `json:"status"`
	// SourceNode is the optional source node for move-like tasks.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the primary task target when set.
	TargetNode uint64 `json:"target_node"`
	// TargetPeers are the peers expected to participate.
	TargetPeers []uint64 `json:"target_peers"`
	// CompletionPolicy describes how participant progress gates completion.
	CompletionPolicy string `json:"completion_policy"`
	// ConfigEpoch ties the task to a Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// Attempt is the global task attempt.
	Attempt uint32 `json:"attempt"`
	// LastError is the latest task-level error.
	LastError string `json:"last_error"`
	// Participants contains per-node task progress.
	Participants []ManagerControllerTaskParticipant `json:"participants"`
}

// ManagerControllerTaskParticipant is one node's Controller task progress row.
type ManagerControllerTaskParticipant struct {
	// NodeID is the participant node identity.
	NodeID uint64 `json:"node_id"`
	// Attempt is the participant-local attempt.
	Attempt uint32 `json:"attempt"`
	// Status is the participant progress state.
	Status string `json:"status"`
	// LastError is the latest participant-level error.
	LastError string `json:"last_error"`
}

func (s *Server) handleControllerTasks(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseControllerTasksRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task query")
		return
	}
	page, err := s.management.ListControllerTasks(c.Request.Context(), req)
	if err != nil {
		writeControllerTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, controllerTasksDTO(page))
}

func (s *Server) handleControllerTask(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	taskID := c.Param("task_id")
	if strings.TrimSpace(taskID) == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task request")
		return
	}
	task, err := s.management.ControllerTask(c.Request.Context(), taskID)
	if err != nil {
		writeControllerTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, controllerTaskDTO(task))
}

func parseControllerTasksRequest(c *gin.Context) (managementusecase.ListControllerTasksRequest, error) {
	kind := c.Query("kind")
	if !validControllerTaskKind(kind) {
		return managementusecase.ListControllerTasksRequest{}, errors.New("invalid kind")
	}
	status := c.Query("status")
	if !validControllerTaskStatus(status) {
		return managementusecase.ListControllerTasksRequest{}, errors.New("invalid status")
	}
	slotID, err := parseOptionalControllerTaskSlotID(c.Query("slot_id"))
	if err != nil {
		return managementusecase.ListControllerTasksRequest{}, err
	}
	nodeID, err := parseOptionalControllerTaskNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListControllerTasksRequest{}, err
	}
	limit, err := parseControllerTaskLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListControllerTasksRequest{}, err
	}
	return managementusecase.ListControllerTasksRequest{
		Kind:   kind,
		Status: status,
		SlotID: slotID,
		NodeID: nodeID,
		Limit:  limit,
	}, nil
}

func validControllerTaskKind(kind string) bool {
	switch kind {
	case "", string(control.TaskKindBootstrap), string(control.TaskKindLeaderTransfer):
		return true
	default:
		return false
	}
}

func validControllerTaskStatus(status string) bool {
	switch status {
	case "", string(control.TaskStatusPending), string(control.TaskStatusRunning), string(control.TaskStatusFailed):
		return true
	default:
		return false
	}
}

func parseOptionalControllerTaskSlotID(raw string) (uint32, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return uint32(value), nil
}

func parseOptionalControllerTaskNodeID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseControllerTaskLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > managementusecase.MaxControllerTaskLimit {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func controllerTasksDTO(page managementusecase.ListControllerTasksResponse) ManagerControllerTasksResponse {
	items := make([]ManagerControllerTask, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, controllerTaskDTO(item))
	}
	return ManagerControllerTasksResponse{Total: page.Total, Items: items}
}

func controllerTaskDTO(task managementusecase.ControllerTask) ManagerControllerTask {
	targetPeers := make([]uint64, 0, len(task.TargetPeers))
	targetPeers = append(targetPeers, task.TargetPeers...)
	return ManagerControllerTask{
		TaskID:           task.TaskID,
		SlotID:           task.SlotID,
		Kind:             task.Kind,
		Step:             task.Step,
		Status:           task.Status,
		SourceNode:       task.SourceNode,
		TargetNode:       task.TargetNode,
		TargetPeers:      targetPeers,
		CompletionPolicy: task.CompletionPolicy,
		ConfigEpoch:      task.ConfigEpoch,
		Attempt:          task.Attempt,
		LastError:        task.LastError,
		Participants:     controllerTaskParticipantDTOs(task.Participants),
	}
}

func controllerTaskParticipantDTOs(items []managementusecase.ControllerTaskParticipant) []ManagerControllerTaskParticipant {
	out := make([]ManagerControllerTaskParticipant, 0, len(items))
	for _, item := range items {
		out = append(out, ManagerControllerTaskParticipant{
			NodeID:    item.NodeID,
			Attempt:   item.Attempt,
			Status:    item.Status,
			LastError: item.LastError,
		})
	}
	return out
}

func writeControllerTaskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task request")
	case errors.Is(err, managementusecase.ErrControllerTaskNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "controller task not found")
	case controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller task read unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
