//go:build integration

package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSingleNodeTerminalStorageCutWaitsForPeriodicWriterAndSealsLast(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	binDir := t.TempDir()
	helper := filepath.Join(binDir, "capture-storage")
	writeExecutable(t, helper, `#!/usr/bin/env bash
set -euo pipefail
sample="" inventory="" observed_at="" run_id="" node=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --sample) sample="$2"; shift 2 ;;
    --inventory) inventory="$2"; shift 2 ;;
    --observed-at) observed_at="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    --node) node="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' "$$" >>"$WK_TEST_HELPER_PIDS"
printf '%s\n' "$sample" >>"$WK_TEST_HELPER_CALLS"
if [[ "$sample" == periodic-000001 ]]; then
  : >"$WK_TEST_PERIODIC_STARTED"
  while [[ ! -f "$WK_TEST_RELEASE_PERIODIC" ]]; do sleep 0.02; done
fi
printf 'slot/chunk\t3\n' >"$inventory"
if command -v sha256sum >/dev/null 2>&1; then
  digest="$(sha256sum "$inventory" | awk '{print $1}')"
else
  digest="$(shasum -a 256 "$inventory" | awk '{print $1}')"
fi
printf '%s\t%s\t%s\t%s\tcomplete\t1\t0\t1\t3\t%s\tsnapshot-inventory/%s-%s.tsv\n' \
  "$observed_at" "$run_id" "$sample" "$node" "$digest" "$sample" "$node"
`)

	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := filepath.Join(t.TempDir(), "terminal-storage-harness.sh")
	harnessBody := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
OUT_DIR="$WK_TEST_RUN_DIR"
STORAGE_OVERLAP_CAPTURE="$WK_TEST_STORAGE_HELPER"
SINGLE_NODE_DATA_DIR="$WK_TEST_RUN_DIR/data"
mkdir -p "$OUT_DIR/reports/000100-qps/evidence" "$OUT_DIR/metrics/000100" "$SINGLE_NODE_DATA_DIR/slotraft-snapshots"
metrics="$OUT_DIR/metrics/000100/candidate.prom"
printf 'wukongim_storage_pebble_compaction_count 1\nwukongim_storage_pebble_compactions_in_progress 0\n' >"$metrics"
initialize_storage_overlap_evidence 000100
periodic_cut='{"observed_at":"2026-08-14T01:02:03Z","run_id":"run-a","assignment_id":"generation-a","phase":"warmup","active_phase":"run"}'
terminal_cut='{"observed_at":"2026-08-14T01:02:04Z","run_id":"run-a","assignment_id":"generation-a","phase":"run","active_phase":"cooldown"}'
(capture_storage_overlap_cut 000100 "$metrics" "$periodic_cut" periodic-000001; : >"$WK_TEST_PERIODIC_DONE") &
periodic_pid=$!
deadline=$((SECONDS + 5))
while [[ ! -f "$WK_TEST_PERIODIC_STARTED" && $SECONDS -lt $deadline ]]; do sleep 0.02; done
[[ -f "$WK_TEST_PERIODIC_STARTED" ]]
(capture_storage_overlap_cut 000100 "$metrics" "$terminal_cut" terminal "$(( $(date -u '+%s') + TERMINAL_CUT_ACK_SAFETY_SECONDS + 5 ))"; : >"$WK_TEST_TERMINAL_DONE") &
terminal_pid=$!
sleep 0.15
[[ ! -f "$WK_TEST_TERMINAL_DONE" ]]
: >"$WK_TEST_RELEASE_PERIODIC"
wait "$periodic_pid"
wait "$terminal_pid"
hash_before="$(sha256_file "$(storage_overlap_evidence_path 000100)")"
capture_storage_overlap_cut 000100 "$metrics" "$periodic_cut" periodic-000002
hash_after="$(sha256_file "$(storage_overlap_evidence_path 000100)")"
[[ "$hash_before" == "$hash_after" ]]
[[ "$(awk -F '\t' 'END {print $3}' "$(storage_overlap_evidence_path 000100)")" == terminal ]]
[[ "$(grep -c '^terminal$' "$WK_TEST_HELPER_CALLS")" -eq 1 ]]
[[ "$(grep -c '^periodic-000002$' "$WK_TEST_HELPER_CALLS" || true)" -eq 0 ]]
printf '%s %s\n' "$periodic_pid" "$terminal_pid" >"$WK_TEST_CAPTURE_PIDS"
`
	if err := os.WriteFile(harness, []byte(harnessBody), 0o700); err != nil {
		t.Fatal(err)
	}

	periodicStarted := filepath.Join(runDir, "periodic.started")
	releasePeriodic := filepath.Join(runDir, "periodic.release")
	helperPIDs := filepath.Join(runDir, "helper.pids")
	capturePIDs := filepath.Join(runDir, "capture.pids")
	commandContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, "bash", harness)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_TEST_RUN_DIR="+runDir,
		"WK_TEST_STORAGE_HELPER="+helper,
		"WK_TEST_PERIODIC_STARTED="+periodicStarted,
		"WK_TEST_RELEASE_PERIODIC="+releasePeriodic,
		"WK_TEST_PERIODIC_DONE="+filepath.Join(runDir, "periodic.done"),
		"WK_TEST_TERMINAL_DONE="+filepath.Join(runDir, "terminal.done"),
		"WK_TEST_HELPER_PIDS="+helperPIDs,
		"WK_TEST_HELPER_CALLS="+filepath.Join(runDir, "helper.calls"),
		"WK_TEST_CAPTURE_PIDS="+capturePIDs,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("terminal storage fence harness failed: %v\n%s", err, output)
	}
	if commandContext.Err() != nil {
		t.Fatalf("terminal storage fence harness timed out: %v", commandContext.Err())
	}
	for _, path := range []string{helperPIDs, capturePIDs} {
		for _, rawPID := range strings.Fields(readFile(t, path)) {
			pid, err := strconv.Atoi(rawPID)
			if err != nil {
				t.Fatalf("invalid recorded pid %q: %v", rawPID, err)
			}
			if err := syscall.Kill(pid, 0); err == nil {
				t.Fatalf("terminal storage harness left process %d alive", pid)
			}
		}
	}
}

func TestSingleNodeTerminalObserverJoinSurvivesParentSignal(t *testing.T) {
	root := repoRoot(t)
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := filepath.Join(t.TempDir(), "terminal-observer-join-harness.sh")
	harnessBody := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT INT TERM
signal_seen=0
trap 'signal_seen=1' TERM
TERMINAL_CUT_OBSERVER_STOP_FILE="$WK_TEST_STOP_FILE"
(
  while [[ ! -f "$TERMINAL_CUT_OBSERVER_STOP_FILE" ]]; do sleep 0.02; done
  sleep 0.35
) &
TERMINAL_CUT_OBSERVER_PID=$!
observer_pid="$TERMINAL_CUT_OBSERVER_PID"
( sleep 0.05; kill -TERM "$$" ) &
signaler=$!
terminate_terminal_cut_observer
wait "$signaler"
[[ "$signal_seen" -eq 1 ]]
[[ -z "$TERMINAL_CUT_OBSERVER_PID" ]]
if kill -0 "$observer_pid" 2>/dev/null; then
  exit 91
fi
printf '%s\n' "$observer_pid" >"$WK_TEST_OBSERVER_PID"
`
	if err := os.WriteFile(harness, []byte(harnessBody), 0o700); err != nil {
		t.Fatal(err)
	}

	runDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", harness)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_TEST_STOP_FILE="+filepath.Join(runDir, "observer.stop"),
		"WK_TEST_OBSERVER_PID="+filepath.Join(runDir, "observer.pid"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("signal-safe observer join failed: %v\n%s", err, output)
	}
	if ctx.Err() != nil {
		t.Fatalf("signal-safe observer join timed out: %v", ctx.Err())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, filepath.Join(runDir, "observer.pid"))))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("terminal observer %d survived signal-safe join", pid)
	}
}

