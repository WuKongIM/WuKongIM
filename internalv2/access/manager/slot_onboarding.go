package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerNodeOnboardingRequest is the JSON body for bounded node onboarding actions.
type ManagerNodeOnboardingRequest struct {
	// MaxSlotMoves bounds the number of Slot replica move tasks.
	MaxSlotMoves uint32 `json:"max_slot_moves"`
}

// ManagerNodeOnboardingPlanResponse is the manager onboarding preview response.
type ManagerNodeOnboardingPlanResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision fences the plan to the observed control snapshot.
	StateRevision uint64 `json:"state_revision"`
	// TargetNodeID is the active data node selected for onboarding.
	TargetNodeID uint64 `json:"target_node_id"`
	// MaxSlotMoves is the normalized request bound.
	MaxSlotMoves uint32 `json:"max_slot_moves"`
	// Candidates contains planned Slot replica moves.
	Candidates []ManagerNodeOnboardingCandidate `json:"candidates"`
	// Skipped contains excluded Slots.
	Skipped []ManagerNodeOnboardingSkip `json:"skipped"`
}

// ManagerNodeOnboardingCandidate is one manager-facing Slot move preview.
type ManagerNodeOnboardingCandidate struct {
	// SlotID is the physical Slot selected for a move.
	SlotID uint32 `json:"slot_id"`
	// SourceNodeID is the desired peer that will be replaced.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetNodeID is the onboarding target node.
	TargetNodeID uint64 `json:"target_node_id"`
	// TargetPeers is the desired peer set after replacement.
	TargetPeers []uint64 `json:"target_peers"`
	// ConfigEpoch fences the move to the observed Slot assignment.
	ConfigEpoch uint64 `json:"config_epoch"`
}

// ManagerNodeOnboardingSkip is one manager-facing skipped Slot row.
type ManagerNodeOnboardingSkip struct {
	// SlotID is the physical Slot that was skipped.
	SlotID uint32 `json:"slot_id"`
	// Reason is a stable low-cardinality skip reason.
	Reason string `json:"reason"`
	// Message is a short operator-facing explanation.
	Message string `json:"message"`
}

// ManagerNodeOnboardingStartResponse reports submitted Slot replica move tasks.
type ManagerNodeOnboardingStartResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision fences the request to the observed control snapshot.
	StateRevision uint64 `json:"state_revision"`
	// TargetNodeID is the active data node selected for onboarding.
	TargetNodeID uint64 `json:"target_node_id"`
	// MaxSlotMoves is the normalized request bound.
	MaxSlotMoves uint32 `json:"max_slot_moves"`
	// Created is the number of new durable tasks.
	Created uint32 `json:"created"`
	// Results contains one row per submitted candidate.
	Results []ManagerNodeOnboardingTaskResult `json:"results"`
	// Skipped contains excluded Slots.
	Skipped []ManagerNodeOnboardingSkip `json:"skipped"`
}

// ManagerNodeOnboardingTaskResult is one submitted task result.
type ManagerNodeOnboardingTaskResult struct {
	// SlotID is the physical Slot submitted to control.
	SlotID uint32 `json:"slot_id"`
	// Created reports whether a new task was accepted.
	Created bool `json:"created"`
	// Task contains the control task when available.
	Task *SlotTaskDTO `json:"task,omitempty"`
}

// ManagerNodeOnboardingStatusResponse summarizes active onboarding tasks.
type ManagerNodeOnboardingStatusResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision is the observed control snapshot revision.
	StateRevision uint64 `json:"state_revision"`
	// TargetNodeID is the selected onboarding target.
	TargetNodeID uint64 `json:"target_node_id"`
	// Summary contains active task counts.
	Summary ManagerNodeOnboardingStatusSummary `json:"summary"`
	// Tasks contains active replica-move tasks for the target.
	Tasks []SlotTaskDTO `json:"tasks"`
}

// ManagerNodeOnboardingStatusSummary contains aggregate active task counts.
type ManagerNodeOnboardingStatusSummary struct {
	// TotalActive is the number of active replica-move tasks for the target.
	TotalActive int `json:"total_active"`
	// Pending is the number of pending replica-move tasks.
	Pending int `json:"pending"`
	// Running is the number of running replica-move tasks.
	Running int `json:"running"`
	// Failed is the number of failed replica-move tasks.
	Failed int `json:"failed"`
}

