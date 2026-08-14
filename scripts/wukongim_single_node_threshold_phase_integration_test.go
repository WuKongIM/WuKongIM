//go:build integration

package scripts_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

func TestSingleNodeLifecycleSamplerStopFailsClosedAfterUnexpectedExit(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	harnessPath := filepath.Join(t.TempDir(), "lifecycle-sampler-exit.sh")
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
ROOT_DIR="$WK_TEST_REPO_ROOT"
OUT_DIR="$WK_TEST_RUN_DIR"
BASELINE_INVOCATION_ID="sampler-exit-test"
LIFECYCLE_SAMPLE_INTERVAL=0.01
mkdir -p "$OUT_DIR/reports/000100-qps"
lifecycle_sampler_loop() {
  sleep 0.05
  printf 'injected lifecycle sampler failure\n' >&2
  return 42
}
start_lifecycle_sampler 000100
sampler_pid="$LIFECYCLE_SAMPLER_PID"
while kill -0 "$sampler_pid" 2>/dev/null; do
  sleep 0.01
done
stop_lifecycle_sampler
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS", "WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL",
	),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL=0.01",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("stop_lifecycle_sampler error = %v, output=%s; want child exit 42", err, output)
	}

	reportDir := filepath.Join(runDir, "reports", "000100-qps")
	logText := readFile(t, filepath.Join(reportDir, "lifecycle-sampler.log"))
	if !strings.Contains(logText, "injected lifecycle sampler failure") {
		t.Fatalf("lifecycle sampler stderr was not retained:\n%s", logText)
	}
	var status struct {
		Schema      string `json:"schema"`
		PID         int    `json:"pid"`
		StartToken  string `json:"start_token"`
		Attempts    int    `json:"attempts"`
		Completions int    `json:"completions"`
		ExitStatus  int    `json:"exit_status"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(reportDir, "lifecycle-sampler-status.json"))), &status); err != nil {
		t.Fatal(err)
	}
	if status.Schema != "wukongim/chat-lifecycle-local-single-node-sampler-status/v1" ||
		status.PID <= 0 || status.StartToken == "" || status.Attempts != 0 || status.Completions != 0 ||
		status.ExitStatus != 42 || status.Reason != "unexpected_exit" {
		t.Fatalf("unexpected lifecycle sampler status: %+v", status)
	}
}

func TestSingleNodeLifecycleSamplerStopRejectsUnmatchedCaptureAttempt(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	harnessPath := filepath.Join(t.TempDir(), "lifecycle-sampler-unmatched.sh")
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
ROOT_DIR="$WK_TEST_REPO_ROOT"
OUT_DIR="$WK_TEST_RUN_DIR"
BASELINE_INVOCATION_ID="sampler-unmatched-test"
LIFECYCLE_SAMPLE_INTERVAL=0.01
report_dir="$OUT_DIR/reports/000100-qps"
mkdir -p "$report_dir"
lifecycle_sampler_loop() {
  LIFECYCLE_SAMPLER_CHILD_ATTEMPTS=1
  LIFECYCLE_SAMPLER_CHILD_COMPLETIONS=0
  write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_CHILD_STATUS_FILE" \
    "$LIFECYCLE_SAMPLER_CHILD_PID" "$LIFECYCLE_SAMPLER_CHILD_START_TOKEN" 1 0 0 capturing
  while [[ ! -f "$2" ]]; do
    sleep 0.01
  done
  return 0
}
start_lifecycle_sampler 000100
ready=false
for _ in $(seq 1 200); do
  if jq -e '.attempts == 1 and .completions == 0 and .reason == "capturing"' "$report_dir/lifecycle-sampler-status.json" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.01
done
[[ "$ready" == true ]]
stop_lifecycle_sampler
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS", "WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL",
	),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL=0.01",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 70 {
		t.Fatalf("stop_lifecycle_sampler error = %v, output=%s; want unmatched capture exit 70", err, output)
	}
	var status struct {
		Attempts    int    `json:"attempts"`
		Completions int    `json:"completions"`
		ExitStatus  int    `json:"exit_status"`
		Reason      string `json:"reason"`
	}
	reportDir := filepath.Join(runDir, "reports", "000100-qps")
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(reportDir, "lifecycle-sampler-status.json"))), &status); err != nil {
		t.Fatal(err)
	}
	if status.Attempts != 1 || status.Completions != 0 || status.ExitStatus != 70 || status.Reason != "unexpected_exit" {
		t.Fatalf("unmatched lifecycle sampler status: %+v", status)
	}
}

func TestSingleNodeLifecycleSamplerStopRejectsUnfinalizedChildStatus(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	harnessPath := filepath.Join(t.TempDir(), "lifecycle-sampler-unfinalized.sh")
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
ROOT_DIR="$WK_TEST_REPO_ROOT"
OUT_DIR="$WK_TEST_RUN_DIR"
BASELINE_INVOCATION_ID="sampler-unfinalized-test"
LIFECYCLE_SAMPLE_INTERVAL=0.01
report_dir="$OUT_DIR/reports/000100-qps"
mkdir -p "$report_dir"
lifecycle_sampler_process() {
  local start_file="$3" status_file="$4" identity pid start_token
  while [[ ! -f "$start_file" ]]; do
    sleep 0.01
  done
  identity="$(jq -er '[.pid,.start_token] | @tsv' "$status_file")"
  IFS=$'\t' read -r pid start_token <<<"$identity"
  write_lifecycle_sampler_status "$status_file" "$pid" "$start_token" 1 1 0 running
  while [[ ! -f "$2" ]]; do
    sleep 0.01
  done
  return 0
}
start_lifecycle_sampler 000100
ready=false
for _ in $(seq 1 200); do
  if jq -e '.attempts == 1 and .completions == 1 and .exit_status == 0 and .reason == "running"' "$report_dir/lifecycle-sampler-status.json" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.01
done
[[ "$ready" == true ]]
stop_lifecycle_sampler
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS", "WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL",
	),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL=0.01",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 70 {
		t.Fatalf("stop_lifecycle_sampler error = %v, output=%s; want unfinalized child exit 70", err, output)
	}
	var status struct {
		Attempts    int    `json:"attempts"`
		Completions int    `json:"completions"`
		ExitStatus  int    `json:"exit_status"`
		Reason      string `json:"reason"`
	}
	reportDir := filepath.Join(runDir, "reports", "000100-qps")
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(reportDir, "lifecycle-sampler-status.json"))), &status); err != nil {
		t.Fatal(err)
	}
	if status.Attempts != 1 || status.Completions != 1 || status.ExitStatus != 70 || status.Reason != "unexpected_exit" {
		t.Fatalf("unfinalized lifecycle sampler status: %+v", status)
	}
}

func TestSingleNodeLifecycleSamplerNormalStopJoinsAndCleansTemporaryFiles(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	harnessPath := filepath.Join(t.TempDir(), "lifecycle-sampler-stop.sh")
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
ROOT_DIR="$WK_TEST_REPO_ROOT"
OUT_DIR="$WK_TEST_RUN_DIR"
BASELINE_INVOCATION_ID="sampler-stop-test"
LIFECYCLE_SAMPLE_INTERVAL=0.01
report_dir="$OUT_DIR/reports/000100-qps"
mkdir -p "$report_dir"
capture_lifecycle_sample() {
  while [[ ! -f "$report_dir/release-capture" ]]; do
    sleep 0.01
  done
  return 0
}
start_lifecycle_sampler 000100
sampler_pid="$LIFECYCLE_SAMPLER_PID"
blocked=false
for _ in $(seq 1 200); do
  if jq -e '.reason == "capturing" and .attempts == 1 and .completions == 0' "$report_dir/lifecycle-sampler-status.json" >/dev/null 2>&1; then
    blocked=true
    break
  fi
  sleep 0.01
done
[[ "$blocked" == true ]]
kill -0 "$sampler_pid"
touch "$report_dir/release-capture"
ready=false
for _ in $(seq 1 200); do
  if jq -e '.attempts >= 3 and .completions >= 3' "$report_dir/lifecycle-sampler-status.json" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.01
done
[[ "$ready" == true ]]
stop_lifecycle_sampler
if kill -0 "$sampler_pid" 2>/dev/null; then
  printf 'lifecycle sampler child remains alive: %s\n' "$sampler_pid" >&2
  exit 71
fi
[[ ! -e "$report_dir/lifecycle-sampler.stop" ]]
[[ ! -e "$report_dir/.lifecycle-sampler.start" ]]
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS", "WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL",
	),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL=0.01",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("normal lifecycle sampler stop failed: %v\n%s", err, output)
	}

	reportDir := filepath.Join(runDir, "reports", "000100-qps")
	var status struct {
		PID         int    `json:"pid"`
		StartToken  string `json:"start_token"`
		Attempts    int    `json:"attempts"`
		Completions int    `json:"completions"`
		ExitStatus  int    `json:"exit_status"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(reportDir, "lifecycle-sampler-status.json"))), &status); err != nil {
		t.Fatal(err)
	}
	if status.PID <= 0 || status.StartToken == "" || status.Attempts < 3 || status.Completions != status.Attempts ||
		status.ExitStatus != 0 || status.Reason != "stopped" {
		t.Fatalf("normal lifecycle sampler status: %+v", status)
	}
	if matches, err := filepath.Glob(filepath.Join(reportDir, "lifecycle-sampler-status.json.next.*")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("lifecycle sampler left temporary status files: %v", matches)
	}
}

