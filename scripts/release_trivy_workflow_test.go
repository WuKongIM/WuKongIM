package scripts_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// The scanner bootstrap must retain reviewed bytes and fail before publishing a PATH.
func TestReleaseTrivyWorkflowUsesVerifiedOfficialDownload(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct{ Steps []struct{ Name, Run string } }
	}
	require.NoError(t, yaml.Unmarshal(readWorkflow(t, "binary-release-publish.yml"), &workflow))
	install, build := -1, -1
	for i, step := range workflow.Jobs["docker"].Steps {
		if strings.Contains(step.Run, "scripts/release/install-trivy.sh") {
			install = i
		}
		if step.Name == "Build amd64 security scan candidate" {
			build = i
		}
	}
	require.GreaterOrEqual(t, install, 0)
	require.Greater(t, build, install, "scanner bootstrap must fail before expensive candidate builds")
	raw, err := os.ReadFile("release/install-trivy.sh")
	require.NoError(t, err)
	script := string(raw)
	require.NotContains(t, script, "get.trivy.dev")
	for _, expected := range []string{"https://github.com/aquasecurity/trivy/releases/download/", "0.74.0", "2ae6fe3ee734b7fdf11335663e18c75ea12dccc76062f09f164a3b0f8be4371a", "bc701c3c3ee8b9acbea2c23257e41381e3854888f51281616a6ba5dc96963821", "--proto '=https'", "--proto-redir '=https'", "--connect-timeout", "--max-time", "sha256sum --check --strict"} {
		require.Contains(t, script, expected)
	}
	verify, extract, publish := strings.Index(script, "sha256sum --check --strict"), strings.Index(script, "tar -xzf"), strings.Index(script, "$GITHUB_PATH")
	require.Greater(t, extract, verify)
	require.Greater(t, publish, extract)
}
