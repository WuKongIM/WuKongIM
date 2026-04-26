package management

import (
	"context"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// NodeOnboardingCandidate is a manager-facing candidate node for explicit Slot allocation.
type NodeOnboardingCandidate struct {
	NodeID      uint64
	Name        string
	Addr        string
	Role        string
	JoinState   string
	Status      string
	SlotCount   int
	LeaderCount int
	Recommended bool
}

// NodeOnboardingCandidatesResponse contains candidate nodes.
type NodeOnboardingCandidatesResponse struct {
	Total int
	Items []NodeOnboardingCandidate
}

// CreateNodeOnboardingPlanRequest creates a reviewed planned job.
type CreateNodeOnboardingPlanRequest struct {
	TargetNodeID uint64
	RetryOfJobID string
}

// ListNodeOnboardingJobsRequest lists manager-facing jobs.
type ListNodeOnboardingJobsRequest struct {
	Limit  int
	Cursor string
}

// NodeOnboardingJobsResponse is one paged job list.
type NodeOnboardingJobsResponse struct {
	Items      []NodeOnboardingJob
	NextCursor string
	HasMore    bool
}

// NodeOnboardingJobResponse wraps one onboarding job.
type NodeOnboardingJobResponse struct {
	Job NodeOnboardingJob
}

// NodeOnboardingJob is the manager-facing durable onboarding job DTO.
type NodeOnboardingJob struct {
	JobID            string
	TargetNodeID     uint64
	RetryOfJobID     string
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	StartedAt        time.Time
	CompletedAt      time.Time
	PlanVersion      uint32
	PlanFingerprint  string
	Plan             NodeOnboardingPlan
	Moves            []NodeOnboardingMove
	CurrentMoveIndex int
	ResultCounts     NodeOnboardingResultCounts
	LastError        string
}

// NodeOnboardingPlan is the manager-facing reviewed plan.
type NodeOnboardingPlan struct {
	TargetNodeID   uint64
	Summary        NodeOnboardingPlanSummary
	Moves          []NodeOnboardingPlanMove
	BlockedReasons []NodeOnboardingBlockedReason
}

// NodeOnboardingPlanSummary contains aggregate plan load effects.
type NodeOnboardingPlanSummary struct {
	CurrentTargetSlotCount   int
	PlannedTargetSlotCount   int
	CurrentTargetLeaderCount int
	PlannedLeaderGain        int
}

// NodeOnboardingPlanMove describes one reviewed Slot move.
type NodeOnboardingPlanMove struct {
	SlotID                 uint32
	SourceNodeID           uint64
	TargetNodeID           uint64
	Reason                 string
	DesiredPeersBefore     []uint64
	DesiredPeersAfter      []uint64
	CurrentLeaderID        uint64
	LeaderTransferRequired bool
}

// NodeOnboardingBlockedReason explains why a plan or Slot is blocked.
type NodeOnboardingBlockedReason struct {
	Code    string
	Scope   string
	SlotID  uint32
	NodeID  uint64
	Message string
}

// NodeOnboardingMove is one durable execution move.
type NodeOnboardingMove struct {
	SlotID                 uint32
	SourceNodeID           uint64
	TargetNodeID           uint64
	Status                 string
	TaskKind               string
	TaskSlotID             uint32
	StartedAt              time.Time
	CompletedAt            time.Time
	LastError              string
	DesiredPeersBefore     []uint64
	DesiredPeersAfter      []uint64
	LeaderBefore           uint64
	LeaderAfter            uint64
	LeaderTransferRequired bool
}

// NodeOnboardingResultCounts summarizes move states.
type NodeOnboardingResultCounts struct {
	Pending   int
	Running   int
	Completed int
	Failed    int
	Skipped   int
}

func (a *App) ListNodeOnboardingCandidates(ctx context.Context) (NodeOnboardingCandidatesResponse, error) {
	if a == nil || a.cluster == nil {
		return NodeOnboardingCandidatesResponse{}, nil
	}
	candidates, err := a.cluster.ListNodeOnboardingCandidates(ctx)
	if err != nil {
		return NodeOnboardingCandidatesResponse{}, err
	}
	items := make([]NodeOnboardingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, nodeOnboardingCandidateDTO(candidate))
	}
	return NodeOnboardingCandidatesResponse{Total: len(items), Items: items}, nil
}