func TestSingleNodeLifecycleSamplerCompletesRepeatedExternalCaptures(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	binDir := t.TempDir()
	writeSingleNodeCooldownStatusCurl(t, filepath.Join(binDir, "curl"))
	harnessPath := filepath.Join(t.TempDir(), "lifecycle-sampler-repeated.sh")
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
BASELINE_INVOCATION_ID="sampler-repeated-test"
LIFECYCLE_SAMPLE_INTERVAL=0.01
report_dir="$OUT_DIR/reports/000100-qps"
mkdir -p "$report_dir" "$WUKONGIM_LOG_DIR"
printf '[fake-single] node pid=%s\n' "$$" >"$OUT_DIR/cluster-start.log"
start_lifecycle_sampler 000100
ready=false
for _ in $(seq 1 400); do
  if jq -e '.attempts >= 3 and .completions >= 3' "$report_dir/lifecycle-sampler-status.json" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.01
done
[[ "$ready" == true ]]
stop_lifecycle_sampler
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS", "WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL=0.01",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
		"WK_TEST_BASELINE_INVOCATION_ID=sampler-repeated-test",
		"WK_TEST_FAKE_STATUS_MODE=run",
	)
	if output, err := command.CombinedOutput(); err != nil {
		reportDir := filepath.Join(runDir, "reports", "000100-qps")
		status, _ := os.ReadFile(filepath.Join(reportDir, "lifecycle-sampler-status.json"))
		logText, _ := os.ReadFile(filepath.Join(reportDir, "lifecycle-sampler.log"))
		t.Fatalf("repeated lifecycle sampler captures failed: %v\n%s\nstatus=%s\nlog=%s", err, output, status, logText)
	}
	timeline := readFile(t, filepath.Join(runDir, "reports", "000100-qps", "lifecycle-status.jsonl"))
	lines := strings.Split(strings.TrimSpace(timeline), "\n")
	if len(lines) < 3 {
		logText := readFile(t, filepath.Join(runDir, "reports", "000100-qps", "lifecycle-sampler.log"))
		t.Fatalf("lifecycle sampler retained %d captures, want at least 3:\n%s\nlog:\n%s", len(lines), timeline, logText)
	}
}

