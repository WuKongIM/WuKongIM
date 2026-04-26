package cluster

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
)

var (
	// ErrOnboardingRunningJobExists means another node onboarding job is already running.
	ErrOnboardingRunningJobExists = errors.New("raftcluster: onboarding running job exists")
	// ErrOnboardingPlanNotExecutable means the reviewed onboarding plan has no safe executable moves.
	ErrOnboardingPlanNotExecutable = errors.New("raftcluster: onboarding plan not executable")
	// ErrOnboardingPlanStale means the reviewed onboarding plan no longer matches controller state.
	ErrOnboardingPlanStale = errors.New("raftcluster: onboarding plan stale")
	// ErrOnboardingInvalidJobState means the requested job transition is invalid for its current state.
	ErrOnboardingInvalidJobState = errors.New("raftcluster: onboarding invalid job state")
)

// NodeOnboardingCandidate describes a data node that can be reviewed for explicit Slot allocation.
type NodeOnboardingCandidate struct {
	// NodeID is the stable cluster node identity.
	NodeID uint64
	// Name is the optional operator-facing display name.
	Name string
	// Addr is the node RPC address.
	Addr string
	// Role is the durable controller role.
	Role controllermeta.NodeRole
	// JoinState is the dynamic-join membership state.
	JoinState controllermeta.NodeJoinState
	// Status is the current controller health status.
	Status controllermeta.NodeStatus
	// SlotCount is the number of physical Slot replicas currently assigned to this node.
	SlotCount int
	// LeaderCount is the number of observed physical Slot leaders currently on this node.
	LeaderCount int
	// Recommended is true when the node is below the current average Slot replica load.
	Recommended bool
}

// ListNodeOnboardingCandidates returns active data nodes with current Slot/leader counts.
func (c *Cluster) ListNodeOnboardingCandidates(ctx context.Context) ([]NodeOnboardingCandidate, error) {
	if c != nil && c.isLocalControllerLeader() && c.controllerMeta != nil {
		return c.listNodeOnboardingCandidatesOnLeader(ctx)
	}
	if c != nil && c.controllerClient != nil {
		var candidates []NodeOnboardingCandidate
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			candidates, err = c.controllerClient.ListNodeOnboardingCandidates(attemptCtx)
			return err
		})
		return candidates, err
	}
	return nil, ErrNotStarted
}

// CreateNodeOnboardingPlan persists a planned onboarding job for operator review.
func (c *Cluster) CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error) {
	if c != nil && c.isLocalControllerLeader() && c.controllerMeta != nil {
		return c.createNodeOnboardingPlanOnLeader(ctx, targetNodeID, retryOfJobID)
	}
	if c != nil && c.controllerClient != nil {
		var job controllermeta.NodeOnboardingJob
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			job, err = c.controllerClient.CreateNodeOnboardingPlan(attemptCtx, targetNodeID, retryOfJobID)
			return err
		})
		return job, err
	}
	return controllermeta.NodeOnboardingJob{}, ErrNotStarted
}

// StartNodeOnboardingJob starts a reviewed planned job without regenerating its plan.
func (c *Cluster) StartNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	if c != nil && c.isLocalControllerLeader() && c.controllerMeta != nil {
		return c.startNodeOnboardingJobOnLeader(ctx, jobID)
	}
	if c != nil && c.controllerClient != nil {
		var job controllermeta.NodeOnboardingJob
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			job, err = c.controllerClient.StartNodeOnboardingJob(attemptCtx, jobID)
			return err
		})
		return job, err
	}
	return controllermeta.NodeOnboardingJob{}, ErrNotStarted
}

// ListNodeOnboardingJobs returns onboarding jobs in manager-facing created_at desc order.
func (c *Cluster) ListNodeOnboardingJobs(ctx context.Context, limit int, cursor string) ([]controllermeta.NodeOnboardingJob, string, bool, error) {
	if c != nil && c.isLocalControllerLeader() && c.controllerMeta != nil {
		return c.listNodeOnboardingJobsOnLeader(ctx, limit, cursor)
	}
	if c != nil && c.controllerClient != nil {
		var jobs []controllermeta.NodeOnboardingJob
		var nextCursor string
		var hasMore bool
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			jobs, nextCursor, hasMore, err = c.controllerClient.ListNodeOnboardingJobs(attemptCtx, limit, cursor)
			return err
		})
		return jobs, nextCursor, hasMore, err
	}
	return nil, "", false, ErrNotStarted
}

