package reviewagent_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestSchedulerEnforcesFIFOAndLeaseLimits(t *testing.T) {
	t.Parallel()

	scheduler := testScheduler()
	limits := testPolicy().Scheduler
	now := time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC)
	generations := []contract.GenerationIdentity{
		generationForPR(1),
		generationForPR(2),
		generationForPR(3),
		generationForPR(4),
	}
	var err error
	for index, generation := range generations {
		scheduler, err = reviewagent.Enqueue(
			scheduler,
			reviewagent.QueueEntry{
				Generation:        generation,
				FirstTimeExternal: index < 2,
				EnqueuedAt:        now.Add(time.Duration(index) * time.Minute),
			},
			limits,
		)
		require.NoError(t, err)
	}

	var acquired *reviewagent.Lease
	scheduler, acquired, err = reviewagent.AcquireNext(
		scheduler, 501, now.Add(5*time.Minute), limits,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), acquired.Generation.PullRequest)

	scheduler, acquired, err = reviewagent.AcquireNext(
		scheduler, 502, now.Add(6*time.Minute), limits,
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), acquired.Generation.PullRequest)

	scheduler, acquired, err = reviewagent.AcquireNext(
		scheduler, 503, now.Add(7*time.Minute), limits,
	)
	require.NoError(t, err)
	require.Equal(t, int64(4), acquired.Generation.PullRequest)

	_, acquired, err = reviewagent.AcquireNext(
		scheduler, 504, now.Add(8*time.Minute), limits,
	)
	require.NoError(t, err)
	require.Nil(t, acquired)
	require.Len(t, scheduler.Queue, 1)
	require.Equal(t, int64(2), scheduler.Queue[0].Generation.PullRequest)
}

func TestSchedulerReleaseIsFencedAndIdempotent(t *testing.T) {
	t.Parallel()

	generation := generationForPR(7)
	scheduler := testSchedulerWithLease(generation, 601, false)
	now := time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC)

	_, err := reviewagent.ReleaseLease(
		scheduler, generation, 999, now,
	)
	require.EqualError(t, err, "scheduler lease run does not match")

	changed := generation
	changed.HeadSHA = "9999999999999999999999999999999999999999"
	_, err = reviewagent.ReleaseLease(scheduler, changed, 601, now)
	require.EqualError(t, err, "scheduler lease generation does not match")

	released, err := reviewagent.ReleaseLease(
		scheduler, generation, 601, now,
	)
	require.NoError(t, err)
	require.Empty(t, released.Active)

	again, err := reviewagent.ReleaseLease(
		released, generation, 601, now,
	)
	require.NoError(t, err)
	require.Equal(t, released, again)
}

func testScheduler() reviewagent.SchedulerState {
	return reviewagent.SchedulerState{
		SchemaVersion: 1,
		Sequence:      1,
		UpdatedAt: time.Date(
			2026, 7, 30, 1, 0, 0, 0, time.UTC,
		),
	}
}

func testSchedulerWithLease(
	generation contract.GenerationIdentity,
	runID int64,
	firstTimeExternal bool,
) reviewagent.SchedulerState {
	scheduler := testScheduler()
	scheduler.Active = []reviewagent.Lease{{
		Generation:        generation,
		RunID:             runID,
		FirstTimeExternal: firstTimeExternal,
		AcquiredAt:        scheduler.UpdatedAt,
	}}
	return scheduler
}

func generationForPR(number int64) contract.GenerationIdentity {
	generation := testReviewingState().Generation
	generation.PullRequest = number
	return generation
}
