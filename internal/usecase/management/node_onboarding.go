package management

import (
	"context"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// NodeOnboardingCandidate is a manager-facing candidate node for explicit Slot allocation.
type NodeOnboardingCandidate struct {
	// NodeID is the stable cluster node identity.
	NodeID uint64
	// Name is the optional operator-facing node name.
	Name string
	// Addr is the node RPC address.
	Addr string
	// Role is the manager-facing node role string.
	Role string
	// JoinState is the dynamic membership state string.
	JoinState string
	// Status is the manager-facing health status string.
	Status string
	// SlotCount is the number of Slot replicas currently assigned to this node.
	SlotCount int
	// LeaderCount is the number of observed Slot leaders on this node.
	LeaderCount int
	// Recommended marks nodes below average replica load.
	Recommended bool
}

// NodeOnboardingCandidatesResponse contains candidate nodes.
type NodeOnboardingCandidatesResponse struct {
	// Total is the number of returned candidates.
	Total int
	// Items contains the candidate node list.
	Items []NodeOnboardingCandidate
}

// CreateNodeOnboardingPlanRequest creates a reviewed planned job.
type CreateNodeOnboardingPlanRequest struct {
	// TargetNodeID is the node that should receive Slot resources.
	TargetNodeID uint64
	// RetryOfJobID links internal retry-created plans to a previous failed job.
	RetryOfJobID string
}

// ListNodeOnboardingJobsRequest lists manager-facing jobs.
type ListNodeOnboardingJobsRequest struct {
	// Limit caps the number of jobs returned.
	Limit int
	// Cursor is the opaque pagination cursor returned by the cluster API.
	Cursor string
}

// NodeOnboardingJobsResponse is one paged job list.
type NodeOnboardingJobsResponse struct {
	// Items contains the returned jobs.
	Items []NodeOnboardingJob
	// NextCursor is the opaque cursor for the next page.
	NextCursor string
	// HasMore reports whether another page is available.
	HasMore bool
}

// NodeOnboardingJobResponse wraps one onboarding job.
type NodeOnboardingJobResponse struct {
	// Job is the manager-facing onboarding job DTO.
	Job NodeOnboardingJob
}

// NodeOnboardingJob is the manager-facing durable onboarding job DTO.
type NodeOnboardingJob struct {
	// JobID is the stable durable job identity.
	JobID string
	// TargetNodeID is the node receiving Slot resources.
	TargetNodeID uint64
	// RetryOfJobID identifies the failed job this job retries, when present.
	RetryOfJobID string
	// Status is the durable job lifecycle status.
	Status string
	// CreatedAt records when the plan was persisted.
	CreatedAt time.Time
	// UpdatedAt records the latest durable mutation time.
	UpdatedAt time.Time
	// StartedAt records when execution began.
	StartedAt time.Time
	// CompletedAt records when the job reached a terminal state.
	CompletedAt time.Time
	// PlanVersion identifies the persisted plan schema.
	PlanVersion uint32
	// PlanFingerprint verifies the reviewed plan against current controller state.
	PlanFingerprint string
	// Plan is the immutable operator-reviewed plan.
	Plan NodeOnboardingPlan
	// Moves contains per-Slot execution state.
	Moves []NodeOnboardingMove
	// CurrentMoveIndex points at the active move, or -1 before execution.
	CurrentMoveIndex int
	// ResultCounts summarizes move status totals.
	ResultCounts NodeOnboardingResultCounts
	// LastError stores the latest job-level error.
	LastError string
}

// NodeOnboardingPlan is the manager-facing reviewed plan.
type NodeOnboardingPlan struct {
	// TargetNodeID is the node receiving Slot resources.
	TargetNodeID uint64
	// Summary contains aggregate before/after load estimates.
	Summary NodeOnboardingPlanSummary
	// Moves contains deterministic Slot move proposals.
	Moves []NodeOnboardingPlanMove
	// BlockedReasons explains why planning could not produce more safe moves.
	BlockedReasons []NodeOnboardingBlockedReason
}

// NodeOnboardingPlanSummary contains aggregate plan load effects.
type NodeOnboardingPlanSummary struct {
	// CurrentTargetSlotCount is the target's current assigned Slot replica count.
	CurrentTargetSlotCount int
	// PlannedTargetSlotCount is the expected target Slot replica count after moves.
	PlannedTargetSlotCount int
	// CurrentTargetLeaderCount is the target's current observed leader count.
	CurrentTargetLeaderCount int
	// PlannedLeaderGain is the number of planned leader transfers to the target.
	PlannedLeaderGain int
}

// NodeOnboardingPlanMove describes one reviewed Slot move.
type NodeOnboardingPlanMove struct {
	// SlotID is the physical Slot whose desired peers change.
	SlotID uint32
	// SourceNodeID is the node giving up the Slot replica.
	SourceNodeID uint64
	// TargetNodeID is the onboarding node receiving the Slot replica.
	TargetNodeID uint64
	// Reason is the stable planner explanation for this move.
	Reason string
	// DesiredPeersBefore is the canonical peer set before the move.
	DesiredPeersBefore []uint64
	// DesiredPeersAfter is the canonical peer set after the move.
	DesiredPeersAfter []uint64
	// CurrentLeaderID is the observed leader when the plan was created.
	CurrentLeaderID uint64
	// LeaderTransferRequired marks moves that should transfer leadership to the target.
	LeaderTransferRequired bool
}

// NodeOnboardingBlockedReason explains why a plan or Slot is blocked.
type NodeOnboardingBlockedReason struct {
	// Code is the stable machine-readable blocked reason.
	Code string
	// Scope identifies the planner level that produced the reason.
	Scope string
	// SlotID optionally identifies the blocked Slot.
	SlotID uint32
	// NodeID optionally identifies the blocked node.
	NodeID uint64
	// Message is a concise operator-facing explanation.
	Message string
}

// NodeOnboardingMove is one durable execution move.
type NodeOnboardingMove struct {
	// SlotID is the physical Slot being moved.
	SlotID uint32
	// SourceNodeID is the node giving up the Slot replica.
	SourceNodeID uint64
	// TargetNodeID is the onboarding node receiving the Slot replica.
	TargetNodeID uint64
	// Status is the durable move lifecycle status.
	Status string
	// TaskKind is the reconcile task kind backing the move.
	TaskKind string
	// TaskSlotID is the reconcile task Slot identifier.
	TaskSlotID uint32
	// StartedAt records when this move started.
	StartedAt time.Time
	// CompletedAt records when this move reached a terminal state.
	CompletedAt time.Time
	// LastError stores the latest move-level error.
	LastError string
	// DesiredPeersBefore is the canonical peer set before the move.
	DesiredPeersBefore []uint64
	// DesiredPeersAfter is the canonical peer set after the move.
	DesiredPeersAfter []uint64
	// LeaderBefore is the leader observed before the move started.
	LeaderBefore uint64
	// LeaderAfter is the leader observed after the move completed.
	LeaderAfter uint64
	// LeaderTransferRequired marks moves that should transfer leadership to the target.
	LeaderTransferRequired bool
}

// NodeOnboardingResultCounts summarizes move states.
type NodeOnboardingResultCounts struct {
	// Pending is the number of moves that have not started.
	Pending int
	// Running is the number of moves currently executing.
	Running int
	// Completed is the number of successful moves.
	Completed int
	// Failed is the number of failed moves.
	Failed int
	// Skipped is the number of moves already satisfied.
	Skipped int
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
