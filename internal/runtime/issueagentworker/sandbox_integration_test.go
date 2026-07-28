//go:build integration

package issueagentworker_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	"github.com/stretchr/testify/require"
)

func TestSandboxHasNoNetworkHostControlOrSupervisorSecrets(t *testing.T) {
	image := os.Getenv("ISSUE_AGENT_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("ISSUE_AGENT_SANDBOX_IMAGE digest is not configured")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker is unavailable")
	}
	workspace := t.TempDir()
	runner, err := issueagentworker.NewDockerSandboxRunner(
		issueagentworker.DockerSandboxConfig{
			Image: image, Workspace: workspace, CPUs: 1,
			MemoryBytes: 256 << 20, PIDs: 64, TempBytes: 64 << 20,
			ModuleCache: t.TempDir(),
		},
	)
	require.NoError(t, err)
	result, err := runner.Run(context.Background(), issueagentworker.ExecRequest{
		Executable: "sh",
		Arguments: []string{"-c", `
set -eu
env
test ! -S /var/run/docker.sock
test ! -e /host
test "$(cat /proc/1/comm)" != "systemd"
if command -v wget >/dev/null 2>&1; then
  ! wget -T 2 -qO- http://169.254.169.254/
fi
`},
		WorkingDir: workspace, Timeout: 20 * time.Second,
		Environment: []string{"PATH=/usr/local/go/bin:/usr/bin:/bin"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode, string(result.Stderr))
	require.NotContains(t, strings.ToUpper(string(result.Stdout)), "TOKEN")
	require.NotContains(t, strings.ToUpper(string(result.Stdout)), "API_KEY")
}
