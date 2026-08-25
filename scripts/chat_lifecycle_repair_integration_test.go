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
)

func TestChatLifecycleRepairMonitorStopsAfterRealWorkerProgressStall(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	tool := filepath.Join(directory, "wkchatlifecycle")
	build := exec.Command("go", "build", "-trimpath", "-o", tool, "./cmd/wkchatlifecycle")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wkchatlifecycle: %v\n%s", err, output)
	}
	state := filepath.Join(directory, "state.json")
	startedAt := time.Now().UTC().Add(-time.Second).Truncate(time.Second).Format(time.RFC3339)
	begin := exec.Command(tool,
		"repair-begin", "--request-id", "repair-monitor-test", "--lease-id", "repair-lease-test",
		"--generation", "1", "--source-sha", strings.Repeat("a", 40),
		"--bundle-digest", "sha256:"+strings.Repeat("b", 64), "--started-at", startedAt,
		"--target-online", "9999", "--minimum-online-percent", "95", "--warmup-timeout", "5m",
		"--minimum-send-rate", "1", "--maximum-ack-backlog", "10000",
		"--stall-after", "1s", "--qualify-after", "1h")
	body, err := begin.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, body, 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(directory, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRepairExecutable(t, filepath.Join(fakeBin, "gh"), "#!/usr/bin/env bash\nprintf '%s\\n' '{\"artifacts\":[]}'\n")
	writeRepairExecutable(t, filepath.Join(fakeBin, "ssh"), `#!/usr/bin/env bash
command_line="$*"
case "$command_line" in
  *"systemctl stop"*) exit 1 ;;
  *"systemctl is-active --quiet"*) exit 1 ;;
  *"systemctl is-active"*) printf 'active\n'; exit 0 ;;
  *"journalctl"*) printf '%064d\n' 0; exit 0 ;;
esac
case "$command_line" in *19091*) worker=0 ;; *19092*) worker=1 ;; *19093*) worker=2 ;; *) exit 91 ;; esac
if [[ "$command_line" == *"/v1/chat-lifecycle/status"* ]]; then
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"unexpected":false,"traffic_ready":true}\n' "$worker"
elif [[ "$command_line" == *"/v1/chat-lifecycle/snapshot"* ]]; then
  uptime=$(( $(date +%s) * 1000000000 + worker + 1 ))
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"uptime":%s,"sessions":{"target":3333,"online":3333,"traffic_ready":3333},"messages":{"sent":100,"send_acknowledged":90},"harness":{}}\n' "$worker" "$uptime"
else
  exit 92
fi
`)
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host wukong-load\n  HostName 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(directory, "output")
	monitor := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "repair-monitor.sh"))
	monitor.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CHAT_REPAIR_TOOL="+tool,
		"WK_CHAT_REPAIR_STATE="+state,
		"WK_CHAT_REPAIR_RUN_START="+writeRepairRunStart(t, directory),
		"WK_CHAT_REPAIR_OUTPUT_DIR="+outputDir,
		"WK_CHAT_REPAIR_SSH_CONFIG="+sshConfig,
		"WK_CHAT_REPAIR_REQUEST_ID=repair-monitor-test",
		"WK_CHAT_REPAIR_POLL_SECONDS=1",
		"WK_CHAT_REPAIR_MAX_SECONDS=4500",
	)
	if output, err := monitor.CombinedOutput(); err == nil {
		t.Fatal("repair monitor unexpectedly qualified stalled traffic")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 20 {
		t.Fatalf("repair monitor exit = %v\n%s", err, output)
	}
	decisionBody, err := os.ReadFile(filepath.Join(outputDir, "repair-decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decision struct {
		Decision struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(decisionBody, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Action != "stop_and_diagnose" || decision.Decision.Reason != "message_progress_stalled" {
		t.Fatalf("decision = %+v", decision.Decision)
	}
	for worker := 1; worker <= 3; worker++ {
		for _, kind := range []string{"status", "snapshot"} {
			path := filepath.Join(outputDir, "terminal-cut", kind+"-"+string(rune('0'+worker))+".json")
			if info, statErr := os.Stat(path); statErr != nil || info.Size() == 0 {
				t.Fatalf("terminal %s for worker %d was not retained: %v", kind, worker, statErr)
			}
		}
	}
	diagnosisBody, err := os.ReadFile(filepath.Join(outputDir, "repair-diagnosis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(diagnosisBody), `"reason": "message_progress_stalled"`) {
		t.Fatalf("failed stop proof erased the original diagnosis: %s", diagnosisBody)
	}
}

func TestChatLifecycleRepairMonitorRetriesTransientRemoteAndInconsistentCuts(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	tool := filepath.Join(directory, "wkchatlifecycle")
	build := exec.Command("go", "build", "-trimpath", "-o", tool, "./cmd/wkchatlifecycle")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wkchatlifecycle: %v\n%s", err, output)
	}
	state := filepath.Join(directory, "state.json")
	startedAt := time.Now().UTC().Add(-time.Second).Truncate(time.Second).Format(time.RFC3339)
	begin := exec.Command(tool,
		"repair-begin", "--request-id", "repair-monitor-retry", "--lease-id", "repair-lease-retry",
		"--generation", "1", "--source-sha", strings.Repeat("a", 40),
		"--bundle-digest", "sha256:"+strings.Repeat("b", 64), "--started-at", startedAt,
		"--target-online", "9999", "--minimum-online-percent", "95", "--warmup-timeout", "5m",
		"--minimum-send-rate", "1", "--maximum-ack-backlog", "10000",
		"--stall-after", "1s", "--qualify-after", "1h")
	body, err := begin.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, body, 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(directory, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	callState := filepath.Join(directory, "snapshot-calls")
	failureMarker := filepath.Join(directory, "worker-2-snapshot-failed")
	writeRepairExecutable(t, filepath.Join(fakeBin, "ssh"), `#!/usr/bin/env bash
command_line="$*"
case "$command_line" in
  *"systemctl stop"*) exit 0 ;;
  *"systemctl is-active --quiet"*) exit 1 ;;
  *"systemctl is-active"*) printf 'active\n'; exit 0 ;;
  *"journalctl"*) printf '%064d\n' 0; exit 0 ;;
esac
case "$command_line" in *19091*) worker=0 ;; *19092*) worker=1 ;; *19093*) worker=2 ;; *) exit 91 ;; esac
if [[ "$command_line" == *"/v1/chat-lifecycle/status"* ]]; then
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"unexpected":false,"traffic_ready":true}\n' "$worker"
elif [[ "$command_line" == *"/v1/chat-lifecycle/snapshot"* ]]; then
  if [[ "$worker" == 1 && ! -f "$WK_TEST_FAILURE_MARKER" ]]; then
    : >"$WK_TEST_FAILURE_MARKER"
    exit 255
  fi
  calls=0
  [[ -f "$WK_TEST_SNAPSHOT_CALLS" ]] && calls="$(cat "$WK_TEST_SNAPSHOT_CALLS")"
  calls=$(( calls + 1 ))
  printf '%s\n' "$calls" >"$WK_TEST_SNAPSHOT_CALLS"
  phase=running
  [[ "$calls" == 3 ]] && phase=final
  uptime=$(( $(date +%s) * 1000000000 + worker + 1 ))
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"%s","generation":1,"worker_id":%s,"worker_count":3,"uptime":%s,"sessions":{"target":3333,"online":3333,"traffic_ready":3333},"messages":{"sent":100,"send_acknowledged":90},"harness":{}}\n' "$phase" "$worker" "$uptime"
else
  exit 92
fi
`)
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host wukong-load\n  HostName 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(directory, "output")
	monitor := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "repair-monitor.sh"))
	monitor.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CHAT_REPAIR_TOOL="+tool,
		"WK_CHAT_REPAIR_STATE="+state,
		"WK_CHAT_REPAIR_RUN_START="+writeRepairRunStart(t, directory),
		"WK_CHAT_REPAIR_OUTPUT_DIR="+outputDir,
		"WK_CHAT_REPAIR_SSH_CONFIG="+sshConfig,
		"WK_CHAT_REPAIR_REQUEST_ID=repair-monitor-retry",
		"WK_CHAT_REPAIR_POLL_SECONDS=1",
		"WK_CHAT_REPAIR_MAX_SECONDS=4500",
		"WK_TEST_SNAPSHOT_CALLS="+callState,
		"WK_TEST_FAILURE_MARKER="+failureMarker,
	)
	if output, err := monitor.CombinedOutput(); err == nil {
		t.Fatal("repair monitor unexpectedly qualified stalled traffic")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 10 {
		t.Fatalf("repair monitor exit = %v\n%s", err, output)
	}
	decisionBody, err := os.ReadFile(filepath.Join(outputDir, "repair-decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decisionBody), `"reason":"message_progress_stalled"`) {
		t.Fatalf("repair monitor did not recover from transient remote and inconsistent cuts: %s", decisionBody)
	}
	for worker := 1; worker <= 3; worker++ {
		for _, kind := range []string{"status", "snapshot"} {
			path := filepath.Join(outputDir, "observation-failure", kind+"-"+string(rune('0'+worker))+".json")
			if info, statErr := os.Stat(path); statErr != nil || info.Size() == 0 {
				t.Fatalf("failed %s cut for worker %d was not retained: %v", kind, worker, statErr)
			}
		}
	}
}

func TestChatLifecycleRepairMonitorSurvivesOneServiceProbeFailure(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	tool := filepath.Join(directory, "wkchatlifecycle")
	build := exec.Command("go", "build", "-trimpath", "-o", tool, "./cmd/wkchatlifecycle")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wkchatlifecycle: %v\n%s", err, output)
	}
	state := filepath.Join(directory, "state.json")
	startedAt := time.Now().UTC().Add(-time.Second).Truncate(time.Second).Format(time.RFC3339)
	begin := exec.Command(tool,
		"repair-begin", "--request-id", "repair-monitor-service-probe", "--lease-id", "repair-lease-service-probe",
		"--generation", "1", "--source-sha", strings.Repeat("a", 40),
		"--bundle-digest", "sha256:"+strings.Repeat("b", 64), "--started-at", startedAt,
		"--target-online", "9999", "--minimum-online-percent", "95", "--warmup-timeout", "5m",
		"--minimum-send-rate", "1", "--maximum-ack-backlog", "10000",
		"--stall-after", "1s", "--qualify-after", "1h")
	body, err := begin.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, body, 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(directory, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	serviceCalls := filepath.Join(directory, "service-calls")
	writeRepairExecutable(t, filepath.Join(fakeBin, "ssh"), `#!/usr/bin/env bash
command_line="$*"
case "$command_line" in
  *"systemctl stop"*) exit 0 ;;
  *"systemctl is-active --quiet"*) exit 1 ;;
  *"systemctl is-active"*)
    calls=0
    [[ -f "$WK_TEST_SERVICE_CALLS" ]] && calls="$(cat "$WK_TEST_SERVICE_CALLS")"
    calls=$(( calls + 1 ))
    printf '%s\n' "$calls" >"$WK_TEST_SERVICE_CALLS"
    [[ "$calls" == 1 ]] && exit 255
    printf 'active\n'
    exit 0
    ;;
  *"journalctl"*) printf '%064d\n' 0; exit 0 ;;
esac
case "$command_line" in *19091*) worker=0 ;; *19092*) worker=1 ;; *19093*) worker=2 ;; *) exit 91 ;; esac
if [[ "$command_line" == *"/v1/chat-lifecycle/status"* ]]; then
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"unexpected":false,"traffic_ready":true}\n' "$worker"
elif [[ "$command_line" == *"/v1/chat-lifecycle/snapshot"* ]]; then
  uptime=$(( $(date +%s) * 1000000000 + worker + 1 ))
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"uptime":%s,"sessions":{"target":3333,"online":3333,"traffic_ready":3333},"messages":{"sent":100,"send_acknowledged":90},"harness":{}}\n' "$worker" "$uptime"
else
  exit 92
fi
`)
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host wukong-load\n  HostName 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(directory, "output")
	monitor := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "repair-monitor.sh"))
	monitor.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CHAT_REPAIR_TOOL="+tool,
		"WK_CHAT_REPAIR_STATE="+state,
		"WK_CHAT_REPAIR_RUN_START="+writeRepairRunStart(t, directory),
		"WK_CHAT_REPAIR_OUTPUT_DIR="+outputDir,
		"WK_CHAT_REPAIR_SSH_CONFIG="+sshConfig,
		"WK_CHAT_REPAIR_REQUEST_ID=repair-monitor-service-probe",
		"WK_CHAT_REPAIR_POLL_SECONDS=1",
		"WK_CHAT_REPAIR_MAX_SECONDS=4500",
		"WK_TEST_SERVICE_CALLS="+serviceCalls,
	)
	if output, err := monitor.CombinedOutput(); err == nil {
		t.Fatal("repair monitor unexpectedly qualified stalled traffic")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 10 {
		t.Fatalf("repair monitor exit = %v\n%s", err, output)
	}
	decisionBody, err := os.ReadFile(filepath.Join(outputDir, "repair-decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decisionBody), `"reason":"message_progress_stalled"`) {
		t.Fatalf("one failed service probe was misclassified as terminal: %s", decisionBody)
	}
	callsBody, err := os.ReadFile(serviceCalls)
	if err != nil {
		t.Fatal(err)
	}
	if calls := strings.TrimSpace(string(callsBody)); calls == "1" {
		t.Fatalf("service probe was not retried: %s", calls)
	}
}

func TestChatLifecycleRepairMonitorFinalizesQualifiedStageBeforeStoppingWorkers(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	state := filepath.Join(directory, "state.json")
	if err := os.WriteFile(state, []byte(`{"schema":"test-state"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(directory, "calls.log")
	tool := filepath.Join(directory, "wkchatlifecycle")
	writeRepairExecutable(t, tool, `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  repair-capture)
    printf '{"schema":"test-observation"}\n'
    ;;
  repair-observe)
    printf '{"state":{"schema":"test-qualified"},"decision":{"action":"qualified","reason":"none","observed_at":"2026-08-26T00:00:00Z"}}\n'
    ;;
  validate-rehearsal-report)
    printf 'validate-report\n' >>"$WK_TEST_CALL_LOG"
    report=''
    run_start=''
    shift
    while (( $# > 0 )); do
      case "$1" in
        --report) report="$2"; shift 2 ;;
        --run-start) run_start="$2"; shift 2 ;;
        *) shift ;;
      esac
    done
    [[ -s "$report" && -s "$run_start" ]]
    printf '{"schema":"wukongim.chat_lifecycle.rehearsal_result/v1","stage":"rehearsal","outcome":"operator_stop","cause":"operator_requested","end":"2026-08-26T00:00:01Z"}\n'
    ;;
  *) exit 91 ;;
