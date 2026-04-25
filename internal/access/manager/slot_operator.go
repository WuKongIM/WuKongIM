package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

type slotLeaderTransferRequest struct {
	TargetNodeID uint64 `json:"target_node_id"`
}

type slotRecoverRequest struct {
	Strategy string `json:"strategy"`
}

type slotRecoverResponse struct {
	Strategy string        `json:"strategy"`
	Result   string        `json:"result"`
	Slot     SlotDetailDTO `json:"slot"`
}

type slotRebalancePlanItemDTO struct {
	HashSlot   uint16 `json:"hash_slot"`
	FromSlotID uint32 `json:"from_slot_id"`
	ToSlotID   uint32 `json:"to_slot_id"`
}

type slotRebalanceResponse struct {
	Total int                        `json:"total"`
	Items []slotRebalancePlanItemDTO `json:"items"`
}

func (s *Server) handleSlotLeaderTransfer(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}

	var req slotLeaderTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if req.TargetNodeID == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid target_node_id")
		return
	}

	item, err := s.management.TransferSlotLeader(c.Request.Context(), slotID, req.TargetNodeID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "slot not found")
			return
		}
		if errors.Is(err, managementusecase.ErrTargetNodeNotAssigned) {
			jsonError(c, http.StatusBadRequest, "bad_request", "target_node_id is not a desired peer")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, slotDetailDTO(item))
}

func (s *Server) handleSlotRecover(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}

	var req slotRecoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}

	item, err := s.management.RecoverSlot(c.Request.Context(), slotID, managementusecase.SlotRecoverStrategy(req.Strategy))
	if err != nil {
		switch {
		case errors.Is(err, managementusecase.ErrUnsupportedRecoverStrategy):
			jsonError(c, http.StatusBadRequest, "bad_request", "unsupported strategy")
			return
		case errors.Is(err, controllermeta.ErrNotFound):
			jsonError(c, http.StatusNotFound, "not_found", "slot not found")
			return
		case errors.Is(err, raftcluster.ErrManualRecoveryRequired):
			jsonError(c, http.StatusConflict, "conflict", "manual recovery required")
			return
		case leaderConsistentReadUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot recovery unavailable")
			return
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}

	c.JSON(http.StatusOK, slotRecoverResponse{
		Strategy: item.Strategy,
		Result:   item.Result,
		Slot:     slotDetailDTO(item.Slot),
	})
}

func (s *Server) handleSlotRebalance(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	item, err := s.management.RebalanceSlots(c.Request.Context())
	if err != nil {
		switch {
		case errors.Is(err, managementusecase.ErrSlotMigrationsInProgress):
			jsonError(c, http.StatusConflict, "conflict", "slot migrations already in progress")
			return
		case leaderConsistentReadUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot rebalance unavailable")
			return
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}

	c.JSON(http.StatusOK, slotRebalanceResponse{
		Total: item.Total,
		Items: slotRebalancePlanItemDTOs(item.Items),
	})
}

func slotRebalancePlanItemDTOs(items []managementusecase.SlotRebalancePlanItem) []slotRebalancePlanItemDTO {
	out := make([]slotRebalancePlanItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, slotRebalancePlanItemDTO{
			HashSlot:   item.HashSlot,
			FromSlotID: item.FromSlotID,
			ToSlotID:   item.ToSlotID,
		})
	}
	return out
}
