package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestWukongIMV2ThreeNodeActivateScriptRebuildsStaleWkbenchBinary(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	wkbenchPath := filepath.Join(binDir, "wkbench")
	writeFakeActivateWkbench(t, wkbenchPath, callsDir)
	old := time.Unix(946684800, 0)
	if err := os.Chtimes(wkbenchPath, old, old); err != nil {
		t.Fatal(err)
	}
	writeFakeActivateGoBuildWkbench(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-10kch.sh",
		"--no-start",
		"--out-dir", outDir,
		"--wkbench-bin", wkbenchPath,
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

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+wkbenchPath+" ./cmd/wkbench") {
		t.Fatalf("stale wkbench binary should be rebuilt, calls:\n%s", goCalls)
	}
	classify := readFile(t, filepath.Join(outDir, "metrics", "127_0_0_1_5011-classify.txt"))
	if !strings.Contains(classify, "classification: rebuilt") {
		t.Fatalf("classification should use rebuilt wkbench binary:\n%s", classify)
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

func TestWukongIMV2ThreeNodeActivateScriptFailsOnMetricsHealthGate(t *testing.T) {
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
		"WK_FAKE_ACTIVATE_CLASSIFY_BAD=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when metrics health gates fail:\n%s", output)
	}

	health := readFile(t, filepath.Join(outDir, "metrics", "health-gates.txt"))
	for _, want := range []string{
		"status: failed",
		"pending_meta_current_max",
		"need_meta_pull_retry_count",
		"pull_hint_err_count",
	} {
		if !strings.Contains(health, want) {
			t.Fatalf("health gates missing %q:\n%s", want, health)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- metrics_health: metrics/health-gates.txt",
		"- metrics_health_status: failed",
		"- exit_status: 7",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("top-level summary missing %q:\n%s", want, topSummary)
		}
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

func TestWukongIMV2ThreeNodePresenceScriptSetsPresenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-three-nodes-presence.sh"))

	for _, want := range []string{
		`USERS="${WK_BENCH_PRESENCE_USERS:-1000}"`,
		`CONNECT_RATE="${WK_BENCH_PRESENCE_CONNECT_RATE:-500}"`,
		`HEARTBEAT_INTERVAL="${WK_BENCH_PRESENCE_HEARTBEAT_INTERVAL:-1s}"`,
		`SAMPLE_INTERVAL="${WK_BENCH_PRESENCE_SAMPLE_INTERVAL:-1}"`,
		`STABLE_SAMPLES="${WK_BENCH_PRESENCE_STABLE_SAMPLES:-2}"`,
		`REQUIRE_TOUCH="${WK_BENCH_PRESENCE_REQUIRE_TOUCH:-1}"`,
		`validate_presence_report`,
		`presence-samples.jsonl`,
		`presence-summary.tsv`,
		`"$ROOT_DIR/pkg/protocol"`,
		`WK_CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-96}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("presence bench script missing %q", want)
		}
	}
	if strings.Contains(script, "docker compose") {
		t.Fatalf("presence bench script should use local startup scripts, not docker compose")
	}
}

func TestWukongIMV2ThreeNodePresenceScriptRebuildsStaleWkbenchBinary(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	wkbenchPath := filepath.Join(binDir, "wkbench")
	writeFakePresenceWkbench(t, wkbenchPath, callsDir)
	old := time.Unix(946684800, 0)
	if err := os.Chtimes(wkbenchPath, old, old); err != nil {
		t.Fatal(err)
	}
	writeFakePresenceGoBuildWkbench(t, filepath.Join(binDir, "go"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-presence.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", wkbenchPath,
		"--users", "10",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--sample-interval", "0",
		"--stable-samples", "1",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script failed: %v\n%s", err, output)
	}

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+wkbenchPath+" ./cmd/wkbench") {
		t.Fatalf("stale presence wkbench binary should be rebuilt, calls:\n%s", goCalls)
	}
	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	if !strings.Contains(wkbenchCalls, "rebuilt") {
		t.Fatalf("script should use rebuilt presence wkbench binary, calls:\n%s", wkbenchCalls)
	}
}

func TestWukongIMV2ThreeNodePresenceScriptRunsBenchAndValidatesSnapshot(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-presence.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--users", "10",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--sample-interval", "0",
		"--stable-samples", "1",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script failed: %v\n%s", err, output)
	}

	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	for _, want := range []string{
		"run --target " + filepath.Join(outDir, "target.yaml"),
		"--scenario " + filepath.Join(outDir, "scenario.yaml"),
		"--workers " + filepath.Join(outDir, "workers.yaml"),
	} {
		if !strings.Contains(wkbenchCalls, want) {
			t.Fatalf("wkbench calls missing %q:\n%s", want, wkbenchCalls)
		}
	}

	scenario := readFile(t, filepath.Join(outDir, "scenario.yaml"))
	for _, want := range []string{
		"total_users: 10",
		"heartbeat:",
		"interval: 1s",
		"traffic: []",
	} {
		if !strings.Contains(scenario, want) {
			t.Fatalf("scenario missing %q:\n%s", want, scenario)
		}
	}

	summary := readFile(t, filepath.Join(outDir, "presence-summary.tsv"))
	for _, want := range []string{
		"expected_users\t10",
		"required_stable_samples\t1",
		"stable_sample_count\t",
		"heartbeat_error_total\t0",
		"live_sample_count\t",
		"owner_routes_active\t10",
		"authority_routes_active\t10",
		"owner_routes_pending\t0",
		"expired_routes_total\t0",
		"presence_status\tpassed",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("presence summary missing %q:\n%s", want, summary)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- presence_status: passed",
		"- live_presence_samples: presence-samples.jsonl",
		"- presence_summary: presence-summary.tsv",
		"- report: report/report.json",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("top-level summary missing %q:\n%s", want, topSummary)
		}
	}

	samples := readFile(t, filepath.Join(outDir, "presence-samples.jsonl"))
	for _, want := range []string{
		`"sample_phase":"run"`,
		`"api_addr":"http://127.0.0.1:5011"`,
		`"sample_seq":`,
	} {
		if !strings.Contains(samples, want) {
			t.Fatalf("live samples missing %q:\n%s", want, samples)
		}
	}
}

func TestWukongIMV2ThreeNodePresenceScriptFailsOnTransientPeak(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-presence.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--users", "10",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--sample-interval", "0.02",
		"--stable-samples", "2",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_PRESENCE_RUN_SLEEP=0.08",
		"WK_FAKE_PRESENCE_TRANSIENT=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("presence script should fail on transient peak:\n%s", output)
	}

	summary := readFile(t, filepath.Join(outDir, "presence-summary.tsv"))
	for _, want := range []string{
		"presence_status\tfailed",
		"required_stable_samples\t2",
		"stable_sample_count\t1",
		"owner_routes_active\t10",
		"authority_routes_active\t10",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("transient summary missing %q:\n%s", want, summary)
		}
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
  if [[ "${WK_FAKE_ACTIVATE_CLASSIFY_BAD:-0}" == "1" ]]; then
    cat <<'OUT'
classification: fake
channelv2_pending_meta_current_max: 1
channelv2_pending_meta_released_count: 0
channelv2_need_meta_pull_submitted_count: 3
channelv2_need_meta_pull_ok_count: 2
channelv2_need_meta_pull_retry_count: 1
channelv2_need_meta_pull_err_count: 0
channelv2_pull_hint_err_count: 1
channelv2_pull_hint_receive_err_count: 0
OUT
    exit 0
  fi
  cat <<'OUT'
classification: fake
channelv2_pending_meta_current_max: 0
channelv2_pending_meta_released_count: 0
channelv2_need_meta_pull_submitted_count: 3
channelv2_need_meta_pull_ok_count: 3
channelv2_need_meta_pull_retry_count: 0
channelv2_need_meta_pull_err_count: 0
channelv2_pull_hint_err_count: 0
channelv2_pull_hint_receive_err_count: 0
OUT
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

func writeFakeActivateGoBuildWkbench(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/go.calls"
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ -z "$out" ]]; then
  echo "missing -o" >&2
  exit 2
fi
mkdir -p "$(dirname "$out")"
cat > "$out" <<'WKFAKE'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" > "` + callsDir + `/wkbench.args"
printf '%s\n' "$*" >> "` + callsDir + `/wkbench.calls"
if [[ "${1:-}" == "metrics" && "${2:-}" == "classify" ]]; then
  cat <<'OUT'
classification: rebuilt
channelv2_pending_meta_current_max: 0
channelv2_pending_meta_released_count: 0
channelv2_need_meta_pull_submitted_count: 3
channelv2_need_meta_pull_ok_count: 3
channelv2_need_meta_pull_retry_count: 0
channelv2_need_meta_pull_err_count: 0
channelv2_pull_hint_err_count: 0
channelv2_pull_hint_receive_err_count: 0
OUT
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
WKFAKE
chmod +x "$out"
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

func writeFakePresenceWkbench(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" > "` + callsDir + `/wkbench.args"
printf '%s\n' "$*" >> "` + callsDir + `/wkbench.calls"
if [[ "${1:-}" == "run" ]]; then
  report_dir=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --scenario)
        scenario="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  report_dir="$(awk '$1 == "report_dir:" { print $2 }' "$scenario")"
  if [[ -z "$report_dir" ]]; then
    echo "missing report_dir in scenario" >&2
    exit 2
  fi
  if [[ -n "${WK_FAKE_PRESENCE_RUN_SLEEP:-}" ]]; then
    sleep "$WK_FAKE_PRESENCE_RUN_SLEEP"
  fi
  mkdir -p "$report_dir"
  echo '{"run_id":"three-node-presence","status":"passed"}' > "$report_dir/report.json"
  echo '# fake report' > "$report_dir/summary.md"
  exit 0
fi
if [[ "${1:-}" == "worker" ]]; then
  while true; do sleep 60; done
fi
echo "unexpected wkbench args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakePresenceGoBuildWkbench(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/go.calls"
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ -z "$out" ]]; then
  echo "missing -o" >&2
  exit 2
fi
mkdir -p "$(dirname "$out")"
cat > "$out" <<'WKFAKE'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf 'rebuilt %s\n' "$*" >> "` + callsDir + `/wkbench.calls"
if [[ "${1:-}" == "run" ]]; then
  scenario=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --scenario)
        scenario="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  report_dir="$(awk '$1 == "report_dir:" { print $2 }' "$scenario")"
  if [[ -n "${WK_FAKE_PRESENCE_RUN_SLEEP:-}" ]]; then
    sleep "$WK_FAKE_PRESENCE_RUN_SLEEP"
  fi
  mkdir -p "$report_dir"
  echo '{"run_id":"three-node-presence","status":"passed"}' > "$report_dir/report.json"
  echo '# fake report' > "$report_dir/summary.md"
  exit 0
fi
if [[ "${1:-}" == "worker" ]]; then
  while true; do sleep 60; done
fi
echo "unexpected rebuilt wkbench args: $*" >&2
exit 2
WKFAKE
chmod +x "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakePresenceCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
echo "$*" >> "` + callsDir + `/curl.calls"
url="${@: -1}"
presence_call_index() {
  local key
  key="$(printf '%s' "$1" | sed 's/[^a-zA-Z0-9]/_/g')"
  local file="` + callsDir + `/presence-${key}.count"
  local count=0
  if [[ -f "$file" ]]; then
    count="$(cat "$file")"
  fi
  echo "$((count + 1))" > "$file"
  echo "$count"
}
case "$url" in
  http://127.0.0.1:501*/readyz)
    echo 'ok'
    ;;
  http://127.0.0.1:5011/bench/v1/presence/snapshot)
    idx="$(presence_call_index "$url")"
    if [[ "${WK_FAKE_PRESENCE_TRANSIENT:-0}" == "1" && "$idx" -gt 0 ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":1,"owner_routes_active":4,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":3,"authority_routes_by_hash_slot":{"1":3},"touch_routes_total":2,"expired_routes_total":0}
JSON
      exit 0
    fi
    cat <<'JSON'
{"version":"bench/v1","node_id":1,"owner_routes_active":4,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":3,"authority_routes_by_hash_slot":{"1":3},"touch_routes_total":2,"expired_routes_total":0}
JSON
    ;;
  http://127.0.0.1:5012/bench/v1/presence/snapshot)
    idx="$(presence_call_index "$url")"
    if [[ "${WK_FAKE_PRESENCE_TRANSIENT:-0}" == "1" && "$idx" -gt 0 ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":2,"owner_routes_active":3,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":4,"authority_routes_by_hash_slot":{"2":4},"touch_routes_total":2,"expired_routes_total":0}
JSON
      exit 0
    fi
    cat <<'JSON'
{"version":"bench/v1","node_id":2,"owner_routes_active":3,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":4,"authority_routes_by_hash_slot":{"2":4},"touch_routes_total":2,"expired_routes_total":0}
JSON
    ;;
  http://127.0.0.1:5013/bench/v1/presence/snapshot)
    idx="$(presence_call_index "$url")"
    if [[ "${WK_FAKE_PRESENCE_TRANSIENT:-0}" == "1" && "$idx" -gt 0 ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":3,"owner_routes_active":2,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":2,"authority_routes_by_hash_slot":{"3":2},"touch_routes_total":2,"expired_routes_total":0}
JSON
      exit 0
    fi
    cat <<'JSON'
{"version":"bench/v1","node_id":3,"owner_routes_active":3,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":3,"authority_routes_by_hash_slot":{"3":3},"touch_routes_total":2,"expired_routes_total":0}
JSON
    ;;
  http://127.0.0.1:19131/healthz)
    echo 'ok'
    ;;
  http://127.0.0.1:19131/v1/stop)
    echo 'ok'
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

func writeFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
