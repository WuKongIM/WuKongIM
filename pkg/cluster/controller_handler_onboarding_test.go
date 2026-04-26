package cluster

import (
	"context"
	"testing"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestControllerHandlerCreateOnboardingPlanRequiresLeader(t *testing.T) {
	c, _, _ := newTestLocalControllerCluster(t, false)
	handler := &controllerHandler{cluster: c}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCCreateOnboardingPlan,
		OnboardingPlan: &nodeOnboardingPlanRequest{
			TargetNodeID: 4,
		},
	})
	require.NoError(t, err)

	respBody, err := handler.Handle(context.Background(), body)
	require.NoError(t, err)

	resp, err := decodeControllerResponse(controllerRPCCreateOnboardingPlan, respBody)
	require.NoError(t, err)
	require.True(t, resp.NotLeader)
}

func TestControllerHandlerCreateOnboardingPlanOnLeader(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	seedOnboardingPlannerState(t, c)
	handler := &controllerHandler{cluster: c}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCCreateOnboardingPlan,
		OnboardingPlan: &nodeOnboardingPlanRequest{
			TargetNodeID: 4,
		},
	})
	require.NoError(t, err)

	respBody, err := handler.Handle(context.Background(), body)
	require.NoError(t, err)

	resp, err := decodeControllerResponse(controllerRPCCreateOnboardingPlan, respBody)
	require.NoError(t, err)
	require.NotNil(t, resp.OnboardingJob)
	require.Equal(t, controllermeta.OnboardingJobStatusPlanned, resp.OnboardingJob.Status)
	require.NotEmpty(t, resp.OnboardingJob.Moves)
}

func TestControllerHandlerStartOnboardingMapsConflictCode(t *testing.T) {
	c := newOnboardingControllerLeaderCluster(t)
	blocked := sampleClusterOnboardingJob("blocked", controllermeta.OnboardingJobStatusPlanned)
	blocked.Moves = nil
	blocked.Plan.Moves = nil
	blocked.Plan.BlockedReasons = []controllermeta.NodeOnboardingBlockedReason{{Code: "no_safe_candidate", Scope: "cluster"}}
	require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), blocked))
	handler := &controllerHandler{cluster: c}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCStartOnboardingJob,
		OnboardingJob: &nodeOnboardingJobRequest{
			JobID: blocked.JobID,
		},
	})
	require.NoError(t, err)

	respBody, err := handler.Handle(context.Background(), body)
	require.NoError(t, err)

	resp, err := decodeControllerResponse(controllerRPCStartOnboardingJob, respBody)
	require.NoError(t, err)
	require.Equal(t, onboardingErrorPlanNotExecutable, resp.OnboardingErrorCode)
}
