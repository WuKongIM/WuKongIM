//go:build integration

package issueagentverify_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentverify"
	"github.com/stretchr/testify/require"
)

func TestProcessRunnerExecutesArgvWithoutShell(t *testing.T) {
	root := t.TempDir()
	runner, err := issueagentverify.NewProcessRunner(
		root,
		t.TempDir(),
		1<<20,
	)
	require.NoError(t, err)

	result, err := runner.Run(context.Background(),
		issueagentverify.VerificationCommandPlan{
			Arguments:      []string{"go", "version"},
			WorkingDir:     ".",
			TimeoutSeconds: 30,
		})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, string(result.Stdout), "go version")
}
