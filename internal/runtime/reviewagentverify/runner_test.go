package reviewagentverify_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestRunnerResolvesOnlyProtectedNamedChecks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ledger, err := verify.NewFileLedger(
		filepath.Join(t.TempDir(), "ledger.jsonl"),
		root,
	)
	require.NoError(t, err)
	executor := &recordingExecutor{
		result: verify.ProcessResult{
			ExitCode: 0,
			Stdout:   []byte("ok\n"),
			Duration: 2 * time.Second,
		},
	}
	runner, err := verify.NewRunner(verify.RunnerConfig{
		WorkspaceRoot: root,
		Policy: verify.Policy{
			TrustedChecks: map[string]verify.CheckPlan{
				"go-unit": {
					Arguments:      []string{"go", "test", "./internal/..."},
					WorkingDir:     ".",
					TimeoutSeconds: 60,
					MaxOutputBytes: 1 << 20,
				},
			},
		},
		Executor: executor,
		Ledger:   ledger,
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
		},
	})
	require.NoError(t, err)

	evidence, err := runner.Run(
		context.Background(),
		testGeneration(),
		"go-unit",
	)
	require.NoError(t, err)
	require.Equal(t, contract.CheckOutcomePassed, evidence.Outcome)
	require.Equal(t, []string{"go", "test", "./internal/..."}, executor.request.Arguments)
	require.Equal(t, root, executor.request.WorkingDir)
	require.Empty(t, executor.request.Environment)

	_, err = runner.Run(
		context.Background(),
		testGeneration(),
		"go test ./...",
	)
	require.EqualError(t, err, "unknown trusted check")
	require.Equal(t, 1, executor.calls)

	records, err := ledger.List(testGeneration())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "go-unit", records[0].Evidence.Name)
}

func TestFileLedgerMustRemainOutsideModelWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := verify.NewFileLedger(
		filepath.Join(root, "ledger.jsonl"),
		root,
	)
	require.EqualError(t, err, "evidence ledger must be outside workspace")
}

type recordingExecutor struct {
	request verify.ProcessRequest
	result  verify.ProcessResult
	err     error
	calls   int
}

func (executor *recordingExecutor) Execute(
	_ context.Context,
	request verify.ProcessRequest,
) (verify.ProcessResult, error) {
	executor.calls++
	executor.request = request
	return executor.result, executor.err
}
