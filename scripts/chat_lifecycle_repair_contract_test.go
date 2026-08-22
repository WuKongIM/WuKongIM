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
		"repair-observations.jsonl", "repair-decision.json", "repair-diagnosis.json",
		"terminal-cut", "status-${worker}.json", "snapshot-${worker}.json",
		"WK_CHAT_REPAIR_POLL_SECONDS", "WK_CHAT_REPAIR_MAX_SECONDS",
		"operator-stop-requested.sh", "WK_CHAT_REPAIR_REQUEST_ID", "operator_stop",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("repair monitor missing %q", fragment)
		}
	}
	if strings.Contains(text, "systemctl kill --kill-who=main") {
		t.Fatal("repair monitor still treats a signal request as proof that the workload stopped")
	}
	for _, forbidden := range []string{"cloud-lease-release", "wkcloudlease", "rm -rf", "docker", "podman"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("repair monitor unexpectedly owns Lease or container mutation %q", forbidden)
		}
	}
}
