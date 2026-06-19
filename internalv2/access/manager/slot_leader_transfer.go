package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerSlotLeaderTransferRequest is the JSON body for a Slot leader transfer.
type ManagerSlotLeaderTransferRequest struct {
	// TargetNode is the desired new Slot Raft leader.
	TargetNode uint64 `json:"target_node"`
}

// ManagerSlotLeaderTransferResponse is returned after evaluating a leader transfer intent.
type ManagerSlotLeaderTransferResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// SlotID is the physical Slot whose leader transfer was requested.
	SlotID uint32 `json:"slot_id"`
	// TargetNode is the desired new Slot Raft leader.
	TargetNode uint64 `json:"target_node"`
	// PreferredLeader is the controller preferred leader from the assignment.
	PreferredLeader uint64 `json:"preferred_leader"`
	// ActualLeader is the observed Slot Raft leader before the request.
	ActualLeader uint64 `json:"actual_leader"`
	// Created reports whether a new durable task was created.
	Created bool `json:"created"`
	// Task contains the active or created Slot task when available.
	Task *SlotTaskDTO `json:"task,omitempty"`
	// Message is a stable operator-facing result summary.
	Message string `json:"message"`
}

func (s *Server) handleSlotLeaderTransfer(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	slotID, err := parseRequiredLogSlotID(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	var body ManagerSlotLeaderTransferRequest
	if err := c.ShouldBindJSON(&body); err != nil || body.TargetNode == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.RequestSlotLeaderTransfer(c.Request.Context(), managementusecase.SlotLeaderTransferRequest{
		SlotID:     slotID,
		TargetNode: body.TargetNode,
	})
	if err != nil {
		writeSlotLeaderTransferError(c, err)
		return
	}
	status := http.StatusOK
	if response.Created {
		status = http.StatusAccepted
	}
	c.JSON(status, slotLeaderTransferResponseDTO(response))
}

func slotLeaderTransferResponseDTO(response managementusecase.SlotLeaderTransferResponse) ManagerSlotLeaderTransferResponse {
	return ManagerSlotLeaderTransferResponse{
		GeneratedAt:     managerTimeString(response.GeneratedAt),
		SlotID:          response.SlotID,
		TargetNode:      response.TargetNode,
		PreferredLeader: response.PreferredLeader,
		ActualLeader:    response.ActualLeader,
		Created:         response.Created,
		Task:            slotTaskDTO(response.Task),
		Message:         response.Message,
	}
}

func writeSlotLeaderTransferError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferSlotNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "not_found")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferConflict):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferUnavailable),
		errors.Is(err, managementusecase.ErrSlotRuntimeStatusUnavailable),
		errors.Is(err, managementusecase.ErrSlotRaftOperatorUnavailable),
		errors.Is(err, clusterv2.ErrSlotNotFound),
		errors.Is(err, clusterv2.ErrNotStarted),
		errors.Is(err, clusterv2.ErrNotLeader),
		errors.Is(err, clusterv2.ErrStopping):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
