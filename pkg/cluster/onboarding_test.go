package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/stretchr/testify/require"
)

func TestCreateNodeOnboardingPlanPersistsPlannedJob(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)

	job, err := c.CreateNodeOnboardingPlan(context.Background(), 4, "")

	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingJobStatusPlanned, job.Status)
	require.NotEmpty(t, job.PlanFingerprint)
	require.NotEmpty(t, job.Moves)

	stored, err := c.controllerMeta.GetOnboardingJob(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, job.JobID, stored.JobID)
}

func TestCreateNodeOnboardingPlanPersistsBlockedPlanWhenJobRuns(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), sampleClusterOnboardingJob("running", controllermeta.OnboardingJobStatusRunning)))

	job, err := c.CreateNodeOnboardingPlan(context.Background(), 4, "")

	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingJobStatusPlanned, job.Status)
	require.Empty(t, job.Moves)
	require.Len(t, job.Plan.BlockedReasons, 1)
	require.Equal(t, "running_job_exists", job.Plan.BlockedReasons[0].Code)
}

func TestStartNodeOnboardingJobRejectsBlockedPlan(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	job := sampleClusterOnboardingJob("blocked", controllermeta.OnboardingJobStatusPlanned)
	job.Moves = nil
	job.Plan.Moves = nil
	job.Plan.BlockedReasons = []controllermeta.NodeOnboardingBlockedReason{{
		Code:  "no_safe_candidate",
		Scope: "cluster",
	}}
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), job))

	_, err := c.StartNodeOnboardingJob(context.Background(), job.JobID)

	require.ErrorIs(t, err, ErrOnboardingPlanNotExecutable)
}

func TestStartNodeOnboardingJobMapsRaftRaceToRunningJobExists(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)
	job, err := c.CreateNodeOnboardingPlan(context.Background(), 4, "")
	require.NoError(t, err)
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), sampleClusterOnboardingJob("other", controllermeta.OnboardingJobStatusRunning)))

	_, err = c.StartNodeOnboardingJob(context.Background(), job.JobID)

	require.ErrorIs(t, err, ErrOnboardingRunningJobExists)
}

func TestListNodeOnboardingCandidatesRecommendsUnderloadedActiveDataNode(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)

	candidates, err := c.ListNodeOnboardingCandidates(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, candidates)
	candidate := requireOnboardingCandidate(t, candidates, 4)
	require.True(t, candidate.Recommended)
	require.Zero(t, candidate.SlotCount)
}

func TestListNodeOnboardingJobsSortsByCreatedAtDescAndCursor(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	base := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"job-a", "job-b", "job-c"} {
		job := sampleClusterOnboardingJob(id, controllermeta.OnboardingJobStatusPlanned)
		job.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		job.UpdatedAt = job.CreatedAt
		require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), job))
	}

	first, cursor, hasMore, err := c.ListNodeOnboardingJobs(context.Background(), 2, "")
	require.NoError(t, err)
	require.True(t, hasMore)
	require.NotEmpty(t, cursor)
	require.Equal(t, []string{"job-c", "job-b"}, clusterOnboardingJobIDs(first))

	second, _, hasMore, err := c.ListNodeOnboardingJobs(context.Background(), 2, cursor)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, []string{"job-a"}, clusterOnboardingJobIDs(second))
}

func TestRetryNodeOnboardingJobCreatesNewPlanFromFailedTarget(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)
	failed := sampleClusterOnboardingJob("failed", controllermeta.OnboardingJobStatusFailed)
	failed.CompletedAt = failed.CreatedAt.Add(time.Minute)
	failed.ResultCounts = controllermeta.OnboardingResultCounts{Failed: 1}
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), failed))

	retry, err := c.RetryNodeOnboardingJob(context.Background(), failed.JobID)

	require.NoError(t, err)
	require.Equal(t, failed.JobID, retry.RetryOfJobID)
	require.Equal(t, failed.TargetNodeID, retry.TargetNodeID)
	require.Equal(t, controllermeta.OnboardingJobStatusPlanned, retry.Status)
	require.NotEqual(t, failed.JobID, retry.JobID)
}

func TestRetryNodeOnboardingJobRejectsNonFailedJobs(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	job := sampleClusterOnboardingJob("planned", controllermeta.OnboardingJobStatusPlanned)
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), job))

	_, err := c.RetryNodeOnboardingJob(context.Background(), job.JobID)

	require.ErrorIs(t, err, ErrOnboardingInvalidJobState)
}

func TestControllerTickStartsOneOnboardingMoveAndSkipsOrdinaryRebalance(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)
	job := sampleClusterOnboardingJob("running", controllermeta.OnboardingJobStatusRunning)
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), job))

	c.controllerTickOnce(context.Background())

	got, err := c.controllerMeta.GetOnboardingJob(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingMoveStatusRunning, got.Moves[0].Status)
	task, err := c.controllerMeta.GetTask(context.Background(), got.Moves[0].SlotID)
	require.NoError(t, err)
	require.Equal(t, controllermeta.TaskKindRebalance, task.Kind)
	require.Equal(t, got.Moves[0].TargetNodeID, task.TargetNode)
}

func TestControllerTickCompletesOnboardingJob(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)
	job := sampleClusterOnboardingJob("running", controllermeta.OnboardingJobStatusRunning)
	job.Moves[0].Status = controllermeta.OnboardingMoveStatusCompleted
	job.CurrentMoveIndex = -1
	job.ResultCounts = controllermeta.OnboardingResultCounts{Completed: 1}
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), job))

	c.controllerTickOnce(context.Background())

	got, err := c.controllerMeta.GetOnboardingJob(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingJobStatusCompleted, got.Status)
}

