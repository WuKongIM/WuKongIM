package manager

import (
	"errors"
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

// SlotsResponse is the manager slot list response body.
type SlotsResponse struct {
	// Total is the number of returned manager slots.
	Total int `json:"total"`
	// Items contains the ordered manager slot DTO list.
	Items []SlotDTO `json:"items"`
}

// SlotDTO is the manager-facing slot response item.
type SlotDTO struct {
	// SlotID is the physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// State contains lightweight derived slot summaries.
	State SlotStateDTO `json:"state"`
	// Assignment contains the desired slot placement view.
	Assignment SlotAssignmentDTO `json:"assignment"`
	// Runtime contains the observed slot runtime view.
	Runtime SlotRuntimeDTO `json:"runtime"`
}

// SlotDetailDTO is the manager-facing slot detail response body.
type SlotDetailDTO struct {
	SlotDTO
	// Task contains the current reconcile task summary when one exists.
	Task *SlotTaskDTO `json:"task"`
}

// SlotStateDTO contains derived slot list state fields.
type SlotStateDTO struct {
	// Quorum summarizes whether the slot currently has quorum.
	Quorum string `json:"quorum"`
	// Sync summarizes whether runtime peers/config match the assignment.
	Sync string `json:"sync"`
}

// SlotAssignmentDTO contains desired slot placement fields.
type SlotAssignmentDTO struct {
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64 `json:"desired_peers"`
	// ConfigEpoch is the desired slot config epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// BalanceVersion is the desired slot balance generation.
	BalanceVersion uint64 `json:"balance_version"`
}

// SlotRuntimeDTO contains observed slot runtime fields.
type SlotRuntimeDTO struct {
	// CurrentPeers is the currently observed voter set.
	CurrentPeers []uint64 `json:"current_peers"`
	// LeaderID is the observed slot leader.
	LeaderID uint64 `json:"leader_id"`
	// HealthyVoters is the observed healthy voter count.
	HealthyVoters uint32 `json:"healthy_voters"`
	// HasQuorum reports whether the slot currently has quorum.
	HasQuorum bool `json:"has_quorum"`
	// ObservedConfigEpoch is the observed runtime config epoch.
	ObservedConfigEpoch uint64 `json:"observed_config_epoch"`
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time `json:"last_report_at"`
}

// SlotTaskDTO contains the optional current task summary for slot detail.
type SlotTaskDTO struct {
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

func (s *Server) handleSlots(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	items, err := s.management.ListSlots(c.Request.Context())
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, SlotsResponse{
		Total: len(items),
		Items: slotDTOs(items),
	})
}

func (s *Server) handleSlot(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}
	item, err := s.management.GetSlot(c.Request.Context(), slotID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "slot not found")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, slotDetailDTO(item))
}

func slotDTOs(items []managementusecase.Slot) []SlotDTO {
	out := make([]SlotDTO, 0, len(items))
	for _, item := range items {
		out = append(out, slotDTO(item))
	}
	return out
}

func slotDTO(item managementusecase.Slot) SlotDTO {
	return SlotDTO{
		SlotID: item.SlotID,
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

func slotDetailDTO(item managementusecase.SlotDetail) SlotDetailDTO {
	return SlotDetailDTO{
		SlotDTO: slotDTO(item.Slot),
		Task:    slotTaskDTO(item.Task),
	}
}

func slotTaskDTO(item *managementusecase.Task) *SlotTaskDTO {
	if item == nil {
		return nil
	}
	return &SlotTaskDTO{
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
