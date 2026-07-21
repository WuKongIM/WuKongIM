package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCloudSimulationSSHRetryEventuallySucceeds(t *testing.T) {
	retryScript := cloudSimulationSSHRetryScript(t)
	temporary := t.TempDir()
	counterPath := filepath.Join(temporary, "attempts")
	commandPath := filepath.Join(temporary, "flaky-command")
	writeSSHRetryExecutable(t, commandPath, `#!/usr/bin/env bash
set -euo pipefail
attempt=0
if [[ -f "$COUNTER_PATH" ]]; then attempt="$(<"$COUNTER_PATH")"; fi
attempt=$((attempt + 1))
printf '%s\n' "$attempt" >"$COUNTER_PATH"
if ((attempt < 3)); then exit 255; fi
`)

	command := exec.CommandContext(t.Context(), "bash", "-c",
		`source "$1"; shift; cloud_ssh_retry node-1-ready 3 0 "$@"`, "--", retryScript, commandPath, "do-not-log")
	command.Env = append(os.Environ(), "COUNTER_PATH="+counterPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("retry command: %v\n%s", err, output)
	}
	if got := readTrimmed(t, counterPath); got != "3" {
		t.Fatalf("attempt count = %s, want 3", got)
	}
	for _, fragment := range []string{
		"cloud-ssh stage=node-1-ready attempt=1/3",
		"cloud-ssh stage=node-1-ready attempt=3/3 result=success",
	} {
		if !strings.Contains(string(output), fragment) {
			t.Fatalf("retry output missing %q:\n%s", fragment, output)
		}
	}
	if strings.Contains(string(output), "do-not-log") {
		t.Fatalf("retry output leaked command arguments:\n%s", output)
	}
}