func (a *App) CreateNodeOnboardingPlan(ctx context.Context, req CreateNodeOnboardingPlanRequest) (NodeOnboardingJobResponse, error) {
	if a == nil || a.cluster == nil {
		return NodeOnboardingJobResponse{}, nil
	}
	job, err := a.cluster.CreateNodeOnboardingPlan(ctx, req.TargetNodeID, req.RetryOfJobID)
	if err != nil {
		return NodeOnboardingJobResponse{}, err
	}
	return NodeOnboardingJobResponse{Job: nodeOnboardingJobDTO(job)}, nil
}

func (a *App) StartNodeOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJobResponse, error) {
	if a == nil || a.cluster == nil {
		return NodeOnboardingJobResponse{}, nil
	}
	job, err := a.cluster.StartNodeOnboardingJob(ctx, jobID)
	if err != nil {
		return NodeOnboardingJobResponse{}, err
	}
	return NodeOnboardingJobResponse{Job: nodeOnboardingJobDTO(job)}, nil
}

func (a *App) ListNodeOnboardingJobs(ctx context.Context, req ListNodeOnboardingJobsRequest) (NodeOnboardingJobsResponse, error) {
	if a == nil || a.cluster == nil {
		return NodeOnboardingJobsResponse{}, nil
	}
	jobs, cursor, hasMore, err := a.cluster.ListNodeOnboardingJobs(ctx, req.Limit, req.Cursor)
	if err != nil {
		return NodeOnboardingJobsResponse{}, err
	}
	items := make([]NodeOnboardingJob, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, nodeOnboardingJobDTO(job))
	}
	return NodeOnboardingJobsResponse{Items: items, NextCursor: cursor, HasMore: hasMore}, nil
}

func (a *App) GetNodeOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJobResponse, error) {
	if a == nil || a.cluster == nil {
		return NodeOnboardingJobResponse{}, nil
	}
	job, err := a.cluster.GetNodeOnboardingJob(ctx, jobID)
	if err != nil {
		return NodeOnboardingJobResponse{}, err
	}
	return NodeOnboardingJobResponse{Job: nodeOnboardingJobDTO(job)}, nil
}

func (a *App) RetryNodeOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJobResponse, error) {
	if a == nil || a.cluster == nil {
		return NodeOnboardingJobResponse{}, nil
	}
	job, err := a.cluster.RetryNodeOnboardingJob(ctx, jobID)
	if err != nil {
		return NodeOnboardingJobResponse{}, err
	}
	return NodeOnboardingJobResponse{Job: nodeOnboardingJobDTO(job)}, nil
}

func nodeOnboardingCandidateDTO(candidate raftcluster.NodeOnboardingCandidate) NodeOnboardingCandidate {
	return NodeOnboardingCandidate{
		NodeID:      candidate.NodeID,
		Name:        candidate.Name,
		Addr:        candidate.Addr,
		Role:        nodeRoleString(candidate.Role),
		JoinState:   nodeJoinStateString(candidate.JoinState),
		Status:      managerNodeStatus(candidate.Status),
		SlotCount:   candidate.SlotCount,
		LeaderCount: candidate.LeaderCount,
		Recommended: candidate.Recommended,
	}
}