// GetNodeOnboardingJob returns one durable onboarding job.
func (c *Cluster) GetNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	if c != nil && c.isLocalControllerLeader() && c.controllerMeta != nil {
		return c.controllerMeta.GetOnboardingJob(ctx, jobID)
	}
	if c != nil && c.controllerClient != nil {
		var job controllermeta.NodeOnboardingJob
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			job, err = c.controllerClient.GetNodeOnboardingJob(attemptCtx, jobID)
			return err
		})
		return job, err
	}
	return controllermeta.NodeOnboardingJob{}, ErrNotStarted
}

// RetryNodeOnboardingJob creates a new planned job for the target of a failed job.
func (c *Cluster) RetryNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	if c != nil && c.isLocalControllerLeader() && c.controllerMeta != nil {
		job, err := c.controllerMeta.GetOnboardingJob(ctx, jobID)
		if err != nil {
			return controllermeta.NodeOnboardingJob{}, err
		}
		if job.Status != controllermeta.OnboardingJobStatusFailed {
			return controllermeta.NodeOnboardingJob{}, ErrOnboardingInvalidJobState
		}
		return c.createNodeOnboardingPlanOnLeader(ctx, job.TargetNodeID, job.JobID)
	}
	if c != nil && c.controllerClient != nil {
		var job controllermeta.NodeOnboardingJob
		err := c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			var err error
			job, err = c.controllerClient.RetryNodeOnboardingJob(attemptCtx, jobID)
			return err
		})
		return job, err
	}
	return controllermeta.NodeOnboardingJob{}, ErrNotStarted
}

func (c *Cluster) listNodeOnboardingCandidatesOnLeader(ctx context.Context) ([]NodeOnboardingCandidate, error) {
	state, _, err := c.snapshotNodeOnboardingState(ctx)
	if err != nil {
		return nil, err
	}
	loads := slotcontrollerSlotLoads(state.Assignments)
	leaders := make(map[uint64]int)
	for _, view := range state.Runtime {
		if view.LeaderID != 0 {
			leaders[view.LeaderID]++
		}
	}
	active := 0
	totalReplicas := 0
	for _, node := range state.Nodes {
		if node.Role == controllermeta.NodeRoleData && node.JoinState == controllermeta.NodeJoinStateActive {
			active++
		}
	}
	for _, assignment := range state.Assignments {
		totalReplicas += len(assignment.DesiredPeers)
	}
	targetMax := 0
	if active > 0 {
		targetMax = (totalReplicas + active - 1) / active
	}

	candidates := make([]NodeOnboardingCandidate, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.Role != controllermeta.NodeRoleData || node.JoinState != controllermeta.NodeJoinStateActive {
			continue
		}
		slotCount := loads[node.NodeID]
		candidates = append(candidates, NodeOnboardingCandidate{
			NodeID:      node.NodeID,
			Name:        node.Name,
			Addr:        node.Addr,
			Role:        node.Role,
			JoinState:   node.JoinState,
			Status:      node.Status,
			SlotCount:   slotCount,
			LeaderCount: leaders[node.NodeID],
			Recommended: slotCount == 0 || (targetMax > 0 && slotCount < targetMax),
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].NodeID < candidates[j].NodeID })
	return candidates, nil
}

func (c *Cluster) createNodeOnboardingPlanOnLeader(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error) {
	if targetNodeID == 0 {
		return controllermeta.NodeOnboardingJob{}, ErrInvalidConfig
	}
	state, runningJobs, err := c.snapshotNodeOnboardingState(ctx)
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	if _, ok := state.Nodes[targetNodeID]; !ok {
		return controllermeta.NodeOnboardingJob{}, controllermeta.ErrNotFound
	}
	state.TargetNodeID = targetNodeID
	state.RunningJobExists = len(runningJobs) > 0

	plan := slotcontroller.NewOnboardingPlanner(slotcontroller.OnboardingPlannerConfig{ReplicaN: c.cfg.SlotReplicaN}).Plan(state)
	now := time.Now().UTC()
	jobID, err := c.nextNodeOnboardingJobID(ctx, now)
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	job := buildPlannedNodeOnboardingJob(jobID, retryOfJobID, now, plan, state)
	if err := c.proposeNodeOnboardingJobUpdate(ctx, slotcontroller.NodeOnboardingJobUpdate{Job: &job}); err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	return c.controllerMeta.GetOnboardingJob(ctx, job.JobID)
}