func TestCloudSimulationSSHRetryReturnsLastStatusAfterBoundedAttempts(t *testing.T) {
	retryScript := cloudSimulationSSHRetryScript(t)
	temporary := t.TempDir()
	counterPath := filepath.Join(temporary, "attempts")
	commandPath := filepath.Join(temporary, "failed-command")
	writeSSHRetryExecutable(t, commandPath, `#!/usr/bin/env bash
set -euo pipefail
attempt=0
if [[ -f "$COUNTER_PATH" ]]; then attempt="$(<"$COUNTER_PATH")"; fi
printf '%s\n' "$((attempt + 1))" >"$COUNTER_PATH"
exit 255
`)

	command := exec.CommandContext(t.Context(), "bash", "-c",
		`source "$1"; shift; cloud_ssh_retry node-2-upload 2 0 "$@"`, "--", retryScript, commandPath)
	command.Env = append(os.Environ(), "COUNTER_PATH="+counterPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("retry command unexpectedly succeeded:\n%s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 255 {
		t.Fatalf("retry exit = %v, want status 255\n%s", err, output)
	}
	if got := readTrimmed(t, counterPath); got != "2" {
		t.Fatalf("attempt count = %s, want 2", got)
	}
	if !strings.Contains(string(output), "cloud-ssh stage=node-2-upload attempt=2/2 result=failed status=255 retryable=true") {
		t.Fatalf("retry output missing terminal evidence:\n%s", output)
	}
}

func TestCloudSimulationSSHRetryDoesNotReplayRemoteCommandFailures(t *testing.T) {
	retryScript := cloudSimulationSSHRetryScript(t)
	temporary := t.TempDir()
	counterPath := filepath.Join(temporary, "attempts")
	commandPath := filepath.Join(temporary, "remote-command-error")
	writeSSHRetryExecutable(t, commandPath, `#!/usr/bin/env bash
set -euo pipefail
attempt=0
if [[ -f "$COUNTER_PATH" ]]; then attempt="$(<"$COUNTER_PATH")"; fi
printf '%s\n' "$((attempt + 1))" >"$COUNTER_PATH"
exit 42
`)

	command := exec.CommandContext(t.Context(), "bash", "-c",
		`source "$1"; shift; cloud_ssh_retry node-3-unpack 3 0 "$@"`, "--", retryScript, commandPath)
	command.Env = append(os.Environ(), "COUNTER_PATH="+counterPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("retry command unexpectedly succeeded:\n%s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 42 {
		t.Fatalf("retry exit = %v, want status 42\n%s", err, output)
	}
	if got := readTrimmed(t, counterPath); got != "1" {
		t.Fatalf("attempt count = %s, want 1", got)
	}
	if !strings.Contains(string(output), "result=failed status=42 retryable=false") {
		t.Fatalf("retry output missing non-retryable evidence:\n%s", output)
	}
}

func TestCloudSimulationSSHRetryCaptureDiscardsFailedAttemptOutput(t *testing.T) {
	retryScript := cloudSimulationSSHRetryScript(t)
	temporary := t.TempDir()
	counterPath := filepath.Join(temporary, "attempts")
	commandPath := filepath.Join(temporary, "flaky-capture")
	writeSSHRetryExecutable(t, commandPath, `#!/usr/bin/env bash
set -euo pipefail
attempt=0
if [[ -f "$COUNTER_PATH" ]]; then attempt="$(<"$COUNTER_PATH")"; fi
attempt=$((attempt + 1))
printf '%s\n' "$attempt" >"$COUNTER_PATH"
if ((attempt == 1)); then
  printf 'stale-device\n'
  exit 255
fi
printf 'fresh-device\n'
`)

	command := exec.CommandContext(t.Context(), "bash", "-c",
		`source "$1"; shift; cloud_ssh_retry_capture node-1-data-device 2 0 "$@"`, "--", retryScript, commandPath)
	command.Env = append(os.Environ(), "COUNTER_PATH="+counterPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("retry capture: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "stale-device") {
		t.Fatalf("retry capture retained failed-attempt output:\n%s", output)
	}
	if !strings.Contains(string(output), "fresh-device") {
		t.Fatalf("retry capture omitted successful output:\n%s", output)
	}
}

func TestCloudSimulationSSHRetryCaptureFailsWhenSuccessfulOutputCannotBeRead(t *testing.T) {
	retryScript := cloudSimulationSSHRetryScript(t)
	command := exec.CommandContext(t.Context(), "bash", "-c",
		`hash -p /usr/bin/false cat; source "$1"; shift; cloud_ssh_retry_capture capture-output 1 0 "$@"`,
		"--", retryScript, "printf", "fresh-value")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("retry capture unexpectedly succeeded:\n%s", output)
	}
	if strings.Contains(string(output), "result=success") {
		t.Fatalf("retry capture reported success after output failure:\n%s", output)
	}
	if !strings.Contains(string(output), "reason=capture-output") {
		t.Fatalf("retry capture omitted output failure evidence:\n%s", output)
	}
}

func TestCloudSimulationSSHRetryStopsBeforeExpiredGlobalDeadline(t *testing.T) {
	retryScript := cloudSimulationSSHRetryScript(t)
	temporary := t.TempDir()
	counterPath := filepath.Join(temporary, "attempts")
	commandPath := filepath.Join(temporary, "must-not-run")
	writeSSHRetryExecutable(t, commandPath, `#!/usr/bin/env bash
set -euo pipefail
printf 'called\n' >"$COUNTER_PATH"
`)

	command := exec.CommandContext(t.Context(), "bash", "-c",
		`source "$1"; shift; cloud_ssh_retry expired-stage 3 0 "$@"`, "--", retryScript, commandPath)
	command.Env = append(os.Environ(),
		"COUNTER_PATH="+counterPath,
		"WK_CLOUD_SSH_DEADLINE_EPOCH=1",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expired retry unexpectedly succeeded:\n%s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 124 {
		t.Fatalf("expired retry exit = %v, want 124\n%s", err, output)
	}
	if _, statErr := os.Stat(counterPath); !os.IsNotExist(statErr) {
		t.Fatalf("expired retry invoked command: stat error=%v", statErr)
	}
	if !strings.Contains(string(output), "status=124 retryable=false reason=deadline-exceeded") {
		t.Fatalf("expired retry omitted deadline evidence:\n%s", output)
	}
}

func cloudSimulationSSHRetryScript(t *testing.T) string {
	t.Helper()
	_, current, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filepath.Dir(current)), "scripts", "cloud-sim", "ssh-retry.sh")
}

func writeSSHRetryExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(content))
}