func newOnboardingControllerLeaderCluster(t *testing.T) *Cluster {
	t.Helper()
	c, _, _ := newTestLocalControllerCluster(t, true)
	waitForTestControllerLeader(t, c.controllerHost, c.cfg.NodeID)
	return c
}

func seedOnboardingPlannerState(t *testing.T, c *Cluster) {
	t.Helper()
	ctx := context.Background()
	nodes := []controllermeta.ClusterNode{
		clusterOnboardingNode(1),
		clusterOnboardingNode(2),
		clusterOnboardingNode(3),
		clusterOnboardingNode(4),
	}
	for _, node := range nodes {
		require.NoError(t, c.controllerMeta.UpsertNode(ctx, node))
	}
	for slotID := uint32(1); slotID <= 3; slotID++ {
		require.NoError(t, c.controllerMeta.UpsertAssignment(ctx, controllermeta.SlotAssignment{
			SlotID:         slotID,
			DesiredPeers:   []uint64{1, 2, 3},
			ConfigEpoch:    1,
			BalanceVersion: uint64(slotID),
		}))
	}
	if c.controllerHost != nil {
		for nodeID := uint64(1); nodeID <= 4; nodeID++ {
			c.controllerHost.applyRuntimeReport(runtimeObservationReport{
				NodeID:     nodeID,
				ObservedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
				FullSync:   true,
				Views: []controllermeta.SlotRuntimeView{
					clusterOnboardingRuntime(1, 1),
					clusterOnboardingRuntime(2, 2),
					clusterOnboardingRuntime(3, 3),
				},
			})
		}
	}
}

func clusterOnboardingNode(nodeID uint64) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:          nodeID,
		Addr:            "127.0.0.1:7000",
		Role:            controllermeta.NodeRoleData,
		JoinState:       controllermeta.NodeJoinStateActive,
		Status:          controllermeta.NodeStatusAlive,
		JoinedAt:        time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		LastHeartbeatAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		CapacityWeight:  1,
	}
}

func clusterOnboardingRuntime(slotID uint32, leaderID uint64) controllermeta.SlotRuntimeView {
	return controllermeta.SlotRuntimeView{
		SlotID:              slotID,
		CurrentPeers:        []uint64{1, 2, 3},
		LeaderID:            leaderID,
		HealthyVoters:       3,
		HasQuorum:           true,
		ObservedConfigEpoch: 1,
		LastReportAt:        time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
}

func sampleClusterOnboardingJob(jobID string, status controllermeta.OnboardingJobStatus) controllermeta.NodeOnboardingJob {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	move := controllermeta.NodeOnboardingPlanMove{
		SlotID:             1,
		SourceNodeID:       1,
		TargetNodeID:       4,
		Reason:             "replica_balance",
		DesiredPeersBefore: []uint64{1, 2, 3},
		DesiredPeersAfter:  []uint64{2, 3, 4},
		CurrentLeaderID:    1,
	}
	job := controllermeta.NodeOnboardingJob{
		JobID:        jobID,
		TargetNodeID: 4,
		Status:       status,
		CreatedAt:    now,
		UpdatedAt:    now,
		PlanVersion:  1,
		Plan: controllermeta.NodeOnboardingPlan{
			TargetNodeID: 4,
			Summary:      controllermeta.NodeOnboardingPlanSummary{PlannedTargetSlotCount: 1},
			Moves:        []controllermeta.NodeOnboardingPlanMove{move},
		},
		Moves: []controllermeta.NodeOnboardingMove{{
			SlotID:                 1,
			SourceNodeID:           1,
			TargetNodeID:           4,
			Status:                 controllermeta.OnboardingMoveStatusPending,
			TaskKind:               controllermeta.TaskKindRebalance,
			TaskSlotID:             1,
			DesiredPeersBefore:     []uint64{1, 2, 3},
			DesiredPeersAfter:      []uint64{2, 3, 4},
			LeaderTransferRequired: move.LeaderTransferRequired,
		}},
		CurrentMoveIndex: -1,
		ResultCounts:     controllermeta.OnboardingResultCounts{Pending: 1},
	}
	input := slotcontrollerFingerprintInputForCluster(job)
	job.PlanFingerprint = slotcontrollerFingerprintForCluster(input)
	if status == controllermeta.OnboardingJobStatusRunning {
		job.StartedAt = now.Add(time.Minute)
	}
	return job
}

func slotcontrollerFingerprintInputForCluster(job controllermeta.NodeOnboardingJob) slotcontroller.OnboardingPlanFingerprintInput {
	return slotcontroller.OnboardingPlanFingerprintInput{
		TargetNode: clusterOnboardingNode(job.TargetNodeID),
		Plan:       job.Plan,
		Assignments: map[uint32]controllermeta.SlotAssignment{
			1: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, BalanceVersion: 1},
		},
		Runtime: map[uint32]controllermeta.SlotRuntimeView{
			1: clusterOnboardingRuntime(1, 1),
		},
		Tasks:          map[uint32]controllermeta.ReconcileTask{},
		MigratingSlots: map[uint32]struct{}{},
	}
}

func slotcontrollerFingerprintForCluster(input slotcontroller.OnboardingPlanFingerprintInput) string {
	return slotcontroller.OnboardingPlanFingerprint(input)
}

func requireOnboardingCandidate(t *testing.T, candidates []NodeOnboardingCandidate, nodeID uint64) NodeOnboardingCandidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.NodeID == nodeID {
			return candidate
		}
	}
	require.Failf(t, "candidate not found", "nodeID=%d candidates=%+v", nodeID, candidates)
	return NodeOnboardingCandidate{}
}

func clusterOnboardingJobIDs(jobs []controllermeta.NodeOnboardingJob) []string {
	out := make([]string, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, job.JobID)
	}
	return out
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		require.ErrorIs(t, err, target)
	}
}
