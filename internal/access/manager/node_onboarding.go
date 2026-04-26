package manager

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

const (
	defaultNodeOnboardingJobLimit = 50
	maxNodeOnboardingJobLimit     = 200
)

type nodeOnboardingCandidatesResponse struct {
	Total int                          `json:"total"`
	Items []nodeOnboardingCandidateDTO `json:"items"`
}

type nodeOnboardingCandidateDTO struct {
	NodeID      uint64 `json:"node_id"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Role        string `json:"role"`
	JoinState   string `json:"join_state"`
	Status      string `json:"status"`
	SlotCount   int    `json:"slot_count"`
	LeaderCount int    `json:"leader_count"`
	Recommended bool   `json:"recommended"`
}

type createNodeOnboardingPlanRequest struct {
	TargetNodeID uint64 `json:"target_node_id"`
}

type nodeOnboardingJobsResponse struct {
	Items      []nodeOnboardingJobDTO `json:"items"`
	NextCursor string                 `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
}

type nodeOnboardingJobDTO struct {
	JobID            string                        `json:"job_id"`
	TargetNodeID     uint64                        `json:"target_node_id"`
	RetryOfJobID     string                        `json:"retry_of_job_id"`
	Status           string                        `json:"status"`
	CreatedAt        time.Time                     `json:"created_at"`
	UpdatedAt        time.Time                     `json:"updated_at"`
	StartedAt        time.Time                     `json:"started_at"`
	CompletedAt      time.Time                     `json:"completed_at"`
	PlanVersion      uint32                        `json:"plan_version"`
	PlanFingerprint  string                        `json:"plan_fingerprint"`
	Plan             nodeOnboardingPlanDTO         `json:"plan"`
	Moves            []nodeOnboardingMoveDTO       `json:"moves"`
	CurrentMoveIndex int                           `json:"current_move_index"`
	ResultCounts     nodeOnboardingResultCountsDTO `json:"result_counts"`
	LastError        string                        `json:"last_error"`
}

type nodeOnboardingPlanDTO struct {
	TargetNodeID   uint64                           `json:"target_node_id"`
	Summary        nodeOnboardingPlanSummaryDTO     `json:"summary"`
	Moves          []nodeOnboardingPlanMoveDTO      `json:"moves"`
	BlockedReasons []nodeOnboardingBlockedReasonDTO `json:"blocked_reasons"`
}

type nodeOnboardingPlanSummaryDTO struct {
	CurrentTargetSlotCount   int `json:"current_target_slot_count"`
	PlannedTargetSlotCount   int `json:"planned_target_slot_count"`
	CurrentTargetLeaderCount int `json:"current_target_leader_count"`
	PlannedLeaderGain        int `json:"planned_leader_gain"`
}

type nodeOnboardingPlanMoveDTO struct {
	SlotID                 uint32   `json:"slot_id"`
	SourceNodeID           uint64   `json:"source_node_id"`
	TargetNodeID           uint64   `json:"target_node_id"`
	Reason                 string   `json:"reason"`
	DesiredPeersBefore     []uint64 `json:"desired_peers_before"`
	DesiredPeersAfter      []uint64 `json:"desired_peers_after"`
	CurrentLeaderID        uint64   `json:"current_leader_id"`
	LeaderTransferRequired bool     `json:"leader_transfer_required"`
}

type nodeOnboardingBlockedReasonDTO struct {
	Code    string `json:"code"`
	Scope   string `json:"scope"`
	SlotID  uint32 `json:"slot_id"`
	NodeID  uint64 `json:"node_id"`
	Message string `json:"message"`
}

type nodeOnboardingMoveDTO struct {
	SlotID                 uint32    `json:"slot_id"`
	SourceNodeID           uint64    `json:"source_node_id"`
	TargetNodeID           uint64    `json:"target_node_id"`
	Status                 string    `json:"status"`
	TaskKind               string    `json:"task_kind"`
	TaskSlotID             uint32    `json:"task_slot_id"`
	StartedAt              time.Time `json:"started_at"`
	CompletedAt            time.Time `json:"completed_at"`
	LastError              string    `json:"last_error"`
	DesiredPeersBefore     []uint64  `json:"desired_peers_before"`
	DesiredPeersAfter      []uint64  `json:"desired_peers_after"`
	LeaderBefore           uint64    `json:"leader_before"`
	LeaderAfter            uint64    `json:"leader_after"`
	LeaderTransferRequired bool      `json:"leader_transfer_required"`
}

type nodeOnboardingResultCountsDTO struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

