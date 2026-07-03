package manager

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/gin-gonic/gin"
)

// ManagerPromoteControllerVoterRequest is the optional JSON body for Controller voter promotion.
type ManagerPromoteControllerVoterRequest struct {
	// ExpectedRevision fences the promotion to the operator-observed control revision.
	ExpectedRevision uint64 `json:"expected_revision"`
}

// ManagerPromoteControllerVoterResponse is returned after evaluating Controller voter promotion.
type ManagerPromoteControllerVoterResponse struct {
	// Changed reports whether durable Controller voter state changed.
	Changed bool `json:"changed"`
	// NodeID is the promoted or already-voting node identity.
	NodeID uint64 `json:"node_id"`
	// StateRevision is the resulting or observed control-state revision.
	StateRevision uint64 `json:"state_revision"`
	// PreviousVoters is the Controller voter set before promotion.
	PreviousVoters []uint64 `json:"previous_voters"`
	// NextVoters is the Controller voter set after promotion.
	NextVoters []uint64 `json:"next_voters"`
	// Warnings contains bounded operator warnings from the promotion write.
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) handlePromoteControllerVoter(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	body, ok := parsePromoteControllerVoterBody(c)
	if !ok {
		return
	}
	response, err := s.management.PromoteControllerVoter(c.Request.Context(), managementusecase.PromoteControllerVoterRequest{
		NodeID:           nodeID,
		ExpectedRevision: body.ExpectedRevision,
	})
	if err != nil {
		writePromoteControllerVoterError(c, err)
		return
	}
	status := http.StatusOK
	if response.Changed {
		status = http.StatusAccepted
	}
	c.JSON(status, promoteControllerVoterResponseDTO(response))
}

func parsePromoteControllerVoterBody(c *gin.Context) (ManagerPromoteControllerVoterRequest, bool) {
	if c.Request.Body == nil || c.Request.ContentLength == 0 {
		return ManagerPromoteControllerVoterRequest{}, true
	}
	decoder := json.NewDecoder(c.Request.Body)
	var body ManagerPromoteControllerVoterRequest
	if err := decoder.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return ManagerPromoteControllerVoterRequest{}, true
		}
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return ManagerPromoteControllerVoterRequest{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return ManagerPromoteControllerVoterRequest{}, false
	}
	return body, true
}

func promoteControllerVoterResponseDTO(response managementusecase.PromoteControllerVoterResponse) ManagerPromoteControllerVoterResponse {
	return ManagerPromoteControllerVoterResponse{
		Changed:        response.Changed,
		NodeID:         response.NodeID,
		StateRevision:  response.StateRevision,
		PreviousVoters: cloneUint64DTOs(response.PreviousVoters),
		NextVoters:     cloneUint64DTOs(response.NextVoters),
		Warnings:       cloneStringDTOs(response.Warnings),
	}
}

func cloneUint64DTOs(in []uint64) []uint64 {
	if in == nil {
		return []uint64{}
	}
	return append([]uint64(nil), in...)
}

func cloneStringDTOs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func writePromoteControllerVoterError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, managementusecase.ErrNodeLifecycleNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "not_found")
	case errors.Is(err, managementusecase.ErrControllerVoterPromotionBlocked):
		message := "conflict"
		if err.Error() != managementusecase.ErrControllerVoterPromotionBlocked.Error() {
			message = err.Error()
		}
		jsonError(c, http.StatusConflict, "conflict", message)
	case errors.Is(err, managementusecase.ErrControllerVoterPromotionUnavailable),
		errors.Is(err, cluster.ErrNotStarted),
		errors.Is(err, cluster.ErrNotLeader),
		errors.Is(err, cluster.ErrStopping),
		controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
