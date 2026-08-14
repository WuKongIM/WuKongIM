//go:build integration

package scripts_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalStorageOverlapCaptureHardDeadlineLeavesNoChild(t *testing.T) {
	for _, commandName := range []string{"find", "stat"} {
		commandName := commandName
		t.Run(commandName, func(t *testing.T) {
			t.Parallel()

			root := repoRoot(t)
			dir := t.TempDir()
			fakeBin := filepath.Join(dir, "bin")
			if err := os.Mkdir(fakeBin, 0o700); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(dir, commandName+".pid")
			writeSlowOverlapCommand(t, filepath.Join(fakeBin, commandName))
			t.Cleanup(func() { killRecordedOverlapProcess(pidFile) })

			metrics := filepath.Join(dir, "node.prom")
			writeTimelineTestFileForScripts(t, metrics, "wukongim_storage_pebble_compaction_count 1\nwukongim_storage_pebble_compactions_in_progress 0\n")
			snapshotRoot := filepath.Join(dir, "data", "slotraft-snapshots")
			if err := os.MkdirAll(snapshotRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if commandName == "stat" {
				if err := os.WriteFile(filepath.Join(snapshotRoot, "chunk-000000"), []byte("abc"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			inventoryDir := filepath.Join(dir, "snapshot-inventory")
			if err := os.Mkdir(inventoryDir, 0o700); err != nil {
				t.Fatal(err)
			}
			inventory := filepath.Join(inventoryDir, "sample-1-node-1.tsv")

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			arguments := []string{
				filepath.Join(root, "scripts", "chat-lifecycle", "capture-local-storage-overlap.sh"),
				"--metrics", metrics, "--snapshot-root", snapshotRoot, "--inventory", inventory,
				"--observed-at", "2026-08-13T10:00:00.123Z", "--run-id", "test-run",
				"--sample", "sample-1", "--node", "node-1",
			}
			cmd := exec.CommandContext(ctx, "bash", arguments...)
			cmd.Env = overlapTestEnvironment(fakeBin, pidFile)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			started := time.Now()
			err := cmd.Run()
			elapsed := time.Since(started)
			if err != nil {
				t.Fatalf("capture with slow %s: %v after %s; stdout=%q stderr=%q", commandName, err, elapsed, stdout.String(), stderr.String())
			}
			if elapsed >= 7*time.Second {
				t.Fatalf("capture with slow %s exceeded hard bound: %s", commandName, elapsed)
			}
			want := "2026-08-13T10:00:00.123Z\ttest-run\tsample-1\tnode-1\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable"
			if got := strings.TrimSpace(stdout.String()); got != want {
				t.Fatalf("capture with slow %s row = %q, want %q; stderr=%q", commandName, got, want, stderr.String())
			}
			if _, err := os.Stat(inventory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("capture with slow %s inventory should not exist: %v", commandName, err)
			}
			assertRecordedOverlapProcessGone(t, pidFile)
		})
	}
}

func TestLocalStorageOverlapCaptureSignalTrapLeavesNoChild(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "find.pid")
	writeSlowOverlapCommand(t, filepath.Join(fakeBin, "find"))
	t.Cleanup(func() { killRecordedOverlapProcess(pidFile) })

	metrics := filepath.Join(dir, "node.prom")
	writeTimelineTestFileForScripts(t, metrics, "wukongim_storage_pebble_compaction_count 1\nwukongim_storage_pebble_compactions_in_progress 0\n")
	snapshotRoot := filepath.Join(dir, "data", "slotraft-snapshots")
	if err := os.MkdirAll(snapshotRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	inventoryDir := filepath.Join(dir, "snapshot-inventory")
	if err := os.Mkdir(inventoryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	arguments := []string{
		filepath.Join(root, "scripts", "chat-lifecycle", "capture-local-storage-overlap.sh"),
		"--metrics", metrics, "--snapshot-root", snapshotRoot,
		"--inventory", filepath.Join(inventoryDir, "sample-1-node-1.tsv"),
		"--observed-at", "2026-08-13T10:00:00.123Z", "--run-id", "test-run",
		"--sample", "sample-1", "--node", "node-1",
	}
	cmd := exec.Command("bash", arguments...)
	cmd.Env = overlapTestEnvironment(fakeBin, pidFile)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForOverlapPIDFile(t, pidFile, 2*time.Second)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal capture helper: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatalf("signal capture helper unexpectedly succeeded; stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatalf("signal capture helper did not terminate; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	assertRecordedOverlapProcessGone(t, pidFile)
}

func writeSlowOverlapCommand(t *testing.T, path string) {
	t.Helper()
	body := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$$" >"${WK_OVERLAP_FAKE_PID_FILE:?}"
trap '' TERM
exec /bin/sleep 30
`
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func overlapTestEnvironment(fakeBin, pidFile string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "PATH=") || strings.HasPrefix(value, "WK_OVERLAP_FAKE_PID_FILE=") {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_OVERLAP_FAKE_PID_FILE="+pidFile,
	)
}

func assertRecordedOverlapProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	pid := readRecordedOverlapPID(t, pidFile)
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("bounded child pid %d still exists: %v", pid, err)
	}
}

func killRecordedOverlapProcess(pidFile string) {
	body, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err == nil && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func readRecordedOverlapPID(t *testing.T, pidFile string) int {
	t.Helper()
	body, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read fake child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse fake child pid %q: %v", body, err)
	}
	return pid
}

func waitForOverlapPIDFile(t *testing.T, pidFile string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(body)) != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fake child pid file %s was not written within %s", pidFile, timeout)
}
