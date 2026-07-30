package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewAgentMainRejectsUnknownCommandWithoutEchoingInput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"merge"},
		strings.NewReader(`{"private_key":"do-not-echo"}`),
		&stdout,
		&stderr,
	)
	require.Equal(t, 1, code)
	require.Empty(t, stdout.String())
	require.Equal(t, "review agent command failed\n", stderr.String())
	require.NotContains(t, stderr.String(), "do-not-echo")
}

func TestReviewAgentConfigUsesTrustedPolicyOverride(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("REVIEW_POLICY_PATH", policyPath)

	config := reviewAgentConfig([]string{"verify-baseline"})

	require.Equal(t, policyPath, config.PolicyPath)
}
