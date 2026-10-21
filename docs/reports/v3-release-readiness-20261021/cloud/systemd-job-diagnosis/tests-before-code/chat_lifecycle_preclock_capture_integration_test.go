//go:build integration

package scripts_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runPreClockFixture owns the shell and its stream collector process group,
// including failures before the shell can join the collector itself.
func runPreClockFixture(command *exec.Cmd) ([]byte, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = time.Second
	out, err := command.CombinedOutput()
	if command.Process != nil {
		cleanupErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if cleanupErr != nil && !errors.Is(cleanupErr, syscall.ESRCH) {
			err = errors.Join(err, cleanupErr)
		}
	}
	return out, err
}

func preClockFunction(t *testing.T, source, name string) string {
	t.Helper()
	start := strings.Index(source, name+"() {\n")
	if start < 0 {
		t.Fatalf("actual function %s missing", name)
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("function %s boundary missing", name)
	}
	return source[start : start+end+3]
}

func TestChatLifecyclePreClockCaptureRetainsExactStatusAndRedactsStreams(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	function := preClockFunction(t, source, "capture_pre_clock_step")
	for _, status := range []string{"0", "17", "255"} {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			ssh := filepath.Join(dir, "ssh")
			if err := os.WriteFile(ssh, []byte("#!/bin/bash\nprintf 'ActiveState=failed\\nJob=0\\n'\nprintf 'secret-canary-do-not-publish' >&2\nexit "+status+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body := "set -euo pipefail\n" + function + "\nstatus=0\ncapture_pre_clock_step state ssh || status=$?\nprintf 'status=%s\\n' \"$status\"\nprintf 'cleanup\\n'\n"
			cmd := exec.CommandContext(ctx, "bash", "-c", body)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "pre_clock_directory="+dir)
			out, err := runPreClockFixture(cmd)
			if err != nil {
				t.Fatalf("capture shell failed: %v: %s", err, out)
			}
			if string(out) != "status="+status+"\ncleanup\n" {
				t.Fatalf("original status/cleanup or redaction changed: %q", out)
			}
			var metadata struct {
				Bytes int    `json:"bytes"`
				SHA   string `json:"sha256"`
			}
			b, err := os.ReadFile(filepath.Join(dir, "state-stderr.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Bytes != len("secret-canary-do-not-publish") || len(metadata.SHA) != 64 {
				t.Fatalf("wrong stderr metadata: %s", b)
			}
		})
	}
}

