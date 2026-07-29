package issueagentworker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

func TestDockerSandboxBoundsOutputDuringCapture(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	docker := filepath.Join(root, "docker")
	script := `#!/bin/sh
set -eu
case "$1 $2" in
  "volume create")
    for last do :; done
    printf '%s\n' "$last"
    ;;
  "volume rm")
    ;;
  *)
    case "$*" in
      *wk-issue-agent-sync-*) exit 0 ;;
    esac
    head -c 4096 /dev/zero | tr '\000' x
    head -c 4096 /dev/zero | tr '\000' y >&2
    ;;
esac
`
	require.NoError(t, os.WriteFile(docker, []byte(script), 0o700))
	workspace := filepath.Join(root, "workspace")
	moduleCache := filepath.Join(root, "modules")
	require.NoError(t, os.Mkdir(workspace, 0o700))
	require.NoError(t, os.Mkdir(moduleCache, 0o700))
	runner, err := issueagentworker.NewDockerSandboxRunner(
		issueagentworker.DockerSandboxConfig{
			DockerBinary: docker,
			Image:        "example.invalid/sandbox@sha256:" + strings.Repeat("a", 64),
			Workspace:    workspace, ModuleCache: moduleCache,
			CPUs: 1, MemoryBytes: 256 << 20, PIDs: 32, TempBytes: 64 << 20,
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	result, err := runner.Run(context.Background(), issueagentworker.ExecRequest{
		Executable: "go", Arguments: []string{"version"},
		WorkingDir: workspace, Timeout: time.Second, OutputLimit: 64,
	})
	require.NoError(t, err)
	require.Len(t, result.Stdout, 64)
	require.Len(t, result.Stderr, 64)
	require.True(t, result.StdoutTruncated)
	require.True(t, result.StderrTruncated)
}
