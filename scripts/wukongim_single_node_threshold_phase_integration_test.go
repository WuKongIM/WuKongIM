//go:build integration

package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

func TestSingleNodeLifecycleSamplerPreservesBoundedReceiveDrainProof(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	binDir := t.TempDir()
	writeSingleNodeCooldownStatusCurl(t, filepath.Join(binDir, "curl"))
	runSingleNodeLifecycleSampleHarness(t, root, runDir, binDir, "WK_TEST_FAKE_STATUS_MODE=receive-drain")

	data := []byte(readFile(t, filepath.Join(runDir, "reports", "000100-qps", "lifecycle-status.jsonl")))
	var capture localbaseline.LifecycleCapture
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatalf("decode shell-projected typed lifecycle: %v\n%s", err, data)
	}
	if capture.Status == nil || capture.Status.Lifecycle == nil {
		t.Fatalf("lifecycle projection missing: %+v", capture)
	}
	drain := capture.Status.Lifecycle.ReceiveDrain
	if !drain.Required || !drain.EvidenceComplete || drain.ClientCount != 2500 || drain.ActiveDrains != 2500 ||
		drain.QueueSnapshotClients != 2500 || drain.ReceiveFramesObserved != 987 || drain.BufferedFramesDrained != 11 ||
		drain.StableZeroObservations != 1 {
		t.Fatalf("receive drain projection = %+v", drain)
	}
}

func TestSingleNodeLifecycleSamplerClosesThresholdProfilePhaseDuringCapture(t *testing.T) {
	root := repoRoot(t)
	server := newLocalPprofTestServer(t, time.Second, "")
	runDir := t.TempDir()
	tag := "000100"
	evidenceDir := filepath.Join(runDir, "reports", tag+"-qps", "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	phasePath := filepath.Join(evidenceDir, "threshold-pprof-phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	profileDir := filepath.Join(evidenceDir, "threshold-pprof")
	helper := localThresholdPprofCommand(root, "single-node-phase-test-token",
		localThresholdPprofArgs(profileDir, phasePath, "actual_offered_ratio", []*localPprofTestServer{server})...)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	select {
	case <-server.profileStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("threshold profile helper did not start its measured capture")
	}

	binDir := t.TempDir()
	writeSingleNodeCooldownStatusCurl(t, filepath.Join(binDir, "curl"))
	runSingleNodeLifecycleSampleHarness(t, root, runDir, binDir)
	if phase := strings.TrimSpace(readFile(t, phasePath)); phase != "drain" {
		t.Fatalf("threshold profile phase = %q, want drain after the worker entered cooldown", phase)
	}
	if err := helper.Wait(); err != nil {
		t.Fatalf("threshold profile helper failed: %v", err)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(profileDir, "metadata.json")))
	if metadata.Capture.Status != "partial" || metadata.Capture.Valid ||
		metadata.Capture.Reason != "phase_changed_during_capture" || metadata.Capture.EndPhase != "drain" {
		t.Fatalf("cross-admission profile capture = %+v, want a closed partial drain result", metadata.Capture)
	}
}

func TestSingleNodeLifecycleSamplerDoesNotCloseProfilePhaseFromPreviousAssignment(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	evidenceDir := filepath.Join(runDir, "reports", "000100-qps", "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	phasePath := filepath.Join(evidenceDir, "threshold-pprof-phase")
	writeLocalPprofPhase(t, phasePath, "warmup")
	binDir := t.TempDir()
	writeSingleNodeCooldownStatusCurl(t, filepath.Join(binDir, "curl"))
	runSingleNodeLifecycleSampleHarness(t, root, runDir, binDir,
		"WK_TEST_FAKE_STATUS_MODE=stopped", "WK_TEST_FAKE_RUN_ID=previous-step")

	if phase := strings.TrimSpace(readFile(t, phasePath)); phase != "warmup" {
		t.Fatalf("previous assignment changed threshold profile phase to %q, want warmup", phase)
	}
}

func TestSingleNodeLifecycleSamplerMapsCurrentAssignmentPhases(t *testing.T) {
	for _, test := range []struct {
		name, statusMode, initial, want string
	}{
		{name: "measured admission", statusMode: "run", initial: "warmup", want: "measurement"},
		{name: "terminal assignment", statusMode: "stopped", initial: "drain", want: "shutdown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := repoRoot(t)
			runDir := t.TempDir()
			evidenceDir := filepath.Join(runDir, "reports", "000100-qps", "evidence")
			if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			phasePath := filepath.Join(evidenceDir, "threshold-pprof-phase")
			writeLocalPprofPhase(t, phasePath, test.initial)
			binDir := t.TempDir()
			writeSingleNodeCooldownStatusCurl(t, filepath.Join(binDir, "curl"))
			runSingleNodeLifecycleSampleHarness(t, root, runDir, binDir,
				"WK_TEST_FAKE_STATUS_MODE="+test.statusMode)

			if phase := strings.TrimSpace(readFile(t, phasePath)); phase != test.want {
				t.Fatalf("threshold profile phase = %q, want %q", phase, test.want)
			}
		})
	}
}

