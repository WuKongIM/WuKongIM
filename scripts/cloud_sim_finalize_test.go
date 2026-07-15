package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		{name: "interrupt after analysis starts still cleans", mode: "interrupt", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "exact cleanup will run"},
		{name: "interrupt during retry wait still cleans", mode: "interrupt-wait", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "retrying terminal analysis"},
		{name: "interrupt during lease clock still cleans", mode: "interrupt-clock", wantExitCode: 130, wantAnalyzeCalls: 2, wantOutput: "exact cleanup will run"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			output, calls, exitCode := runFinalizeHarness(t, testCase.mode)
			if exitCode != testCase.wantExitCode {
				t.Fatalf("finalize exit code = %d, want %d\n%s\ncalls:\n%s", exitCode, testCase.wantExitCode, output, calls)
			}
			if !strings.Contains(output, testCase.wantOutput) ||
				!strings.Contains(output, "Simulation Run gh-12345-1 已由云厂商确认自动销毁") {
				t.Fatalf("finalize output missing terminal evidence:\n%s", output)
			}
			for _, required := range []string{
				"gh run download 12345 --repo example/project --name cloud-sim-finalize-gh-12345-1",
				"analyze gh-12345-1 --repository example/project --allow-fix-pr --result-file ",
				"gh workflow run cloud-sim-cleanup.yml --repo example/project --ref main -f run_id=gh-12345-1 -f request_id=finalize-",
				"gh run watch 9001 --repo example/project --exit-status",
				"analyze gh-12345-1 --repository example/project --result-file ",
			} {
				if !strings.Contains(calls, required) {
					t.Fatalf("finalize calls missing %q:\n%s", required, calls)
				}
			}
			if got := strings.Count(calls, "analyze gh-12345-1"); got != testCase.wantAnalyzeCalls {
				t.Fatalf("analysis call count = %d, want %d:\n%s", got, testCase.wantAnalyzeCalls, calls)
			}
		})
	}
}

func runFinalizeHarness(t *testing.T, mode string) (output string, calls string, exitCode int) {
	t.Helper()
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

	copyExecutable(t, filepath.Join(root, "scripts", "cloud-sim", "finalize.sh"), filepath.Join(scriptDir, "finalize.sh"))
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
  printf '%s\n' '{"schema":"wukongim/cloud-simulation-analysis-result/v1","run_id":"gh-12345-1","state":"released","diagnosis":null}' >"$result_file"
  printf '%s\n' 'Simulation Run gh-12345-1 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。'
}
case "$WK_FINALIZE_MODE:$count" in
  completed:1) write_diagnosed completed ;;
  in-progress:1) write_diagnosed in_progress ;;
  in-progress:2) write_diagnosed completed ;;
  interrupt-wait:1) write_diagnosed in_progress ;;
  interrupt-clock:1) write_diagnosed in_progress ;;
  interrupt:1) kill -INT "$PPID"; exit 130 ;;
  *) write_released ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "sleep"), `#!/usr/bin/env bash
set -euo pipefail
printf 'sleep %s\n' "$*" >>"$WK_FINALIZE_CALL_LOG"
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
  if [[ "$count" == 2 ]]; then
    kill -INT "$PPID"
    exit 130
  fi
fi
exec /usr/bin/env -i PATH=/usr/bin:/bin date "$@"
`)
	writeExecutable(t, filepath.Join(binDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$WK_FINALIZE_CALL_LOG"
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
    printf '%s\n' '{"schema":"wukongim/cloud-simulation-finalize/v1","run_id":"gh-12345-1","active_until":"2000-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z"}' >"$destination/finalize.json"
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
    ;;
  "run list")
    title="$(cat "$WK_FINALIZE_STATE_DIR/cleanup-title")"
    printf '[{"databaseId":9001,"displayTitle":"%s"}]\n' "$title"
    ;;
  "run watch") exit 0 ;;
  *) printf 'unexpected gh call: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	callLog := filepath.Join(stateDir, "calls.log")
	command := exec.Command("bash", filepath.Join(scriptDir, "finalize.sh"), "gh-12345-1",
		"--repository", "example/project", "--allow-fix-pr")
	command.Dir = temp
	command.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"WK_FINALIZE_CALL_LOG="+callLog,
		"WK_FINALIZE_STATE_DIR="+stateDir,
		"WK_FINALIZE_MODE="+mode,
	)
	outputBytes, commandErr := command.CombinedOutput()
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