func (c *Cluster) startNodeOnboardingJobOnLeader(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error) {
	job, err := c.controllerMeta.GetOnboardingJob(ctx, jobID)
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	if job.Status != controllermeta.OnboardingJobStatusPlanned {
		return controllermeta.NodeOnboardingJob{}, ErrOnboardingInvalidJobState
	}
	state, runningJobs, err := c.snapshotNodeOnboardingState(ctx)
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	for _, running := range runningJobs {
		if running.JobID != job.JobID {
			return controllermeta.NodeOnboardingJob{}, ErrOnboardingRunningJobExists
		}
	}
	state.TargetNodeID = job.TargetNodeID
	state.RunningJobExists = false
	if result := slotcontroller.ValidateNodeOnboardingStart(state, job); result.Err != nil {
		return controllermeta.NodeOnboardingJob{}, mapPlaneOnboardingError(result.Err)
	}

	now := time.Now().UTC()
	next := cloneClusterOnboardingJob(job)
	next.Status = controllermeta.OnboardingJobStatusRunning
	next.StartedAt = now
	next.UpdatedAt = now
	next.CurrentMoveIndex = -1
	next.CurrentTask = nil
	next.ResultCounts = countClusterOnboardingMoves(next.Moves)
	expected := controllermeta.OnboardingJobStatusPlanned
	if err := c.proposeNodeOnboardingJobUpdate(ctx, slotcontroller.NodeOnboardingJobUpdate{Job: &next, ExpectedStatus: &expected}); err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	stored, err := c.controllerMeta.GetOnboardingJob(ctx, jobID)
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	if stored.Status == controllermeta.OnboardingJobStatusRunning {
		return stored, nil
	}
	runningJobs, err = c.controllerMeta.ListRunningOnboardingJobs(ctx)
	if err != nil {
		return controllermeta.NodeOnboardingJob{}, err
	}
	for _, running := range runningJobs {
		if running.JobID != jobID {
			return controllermeta.NodeOnboardingJob{}, ErrOnboardingRunningJobExists
		}
	}
	return controllermeta.NodeOnboardingJob{}, ErrOnboardingPlanStale
}

func (c *Cluster) listNodeOnboardingJobsOnLeader(ctx context.Context, limit int, cursor string) ([]controllermeta.NodeOnboardingJob, string, bool, error) {
	if limit < 0 {
		return nil, "", false, ErrInvalidConfig
	}
	jobs, _, _, err := c.controllerMeta.ListOnboardingJobs(ctx, 0, "")
	if err != nil {
		return nil, "", false, err
	}
	sortNodeOnboardingJobsForAPI(jobs)
	start := 0
	if cursor != "" {
		cursorTime, cursorID, err := decodeNodeOnboardingCursor(cursor)
		if err != nil {
			return nil, "", false, ErrInvalidConfig
		}
		for start < len(jobs) {
			job := jobs[start]
			if job.CreatedAt.Equal(cursorTime) && job.JobID == cursorID {
				start++
				break
			}
			start++
		}
	}
	if start >= len(jobs) {
		return nil, "", false, nil
	}
	if limit == 0 {
		limit = len(jobs)
	}
	end := start + limit
	hasMore := false
	if end < len(jobs) {
		hasMore = true
	} else {
		end = len(jobs)
	}
	out := cloneClusterOnboardingJobs(jobs[start:end])
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeNodeOnboardingCursor(last.CreatedAt, last.JobID)
	}
	return out, nextCursor, hasMore, nil
}