func runSingleNodeLifecycleSampleHarness(t *testing.T, root, runDir, binDir string, extraEnv ...string) {
	t.Helper()
	harnessPath := filepath.Join(t.TempDir(), "sample-cooldown.sh")
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
ROOT_DIR="$WK_TEST_REPO_ROOT"
OUT_DIR="$WK_TEST_RUN_DIR"
WORKER_ADDR="http://127.0.0.1:19130"
WORKER_PID="$$"
HOST_METRICS_PID=""
MAIN_SHELL_PID="$$"
WUKONGIM_LOG_DIR="$WK_TEST_RUN_DIR/fake-node-logs"
HOST_OVERLAP_DETECTOR="/usr/bin/true"
BASELINE_INVOCATION_ID="$WK_TEST_BASELINE_INVOCATION_ID"
mkdir -p "$OUT_DIR/reports/000100-qps" "$WUKONGIM_LOG_DIR"
printf '[fake-single] node pid=%s\n' "$$" >"$OUT_DIR/cluster-start.log"
capture_lifecycle_sample 000100
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS", "WK_BENCH_PROFILE_SECONDS",
		"WK_CLUSTER_INITIAL_SLOT_COUNT", "WK_CLUSTER_HASH_SLOT_COUNT", "WK_CLUSTER_SLOT_REPLICA_N",
		"WK_CLUSTER_CHANNEL_REPLICA_N", "WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW",
		"WK_CLUSTER_COMMIT_COORDINATOR_SHARDS", "WK_CLUSTER_COMMIT_COORDINATOR_SYNC",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
		"WK_TEST_BASELINE_INVOCATION_ID=phase-test-invocation",
	)
	command.Env = append(command.Env, extraEnv...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("capture typed cooldown status through the single-node sampler: %v\n%s", err, output)
	}
}

func writeSingleNodeCooldownStatusCurl(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
url="${@: -1}"
if [[ "$url" != "http://127.0.0.1:19130/v1/status" ]]; then
  printf 'unexpected curl URL: %s\n' "$url" >&2
  exit 2
fi
run_id="${WK_TEST_FAKE_RUN_ID:-single-node-${WK_TEST_BASELINE_INVOCATION_ID:-phase-test-invocation}-fixed-1000ch-000100-qps}"
case "${WK_TEST_FAKE_STATUS_MODE:-cooldown}" in
  receive-drain)
    printf '{"phase":"run","active_phase":"cooldown","completed_phase":"run","last_error":"","observed_at":"2026-08-13T08:00:02Z","lifecycle":{"active_connections":2500,"terminal_pre_close":false,"traffic":{"planned":100,"dispatched":100,"logical_sent":100,"send_attempts":100,"sendacks":80,"terminal_errors":0,"correctness_errors":0,"remaining":20,"retry_attempts":0,"retry_exhausted":0,"stable_client_msg_no":true,"retry_evidence_complete":true,"max_retries":3},"receive_drain":{"required":true,"evidence_complete":true,"drain_complete":false,"client_count":2500,"active_drains":2500,"queue_snapshot_clients":2500,"inner_recv_depth":0,"adapter_queue_depth":0,"matching_buffer_depth":0,"foreground_matchers":0,"read_frames_inflight":0,"recvacks_inflight":0,"publications_inflight":0,"publication_waiters":0,"recvack_failures":0,"read_failures":0,"receive_frames_observed":987,"buffered_frames_drained":11,"stable_zero_observations":1}},"assignment":{"run_id":"%s","assignment_id":"phase-test-assignment","worker_id":"worker-a"}}\n' "$run_id"
    ;;
  run)
    printf '{"phase":"warmup","active_phase":"run","completed_phase":"warmup","last_error":"","observed_at":"2026-08-13T08:00:01Z","lifecycle":{"active_connections":2500,"terminal_pre_close":false,"traffic":{"planned":50,"dispatched":50,"logical_sent":50,"send_attempts":50,"sendacks":50,"terminal_errors":0,"correctness_errors":0,"remaining":0,"retry_attempts":0,"retry_exhausted":0,"stable_client_msg_no":true,"retry_evidence_complete":true,"max_retries":3}},"assignment":{"run_id":"%s","assignment_id":"phase-test-assignment","worker_id":"worker-a"}}\n' "$run_id"
    ;;
  stopped)
    printf '{"phase":"stopped","completed_phase":"cooldown","last_error":"","observed_at":"2026-08-13T08:00:03Z","lifecycle":{"active_connections":2500,"terminal_pre_close":true,"traffic":{"planned":100,"dispatched":100,"logical_sent":100,"send_attempts":100,"sendacks":100,"terminal_errors":0,"correctness_errors":0,"remaining":0,"retry_attempts":0,"retry_exhausted":0,"stable_client_msg_no":true,"retry_evidence_complete":true,"max_retries":3}},"assignment":{"run_id":"%s","assignment_id":"phase-test-assignment","worker_id":"worker-a"}}\n' "$run_id"
    ;;
  *)
    printf '{"phase":"run","active_phase":"cooldown","completed_phase":"run","last_error":"","observed_at":"2026-08-13T08:00:02Z","lifecycle":{"active_connections":2500,"terminal_pre_close":false,"traffic":{"planned":100,"dispatched":100,"logical_sent":100,"send_attempts":100,"sendacks":80,"terminal_errors":0,"correctness_errors":0,"remaining":20,"retry_attempts":0,"retry_exhausted":0,"stable_client_msg_no":true,"retry_evidence_complete":true,"max_retries":3}},"assignment":{"run_id":"%s","assignment_id":"phase-test-assignment","worker_id":"worker-a"}}\n' "$run_id"
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}
