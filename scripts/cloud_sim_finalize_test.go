package scripts_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCloudSimulationLocalRuntimeDoesNotDeleteInheritedShimDirectory(t *testing.T) {
	root := repoRoot(t)
	callerDir := t.TempDir()
	sentinel := filepath.Join(callerDir, "caller-owned")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", "-c", `source "$1"; wk_cleanup_local_cloud_tools`, "bash",
		filepath.Join(root, "scripts", "cloud-sim", "local-runtime.sh"))
	command.Env = append(os.Environ(),
		"WK_LOCAL_TOOL_SHIM_DIR="+callerDir,
		"WK_LOCAL_TOOL_SHIM_OWNED=true",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cleanup inherited shim state: %v\n%s", err, output)
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "preserve" {
		t.Fatalf("caller-owned shim directory was changed: err=%v content=%q", err, content)
	}
}

func TestCloudSimulationLocalRuntimePrefersFixedCandidateOverCallerPATH(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	fixed := filepath.Join(temp, "fixed-gh")
	staleDir := filepath.Join(temp, "stale")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, fixed, "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(staleDir, "gh"), "#!/usr/bin/env bash\nexit 0\n")

	command := exec.Command("bash", "-c", `source "$1"; wk_resolve_executable gh WK_GH_BIN "$2"`, "bash",
		filepath.Join(root, "scripts", "cloud-sim", "local-runtime.sh"), fixed)
	command.Env = append(os.Environ(), "PATH="+staleDir+":/usr/bin:/bin")
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != fixed {
		t.Fatalf("resolved executable = %q, err=%v, want fixed candidate %q", output, err, fixed)
	}
}

func TestCloudSimulationLocalRuntimeRejectsVersionDrift(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	staleGH := filepath.Join(temp, "stale-gh")
	exactGH := filepath.Join(temp, "exact-gh")
	staleGo := filepath.Join(temp, "stale-go")
	writeExecutable(t, staleGH, "#!/usr/bin/env bash\nprintf '%s\\n' 'gh version 2.95.0 (stale)'\n")
	writeExecutable(t, exactGH, "#!/usr/bin/env bash\nprintf '%s\\n' 'gh version 2.96.0 (test)'\n")
	writeExecutable(t, staleGo, `#!/usr/bin/env bash
set -euo pipefail
root="${WK_FAKE_GO_ROOT:?}"
if [[ "$*" == "env GOROOT" ]]; then
  mkdir -p "$root/bin"
  [[ -e "$root/bin/go" ]] || ln -s "$0" "$root/bin/go"
  printf '%s\n' "$root"
elif [[ "$*" == "env GOVERSION" ]]; then
  printf '%s\n' go1.25.10
fi
`)

	for _, testCase := range []struct {
		name  string
		gh    string
		goBin string
		want  string
	}{
		{name: "stale GitHub CLI", gh: staleGH, goBin: staleGo, want: "GitHub CLI 2.96.0 is required"},
		{name: "stale Go toolchain", gh: exactGH, goBin: staleGo, want: "Go 1.25.11 is required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command("bash", "-c", `set -euo pipefail; source "$1"; wk_resolve_local_cloud_tools`, "bash",
				filepath.Join(root, "scripts", "cloud-sim", "local-runtime.sh"))
			command.Dir = root
			command.Env = append(os.Environ(),
				"WK_GH_BIN="+testCase.gh,
				"WK_GO_BIN="+testCase.goBin,
				"WK_FAKE_GO_ROOT="+filepath.Join(temp, "go-root"),
				"WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS=2",
			)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), testCase.want) {
				t.Fatalf("version drift err=%v output=%s", err, output)
			}
		})
	}
}

