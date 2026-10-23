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
			if got, want := stderr.String(), "storage_overlap_capture reason=deadline node=node-1 phase=periodic\n"; got != want {
				t.Errorf("slow %s diagnostic = %q, want %q", commandName, got, want)
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
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 143 {
			t.Fatalf("signal capture helper exit = %v, want 143; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatalf("signal capture helper did not terminate; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	assertRecordedOverlapProcessGone(t, pidFile)
}

func TestLocalStorageOverlapCaptureClosedFailureDiagnostics(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		body          string
		sample        string
		node          string
		phase         string
		reason        string
		exitCode      int
		invalidMetric bool
		fileLimit     bool
		stderrFails   bool
	}{
		{name: "complete", sample: "before", node: "node-2"},
		{name: "invalid_metrics_precedes_scan", command: "find", body: "exit 124", invalidMetric: true},
		{name: "failed_find", command: "find", body: "printf 'private-path-and-error\\n' >&2; exit 47", sample: "warmup-before", phase: "warmup_before", reason: "find"},
		{name: "find_exit124_is_not_own_deadline", command: "find", body: "exit 124", sample: "warmup_before", phase: "warmup_before", reason: "find"},
		{name: "find_stderr", command: "find", body: "printf 'private-path-and-error\\n' >&2", sample: "before", node: "node-2", phase: "before", reason: "find"},
		{name: "find_empty_entry", command: "find", body: "printf '\\n'", reason: "find"},
		{name: "failed_stat", command: "stat", body: "printf 'private-path-and-error\\n' >&2; exit 47", sample: "after", node: "node-3", phase: "after", reason: "stat"},
		{name: "stat_exit124_is_not_own_deadline", command: "stat", body: "exit 124", reason: "stat"},
		{name: "stat_stderr", command: "stat", body: "printf '3\\n'; printf 'private-path-and-error\\n' >&2", reason: "stat"},
		{name: "stat_invalid_size", command: "stat", body: "printf 'invalid-size\\n'", reason: "stat"},
		{name: "file_disappeared_after_find", command: "find", body: "printf '%s/chunk-000000\\n' \"$1\"; rm \"$1/chunk-000000\"", reason: "stat"},
		{name: "file_limit4097", fileLimit: true, reason: "entrylimit"},
		// Inject filesystem measurement results at the OS command boundary. These
		// exercise both byte limits without thousands of timed stat processes.
		{name: "raw_byte_limit", command: "wc", body: "printf '1048577\\n'", reason: "entrylimit"},
		{name: "inventory_byte_limit", command: "stat", body: "printf '%0524280d\\n' 0", reason: "entrylimit"},
		{name: "failed_sort", command: "sort", body: "exit 47", reason: "output"},
		{name: "failed_mktemp", command: "mktemp", body: "exit 47", reason: "output", exitCode: 73},
		{name: "failed_mv_preserves73", command: "mv", body: "exit 47", reason: "output", exitCode: 73},
		{name: "failed_hash_preserves_status", command: "sha256sum", body: "exit 47", reason: "output", exitCode: 47},
		{name: "invalid_hash_preserves73", command: "sha256sum", body: "printf 'invalid-hash\\n'", reason: "output", exitCode: 73},
		{name: "diagnostic_write_failure_preserves_missing", command: "find", body: "exit 47", reason: "find", stderrFails: true},
		{name: "diagnostic_write_failure_preserves73", command: "mv", body: "exit 47", reason: "output", exitCode: 73, stderrFails: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			snapshots := filepath.Join(dir, "data", "slotraft-snapshots")
			inventoryDir := filepath.Join(dir, "snapshot-inventory")
			for _, path := range []string{bin, snapshots, inventoryDir} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			metrics := filepath.Join(dir, "node.prom")
			metric := "wukongim_storage_pebble_compaction_count 1\nwukongim_storage_pebble_compactions_in_progress 0\n"
			if test.invalidMetric {
				metric = "wukongim_storage_pebble_compaction_count 1.5\nwukongim_storage_pebble_compactions_in_progress 0\n"
			}
			writeTimelineTestFileForScripts(t, metrics, metric)
			files := 1
			if test.fileLimit {
				files = 4097
			}
			for index := 0; index < files; index++ {
				name := "chunk-000000"
				if index != 0 {
					name = "chunk-" + strconv.Itoa(index)
				}
				if err := os.WriteFile(filepath.Join(snapshots, name), []byte("abc"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.command != "" {
				body := "#!/usr/bin/env bash\nset -euo pipefail\n" + test.body + "\n"
				if err := os.WriteFile(filepath.Join(bin, test.command), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			sample, node, phase := test.sample, test.node, test.phase
			if sample == "" {
				sample = "private-sample-id-123"
			}
			if node == "" {
				node = "node-1"
			}
			if phase == "" {
				phase = "periodic"
			}
			inventory := filepath.Join(inventoryDir, sample+"-"+node+".tsv")
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "capture-local-storage-overlap.sh"),
				"--metrics", metrics, "--snapshot-root", snapshots, "--inventory", inventory,
				"--observed-at", "2026-08-13T10:00:00.123Z", "--run-id", "test-run", "--sample", sample, "--node", node)
			cmd.Env = overlapTestEnvironment(bin, filepath.Join(dir, "unused.pid"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if test.stderrFails {
				readOnly, err := os.Open(metrics)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = readOnly.Close() })
				cmd.Stderr = readOnly
			}
			err := cmd.Run()
			gotExit := 0
			if err != nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatalf("capture execution: %v", err)
				}
				gotExit = exitError.ExitCode()
			}
			if gotExit != test.exitCode {
				t.Errorf("exit = %d, want %d; stdout=%q stderr=%q", gotExit, test.exitCode, stdout.String(), stderr.String())
			}
			if test.exitCode != 0 {
				if stdout.Len() != 0 {
					t.Errorf("failed output must not fabricate a row: %q", stdout.String())
				}
			} else if test.reason != "" || test.invalidMetric {
				want := "2026-08-13T10:00:00.123Z\ttest-run\t" + sample + "\t" + node + "\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n"
				if stdout.String() != want {
					t.Errorf("missing row = %q, want %q", stdout.String(), want)
				}
				if _, err := os.Stat(inventory); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("missing capture must not publish inventory: %v", err)
				}
			} else {
				columns := strings.Split(strings.TrimSpace(stdout.String()), "\t")
				if len(columns) != 11 || columns[4] != "complete" || columns[5] != "1" || columns[6] != "0" || columns[7] != "1" || columns[8] != "3" {
					t.Errorf("complete row = %q", stdout.String())
				}
				if body, err := os.ReadFile(inventory); err != nil || string(body) != "chunk-000000\t3\n" {
					t.Errorf("complete inventory = %q, %v", body, err)
				}
			}
			if !test.stderrFails {
				want := ""
				if test.reason != "" {
					want = "storage_overlap_capture reason=" + test.reason + " node=" + node + " phase=" + phase + "\n"
				}
				if stderr.String() != want {
					t.Errorf("closed diagnostic = %q, want %q", stderr.String(), want)
				}
			}
		})
	}
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