func (s *Server) handleNodeOnboardingCandidates(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	items, err := s.management.ListNodeOnboardingCandidates(c.Request.Context())
	if err != nil {
		handleNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingCandidatesResponse{Total: items.Total, Items: nodeOnboardingCandidateDTOs(items.Items)})
}

func (s *Server) handleNodeOnboardingPlan(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	var req createNodeOnboardingPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid body")
		return
	}
	if req.TargetNodeID == 0 {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid target_node_id")
		return
	}

	job, err := s.management.CreateNodeOnboardingPlan(c.Request.Context(), managementusecase.CreateNodeOnboardingPlanRequest{TargetNodeID: req.TargetNodeID})
	if err != nil {
		handleNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingJobDTOFromUsecase(job.Job))
}

func (s *Server) handleNodeOnboardingStart(c *gin.Context) {
	s.handleNodeOnboardingJobAction(c, func(ctx context.Context, jobID string) (managementusecase.NodeOnboardingJobResponse, error) {
		return s.management.StartNodeOnboardingJob(ctx, jobID)
	})
}

func (s *Server) handleNodeOnboardingRetry(c *gin.Context) {
	s.handleNodeOnboardingJobAction(c, func(ctx context.Context, jobID string) (managementusecase.NodeOnboardingJobResponse, error) {
		return s.management.RetryNodeOnboardingJob(ctx, jobID)
	})
}

func (s *Server) handleNodeOnboardingJobAction(c *gin.Context, action func(context.Context, string) (managementusecase.NodeOnboardingJobResponse, error)) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	jobID := c.Param("job_id")
	if jobID == "" {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid job_id")
		return
	}

	job, err := action(c.Request.Context(), jobID)
	if err != nil {
		handleNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingJobDTOFromUsecase(job.Job))
}

func (s *Server) handleNodeOnboardingJobs(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	limit, err := parseNodeOnboardingJobLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid limit")
		return
	}

	page, err := s.management.ListNodeOnboardingJobs(c.Request.Context(), managementusecase.ListNodeOnboardingJobsRequest{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		handleNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingJobsResponse{Items: nodeOnboardingJobDTOs(page.Items), NextCursor: page.NextCursor, HasMore: page.HasMore})
}

func (s *Server) handleNodeOnboardingJob(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	jobID := c.Param("job_id")
	if jobID == "" {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid job_id")
		return
	}

	job, err := s.management.GetNodeOnboardingJob(c.Request.Context(), jobID)
	if err != nil {
		handleNodeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeOnboardingJobDTOFromUsecase(job.Job))
}

func handleNodeOnboardingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, controllermeta.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid request")
	case errors.Is(err, raftcluster.ErrInvalidConfig):
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid request")
	case errors.Is(err, controllermeta.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "job not found")
	case errors.Is(err, raftcluster.ErrOnboardingRunningJobExists):
		jsonError(c, http.StatusConflict, "running_job_exists", "running job exists")
	case errors.Is(err, raftcluster.ErrOnboardingPlanNotExecutable):
		jsonError(c, http.StatusConflict, "plan_not_executable", "plan not executable")
	case errors.Is(err, raftcluster.ErrOnboardingPlanStale):
		jsonError(c, http.StatusConflict, "plan_stale", "plan stale")
	case errors.Is(err, raftcluster.ErrOnboardingInvalidJobState):
		jsonError(c, http.StatusConflict, "invalid_job_state", "invalid job state")
	case leaderConsistentReadUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "node onboarding unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func parseNodeOnboardingJobLimit(raw string) (int, error) {
	if raw == "" {
		return defaultNodeOnboardingJobLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxNodeOnboardingJobLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func nodeOnboardingCandidateDTOs(items []managementusecase.NodeOnboardingCandidate) []nodeOnboardingCandidateDTO {
	out := make([]nodeOnboardingCandidateDTO, 0, len(items))
	for _, item := range items {
		out = append(out, nodeOnboardingCandidateDTO{
			NodeID:      item.NodeID,
			Name:        item.Name,
			Addr:        item.Addr,
			Role:        item.Role,
			JoinState:   item.JoinState,
			Status:      item.Status,
			SlotCount:   item.SlotCount,
			LeaderCount: item.LeaderCount,
			Recommended: item.Recommended,
		})
	}
	return out
}

func nodeOnboardingJobDTOs(items []managementusecase.NodeOnboardingJob) []nodeOnboardingJobDTO {
	out := make([]nodeOnboardingJobDTO, 0, len(items))
	for _, item := range items {
		out = append(out, nodeOnboardingJobDTOFromUsecase(item))
	}
	return out
}

func nodeOnboardingJobDTOFromUsecase(item managementusecase.NodeOnboardingJob) nodeOnboardingJobDTO {
	return nodeOnboardingJobDTO{
		JobID:            item.JobID,
		TargetNodeID:     item.TargetNodeID,
		RetryOfJobID:     item.RetryOfJobID,
		Status:           item.Status,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
		StartedAt:        item.StartedAt,
		CompletedAt:      item.CompletedAt,
		PlanVersion:      item.PlanVersion,
		PlanFingerprint:  item.PlanFingerprint,
		Plan:             nodeOnboardingPlanDTOFromUsecase(item.Plan),
		Moves:            nodeOnboardingMoveDTOs(item.Moves),
		CurrentMoveIndex: item.CurrentMoveIndex,
		ResultCounts: nodeOnboardingResultCountsDTO{
			Pending:   item.ResultCounts.Pending,
			Running:   item.ResultCounts.Running,
			Completed: item.ResultCounts.Completed,
			Failed:    item.ResultCounts.Failed,
			Skipped:   item.ResultCounts.Skipped,
		},
		LastError: item.LastError,
	}
}

func nodeOnboardingPlanDTOFromUsecase(item managementusecase.NodeOnboardingPlan) nodeOnboardingPlanDTO {
	return nodeOnboardingPlanDTO{
		TargetNodeID: item.TargetNodeID,
		Summary: nodeOnboardingPlanSummaryDTO{
			CurrentTargetSlotCount:   item.Summary.CurrentTargetSlotCount,
			PlannedTargetSlotCount:   item.Summary.PlannedTargetSlotCount,
			CurrentTargetLeaderCount: item.Summary.CurrentTargetLeaderCount,
			PlannedLeaderGain:        item.Summary.PlannedLeaderGain,
		},
		Moves:          nodeOnboardingPlanMoveDTOs(item.Moves),
		BlockedReasons: nodeOnboardingBlockedReasonDTOs(item.BlockedReasons),
	}
}

func nodeOnboardingPlanMoveDTOs(items []managementusecase.NodeOnboardingPlanMove) []nodeOnboardingPlanMoveDTO {
	out := make([]nodeOnboardingPlanMoveDTO, 0, len(items))
	for _, item := range items {
		out = append(out, nodeOnboardingPlanMoveDTO{
			SlotID:                 item.SlotID,
			SourceNodeID:           item.SourceNodeID,
			TargetNodeID:           item.TargetNodeID,
			Reason:                 item.Reason,
			DesiredPeersBefore:     append([]uint64(nil), item.DesiredPeersBefore...),
			DesiredPeersAfter:      append([]uint64(nil), item.DesiredPeersAfter...),
			CurrentLeaderID:        item.CurrentLeaderID,
			LeaderTransferRequired: item.LeaderTransferRequired,
		})
	}
	return out
}

func nodeOnboardingBlockedReasonDTOs(items []managementusecase.NodeOnboardingBlockedReason) []nodeOnboardingBlockedReasonDTO {
	out := make([]nodeOnboardingBlockedReasonDTO, 0, len(items))
	for _, item := range items {
		out = append(out, nodeOnboardingBlockedReasonDTO{Code: item.Code, Scope: item.Scope, SlotID: item.SlotID, NodeID: item.NodeID, Message: item.Message})
	}
	return out
}

func nodeOnboardingMoveDTOs(items []managementusecase.NodeOnboardingMove) []nodeOnboardingMoveDTO {
	out := make([]nodeOnboardingMoveDTO, 0, len(items))
	for _, item := range items {
		out = append(out, nodeOnboardingMoveDTO{
			SlotID:                 item.SlotID,
			SourceNodeID:           item.SourceNodeID,
			TargetNodeID:           item.TargetNodeID,
			Status:                 item.Status,
			TaskKind:               item.TaskKind,
			TaskSlotID:             item.TaskSlotID,
			StartedAt:              item.StartedAt,
			CompletedAt:            item.CompletedAt,
			LastError:              item.LastError,
			DesiredPeersBefore:     append([]uint64(nil), item.DesiredPeersBefore...),
			DesiredPeersAfter:      append([]uint64(nil), item.DesiredPeersAfter...),
			LeaderBefore:           item.LeaderBefore,
			LeaderAfter:            item.LeaderAfter,
			LeaderTransferRequired: item.LeaderTransferRequired,
		})
	}
	return out
}