func TestCloudSimulationLocalRuntimeBoundsOneCommand(t *testing.T) {
	root := repoRoot(t)
	descendantPIDFile := filepath.Join(t.TempDir(), "descendant.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail; source "$1"; export WK_DESCENDANT_PID_FILE="$2"; wk_run_bounded 1 bash -c 'trap "" TERM; /bin/sleep 20 & child=$!; printf "%s\n" "$child" >"$WK_DESCENDANT_PID_FILE"; wait "$child"'`, "bash",
		filepath.Join(root, "scripts", "cloud-sim", "local-runtime.sh"))
	command.Args = append(command.Args, descendantPIDFile)
	started := time.Now()
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("bounded command exceeded test deadline: %v\n%s", ctx.Err(), output)
	}
	if err == nil {
		t.Fatal("bounded command unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("bounded command took %s", elapsed)
	}
	pidBytes, readErr := os.ReadFile(descendantPIDFile)
	if readErr != nil {
		t.Fatalf("read descendant pid: %v\n%s", readErr, output)
	}
	descendantPID, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if parseErr != nil {
		t.Fatalf("parse descendant pid %q: %v", pidBytes, parseErr)
	}
	t.Cleanup(func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) })
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(descendantPID, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if killErr := syscall.Kill(descendantPID, 0); killErr == nil {
		t.Fatalf("bounded command leaked descendant pid %d", descendantPID)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 124 {
		t.Fatalf("bounded command exit = %v, want timeout status 124\n%s", err, output)
	}
}

func TestCloudSimulationLocalRuntimeHangupCleansProcessGroup(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	descendantPIDFile := filepath.Join(temp, "descendant.pid")
	command := exec.Command("bash", "-c", `set -euo pipefail; source "$1"; export WK_DESCENDANT_PID_FILE="$2"; wk_run_bounded 20 bash -c 'trap "" HUP TERM; /bin/sleep 20 & child=$!; printf "%s\n" "$child" >"$WK_DESCENDANT_PID_FILE"; wait "$child"'`, "bash",
		filepath.Join(root, "scripts", "cloud-sim", "local-runtime.sh"), descendantPIDFile)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(descendantPIDFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bounded command did not start its descendant")
		}
		time.Sleep(10 * time.Millisecond)
	}
	descendantBytes, err := os.ReadFile(descendantPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(strings.TrimSpace(string(descendantBytes)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) })

	wrapperPID := command.Process.Pid
	psOutput, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(psOutput), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr == nil && ppidErr == nil && ppid == command.Process.Pid {
			wrapperPID = pid
			break
		}
	}
	if err := syscall.Kill(wrapperPID, syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case err := <-waited:
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 129 {
			t.Fatalf("bounded command hangup exit = %v, want 129", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bounded command did not terminate after SIGHUP")
	}
	deadline = time.Now().Add(time.Second)
	for syscall.Kill(descendantPID, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(descendantPID, 0); err == nil {
		t.Fatalf("SIGHUP leaked descendant pid %d", descendantPID)
	}
}

func TestCloudSimulationFinalizeWaitsAnalyzesCleansAndVerifiesRelease(t *testing.T) {
	testCases := []struct {
		name             string
		mode             string
		wantExitCode     int
		wantAnalyzeCalls int
		wantOutput       string
	}{
		{name: "completed workload", mode: "completed", wantAnalyzeCalls: 2, wantOutput: "Finalization complete"},
		{name: "in-progress workload retries", mode: "in-progress", wantAnalyzeCalls: 3, wantOutput: "retrying terminal analysis"},
		{name: "interrupt after analysis starts still cleans", mode: "interrupt", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "exact cleanup will still run"},
		{name: "interrupt during retry wait still cleans", mode: "interrupt-wait", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "retrying terminal analysis"},
		{name: "interrupt during lease clock still cleans", mode: "interrupt-clock", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "exact cleanup will run"},
		{name: "explicit tools outside stripped PATH", mode: "external-tools", wantAnalyzeCalls: 2, wantOutput: "Finalization complete"},
		{name: "missing openssl still cleans and verifies", mode: "missing-openssl", wantAnalyzeCalls: 2, wantOutput: "Finalization complete"},
		{name: "terminal interrupt during auth still cleans", mode: "pid-preflight-auth-hang", wantExitCode: 130, wantAnalyzeCalls: 1, wantOutput: "pre-analysis interrupt"},
		{name: "terminal interrupt during cleanup API check still cleans", mode: "pid-preflight-api-hang", wantExitCode: 130, wantAnalyzeCalls: 1, wantOutput: "pre-analysis interrupt"},
		{name: "terminal interrupt during schedule download still cleans", mode: "pid-preflight-download-hang", wantExitCode: 130, wantAnalyzeCalls: 1, wantOutput: "pre-analysis interrupt"},
		{name: "pid-only interrupt during analysis-ready wait still cleans", mode: "pid-preanalysis-hang", wantExitCode: 130, wantAnalyzeCalls: 1, wantOutput: "before analysis"},
		{name: "pid-only term kills analysis group then cleans", mode: "pid-term-hang", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "exact cleanup will run"},
		{name: "pid-only hup kills analysis group then cleans", mode: "pid-hup-hang", wantExitCode: 129, wantAnalyzeCalls: 2, wantOutput: "exact cleanup will run"},
		{name: "terminal interrupt cannot stop exact cleanup", mode: "pid-cleanup-signal", wantAnalyzeCalls: 2, wantOutput: "Finalization complete"},
		{name: "released analysis without provider proof still cleans", mode: "analysis-missing-provider", wantExitCode: 1, wantAnalyzeCalls: 2, wantOutput: "terminal analysis failed"},
		{name: "invalid analysis timeout still cleans", mode: "invalid-analysis-timeout", wantExitCode: 125, wantAnalyzeCalls: 1, wantOutput: "must be a positive integer"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if strings.HasPrefix(testCase.mode, "pid-") {
				runTimingSensitiveShellScriptTestExclusively(t)
			} else {
				runHeavyShellScriptTestInParallel(t)
			}
			output, calls, exitCode := runFinalizeHarness(t, testCase.mode)
			if exitCode != testCase.wantExitCode {
				t.Fatalf("finalize exit code = %d, want %d\n%s\ncalls:\n%s", exitCode, testCase.wantExitCode, output, calls)
			}
			if !strings.Contains(output, testCase.wantOutput) ||
				!strings.Contains(output, "Simulation Run gh-12345-1 已由云厂商确认自动销毁") {
				t.Fatalf("finalize output missing terminal evidence:\n%s", output)
			}
			requiredCalls := []string{
				"analyze gh-12345-1 --repository example/project --result-file ",
				"gh workflow run cloud-sim-cleanup.yml --repo example/project --ref main -f run_id=gh-12345-1 -f request_id=finalize-",
				"gh run view 9001 --repo example/project --json status,conclusion",
			}
			if testCase.mode != "pid-preflight-auth-hang" && testCase.mode != "pid-preflight-api-hang" {
				requiredCalls = append(requiredCalls,
					"gh run download 12345 --repo example/project --name cloud-sim-finalize-gh-12345-1")
			}
			for _, required := range requiredCalls {
				if !strings.Contains(calls, required) {
					t.Fatalf("finalize calls missing %q:\n%s", required, calls)
				}
			}
			if got := strings.Count(calls, "analyze gh-12345-1"); got != testCase.wantAnalyzeCalls {
				t.Fatalf("analysis call count = %d, want %d:\n%s", got, testCase.wantAnalyzeCalls, calls)
			}
			if strings.Contains(calls, "--allow-fix-pr") || strings.Contains(calls, "cloud-remediation") ||
				strings.Contains(calls, "workflow run ci.yml") {
				t.Fatalf("finalize ran optional remediation before exact Cleanup:\n%s", calls)
			}
			cleanupRequest := regexp.MustCompile(`(?m)^gh workflow run cloud-sim-cleanup\.yml .* -f request_id=finalize-[0-9a-f]{16}$`)
			if !cleanupRequest.MatchString(calls) {
				t.Fatalf("cleanup request id violated the workflow contract:\n%s", calls)
			}
			if got := strings.Count(calls, "gh workflow run cloud-sim-cleanup.yml"); got != 1 {
				t.Fatalf("cleanup dispatches = %d, want exactly one:\n%s", got, calls)
			}
		})
	}
}

func TestCloudSimulationFinalizeRecoversCorrelatedCleanupAfterLocalGitHubErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		mode       string
		wantOutput string
	}{
		{
			name:       "dispatch accepted before client error",
			mode:       "cleanup-dispatch-ambiguous",
			wantOutput: "dispatch returned gh exit 72, but the exact correlated workflow",
		},
		{
			name:       "poll interrupted before terminal success",
			mode:       "cleanup-view-transient",
			wantOutput: "completed successfully",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runHeavyShellScriptTestInParallel(t)
			output, calls, exitCode := runFinalizeHarness(t, testCase.mode)
			if exitCode != 0 || !strings.Contains(output, testCase.wantOutput) ||
				!strings.Contains(output, "Billing cleanup proven by exact Cleanup workflow") {
				t.Fatalf("finalize cleanup recovery exit=%d\n%s\ncalls:\n%s", exitCode, output, calls)
			}
			if got := strings.Count(calls, "gh workflow run cloud-sim-cleanup.yml"); got != 1 {
				t.Fatalf("cleanup dispatches = %d, want one correlated dispatch:\n%s", got, calls)
			}
		})
	}
}