esac
`)

	fakeBin := filepath.Join(directory, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	signalMarker := filepath.Join(directory, "stage-signaled")
	serviceShowCalls := filepath.Join(directory, "service-show-calls")
	writeRepairExecutable(t, filepath.Join(fakeBin, "ssh"), `#!/usr/bin/env bash
set -euo pipefail
command_line="$*"
case "$command_line" in
  *"systemctl kill --kill-who=main --signal=SIGTERM"*)
    printf 'signal-stage\n' >>"$WK_TEST_CALL_LOG"
    : >"$WK_TEST_SIGNAL_MARKER"
    ;;
  *"/var/lib/wukongim-cloud/reports/rehearsal/final.json"*)
    printf 'fetch-report\n' >>"$WK_TEST_CALL_LOG"
    printf '{"schema":"test-final-report"}\n'
    ;;
  *"systemctl show"*)
    printf 'prove-stage-exit\n' >>"$WK_TEST_CALL_LOG"
    calls=0
    [[ -f "$WK_TEST_SERVICE_SHOW_CALLS" ]] && calls="$(cat "$WK_TEST_SERVICE_SHOW_CALLS")"
    calls=$(( calls + 1 ))
    printf '%s\n' "$calls" >"$WK_TEST_SERVICE_SHOW_CALLS"
    if [[ "$calls" == 1 ]]; then
      printf 'ActiveState=deactivating\nSubState=stop-sigterm\nResult=success\nExecMainCode=0\nExecMainStatus=0\n'
    else
      printf 'ActiveState=inactive\nSubState=dead\nResult=success\nExecMainCode=1\nExecMainStatus=130\n'
    fi
    ;;
  *"systemctl stop wkbench-worker@1.service"*)
    printf 'stop-workers\n' >>"$WK_TEST_CALL_LOG"
    ;;
  *"systemctl is-active --quiet"*) exit 1 ;;
  *"systemctl is-active"*)
    if [[ -f "$WK_TEST_SIGNAL_MARKER" ]]; then
      printf 'deactivating\n'
    else
      printf 'active\n'
    fi
    ;;
  *"journalctl"*) printf '%064d\n' 0 ;;
  *":19091/v1/chat-lifecycle/"*|*":19092/v1/chat-lifecycle/"*|*":19093/v1/chat-lifecycle/"*)
    printf '{}\n'
    ;;
  *) exit 92 ;;
