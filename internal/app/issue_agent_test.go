package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

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
	require.Equal(t, issueagentusecase.OperationWait, plan.Operation)
}