func TestCloudSimulationFinalizeDistinguishesCleanupFailureFromVerificationFailure(t *testing.T) {
	t.Run("cleanup list outage does not redispatch", func(t *testing.T) {
		runHeavyShellScriptTestInParallel(t)
		output, calls, exitCode := runFinalizeHarness(t, "cleanup-list-outage")
		if exitCode == 0 || !strings.Contains(output, "refusing to redispatch an ambiguous request correlation") ||
			!strings.Contains(output, "exact Cleanup was not proven") {
			t.Fatalf("cleanup list outage was not failed closed, exit=%d\n%s\ncalls:\n%s", exitCode, output, calls)
		}
		if got := strings.Count(calls, "gh workflow run cloud-sim-cleanup.yml"); got != 1 {
			t.Fatalf("ambiguous cleanup dispatches = %d, want exactly one:\n%s", got, calls)
		}
		if got := strings.Count(calls, "analyze gh-12345-1"); got != 1 {
			t.Fatalf("release verification ran without cleanup proof: calls=%d\n%s", got, calls)
		}
	})

	t.Run("terminal cleanup failure", func(t *testing.T) {
		runHeavyShellScriptTestInParallel(t)
		output, calls, exitCode := runFinalizeHarness(t, "cleanup-terminal-failure")
		if exitCode == 0 || !strings.Contains(output, "Exact Cleanup workflow 9001 completed with conclusion failure") ||
			!strings.Contains(output, "exact Cleanup was not proven") ||
			strings.Contains(output, "Billing cleanup proven") {
			t.Fatalf("terminal cleanup failure was not distinguished, exit=%d\n%s\ncalls:\n%s", exitCode, output, calls)
		}
		if got := strings.Count(calls, "analyze gh-12345-1"); got != 1 {
			t.Fatalf("release verification ran after failed cleanup: calls=%d\n%s", got, calls)
		}
	})

	t.Run("independent release verification failure", func(t *testing.T) {
		runHeavyShellScriptTestInParallel(t)
		output, calls, exitCode := runFinalizeHarness(t, "verification-failure")
		if exitCode != 77 || !strings.Contains(output, "Billing cleanup proven by exact Cleanup workflow") ||
			!strings.Contains(output, "Billing cleanup is already proven") ||
			!strings.Contains(output, "independent provider release verification failed with status 77") {
			t.Fatalf("verification failure obscured proven cleanup, exit=%d\n%s\ncalls:\n%s", exitCode, output, calls)
		}
	})

	t.Run("independent release verification timeout kills descendants", func(t *testing.T) {
		runHeavyShellScriptTestInParallel(t)
		output, calls, exitCode := runFinalizeHarness(t, "verification-timeout")
		if exitCode != 124 || !strings.Contains(output, "Billing cleanup proven by exact Cleanup workflow") ||
			!strings.Contains(output, "independent provider release verification failed with status 124") {
			t.Fatalf("verification timeout obscured proven cleanup, exit=%d\n%s\ncalls:\n%s", exitCode, output, calls)
		}
	})
	t.Run("released result without provider proof is rejected", func(t *testing.T) {
		runHeavyShellScriptTestInParallel(t)
		output, calls, exitCode := runFinalizeHarness(t, "verification-missing-provider")
		if exitCode != 1 || !strings.Contains(output, "Billing cleanup proven by exact Cleanup workflow") ||
			!strings.Contains(output, "independent provider verification did not return the structured released state") {
			t.Fatalf("missing provider proof passed release verification, exit=%d\n%s\ncalls:\n%s", exitCode, output, calls)
		}
		if got := strings.Count(calls, "gh workflow run cloud-sim-cleanup.yml"); got != 1 {
			t.Fatalf("cleanup dispatches = %d, want exactly one:\n%s", got, calls)
		}
	})
}

