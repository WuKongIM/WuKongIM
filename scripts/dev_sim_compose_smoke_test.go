package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDevSimComposeSmokeRetriesUpAndChecksStatusAndLogs(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	writeFakeDocker(t, filepath.Join(binDir, "docker"), callsDir)
	writeFakeCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/dev-sim-compose-smoke.sh", "--timeout", "5", "--poll", "0", "--log-tail", "200")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_DEV_SIM_UP_RETRIES=2",
		"WK_DEV_SIM_UP_RETRY_BACKOFF=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "dev-sim smoke passed") {
		t.Fatalf("script output missing pass marker:\n%s", output)
	}

	dockerCalls := readFile(t, filepath.Join(callsDir, "docker.calls"))
	wantUp := "compose --profile dev-sim up -d --build wk-node1 wk-node2 wk-node3 wk-sim"
	if got := strings.Count(dockerCalls, wantUp); got != 2 {
		t.Fatalf("expected docker up to be retried once, got %d calls\n%s", got, dockerCalls)
	}
	if !strings.Contains(dockerCalls, "compose --profile dev-sim logs --tail=200 wk-sim wk-node1 wk-node2 wk-node3") {
		t.Fatalf("expected log inspection call, got:\n%s", dockerCalls)
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	if got := strings.Count(curlCalls, "http://127.0.0.1:19091/status"); got < 2 {
		t.Fatalf("expected status polling at least twice, got %d calls\n%s", got, curlCalls)
	}
}

func TestDevSimComposeSmokeNoBuildOmitsBuildFlag(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	writeFakeDockerNoBuild(t, filepath.Join(binDir, "docker"), callsDir)
	writeFakeCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/dev-sim-compose-smoke.sh", "--no-build", "--skip-logs", "--timeout", "5", "--poll", "0")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_DEV_SIM_UP_RETRIES=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	dockerCalls := readFile(t, filepath.Join(callsDir, "docker.calls"))
	if strings.Contains(dockerCalls, "--build") {
		t.Fatalf("expected --no-build to omit --build, got:\n%s", dockerCalls)
	}
	if !strings.Contains(dockerCalls, "compose --profile dev-sim up -d wk-node1 wk-node2 wk-node3 wk-sim") {
		t.Fatalf("expected no-build compose up call, got:\n%s", dockerCalls)
	}
}

func TestDevSimComposeSmokeRejectsStatusErrorCounters(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	writeFakeDockerNoBuild(t, filepath.Join(binDir, "docker"), callsDir)
	writeFakeCurlWithTrafficErrors(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/dev-sim-compose-smoke.sh", "--no-build", "--skip-logs", "--timeout", "1", "--poll", "0")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_DEV_SIM_UP_RETRIES=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when /status reports send or recv errors:\n%s", output)
	}
	if !strings.Contains(string(output), "send_errors=2") || !strings.Contains(string(output), "recv_errors=1") {
		t.Fatalf("script output should include error counters for diagnostics:\n%s", output)
	}
}

func TestDevSimComposeSmokeTrustsStatusCountersForTraffic(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "dev-sim-compose-smoke.sh"))

	if strings.Contains(script, "recent logs do not show simulator traffic markers") ||
		strings.Contains(script, "sim-msg|delivery[.]diag[.]committed_route") {
		t.Fatal("dev-sim smoke should not require debug log traffic markers after /status reports running traffic")
	}
	if !strings.Contains(script, "recent logs contain panic markers") {
		t.Fatal("dev-sim smoke should still fail on panic markers in recent logs")
	}
}

func TestDevSimComposeSmokeDefaultTimeoutCoversHighTrafficStartup(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "dev-sim-compose-smoke.sh"))

	if !strings.Contains(script, `READY_TIMEOUT="${WK_DEV_SIM_READY_TIMEOUT:-180}"`) {
		t.Fatal("default dev-sim smoke timeout should cover the 1000-user Compose startup profile")
	}
	if !strings.Contains(script, "Default: WK_DEV_SIM_READY_TIMEOUT or 180.") {
		t.Fatal("usage text should document the default dev-sim smoke timeout")
	}
}

