package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerSlotLeaderTransferBatchPlanRequest is the JSON body for previewing batch Slot leader transfers.
type ManagerSlotLeaderTransferBatchPlanRequest struct {
	// SourceNodeID is the node whose Slot leadership should be moved away.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetNodeID is the explicit desired target node, or zero to use TargetPolicy.
	TargetNodeID uint64 `json:"target_node_id"`
	// SlotIDs optionally restricts planning to the listed physical Slots.
	SlotIDs []uint32 `json:"slot_ids"`
	// MaxTasks caps how many new leader-transfer tasks execute may create.
	MaxTasks int `json:"max_tasks"`
	// TargetPolicy selects targets when TargetNodeID is zero.
	TargetPolicy string `json:"target_policy"`
}

// ManagerSlotLeaderTransferBatchExecuteRequest is the JSON body for executing a fenced batch plan.
type ManagerSlotLeaderTransferBatchExecuteRequest struct {
	// SourceNodeID is the node whose Slot leadership should be moved away.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetNodeID is the explicit desired target node, or zero to use TargetPolicy.
	TargetNodeID uint64 `json:"target_node_id"`
	// SlotIDs optionally restricts execution to the listed physical Slots.
	SlotIDs []uint32 `json:"slot_ids"`
	// MaxTasks caps how many new leader-transfer tasks execute may create.
	MaxTasks int `json:"max_tasks"`
	// TargetPolicy selects targets when TargetNodeID is zero.
	TargetPolicy string `json:"target_policy"`
	// StateRevision fences execute to the control-state revision accepted by the operator.
	StateRevision uint64 `json:"state_revision"`
	// PlanID fences execute to the deterministic plan accepted by the operator.
	PlanID string `json:"plan_id"`
}

// ManagerSlotLeaderTransferBatchPlanResponse is returned after previewing batch Slot leader transfers.
type ManagerSlotLeaderTransferBatchPlanResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision is the control-state revision used by the planner.
	StateRevision uint64 `json:"state_revision"`
	// PlanID is the deterministic identity of the normalized plan.
	PlanID string `json:"plan_id"`
	// SourceNodeID is the node whose Slot leadership should move away.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetPolicy is the normalized target-selection policy.
	TargetPolicy string `json:"target_policy"`
	// MaxTasks is the normalized create-task cap.
	MaxTasks int `json:"max_tasks"`
	// Summary contains aggregate counts for candidates and skipped Slots.
	Summary ManagerSlotLeaderTransferBatchPlanSummary `json:"summary"`
	// Candidates contains ordered Slots that execute can create or reuse.
	Candidates []ManagerSlotLeaderTransferBatchCandidate `json:"candidates"`
	// Skipped contains ordered Slots that could not be planned.
	Skipped []ManagerSlotLeaderTransferBatchSkip `json:"skipped"`
}

// ManagerSlotLeaderTransferBatchPlanSummary contains aggregate plan counters.
type ManagerSlotLeaderTransferBatchPlanSummary struct {
	// Scanned counts assignments considered after allow-list filtering.
	Scanned int `json:"scanned"`
	// Candidates counts Slots included in the plan.
	Candidates int `json:"candidates"`
	// Skipped counts Slots excluded from the plan.
	Skipped int `json:"skipped"`
	// ExistingTasks counts candidates backed by matching active tasks.
	ExistingTasks int `json:"existing_tasks"`
	// WouldCreate counts candidates that would create new tasks.
	WouldCreate int `json:"would_create"`
}

// ManagerSlotLeaderTransferBatchCandidate describes one Slot that can be transferred.
type ManagerSlotLeaderTransferBatchCandidate struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32 `json:"slot_id"`
	// SourceNodeID is the node whose leadership is being moved away.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetNodeID is the selected target leader node.
	TargetNodeID uint64 `json:"target_node_id"`
	// PreferredLeader is the control-plane preferred leader from the assignment.
	PreferredLeader uint64 `json:"preferred_leader"`
	// ActualLeader is the live Slot Raft leader observed during planning.
	ActualLeader uint64 `json:"actual_leader"`
	// DesiredPeers is the desired Slot replica set for the assignment epoch.
	DesiredPeers []uint64 `json:"desired_peers"`
	// CurrentVoters is the live Slot Raft voter set observed during planning.
	CurrentVoters []uint64 `json:"current_voters"`
	// ConfigEpoch fences the candidate to the observed Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// ExistingTaskID is set when Action is existing.
	ExistingTaskID string `json:"existing_task_id"`
	// Action reports whether execute would create or reuse a task.
	Action string `json:"action"`
}