func TestCloudSimulationFinalizeRealAnalyzeVerifiesReleasedWithoutGo(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	temp := t.TempDir()
	scriptDir := filepath.Join(temp, "scripts", "cloud-sim")
	binDir := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	for _, directory := range []string{scriptDir, binDir, stateDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range []string{"analyze.sh", "finalize.sh", "local-runtime.sh"} {
		copyExecutable(t, filepath.Join(root, "scripts", "cloud-sim", name), filepath.Join(scriptDir, name))
	}
	writeExecutable(t, filepath.Join(binDir, "curl"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '198.51.100.42'
`)
	writeExecutable(t, filepath.Join(binDir, "git"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *' remote get-url origin' ]]; then
  printf '%s\n' 'https://github.com/example/project.git'
  exit 0
fi
exit 91
`)
	writeExecutable(t, filepath.Join(binDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$WK_FINALIZE_CALL_LOG"
if [[ "${1:-}" == --version ]]; then
  printf '%s\n' 'gh version 2.96.0 (test)'
  exit 0
fi
case "${1:-} ${2:-}" in
  "auth status") exit 0 ;;
  "repo view") printf '%s\n' 'example/project' ;;
  "api "*) exit 0 ;;
  "workflow run")
    workflow="${3:-}"
    operation=""
    request_id=""
    run_id=""
    while (($#)); do
      case "$1" in
        operation=*) operation="${1#operation=}" ;;
        request_id=*) request_id="${1#request_id=}" ;;
        run_id=*) run_id="${1#run_id=}" ;;
      esac
      shift
    done
    count_file="$WK_FINALIZE_STATE_DIR/workflow-count"
    count="$(cat "$count_file" 2>/dev/null || printf '0')"
    count=$((count + 1))
    printf '%s' "$count" >"$count_file"
    workflow_run=$((9100 + count))
    case "$workflow" in
      cloud-sim-analyze.yml)
        title="Cloud Simulation Analysis $operation $request_id"
        printf '%s' "$operation" >"$WK_FINALIZE_STATE_DIR/analysis-operation"
        printf '%s' "$request_id" >"$WK_FINALIZE_STATE_DIR/analysis-request-id"
        ;;
      cloud-sim-cleanup.yml)
        title="Cloud Simulation Cleanup $run_id $request_id"
        printf '%s\n' '{"state":"released","resources":[]}' >"$WK_FINALIZE_STATE_DIR/provider-inventory.json"
        ;;
      *) exit 91 ;;
    esac
    printf '%s' "$workflow_run" >"$WK_FINALIZE_STATE_DIR/current-workflow-run"
    printf '%s' "$title" >"$WK_FINALIZE_STATE_DIR/current-title"
    ;;
  "run list")
    workflow_run="$(cat "$WK_FINALIZE_STATE_DIR/current-workflow-run")"
    title="$(cat "$WK_FINALIZE_STATE_DIR/current-title")"
    jq -cn --argjson workflow_run "$workflow_run" --arg title "$title" \
      '[{databaseId:$workflow_run,displayTitle:$title}]'
    ;;
  "run view")
    printf '%s\n' '{"status":"completed","conclusion":"success"}'
    ;;
  "run download")
    artifact=""
    destination=""
    while (($#)); do
      case "$1" in
        --name) artifact="$2"; shift 2; continue ;;
        --dir) destination="$2"; shift 2; continue ;;
      esac
      shift
    done
    mkdir -p "$destination"
    if [[ "$artifact" == cloud-sim-finalize-* ]]; then
      printf '%s\n' '{"schema":"wukongim/cloud-simulation-finalize/v1","run_id":"gh-12345-1","active_until":"2000-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z"}' >"$destination/finalize.json"
      exit 0
    fi
    request_id="$(cat "$WK_FINALIZE_STATE_DIR/analysis-request-id")"
    if [[ "$artifact" == cloud-sim-analysis-preflight-* ]]; then
      if jq -e '.state == "released" and (.resources | length) == 0' \
        "$WK_FINALIZE_STATE_DIR/provider-inventory.json" >/dev/null 2>&1; then
        jq -n --arg request_id "$request_id" \
          '{schema:"wukongim/cloud-simulation-analysis-preflight/v1",state:"released",run_id:"gh-12345-1",request_id:$request_id,message:"Simulation Run gh-12345-1 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。",provider:{state:"released",resources:[]}}' \
          >"$destination/preflight.json"
      else
        jq -n --arg request_id "$request_id" \
          '{schema:"wukongim/cloud-simulation-analysis-preflight/v1",state:"live",run_id:"gh-12345-1",request_id:$request_id,message:"Provider preflight confirmed a live identity-matched Simulation Run; client material is required to open a session.",provider:{state:"live",resources:[{kind:"instance",role:"sim"}]}}' \
          >"$destination/preflight.json"
      fi
    elif [[ "$artifact" == cloud-sim-analysis-session-* ]]; then
      jq -n --arg request_id "$request_id" \
        '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"live",run_id:"gh-12345-1",request_id:$request_id,source_sha:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",scenario_digest:"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",mcp_url:"https://198.51.100.20:19092/mcp",expires_at:"2099-07-14T01:00:00Z",ca_fingerprint:"sha256:494c9d9f353d3aa18a4ada2697e2a7bac90492022d9821ba0a5a6f4c3b15233a"}' \
        >"$destination/session.json"
    else
      exit 93
    fi
    ;;
  *) printf 'unexpected gh call: %s\n' "$*" >&2; exit 92 ;;
