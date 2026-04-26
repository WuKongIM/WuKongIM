package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

type slotRemoveResponse struct {
	SlotID uint32 `json:"slot_id"`
	Result string `json:"result"`
}

func (s *Server) handleSlotAdd(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	item, err := s.management.AddSlot(c.Request.Context())
	if err != nil {
		switch {
		case errors.Is(err, managementusecase.ErrSlotMigrationsInProgress):
			jsonError(c, http.StatusConflict, "conflict", "slot migrations already in progress")
			return
		case leaderConsistentReadUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot add unavailable")
			return
		case errors.Is(err, raftcluster.ErrInvalidConfig), errors.Is(err, controllermeta.ErrInvalidArgument):
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot operation")
			return
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}

	c.JSON(http.StatusOK, slotDetailDTO(item))
}

func (s *Server) handleSlotDelete(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}

	item, err := s.management.RemoveSlot(c.Request.Context(), slotID)
	if err != nil {
		switch {
		case errors.Is(err, managementusecase.ErrSlotMigrationsInProgress):
			jsonError(c, http.StatusConflict, "conflict", "slot migrations already in progress")
			return
		case errors.Is(err, controllermeta.ErrNotFound), errors.Is(err, raftcluster.ErrSlotNotFound):
			jsonError(c, http.StatusNotFound, "not_found", "slot not found")
			return
		case leaderConsistentReadUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot remove unavailable")
			return
		case errors.Is(err, raftcluster.ErrInvalidConfig), errors.Is(err, controllermeta.ErrInvalidArgument):
			jsonError(c, http.StatusConflict, "conflict", "slot remove unavailable")
			return
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}

	c.JSON(http.StatusOK, slotRemoveResponse{
		SlotID: item.SlotID,
		Result: item.Result,
	})
}