// ManagerSlotLeaderTransferBatchSkip describes one Slot excluded from the plan.
type ManagerSlotLeaderTransferBatchSkip struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32 `json:"slot_id"`
	// Reason is a stable machine-readable skip reason.
	Reason string `json:"reason"`
	// Message is a short operator-facing explanation.
	Message string `json:"message"`
}

// ManagerSlotLeaderTransferBatchExecuteResponse reports per-Slot batch execute outcomes.
type ManagerSlotLeaderTransferBatchExecuteResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision is the control-state revision used by execute.
	StateRevision uint64 `json:"state_revision"`
	// PlanID is the deterministic identity of the executed plan.
	PlanID string `json:"plan_id"`
	// Summary contains aggregate execute counters.
	Summary ManagerSlotLeaderTransferBatchExecuteSummary `json:"summary"`
	// Results contains one ordered row for each executed candidate.
	Results []ManagerSlotLeaderTransferBatchExecuteResult `json:"results"`
}

// ManagerSlotLeaderTransferBatchExecuteSummary contains aggregate execute counters.
type ManagerSlotLeaderTransferBatchExecuteSummary struct {
	// Requested counts candidate rows considered by execute.
	Requested int `json:"requested"`
	// Created counts new transfer tasks accepted by the writer.
	Created int `json:"created"`
	// Existing counts candidates or writer responses backed by existing tasks.
	Existing int `json:"existing"`
	// AlreadyLeader counts no-op rows where the target already leads the Slot.
	AlreadyLeader int `json:"already_leader"`
	// Skipped counts rows that execute skipped without a writer call.
	Skipped int `json:"skipped"`
	// Failed counts per-Slot writer failures.
	Failed int `json:"failed"`
}

// ManagerSlotLeaderTransferBatchExecuteResult describes one Slot execute outcome.
type ManagerSlotLeaderTransferBatchExecuteResult struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32 `json:"slot_id"`
	// TargetNodeID is the target leader node for this Slot.
	TargetNodeID uint64 `json:"target_node_id"`
	// Status is a stable machine-readable execute status.
	Status string `json:"status"`
	// TaskID is the Controller task identifier when one is available.
	TaskID string `json:"task_id"`
	// Message is a short operator-facing result explanation.
	Message string `json:"message"`
}

func (s *Server) handleSlotLeaderTransferBatchPlan(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ManagerSlotLeaderTransferBatchPlanRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.PlanSlotLeaderTransfers(c.Request.Context(), slotLeaderTransferBatchPlanUsecaseRequest(body))
	if err != nil {
		writeSlotLeaderTransferBatchError(c, err)
		return
	}
	c.JSON(http.StatusOK, slotLeaderTransferBatchPlanResponseDTO(response))
}

func (s *Server) handleSlotLeaderTransferBatchExecute(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ManagerSlotLeaderTransferBatchExecuteRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.ExecuteSlotLeaderTransferBatch(c.Request.Context(), slotLeaderTransferBatchExecuteUsecaseRequest(body))
	if err != nil {
		writeSlotLeaderTransferBatchError(c, err)
		return
	}
	status := http.StatusOK
	if response.Summary.Created > 0 {
		status = http.StatusAccepted
	}
	c.JSON(status, slotLeaderTransferBatchExecuteResponseDTO(response))
}

func slotLeaderTransferBatchPlanUsecaseRequest(req ManagerSlotLeaderTransferBatchPlanRequest) managementusecase.SlotLeaderTransferBatchPlanRequest {
	slotIDs := append([]uint32(nil), req.SlotIDs...)
	return managementusecase.SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
		SlotIDs:      slotIDs,
		MaxTasks:     req.MaxTasks,
		TargetPolicy: req.TargetPolicy,
	}
}

func slotLeaderTransferBatchExecuteUsecaseRequest(req ManagerSlotLeaderTransferBatchExecuteRequest) managementusecase.SlotLeaderTransferBatchExecuteRequest {
	slotIDs := append([]uint32(nil), req.SlotIDs...)
	return managementusecase.SlotLeaderTransferBatchExecuteRequest{
		SourceNodeID:  req.SourceNodeID,
		TargetNodeID:  req.TargetNodeID,
		SlotIDs:       slotIDs,
		MaxTasks:      req.MaxTasks,
		TargetPolicy:  req.TargetPolicy,
		StateRevision: req.StateRevision,
		PlanID:        req.PlanID,
	}
}

func slotLeaderTransferBatchPlanResponseDTO(response managementusecase.SlotLeaderTransferBatchPlanResponse) ManagerSlotLeaderTransferBatchPlanResponse {
	return ManagerSlotLeaderTransferBatchPlanResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		PlanID:        response.PlanID,
		SourceNodeID:  response.SourceNodeID,
		TargetPolicy:  response.TargetPolicy,
		MaxTasks:      response.MaxTasks,
		Summary:       slotLeaderTransferBatchPlanSummaryDTO(response.Summary),
		Candidates:    slotLeaderTransferBatchCandidateDTOs(response.Candidates),
		Skipped:       slotLeaderTransferBatchSkipDTOs(response.Skipped),
	}
}