esac
`)

	callLog := filepath.Join(stateDir, "calls.log")
	missingGo := filepath.Join(temp, "missing-go")
	command := exec.Command("bash", filepath.Join(scriptDir, "finalize.sh"), "gh-12345-1",
		"--repository", "example/project", "--allow-fix-pr")
	command.Dir = temp
	command.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"WK_CLOUD_TOOLCHAIN_FILE="+filepath.Join(root, ".github", "cloud-sim", "toolchain.env"),
		"WK_FINALIZE_CALL_LOG="+callLog,
		"WK_FINALIZE_STATE_DIR="+stateDir,
		"WK_GH_BIN="+filepath.Join(binDir, "gh"),
		"WK_GO_BIN="+missingGo,
		"WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS=3",
		"WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS=3",
	)
	outputBytes, commandErr := command.CombinedOutput()
	if commandErr == nil {
		t.Fatalf("finalize unexpectedly hid the failed terminal analysis:\n%s", outputBytes)
	}
	var exitError *exec.ExitError
	if !errors.As(commandErr, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("finalize error = %v, want terminal-analysis status 1:\n%s", commandErr, outputBytes)
	}
	output := string(outputBytes)
	for _, evidence := range []string{
		"cannot resolve the pinned Go toolchain required for live diagnosis",
		"Analysis failed with status 1; exact cleanup will still run",
		"Billing cleanup proven by exact Cleanup workflow",
		"Simulation Run gh-12345-1 已由云厂商确认自动销毁",
		"Finalization cleaned and verified the exact Run, but terminal analysis failed",
	} {
		if !strings.Contains(output, evidence) {
			t.Fatalf("finalize output missing %q:\n%s", evidence, output)
		}
	}
	callsBytes, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(callsBytes)
	if got := strings.Count(calls, "gh workflow run cloud-sim-cleanup.yml"); got != 1 {
		t.Fatalf("cleanup dispatches = %d, want exactly one:\n%s", got, calls)
	}
	if got := strings.Count(calls, "-f operation=inspect"); got != 2 {
		t.Fatalf("analysis inspect dispatches = %d, want live attempt plus released verification:\n%s", got, calls)
	}
	if got := strings.Count(calls, "-f operation=prepare"); got != 1 {
		t.Fatalf("analysis prepare dispatches = %d, want only the provider-confirmed live attempt:\n%s", got, calls)
	}
	inventoryBytes, err := os.ReadFile(filepath.Join(stateDir, "provider-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if inventory := strings.TrimSpace(string(inventoryBytes)); inventory != `{"state":"released","resources":[]}` {
		t.Fatalf("provider inventory = %s, want state=released and resources.length=0", inventory)
	}
}

func runFinalizeHarness(t *testing.T, mode string) (output string, calls string, exitCode int) {
	t.Helper()
	root := repoRoot(t)
	temp := t.TempDir()
	scriptDir := filepath.Join(temp, "scripts", "cloud-sim")
	binDir := filepath.Join(temp, "bin")
	toolBin := filepath.Join(temp, "tool-bin")
	stateDir := filepath.Join(temp, "state")
	for _, directory := range []string{scriptDir, binDir, toolBin, stateDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	copyExecutable(t, filepath.Join(root, "scripts", "cloud-sim", "finalize.sh"), filepath.Join(scriptDir, "finalize.sh"))
	copyExecutable(t, filepath.Join(root, "scripts", "cloud-sim", "local-runtime.sh"), filepath.Join(scriptDir, "local-runtime.sh"))
	writeExecutable(t, filepath.Join(binDir, "go"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "env GOVERSION" ]]; then
  printf '%s\n' go1.25.11
elif [[ "$*" == "env GOROOT" ]]; then
  go_root="$WK_FINALIZE_STATE_DIR/goroot"
  mkdir -p "$go_root/bin"
  if [[ ! -e "$go_root/bin/go" ]]; then
    ln -s "$(cd "$(dirname "$0")" && pwd)/${0##*/}" "$go_root/bin/go"
  fi
  printf '%s\n' "$go_root"
fi
`)
	writeExecutable(t, filepath.Join(binDir, "git"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *' remote get-url origin' ]]; then
  printf '%s\n' 'https://github.com/example/project.git'
  exit 0
