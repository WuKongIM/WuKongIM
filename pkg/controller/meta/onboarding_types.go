package meta

import "time"

// OnboardingJobStatus records the durable lifecycle state of a node onboarding job.
type OnboardingJobStatus string

const (
	// OnboardingJobStatusPlanned means the job is persisted for operator review.
	OnboardingJobStatusPlanned OnboardingJobStatus = "planned"
	// OnboardingJobStatusRunning means the controller leader may advance job moves.
	OnboardingJobStatusRunning OnboardingJobStatus = "running"
	// OnboardingJobStatusFailed means execution stopped after a move or validation error.
	OnboardingJobStatusFailed OnboardingJobStatus = "failed"
	// OnboardingJobStatusCompleted means all moves reached a terminal successful state.
	OnboardingJobStatusCompleted OnboardingJobStatus = "completed"
	// OnboardingJobStatusCancelled is reserved for explicit operator cancellation.
	OnboardingJobStatusCancelled OnboardingJobStatus = "cancelled"
)

// OnboardingMoveStatus records the durable lifecycle state of one slot move.
type OnboardingMoveStatus string

const (
	// OnboardingMoveStatusPending means the move has not started yet.
	OnboardingMoveStatusPending OnboardingMoveStatus = "pending"
	// OnboardingMoveStatusRunning means the move has a reconcile task in progress.
	OnboardingMoveStatusRunning OnboardingMoveStatus = "running"
	// OnboardingMoveStatusCompleted means the move finished successfully.
	OnboardingMoveStatusCompleted OnboardingMoveStatus = "completed"
	// OnboardingMoveStatusFailed means the move stopped with an error.
	OnboardingMoveStatusFailed OnboardingMoveStatus = "failed"
	// OnboardingMoveStatusSkipped means the target state was already satisfied.
	OnboardingMoveStatusSkipped OnboardingMoveStatus = "skipped"
)

// NodeOnboardingJob is the durable audit and execution state for moving slot resources to one target node.
type NodeOnboardingJob struct {
	// JobID is the stable non-empty identity for this onboarding job.
	JobID string
	// TargetNodeID is the active data node that should receive slot resources.
	TargetNodeID uint64
	// RetryOfJobID links a retry job to a previous failed job when applicable.
	RetryOfJobID string
	// Status is the durable job lifecycle state.
	Status OnboardingJobStatus
	// CreatedAt records when the reviewed plan was first persisted.
	CreatedAt time.Time
	// UpdatedAt records the latest durable state mutation time.
	UpdatedAt time.Time
	// StartedAt records when execution first moved from planned to running.
	StartedAt time.Time
	// CompletedAt records when the job reached any terminal state.
	CompletedAt time.Time
	// PlanVersion identifies the plan schema used to build this job.
	PlanVersion uint32
	// PlanFingerprint verifies that the leader view still matches the reviewed plan.
	PlanFingerprint string
	// Plan is the operator-reviewed onboarding plan.
	Plan NodeOnboardingPlan
	// Moves is the durable execution state for each planned slot move.
	Moves []NodeOnboardingMove
	// CurrentMoveIndex points at the active move, or -1 before execution starts.
	CurrentMoveIndex int
	// ResultCounts stores the latest summarized move counts for audit responses.
	ResultCounts OnboardingResultCounts
	// CurrentTask stores the reconcile task currently backing the active move.
	CurrentTask *ReconcileTask
	// LastError stores the latest job-level execution or validation error.
	LastError string
}

// NodeOnboardingPlan is the immutable reviewed plan for one target node.
type NodeOnboardingPlan struct {
	// TargetNodeID is the node that should receive the planned resources.
	TargetNodeID uint64
	// Summary captures aggregate load effects of the planned moves.
	Summary NodeOnboardingPlanSummary
	// Moves contains deterministic slot move proposals.
	Moves []NodeOnboardingPlanMove
	// BlockedReasons explains why planning could not produce more safe moves.
	BlockedReasons []NodeOnboardingBlockedReason
}

// NodeOnboardingPlanSummary records aggregate before and after load estimates.
type NodeOnboardingPlanSummary struct {
	// CurrentTargetSlotCount is the current number of slots on the target node.
	CurrentTargetSlotCount int
	// PlannedTargetSlotCount is the expected target slot count after planned moves.
	PlannedTargetSlotCount int
	// CurrentTargetLeaderCount is the current leader count on the target node.
	CurrentTargetLeaderCount int
	// PlannedLeaderGain is the number of planned leader transfers to the target.
	PlannedLeaderGain int
}

// NodeOnboardingPlanMove describes one planned slot replica move.
type NodeOnboardingPlanMove struct {
	// SlotID is the physical slot whose desired peer set changes.
	SlotID uint32
	// SourceNodeID is the node that gives up the slot replica.
	SourceNodeID uint64
	// TargetNodeID is the onboarding node that receives the slot replica.
	TargetNodeID uint64
	// Reason is a stable planner explanation for this move.
	Reason string
	// DesiredPeersBefore is the canonical peer set before the move.
	DesiredPeersBefore []uint64
	// DesiredPeersAfter is the canonical peer set expected after the move.
	DesiredPeersAfter []uint64
	// CurrentLeaderID is the observed slot leader when the plan was created.
	CurrentLeaderID uint64
	// LeaderTransferRequired requires the executor to move leadership to the target.
	LeaderTransferRequired bool
}

// NodeOnboardingBlockedReason explains a planner safety or capacity limitation.
type NodeOnboardingBlockedReason struct {
	// Code is the stable machine-readable blocked reason code.
	Code string
	// SlotID optionally identifies the blocked slot.
	SlotID uint32
	// NodeID optionally identifies the blocked node.
	NodeID uint64
	// Message is a concise operator-facing explanation.
	Message string
}

// NodeOnboardingMove stores execution state for one planned slot move.
type NodeOnboardingMove struct {
	// SlotID is the physical slot being moved.
	SlotID uint32
	// SourceNodeID is the node that gives up the slot replica.
	SourceNodeID uint64
	// TargetNodeID is the onboarding node that receives the slot replica.
	TargetNodeID uint64
	// Status is the durable move lifecycle state.
	Status OnboardingMoveStatus
	// TaskKind is the reconcile task kind used to execute the move.
	TaskKind TaskKind
	// TaskSlotID is the reconcile task slot identity used for execution.
	TaskSlotID uint32
	// StartedAt records when this move first started execution.
	StartedAt time.Time
	// CompletedAt records when this move reached a terminal state.
	CompletedAt time.Time
	// LastError stores the latest move-level execution error.
	LastError string
	// DesiredPeersBefore is the canonical peer set before the move.
	DesiredPeersBefore []uint64
	// DesiredPeersAfter is the canonical peer set expected after the move.
	DesiredPeersAfter []uint64
	// LeaderBefore is the leader observed before the move started.
	LeaderBefore uint64
	// LeaderAfter is the leader observed after the move completed.
	LeaderAfter uint64
	// LeaderTransferRequired requires the executor to move leadership to the target.
	LeaderTransferRequired bool
}

// OnboardingResultCounts stores summarized move counts by durable status.
type OnboardingResultCounts struct {
	// Pending is the number of moves that have not started yet.
	Pending int
	// Running is the number of moves currently executing.
	Running int
	// Completed is the number of moves that finished successfully.
	Completed int
	// Failed is the number of moves that stopped with an error.
	Failed int
	// Skipped is the number of moves whose target state was already satisfied.
	Skipped int
}