func (s *Server) handleNodeOnboardingPlan(c *gin.Context) {
	nodeID, body, ok := s.parseNodeOnboardingRequest(c)
	if !ok {
		return
	}
	response, err := s.management.PlanNodeOnboarding(c.Request.Context(), managementusecase.NodeOnboardingPlanRequest{
		TargetNodeID: nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingPlanResponseDTO(response))
}

func (s *Server) handleNodeOnboardingStart(c *gin.Context) {
	nodeID, body, ok := s.parseNodeOnboardingRequest(c)
	if !ok {
		return
	}
	response, err := s.management.StartNodeOnboarding(c.Request.Context(), managementusecase.NodeOnboardingStartRequest{
		TargetNodeID: nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeOnboardingError(c, err)
		return
	}
	status := http.StatusOK
	if response.Created > 0 {
		status = http.StatusAccepted
	}
	c.JSON(status, nodeOnboardingStartResponseDTO(response))
}

func (s *Server) handleNodeOnboardingAdvance(c *gin.Context) {
	nodeID, body, ok := s.parseNodeOnboardingRequest(c)
	if !ok {
		return
	}
	response, err := s.management.AdvanceNodeOnboarding(c.Request.Context(), managementusecase.NodeOnboardingAdvanceRequest{
		TargetNodeID: nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeOnboardingError(c, err)
		return
	}
	status := http.StatusOK
	if response.Created > 0 {
		status = http.StatusAccepted
	}
	c.JSON(status, nodeOnboardingStartResponseDTO(response))
}

func (s *Server) handleNodeOnboardingStatus(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.NodeOnboardingStatus(c.Request.Context(), managementusecase.NodeOnboardingStatusRequest{TargetNodeID: nodeID})
	if err != nil {
		writeNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingStatusResponseDTO(response))
}

func (s *Server) parseNodeOnboardingRequest(c *gin.Context) (uint64, ManagerNodeOnboardingRequest, bool) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return 0, ManagerNodeOnboardingRequest{}, false
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return 0, ManagerNodeOnboardingRequest{}, false
	}
	var body ManagerNodeOnboardingRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
			return 0, ManagerNodeOnboardingRequest{}, false
		}
	}
	return nodeID, body, true
}

func nodeOnboardingPlanResponseDTO(response managementusecase.NodeOnboardingPlanResponse) ManagerNodeOnboardingPlanResponse {
	return ManagerNodeOnboardingPlanResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		TargetNodeID:  response.TargetNodeID,
		MaxSlotMoves:  response.MaxSlotMoves,
		Candidates:    nodeOnboardingCandidateDTOs(response.Candidates),
		Skipped:       nodeOnboardingSkipDTOs(response.Skipped),
	}
}

func nodeOnboardingStartResponseDTO(response managementusecase.NodeOnboardingStartResponse) ManagerNodeOnboardingStartResponse {
	results := make([]ManagerNodeOnboardingTaskResult, 0, len(response.Results))
	for _, result := range response.Results {
		results = append(results, ManagerNodeOnboardingTaskResult{
			SlotID:  result.SlotID,
			Created: result.Created,
			Task:    slotTaskDTO(result.Task),
		})
	}
	return ManagerNodeOnboardingStartResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		TargetNodeID:  response.TargetNodeID,
		MaxSlotMoves:  response.MaxSlotMoves,
		Created:       response.Created,
		Results:       results,
		Skipped:       nodeOnboardingSkipDTOs(response.Skipped),
	}
}

func nodeOnboardingStatusResponseDTO(response managementusecase.NodeOnboardingStatusResponse) ManagerNodeOnboardingStatusResponse {
	tasks := make([]SlotTaskDTO, 0, len(response.Tasks))
	for i := range response.Tasks {
		if task := slotTaskDTO(&response.Tasks[i]); task != nil {
			tasks = append(tasks, *task)
		}
	}
	return ManagerNodeOnboardingStatusResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		TargetNodeID:  response.TargetNodeID,
		Summary: ManagerNodeOnboardingStatusSummary{
			TotalActive: response.Summary.TotalActive,
			Pending:     response.Summary.Pending,
			Running:     response.Summary.Running,
			Failed:      response.Summary.Failed,
		},
		Tasks: tasks,
	}
}

func nodeOnboardingCandidateDTOs(items []managementusecase.NodeOnboardingCandidate) []ManagerNodeOnboardingCandidate {
	out := make([]ManagerNodeOnboardingCandidate, 0, len(items))
	for _, item := range items {
		out = append(out, ManagerNodeOnboardingCandidate{
			SlotID:       item.SlotID,
			SourceNodeID: item.SourceNodeID,
			TargetNodeID: item.TargetNodeID,
			TargetPeers:  append([]uint64(nil), item.TargetPeers...),
			ConfigEpoch:  item.ConfigEpoch,
		})
	}
	return out
}

func nodeOnboardingSkipDTOs(items []managementusecase.NodeOnboardingSkip) []ManagerNodeOnboardingSkip {
	out := make([]ManagerNodeOnboardingSkip, 0, len(items))
	for _, item := range items {
		out = append(out, ManagerNodeOnboardingSkip{
			SlotID:  item.SlotID,
			Reason:  item.Reason,
			Message: item.Message,
		})
	}
	return out
}

func writeNodeOnboardingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrNodeOnboardingTargetNotActive):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrNodeOnboardingUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