func nodeOnboardingJobDTO(job controllermeta.NodeOnboardingJob) NodeOnboardingJob {
	return NodeOnboardingJob{
		JobID:            job.JobID,
		TargetNodeID:     job.TargetNodeID,
		RetryOfJobID:     job.RetryOfJobID,
		Status:           string(job.Status),
		CreatedAt:        job.CreatedAt,
		UpdatedAt:        job.UpdatedAt,
		StartedAt:        job.StartedAt,
		CompletedAt:      job.CompletedAt,
		PlanVersion:      job.PlanVersion,
		PlanFingerprint:  job.PlanFingerprint,
		Plan:             nodeOnboardingPlanDTO(job.Plan),
		Moves:            nodeOnboardingMoveDTOs(job.Moves),
		CurrentMoveIndex: job.CurrentMoveIndex,
		ResultCounts:     nodeOnboardingResultCountsDTO(job.ResultCounts),
		LastError:        job.LastError,
	}
}

func nodeOnboardingPlanDTO(plan controllermeta.NodeOnboardingPlan) NodeOnboardingPlan {
	return NodeOnboardingPlan{TargetNodeID: plan.TargetNodeID, Summary: NodeOnboardingPlanSummary(plan.Summary), Moves: nodeOnboardingPlanMoveDTOs(plan.Moves), BlockedReasons: nodeOnboardingBlockedReasonDTOs(plan.BlockedReasons)}
}

func nodeOnboardingPlanMoveDTOs(moves []controllermeta.NodeOnboardingPlanMove) []NodeOnboardingPlanMove {
	out := make([]NodeOnboardingPlanMove, 0, len(moves))
	for _, move := range moves {
		out = append(out, NodeOnboardingPlanMove{SlotID: move.SlotID, SourceNodeID: move.SourceNodeID, TargetNodeID: move.TargetNodeID, Reason: move.Reason, DesiredPeersBefore: append([]uint64(nil), move.DesiredPeersBefore...), DesiredPeersAfter: append([]uint64(nil), move.DesiredPeersAfter...), CurrentLeaderID: move.CurrentLeaderID, LeaderTransferRequired: move.LeaderTransferRequired})
	}
	return out
}

func nodeOnboardingBlockedReasonDTOs(reasons []controllermeta.NodeOnboardingBlockedReason) []NodeOnboardingBlockedReason {
	out := make([]NodeOnboardingBlockedReason, 0, len(reasons))
	for _, reason := range reasons {
		out = append(out, NodeOnboardingBlockedReason(reason))
	}
	return out
}

func nodeOnboardingMoveDTOs(moves []controllermeta.NodeOnboardingMove) []NodeOnboardingMove {
	out := make([]NodeOnboardingMove, 0, len(moves))
	for _, move := range moves {
		out = append(out, NodeOnboardingMove{SlotID: move.SlotID, SourceNodeID: move.SourceNodeID, TargetNodeID: move.TargetNodeID, Status: string(move.Status), TaskKind: managerTaskKind(move.TaskKind), TaskSlotID: move.TaskSlotID, StartedAt: move.StartedAt, CompletedAt: move.CompletedAt, LastError: move.LastError, DesiredPeersBefore: append([]uint64(nil), move.DesiredPeersBefore...), DesiredPeersAfter: append([]uint64(nil), move.DesiredPeersAfter...), LeaderBefore: move.LeaderBefore, LeaderAfter: move.LeaderAfter, LeaderTransferRequired: move.LeaderTransferRequired})
	}
	return out
}

func nodeOnboardingResultCountsDTO(counts controllermeta.OnboardingResultCounts) NodeOnboardingResultCounts {
	return NodeOnboardingResultCounts{Pending: counts.Pending, Running: counts.Running, Completed: counts.Completed, Failed: counts.Failed, Skipped: counts.Skipped}
}

func nodeRoleString(role controllermeta.NodeRole) string {
	switch role {
	case controllermeta.NodeRoleData:
		return "data"
	case controllermeta.NodeRoleControllerVoter:
		return "controller_voter"
	default:
		return "unknown"
	}
}

func nodeJoinStateString(state controllermeta.NodeJoinState) string {
	switch state {
	case controllermeta.NodeJoinStateJoining:
		return "joining"
	case controllermeta.NodeJoinStateActive:
		return "active"
	case controllermeta.NodeJoinStateRejected:
		return "rejected"
	default:
		return "unknown"
	}
}
