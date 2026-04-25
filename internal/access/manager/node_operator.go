package manager

import (
	"context"
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleNodeDraining(c *gin.Context) {
	s.handleNodeAction(c, func(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error) {
		return s.management.MarkNodeDraining(ctx, nodeID)
	})
}

func (s *Server) handleNodeResume(c *gin.Context) {
	s.handleNodeAction(c, func(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error) {
		return s.management.ResumeNode(ctx, nodeID)
	})
}

func (s *Server) handleNodeAction(c *gin.Context, action func(context.Context, uint64) (managementusecase.NodeDetail, error)) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}

	item, err := action(c.Request.Context(), nodeID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "node not found")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, nodeDetailDTO(item))
}