func (c *Cluster) snapshotNodeOnboardingState(ctx context.Context) (slotcontroller.OnboardingPlanInput, []controllermeta.NodeOnboardingJob, error) {
	planner, err := c.snapshotPlannerState(ctx)
	if err != nil {
		return slotcontroller.OnboardingPlanInput{}, nil, err
	}
	state := slotcontroller.OnboardingPlanInput{
		Nodes:          planner.Nodes,
		Assignments:    planner.Assignments,
		Runtime:        planner.Runtime,
		Tasks:          planner.Tasks,
		MigratingSlots: activeMigratingSlots(c.GetHashSlotTable()),
		Now:            planner.Now,
	}
	running, err := c.controllerMeta.ListRunningOnboardingJobs(ctx)
	if err != nil {
		return slotcontroller.OnboardingPlanInput{}, nil, err
	}
	return state, running, nil
}

func (c *Cluster) proposeNodeOnboardingJobUpdate(ctx context.Context, update slotcontroller.NodeOnboardingJobUpdate) error {
	if c == nil || c.controller == nil {
		return ErrNotStarted
	}
	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind:           slotcontroller.CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &update,
	}); err != nil {
		if errors.Is(err, controllerraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	return nil
}

func (c *Cluster) nextNodeOnboardingJobID(ctx context.Context, now time.Time) (string, error) {
	jobs, _, _, err := c.controllerMeta.ListOnboardingJobs(ctx, 0, "")
	if err != nil {
		return "", err
	}
	prefix := "onboard-" + now.UTC().Format("20060102") + "-"
	maxSuffix := 0
	for _, job := range jobs {
		if !strings.HasPrefix(job.JobID, prefix) {
			continue
		}
		suffix, err := strconv.Atoi(strings.TrimPrefix(job.JobID, prefix))
		if err == nil && suffix > maxSuffix {
			maxSuffix = suffix
		}
	}
	return fmt.Sprintf("%s%06d", prefix, maxSuffix+1), nil
}

func buildPlannedNodeOnboardingJob(jobID, retryOfJobID string, now time.Time, plan controllermeta.NodeOnboardingPlan, state slotcontroller.OnboardingPlanInput) controllermeta.NodeOnboardingJob {
	moves := make([]controllermeta.NodeOnboardingMove, 0, len(plan.Moves))
	for _, move := range plan.Moves {
		moves = append(moves, controllermeta.NodeOnboardingMove{
			SlotID:                 move.SlotID,
			SourceNodeID:           move.SourceNodeID,
			TargetNodeID:           move.TargetNodeID,
			Status:                 controllermeta.OnboardingMoveStatusPending,
			TaskKind:               controllermeta.TaskKindRebalance,
			TaskSlotID:             move.SlotID,
			DesiredPeersBefore:     append([]uint64(nil), move.DesiredPeersBefore...),
			DesiredPeersAfter:      append([]uint64(nil), move.DesiredPeersAfter...),
			LeaderBefore:           move.CurrentLeaderID,
			LeaderTransferRequired: move.LeaderTransferRequired,
		})
	}
	job := controllermeta.NodeOnboardingJob{
		JobID:            jobID,
		TargetNodeID:     plan.TargetNodeID,
		RetryOfJobID:     retryOfJobID,
		Status:           controllermeta.OnboardingJobStatusPlanned,
		CreatedAt:        now,
		UpdatedAt:        now,
		PlanVersion:      1,
		Plan:             plan,
		Moves:            moves,
		CurrentMoveIndex: -1,
		ResultCounts:     countClusterOnboardingMoves(moves),
	}
	job.PlanFingerprint = slotcontroller.OnboardingPlanFingerprint(slotcontroller.OnboardingPlanFingerprintInput{
		TargetNode:     state.Nodes[plan.TargetNodeID],
		Plan:           plan,
		Assignments:    state.Assignments,
		Runtime:        state.Runtime,
		Tasks:          state.Tasks,
		MigratingSlots: state.MigratingSlots,
	})
	return job
}

func activeMigratingSlots(table *HashSlotTable) map[uint32]struct{} {
	out := make(map[uint32]struct{})
	if table == nil {
		return out
	}
	for _, migration := range table.ActiveMigrations() {
		out[uint32(migration.Source)] = struct{}{}
		out[uint32(migration.Target)] = struct{}{}
	}
	return out
}

func slotcontrollerSlotLoads(assignments map[uint32]controllermeta.SlotAssignment) map[uint64]int {
	loads := make(map[uint64]int)
	for _, assignment := range assignments {
		for _, peer := range assignment.DesiredPeers {
			loads[peer]++
		}
	}
	return loads
}

func mapPlaneOnboardingError(err error) error {
	switch {
	case errors.Is(err, slotcontroller.ErrOnboardingPlanNotExecutable):
		return ErrOnboardingPlanNotExecutable
	case errors.Is(err, slotcontroller.ErrOnboardingPlanStale):
		return ErrOnboardingPlanStale
	default:
		return err
	}
}

func onboardingErrorCodeFromError(err error) onboardingErrorCode {
	switch {
	case errors.Is(err, ErrOnboardingRunningJobExists):
		return onboardingErrorRunningJobExists
	case errors.Is(err, ErrOnboardingPlanNotExecutable):
		return onboardingErrorPlanNotExecutable
	case errors.Is(err, ErrOnboardingPlanStale):
		return onboardingErrorPlanStale
	case errors.Is(err, ErrOnboardingInvalidJobState):
		return onboardingErrorInvalidJobState
	default:
		return onboardingErrorNone
	}
}

func onboardingErrorFromCode(code onboardingErrorCode) error {
	switch code {
	case onboardingErrorRunningJobExists:
		return ErrOnboardingRunningJobExists
	case onboardingErrorPlanNotExecutable:
		return ErrOnboardingPlanNotExecutable
	case onboardingErrorPlanStale:
		return ErrOnboardingPlanStale
	case onboardingErrorInvalidJobState:
		return ErrOnboardingInvalidJobState
	default:
		return nil
	}
}

func sortNodeOnboardingJobsForAPI(jobs []controllermeta.NodeOnboardingJob) {
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].JobID > jobs[j].JobID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
}

