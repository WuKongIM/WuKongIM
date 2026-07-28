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
	require.True(t, issueagentusecase.AllowsAutomatedRemediation(policy, 42))
	require.False(t, issueagentusecase.AllowsAutomatedRemediation(policy, 43))
	policy.RolloutMode = issueagentusecase.RolloutIntake
	require.False(t, issueagentusecase.AllowsAutomatedRemediation(policy, 42))
	policy.RolloutMode = issueagentusecase.RolloutGeneral
	require.True(t, issueagentusecase.AllowsAutomatedRemediation(policy, 43))
	policy.Enabled = false
	require.False(t, issueagentusecase.AllowsAutomatedRemediation(policy, 42))
}

func TestPerIssueWorkerBudgetFencesAttemptsAndReservedTime(t *testing.T) {
	t.Parallel()

	policy := issueagentusecase.Policy{
		IssueBudget: issueagentusecase.IssueBudget{
			MaxRemediationAttempts: 3,
			MaxWorkerTime:          6 * time.Hour,
		},
	}
	checkpoint := issueagentcontract.Checkpoint{}
	checkpoint.Budget.RemediationAttempts = 1
	require.NoError(t, issueagentusecase.CheckIssueWorkerBudget(
		checkpoint, policy, issueagentcontract.PhaseFix,
	))
	checkpoint.Budget.RemediationAttempts = 3
	require.Error(t, issueagentusecase.CheckIssueWorkerBudget(
		checkpoint, policy, issueagentcontract.PhaseFix,
	))
	checkpoint.Budget.RemediationAttempts = 1
	checkpoint.Budget.WorkerSeconds = uint64((5 * time.Hour).Seconds())
	require.Error(t, issueagentusecase.CheckIssueWorkerBudget(
		checkpoint, policy, issueagentcontract.PhaseFix,
	))
}

func TestMovingMainCommandIdentityMatchesProtectedWorkflow(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		"WK_E2E_BINARY=current-main timeout --signal=TERM --kill-after=30s "+
			"50m go test -tags=e2e ./test/e2e/issue_agent/issue_42 "+
			"-count=3 -timeout=45m -p=1",
		movingMainCommandIdentity(42),
	)
}
