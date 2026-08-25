package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleRepairMonitorStopsOnTypedFailureWithoutReleasingLease(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "repair-monitor.sh")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, fragment := range []string{
		"repair-capture", "repair-observe", "/v1/chat-lifecycle/status", "/v1/chat-lifecycle/snapshot",
		"stop_and_diagnose", "qualified", "sudo systemctl stop", "systemctl is-active --quiet",
		"wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service",
		"repair-observations.jsonl", "repair-decision.json", "repair-diagnosis.json",
		"terminal-cut", "status-${worker}.json", "snapshot-${worker}.json",
		"observation-failure", "strict_capture_rejected", "remote_fetch_failed",
		"strict_observe_rejected", "seal_abort observation_unavailable",
		"for capture_attempt in 1 2 3", "capture_succeeded",
		"WK_CHAT_REPAIR_POLL_SECONDS", "WK_CHAT_REPAIR_MAX_SECONDS",
		"operator-stop-requested.sh", "WK_CHAT_REPAIR_REQUEST_ID", "operator_stop",
		"query_stage_service_state", "for attempt in 1 2 3", "observation_unavailable",
		"request_qualified_stage_stop", "systemctl kill --kill-who=main --signal=SIGTERM",
		"fetch_qualified_report", "validate-rehearsal-report", "prove_qualified_stage_exit",
		"NRestarts", "MainPID", "qualification-service-state-last.txt",
		"WK_CHAT_REPAIR_RUN_START", "--run-start", "stop_stage_with_retries",
		"qualified-final.json", "qualified-result.json", "qualification-finalization.json",
		"stop_workers", "qualification_finalize_failed",
		`max_seconds="${WK_CHAT_REPAIR_MAX_SECONDS:-4500}"`,
		"max_seconds <= 260100",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("repair monitor missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"cloud-lease-release", "wkcloudlease", "rm -rf", "docker", "podman"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("repair monitor unexpectedly owns Lease or container mutation %q", forbidden)
		}
	}
}

func TestChatLifecycleStageStartRemovesStaleTerminalReports(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "start-local-stage.sh")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, fragment := range []string{
		`remote_report_dir="/var/lib/wukongim-cloud/reports/$report_dir"`,
		`'$remote_report_dir/final.json'`, `'$remote_report_dir/final.md'`,
		"systemctl start --no-block",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("stage starter missing stale-report fence %q", fragment)
		}
	}
}