esac
`)
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host wukong-load\n  HostName 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(directory, "output")
	operatorStop := filepath.Join(directory, "operator-stop")
	monitor := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "repair-monitor.sh"))
	monitor.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CHAT_REPAIR_TOOL="+tool,
		"WK_CHAT_REPAIR_STATE="+state,
		"WK_CHAT_REPAIR_RUN_START="+writeRepairRunStart(t, directory),
		"WK_CHAT_REPAIR_OUTPUT_DIR="+outputDir,
		"WK_CHAT_REPAIR_SSH_CONFIG="+sshConfig,
		"WK_CHAT_REPAIR_REQUEST_ID=repair-monitor-qualified",
		"WK_CHAT_REPAIR_OPERATOR_STOP_FILE="+operatorStop,
		"WK_CHAT_REPAIR_POLL_SECONDS=1",
		"WK_CHAT_REPAIR_MAX_SECONDS=4500",
		"WK_CHAT_REPAIR_QUALIFICATION_FINALIZE_SECONDS=4",
		"WK_TEST_CALL_LOG="+callLog,
		"WK_TEST_SIGNAL_MARKER="+signalMarker,
		"WK_TEST_SERVICE_SHOW_CALLS="+serviceShowCalls,
	)
	if output, err := monitor.CombinedOutput(); err != nil {
		t.Fatalf("qualified repair monitor failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(calls), "signal-stage\nfetch-report\nvalidate-report\nprove-stage-exit\nprove-stage-exit\nstop-workers\n"; got != want {
		t.Fatalf("qualification finalization order = %q, want %q", got, want)
	}
	for _, name := range []string{"qualified-final.json", "qualified-result.json", "qualification-finalization.json"} {
		if info, statErr := os.Stat(filepath.Join(outputDir, name)); statErr != nil || info.Size() == 0 {
			t.Fatalf("qualified finalization artifact %s missing: %v", name, statErr)
		}
	}
}

func TestChatLifecycleDirectLabPreservesTypedReasonWhenStopCannotBeProven(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260825T010203Z-11223344"
	requestDirectory := filepath.Join(directory, requestID)
	generationDirectory := filepath.Join(requestDirectory, "generations", "1")
	if err := os.MkdirAll(generationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(requestDirectory, "state.json"):            `{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"` + requestID + `","lease_id":"lease-direct","source_sha":"` + strings.Repeat("a", 40) + `","bundle_digest":"sha256:` + strings.Repeat("b", 64) + `","state":"deployed","generation":1}`,
		filepath.Join(requestDirectory, "receipt.json"):          `{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{"lease_id":"lease-direct","request_id":"` + requestID + `","state":"active","expires_at":"2099-01-01T00:00:00Z"}}`,
		filepath.Join(requestDirectory, "run-policy.json"):       `{"schema":"wukongim.chat_lifecycle.direct_lab_run_policy/v1","duration":"60m","duration_seconds":3600,"max_duration_seconds":4500,"qualification_reserve_seconds":900,"lease_duration_seconds":21600}`,
		filepath.Join(requestDirectory, "deployment-ssh-config"): "Host wukong-load\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stageStarter := filepath.Join(directory, "stage-starter")
	writeRepairExecutable(t, stageStarter, `#!/usr/bin/env bash
