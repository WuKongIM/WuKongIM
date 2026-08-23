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
  *"systemctl stop"*) exit 0 ;;
  *"systemctl is-active --quiet"*) exit 1 ;;
  *"systemctl is-active"*) printf 'active\n'; exit 0 ;;
  *"journalctl"*) printf '%064d\n' 0; exit 0 ;;
esac
case "$command_line" in *19091*) worker=0 ;; *19092*) worker=1 ;; *19093*) worker=2 ;; *) exit 91 ;; esac
if [[ "$command_line" == *"/v1/chat-lifecycle/status"* ]]; then
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"unexpected":false,"traffic_ready":true}\n' "$worker"
elif [[ "$command_line" == *"/v1/chat-lifecycle/snapshot"* ]]; then
  printf '{"run_id":"repair-run","assignment_id":"repair-assignment","phase":"running","generation":1,"worker_id":%s,"worker_count":3,"sessions":{"target":3333,"online":3333,"traffic_ready":3333},"messages":{"sent":100,"send_acknowledged":90},"harness":{}}\n' "$worker"
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
		"WK_CHAT_REPAIR_OUTPUT_DIR="+outputDir,
		"WK_CHAT_REPAIR_SSH_CONFIG="+sshConfig,
		"WK_CHAT_REPAIR_REQUEST_ID=repair-monitor-test",
		"WK_CHAT_REPAIR_POLL_SECONDS=1",
		"WK_CHAT_REPAIR_MAX_SECONDS=4500",
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
  *"journalctl"*) printf 'bounded stage journal\n' ;;
  *"api/v1/targets"*) printf '{"status":"success","data":{"activeTargets":[]}}\n' ;;
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
	summary, err := os.ReadFile(filepath.Join(evidence, "summary.json"))
	if err != nil || !strings.Contains(string(summary), `"classification": "captured"`) {
		t.Fatalf("summary = %s, %v", summary, err)
	}
}

func writeRepairExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
