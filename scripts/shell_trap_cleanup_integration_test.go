//go:build integration

package scripts_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Failure inventory: a no-bridge return, a no-op close, or a successful close
// must not inherit the EXIT trap's failure and skip the remaining cleanup.
// A failed close still needs a warning and local cleanup; the original command
// status must survive every branch. These are real shell traps, not cloud calls.
func TestCloudSimulationAnalyzeExitTrapCompletesCleanupAndPreservesStatus(t *testing.T) {
	root := repoRoot(t)
	production := readFile(t, filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"))
	functions := ""
	for _, name := range []string{"dispatch_close_best_effort", "stop_analysis_bridge", "cleanup"} {
		functions += extractBashFunction(t, production, name) + "\n"
	}
	for _, status := range []int{17, 143} {
		for _, tc := range []struct {
			name, open, attempted string
			closeStatus           int
			wantClose             bool
		}{
			{name: "live_close", open: "true", wantClose: true},
			{name: "no_open_window", open: "false"},
			{name: "close_already_attempted", open: "true", attempted: "fixture-request"},
			{name: "close_failed", open: "true", closeStatus: 29, wantClose: true},
		} {
			t.Run(fmt.Sprintf("%s/status_%d", tc.name, status), func(t *testing.T) {
				dir := t.TempDir()
				owned := filepath.Join(dir, "owned")
				resultTemp := filepath.Join(dir, "result.tmp")
				trace := filepath.Join(dir, "cleanup.trace")
				if err := os.Mkdir(owned, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(resultTemp, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				harness := `set -euo pipefail
analysis_bridge_pid=""; analysis_bridge_url=""; analysis_token="fixture"
session_open="$TEST_SESSION_OPEN"; repository="fixture/project"; request_id="fixture-request"; run_id="fixture-run"
close_attempted_request_id="$TEST_ATTEMPTED"
analysis_temp="$TEST_OWNED"; result_temp="$TEST_RESULT_TEMP"
analysis_worktree_added=true; analysis_worktree="fixture-worktree"; REPO_ROOT="fixture-root"
dispatch_close_once() { printf 'close\n' >>"$TEST_TRACE"; return "$TEST_CLOSE_STATUS"; }
run_git_checkout_bounded() { printf 'worktree\n' >>"$TEST_TRACE"; }
wk_cleanup_local_cloud_tools() { printf 'tools\n' >>"$TEST_TRACE"; }
` + functions + "\ntrap cleanup EXIT\nexit \"$TEST_ORIGINAL_STATUS\"\n"
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "bash", "-c", harness)
				cmd.Env = append(os.Environ(), "LC_ALL=C", "TEST_SESSION_OPEN="+tc.open,
					"TEST_ATTEMPTED="+tc.attempted, fmt.Sprintf("TEST_CLOSE_STATUS=%d", tc.closeStatus),
					fmt.Sprintf("TEST_ORIGINAL_STATUS=%d", status), "TEST_OWNED="+owned,
					"TEST_RESULT_TEMP="+resultTemp, "TEST_TRACE="+trace)
				output, err := cmd.CombinedOutput()
				requireLocalBaselineExitCode(t, err, output, status)
				wantTrace := "worktree\ntools\n"
				if tc.wantClose {
					wantTrace = "close\n" + wantTrace
				}
				got, readErr := os.ReadFile(trace)
				if readErr != nil || string(got) != wantTrace {
					t.Fatalf("EXIT cleanup trace = %q, read error %v, want %q; output:\n%s", got, readErr, wantTrace, output)
				}
				for _, path := range []string{owned, resultTemp} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Errorf("EXIT cleanup retained %s: %v", path, err)
					}
				}
				if tc.closeStatus != 0 && !strings.Contains(string(output), "could not be confirmed (status 29)") {
					t.Errorf("failed close warning missing: %s", output)
				}
			})
		}
	}
}
