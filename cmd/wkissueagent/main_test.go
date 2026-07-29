package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunRejectsMissingCommandWithoutStartingWuKongIM(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	require.Equal(t, 2, exitCode)
	require.Empty(t, stdout.String())
}

func TestIssueAgentWorkerConfigUsesCodexBootstrapWithoutAPIKey(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "must-not-be-read")
	t.Setenv(
		"ISSUE_AGENT_CODEX_BOOTSTRAP_HOME",
		"/runner/temp/issue-agent-codex-bootstrap",
	)

	config := issueAgentWorkerConfigFromEnv()
	require.Equal(
		t,
		"/runner/temp/issue-agent-codex-bootstrap",
		config.CodexBootstrapHome,
	)
}
