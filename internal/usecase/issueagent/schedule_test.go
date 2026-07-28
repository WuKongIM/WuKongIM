package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestScheduleAppliesPriorityFIFOAndGlobalCapacity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	budget := issueagentusecase.RepositoryBudget{
		MaxActiveWorkers:     3,
		MaxHeavyWorkers:      1,
		RollingWindow:        24 * time.Hour,
		MaxStartedWorkerTime: 24 * time.Hour,
	}
	candidates := []issueagentusecase.Candidate{
		scheduleCandidate(40, now.Add(-3*time.Hour), false, false),
		scheduleCandidate(41, now.Add(-2*time.Hour), true, true),
		scheduleCandidate(42, now.Add(-time.Hour), true, false),
	}
	active := []issueagentusecase.ActiveLease{{
		IssueNumber: 39,
		Heavy:       false,
		ExpiresAt:   now.Add(time.Hour),
	}}

	plans, err := issueagentusecase.Schedule(now, candidates, active, nil, budget, 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, plans, 2)
	require.Equal(t, int64(41), plans[0].IssueNumber)
	require.Equal(t, int64(40), plans[1].IssueNumber)
	require.True(t, plans[0].Heavy)
	require.False(t, plans[1].Heavy)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, plans[0].OperationID)

	repeated, err := issueagentusecase.Schedule(now, candidates, active, nil, budget, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, plans, repeated)
}

func TestScheduleStopsAtRollingWorkerTimeBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	budget := issueagentusecase.RepositoryBudget{
		MaxActiveWorkers:     3,
		MaxHeavyWorkers:      1,
		RollingWindow:        24 * time.Hour,
		MaxStartedWorkerTime: 24 * time.Hour,
	}
	starts := []issueagentusecase.WorkerStart{{
		StartedAt: now.Add(-time.Hour),
		Reserved:  24 * time.Hour,
	}}

	plans, err := issueagentusecase.Schedule(
		now,
		[]issueagentusecase.Candidate{
			scheduleCandidate(42, now.Add(-time.Hour), false, false),
		},
		nil,
		starts,
		budget,
		5*time.Minute,
	)
	require.NoError(t, err)
	require.Empty(t, plans)
}

func TestScheduleRejectsDuplicateIssueCandidates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	candidate := scheduleCandidate(42, now.Add(-time.Hour), false, false)
	_, err := issueagentusecase.Schedule(
		now,
		[]issueagentusecase.Candidate{candidate, candidate},
		nil,
		nil,
		issueagentusecase.RepositoryBudget{
			MaxActiveWorkers:     3,
			MaxHeavyWorkers:      1,
			RollingWindow:        24 * time.Hour,
			MaxStartedWorkerTime: 24 * time.Hour,
		},
		5*time.Minute,
	)
	require.Error(t, err)
}

func scheduleCandidate(
	issueNumber int64,
	eligibleAt time.Time,
	heavy bool,
	priority bool,
) issueagentusecase.Candidate {
	return issueagentusecase.Candidate{
		Repository:   "WuKongIM/WuKongIM",
		IssueNumber:  issueNumber,
		Generation:   1,
		NextSequence: 2,
		Phase:        issueagent.PhaseReproduce,
		TaskDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		EligibleAt:   eligibleAt,
		Timeout:      20 * time.Minute,
		Reserved:     30 * time.Minute,
		Heavy:        heavy,
		PriorityHigh: priority,
	}
}
