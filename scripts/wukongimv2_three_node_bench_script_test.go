package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMV2ThreeNode10kBenchScriptSetsEvidenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-three-nodes-10kch.sh"))

	for _, want := range []string{
		`CHANNELS="${WK_BENCH_ACTIVATE_CHANNELS:-10000}"`,
		`USERS="${WK_BENCH_ACTIVATE_USERS:-1000}"`,
		`CONNECT_RATE="${WK_BENCH_ACTIVATE_CONNECT_RATE:-500}"`,
		`ACTIVATION_WINDOW="${WK_BENCH_ACTIVATE_WINDOW:-120s}"`,
		`STABLE_P99="${WK_BENCH_ACTIVATE_STABLE_P99:-2s}"`,
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("10k activate script missing default %q", want)
		}
	}
}

func TestWukongIMV2ThreeNodeActivateScriptCollectsServerProcessResources(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeActivateWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-10kch.sh",
		"--no-start",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--channels", "10",
		"--users", "10",
		"--activation-window", "1s",
		"--hold", "0s",
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

	samples := readFile(t, filepath.Join(outDir, "resources", "server-process.jsonl"))
	for _, want := range []string{
		`"node":"node1"`,
		`"pid":111`,
		`"cpu_percent":12.500`,
		`"rss_kb":123456`,
		`"phase":"before"`,
		`"phase":"after"`,
	} {
		if !strings.Contains(samples, want) {
			t.Fatalf("resource samples missing %q:\n%s", want, samples)
		}
	}
	summary := readFile(t, filepath.Join(outDir, "resources", "server-process-summary.tsv"))
	for _, want := range []string{
		"node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb",
		"node1\t111\t2\t12.500\t12.500\t1.200\t1.200\t123456\t789000",
		"node2\t222\t2\t25.000\t25.000\t2.300\t2.300\t234567\t890000",
		"node3\t333\t2\t3.500\t3.500\t0.700\t0.700\t345678\t901000",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("resource summary missing %q:\n%s", want, summary)
		}
	}
	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(topSummary, "- resources: resources/") {
		t.Fatalf("top-level summary should point to resources evidence:\n%s", topSummary)
	}
}

func TestWukongIMV2ThreeNodeActivateScriptClassifiesMetricsEvidence(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeActivateWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-10kch.sh",
		"--no-start",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--channels", "10",
		"--users", "10",
		"--activation-window", "1s",
		"--hold", "0s",
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

	for _, rel := range []string{
		"metrics/127_0_0_1_5011-classify.txt",
		"metrics/127_0_0_1_5012-classify.txt",
		"metrics/127_0_0_1_5013-classify.txt",
	} {
		text := readFile(t, filepath.Join(outDir, rel))
		if !strings.Contains(text, "classification: fake") {
			t.Fatalf("classification file %s missing fake output:\n%s", rel, text)
		}
	}

	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	for _, want := range []string{
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5011-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5011-after.prom"),
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5012-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5012-after.prom"),
		"metrics classify --before " + filepath.Join(outDir, "metrics", "127_0_0_1_5013-before.prom") + " --after " + filepath.Join(outDir, "metrics", "127_0_0_1_5013-after.prom"),
	} {
		if !strings.Contains(wkbenchCalls, want) {
			t.Fatalf("wkbench calls missing %q:\n%s", want, wkbenchCalls)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(topSummary, "- metrics_classification: metrics/*-classify.txt") {
		t.Fatalf("top-level summary should point to metrics classification evidence:\n%s", topSummary)
	}
}

func TestWukongIMV2ThreeNodeBenchScriptCollectsLocalEvidence(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-three-nodes-1000ch.sh"))

	for _, want := range []string{
		"scripts/start-wukongimv2-three-nodes.sh",
		"collect_node_logs",
		"capture_node_pprof",
		"channelv2_metrics_summary",
		"scripts/channelv2-metrics-summary.awk",
		"channelv2_metrics_summary.tsv",
		"write_evidence_summary",
		"/debug/pprof/goroutine?debug=2",
		"/debug/pprof/heap",
		"summary.md",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bench script missing local evidence hook %q", want)
		}
	}
	if strings.Contains(script, "docker compose") {
		t.Fatalf("three-node bench script should use local startup scripts, not docker compose")
	}
}

func TestChannelV2MetricsSummaryAwkSummarizesBeforeAfterPrometheus(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `# HELP ignored ignored
wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="0",role="leader"} 5
wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="1",role="follower"} 7
wukongim_channelv2_follower_parked{node_id="1",node_name="node-1",reactor_id="0"} 2
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="0",priority="normal"} 3
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="1",priority="normal"} 6
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="store_append"} 2
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="rpc"} 4
wukongim_channelv2_activation_rejected_total{node_id="1",node_name="node-1",reason="max_channels"} 1
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="submitted"} 10
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="ok"} 4
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="err"} 1
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="false"} 20
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="true"} 8
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="err",empty="false"} 2
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="ok"} 6
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="err"} 1
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="hit"} 100
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="miss"} 3
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="invalidate"} 1
wukongim_channelv2_append_duration_seconds_count{node_id="1",node_name="node-1",commit_mode="local"} 20
wukongim_channelv2_append_duration_seconds_sum{node_id="1",node_name="node-1",commit_mode="local"} 0.100
wukongim_channelv2_append_batch_records_count{node_id="1",node_name="node-1"} 5
wukongim_channelv2_append_batch_records_sum{node_id="1",node_name="node-1"} 25
wukongim_channelv2_append_batch_bytes_count{node_id="1",node_name="node-1"} 5
wukongim_channelv2_append_batch_bytes_sum{node_id="1",node_name="node-1"} 500
wukongim_channelv2_append_batch_wait_duration_seconds_count{node_id="1",node_name="node-1"} 5
wukongim_channelv2_append_batch_wait_duration_seconds_sum{node_id="1",node_name="node-1"} 0.015
wukongim_channelv2_worker_task_duration_seconds_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 40
wukongim_channelv2_worker_task_duration_seconds_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 0.200
`)
	writeFile(t, after, `wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="0",role="leader"} 8
wukongim_channelv2_active_runtimes{node_id="1",node_name="node-1",reactor_id="1",role="follower"} 9
wukongim_channelv2_follower_parked{node_id="1",node_name="node-1",reactor_id="0"} 4
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="0",priority="normal"} 12
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="1",priority="normal"} 6
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="store_append"} 6
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="rpc"} 5
wukongim_channelv2_activation_rejected_total{node_id="1",node_name="node-1",reason="max_channels"} 3
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="submitted"} 15
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="ok"} 9
wukongim_channelv2_recovery_probe_total{node_id="1",node_name="node-1",result="err"} 2
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="false"} 35
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="ok",empty="true"} 10
wukongim_channelv2_pull_total{node_id="1",node_name="node-1",result="err",empty="false"} 5
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="ok"} 16
wukongim_channelv2_rpc_pull_total{node_id="1",node_name="node-1",result="err"} 2
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="hit"} 160
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="miss"} 13
wukongim_channelv2_meta_cache_total{node_id="1",node_name="node-1",result="invalidate"} 4
wukongim_channelv2_append_duration_seconds_count{node_id="1",node_name="node-1",commit_mode="local"} 30
wukongim_channelv2_append_duration_seconds_sum{node_id="1",node_name="node-1",commit_mode="local"} 0.160
wukongim_channelv2_append_batch_records_count{node_id="1",node_name="node-1"} 8
wukongim_channelv2_append_batch_records_sum{node_id="1",node_name="node-1"} 37
wukongim_channelv2_append_batch_bytes_count{node_id="1",node_name="node-1"} 8
wukongim_channelv2_append_batch_bytes_sum{node_id="1",node_name="node-1"} 1100
wukongim_channelv2_append_batch_wait_duration_seconds_count{node_id="1",node_name="node-1"} 8
wukongim_channelv2_append_batch_wait_duration_seconds_sum{node_id="1",node_name="node-1"} 0.027
wukongim_channelv2_worker_task_duration_seconds_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 55
wukongim_channelv2_worker_task_duration_seconds_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 0.320
`)

	cmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=10",
		"-f", filepath.Join(root, "scripts", "channelv2-metrics-summary.awk"),
		before,
		after,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("summary awk failed: %v\n%s", err, output)
	}

	want := "qps_1000\tnode1\t17\t8\t9\t4\t12\t6\t2\t5\t5\t1\t15\t2\t3\t10\t1\t1.100\t60\t10\t3\t10\t6.000\t3\t4.000\t200.000\t4.000\t15\t8.000\n"
	if string(output) != want {
		t.Fatalf("unexpected summary row:\nwant %q\n got %q", want, output)
	}
}

