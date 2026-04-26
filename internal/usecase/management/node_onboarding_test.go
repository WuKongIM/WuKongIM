package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestManagementCreatesNodeOnboardingPlan(t *testing.T) {
	fake := &fakeNodeOnboardingCluster{plannedJob: sampleManagementOnboardingJob()}
	app := New(Options{Cluster: fake})

	got, err := app.CreateNodeOnboardingPlan(context.Background(), CreateNodeOnboardingPlanRequest{TargetNodeID: 4})

	require.NoError(t, err)
	require.Equal(t, "planned", got.Job.Status)
	require.Equal(t, 1, got.Job.ResultCounts.Pending)
	require.True(t, fake.createPlanCalled)
}

func TestManagementListsNodeOnboardingCandidates(t *testing.T) {
	fake := &fakeNodeOnboardingCluster{candidates: []raftcluster.NodeOnboardingCandidate{{
		NodeID: 4, Status: controllermeta.NodeStatusAlive, JoinState: controllermeta.NodeJoinStateActive, Role: controllermeta.NodeRoleData, Recommended: true,
	}}}
	app := New(Options{Cluster: fake})

	got, err := app.ListNodeOnboardingCandidates(context.Background())

	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Equal(t, "alive", got.Items[0].Status)
	require.True(t, got.Items[0].Recommended)
}

type fakeNodeOnboardingCluster struct {
	fakeClusterReader
	candidates       []raftcluster.NodeOnboardingCandidate
	plannedJob       controllermeta.NodeOnboardingJob
	createPlanCalled bool
}

func (f *fakeNodeOnboardingCluster) ListNodeOnboardingCandidates(context.Context) ([]raftcluster.NodeOnboardingCandidate, error) {
	return f.candidates, nil
}

func (f *fakeNodeOnboardingCluster) CreateNodeOnboardingPlan(_ context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error) {
	f.createPlanCalled = true
	job := f.plannedJob
	job.TargetNodeID = targetNodeID
	job.RetryOfJobID = retryOfJobID
	return job, nil
}

func (f *fakeNodeOnboardingCluster) StartNodeOnboardingJob(context.Context, string) (controllermeta.NodeOnboardingJob, error) {
	return f.plannedJob, nil
}

func (f *fakeNodeOnboardingCluster) ListNodeOnboardingJobs(context.Context, int, string) ([]controllermeta.NodeOnboardingJob, string, bool, error) {
	return []controllermeta.NodeOnboardingJob{f.plannedJob}, "", false, nil
}

func (f *fakeNodeOnboardingCluster) GetNodeOnboardingJob(context.Context, string) (controllermeta.NodeOnboardingJob, error) {
	return f.plannedJob, nil
}

func (f *fakeNodeOnboardingCluster) RetryNodeOnboardingJob(context.Context, string) (controllermeta.NodeOnboardingJob, error) {
	return f.plannedJob, nil
}

func sampleManagementOnboardingJob() controllermeta.NodeOnboardingJob {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	return controllermeta.NodeOnboardingJob{
		JobID:        "onboard-20260426-000001",
		TargetNodeID: 4,
		Status:       controllermeta.OnboardingJobStatusPlanned,
		CreatedAt:    now,
		UpdatedAt:    now,
		PlanVersion:  1,
		Plan: controllermeta.NodeOnboardingPlan{
			TargetNodeID: 4,
			Moves: []controllermeta.NodeOnboardingPlanMove{{
				SlotID: 2, SourceNodeID: 1, TargetNodeID: 4, DesiredPeersBefore: []uint64{1, 2, 3}, DesiredPeersAfter: []uint64{2, 3, 4},
			}},
		},
		Moves: []controllermeta.NodeOnboardingMove{{
			SlotID: 2, SourceNodeID: 1, TargetNodeID: 4, Status: controllermeta.OnboardingMoveStatusPending,
		}},
		CurrentMoveIndex: -1,
		ResultCounts:     controllermeta.OnboardingResultCounts{Pending: 1},
	}
}