fi
exit 91
`)
	writeExecutable(t, filepath.Join(scriptDir, "analyze.sh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'analyze %s\n' "$*" >>"$WK_FINALIZE_CALL_LOG"
result_file=""
while (($#)); do
  if [[ "$1" == --result-file ]]; then result_file="$2"; shift 2; else shift; fi
done
count_file="$WK_FINALIZE_STATE_DIR/analyze-count"
count=0
[[ ! -f "$count_file" ]] || count="$(cat "$count_file")"
count=$((count + 1))
printf '%s\n' "$count" >"$count_file"
write_diagnosed() {
  local workload_state="$1"
  printf '{"schema":"wukongim/cloud-simulation-analysis-result/v1","run_id":"gh-12345-1","state":"diagnosed","diagnosis":{"observation_references":[{"tool":"workload_inspect","state":"%s"}]}}\n' "$workload_state" >"$result_file"
  printf '{"verdict":"healthy","workload_state":"%s"}\n' "$workload_state"
}
write_released() {
  printf '%s\n' '{"schema":"wukongim/cloud-simulation-analysis-result/v1","run_id":"gh-12345-1","state":"released","diagnosis":null,"provider":{"state":"released","resources":[]}}' >"$result_file"
  printf '%s\n' 'Simulation Run gh-12345-1 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。'
}
write_unproven_released() {
  printf '%s\n' '{"schema":"wukongim/cloud-simulation-analysis-result/v1","run_id":"gh-12345-1","state":"released","diagnosis":null}' >"$result_file"
  printf '%s\n' 'unproven released result'
}
case "$WK_FINALIZE_MODE:$count" in
  completed:1) write_diagnosed completed ;;
  in-progress:1) write_diagnosed in_progress ;;
  in-progress:2) write_diagnosed completed ;;
  interrupt-wait:1) write_diagnosed in_progress ;;
  interrupt-clock:1) write_diagnosed in_progress ;;
  interrupt:1) kill -INT "$PPID"; exit 130 ;;
	analysis-missing-provider:1|verification-missing-provider:2) write_unproven_released ;;
  pid-term-hang:1|pid-hup-hang:1)
    trap '' HUP TERM
    /bin/sleep 30 &
    child=$!
    printf '%s\n' "$child" >"$WK_FINALIZE_STATE_DIR/analyze-descendant.pid"
    wait "$child"
    ;;
  external-tools:1|missing-openssl:1|pid-cleanup-signal:1|cleanup-dispatch-ambiguous:1|cleanup-view-transient:1|cleanup-list-outage:1|cleanup-terminal-failure:1|verification-failure:1|verification-timeout:1|verification-missing-provider:1)
    write_diagnosed completed
    ;;
  verification-failure:2) exit 77 ;;
  verification-timeout:2)
    trap '' HUP TERM
    /bin/sleep 30 &
    child=$!
    printf '%s\n' "$child" >"$WK_FINALIZE_STATE_DIR/verification-descendant.pid"
    wait "$child"
    ;;
  *) write_released ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "sleep"), `#!/usr/bin/env bash
set -euo pipefail
printf 'sleep %s\n' "$*" >>"$WK_FINALIZE_CALL_LOG"
	if [[ "$WK_FINALIZE_MODE" == pid-preanalysis-hang && "$1" == 60 ]]; then
	  trap '' HUP TERM
	  /bin/sleep 30 &
	  child=$!
	  printf '%s\n' "$child" >"$WK_FINALIZE_STATE_DIR/preanalysis-descendant.pid"
	  wait "$child"
	  exit
	fi
if [[ "$WK_FINALIZE_MODE" == interrupt-wait && "$1" == 60 ]]; then
  kill -INT "$PPID"
  exit 130
fi
`)
	writeExecutable(t, filepath.Join(binDir, "date"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "$WK_FINALIZE_MODE" == interrupt-clock && "$*" == '-u +%s' ]]; then
  count_file="$WK_FINALIZE_STATE_DIR/date-count"
  count=0
  [[ ! -f "$count_file" ]] || count="$(cat "$count_file")"
  count=$((count + 1))
  printf '%s\n' "$count" >"$count_file"
  if [[ "$count" == 3 ]]; then
    kill -INT "$PPID"
    exit 130
  fi
fi
exec /usr/bin/env -i PATH=/usr/bin:/bin date "$@"
`)
	writeExecutable(t, filepath.Join(binDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$WK_FINALIZE_CALL_LOG"
if [[ "${1:-}" == --version ]]; then
  printf '%s\n' 'gh version 2.96.0 (test)'
  exit 0
fi
hang_marker=""
case "$WK_FINALIZE_MODE:${1:-} ${2:-}" in
  pid-preflight-auth-hang:auth\ status|pid-preflight-api-hang:api\ *|pid-preflight-download-hang:run\ download)
    hang_marker="$WK_FINALIZE_STATE_DIR/preflight-descendant.pid"
    ;;
  pid-cleanup-signal:workflow\ run)
    hang_marker="$WK_FINALIZE_STATE_DIR/cleanup-descendant.pid"
    ;;
