//go:build integration

package scripts_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func TestChatLifecycleLocalBaselineForwardsSignalAndJoinsActiveStep(t *testing.T) {
	root := repoRoot(t)
	testRoot := t.TempDir()
	scriptsDir := filepath.Join(testRoot, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	baselinePath := filepath.Join(scriptsDir, "run-wukongim-three-node-chat-lifecycle-local-baseline.sh")
	baseline := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-local-baseline.sh"))
	if err := os.WriteFile(baselinePath, []byte(baseline), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeShakeout := `#!/usr/bin/env bash
set -euo pipefail
trap 'printf "term\n" >>"$FAKE_SIGNAL_LOG"; sleep 0.25; printf "joined\n" >>"$FAKE_SIGNAL_LOG"; exit 143' TERM
printf '%s\n' "$$" >"$FAKE_CHILD_PID_FILE"
: >"$FAKE_READY_FILE"
while :; do sleep 0.05; done
`
	shakeoutPath := filepath.Join(scriptsDir, "run-wukongim-three-node-chat-lifecycle-shakeout.sh")
	if err := os.WriteFile(shakeoutPath, []byte(fakeShakeout), 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(testRoot, "test-bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "df"), []byte("#!/usr/bin/env bash\nprintf 'Filesystem 1024-blocks Used Available Capacity Mounted on\\nfake 100000 10000 90000 10%% /\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(testRoot, "baseline-run")
	readyFile := filepath.Join(testRoot, "child.ready")
	childPIDFile := filepath.Join(testRoot, "child.pid")
	signalLog := filepath.Join(testRoot, "child-signals.log")
	command := exec.Command("bash", baselinePath, "--run-dir", runDir, "--base-port", "25000")
	command.Dir = testRoot
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_API_TOKEN=test-api-token",
		"WK_BENCH_WORKER_TOKEN=test-worker-token",
		"FAKE_READY_FILE="+readyFile,
		"FAKE_CHILD_PID_FILE="+childPIDFile,
		"FAKE_SIGNAL_LOG="+signalLog,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		if encoded, err := os.ReadFile(childPIDFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(encoded))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	waitForChatLifecycleFile(t, readyFile, 3*time.Second)
	encodedPID := readFile(t, childPIDFile)
	childPID, err := strconv.Atoi(strings.TrimSpace(encodedPID))
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case err := <-waitDone:
		requireLocalBaselineExitCode(t, err, output.Bytes(), 143)
	case <-time.After(5 * time.Second):
		t.Fatalf("baseline did not join its signaled step child:\n%s", output.String())
	}
	if got := readFile(t, signalLog); got != "term\njoined\n" {
		t.Fatalf("step signal/join log = %q, want exactly one forwarded TERM followed by joined", got)
	}
	if err := syscall.Kill(childPID, 0); err == nil || !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signaled step child %d is still alive or indeterminate: %v", childPID, err)
	}
	result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
	if !strings.Contains(result, `"outcome": "insufficient_evidence"`) ||
		!strings.Contains(result, `"reason": "operator_interrupted"`) {
		t.Fatalf("operator-interrupted baseline result = %s", result)
	}
	if _, err := os.Stat(filepath.Join(runDir, "checksums.sha256")); err != nil {
		t.Fatalf("operator-interrupted baseline did not seal evidence: %v", err)
	}
}

func TestChatLifecycleOverlapDetectorExcludesOwnedProcesses(t *testing.T) {
	root := repoRoot(t)
	detector := filepath.Join(root, "scripts", "chat-lifecycle", "detect-local-workload-overlap.sh")
	binDir := t.TempDir()
	fixture := filepath.Join(root, "scripts", "testdata", "local-workload-overlap-ps.txt")
	fakePS := `#!/usr/bin/env bash
if [[ "${FAKE_PS_FAIL:-0}" == 1 ]]; then exit 7; fi
cat "$FAKE_PS_FIXTURE"
`
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte(fakePS), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", detector, "101")
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_PS_FIXTURE="+fixture)
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "202\twkbench\n303\twkbench-test\n" {
		t.Fatalf("detector did not isolate the foreign workload: err=%v output=%q", err, output)
	}

	command = exec.Command("bash", detector, "101", "202", "303")
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_PS_FIXTURE="+fixture)
	output, err = command.CombinedOutput()
	if err != nil || len(output) != 0 {
		t.Fatalf("detector reported only owned workloads: err=%v output=%q", err, output)
	}

	command = exec.Command("bash", detector, "101", "202", "303")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_PS_FIXTURE="+fixture,
		"FAKE_PS_FAIL=1",
	)
	if output, err = command.CombinedOutput(); err == nil {
		t.Fatalf("detector accepted a failed process observation: %q", output)
	}
}

func TestChatLifecycleShakeoutSealsBuildWindowAndRendersNonAliasingPorts(t *testing.T) {
	root := repoRoot(t)
	testRoot := t.TempDir()
	scriptsDir := filepath.Join(testRoot, "scripts")
	if err := os.MkdirAll(filepath.Join(scriptsDir, "wukongim"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(scriptsDir, "chat-lifecycle"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "chat-lifecycle", "detect-local-workload-overlap.sh"),
		[]byte("#!/usr/bin/env bash\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "chat-lifecycle", "capture-local-storage-overlap.sh"),
		[]byte("#!/usr/bin/env bash\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	validator := readFile(t, filepath.Join(root, "scripts", "storage-metrics-cut-consistent.awk"))
	if err := os.WriteFile(filepath.Join(scriptsDir, "storage-metrics-cut-consistent.awk"), []byte(validator), 0o600); err != nil {
		t.Fatal(err)
	}
	shakeoutPath := filepath.Join(scriptsDir, "run-wukongim-three-node-chat-lifecycle-shakeout.sh")
	shakeout := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	if err := os.WriteFile(shakeoutPath, []byte(shakeout), 0o700); err != nil {
		t.Fatal(err)
	}
	for node := 1; node <= 3; node++ {
		path := filepath.Join(scriptsDir, "wukongim", fmt.Sprintf("wukongim-node%d.toml", node))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("node_id = %d\n", node)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configDir := filepath.Join(testRoot, "configs", "wkbench", "chat-lifecycle")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "local-shakeout.yaml"), []byte(`run_id: local-chat-lifecycle-shakeout
timeline: {warmup: 10m, checkpoint: 20m, final: 30m}
workload: {send_rate_per_second: 100, max_global_burst: 200}
observation:
  api_addrs: ["http://127.0.0.1:15001", "http://127.0.0.1:15002", "http://127.0.0.1:15003"]
  gateway_tcp_addrs: ["127.0.0.1:15101", "127.0.0.1:15102", "127.0.0.1:15103"]
  metrics_addrs: ["http://127.0.0.1:15011/metrics", "http://127.0.0.1:15012/metrics", "http://127.0.0.1:15013/metrics"]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(testRoot, "test-bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(testRoot, "git-state")
	if err := os.WriteFile(stateFile, []byte("clean\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeGit := `#!/usr/bin/env bash
set -euo pipefail
case " $* " in
  *" rev-parse HEAD "*) printf '0123456789abcdef0123456789abcdef01234567\n' ;;
  *" diff --quiet "*) [[ "$(tr -d '\n' <"$FAKE_GIT_STATE")" == clean ]] ;;
  *" ls-files --others "*)
    if [[ "$(tr -d '\n' <"$FAKE_GIT_STATE")" != clean ]]; then
      printf 'source-mutated-during-build.go\n'
    fi
    ;;
  *) exit 2 ;;
esac
`
	fakeGo := `#!/usr/bin/env bash
set -euo pipefail
output=""
previous=""
for argument in "$@"; do
  if [[ "$previous" == -o ]]; then output="$argument"; fi
  previous="$argument"
done
[[ -n "$output" ]]
printf '#!/usr/bin/env bash\nexit 1\n' >"$output"
chmod 0700 "$output"
if [[ " $* " == *" ./cmd/wkbench "* ]]; then
  printf 'dirty\n' >"$FAKE_GIT_STATE"
fi
`
	for name, body := range map[string]string{
		"git": fakeGit,
		"go":  fakeGo,
		"ps":  "#!/usr/bin/env bash\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runDir := filepath.Join(testRoot, "run")
	command := exec.Command("bash", shakeoutPath, "--run-dir", runDir, "--base-port", "15100", "--ready-timeout", "1")
	command.Dir = testRoot
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_API_TOKEN=test-api-token",
		"WK_BENCH_WORKER_TOKEN=test-worker-token",
		"FAKE_GIT_STATE="+stateFile,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("fake service unexpectedly completed shakeout:\n%s", output)
	}
	identity := readChatLifecycleIdentity(t, filepath.Join(runDir, "evidence", "identity.tsv"))
	if identity["source_revision"] != "0123456789abcdef0123456789abcdef01234567" ||
		identity["source_dirty"] != "true" ||
		identity["source_rebuildable_from_revision"] != "false" ||
		identity["source_capture"] != "binary_identity_only" {
		t.Fatalf("build-window source identity did not fail closed: %#v\n%s", identity, output)
	}
	rendered := readFile(t, filepath.Join(runDir, "chat-lifecycle.yaml"))
	for _, want := range []string{
		`api_addrs: ["http://127.0.0.1:15101", "http://127.0.0.1:15102", "http://127.0.0.1:15103"]`,
		`gateway_tcp_addrs: ["127.0.0.1:15121", "127.0.0.1:15122", "127.0.0.1:15123"]`,
		`metrics_addrs: ["http://127.0.0.1:15101/metrics", "http://127.0.0.1:15102/metrics", "http://127.0.0.1:15103/metrics"]`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered lifecycle config missing %q:\n%s", want, rendered)
		}
	}
}

func TestChatLifecycleShakeoutRequiresStableServiceReadiness(t *testing.T) {
	root := repoRoot(t)
	testDir := t.TempDir()
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	harness := `#!/usr/bin/env bash
set -euo pipefail
READY_TIMEOUT=5
PID_DIR="$TEST_DIR"
printf '%s\n' "$$" >"$PID_DIR/service-1.pid"
CURL_CALLS=0
curl() {
  CURL_CALLS=$((CURL_CALLS + 1))
  case "$CURL_CALLS" in
    1|3|4|5) return 0 ;;
    *) return 1 ;;
  esac
}
sleep() { :; }
log() { :; }
die() { printf 'die: %s\n' "$*" >&2; exit 90; }
` + extractBashFunction(t, script, "wait_url") + `
wait_url service-1 http://127.0.0.1:15001/readyz "" 3
test "$CURL_CALLS" -eq 5
`
	harnessPath := filepath.Join(testDir, "stable-readiness.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Env = append(os.Environ(), "TEST_DIR="+testDir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("service readiness did not wait for three consecutive successes: %v\n%s", err, output)
	}
}

func waitForChatLifecycleFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func readChatLifecycleIdentity(t *testing.T, path string) map[string]string {
	t.Helper()
	identity := make(map[string]string)
	for _, line := range strings.Split(readFile(t, path), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 2 {
			identity[fields[0]] = fields[1]
		}
	}
	return identity
}

func TestChatLifecycleShakeoutScriptIntegration(t *testing.T) {
	runTimingSensitiveShellScriptTestBeforeParallelPhase(t)
	root := repoRoot(t)
	runDir := filepath.Join(t.TempDir(), "chat-lifecycle-run")
	basePort := reserveChatLifecyclePortRange(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh",
		"--run-dir", runDir, "--base-port", strconv.Itoa(basePort),
		"--ready-timeout", "120", "--stop-after", "90")
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_BENCH_API_TOKEN", "WK_BENCH_WORKER_TOKEN"),
		"WK_BENCH_API_TOKEN=integration-bench-token", "WK_BENCH_WORKER_TOKEN=integration-worker-token")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("shakeout timed out: %v\n%s", ctx.Err(), output)
	}
	coordinatorLog, _ := os.ReadFile(filepath.Join(runDir, "logs", "coordinator.log"))
	if err != nil {
		if strings.Contains(string(coordinatorLog), "preflight_code=disk_free") ||
			strings.Contains(string(coordinatorLog), "preflight_code=disk_capacity") {
			t.Skipf("host filesystem cannot satisfy the real shakeout disk gate:\n%s", coordinatorLog)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
			t.Fatalf("shakeout failed: %v\n%s\n%s", err, output, coordinatorLog)
		}
	} else {
		t.Fatalf("coordinated operator stop returned success, want exit 130\n%s\n%s", output, coordinatorLog)
	}
	report, err := chatlifecycle.ReadReport(filepath.Join(runDir, "report", "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != chatlifecycle.VerdictOperatorStop || !report.Final ||
		report.Topology.LogicalSlotGroups != 12 || report.Topology.HashSlots != 256 ||
		report.Topology.SlotReplicas != 3 || report.Topology.ChannelReplicas != 3 {
		t.Fatalf("final report = %+v", report)
	}
	if report.Messages.FirstAttempts == 0 || report.Sync.SyncStarted == 0 {
		t.Fatalf("bounded run produced no real message/sync work: messages=%+v sync=%+v", report.Messages, report.Sync)
	}
	assertChatLifecyclePIDsExited(t, filepath.Join(runDir, "pids"))
}

func TestChatLifecycleShakeoutJoinSurvivesFirstSignal(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	harness := `#!/usr/bin/env bash
set -euo pipefail
WAIT_CHILD_STATUS=0
PPROF_PID=""
PPROF_EXIT_STATUS=0
signal_seen=0
trap 'signal_seen=1' TERM
` + extractBashFunction(t, script, "wait_child_uninterrupted") + "\n" +
		extractBashFunction(t, script, "join_threshold_pprof_capture") + `
sleep 0.4 &
PPROF_PID=$!
joined_pid="$PPROF_PID"
( sleep 0.05; kill -TERM "$$" ) &
signaler=$!
join_threshold_pprof_capture
wait "$signaler"
if kill -0 "$joined_pid" 2>/dev/null; then
  exit 91
fi
printf 'signal_seen=%s\npprof_pid=%s\npprof_exit_status=%s\n' \
  "$signal_seen" "$PPROF_PID" "$PPROF_EXIT_STATUS"
`
	harnessPath := filepath.Join(t.TempDir(), "join-signal.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", harnessPath).CombinedOutput()
	if err != nil || ctx.Err() != nil {
		t.Fatalf("signal-safe join failed: %v/%v\n%s", err, ctx.Err(), output)
	}
	for _, want := range []string{"signal_seen=1", "pprof_pid=\n", "pprof_exit_status=0"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("signal-safe join output missing %q:\n%s", want, output)
		}
	}
}

func TestChatLifecycleMeasuredHostOverlapStatusSupportsBashWithoutBASHPID(t *testing.T) {
	root := repoRoot(t)
	testDir := t.TempDir()
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	harness := `#!/usr/bin/env bash
set -euo pipefail
HOST_OVERLAP_STATUS_FILE="$TEST_DIR/measured-host-overlap.tsv"
unset BASHPID 2>/dev/null || true
` + extractBashFunction(t, script, "write_measured_host_overlap_status") + `
write_measured_host_overlap_status clear \
  2026-08-14T16:00:00Z 2026-08-14T16:00:01Z 1 0 none
test -s "$HOST_OVERLAP_STATUS_FILE"
test "$(awk -F '\t' '$1 == "status" { print $2 }' "$HOST_OVERLAP_STATUS_FILE")" = clear
`
	harnessPath := filepath.Join(testDir, "bash-without-bashpid.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Env = append(os.Environ(), "TEST_DIR="+testDir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("measured host-overlap status is not Bash 3.2 compatible: %v\n%s", err, output)
	}
}

func TestChatLifecycleGracefulStopTimeoutCapturesAuthenticatedSnapshotsAndJoinsCoordinator(t *testing.T) {
	root := repoRoot(t)
	testDir := t.TempDir()
	evidenceDir := filepath.Join(testDir, "evidence")
	if err := os.MkdirAll(filepath.Join(testDir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeCurl := `#!/usr/bin/env bash
set -euo pipefail
header=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --header) header_path="${2#@}"; header="$(<"$header_path")"; shift 2 ;;
    --connect-timeout|--max-time) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
