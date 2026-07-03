package manager

import (
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleNodeSlotMoveOutPlan(c *gin.Context) {
	nodeID, body, ok := s.parseNodeScaleInRequest(c)
	if !ok {
		return
	}
	response, err := s.management.PlanNodeSlotMoveOut(c.Request.Context(), managementusecase.NodeSlotMoveOutPlanRequest{
		NodeID:       nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeSlotMoveOutPlanResponseDTO(response))
}

func (s *Server) handleNodeSlotMoveOutAdvance(c *gin.Context) {
	nodeID, body, ok := s.parseNodeScaleInRequest(c)
	if !ok {
		return
	}
	response, err := s.management.AdvanceNodeSlotMoveOut(c.Request.Context(), managementusecase.NodeSlotMoveOutAdvanceRequest{
		NodeID:       nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	status := http.StatusOK
	if response.Created > 0 {
		status = http.StatusAccepted
	}
	c.JSON(status, nodeSlotMoveOutAdvanceResponseDTO(response))
}

func nodeSlotMoveOutPlanResponseDTO(response managementusecase.NodeSlotMoveOutPlanResponse) ManagerNodeScaleInPlanResponse {
	return ManagerNodeScaleInPlanResponse{
		GeneratedAt:     managerTimeString(response.GeneratedAt),
		StateRevision:   response.StateRevision,
		NodeID:          response.NodeID,
		Candidates:      nodeScaleInCandidateDTOs(response.Candidates),
		BlockedByStatus: response.BlockedByStatus,
	}
}

func nodeSlotMoveOutAdvanceResponseDTO(response managementusecase.NodeSlotMoveOutAdvanceResponse) ManagerNodeScaleInAdvanceResponse {
	return ManagerNodeScaleInAdvanceResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		NodeID:        response.NodeID,
		Created:       response.Created,
		Skipped:       response.Skipped,
		Candidates:    nodeScaleInCandidateDTOs(response.Candidates),
	}
}