esac
if [[ -n "$hang_marker" ]]; then
  trap '' HUP TERM
  delay=30
  [[ "$WK_FINALIZE_MODE" != pid-cleanup-signal ]] || delay=1
  /bin/sleep "$delay" &
  child=$!
  printf '%s\n' "$child" >"$hang_marker"
  wait "$child"
fi
case "${1:-} ${2:-}" in
  "auth status") exit 0 ;;
  "repo view") printf '%s\n' 'example/project' ;;
  "api "*) exit 0 ;;
  "run download")
    destination=""
    while (($#)); do
      if [[ "$1" == --dir ]]; then destination="$2"; break; fi
      shift
	    done
	    mkdir -p "$destination"
	    if [[ "$WK_FINALIZE_MODE" == pid-preanalysis-hang ]]; then
	      printf '%s\n' '{"schema":"wukongim/cloud-simulation-finalize/v1","run_id":"gh-12345-1","active_until":"2000-01-01T00:00:00Z","analysis_ready_at":"2099-01-01T00:00:00Z","expires_at":"2099-02-01T00:00:00Z"}' >"$destination/finalize.json"
	    else
	      printf '%s\n' '{"schema":"wukongim/cloud-simulation-finalize/v1","run_id":"gh-12345-1","active_until":"2000-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z"}' >"$destination/finalize.json"
	    fi
    ;;
  "workflow run")
    request_id=""
    while (($#)); do
      case "$1" in
        request_id=*) request_id="${1#request_id=}" ;;
      esac
      shift
    done
    printf 'Cloud Simulation Cleanup gh-12345-1 %s\n' "$request_id" >"$WK_FINALIZE_STATE_DIR/cleanup-title"
    if [[ "$WK_FINALIZE_MODE" == cleanup-dispatch-ambiguous ]]; then
      exit 72
    fi
    ;;
  "run list")
    if [[ "$WK_FINALIZE_MODE" == cleanup-list-outage ]]; then
      exit 75
    fi
    title="$(cat "$WK_FINALIZE_STATE_DIR/cleanup-title")"
    printf '[{"databaseId":9001,"displayTitle":"%s"}]\n' "$title"
    ;;
  "run view")
    if [[ "$WK_FINALIZE_MODE" == cleanup-view-transient ]]; then
      count_file="$WK_FINALIZE_STATE_DIR/view-count"
      count=0
      [[ ! -f "$count_file" ]] || count="$(cat "$count_file")"
      count=$((count + 1))
      printf '%s\n' "$count" >"$count_file"
      if [[ "$count" == 1 ]]; then
        exit 73
      fi
    fi
    if [[ "$WK_FINALIZE_MODE" == cleanup-terminal-failure ]]; then
      printf '%s\n' '{"status":"completed","conclusion":"failure"}'
    else
      printf '%s\n' '{"status":"completed","conclusion":"success"}'
    fi
    ;;
  *) printf 'unexpected gh call: %s\n' "$*" >&2; exit 1 ;;
esac
`)
	commandPath := binDir + ":" + os.Getenv("PATH")
	extraEnvironment := []string{
		"WK_GH_BIN=" + filepath.Join(binDir, "gh"),
		"WK_GO_BIN=" + filepath.Join(binDir, "go"),
	}
	if mode == "external-tools" || mode == "missing-openssl" {
		if mode == "external-tools" {
			for _, tool := range []struct {
				name     string
				override string
			}{
				{name: "gh", override: "selected-gh"},
				{name: "go", override: "selected-go"},
			} {
				if err := os.Rename(filepath.Join(binDir, tool.name), filepath.Join(toolBin, tool.override)); err != nil {
					t.Fatal(err)
				}
				writeExecutable(t, filepath.Join(binDir, tool.name), `#!/usr/bin/env bash