func writeFakeDocker(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/docker.calls"
case "$*" in
  "compose --profile dev-sim up -d --build wk-node1 wk-node2 wk-node3 wk-sim")
    count_file="$calls_dir/up.count"
    count=0
    [[ -f "$count_file" ]] && count=$(cat "$count_file")
    count=$((count + 1))
    echo "$count" > "$count_file"
    if [[ "$count" -eq 1 ]]; then
      echo "simulated transient build failure" >&2
      exit 1
    fi
    exit 0
    ;;
  "compose --profile dev-sim logs --tail=200 wk-sim wk-node1 wk-node2 wk-node3")
    echo 'wk-node1 | normal startup line without traffic debug marker'
    exit 0
    ;;
  *)
    echo "unexpected docker args: $*" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeDockerNoBuild(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/docker.calls"
case "$*" in
  "compose --profile dev-sim up -d wk-node1 wk-node2 wk-node3 wk-sim")
    exit 0
    ;;
  *)
    echo "unexpected docker args: $*" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
count_file="$calls_dir/curl.count"
count=0
[[ -f "$count_file" ]] && count=$(cat "$count_file")
count=$((count + 1))
echo "$count" > "$count_file"
if [[ "$count" -eq 1 ]]; then
  echo '{"state":"waiting","connected_users":0,"messages_sent":0,"last_error":"not ready"}'
else
  echo '{"state":"running","connected_users":20,"active_users":18,"person_channels":5,"group_channels":2,"messages_sent":3,"last_error":""}'
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeCurlWithTrafficErrors(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
echo '{"state":"running","connected_users":20,"active_users":19,"person_channels":5,"group_channels":2,"messages_sent":3,"send_errors":2,"recv_errors":1,"last_error":"send failed"}'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDevSimComposeSmokeReportsActiveUsers(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "dev-sim-compose-smoke.sh"))
	if !strings.Contains(script, "active_users") {
		t.Fatal("dev-sim smoke should report active_users from /status")
	}
	if !strings.Contains(script, "(( connected > 0 ))") || !strings.Contains(script, "(( active > 0 ))") {
		t.Fatal("dev-sim smoke should require active users before passing")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// Nearly every shell-script test is isolated under t.TempDir. Run top-level
	// cases through one bounded parallel pool so fault-injection sleeps and
	// subprocess startup do not accumulate serially across the whole package.
	// Subtests are excluded to avoid a parent holding a pool slot while waiting
	// for its own parallel child.
	if !strings.Contains(t.Name(), "/") {
		runHeavyShellScriptTestInParallel(t)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func TestDockerComposeDevSimDefaultsTargetHighTraffic(t *testing.T) {
	compose := readFile(t, filepath.Join(repoRoot(t), "docker-compose.yml"))

	for _, want := range []string{
		"WK_SIM_USERS: ${WK_SIM_USERS:-1000}",
		"WK_SIM_PERSON_CHANNELS: ${WK_SIM_PERSON_CHANNELS:-500}",
		"WK_SIM_GROUP_CHANNELS: ${WK_SIM_GROUP_CHANNELS:-500}",
		"WK_SIM_GROUP_MEMBERS: ${WK_SIM_GROUP_MEMBERS:-10}",
		"WK_SIM_RATE: ${WK_SIM_RATE:-0.25/s}",
		"WK_SIM_TRAFFIC_CONCURRENCY: ${WK_SIM_TRAFFIC_CONCURRENCY:-128}",
		"WK_SIM_VERIFY_RECV: ${WK_SIM_VERIFY_RECV:-none}",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("docker-compose.yml missing high-traffic dev-sim default %q", want)
		}
	}
}