func slotLeaderTransferBatchPlanSummaryDTO(summary managementusecase.SlotLeaderTransferBatchPlanSummary) ManagerSlotLeaderTransferBatchPlanSummary {
	return ManagerSlotLeaderTransferBatchPlanSummary{
		Scanned:       summary.Scanned,
		Candidates:    summary.Candidates,
		Skipped:       summary.Skipped,
		ExistingTasks: summary.ExistingTasks,
		WouldCreate:   summary.WouldCreate,
	}
}

func slotLeaderTransferBatchCandidateDTOs(items []managementusecase.SlotLeaderTransferBatchCandidate) []ManagerSlotLeaderTransferBatchCandidate {
	out := make([]ManagerSlotLeaderTransferBatchCandidate, 0, len(items))
	for _, item := range items {
		out = append(out, slotLeaderTransferBatchCandidateDTO(item))
	}
	return out
}

func slotLeaderTransferBatchCandidateDTO(item managementusecase.SlotLeaderTransferBatchCandidate) ManagerSlotLeaderTransferBatchCandidate {
	return ManagerSlotLeaderTransferBatchCandidate{
		SlotID:          item.SlotID,
		SourceNodeID:    item.SourceNodeID,
		TargetNodeID:    item.TargetNodeID,
		PreferredLeader: item.PreferredLeader,
		ActualLeader:    item.ActualLeader,
		DesiredPeers:    append([]uint64(nil), item.DesiredPeers...),
		CurrentVoters:   append([]uint64(nil), item.CurrentVoters...),
		ConfigEpoch:     item.ConfigEpoch,
		ExistingTaskID:  item.ExistingTaskID,
		Action:          item.Action,
	}
}

func slotLeaderTransferBatchSkipDTOs(items []managementusecase.SlotLeaderTransferBatchSkip) []ManagerSlotLeaderTransferBatchSkip {
	out := make([]ManagerSlotLeaderTransferBatchSkip, 0, len(items))
	for _, item := range items {
		out = append(out, ManagerSlotLeaderTransferBatchSkip{
			SlotID:  item.SlotID,
			Reason:  item.Reason,
			Message: item.Message,
		})
	}
	return out
}

func slotLeaderTransferBatchExecuteResponseDTO(response managementusecase.SlotLeaderTransferBatchExecuteResponse) ManagerSlotLeaderTransferBatchExecuteResponse {
	return ManagerSlotLeaderTransferBatchExecuteResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		PlanID:        response.PlanID,
		Summary:       slotLeaderTransferBatchExecuteSummaryDTO(response.Summary),
		Results:       slotLeaderTransferBatchExecuteResultDTOs(response.Results),
	}
}

func slotLeaderTransferBatchExecuteSummaryDTO(summary managementusecase.SlotLeaderTransferBatchExecuteSummary) ManagerSlotLeaderTransferBatchExecuteSummary {
	return ManagerSlotLeaderTransferBatchExecuteSummary{
		Requested:     summary.Requested,
		Created:       summary.Created,
		Existing:      summary.Existing,
		AlreadyLeader: summary.AlreadyLeader,
		Skipped:       summary.Skipped,
		Failed:        summary.Failed,
	}
}

func slotLeaderTransferBatchExecuteResultDTOs(items []managementusecase.SlotLeaderTransferBatchExecuteResult) []ManagerSlotLeaderTransferBatchExecuteResult {
	out := make([]ManagerSlotLeaderTransferBatchExecuteResult, 0, len(items))
	for _, item := range items {
		out = append(out, ManagerSlotLeaderTransferBatchExecuteResult{
			SlotID:       item.SlotID,
			TargetNodeID: item.TargetNodeID,
			Status:       item.Status,
			TaskID:       item.TaskID,
			Message:      item.Message,
		})
	}
	return out
}

func writeSlotLeaderTransferBatchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferPlanStale),
		errors.Is(err, managementusecase.ErrSlotLeaderTransferPlanMismatch),
		errors.Is(err, managementusecase.ErrSlotLeaderTransferConflict):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferUnavailable),
		errors.Is(err, managementusecase.ErrSlotRuntimeStatusUnavailable),
		errors.Is(err, managementusecase.ErrSlotRaftOperatorUnavailable),
		errors.Is(err, cluster.ErrSlotNotFound),
		errors.Is(err, cluster.ErrNotStarted),
		errors.Is(err, cluster.ErrNotLeader),
		errors.Is(err, cluster.ErrStopping):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