[[ "$header" == 'Authorization: Bearer integration-worker-token' ]]
printf '%s\n' "$url" >>"$FAKE_CURL_LOG"
case "$url" in
  http://127.0.0.1:25051/v1/chat-lifecycle/snapshot) worker_id=0 ;;
  http://127.0.0.1:25052/v1/chat-lifecycle/snapshot) worker_id=1 ;;
  http://127.0.0.1:25053/v1/chat-lifecycle/snapshot) worker_id=2 ;;
  *) exit 90 ;;
esac
printf '{"run_id":"local-chat-lifecycle-shakeout","worker_id":%s,"phase":"stopping","sessions":{"online":17,"starting":2,"closing":3},"messages":{"sent":101,"send_acknowledged":97,"retry_attempts":4,"terminal":1},"correlation":{"pending_unfinished":4,"outstanding":3},"queues":{"work_current":6,"retry_current":4,"inflight_current":3,"transport_current":2}}\n' "$worker_id"
`
	fakeCurlPath := filepath.Join(testDir, "bin", "curl")
	if err := os.WriteFile(fakeCurlPath, []byte(fakeCurl), 0o700); err != nil {
		t.Fatal(err)
	}
	coordinator := `#!/usr/bin/env bash
set -euo pipefail
trap '' TERM
: >"$1"
while true; do sleep 0.05; done
`
	coordinatorPath := filepath.Join(testDir, "coordinator.sh")
	if err := os.WriteFile(coordinatorPath, []byte(coordinator), 0o700); err != nil {
		t.Fatal(err)
	}
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	harness := `#!/usr/bin/env bash
set -euo pipefail
WAIT_CHILD_STATUS=0
COORDINATOR_PID=""
COORDINATOR_STATUS=0
COORDINATOR_JOINED=0
CLEANUP_TIMEOUT=1
GRACEFUL_STOP_TIMEOUT=1
RUN_ID=local-chat-lifecycle-shakeout
WK_BENCH_WORKER_TOKEN=integration-worker-token
EVIDENCE_DIR="$TEST_DIR/evidence"
GRACEFUL_STOP_STATUS_FILE="$EVIDENCE_DIR/graceful-stop-status.json"
GRACEFUL_STOP_SNAPSHOT_DIR="$EVIDENCE_DIR/graceful-stop-timeout"
CUT_QUERY_FILE="$EVIDENCE_DIR/worker-cut-query.json"
NAMES=(coordinator)
PIDS=()
worker_port() { printf '%s' "$((25050 + $1))"; }
log() { printf '[harness] %s\n' "$*"; }
` + extractBashFunction(t, script, "wait_child_uninterrupted") + "\n" +
		extractBashFunction(t, script, "mark_recorded_stopped") + "\n" +
		extractBashFunction(t, script, "write_worker_authorization_header") + "\n" +
		extractBashFunction(t, script, "capture_graceful_stop_worker_snapshot") + "\n" +
		extractBashFunction(t, script, "capture_graceful_stop_timeout_evidence") + "\n" +
		extractBashFunction(t, script, "force_stop_and_join_coordinator") + "\n" +
		extractBashFunction(t, script, "force_stop_timed_out_coordinator") + `
