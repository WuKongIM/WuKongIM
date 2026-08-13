//go:build integration

package scripts_test

import (
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

func TestChatLifecycleShakeoutScriptIntegration(t *testing.T) {
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
