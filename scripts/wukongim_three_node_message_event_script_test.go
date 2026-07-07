package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNodeMessageEventScriptSetsSafeDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-message-event.sh"))

	for _, want := range []string{
		`PROFILE="${WK_BENCH_MESSAGE_EVENT_PROFILE:-smoke}"`,
		`CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-10}"`,
		`CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-256}"`,
		`apply_profile_defaults`,
		`smoke)`,
		`profile_channels=32`,
		`medium)`,
		`profile_channels=1000`,
		`pressure)`,
		`profile_channels=10000`,
		`LIVE_METRICS_INTERVAL="${WK_BENCH_MESSAGE_EVENT_LIVE_METRICS_INTERVAL:-1}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("message-event script missing default %q", want)
		}
	}
}

func TestWukongIMThreeNodeMessageEventScriptDryRunBuildsCommandAndEvidence(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeMessageEventWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-message-event.sh",
		"--no-start",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--run-id", "message-event-test",
		"--channels", "4",
		"--streams-per-channel", "3",
		"--lanes-per-stream", "2",
		"--deltas-per-lane", "5",
		"--payload-bytes", "96",
		"--concurrency", "7",
		"--request-timeout", "3s",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_RESOURCE_SAMPLE_INTERVAL=0",
		"WK_BENCH_MESSAGE_EVENT_LIVE_METRICS_INTERVAL=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	for _, want := range []string{
		"capacity message-event --api http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013 --run-id message-event-test --channels 4 --streams-per-channel 3 --lanes-per-stream 2 --deltas-per-lane 5 --payload-bytes 96 --concurrency 7 --request-timeout 3s --report-dir " + filepath.Join(outDir, "report"),
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5011-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5011-after.prom"),
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5012-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5012-after.prom"),
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5013-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5013-after.prom"),
	} {
		if !strings.Contains(wkbenchCalls, want) {
			t.Fatalf("wkbench calls missing %q:\n%s", want, wkbenchCalls)
		}
	}

	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"# Three-Node Message Event Evidence",
		"- workload: local wukongim three-node cluster wkbench message-event",
		"- metrics_classification: metrics/*-classify.txt",
		"- debug_config: config/*-debug-config.json",
		"- debug_cluster: config/*-debug-cluster.json",
		"- live_metrics_summary: metrics/live-summary.tsv",
		"- message_event_gates: report/summary.md",
		"- message_event_report: report/message_event_report.json",
		"- message_event_summary: report/summary.md",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}

	debugConfig := readFile(t, filepath.Join(outDir, "config", "127_0_0_1_5011-debug-config.json"))
	if !strings.Contains(debugConfig, `"node_id":1`) {
		t.Fatalf("debug config missing node id evidence:\n%s", debugConfig)
	}
	debugCluster := readFile(t, filepath.Join(outDir, "config", "127_0_0_1_5011-debug-cluster.json"))
	if !strings.Contains(debugCluster, `"initial_slot_count":10`) || !strings.Contains(debugCluster, `"hash_slot_count":256`) {
		t.Fatalf("debug cluster missing slot count evidence:\n%s", debugCluster)
	}
}

func TestWukongIMThreeNodeMessageEventScriptStartsWithCurrentClusterSlotDefaults(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	startScript := filepath.Join(binDir, "start-three.sh")
	writeFakeMessageEventWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeMessageEventStartScript(t, startScript, callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-message-event.sh",
		"--start-script", startScript,
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--run-id", "message-event-start-defaults",
		"--ready-timeout", "2",
		"--channels", "1",
		"--streams-per-channel", "1",
		"--lanes-per-stream", "1",
		"--deltas-per-lane", "1",
		"--live-metrics-interval", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_RESOURCE_SAMPLE_INTERVAL=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	startEnv := readFile(t, filepath.Join(callsDir, "start.env"))
	for _, want := range []string{
		"WK_CLUSTER_INITIAL_SLOT_COUNT=10",
		"WK_CLUSTER_HASH_SLOT_COUNT=256",
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT=32",
	} {
		if !strings.Contains(startEnv, want) {
			t.Fatalf("start env missing %q:\n%s", want, startEnv)
		}
	}

	envText := readFile(t, filepath.Join(outDir, "env.txt"))
	for _, want := range []string{
		"CLUSTER_INITIAL_SLOT_COUNT=10",
		"CLUSTER_HASH_SLOT_COUNT=256",
		"CLUSTER_CHANNEL_REACTOR_COUNT=32",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf("env evidence missing %q:\n%s", want, envText)
		}
	}
}

func TestWukongIMThreeNodeMessageEventScriptAppliesProfileWarmupAndEvidenceGates(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeMessageEventWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-message-event.sh",
		"--no-start",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--run-id", "message-event-medium",
		"--profile", "medium",
		"--warm-channels",
		"--warm-runtime",
		"--live-metrics-interval", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_RESOURCE_SAMPLE_INTERVAL=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	for _, want := range []string{
		"capacity message-event --api http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013 --run-id message-event-medium --channels 1000 --streams-per-channel 2 --lanes-per-stream 2 --deltas-per-lane 4 --payload-bytes 128 --concurrency 512 --request-timeout 15s --report-dir " + filepath.Join(outDir, "report") + " --warm-channels --warm-runtime",
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5011-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5011-after.prom"),
	} {
		if !strings.Contains(wkbenchCalls, want) {
			t.Fatalf("wkbench calls missing %q:\n%s", want, wkbenchCalls)
		}
	}

	envText := readFile(t, filepath.Join(outDir, "env.txt"))
	for _, want := range []string{
		"PROFILE=medium",
		"WARM_CHANNELS=1",
		"WARM_RUNTIME=1",
		"LIVE_METRICS_INTERVAL=0",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf("env missing %q:\n%s", want, envText)
		}
	}

	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- profile: medium",
		"- warm_channels: 1",
		"- warm_runtime: 1",
		"- live_metrics_summary: metrics/live-summary.tsv",
		"- message_event_gates: report/summary.md",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func writeFakeMessageEventWkbench(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/wkbench.calls"
if [[ "${1:-}" == "metrics" && "${2:-}" == "classify" ]]; then
  echo 'classification: message-event-fake'
  echo 'message_event_stream_cache_sessions_max: 2'
  echo 'message_event_stream_cache_open_lanes_max: 4'
  echo 'message_event_propose_count{path="finish_batch"}: 12'
  echo 'message_event_cache_miss_count: 0'
  exit 0
fi
if [[ "${1:-}" == "capacity" && "${2:-}" == "message-event" ]]; then
  report_dir=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --report-dir)
        report_dir="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  if [[ -z "$report_dir" ]]; then
    echo "missing report dir" >&2
    exit 2
  fi
  mkdir -p "$report_dir"
  cat >"$report_dir/message_event_report.json" <<'JSON'
{"status":"passed","shape":{"streams":12,"delta_events":120,"finish_events":12,"expected_durable_events":36,"expected_finish_proposals":12},"requests":{"errors":0}}
JSON
  cat >"$report_dir/summary.md" <<'MD'
# wkbench message-event
- status: passed
MD
  echo 'wkbench message-event: passed streams=12 deltas=120 finishes=12 errors=0 duration=1s report='"$report_dir"
  exit 0
fi
echo "unexpected wkbench args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeMessageEventStartScript(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/start.calls"
{
  printf 'WK_CLUSTER_INITIAL_SLOT_COUNT=%s\n' "${WK_CLUSTER_INITIAL_SLOT_COUNT:-}"
  printf 'WK_CLUSTER_HASH_SLOT_COUNT=%s\n' "${WK_CLUSTER_HASH_SLOT_COUNT:-}"
  printf 'WK_CLUSTER_CHANNEL_REACTOR_COUNT=%s\n' "${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-}"
} >> "` + callsDir + `/start.env"
if [[ "${1:-}" == "--dry-run" ]]; then
  echo 'fake dry run'
  exit 0
fi
trap 'exit 0' TERM INT
while true; do
  sleep 1
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
