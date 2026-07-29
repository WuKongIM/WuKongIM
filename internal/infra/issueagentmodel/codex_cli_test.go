package issueagentmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexCLIRunnerUsesProxyWithoutCredentialOrPersistentHome(t *testing.T) {
	t.Parallel()

	capture := t.TempDir()
	binary := writeFakeCodexCLI(t, capture, "0.145.0", false)
	bootstrap := writeCodexBootstrapHome(
		t, validCodexActionProxyConfig, 0o644,
	)
	runner, err := NewCodexCLIRunner(CodexCLIConfig{
		Binary: binary, BootstrapHome: bootstrap,
		MinVersion: "0.145.0", TempRoot: t.TempDir(),
	})
	require.NoError(t, err)

	response, err := runner.RunRound(context.Background(), CodexRoundRequest{
		Model: "gpt-5.6-sol", Prompt: "strict prompt", MaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(25), response.InputTokens)
	require.Equal(t, uint64(7), response.OutputTokens)
	require.JSONEq(t,
		`{"schema_version":1,"kind":"final","tool_calls":[],"result":null}`,
		string(response.Envelope),
	)

	args := readCodexCapture(t, capture, "args")
	require.Contains(t, args, "--ephemeral\n")
	require.Contains(t, args, "--ignore-user-config\n")
	require.Contains(t, args, "--ignore-rules\n")
	require.Contains(t, args, "--strict-config\n")
	require.Contains(t, args, "--sandbox\nread-only\n")
	require.Contains(t, args, "approval_policy=\"never\"\n")
	require.Contains(t, args,
		"model_provider=\"codex-action-responses-proxy\"\n")
	require.Contains(t, args,
		"model_providers.codex-action-responses-proxy.name="+
			"\"Codex Action Responses Proxy\"\n")
	require.Contains(t, args,
		"model_providers.codex-action-responses-proxy.base_url="+
			"\"http://127.0.0.1:43123/v1\"\n")
	require.Contains(t, args,
		"model_providers.codex-action-responses-proxy.wire_api="+
			"\"responses\"\n")
	for _, disabled := range []string{
		"shell_tool", "unified_exec", "apps",
		"browser_use", "computer_use", "image_generation",
	} {
		require.Contains(t, args, disabled+"\n")
	}
	environment := readCodexCapture(t, capture, "env")
	require.NotContains(t, environment, "CODEX_API_KEY")
	require.NotContains(t, environment, "DEEPSEEK_API_KEY")
	require.NotContains(t, environment, "GITHUB_TOKEN")
	roundHome := codexCapturedValue(t, environment, "CODEX_HOME")
	require.Equal(t, roundHome, codexCapturedValue(t, environment, "HOME"))
	require.NoDirExists(t, roundHome)
	require.Contains(t, args, filepath.Join(roundHome, "empty-workspace")+"\n")
	require.Equal(t, "strict prompt", readCodexCapture(t, capture, "stdin"))
}

func TestCodexCLIRunnerUsesDistinctTemporaryHomeForEveryRound(t *testing.T) {
	t.Parallel()

	capture := t.TempDir()
	runner, err := NewCodexCLIRunner(CodexCLIConfig{
		Binary: writeFakeCodexCLI(t, capture, "0.145.0", false),
		BootstrapHome: writeCodexBootstrapHome(
			t, validCodexActionProxyConfig, 0o644,
		),
		MinVersion: "0.145.0",
		TempRoot:   t.TempDir(),
	})
	require.NoError(t, err)
	request := CodexRoundRequest{
		Model: "gpt-5.6-sol", Prompt: "strict prompt", MaxBytes: 1 << 20,
	}
	_, err = runner.RunRound(context.Background(), request)
	require.NoError(t, err)
	first := codexCapturedValue(
		t, readCodexCapture(t, capture, "env"), "CODEX_HOME",
	)
	_, err = runner.RunRound(context.Background(), request)
	require.NoError(t, err)
	second := codexCapturedValue(
		t, readCodexCapture(t, capture, "env"), "CODEX_HOME",
	)
	require.NotEqual(t, first, second)
	require.NoDirExists(t, first)
	require.NoDirExists(t, second)
}

func TestCodexCLIRunnerRejectsOldBinary(t *testing.T) {
	t.Parallel()

	_, err := NewCodexCLIRunner(CodexCLIConfig{
		Binary: writeFakeCodexCLI(t, t.TempDir(), "0.144.0", false),
		BootstrapHome: writeCodexBootstrapHome(
			t, validCodexActionProxyConfig, 0o644,
		),
		MinVersion: "0.145.0",
	})
	require.EqualError(t, err, "Codex CLI version is unavailable or too old")
}

func TestCodexCLIRunnerDoesNotLeakProcessFailure(t *testing.T) {
	t.Parallel()

	runner, err := NewCodexCLIRunner(CodexCLIConfig{
		Binary: writeFakeCodexCLI(t, t.TempDir(), "0.145.0", true),
		BootstrapHome: writeCodexBootstrapHome(
			t, validCodexActionProxyConfig, 0o644,
		),
		MinVersion: "0.145.0",
	})
	require.NoError(t, err)
	_, err = runner.RunRound(context.Background(), CodexRoundRequest{
		Model: "gpt-5.6-sol", Prompt: "strict prompt", MaxBytes: 1 << 20,
	})
	require.EqualError(t, err, "Codex CLI process failed")
	require.NotContains(t, err.Error(), "strict prompt")
	require.NotContains(t, err.Error(), "43123")
}

func writeFakeCodexCLI(
	t *testing.T,
	capture string,
	version string,
	failExec bool,
) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "codex")
	exitCode := 0
	if failExec {
		exitCode = 7
	}
	script := fmt.Sprintf(`#!/bin/sh
set -eu
capture=%s
if [ "${1:-}" = "--version" ]; then
  printf 'codex-cli %s\n'
  exit 0
fi
: >"$capture/args"
for argument in "$@"; do
  printf '%%s\n' "$argument" >>"$capture/args"
done
env | sort >"$capture/env"
cat >"$capture/stdin"
if [ %d -ne 0 ]; then
  printf 'provider rejected sensitive-value\n' >&2
  exit %d
fi
output=
previous=
for argument in "$@"; do
  if [ "$previous" = "--output-last-message" ]; then
    output=$argument
  fi
  previous=$argument
done
test -n "$output"
printf '%%s' '{"schema_version":1,"kind":"final","tool_calls":[],"result":null}' >"$output"
printf '%%s\n' '{"type":"turn.completed","usage":{"input_tokens":25,"output_tokens":7}}'
`, shellQuote(capture), version, exitCode, exitCode)
	require.NoError(t, os.WriteFile(binary, []byte(script), 0o700))
	return binary
}

func readCodexCapture(t *testing.T, directory string, name string) string {
	t.Helper()
	value, err := os.ReadFile(filepath.Join(directory, name))
	require.NoError(t, err)
	return string(value)
}

func codexCapturedValue(t *testing.T, environment string, name string) string {
	t.Helper()
	prefix := name + "="
	for _, line := range strings.Split(environment, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("%s is absent from captured environment", name)
	return ""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