mkdir -p "$EVIDENCE_DIR"
printf '{"terminal_cut_present":false}\n' >"$CUT_QUERY_FILE"
bash "$COORDINATOR_SCRIPT" "$TEST_DIR/coordinator-ready" &
COORDINATOR_PID=$!
PIDS=("$COORDINATOR_PID")
printf '%s\n' "$COORDINATOR_PID" >"$TEST_DIR/coordinator.pid"
while [[ ! -e "$TEST_DIR/coordinator-ready" ]]; do sleep 0.01; done
kill -TERM "$COORDINATOR_PID"
sleep 0.05
kill -0 "$COORDINATOR_PID"
set -x
capture_graceful_stop_timeout_evidence
force_stop_timed_out_coordinator
set +x
if kill -0 "$COORDINATOR_PID" 2>/dev/null; then
  exit 92
fi
printf 'coordinator_status=%s\ncoordinator_joined=%s\nrecorded_pid=%s\n' \
  "$COORDINATOR_STATUS" "$COORDINATOR_JOINED" "${PIDS[0]}"
`
	harnessPath := filepath.Join(testDir, "timeout-harness.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(testDir, "coordinator.pid")
	t.Cleanup(func() {
		encoded, err := os.ReadFile(pidPath)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(encoded)))
		if err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", harnessPath)
	command.Env = append(os.Environ(),
		"PATH="+filepath.Join(testDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TEST_DIR="+testDir,
		"COORDINATOR_SCRIPT="+coordinatorPath,
		"FAKE_CURL_LOG="+filepath.Join(testDir, "curl.log"),
	)
	output, err := command.CombinedOutput()
	if err != nil || ctx.Err() != nil {
		t.Fatalf("timeout capture/join failed: %v/%v\n%s", err, ctx.Err(), output)
	}
	if strings.Contains(string(output), "integration-worker-token") {
		t.Fatalf("worker token leaked under parent xtrace:\n%s", output)
	}
	for _, want := range []string{"coordinator_status=137", "coordinator_joined=1", "recorded_pid=\n"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("timeout capture/join output missing %q:\n%s", want, output)
		}
	}
	statusPath := filepath.Join(evidenceDir, "graceful-stop-status.json")
	status := readFile(t, statusPath)
	for _, want := range []string{
		`"status": "timeout"`, `"reason": "coordinator_graceful_stop_timeout"`,
		`"terminal_cut_present": false`, `"evidence_complete": true`,
		`"node": "node-1"`, `"node": "node-2"`, `"node": "node-3"`,
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("timeout status missing %q:\n%s", want, status)
		}
	}
	for node := 1; node <= 3; node++ {
		if _, err := os.Stat(filepath.Join(evidenceDir, "graceful-stop-timeout", fmt.Sprintf("node-%d.json", node))); err != nil {
			t.Fatalf("raw timeout snapshot %d missing: %v", node, err)
		}
	}
	requests := readFile(t, filepath.Join(testDir, "curl.log"))
	if strings.Count(requests, "/v1/chat-lifecycle/snapshot") != 3 {
		t.Fatalf("authenticated snapshot requests = %q", requests)
	}
}

func TestChatLifecycleUnmeasuredStopRequestRaceSealsTypedArtifactsWithoutLocalStep(t *testing.T) {
	root := repoRoot(t)
	testDir := t.TempDir()
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	harness := `#!/usr/bin/env bash
set -euo pipefail
RUN_DIR="$TEST_DIR/run"
EVIDENCE_DIR="$RUN_DIR/evidence"
GRACEFUL_STOP_STATUS_FILE="$EVIDENCE_DIR/graceful-stop-status.json"
HARNESS_FAILURE_REASON=coordinator_exited_before_stop_request
WRITER_PID=""
mkdir -p "$EVIDENCE_DIR"
summarize_storage_metrics() { printf 'storage\n' >"$RUN_DIR/storage_metrics_summary.tsv"; }
summarize_host_io() { printf 'host\n' >"$RUN_DIR/host_io_summary.tsv"; }
record_process_continuity() { printf 'name\talive\nwriter\ttrue\n' >"$EVIDENCE_DIR/process-continuity.tsv"; }
stop_process_metrics_collector() { :; }
join_threshold_pprof_capture() { :; }
write_threshold_pprof_status() { printf '{"status":"not_triggered"}\n' >"$EVIDENCE_DIR/threshold-pprof-status.json"; }
write_graceful_stop_status_if_absent() { [[ -s "$GRACEFUL_STOP_STATUS_FILE" ]]; }
stop_recorded_processes() {
  kill -TERM "$WRITER_PID" 2>/dev/null || true
  wait "$WRITER_PID" 2>/dev/null || true
  WRITER_PID=""
  printf 'joined\n' >>"$RUN_DIR/writer.log"
}
write_artifact_checksums() {
  [[ -z "$WRITER_PID" ]]
  {
    printf '%064d  %s\n' 0 "$GRACEFUL_STOP_STATUS_FILE"
    printf '%064d  %s\n' 0 "$RUN_DIR/writer.log"
  } >"$EVIDENCE_DIR/checksums.sha256"
}
log() { printf '[harness] %s\n' "$*"; }
` + extractBashFunction(t, script, "finalize_unmeasured_harness_failure") + `
printf '%s\n' '{"schema":"wukongim/chat-lifecycle-graceful-stop-status/v1","status":"request_failed","reason":"coordinator_exited_before_stop_request","observed_at_utc":"2026-08-13T00:00:00Z","timeout_seconds":0,"terminal_cut_present":false,"evidence_complete":true,"nodes":[]}' >"$GRACEFUL_STOP_STATUS_FILE"
printf 'writing\n' >"$RUN_DIR/writer.log"
sleep 30 </dev/null >/dev/null 2>&1 &
WRITER_PID=$!
printf '%s\n' "$WRITER_PID" >"$TEST_DIR/writer.pid"
finalize_unmeasured_harness_failure
`
	harnessPath := filepath.Join(testDir, "unmeasured-finalize.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", harnessPath)
	command.Env = append(os.Environ(), "TEST_DIR="+testDir)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 6 || ctx.Err() != nil {
		t.Fatalf("unmeasured finalizer exit = %v/%v, want 6\n%s", err, ctx.Err(), output)
	}
	runDir := filepath.Join(testDir, "run")
	if _, err := os.Stat(filepath.Join(runDir, "local-step.json")); !os.IsNotExist(err) {
		t.Fatalf("unmeasured finalizer fabricated local-step.json: %v", err)
	}
	manifest := readFile(t, filepath.Join(runDir, "evidence", "checksums.sha256"))
	if !strings.Contains(manifest, "graceful-stop-status.json") || !strings.Contains(manifest, "writer.log") {
		t.Fatalf("unmeasured finalizer did not seal typed status and stopped log:\n%s", manifest)
	}
	writerLog := readFile(t, filepath.Join(runDir, "writer.log"))
	if !strings.Contains(writerLog, "joined\n") {
		t.Fatalf("unmeasured finalizer sealed before writer joined:\n%s", writerLog)
	}
	encodedPID := readFile(t, filepath.Join(testDir, "writer.pid"))
	writerPID, err := strconv.Atoi(strings.TrimSpace(encodedPID))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(writerPID, 0); err == nil || !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("unmeasured finalizer left writer %d alive or indeterminate: %v", writerPID, err)
	}
}

func TestChatLifecycleEarlyTerminalCutClosesWarmupTimelineAtTerminalUTC(t *testing.T) {
	root := repoRoot(t)
	testDir := t.TempDir()
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))
	harness := `#!/usr/bin/env bash
