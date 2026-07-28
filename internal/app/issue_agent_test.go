package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestIntakeKeepsAuthorizationLabelUntilExplicitRevision(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"needs-info", "ready-for-agent"},
		reconcileIntakeLabels(
			[]string{"needs-triage", "ready-for-agent"}, "needs-info",
		),
	)
}

func TestIssueAgentOperationsComposeWithoutServerLifecycle(t *testing.T) {
	t.Parallel()

	operations := NewIssueAgentOperations(IssueAgentDependencies{})
	require.NotNil(t, operations.PlanEvent)
	require.NotNil(t, operations.PlanSweep)

	result, err := operations.PlanEvent(context.Background(), issueagentcli.PlanEventRequest{
		Now:         time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutIntake,
		ChainStatus: issueagentusecase.ChainMissing,
	})
	require.NoError(t, err)
	plan, ok := result.(issueagentusecase.Plan)
	require.True(t, ok)
	require.Equal(t, issueagentusecase.OperationIntakeIssue, plan.Operation)
}

func TestAutomatedRemediationHonorsCurrentRolloutPolicy(t *testing.T) {
	t.Parallel()

	policy := issueagentusecase.Policy{
		Enabled:                   true,
		RolloutMode:               issueagentusecase.RolloutRemediation,
		RemediationIssueAllowlist: []int64{41, 42},
	}
	require.True(t, policyAllowsAutomatedRemediation(policy, 42))
	require.False(t, policyAllowsAutomatedRemediation(policy, 43))
	policy.RolloutMode = issueagentusecase.RolloutIntake
	require.False(t, policyAllowsAutomatedRemediation(policy, 42))
	policy.RolloutMode = issueagentusecase.RolloutGeneral
	require.True(t, policyAllowsAutomatedRemediation(policy, 43))
	policy.Enabled = false
	require.False(t, policyAllowsAutomatedRemediation(policy, 42))
}

func TestPerIssueWorkerBudgetFencesAttemptsAndReservedTime(t *testing.T) {
	t.Parallel()

	policy := issueagentusecase.Policy{
		IssueBudget: issueagentusecase.IssueBudget{
			MaxWorkerTime: 6 * time.Hour,
		},
	}
	checkpoint := issueagentcontract.Checkpoint{}
	require.NoError(t, ensureIssueWorkerBudget(
		checkpoint, policy, 95*time.Minute, 1, 3,
	))
	require.Error(t, ensureIssueWorkerBudget(
		checkpoint, policy, 95*time.Minute, 3, 3,
	))
	checkpoint.Budget.WorkerSeconds = uint64((5 * time.Hour).Seconds())
	require.Error(t, ensureIssueWorkerBudget(
		checkpoint, policy, 95*time.Minute, 1, 3,
	))
}