func writeFakeBenchBase(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" > "` + callsDir + `/args.txt"
env | LC_ALL=C sort | grep -E '^(WK_BENCH_|WK_CLUSTER_|WK_PPROF_)' > "` + callsDir + `/env.txt"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeActivateWkbench(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" > "` + callsDir + `/wkbench.args"
printf '%s\n' "$*" >> "` + callsDir + `/wkbench.calls"
if [[ "${1:-}" == "metrics" && "${2:-}" == "classify" ]]; then
  echo 'classification: fake'
  exit 0
fi
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
if [[ -n "$report_dir" ]]; then
  mkdir -p "$report_dir"
  echo '{"status":"passed"}' > "$report_dir/activation_report.json"
  echo '# fake summary' > "$report_dir/summary.md"
fi
echo 'wkbench activate-channels'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeActivateCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
echo "$*" >> "` + callsDir + `/curl.calls"
url="${@: -1}"
case "$url" in
  http://127.0.0.1:501*/readyz)
    echo 'ok'
    ;;
  http://127.0.0.1:501*/metrics)
    echo 'metric 1'
    ;;
  http://127.0.0.1:501*/debug/pprof/goroutine?debug=2)
    echo 'goroutine profile'
    ;;
  http://127.0.0.1:501*/debug/pprof/heap)
    echo 'heap profile'
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

func writeFakeActivatePgrep(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
echo "$*" >> "` + callsDir + `/pgrep.calls"
case "$*" in
  *wukongimv2-node1.conf*) echo 111 ;;
  *wukongimv2-node2.conf*) echo 222 ;;
  *wukongimv2-node3.conf*) echo 333 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeActivatePS(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
echo "$*" >> "` + callsDir + `/ps.calls"
pid=""
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "-p" ]]; then
    pid="$2"
    shift 2
    continue
  fi
  shift
done
case "$pid" in
  111) echo ' 12.5  1.2 123456 789000 00:10 /tmp/wukongimv2-node1' ;;
  222) echo ' 25.0  2.3 234567 890000 00:20 /tmp/wukongimv2-node2' ;;
  333) echo '  3.5  0.7 345678 901000 00:30 /tmp/wukongimv2-node3' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