set -euo pipefail
EVIDENCE_DIR="$TEST_DIR/evidence"
CUT_QUERY_FILE="$EVIDENCE_DIR/worker-cut-query.json"
TERMINAL_BOUNDARY_AT=""
DRAIN_BOUNDARY_RECORDED=0
qualification_seen=0
mkdir -p "$EVIDENCE_DIR"
printf 'observed_at_utc\tphase\tnode\tstatus\n' >"$EVIDENCE_DIR/timeline.tsv"
printf '%s\n' '{"terminal_cut_present":true,"latest_cut":{"cut":"terminal","at":"2026-08-15T03:37:19.168710Z"}}' >"$CUT_QUERY_FILE"
write_phase_state() { printf '%s\n' "$1" >"$EVIDENCE_DIR/phase"; }
` + extractBashFunction(t, script, "record_timeline_boundary_at") + `
` + extractBashFunction(t, script, "close_terminal_drain_boundary") + `
close_terminal_drain_boundary
cat "$EVIDENCE_DIR/timeline.tsv"
printf 'phase=%s\ndrain_recorded=%s\nterminal=%s\n' \
  "$(<"$EVIDENCE_DIR/phase")" "$DRAIN_BOUNDARY_RECORDED" "$TERMINAL_BOUNDARY_AT"
