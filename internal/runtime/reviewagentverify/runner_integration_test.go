//go:build integration

package reviewagentverify_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestOSExecutorRunsWithCredentialFreeEnvironment(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	executor, err := verify.NewOSExecutor(verify.OSExecutorConfig{
		HomeDir: home,
		Path:    os.Getenv("PATH"),
	})
	require.NoError(t, err)
	result, err := executor.Execute(
		context.Background(),
		verify.ProcessRequest{
			Arguments: []string{
				"/bin/sh", "-c",
				`printf '%s|%s|%s' "$HOME" "${GITHUB_TOKEN:-}" "${AWS_SECRET_ACCESS_KEY:-}"`,
			},
			WorkingDir:     t.TempDir(),
			Timeout:        5 * time.Second,
			MaxOutputBytes: 4096,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, home+"||", string(result.Stdout))
}

func TestOSExecutorTerminatesTimeoutAndOutputAbuse(t *testing.T) {
	t.Parallel()

	executor, err := verify.NewOSExecutor(verify.OSExecutorConfig{
		HomeDir: t.TempDir(),
		Path:    os.Getenv("PATH"),
	})
	require.NoError(t, err)
	_, err = executor.Execute(
		context.Background(),
		verify.ProcessRequest{
			Arguments:  []string{"/bin/sh", "-c", "sleep 5"},
			WorkingDir: t.TempDir(),
			Timeout:    100 * time.Millisecond, MaxOutputBytes: 4096,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "deadline")

	result, err := executor.Execute(
		context.Background(),
		verify.ProcessRequest{
			Arguments: []string{
				"/bin/sh", "-c",
				"i=0; while [ $i -lt 5000 ]; do printf x; i=$((i+1)); done",
			},
			WorkingDir: t.TempDir(),
			Timeout:    5 * time.Second, MaxOutputBytes: 100,
		},
	)
	require.Error(t, err)
	require.LessOrEqual(t, len(result.Stdout), 100)
}

func TestOSExecutorRejectsCallerEnvironmentAndUnsafeHome(t *testing.T) {
	t.Parallel()

	_, err := verify.NewOSExecutor(verify.OSExecutorConfig{
		HomeDir: filepath.Clean("/"),
		Path:    os.Getenv("PATH"),
	})
	require.Error(t, err)

	executor, err := verify.NewOSExecutor(verify.OSExecutorConfig{
		HomeDir: t.TempDir(),
		Path:    os.Getenv("PATH"),
	})
	require.NoError(t, err)
	_, err = executor.Execute(
		context.Background(),
		verify.ProcessRequest{
			Arguments:   []string{"/usr/bin/env"},
			WorkingDir:  t.TempDir(),
			Environment: []string{"GITHUB_TOKEN=secret"},
			Timeout:     time.Second, MaxOutputBytes: 4096,
		},
	)
	require.EqualError(t, err, "caller environment override is forbidden")

	for _, value := range executor.Environment() {
		require.False(t, strings.HasPrefix(value, "GITHUB_"))
		require.False(t, strings.HasPrefix(value, "AWS_"))
	}
}