set -euo pipefail
printf '{"schema":"wukongim.chat_lifecycle.run_start/v1","stage":"rehearsal","started_at":"2026-08-25T01:02:03Z","expected_end_at":"2026-08-25T02:17:03Z","run_hash":"sha256:%064d","assignment_hash":"sha256:%064d","generation":1}\n' 1 2 >"$WK_CHAT_LAB_RUN_START_OUTPUT"
`)
	chatTool := filepath.Join(directory, "wkchatlifecycle")
	writeRepairExecutable(t, chatTool, `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == repair-begin ]]
printf '{"schema":"wukongim.chat_lifecycle.repair_state/v2"}\n'
`)
	monitor := filepath.Join(directory, "repair-monitor")
	writeRepairExecutable(t, monitor, `#!/usr/bin/env bash
set -euo pipefail
[[ -s "$WK_CHAT_REPAIR_RUN_START" ]]
printf '{"schema":"wukongim.chat_lifecycle.repair_step/v1","decision":{"action":"stop_and_diagnose","reason":"observation_unavailable","observed_at":"2026-08-25T01:02:10Z"}}\n' >"$WK_CHAT_REPAIR_OUTPUT_DIR/repair-decision.json"
exit 20
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "run", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CHAT_TOOL="+chatTool,
		"WK_CHAT_LAB_STAGE_STARTER="+stageStarter,
		"WK_CHAT_LAB_REPAIR_MONITOR="+monitor,
	)
	output, err := command.CombinedOutput()
	if err == nil || command.ProcessState.ExitCode() != 20 {
		t.Fatalf("run exit = %v, code=%d\n%s", err, command.ProcessState.ExitCode(), output)
	}
	state, readErr := os.ReadFile(filepath.Join(requestDirectory, "state.json"))
	if readErr != nil || !strings.Contains(string(state), `"state": "diagnosis_ready"`) ||
		!strings.Contains(string(state), `"reason": "observation_unavailable"`) {
		t.Fatalf("typed monitor reason was lost: state=%s, err=%v", state, readErr)
	}
}

