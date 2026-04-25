package manager

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

// TasksResponse is the manager task list response body.
type TasksResponse struct {
	// Total is the number of returned manager tasks.
	Total int `json:"total"`
	// Items contains the ordered manager task DTO list.
	Items []TaskDTO `json:"items"`
}

// TaskDTO is the manager-facing task response item.
type TaskDTO struct {
	// SlotID is the physical slot identifier associated with the task.
	SlotID uint32 `json:"slot_id"`
	// Kind is the stringified task kind.
	Kind string `json:"kind"`
	// Step is the stringified task step.
	Step string `json:"step"`
	// Status is the stringified task status.
	Status string `json:"status"`
	// SourceNode is the source node when the task has one.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the target node when the task has one.
	TargetNode uint64 `json:"target_node"`
	// Attempt is the current task attempt count.
	Attempt uint32 `json:"attempt"`
	// NextRunAt is the next retry schedule when the task is retrying.
	NextRunAt *time.Time `json:"next_run_at"`
	// LastError is the last recorded task error message.
	LastError string `json:"last_error"`
}

// TaskDetailDTO is the manager-facing task detail response body.
type TaskDetailDTO struct {
	TaskDTO
	// Slot contains lightweight slot context for the task.
	Slot TaskSlotDTO `json:"slot"`
}

// TaskSlotDTO contains lightweight slot context for a task detail response.
type TaskSlotDTO struct {
	// State contains lightweight derived slot summaries.
	State SlotStateDTO `json:"state"`
	// Assignment contains the desired slot placement view.
	Assignment SlotAssignmentDTO `json:"assignment"`
	// Runtime contains the observed slot runtime view.
	Runtime SlotRuntimeDTO `json:"runtime"`
}

func (s *Server) handleTasks(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	items, err := s.management.ListTasks(c.Request.Context())
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	out := make([]TaskDTO, 0, len(items))
	for _, item := range items {
		out = append(out, taskDTO(item))
	}
	c.JSON(http.StatusOK, TasksResponse{Total: len(items), Items: out})
}

func (s *Server) handleTask(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}
	item, err := s.management.GetTask(c.Request.Context(), slotID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "task not found")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, TaskDetailDTO{
		TaskDTO: taskDTO(item.Task),
		Slot:    taskSlotDTO(item.Slot),
	})
}

func taskDTO(item managementusecase.Task) TaskDTO {
	return TaskDTO{
		SlotID:     item.SlotID,
		Kind:       item.Kind,
		Step:       item.Step,
		Status:     item.Status,
		SourceNode: item.SourceNode,
		TargetNode: item.TargetNode,
		Attempt:    item.Attempt,
		NextRunAt:  item.NextRunAt,
		LastError:  item.LastError,
	}
}

func taskSlotDTO(item managementusecase.Slot) TaskSlotDTO {
	return TaskSlotDTO{
		State: SlotStateDTO{
			Quorum: item.State.Quorum,
			Sync:   item.State.Sync,
		},
		Assignment: SlotAssignmentDTO{
			DesiredPeers:   append([]uint64(nil), item.Assignment.DesiredPeers...),
			ConfigEpoch:    item.Assignment.ConfigEpoch,
			BalanceVersion: item.Assignment.BalanceVersion,
		},
		Runtime: SlotRuntimeDTO{
			CurrentPeers:        append([]uint64(nil), item.Runtime.CurrentPeers...),
			LeaderID:            item.Runtime.LeaderID,
			HealthyVoters:       item.Runtime.HealthyVoters,
			HasQuorum:           item.Runtime.HasQuorum,
			ObservedConfigEpoch: item.Runtime.ObservedConfigEpoch,
			LastReportAt:        item.Runtime.LastReportAt,
		},
	}
}

func parseSlotIDParam(raw string) (uint32, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return uint32(value), nil
}
