package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestComposeModelRunnerKeepsProviderInputsSeparated(t *testing.T) {
	t.Run("DeepSeek does not inspect Codex bootstrap", func(t *testing.T) {
		_, err := composeModelRunner(IssueAgentWorkerConfig{
			DeepSeekAPIKey: "deepseek-test-key",
			HTTPClient:     &http.Client{Timeout: time.Second},
		}, issueagentcontract.TaskEnvelope{
			Provider: issueagentcontract.ProviderDeepSeek,
		})
		require.NoError(t, err)
	})

	t.Run("Codex does not require DeepSeek key", func(t *testing.T) {
		binary, bootstrap := writeAppTestCodexBootstrap(t)
		_, err := composeModelRunner(IssueAgentWorkerConfig{
			CodexBinary:         binary,
			CodexBootstrapHome:  bootstrap,
			CodexMinimumVersion: "0.145.0",
		}, issueagentcontract.TaskEnvelope{
			Provider: issueagentcontract.ProviderCodex,
		})
		require.NoError(t, err)
	})
}

func TestIssueAgentWorkerRejectsPublisherCredentialsBeforePayload(t *testing.T) {
	t.Parallel()

	run := NewIssueAgentWorkerDependency(IssueAgentWorkerConfig{
		ForbiddenPublisherData: true,
	})
	_, err := run(context.Background(), issueagentcli.DocumentRequest{})
	require.EqualError(t, err, "Worker process contains Publisher credentials")
}

func writeAppTestCodexBootstrap(t *testing.T) (string, string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "codex")
	require.NoError(t, os.WriteFile(
		binary,
		[]byte("#!/bin/sh\nprintf 'codex-cli 0.145.0\\n'\n"),
		0o700,
	))
	bootstrap := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(bootstrap, "config.toml"),
		[]byte(`
model_provider = "codex-action-responses-proxy"

[model_providers.codex-action-responses-proxy]
name = "Codex Action Responses Proxy"
base_url = "http://127.0.0.1:43123/v1"
wire_api = "responses"
`),
		0o644,
	))
	return binary, bootstrap
}
