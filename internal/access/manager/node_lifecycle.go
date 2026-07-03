package manager

import (
	"errors"
	"net/http"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerJoinNodeRequest is the JSON body for a node join request.
type ManagerJoinNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64 `json:"node_id"`
	// Name is an optional operator-facing node label.
	Name string `json:"name"`
	// Addr is the stable cluster control-plane address for the node.
	Addr string `json:"addr"`
	// CapacityWeight is the optional planner placement weight.
	CapacityWeight uint32 `json:"capacity_weight"`
}

// ManagerJoinNodeResponse is returned after evaluating a node join request.
type ManagerJoinNodeResponse struct {
	// Created reports whether the control writer advanced cluster state.
	Created bool `json:"created"`
	// NodeID is the durable node identity returned by control state.
	NodeID uint64 `json:"node_id"`
	// Addr is the durable cluster control-plane address returned by control state.
	Addr string `json:"addr"`
	// JoinState is the durable membership lifecycle state.
	JoinState string `json:"join_state"`
	// Revision is the control-state revision observed by the writer.
	Revision uint64 `json:"revision"`
}

// ManagerActivateNodeResponse is returned after evaluating node activation.
type ManagerActivateNodeResponse struct {
	// Changed reports whether the control writer advanced cluster state.
	Changed bool `json:"changed"`
	// NodeID is the durable node identity returned by control state.
	NodeID uint64 `json:"node_id"`
	// Addr is the durable cluster control-plane address returned by control state.
	Addr string `json:"addr,omitempty"`
	// JoinState is the durable membership lifecycle state.
	JoinState string `json:"join_state"`
	// Revision is the control-state revision observed by the writer.
	Revision uint64 `json:"revision"`
}

func (s *Server) handleJoinNode(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ManagerJoinNodeRequest
	if err := c.ShouldBindJSON(&body); err != nil || body.NodeID == 0 || strings.TrimSpace(body.Addr) == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.JoinNode(c.Request.Context(), managementusecase.JoinNodeRequest{
		NodeID:         body.NodeID,
		Name:           body.Name,
		Addr:           body.Addr,
		CapacityWeight: body.CapacityWeight,
	})
	if err != nil {
		writeNodeLifecycleError(c, err)
		return
	}
	status := http.StatusOK
	if response.Created {
		status = http.StatusAccepted
	}
	c.JSON(status, joinNodeResponseDTO(response))
}

func (s *Server) handleActivateNode(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.ActivateNode(c.Request.Context(), managementusecase.ActivateNodeRequest{NodeID: nodeID})
	if err != nil {
		writeNodeLifecycleError(c, err)
		return
	}
	status := http.StatusOK
	if response.Changed {
		status = http.StatusAccepted
	}
	c.JSON(status, activateNodeResponseDTO(response))
}

func joinNodeResponseDTO(response managementusecase.JoinNodeResponse) ManagerJoinNodeResponse {
	return ManagerJoinNodeResponse{
		Created:   response.Created,
		NodeID:    response.NodeID,
		Addr:      response.Addr,
		JoinState: response.JoinState,
		Revision:  response.Revision,
	}
}

func activateNodeResponseDTO(response managementusecase.ActivateNodeResponse) ManagerActivateNodeResponse {
	return ManagerActivateNodeResponse{
		Changed:   response.Changed,
		NodeID:    response.NodeID,
		Addr:      response.Addr,
		JoinState: response.JoinState,
		Revision:  response.Revision,
	}
}

func writeNodeLifecycleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrNodeLifecycleNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "not_found")
	case errors.Is(err, managementusecase.ErrNodeLifecycleConflict):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrNodeNotReadyForActivation):
		message := "conflict"
		if err.Error() != managementusecase.ErrNodeNotReadyForActivation.Error() {
			message = err.Error()
		}
		jsonError(c, http.StatusConflict, "conflict", message)
	case errors.Is(err, managementusecase.ErrNodeLifecycleUnavailable),
		errors.Is(err, cluster.ErrNotStarted),
		errors.Is(err, cluster.ErrNotLeader),
		errors.Is(err, cluster.ErrStopping):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
