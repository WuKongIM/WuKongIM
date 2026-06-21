package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWkcliSimSmokeScriptDryRunPrintsNodeAndSimulatorCommands(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongimv2.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--api-addr", "http://127.0.0.1:15001",
		"--gateway-addr", "127.0.0.1:15100",
		"--cluster-addr", "127.0.0.1:17001",
		"--users", "10",
		"--groups", "2",
		"--members", "5",
		"--rate", "5/s",
		"--duration", "5s",
	)
	cmd.Dir = root
	cmd.Env = envWithout("WK_DEBUG_API_ENABLE")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"api_addr=http://127.0.0.1:15001",
		"gateway_addr=127.0.0.1:15100",
		"cluster_addr=127.0.0.1:17001",
		"node_data_dir=" + filepath.Join(outDir, "node"),
		"node_log=" + filepath.Join(outDir, "node.log"),
		"sim_output=" + filepath.Join(outDir, "sim.jsonl"),
		"snapshot_output=" + filepath.Join(outDir, "bench-snapshot.json"),
		"node_cmd=env",
		"WK_BENCH_API_ENABLE=true",
		"go run ./cmd/wukongimv2",
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:15001 --users 10 --groups 2 --group-members 5 --rate 5/s --max-runtime 5s",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptDryRunPrintsClusterAndSimulatorCommands(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	startScript := filepath.Join(t.TempDir(), "start-three.sh")

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		"--ready-timeout", "7",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"api_addrs=http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"gateway_addrs=127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"cluster_log=" + filepath.Join(outDir, "cluster.log"),
		"node_log_dir=" + filepath.Join(outDir, "node-logs"),
		"sim_output=" + filepath.Join(outDir, "sim.jsonl"),
		"snapshot_output_dir=" + filepath.Join(outDir, "bench-snapshots"),
		"metrics_output_dir=" + filepath.Join(outDir, "metrics"),
		"max_flush_error_selected_rows=0",
		"max_handoff_error_total=0",
		"max_handoff_timeout_total=0",
		"max_goroutines=2000",
		"max_heap_alloc_bytes=4294967296",
		"start_cmd=env WK_DEBUG_API_ENABLE=true " + startScript + " --clean --ready-timeout 7 --bin " + filepath.Join(outDir, "wukongimv2") + " --log-dir " + filepath.Join(outDir, "node-logs"),
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013 --gateway 127.0.0.1:5111 --gateway 127.0.0.1:5112 --gateway 127.0.0.1:5113 --users 12 --groups 3 --group-members 4 --rate 6/s --max-runtime 4s",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptAllowsFollowerSnapshotsWithoutCounts(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh",
		"--no-start",
		"--out-dir", outDir,
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		"--ready-timeout", "2",
		"--poll", "0",
	)
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_DEBUG_API_ENABLE"), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "smoke passed") {
		t.Fatalf("script output missing success marker:\n%s", output)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- state: stopped",
		"- messages_sent: 9",
		"- send_errors: 0",
		"- snapshots: bench-snapshots/",
		"- metrics: metrics/",
		"- max_flush_error_selected_rows: 0",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	node2Snapshot := readFile(t, filepath.Join(outDir, "bench-snapshots", "node2.json"))
	if strings.Contains(node2Snapshot, "accepted_channels") {
		t.Fatalf("test fixture should model an empty follower snapshot, got:\n%s", node2Snapshot)
	}
}

func TestWkcliSimThreeNodeSmokeScriptFailsOnConversationActiveMetricGate(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh",
		"--no-start",
		"--out-dir", outDir,
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		"--ready-timeout", "2",
		"--poll", "0",
	)
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_DEBUG_API_ENABLE"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_THREE_NODE_SIM_METRICS_BAD=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when conversation active metrics exceed gates:\n%s", output)
	}
	if !strings.Contains(string(output), "conversation_active selected_error_rows=5 exceeds limit 0") {
		t.Fatalf("failure output missing selected-error gate:\n%s", output)
	}
}

func TestWkcliSimThreeNodeSmokeScriptStopsClusterAndPrintsEvidenceWhenSimFails(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	startScript := filepath.Join(binDir, "start-three.sh")
	writeFakeThreeNodeSimFailingGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeThreeNodeSimStartScript(t, startScript, callsDir)
	t.Cleanup(func() {
		terminateRecordedProcess(t, filepath.Join(callsDir, "start.pid"))
	})

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		"--ready-timeout", "2",
		"--poll", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when wkcli sim fails:\n%s", output)
	}
	text := string(output)
	for _, want := range []string{
		"wkcli sim failed with status 3",
		"--- cluster log:",
		"fake cluster running",
		"--- node log:",
		"fake node1 log",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure output missing %q:\n%s", want, text)
		}
	}
	if !waitForFile(filepath.Join(callsDir, "start.term")) {
		t.Fatalf("start script did not receive TERM; calls dir: %s", callsDir)
	}
	debugAPI := strings.TrimSpace(readFile(t, filepath.Join(callsDir, "start.debug_api")))
	if debugAPI != "true" {
		t.Fatalf("start script WK_DEBUG_API_ENABLE=%q, want true", debugAPI)
	}
}