func TestSingleNodeRunAttemptMapsLifecycleSamplerFailureToLocalExitSix(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	harnessPath := filepath.Join(t.TempDir(), "run-attempt-sampler-failure.sh")
	production := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-single-node-1000ch.sh"))
	const entrypoint = "\nmain \"$@\"\n"
	if !strings.HasSuffix(production, entrypoint) {
		t.Fatal("single-node wrapper entrypoint changed; integration harness cannot disable main safely")
	}
	harness := strings.TrimSuffix(production, entrypoint) + `
trap - EXIT
ROOT_DIR="$WK_TEST_REPO_ROOT"
OUT_DIR="$WK_TEST_RUN_DIR"
BASELINE_INVOCATION_ID="sampler-run-attempt-test"
DURATION=1s
METRICS_VALUES=("http://127.0.0.1:5001")
mkdir -p "$OUT_DIR/reports/000100-qps"
printf 'tag\toffered_qps\tstatus\texit_status\tactual_qps\tsend_success\tsend_errors\tconnect_error_rate\tsendack_error_rate\tp50_seconds\tp95_seconds\tp99_seconds\tmax_seconds\tconnect_success\tscheduler_planned\tscheduler_dispatched\tscheduler_dropped\n' >"$OUT_DIR/summary.tsv"
write_scenario() { return 0; }
stop_worker_exact_from_status() { return 0; }
scrape_metrics() { return 0; }
start_lifecycle_sampler() {
  : >"$OUT_DIR/reports/000100-qps/lifecycle-status.jsonl"
  LIFECYCLE_SAMPLER_PID=99999
  return 0
}
start_threshold_profile_watcher() { return 0; }
start_runtime_pool_sampler() { return 0; }
start_terminal_cut_observer() { return 0; }
write_threshold_profile_phase() { return 0; }
stop_terminal_cut_observer() { return 0; }
stop_lifecycle_sampler() {
  LIFECYCLE_SAMPLER_PID=""
  return 42
}
capture_lifecycle_sample() { return 0; }
stop_threshold_profile_watcher() { return 0; }
stop_runtime_pool_sampler() { return 0; }
classify_metrics() { return 0; }
rpc_pull_qps_summary() { return 0; }
channel_metrics_summary() { return 0; }
channelappend_metrics_summary() { return 0; }
storage_metrics_summary() { return 0; }
host_io_summary() { return 0; }
runtime_pool_pressure_summary() { return 0; }
ants_pool_usage_summary() { return 0; }
cluster_transport_peak_summary() { return 0; }
fake_wkbench() {
  printf '{"status":"passed","summary":{"send_success":1,"connect_error_rate":0,"sendack_error_rate":0,"connect_success":1},"metrics":{"counters":{},"histograms":{}}}\n' >"$OUT_DIR/reports/000100-qps/report.json"
}
WK_BENCH_BIN=fake_wkbench
run_attempt 100
awk -F '\t' 'NR == 2 { exit !($4 == 6) }' "$OUT_DIR/summary.tsv"
jq -e 'select(.error == "lifecycle_sampler_failed")' "$OUT_DIR/reports/000100-qps/lifecycle-status.jsonl" >/dev/null
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_SINGLE_NODE_QPS",
	),
		"WK_BENCH_SINGLE_NODE_OUT_DIR="+runDir,
		"WK_BENCH_SINGLE_NODE_QPS=100",
		"WK_TEST_REPO_ROOT="+root,
		"WK_TEST_RUN_DIR="+runDir,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run_attempt did not retain a typed local exit after sampler failure: %v\n%s", err, output)
	}
}

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