func TestChatLifecycleStagePreflightRejectsBeforeAnyBuildKeyOrCloudAction(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	spy := `#!/bin/bash
set -eu
printf '%s %s\n' "${0##*/}" "$*" >>"$SPY_LOG"
if [[ "${0##*/}" == gh && "$*" == *"--method GET /search/issues"* ]]; then
  printf '{"total_count":0,"incomplete_results":false,"items":[]}\n'
  exit 0
fi
exit 91
`
	for _, name := range []string{"gh", "ssh-keygen", "go", "wktool"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(spy), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts/chat-lifecycle/stage-orchestrate.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "SPY_LOG="+filepath.Join(dir, "calls"), "GH_TOKEN=secret-canary-do-not-publish", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_REQUEST_ID=preclock-fixture", "WK_CHAT_SOURCE_SHA="+strings.Repeat("a", 40), "WK_CHAT_OPERATOR=tangtaoit", "WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY=fixture-public-key", "WK_CHAT_WRAPPING_PUBLIC_KEY=fixture-wrapping-public-key", "WK_CHAT_TOOL="+filepath.Join(dir, "wktool"), "WK_CHAT_TEMPLATE="+filepath.Join(root, "configs/cloud/chat-lifecycle/rehearsal-v1.json"), "WK_CHAT_OUTPUT_DIR="+filepath.Join(dir, "output"), "WK_CHAT_WORK_DIR="+filepath.Join(dir, "work"))
	out, err := runPreClockFixture(cmd)
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || len(out) != 0 {
		t.Fatalf("expected exact preflight rejection exit1, got %v / %q", err, out)
	}
	var receipt struct{ Status, Reason, Schema string }
	b, err := os.ReadFile(filepath.Join(dir, "output/issue-preflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "rejected" || receipt.Reason != "issue_missing" || receipt.Schema != "wukongim.chat_lifecycle.issue_preflight/v1" {
		t.Fatalf("wrong rejection receipt: %s", b)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "\n") != 1 || !strings.Contains(string(calls), "--method GET /search/issues") {
		t.Fatalf("preflight allowed another action: %s", calls)
	}
}

func TestChatLifecyclePreClockDiagnosticWriteFailureCannotReplaceStatusOrCleanup(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	for _, missingDir := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_directory_%t", missingDir), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "diagnostic")
			if !missingDir {
				for _, name := range []string{"state-stderr.json", "state-stdout.json", "diagnostic.json"} {
					if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			body := "set -euo pipefail\n" + preClockFunction(t, source, "capture_pre_clock_step") + "\n" + preClockFunction(t, source, "record_pre_clock_diagnostic") + `
status=0
capture_pre_clock_step state bash -c 'printf output; printf secret-canary >&2; exit 17' || status=$?
record_pre_clock_diagnostic stage_state_read_failed
printf 'status=%s\ncleanup\n' "$status"
`
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", body)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "pre_clock_directory="+dir, "WK_CHAT_REQUEST_ID=fixture", "WK_CHAT_SOURCE_SHA="+strings.Repeat("a", 40), "deployment_control_sha="+strings.Repeat("b", 40), "bundle_digest=sha256:"+strings.Repeat("c", 64), "chat_stage=rehearsal", "deployment_generation=1")
			out, err := runPreClockFixture(cmd)
			if err != nil || string(out) != "status=17\ncleanup\n" {
				t.Fatalf("diagnostic failure escaped original status/cleanup: %v / %q", err, out)
			}
			if _, err := os.Stat(filepath.Join(dir, "state-stderr.fifo")); !os.IsNotExist(err) {
				t.Fatalf("owned diagnostic pipe not removed: %v", err)
			}
		})
	}
}

func TestChatLifecyclePreClockActualReadinessBranchesFreezeBeforeKill(t *testing.T) {
	root := repoRoot(t)
	if len(originalActiveEmptyJob) != 104 || fmt.Sprintf("%x", sha256.Sum256([]byte(originalActiveEmptyJob))) != originalActiveEmptyJobSHA {
		t.Fatal("original pre-kill public state fixture changed")
	}
	source := readFile(t, filepath.Join(root, "scripts/chat-lifecycle/stage-orchestrate.sh"))
	start := strings.Index(source, "    stage_terminal_code=''\n")
	if start < 0 {
		t.Fatal("actual pre-clock loop missing")
	}
	end := strings.Index(source[start:], "    if [[ -n \"$stage_terminal_code\" ]]")
	if end < 0 {
		t.Fatal("actual pre-clock loop missing")
	}
	loop := source[start : start+end]
	functions := ""
	for _, name := range []string{"capture_pre_clock_step", "record_pre_clock_diagnostic", "read_pre_clock_terminal_code"} {
		functions += preClockFunction(t, source, name) + "\n"
	}
	// Darwin's regex implementation rejects the unchanged {1,512} bound.
	// Keep that baseline behavior visible instead of replacing production parsing
	// in the fixture; Linux still exercises the complete summary command path.
	probe := exec.Command("bash", "-c", `journal_cursor='s=abc;i=1'; [[ "$journal_cursor" =~ ^[A-Za-z0-9_=\;:.-]{1,512}$ ]]`)
	cursorSupported := probe.Run() == nil
	for _, tc := range []struct {
		name, branch, state, summary string
		startExit, stateExit         int
	}{
		{"start failed", "stage_start_failed", "", "", 17, 0},
		{"SSH read failed", "stage_state_read_failed", "", "", 0, 255},
		{"classifier rejected", "stage_state_parse_failed", "ActiveState=secret-canary-do-not-publish\nJob=0\n", "", 0, 0},
		{"terminal preflight", "stage_terminal_before_run_start", "ActiveState=failed\nJob=0\n", "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=preflight preflight_code=target_config report=unavailable", 0, 0},
		{"terminal runtime", "stage_terminal_before_run_start", "ActiveState=failed\nJob=0\n", "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=runtime worker_runtime_code=engine_cpu_saturated preflight_code= report=unavailable", 0, 0},
		{"terminal unknown summary", "stage_terminal_before_run_start", "ActiveState=failed\nJob=0\n", "secret-canary-do-not-publish", 0, 0},
		{"actual deadline", "stage_readiness_deadline_elapsed", "ActiveState=active\nJob=0\n", "", 0, 0},
		{"original active empty job", "stage_readiness_deadline_elapsed", originalActiveEmptyJob, "", 0, 0},
		{"inactive empty job", "stage_terminal_before_run_start", "ActiveState=inactive\nJob=\n", "", 0, 0},
		{"missing job", "stage_state_parse_failed", "ActiveState=active\n", "", 0, 0},
		{"duplicate empty job", "stage_state_parse_failed", "ActiveState=active\nJob=\nJob=0\n", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			ssh := `#!/bin/bash
set -eu
case "$*" in
 *"reset-failed"*) printf 'start-secret-canary' >&2; exit "$START_EXIT";;
 *"sudo test -f"*) exit 1;;
 *"systemctl show"*) printf '%s' "$UNIT_OUTPUT"; exit "$STATE_EXIT";;
 *"journalctl"*) printf '%s' "$SUMMARY_OUTPUT"; exit 0;;
 *"systemctl kill"*) test -s "$pre_clock_directory/diagnostic.json"; cp "$pre_clock_directory/diagnostic.json" "$pre_clock_directory/before-kill.json"; exit 0;;
 *) exit 93;;
esac
`
			for name, body := range map[string]string{"ssh": ssh, "timeout": "#!/bin/bash\nshift\nexec \"$@\"\n", "date": "#!/bin/bash\nif [[ -e \"$CLOCK_MARKER\" ]]; then printf '1002\\n'; else : >\"$CLOCK_MARKER\"; printf '1000\\n'; fi\n"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			body := "set -euo pipefail\n" + functions + "\ncheck_operator_stop() { return 1; }\n" + `pre_clock_start_status=null
pre_clock_state_status=null
pre_clock_classifier_status=null
pre_clock_summary_status=null
readiness_deadline=null
deadline_checked_epoch=null
state=unobserved
stage_service=wkbench-rehearsal.service
stage_report_dir=rehearsal
stage_runtime_name=rehearsal
stage_journal_cursor='s=abc;i=1'
readiness_timeout=1
stage_duration_seconds=7200
` + loop + "\nprintf 'cleanup\\n'\n"
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", body)
			cmd.Dir = root
			unitOutput := tc.state + "LoadState=loaded\nSubState=failed\nResult=exit-code\nExecMainCode=1\nExecMainStatus=2\n"
			if tc.state == originalActiveEmptyJob {
				unitOutput = tc.state
			}
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "pre_clock_directory="+dir, "attempt_dir="+dir, "WK_CHAT_OUTPUT_DIR="+dir, "WK_CLOUD_SSH_CONFIG=fixture", "WK_CHAT_REQUEST_ID=fixture", "WK_CHAT_SOURCE_SHA="+strings.Repeat("a", 40), "deployment_control_sha="+strings.Repeat("b", 40), "bundle_digest=sha256:"+strings.Repeat("c", 64), "chat_stage=rehearsal", "deployment_generation=1", "CLOCK_MARKER="+filepath.Join(dir, "clock"), fmt.Sprintf("START_EXIT=%d", tc.startExit), fmt.Sprintf("STATE_EXIT=%d", tc.stateExit), "UNIT_OUTPUT="+unitOutput, "SUMMARY_OUTPUT="+tc.summary)
			out, err := runPreClockFixture(cmd)
			if err != nil || string(out) != "cleanup\n" {
				t.Fatalf("actual loop failed: %v / %q", err, out)
			}
			b, err := os.ReadFile(filepath.Join(dir, "before-kill.json"))
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Branch        string `json:"branch"`
				Before        bool   `json:"before_kill"`
				Start         int    `json:"start_command_exit"`
				State         *int   `json:"state_command_exit"`
				Classifier    *int   `json:"state_classifier_exit"`
				SummaryStatus *int   `json:"summary_classifier_exit"`
			}
			if err := json.Unmarshal(b, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Branch != tc.branch || !receipt.Before || receipt.Start != tc.startExit {
				t.Fatalf("wrong decision receipt: %s", b)
			}
			if tc.startExit == 0 && (receipt.State == nil || *receipt.State != tc.stateExit) {
				t.Fatalf("state status lost: %s", b)
			}
			if tc.branch == "stage_state_parse_failed" && (receipt.Classifier == nil || *receipt.Classifier != 2) {
				t.Fatalf("exact classifier rejection status lost: %s", b)
			}
			if tc.branch == "stage_terminal_before_run_start" || tc.branch == "stage_readiness_deadline_elapsed" {
				if receipt.Classifier == nil || *receipt.Classifier != 0 {
					t.Fatalf("valid state must not trigger parse failure: %s", b)
				}
			}
			if tc.state == originalActiveEmptyJob {
				unitBytes, err := os.ReadFile(filepath.Join(dir, "unit.json"))
				if err != nil {
					t.Fatal(err)
				}
				var unit struct {
					Valid      bool           `json:"valid"`
					Bytes      int            `json:"bytes"`
					SHA        string         `json:"sha256"`
					Properties map[string]any `json:"properties"`
				}
				if err := json.Unmarshal(unitBytes, &unit); err != nil {
					t.Fatal(err)
				}
				if !unit.Valid || unit.Bytes != 104 || unit.SHA != originalActiveEmptyJobSHA || unit.Properties["ActiveState"] != "active" || unit.Properties["Job"] != float64(0) {
					t.Fatalf("original unit evidence must remain valid and exact: %s", unitBytes)
				}
			}
			if tc.branch == "stage_terminal_before_run_start" {
				t.Run("summary_command_and_classifier", func(t *testing.T) {
					if !cursorSupported {
						t.Skip("host regex does not support unchanged {1,512} cursor bound; summary SSH branch requires Linux CI")
					}
					wantSummaryStatus := 1
					if tc.name == "terminal runtime" {
						wantSummaryStatus = 0
					}
					if receipt.SummaryStatus == nil || *receipt.SummaryStatus != wantSummaryStatus {
						t.Fatalf("summary classification status changed: %s", b)
					}
				})
			}
			if tc.branch == "stage_terminal_before_run_start" && !cursorSupported && receipt.SummaryStatus != nil {
				t.Fatalf("unsupported cursor parser invented summary status: %s", b)
			}
			for _, file := range []string{"diagnostic.json", "unit.json", "summary.json", "start-stderr.json", "state-stderr.json"} {
				bytes, readErr := os.ReadFile(filepath.Join(dir, file))
				if readErr == nil && (strings.Contains(string(bytes), "secret-canary") || strings.Contains(string(bytes), "outcome=")) {
					t.Fatalf("raw evidence leaked in %s", file)
				}
			}
		})
	}
}
