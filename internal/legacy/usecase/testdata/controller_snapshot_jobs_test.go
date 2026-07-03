package testdata

import (
	"context"
	"fmt"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestGenerateControllerSnapshotJobsCreatesPayloadHeavyPlans(t *testing.T) {
	cluster := &recordingControllerSnapshotJobCluster{}
	app := New(AppOptions{ControllerSnapshotJobs: cluster})

	result, err := app.GenerateControllerSnapshotJobs(context.Background(), GenerateControllerSnapshotJobsCommand{
		Prefix:       "snap-job",
		TargetNodeID: 2,
		Count:        2,
		PayloadBytes: 32,
		Seed:         "seed-a",
	})

	require.NoError(t, err)
	require.Equal(t, GenerateControllerSnapshotJobsResult{
		Dataset:      "cluster/controller-snapshot-jobs",
		Prefix:       "snap-job",
		TargetNodeID: 2,
		Count:        2,
		PayloadBytes: 32,
		FirstJobID:   "job-000001",
		LastJobID:    "job-000002",
	}, result)
	require.Equal(t, []uint64{2, 2}, cluster.targetNodeIDs)
	require.Len(t, cluster.retryOfJobIDs, 2)
	require.Len(t, cluster.retryOfJobIDs[0], 32)
	require.Contains(t, cluster.retryOfJobIDs[0], "seed-a")
	require.NotEqual(t, cluster.retryOfJobIDs[0], cluster.retryOfJobIDs[1])
	require.True(t, cluster.deadlineSeen, "controller writes should receive a deadline")
}

func TestGenerateControllerSnapshotJobsRejectsInvalidInput(t *testing.T) {
	app := New(AppOptions{ControllerSnapshotJobs: &recordingControllerSnapshotJobCluster{}})

	for _, cmd := range []GenerateControllerSnapshotJobsCommand{
		{Prefix: "", TargetNodeID: 1, Count: 1, PayloadBytes: 1},
		{Prefix: "snap", TargetNodeID: 0, Count: 1, PayloadBytes: 1},
		{Prefix: "snap", TargetNodeID: 1, Count: 0, PayloadBytes: 1},
		{Prefix: "snap", TargetNodeID: 1, Count: 1, PayloadBytes: 0},
	} {
		_, err := app.GenerateControllerSnapshotJobs(context.Background(), cmd)
		require.Error(t, err)
	}
}

type recordingControllerSnapshotJobCluster struct {
	targetNodeIDs []uint64
	retryOfJobIDs []string
	deadlineSeen  bool
}

func (r *recordingControllerSnapshotJobCluster) CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) > 0 {
		r.deadlineSeen = true
	}
	r.targetNodeIDs = append(r.targetNodeIDs, targetNodeID)
	r.retryOfJobIDs = append(r.retryOfJobIDs, retryOfJobID)
	return controllermeta.NodeOnboardingJob{
		JobID:        fmt.Sprintf("job-%06d", len(r.retryOfJobIDs)),
		TargetNodeID: targetNodeID,
		Status:       controllermeta.OnboardingJobStatusPlanned,
		CreatedAt:    time.Unix(int64(len(r.retryOfJobIDs)), 0),
		UpdatedAt:    time.Unix(int64(len(r.retryOfJobIDs)), 0),
		PlanVersion:  1,
		Plan: controllermeta.NodeOnboardingPlan{
			TargetNodeID: targetNodeID,
		},
		CurrentMoveIndex: -1,
	}, nil
}
