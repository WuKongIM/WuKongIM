package issueagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestDecodePolicyLocksApprovedSafetyDefaults(t *testing.T) {
	t.Parallel()

	file, err := os.Open(filepath.Join(
		"..", "..", "..", ".github", "issue-agent", "policy.json",
	))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })

	policy, err := issueagentusecase.DecodePolicy(file, 64<<10)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.RolloutReproduction, policy.RolloutMode)
	require.True(t, policy.Enabled)
	require.Contains(t, policy.SandboxImage, "@sha256:")
	require.Equal(t, 3, policy.IssueBudget.MaxReproductionAttempts)
	require.Equal(t, 3, policy.IssueBudget.MaxRemediationAttempts)
	require.Equal(t, 2, policy.IssueBudget.MaxCIRepairAttempts)
	require.Equal(t, 3, policy.IssueBudget.MaxInfrastructureRetries)
	require.Equal(t, 6*time.Hour, policy.IssueBudget.MaxWorkerTime)
	require.Equal(t, 3, policy.RepositoryBudget.MaxActiveWorkers)
	require.Equal(t, 1, policy.RepositoryBudget.MaxHeavyWorkers)
	require.Equal(t, 24*time.Hour, policy.RepositoryBudget.RollingWindow)
	require.Equal(t, 24*time.Hour, policy.RepositoryBudget.MaxStartedWorkerTime)
	require.Contains(t, policy.ProtectedPaths, ".github/issue-agent")
	require.Contains(t, policy.ProtectedPaths, "internal/usecase/issueagent")
	require.Equal(t, issueagent.ProviderCodex, policy.DefaultProvider)
	require.Len(t, policy.Providers, 2)
}

func TestDecodePolicyRejectsUnknownAndUnsafeExpansion(t *testing.T) {
	t.Parallel()

	unknown := `{"schema_version":1,"enabled":false,"unexpected":true}`
	_, err := issueagentusecase.DecodePolicy(
		strings.NewReader(unknown), int64(len(unknown)),
	)
	require.Error(t, err)

	policy := issueagentusecase.Policy{
		SchemaVersion:   1,
		Enabled:         true,
		RolloutMode:     issueagentusecase.RolloutGeneral,
		DefaultProvider: issueagent.ProviderCodex,
		IssueBudget: issueagentusecase.IssueBudget{
			MaxReproductionAttempts:  3,
			MaxRemediationAttempts:   3,
			MaxCIRepairAttempts:      2,
			MaxInfrastructureRetries: 3,
			MaxWorkerTime:            6 * time.Hour,
		},
		RepositoryBudget: issueagentusecase.RepositoryBudget{
			MaxActiveWorkers:     4,
			MaxHeavyWorkers:      2,
			RollingWindow:        24 * time.Hour,
			MaxStartedWorkerTime: 48 * time.Hour,
		},
		ProtectedPaths: []string{"README.md"},
		Providers: []issueagentusecase.ProviderPolicy{{
			Provider:      issueagent.ProviderCodex,
			ModelVariable: "MODEL",
		}},
	}
	require.Error(t, issueagentusecase.ValidatePolicy(policy))
}

func TestWorkerRolloutReservationAndIssueBudgetAreUsecaseOwned(t *testing.T) {
	t.Parallel()

	policy := issueagentusecase.Policy{
		Enabled: true, RolloutMode: issueagentusecase.RolloutRemediation,
		RemediationIssueAllowlist: []int64{42},
		IssueBudget: issueagentusecase.IssueBudget{
			MaxReproductionAttempts: 3,
			MaxRemediationAttempts:  3,
			MaxWorkerTime:           6 * time.Hour,
		},
	}
	require.True(t, issueagentusecase.AllowsReproduction(policy))
	require.True(t, issueagentusecase.AllowsAutomatedRemediation(policy, 42))
	require.False(t, issueagentusecase.AllowsAutomatedRemediation(policy, 43))
	reservation, err := issueagentusecase.WorkerReservationForPhase(
		issueagent.PhaseDiagnose,
	)
	require.NoError(t, err)
	require.Equal(t, 65*time.Minute, reservation.Duration)
	require.False(t, reservation.Heavy)

	checkpoint := issueagent.Checkpoint{}
	require.NoError(t, issueagentusecase.CheckIssueWorkerBudget(
		checkpoint, policy, issueagent.PhaseFix,
	))
	checkpoint.Budget.RemediationAttempts = 3
	require.Error(t, issueagentusecase.CheckIssueWorkerBudget(
		checkpoint, policy, issueagent.PhaseFix,
	))
}