func encodeNodeOnboardingCursor(createdAt time.Time, jobID string) string {
	raw := fmt.Sprintf("%d|%s", createdAt.UTC().UnixNano(), jobID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeNodeOnboardingCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", ErrInvalidConfig
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", err
	}
	return time.Unix(0, nanos).UTC(), parts[1], nil
}

func cloneClusterOnboardingJobs(jobs []controllermeta.NodeOnboardingJob) []controllermeta.NodeOnboardingJob {
	out := make([]controllermeta.NodeOnboardingJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, cloneClusterOnboardingJob(job))
	}
	return out
}

func cloneClusterOnboardingJob(job controllermeta.NodeOnboardingJob) controllermeta.NodeOnboardingJob {
	next := job
	next.Plan.Moves = append([]controllermeta.NodeOnboardingPlanMove(nil), job.Plan.Moves...)
	for i := range next.Plan.Moves {
		next.Plan.Moves[i].DesiredPeersBefore = append([]uint64(nil), job.Plan.Moves[i].DesiredPeersBefore...)
		next.Plan.Moves[i].DesiredPeersAfter = append([]uint64(nil), job.Plan.Moves[i].DesiredPeersAfter...)
	}
	next.Plan.BlockedReasons = append([]controllermeta.NodeOnboardingBlockedReason(nil), job.Plan.BlockedReasons...)
	next.Moves = append([]controllermeta.NodeOnboardingMove(nil), job.Moves...)
	for i := range next.Moves {
		next.Moves[i].DesiredPeersBefore = append([]uint64(nil), job.Moves[i].DesiredPeersBefore...)
		next.Moves[i].DesiredPeersAfter = append([]uint64(nil), job.Moves[i].DesiredPeersAfter...)
	}
	if job.CurrentTask != nil {
		task := *job.CurrentTask
		next.CurrentTask = &task
	}
	return next
}

func countClusterOnboardingMoves(moves []controllermeta.NodeOnboardingMove) controllermeta.OnboardingResultCounts {
	var counts controllermeta.OnboardingResultCounts
	for _, move := range moves {
		switch move.Status {
		case controllermeta.OnboardingMoveStatusPending:
			counts.Pending++
		case controllermeta.OnboardingMoveStatusRunning:
			counts.Running++
		case controllermeta.OnboardingMoveStatusCompleted:
			counts.Completed++
		case controllermeta.OnboardingMoveStatusFailed:
			counts.Failed++
		case controllermeta.OnboardingMoveStatusSkipped:
			counts.Skipped++
		}
	}
	return counts
}