func TestSingleNodeHostOverlapPreflightRetainsPriorFilesystemObservation(t *testing.T) {
	root := repoRoot(t)
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "df"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$WK_TEST_DF_CALLS"
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
printf '/dev/fake 1000 250 750 25%% /fake\n'
`)
	detector := filepath.Join(binDir, "detect-overlap")
	writeExecutable(t, detector, `#!/usr/bin/env bash
set -euo pipefail
printf '999\tfake-existing-wukongim\n'
`)
	harness := filepath.Join(t.TempDir(), "preflight-order-harness.sh")
	harnessBody := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT INT TERM
OUT_DIR="$WK_TEST_RUN_DIR"
SINGLE_NODE_DATA_DIR="$WK_TEST_DATA_DIR"
HOST_OVERLAP_DETECTOR="$WK_TEST_DETECTOR"
START_CLUSTER=1
MINIMUM_FREE_PERCENT=10
mkdir -p "$OUT_DIR"
mkdir -p "$SINGLE_NODE_DATA_DIR"
status=0
local_baseline_preflight || status=$?
[[ "$status" -eq 2 ]]
[[ "$PREFLIGHT_OUTCOME" == host_confounded ]]
[[ "$PREFLIGHT_REASON" == overlapping_wukongim_workload ]]
[[ "$PREFLIGHT_FREE_PERCENT" -eq 75 ]]
[[ "$PREFLIGHT_FILESYSTEM_OBSERVATION_COMPLETE" == true ]]
grep -q $'observed_filesystem_free_percent\t75' "$OUT_DIR/preflight-result.tsv"
[[ "$(tail -n 1 "$WK_TEST_DF_CALLS")" == "-Pk $SINGLE_NODE_DATA_DIR" ]]
`
	if err := os.WriteFile(harness, []byte(harnessBody), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", harness)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"WK_TEST_RUN_DIR="+t.TempDir(),
		"WK_TEST_DATA_DIR="+filepath.Join(t.TempDir(), "product-data"),
		"WK_TEST_DF_CALLS="+filepath.Join(t.TempDir(), "df.calls"),
		"WK_TEST_DETECTOR="+detector,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("filesystem-first host-overlap preflight failed: %v\n%s", err, output)
	}
	if ctx.Err() != nil {
		t.Fatalf("filesystem-first host-overlap preflight timed out: %v", ctx.Err())
	}
}