func TestChatLifecycleDirectLabRefusesRunThatOutlivesLease(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260825T020304Z-22334455"
	requestDirectory := filepath.Join(directory, requestID)
	generationDirectory := filepath.Join(requestDirectory, "generations", "1")
	if err := os.MkdirAll(generationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	for path, body := range map[string]string{
		filepath.Join(requestDirectory, "state.json"):            `{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"` + requestID + `","lease_id":"lease-direct","source_sha":"` + strings.Repeat("a", 40) + `","bundle_digest":"sha256:` + strings.Repeat("b", 64) + `","state":"deployed","generation":1}`,
		filepath.Join(requestDirectory, "receipt.json"):          `{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{"lease_id":"lease-direct","request_id":"` + requestID + `","state":"active","expires_at":"` + expiresAt + `"}}`,
		filepath.Join(requestDirectory, "run-policy.json"):       `{"schema":"wukongim.chat_lifecycle.direct_lab_run_policy/v1","duration":"60m","duration_seconds":3600,"max_duration_seconds":4500,"qualification_reserve_seconds":900,"lease_duration_seconds":21600}`,
		filepath.Join(requestDirectory, "deployment-ssh-config"): "Host wukong-load\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	starterMarker := filepath.Join(directory, "stage-starter-called")
	stageStarter := filepath.Join(directory, "stage-starter")
	writeRepairExecutable(t, stageStarter, `#!/usr/bin/env bash
set -euo pipefail
: >"$WK_TEST_STAGE_STARTER_MARKER"
exit 99
`)
	chatTool := filepath.Join(directory, "wkchatlifecycle")
	writeRepairExecutable(t, chatTool, "#!/usr/bin/env bash\nexit 99\n")
	monitor := filepath.Join(directory, "repair-monitor")
	writeRepairExecutable(t, monitor, "#!/usr/bin/env bash\nexit 99\n")

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "run", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CHAT_TOOL="+chatTool,
		"WK_CHAT_LAB_STAGE_STARTER="+stageStarter,
		"WK_CHAT_LAB_REPAIR_MONITOR="+monitor,
		"WK_TEST_STAGE_STARTER_MARKER="+starterMarker,
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "lease remaining lifetime is shorter than the bounded workload") {
		t.Fatalf("short lease run result = %v\n%s", err, output)
	}
	if _, statErr := os.Stat(starterMarker); !os.IsNotExist(statErr) {
		t.Fatalf("short lease contacted the stage starter: %v", statErr)
	}
}

func TestChatLifecycleDiagnosisCollectorUsesCurrentWorkerPorts(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	fakeBin := filepath.Join(directory, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRepairExecutable(t, filepath.Join(fakeBin, "timeout"), "#!/usr/bin/env bash\nshift\nexec \"$@\"\n")
	writeRepairExecutable(t, filepath.Join(fakeBin, "ssh"), `#!/usr/bin/env bash
printf '%s\n' "$*" >>"$WK_TEST_CALL_LOG"
case "$*" in
  *"systemctl show"*) printf 'Id=wukongim.service\nActiveState=active\n' ;;
  *"journalctl"*)
    if [[ "$*" == *"tail -c 250000"* ]]; then
      printf 'bounded stage journal tail\n'
    else
      head -c 300000 /dev/zero
    fi
    ;;
  *"api/v1/targets"*) printf '{"status":"success","data":{"activeTargets":[]}}\n' ;;
  *"reports/rehearsal/final.json"*)
    [[ "${WK_TEST_REPORTS_ABSENT:-false}" == true ]] && exit 3
    printf '{"schema":"wkbench.chat_lifecycle.report/test","verdict":{"outcome":"product_failure","cause":"hot_latency"}}\n'
    ;;
  *"reports/rehearsal/diagnostic-status.json"*)
    [[ "${WK_TEST_REPORTS_ABSENT:-false}" == true ]] && exit 3
    printf '{"schema":"wukongim/chat-lifecycle-diagnostic-status/v1"}\n'
    ;;
  *"/v1/chat-lifecycle/status"*) printf '{"phase":"final"}\n' ;;
  *"/v1/chat-lifecycle/snapshot"*) printf '{"messages":{"sent":1}}\n' ;;
  *) exit 91 ;;
esac
`)
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host wukong-load\n  HostName 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(directory, "evidence")
	if err := os.Mkdir(evidence, 0o700); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(directory, "calls.log")
	collector := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "collect-local-diagnosis.sh"))
	collector.Dir = root
	collector.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CHAT_LAB_DIAGNOSIS_DIR="+evidence,
		"WK_CHAT_LAB_SSH_CONFIG="+sshConfig,
		"WK_CHAT_LAB_REQUEST_ID=chat-20260823T000000Z-0123abcd",
		"WK_TEST_CALL_LOG="+callLog,
	)
	if output, err := collector.CombinedOutput(); err != nil {
		t.Fatalf("collector failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	callText := string(calls)
	for _, port := range []string{"19091", "19092", "19093"} {
		if count := strings.Count(callText, ":"+port+"/v1/chat-lifecycle/"); count != 2 {
			t.Fatalf("worker port %s call count = %d, want status and snapshot; calls:\n%s", port, count, callText)
		}
	}
	if strings.Contains(callText, ":2505") {
		t.Fatalf("collector retained legacy worker ports:\n%s", callText)
	}
	for _, fragment := range []string{
		"journalctl -u wkbench-rehearsal.service --no-pager -n 2000 -o short-iso | tail -c 250000",
		"/var/lib/wukongim-cloud/reports/rehearsal/final.json",
		"/var/lib/wukongim-cloud/reports/rehearsal/diagnostic-status.json",
	} {
		if !strings.Contains(callText, fragment) {
			t.Fatalf("collector did not request bounded terminal evidence %q; calls:\n%s", fragment, callText)
		}
	}
	for _, name := range []string{"final-report.json", "diagnostic-status.json", "stage-journal.txt"} {
		info, statErr := os.Stat(filepath.Join(evidence, name))
		if statErr != nil || info.Size() == 0 {
			t.Fatalf("terminal evidence %s missing or empty: size=%v err=%v", name, info, statErr)
		}
	}
	summary, err := os.ReadFile(filepath.Join(evidence, "summary.json"))
	if err != nil || !strings.Contains(string(summary), `"classification": "captured"`) {
		t.Fatalf("summary = %s, %v", summary, err)
	}

	absentEvidence := filepath.Join(directory, "evidence-without-terminal-reports")
	if err := os.Mkdir(absentEvidence, 0o700); err != nil {
		t.Fatal(err)
	}
	absentCollector := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "collect-local-diagnosis.sh"))
	absentCollector.Dir = root
	absentCollector.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CHAT_LAB_DIAGNOSIS_DIR="+absentEvidence,
		"WK_CHAT_LAB_SSH_CONFIG="+sshConfig,
		"WK_CHAT_LAB_REQUEST_ID=chat-20260823T000000Z-0123abcd",
		"WK_TEST_CALL_LOG="+callLog,
		"WK_TEST_REPORTS_ABSENT=true",
	)
	if output, err := absentCollector.CombinedOutput(); err != nil {
		t.Fatalf("collector rejected an expected absent in-progress report: %v\n%s", err, output)
	}
	absentSummary, err := os.ReadFile(filepath.Join(absentEvidence, "summary.json"))
	if err != nil || !strings.Contains(string(absentSummary), `"classification": "captured"`) {
		t.Fatalf("absent-report summary = %s, %v", absentSummary, err)
	}
	for _, name := range []string{"final-report.json", "diagnostic-status.json"} {
		if _, err := os.Stat(filepath.Join(absentEvidence, name)); !os.IsNotExist(err) {
			t.Fatalf("absent optional report %s was fabricated: %v", name, err)
		}
	}
}

func writeRepairExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeRepairRunStart(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "run-start.json")
	body := `{"schema":"wukongim.chat_lifecycle.run_start/v1","stage":"rehearsal","started_at":"2026-08-26T00:00:00Z","expected_end_at":"2026-08-26T04:15:00Z","run_hash":"sha256:` + strings.Repeat("a", 64) + `","assignment_hash":"sha256:` + strings.Repeat("b", 64) + `","generation":1}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