`
	harnessPath := filepath.Join(testDir, "early-terminal-timeline.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", harnessPath)
	command.Env = append(os.Environ(), "TEST_DIR="+testDir)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("early terminal timeline harness failed: %v\n%s", err, output)
	}
	want := "observed_at_utc\tphase\tnode\tstatus\n" +
		"2026-08-15T03:37:19.168710Z\twarmup_end\tboundary\tcomplete\n" +
		"2026-08-15T03:37:19.168710Z\tdrain_start\tboundary\tcomplete\n" +
		"2026-08-15T03:37:19.168710Z\tdrain_end\tboundary\tcomplete\n" +
		"2026-08-15T03:37:19.168710Z\tshutdown_start\tboundary\tcomplete\n" +
		"phase=shutdown\n" +
		"drain_recorded=1\n" +
		"terminal=2026-08-15T03:37:19.168710Z\n"
	if string(output) != want {
		t.Fatalf("early terminal timeline =\n%s\nwant:\n%s", output, want)
	}
}

func extractBashFunction(t *testing.T, script, name string) string {
	t.Helper()
	startToken := name + "() {\n"
	start := strings.Index(script, startToken)
	if start < 0 {
		t.Fatalf("bash function %s not found", name)
	}
	end := strings.Index(script[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("bash function %s is not closed", name)
	}
	return script[start : start+end+2]
}

func reserveChatLifecyclePortRange(t *testing.T) int {
	t.Helper()
	offsets := []int{1, 2, 3, 11, 12, 13, 21, 22, 23, 31, 32, 33, 41, 42, 43, 51, 52, 53, 60, 61, 62, 63}
	for base := 30000; base <= 60000; base += 100 {
		listeners := make([]net.Listener, 0, len(offsets))
		available := true
		for _, offset := range offsets {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+offset))
			if err != nil {
				available = false
				break
			}
			listeners = append(listeners, listener)
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		if available {
			return base
		}
	}
	t.Fatal("no free contiguous chat-lifecycle port range")
	return 0
}

func assertChatLifecyclePIDsExited(t *testing.T, pidDir string) {
	t.Helper()
	entries, err := os.ReadDir(pidDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 12 {
		t.Fatalf("PID files = %d, want 12", len(entries))
	}
	for _, entry := range entries {
		encoded, err := os.ReadFile(filepath.Join(pidDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, 0); err == nil || !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("recorded process %s (%d) is still alive or indeterminate: %v", entry.Name(), pid, err)
		}
	}
}