exit 98
`)
			}
		}
		preservedCommands := []string{
			"awk", "bash", "cat", "chmod", "date", "dirname", "env", "jq", "ln", "mkdir", "mktemp", "perl",
			"rm", "sleep", "tee",
		}
		if mode == "external-tools" {
			preservedCommands = append(preservedCommands, "openssl")
		}
		for _, name := range preservedCommands {
			source, err := exec.LookPath(name)
			if err != nil {
				t.Fatalf("find required test command %s: %v", name, err)
			}
			target := filepath.Join(binDir, name)
			if _, err := os.Lstat(target); err == nil {
				continue
			}
			if err := os.Symlink(source, target); err != nil {
				t.Fatal(err)
			}
		}
		commandPath = binDir
		if mode == "external-tools" {
			extraEnvironment = []string{
				"WK_GH_BIN=" + filepath.Join(toolBin, "selected-gh"),
				"WK_GO_BIN=" + filepath.Join(toolBin, "selected-go"),
			}
		}
	}
	callLog := filepath.Join(stateDir, "calls.log")
	command := exec.Command("bash", filepath.Join(scriptDir, "finalize.sh"), "gh-12345-1",
		"--repository", "example/project", "--allow-fix-pr")
	command.Dir = temp
	command.Env = append(os.Environ(),
		"PATH="+commandPath,
		"WK_CLOUD_TOOLCHAIN_FILE="+filepath.Join(root, ".github", "cloud-sim", "toolchain.env"),
		"WK_FINALIZE_CALL_LOG="+callLog,
		"WK_FINALIZE_STATE_DIR="+stateDir,
		"WK_FINALIZE_MODE="+mode,
	)
	command.Env = append(command.Env, extraEnvironment...)
	if mode == "invalid-analysis-timeout" {
		command.Env = append(command.Env, "WK_LOCAL_FINALIZE_ANALYSIS_TIMEOUT_SECONDS=invalid")
	} else if mode == "verification-timeout" {
		command.Env = append(command.Env, "WK_LOCAL_FINALIZE_VERIFICATION_TIMEOUT_SECONDS=1")
	}
	var outputBytes []byte
	var commandErr error
	if mode == "pid-preanalysis-hang" || mode == "pid-term-hang" || mode == "pid-hup-hang" ||
		strings.HasPrefix(mode, "pid-preflight-") || mode == "pid-cleanup-signal" {
		var outputBuffer bytes.Buffer
		command.Stdout = &outputBuffer
		command.Stderr = &outputBuffer
		groupSignal := strings.HasPrefix(mode, "pid-preflight-") || mode == "pid-cleanup-signal"
		if groupSignal {
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		descendantPIDFile := filepath.Join(stateDir, "analyze-descendant.pid")
		switch {
		case mode == "pid-preanalysis-hang":
			descendantPIDFile = filepath.Join(stateDir, "preanalysis-descendant.pid")
		case strings.HasPrefix(mode, "pid-preflight-"):
			descendantPIDFile = filepath.Join(stateDir, "preflight-descendant.pid")
		case mode == "pid-cleanup-signal":
			descendantPIDFile = filepath.Join(stateDir, "cleanup-descendant.pid")
		}
		deadline := time.Now().Add(4 * time.Second)
		for {
			if _, err := os.Stat(descendantPIDFile); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = command.Process.Kill()
				calls, _ := os.ReadFile(callLog)
				t.Fatalf("finalize did not start the hanging descendant\n%s\ncalls:\n%s", outputBuffer.String(), calls)
			}
			time.Sleep(10 * time.Millisecond)
		}
		signal := syscall.SIGTERM
		if mode == "pid-preanalysis-hang" || groupSignal {
			signal = syscall.SIGINT
		} else if mode == "pid-hup-hang" {
			signal = syscall.SIGHUP
		}
		signals := []syscall.Signal{signal}
		if mode == "pid-cleanup-signal" {
			signals = []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT}
		}
		for _, deliveredSignal := range signals {
			var signalErr error
			if groupSignal {
				signalErr = syscall.Kill(-command.Process.Pid, deliveredSignal)
			} else {
				signalErr = command.Process.Signal(deliveredSignal)
			}
			if signalErr != nil {
				t.Fatal(signalErr)
			}
			if len(signals) > 1 {
				time.Sleep(20 * time.Millisecond)
			}
		}
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		select {
		case commandErr = <-waited:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			t.Fatalf("%s did not advance Finalize to exact Cleanup", signal)
		}
		outputBytes = outputBuffer.Bytes()
		descendantPayload, err := os.ReadFile(descendantPIDFile)
		if err != nil {
			t.Fatal(err)
		}
		descendantPID, err := strconv.Atoi(strings.TrimSpace(string(descendantPayload)))
		if err != nil {
			t.Fatal(err)
		}
		deadline = time.Now().Add(2 * time.Second)
		for syscall.Kill(descendantPID, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if err := syscall.Kill(descendantPID, 0); err == nil {
			_ = syscall.Kill(descendantPID, syscall.SIGKILL)
			t.Fatalf("Finalize leaked bounded descendant pid %d", descendantPID)
		}
	} else {
		outputBytes, commandErr = command.CombinedOutput()
	}
	if mode == "verification-timeout" {
		descendantPIDFile := filepath.Join(stateDir, "verification-descendant.pid")
		descendantPayload, err := os.ReadFile(descendantPIDFile)
		if err != nil {
			t.Fatal(err)
		}
		descendantPID, err := strconv.Atoi(strings.TrimSpace(string(descendantPayload)))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for syscall.Kill(descendantPID, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if err := syscall.Kill(descendantPID, 0); err == nil {
			_ = syscall.Kill(descendantPID, syscall.SIGKILL)
			t.Fatalf("Finalize leaked release-verification descendant pid %d", descendantPID)
		}
	}
	exitCode = 0
	if commandErr != nil {
		var exitError *exec.ExitError
		if !errors.As(commandErr, &exitError) {
			t.Fatalf("finalize execution: %v\n%s", commandErr, outputBytes)
		}
		exitCode = exitError.ExitCode()
	}
	callBytes, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	return string(outputBytes), string(callBytes), exitCode
}

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, destination, string(content))
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