func writeFakeThreeNodeSimGo(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/go.calls"
if [[ "${1:-}" == "run" && "${2:-}" == "./cmd/wkcli" && "${3:-}" == "sim" ]]; then
  cat <<'JSON'
{"state":"running","run_id":"test-run","target_servers":["http://127.0.0.1:5011","http://127.0.0.1:5012","http://127.0.0.1:5013"],"gateway_tcp_addrs":["127.0.0.1:5111","127.0.0.1:5112","127.0.0.1:5113"],"users":12,"active_users":12,"groups":3,"group_members":4,"messages_sent":3,"send_errors":0,"recv_messages":0,"recv_dropped":0,"reconnects":0,"last_error":"","last_transition_at":"2026-06-17T00:00:00Z"}
{"state":"stopped","run_id":"test-run","target_servers":["http://127.0.0.1:5011","http://127.0.0.1:5012","http://127.0.0.1:5013"],"gateway_tcp_addrs":["127.0.0.1:5111","127.0.0.1:5112","127.0.0.1:5113"],"users":12,"active_users":12,"groups":3,"group_members":4,"messages_sent":9,"send_errors":0,"recv_messages":0,"recv_dropped":0,"reconnects":0,"last_error":"","last_transition_at":"2026-06-17T00:00:04Z"}
JSON
  exit 0
fi
echo "unexpected go args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeThreeNodeSimFailingGo(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/go.calls"
if [[ "${1:-}" == "run" && "${2:-}" == "./cmd/wkcli" && "${3:-}" == "sim" ]]; then
  cat <<'JSON'
{"state":"running","run_id":"test-run","target_servers":["http://127.0.0.1:5011","http://127.0.0.1:5012","http://127.0.0.1:5013"],"gateway_tcp_addrs":["127.0.0.1:5111","127.0.0.1:5112","127.0.0.1:5113"],"users":12,"active_users":12,"groups":3,"group_members":4,"messages_sent":3,"send_errors":0,"recv_messages":0,"recv_dropped":0,"reconnects":0,"last_error":"","last_transition_at":"2026-06-17T00:00:00Z"}
{"state":"stopped","run_id":"test-run","target_servers":["http://127.0.0.1:5011","http://127.0.0.1:5012","http://127.0.0.1:5013"],"gateway_tcp_addrs":["127.0.0.1:5111","127.0.0.1:5112","127.0.0.1:5113"],"users":12,"active_users":12,"groups":3,"group_members":4,"messages_sent":3,"send_errors":1,"recv_messages":0,"recv_dropped":0,"reconnects":0,"last_error":"fake failure","last_transition_at":"2026-06-17T00:00:04Z"}
JSON
  exit 3
fi
echo "unexpected go args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeThreeNodeSimStartScript(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$$" > "` + callsDir + `/start.pid"
printf '%s\n' "${WK_DEBUG_API_ENABLE-}" > "` + callsDir + `/start.debug_api"
printf '%s\n' "$*" >> "` + callsDir + `/start.calls"
log_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --log-dir)
      log_dir="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
mkdir -p "$log_dir"
printf 'fake node1 log\n' > "$log_dir/node1.log"
printf 'fake node2 log\n' > "$log_dir/node2.log"
printf 'fake node3 log\n' > "$log_dir/node3.log"
trap 'printf term > "` + callsDir + `/start.term"; exit 0' TERM INT
echo fake cluster running
while true; do
  sleep 1
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitForFile(path string) bool {
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func terminateRecordedProcess(t *testing.T, pidPath string) {
	t.Helper()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return
	}
	_ = exec.Command("kill", "-TERM", pid).Run()
}

func writeFakeThreeNodeSimCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/curl.calls"
url=""
for arg in "$@"; do
  url="$arg"
done
case "$url" in
  */readyz)
    echo ok
    ;;
  */bench/v1/capabilities)
    echo '{"version":"bench/v1","enabled":true,"features":{"channels_batch":true,"channel_subscribers_batch":true,"snapshot":true},"channel_types":["group"]}'
    ;;
  http://127.0.0.1:5011/bench/v1/capacity-target)
    echo '{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:5111"}}'
    ;;
  http://127.0.0.1:5012/bench/v1/capacity-target)
    echo '{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:5112"}}'
    ;;
  http://127.0.0.1:5013/bench/v1/capacity-target)
    echo '{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:5113"}}'
    ;;
  http://127.0.0.1:5011/bench/v1/snapshot)
    echo '{"version":"bench/v1","counts":{"accepted_channels":3,"accepted_subscriber_items":3,"accepted_subscribers":12}}'
    ;;
  http://127.0.0.1:5012/bench/v1/snapshot|http://127.0.0.1:5013/bench/v1/snapshot)
    echo '{"version":"bench/v1"}'
    ;;
  */metrics)
    count_file="` + callsDir + `/metrics.count"
    count=0
    if [[ -f "$count_file" ]]; then
      count="$(cat "$count_file")"
    fi
    count=$((count + 1))
    printf '%s\n' "$count" > "$count_file"
    selected_error=0
    if [[ "${WK_FAKE_THREE_NODE_SIM_METRICS_BAD:-}" == "1" && "$count" -gt 3 ]]; then
      selected_error=5
    fi
    cat <<METRICS
go_goroutines 100
go_memstats_heap_alloc_bytes 1000
wukongim_conversation_active_flush_rows_sum{kind="selected",result="error"} ${selected_error}
wukongim_conversation_authority_handoff_total{result="error"} 0
wukongim_conversation_authority_handoff_total{result="timeout"} 0
METRICS
    ;;
  *)
    echo "unexpected curl url: $url" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
