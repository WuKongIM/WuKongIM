package manager

import (
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/gin-gonic/gin"
)

// ManagerNodeScaleInRemoveResponse reports the durable node state after final scale-in removal.
type ManagerNodeScaleInRemoveResponse struct {
	// Changed reports whether the control writer advanced cluster state.
	Changed bool `json:"changed"`
	// NodeID is the durable node identity returned by control state.
	NodeID uint64 `json:"node_id"`
	// JoinState is the durable membership lifecycle state.
	JoinState string `json:"join_state"`
	// Revision is the control-state revision observed by the writer.
	Revision uint64 `json:"revision"`
}

func (s *Server) handleNodeScaleInRemove(c *gin.Context) {
	nodeID, ok := s.parseNodeScaleInNodeID(c)
	if !ok {
		return
	}
	response, err := s.management.MarkNodeRemoved(c.Request.Context(), managementusecase.MarkNodeRemovedRequest{NodeID: nodeID})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	status := http.StatusOK
	if response.Changed {
		status = http.StatusAccepted
	}
	c.JSON(status, nodeScaleInRemoveResponseDTO(response))
}

func nodeScaleInRemoveResponseDTO(response managementusecase.MarkNodeRemovedResponse) ManagerNodeScaleInRemoveResponse {
	return ManagerNodeScaleInRemoveResponse{
		Changed:   response.Changed,
		NodeID:    response.NodeID,
		JoinState: response.JoinState,
		Revision:  response.Revision,
	}
}
