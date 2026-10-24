//go:build integration

package scripts_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalStorageOverlapCaptureConcurrentSnapshotLifecycle(t *testing.T) {
	for _, operation := range []string{"publish", "gc"} {
		t.Run(operation, func(t *testing.T) {
			result := runSnapshotLifecycleCapture(t, operation)
			columns := strings.Split(strings.TrimSpace(result.stdout), "\t")
			if len(columns) != 11 || columns[4] != "complete" || columns[7] != "1" || columns[8] != "3" {
				t.Fatalf("capture after normal snapshot %s must retain a complete inventory: stdout=%q stderr=%q", operation, result.stdout, result.stderr)
			}
			if result.inventory != "slot-1/snap-final/chunk-000000\t3\n" || result.findCalls < 2 {
				t.Fatalf("capture must enumerate a fresh complete inventory: inventory=%q findCalls=%d", result.inventory, result.findCalls)
			}
			if result.stderr != "" {
				t.Fatalf("successful capture diagnostic = %q", result.stderr)
			}
		})
	}
}

func TestLocalStorageOverlapCaptureSnapshotChurnStaysBoundedAndFailClosed(t *testing.T) {
	for _, operation := range []string{"continuous", "permission", "false_enoent"} {
		t.Run(operation, func(t *testing.T) {
			result := runSnapshotLifecycleCapture(t, operation)
			columns := strings.Split(strings.TrimSpace(result.stdout), "\t")
			if len(columns) != 11 || columns[4] != "missing" || result.inventory != "" {
				t.Fatalf("unverified capture must remain missing: stdout=%q inventory=%q", result.stdout, result.inventory)
			}
			reason := "stat"
			if operation == "continuous" {
				reason = "deadline"
				if result.findCalls < 2 {
					t.Fatalf("continuous churn did not exercise rescans: %d", result.findCalls)
				}
			} else if result.findCalls != 1 {
				t.Fatalf("unproved disappearance must not rescan: %d", result.findCalls)
			}
			if result.stderr != "storage_overlap_capture reason="+reason+" node=node-1 phase=before\n" {
				t.Fatalf("failure diagnostic = %q", result.stderr)
			}
		})
	}
}

type snapshotLifecycleCaptureResult struct {
	stdout, stderr, inventory string
	findCalls                 int
}

// runSnapshotLifecycleCapture changes the real filesystem after find has
// enumerated it, matching snapshot publication or GC before the later stat.
func runSnapshotLifecycleCapture(t *testing.T, operation string) snapshotLifecycleCaptureResult {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	snapshots := filepath.Join(dir, "data", "slotraft-snapshots")
	inventoryDir := filepath.Join(dir, "snapshot-inventory")
	for _, path := range []string{bin, inventoryDir, filepath.Join(snapshots, "slot-1", ".tmp-snap-final")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTimelineTestFileForScripts(t, filepath.Join(snapshots, "slot-1", ".tmp-snap-final", "chunk-000000"), "abc")
	if operation == "gc" {
		if err := os.MkdirAll(filepath.Join(snapshots, "slot-1", "snap-final"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeTimelineTestFileForScripts(t, filepath.Join(snapshots, "slot-1", "snap-final", "chunk-000000"), "abc")
	}
	metrics := filepath.Join(dir, "node.prom")
	writeTimelineTestFileForScripts(t, metrics, "wukongim_storage_pebble_compaction_count 1\nwukongim_storage_pebble_compactions_in_progress 0\n")
	realFind, err := exec.LookPath("find")
	if err != nil {
		t.Fatal(err)
	}
	findWrapper := `#!/usr/bin/env bash
set -euo pipefail
printf 'call\n' >>"$WK_OVERLAP_FIND_CALLS"
"$WK_OVERLAP_REAL_FIND" "$@" >"$WK_OVERLAP_ENUMERATION"
if [[ "$WK_OVERLAP_MUTATION" == continuous || ! -e "$WK_OVERLAP_MUTATED" ]]; then
  case "$WK_OVERLAP_MUTATION" in
    publish) mv "$1/slot-1/.tmp-snap-final" "$1/slot-1/snap-final" ;;
    gc) rm -r "$1/slot-1/.tmp-snap-final" ;;
    continuous)
      if [[ -d "$1/slot-1/.tmp-snap-final" ]]; then
        mv "$1/slot-1/.tmp-snap-final" "$1/slot-1/snap-final"
      else
        mv "$1/slot-1/snap-final" "$1/slot-1/.tmp-snap-final"
      fi ;;
  esac
  : >"$WK_OVERLAP_MUTATED"
fi
cat "$WK_OVERLAP_ENUMERATION"
`
	if err := os.WriteFile(filepath.Join(bin, "find"), []byte(findWrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	if operation == "permission" || operation == "false_enoent" {
		message := "Permission denied"
		if operation == "false_enoent" {
			message = "No such file or directory"
		}
		body := "#!/usr/bin/env bash\nprintf 'stat: cannot stat: " + message + "\\n' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, "stat"), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	inventory := filepath.Join(inventoryDir, "before-node-1.tsv")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "capture-local-storage-overlap.sh"),
		"--metrics", metrics, "--snapshot-root", snapshots, "--inventory", inventory,
		"--observed-at", "2026-08-13T10:00:00.123Z", "--run-id", "test-run", "--sample", "before", "--node", "node-1")
	findCalls := filepath.Join(dir, "find.calls")
	cmd.Env = append(overlapTestEnvironment(bin, filepath.Join(dir, "unused.pid")),
		"WK_OVERLAP_REAL_FIND="+realFind, "WK_OVERLAP_FIND_CALLS="+findCalls,
		"WK_OVERLAP_ENUMERATION="+filepath.Join(dir, "enumeration"),
		"WK_OVERLAP_MUTATED="+filepath.Join(dir, "mutated"), "WK_OVERLAP_MUTATION="+operation)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("capture execution: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if elapsed := time.Since(started); elapsed >= 6*time.Second {
		t.Fatalf("capture exceeded its original three-second scan plus cleanup allowance: %s", elapsed)
	}
	body, err := os.ReadFile(inventory)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(findCalls)
	if err != nil {
		t.Fatal(err)
	}
	return snapshotLifecycleCaptureResult{stdout: stdout.String(), stderr: stderr.String(), inventory: string(body), findCalls: strings.Count(string(calls), "call\n")}
}
