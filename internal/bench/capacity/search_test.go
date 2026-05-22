package capacity

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSearchFindsHighestStableQPS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.StartQPS = 100
	cfg.MaxQPS = 1000
	cfg.StepFactor = 2
	cfg.BinarySearch = true
	cfg.BinarySearchMinDeltaRatio = 0.10

	runner := attemptRunnerFunc(func(ctx context.Context, attempt Attempt) (AttemptResult, error) {
		passed := attempt.OfferedQPS <= 400
		return AttemptResult{
			Attempt:    attempt,
			Passed:     passed,
			ActualQPS:  attempt.OfferedQPS,
			SendackP99: 50 * time.Millisecond,
		}, nil
	})

	got, err := Search(context.Background(), cfg, runner)

	require.NoError(t, err)
	require.InDelta(t, 400, got.MaxStableQPS, 50)
	require.NotNil(t, got.StableAttempt)
	require.NotNil(t, got.FailedAttempt)
	require.NotEmpty(t, got.Attempts)
}

func TestSearchReportsNoStableAttempt(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.StartQPS = 100
	cfg.MaxQPS = 100

	runner := attemptRunnerFunc(func(ctx context.Context, attempt Attempt) (AttemptResult, error) {
		return AttemptResult{Attempt: attempt, Passed: false, FailureReason: "sendack_p99_exceeded"}, nil
	})

	got, err := Search(context.Background(), cfg, runner)

	require.NoError(t, err)
	require.Equal(t, StatusFailed, got.Status)
	require.Nil(t, got.StableAttempt)
	require.NotNil(t, got.FailedAttempt)
}

func TestSearchPreservesFailedAttemptReports(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.StartQPS = 100
	cfg.MaxQPS = 200
	cfg.StepFactor = 2
	cfg.BinarySearch = false

	runner := attemptRunnerFunc(func(ctx context.Context, attempt Attempt) (AttemptResult, error) {
		return AttemptResult{
			Attempt:   attempt,
			Passed:    attempt.OfferedQPS == 100,
			ActualQPS: attempt.OfferedQPS,
			ReportDir: attemptReportDir("./reports", attempt),
		}, nil
	})

	got, err := Search(context.Background(), cfg, runner)

	require.NoError(t, err)
	require.Len(t, got.Attempts, 2)
	require.Equal(t, "reports/attempts/000100-qps", got.Attempts[0].ReportDir)
	require.Equal(t, "reports/attempts/000200-qps", got.Attempts[1].ReportDir)
}
