package scripts_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWukongIMThreeNode10kBenchScriptSetsEvidenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-10kch.sh"))

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

func TestWukongIMThreeNodeActivateScriptRebuildsStaleWkbenchBinary(t *testing.T) {
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

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-10kch.sh",
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

func TestWukongIMThreeNodeActivateScriptCollectsServerProcessResources(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeActivateWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-10kch.sh",
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

func TestWukongIMThreeNodeActivateScriptClassifiesMetricsEvidence(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeActivateWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-10kch.sh",
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

func TestWukongIMThreeNodeActivateScriptFailsOnMetricsHealthGate(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeActivateWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeActivateCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-10kch.sh",
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

func TestWukongIMThreeNodeBenchScriptCollectsLocalEvidence(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-1000ch.sh"))

	for _, want := range []string{
		"scripts/start-wukongim-three-nodes.sh",
		"collect_node_logs",
		"capture_node_pprof",
		"channelv2_metrics_summary",
		"scripts/channelv2-metrics-summary.awk",
		"channelv2_metrics_summary.tsv",
		"runtime_pool_queue_depth_max",
		"runtime_pool_admission_full_delta",
		"- ants_pool_usage: ants_pool_usage_summary.tsv",
		"ants_pool_usage_summary",
		"runtime_pool_pressure_summary",
		"runtime_pool_pressure_summary.tsv",
		`RUNTIME_POOL_SAMPLE_INTERVAL="${WK_BENCH_RUNTIME_POOL_SAMPLE_INTERVAL:-1}"`,
		`newer_source="$(find "$ROOT_DIR/cmd/wkbench" "$ROOT_DIR/internal/bench" -type f -newer "$WK_BENCH_BIN" -print -quit)"`,
		"write_evidence_summary",
		`ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"`,
		`RECV_ACK="${WK_BENCH_RECV_ACK:-true}"`,
		`HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"`,
		`CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"`,
		`SENDER_PICK="${WK_BENCH_SENDER_PICK:-round_robin}"`,
		`ACTUAL_QPS_MIN_RATIO="${WK_BENCH_ACTUAL_QPS_MIN_RATIO:-0.90}"`,
		"actual/offered gate: >= %.2f",
		"--concurrency N        wkbench send concurrency. Default: 2800.",
		"--sender-pick MODE     Group sender selection: round_robin or first_online. Default: round_robin.",
		"ack_timeout: $ACK_TIMEOUT",
		"recv_ack: $RECV_ACK",
		"enabled: $HEARTBEAT_ENABLED",
		`--phase-poll-timeout "$PHASE_POLL_TIMEOUT"`,
		"--recv-ack BOOL",
		"--heartbeat BOOL",
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-128}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-2ms}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SYNC="${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}"`,
		`WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}"`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}`,
		`CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-2ms}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}`,
		`CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-0}`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"gateway_ready()",
		`>/dev/tcp/"$host"/"$port"`,
		`for gateway in "${GATEWAY_VALUES[@]}"`,
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

func TestWukongIMThreeNodeRealQPSScriptUses15KTunedDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-real-qps.sh"))

	for _, want := range []string{
		`CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"`,
		`ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"`,
		`RECV_ACK="${WK_BENCH_RECV_ACK:-true}"`,
		`HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"`,
		"--concurrency N            wkbench send concurrency. Default: 2800.",
		"--ack-timeout DURATION     Per-SEND sendack wait timeout. Default: 15s.",
		`--phase-poll-timeout "$PHASE_POLL_TIMEOUT"`,
		"--recv-ack BOOL            Whether group recv frames are acknowledged. Default: true.",
		"--heartbeat BOOL           Whether benchmark clients send heartbeat pings. Default: true.",
		`--recv-ack "$RECV_ACK"`,
		`--heartbeat "$HEARTBEAT_ENABLED"`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-2ms}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}`,
		`CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-0}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}`,
		`CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}"`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"runtime_pool_attempt_summary",
		"runtime_pool_queue_fill_max",
		"runtime_pool_admission_busy_delta",
		"write_ants_pool_usage_summary",
		"# ants pool usage",
		"ants_pool_usage_summary.tsv",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("real-qps script missing high-concurrency default %q", want)
		}
	}
}

func TestWukongIMThreeNodeRealQPSScriptAggregatesRuntimePoolMetrics(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	outDir := t.TempDir()
	baseScript := filepath.Join(binDir, "fake-base.sh")
	writeFakeRealQPSBenchBase(t, baseScript)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-real-qps.sh",
		"--qps", "100",
		"--out-dir", outDir,
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WK_BENCH_REAL_QPS_BASE_SCRIPT="+baseScript,
		"GOWORK=off",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real-qps script failed: %v\n%s", err, output)
	}
	if got := strings.Count(string(output), "BENCH RESULT"); got != 1 {
		t.Fatalf("real-qps console should print one aggregate BENCH RESULT, got %d:\n%s", got, output)
	}
	childConsole := readFile(t, filepath.Join(outDir, "000100-qps", "real-qps-console.txt"))
	if !strings.Contains(childConsole, "child summary should stay in attempt console") {
		t.Fatalf("child console should retain child output:\n%s", childConsole)
	}

	summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
	for _, want := range []string{
		"runtime_pool_queue_depth_max",
		"runtime_pool_queue_fill_max",
		"runtime_pool_admission_full_delta",
		"\t7\t0.700\t300\t0.500\t12\t0.750\t5\t3\t1\t6\t",
		"\tPASS\tok\t",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("real-qps summary missing %q:\n%s", want, summary)
		}
	}

	display := readFile(t, filepath.Join(outDir, "summary.txt"))
	for _, want := range []string{
		"BENCH RESULT",
		"p99 gate: <= 400 ms",
		"send_errors: 0",
		"actual/offered gate: >= 0.95",
		"best pass: offered=100 actual=99.0 qps p99=3.0ms",
		"# ants pool usage",
		"node=node1",
		"pools=4",
		"transportv2/service_executor",
		"3/4",
		"channelv2/store_append",
		"5/64",
		"channelappend/advance",
		"1/2",
		"channelappend/effect",
		"4/8",
		"details=ants_pool_usage_summary.tsv",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("real-qps display summary missing %q:\n%s", want, display)
		}
	}
	for _, unwanted := range []string{
		"# runtime pool pressure",
		"cw_err",
		"entries=1 max_fill",
		"gateway/async_send",
		"gateway/async_auth",
	} {
		if strings.Contains(display, unwanted) {
			t.Fatalf("real-qps display summary should hide runtime/channelwrite pressure, found %q:\n%s", unwanted, display)
		}
	}
	if strings.Contains(display, "component        pool") {
		t.Fatalf("real-qps display summary should not print per-pool table rows:\n%s", display)
	}

	antsUsage := readFile(t, filepath.Join(outDir, "ants_pool_usage_summary.tsv"))
	for _, want := range []string{
		"offered_qps\tattempt_dir\ttag\tnode\tcomponent\tpool\trunning\tcapacity\twaiting\tutilization_max",
		"100\t" + filepath.Join(outDir, "000100-qps") + "\t000100\tnode1\ttransportv2\tservice_executor\t3\t4\t2\t0.750",
		"100\t" + filepath.Join(outDir, "000100-qps") + "\t000100\tnode1\tchannelv2\tstore_append\t5\t64\t0\t0.078",
		"100\t" + filepath.Join(outDir, "000100-qps") + "\t000100\tnode1\tchannelappend\tadvance\t1\t2\t0\t0.500",
		"100\t" + filepath.Join(outDir, "000100-qps") + "\t000100\tnode1\tchannelappend\teffect\t4\t8\t1\t0.500",
	} {
		if !strings.Contains(antsUsage, want) {
			t.Fatalf("real-qps ants pool usage summary missing %q:\n%s", want, antsUsage)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"## Result",
		"p99 gate: <= 400 ms",
		"send_errors: 0",
		"actual/offered gate: >= 0.95",
		"best pass: offered=100 actual=99.0 qps p99=3.0ms",
		"- ants_pool_usage: ants_pool_usage_summary.tsv",
		"## Ants Pool Usage",
		"node=node1",
		"max_util=0.750",
		"transportv2/service_executor",
		"channelv2/store_append",
		"channelappend/advance",
		"channelappend/effect",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("real-qps markdown summary missing %q:\n%s", want, topSummary)
		}
	}
	for _, unwanted := range []string{
		"## Runtime Pool Pressure",
		"- runtime_pool_pressure: runtime_pool_pressure_summary.tsv",
		"admission_full",
		"gateway/async_send",
	} {
		if strings.Contains(topSummary, unwanted) {
			t.Fatalf("real-qps markdown summary should hide runtime/channelwrite pressure, found %q:\n%s", unwanted, topSummary)
		}
	}
	if strings.Contains(topSummary, "component\tpool\tqueue\tpriority") {
		t.Fatalf("real-qps markdown summary should not print every pressure row:\n%s", topSummary)
	}
}

func TestWukongIMRealQPSScriptsForwardDefaultAndExplicitChannelCount(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		name       string
		scriptPath string
		baseEnv    string
	}{
		{
			name:       "single-node",
			scriptPath: "scripts/bench-wukongim-single-node-real-qps.sh",
			baseEnv:    "WK_BENCH_SINGLE_REAL_QPS_BASE_SCRIPT",
		},
		{
			name:       "three-node",
			scriptPath: "scripts/bench-wukongim-three-nodes-real-qps.sh",
			baseEnv:    "WK_BENCH_REAL_QPS_BASE_SCRIPT",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/default", func(t *testing.T) {
			outDir := t.TempDir()
			baseScript := filepath.Join(t.TempDir(), "fake-base.sh")
			writeFakeRealQPSBenchBase(t, baseScript)

			cmd := exec.Command("bash", tc.scriptPath,
				"--qps", "100",
				"--out-dir", outDir,
				"--duration", "1s",
				"--warmup", "0s",
				"--cooldown", "0s",
			)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				tc.baseEnv+"="+baseScript,
				"GOWORK=off",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("real-qps script failed: %v\n%s", err, output)
			}

			args := readFile(t, filepath.Join(outDir, "000100-qps", "base.args"))
			if !strings.Contains(args, "--channels 1000") {
				t.Fatalf("default channel count should be forwarded as 1000, args:\n%s", args)
			}
			envText := readFile(t, filepath.Join(outDir, "env.txt"))
			if !strings.Contains(envText, "CHANNELS=1000") {
				t.Fatalf("env metadata should record default channel count 1000:\n%s", envText)
			}
		})

		t.Run(tc.name+"/channel-count", func(t *testing.T) {
			outDir := t.TempDir()
			baseScript := filepath.Join(t.TempDir(), "fake-base.sh")
			writeFakeRealQPSBenchBase(t, baseScript)

			cmd := exec.Command("bash", tc.scriptPath,
				"--qps", "100",
				"--out-dir", outDir,
				"--channel-count", "123",
				"--duration", "1s",
				"--warmup", "0s",
				"--cooldown", "0s",
			)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				tc.baseEnv+"="+baseScript,
				"GOWORK=off",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("real-qps script failed: %v\n%s", err, output)
			}

			args := readFile(t, filepath.Join(outDir, "000100-qps", "base.args"))
			if !strings.Contains(args, "--channels 123") {
				t.Fatalf("explicit channel count should be forwarded as 123, args:\n%s", args)
			}
			envText := readFile(t, filepath.Join(outDir, "env.txt"))
			if !strings.Contains(envText, "CHANNELS=123") {
				t.Fatalf("env metadata should record explicit channel count 123:\n%s", envText)
			}
		})
	}
}

func TestWukongIMBenchScriptsLogActualChannelCount(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		name       string
		scriptPath string
		prefix     string
		oldPrefix  string
	}{
		{
			name:       "single-node",
			scriptPath: "scripts/bench-wukongim-single-node-1000ch.sh",
			prefix:     "[bench-single-10ch]",
			oldPrefix:  "[bench-single-1000ch]",
		},
		{
			name:       "three-node",
			scriptPath: "scripts/bench-wukongim-three-nodes-1000ch.sh",
			prefix:     "[bench-three-10ch]",
			oldPrefix:  "[bench-three-1000ch]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			callsDir := t.TempDir()
			outDir := t.TempDir()
			writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
			writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
			writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
			writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
			gatewayAddr := listenLocalTCP(t)

			cmd := exec.Command("bash", tc.scriptPath,
				"--no-start",
				"--no-worker",
				"--out-dir", outDir,
				"--wkbench-bin", filepath.Join(binDir, "wkbench"),
				"--qps", "100",
				"--channels", "10",
				"--users", "20",
				"--members", "2",
				"--duration", "1s",
				"--warmup", "0s",
				"--cooldown", "0s",
				"--resource-interval", "0",
				"--api", "http://127.0.0.1:5011",
				"--metrics", "http://127.0.0.1:5011",
				"--gateway", gatewayAddr,
			)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v\n%s", err, output)
			}
			text := string(output)
			if !strings.Contains(text, tc.prefix) {
				t.Fatalf("script output should include dynamic prefix %q:\n%s", tc.prefix, text)
			}
			if strings.Contains(text, tc.oldPrefix) {
				t.Fatalf("script output should not include stale prefix %q:\n%s", tc.oldPrefix, text)
			}
		})
	}
}

func TestWukongIMThreeNodeBenchScriptPrintsAntsPoolUsageByNode(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-1000ch.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--qps", "100",
		"--channels", "10",
		"--users", "20",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--gateway", gatewayAddr,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_RUNTIME_POOL_PRESSURE=1",
		"WK_FAKE_WKBENCH_RUN_SLEEP=0.20",
		"WK_BENCH_RUNTIME_POOL_SAMPLE_INTERVAL=0.05",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	pressure := readFile(t, filepath.Join(outDir, "runtime_pool_pressure_summary.tsv"))
	for _, want := range []string{
		"component\tpool\tqueue\tpriority",
		"gateway\tasync_send\tsend\tnone",
		"queue_backlog",
		"admission_full",
	} {
		if !strings.Contains(pressure, want) {
			t.Fatalf("runtime pool pressure summary missing %q:\n%s", want, pressure)
		}
	}

	channelappend := readFile(t, filepath.Join(outDir, "channelappend_metrics_summary.tsv"))
	for _, want := range []string{
		"effect_pool_submit_delta",
		"effect_pool_full_delta",
		"effect_pool_error_delta",
		"effect_pool_inflight_max",
		"effect_pool_capacity_max",
		"effect_pool_util_max",
		"effect_pool_saturated_max",
		"effect_pool_over90_count",
		"000100\t127_0_0_1_5011",
		"\t10\t10\t1.000\t1\t1",
	} {
		if !strings.Contains(channelappend, want) {
			t.Fatalf("channelappend metrics summary missing %q:\n%s", want, channelappend)
		}
	}

	clusterTransport := readFile(t, filepath.Join(outDir, "cluster_transport_peak_summary.tsv"))
	for _, want := range []string{
		"tag\tnode\tsample_points\tsample_pairs\tpeak_internal_mib_s",
		"000100",
		"127_0_0_1_5011",
	} {
		if !strings.Contains(clusterTransport, want) {
			t.Fatalf("cluster transport peak summary missing %q:\n%s", want, clusterTransport)
		}
	}

	display := readFile(t, filepath.Join(outDir, "summary.txt"))
	for _, want := range []string{
		"BENCH RESULT",
		"p99 gate: <= 400 ms",
		"send_errors: 0",
		"actual/offered gate: >= 0.90",
		"best pass: none",
		"SERVER PROCESS PEAKS",
		"details=resources/server-process-summary.tsv",
		"CLUSTER INTERNAL TRANSPORT PEAK",
		"details=cluster_transport_peak_summary.tsv",
		"ANTS POOL USAGE",
		"details=ants_pool_usage_summary.tsv",
		"node=127_0_0_1_5011",
		"pools=4",
		"pool",
		"used/cap",
		"waiting",
		"transportv2/service_executor",
		"3/4",
		"channelv2/store_append",
		"5/64",
		"channelappend/advance",
		"2/4",
		"channelappend/effect",
		"10/10",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("display summary missing %q:\n%s", want, display)
		}
	}
	for _, unwanted := range []string{
		"RUNTIME POOL PRESSURE",
		"CHANNELWRITE POOL PRESSURE",
		"details=channelappend_metrics_summary.tsv",
		"gateway/async_send",
		"gateway/async_auth",
	} {
		if strings.Contains(display, unwanted) {
			t.Fatalf("display summary should hide runtime/channelwrite pressure, found %q:\n%s", unwanted, display)
		}
	}
	if strings.Contains(display, "component        pool") {
		t.Fatalf("display summary should not print per-pool table rows:\n%s", display)
	}

	antsUsage := readFile(t, filepath.Join(outDir, "ants_pool_usage_summary.tsv"))
	for _, want := range []string{
		"tag\tnode\tcomponent\tpool\trunning\tcapacity\twaiting\tutilization_max",
		"000100\t127_0_0_1_5011\ttransportv2\tservice_executor\t3\t4\t2\t0.750",
		"000100\t127_0_0_1_5011\tchannelv2\tstore_append\t5\t64\t0\t0.078",
		"000100\t127_0_0_1_5011\tchannelappend\tadvance\t2\t4\t0\t0.500",
		"000100\t127_0_0_1_5011\tchannelappend\teffect\t10\t10\t1\t1.000",
	} {
		if !strings.Contains(antsUsage, want) {
			t.Fatalf("ants pool usage summary missing %q:\n%s", want, antsUsage)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"## Result",
		"p99 gate: <= 400 ms",
		"send_errors: 0",
		"actual/offered gate: >= 0.90",
		"best pass: none",
		"- server_process: resources/server-process-summary.tsv",
		"- cluster_transport: cluster_transport_peak_summary.tsv",
		"- ants_pool_usage: ants_pool_usage_summary.tsv",
		"## Server Process Peaks",
		"## Cluster Internal Transport Peak",
		"## Ants Pool Usage",
		"node=127_0_0_1_5011",
		"max_util=1.000",
		"transportv2/service_executor",
		"channelv2/store_append",
		"channelappend/advance",
		"channelappend/effect",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("markdown summary missing %q:\n%s", want, topSummary)
		}
	}
	for _, unwanted := range []string{
		"## Runtime Pool Pressure",
		"## ChannelAppend Pool Pressure",
		"- runtime_pool_pressure: runtime_pool_pressure_summary.tsv",
		"details=channelappend_metrics_summary.tsv",
	} {
		if strings.Contains(topSummary, unwanted) {
			t.Fatalf("markdown summary should hide runtime/channelwrite pressure, found %q:\n%s", unwanted, topSummary)
		}
	}
	if strings.Contains(topSummary, "component\tpool\tqueue\tpriority") {
		t.Fatalf("markdown summary should not print every pressure row:\n%s", topSummary)
	}
}

func TestWukongIMThreeNodeBenchScriptPrintsServerResourcePeaks(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-1000ch.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--qps", "100",
		"--channels", "10",
		"--users", "20",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--resource-interval", "0",
		"--gateway", gatewayAddr,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
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

	resourceSummary := readFile(t, filepath.Join(outDir, "resources", "server-process-summary.tsv"))
	for _, want := range []string{
		"node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb\tmax_goroutines",
		"node1\t111\t2\t12.500\t12.500\t1.200\t1.200\t123456\t789000\t1111",
		"node2\t222\t2\t25.000\t25.000\t2.300\t2.300\t234567\t890000\t1111",
		"node3\t333\t2\t3.500\t3.500\t0.700\t0.700\t345678\t901000\t1111",
	} {
		if !strings.Contains(resourceSummary, want) {
			t.Fatalf("resource summary missing %q:\n%s", want, resourceSummary)
		}
	}

	display := readFile(t, filepath.Join(outDir, "summary.txt"))
	for _, want := range []string{
		"BENCH RESULT",
		"actual/offered gate: >= 0.90",
		"best pass: none",
		"SERVER PROCESS PEAKS",
		"details=resources/server-process-summary.tsv",
		"peak_goroutines=node1 1111",
		"max_goroutines",
		"CLUSTER INTERNAL TRANSPORT PEAK",
		"details=cluster_transport_peak_summary.tsv",
		"ANTS POOL USAGE",
		"none",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("display summary missing %q:\n%s", want, display)
		}
		if !strings.Contains(string(output), want) {
			t.Fatalf("console output missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"RUNTIME POOL PRESSURE",
		"CHANNELWRITE POOL PRESSURE",
	} {
		if strings.Contains(display, unwanted) {
			t.Fatalf("display summary should hide runtime/channelwrite pressure, found %q:\n%s", unwanted, display)
		}
		if strings.Contains(string(output), unwanted) {
			t.Fatalf("console output should hide runtime/channelwrite pressure, found %q:\n%s", unwanted, output)
		}
	}
	for _, want := range []string{
		"[bench-three-10ch] evidence:",
		"summary                 summary.tsv",
		"server_process          resources/server-process-summary.tsv",
		"cluster_transport       cluster_transport_peak_summary.tsv",
		"ants_pool_usage         ants_pool_usage_summary.tsv",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("console output missing compact evidence line %q:\n%s", want, output)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"## Server Process Peaks",
		"details: resources/server-process-summary.tsv",
		"## Cluster Internal Transport Peak",
		"details=cluster_transport_peak_summary.tsv",
		"- ants_pool_usage: ants_pool_usage_summary.tsv",
		"## Ants Pool Usage",
		"- none",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("markdown summary missing %q:\n%s", want, topSummary)
		}
	}
	for _, unwanted := range []string{
		"- runtime_pool_pressure: runtime_pool_pressure_summary.tsv",
		"## Runtime Pool Pressure",
	} {
		if strings.Contains(topSummary, unwanted) {
			t.Fatalf("markdown summary should hide runtime pressure, found %q:\n%s", unwanted, topSummary)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptKeepsGateResultWithAntsPoolDisplay(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-1000ch.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--qps", "10000",
		"--channels", "10",
		"--users", "20",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--gateway", gatewayAddr,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
	for _, want := range []string{
		"010000\t10000\tpassed\t0\t1\t1\t0\t0\t0\t0.001\t0.002\t0.003\t0.004",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary tsv missing %q:\n%s", want, summary)
		}
	}

	display := readFile(t, filepath.Join(outDir, "summary.txt"))
	for _, want := range []string{
		"BENCH RESULT",
		"actual/offered gate: >= 0.90",
		"best pass: none",
		"ANTS POOL USAGE",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("display summary should keep result and ants pool usage, missing %q:\n%s", want, display)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptRebuildsStaleWkbenchBinary(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	wkbenchPath := filepath.Join(binDir, "wkbench")
	writeFakeThreeNode1000Wkbench(t, wkbenchPath, callsDir, "old")
	old := time.Unix(946684800, 0)
	if err := os.Chtimes(wkbenchPath, old, old); err != nil {
		t.Fatal(err)
	}
	writeFakeThreeNode1000GoBuildWkbench(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-1000ch.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", wkbenchPath,
		"--qps", "100",
		"--channels", "10",
		"--users", "20",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--gateway", gatewayAddr,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+wkbenchPath+" ./cmd/wkbench") {
		t.Fatalf("stale wkbench binary should be rebuilt, calls:\n%s", goCalls)
	}
	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	if !strings.Contains(wkbenchCalls, "rebuilt run ") {
		t.Fatalf("script should use rebuilt wkbench binary, calls:\n%s", wkbenchCalls)
	}
	classify := readFile(t, filepath.Join(outDir, "metrics", "000100", "127_0_0_1_5011-classify.txt"))
	if !strings.Contains(classify, "classification: rebuilt") {
		t.Fatalf("classification should use rebuilt wkbench binary:\n%s", classify)
	}
	scenario := readFile(t, filepath.Join(outDir, "scenario-000100.yaml"))
	if !strings.Contains(scenario, "recv_ack: true") {
		t.Fatalf("generated scenario should ack drained group recv frames by default:\n%s", scenario)
	}
	if !strings.Contains(scenario, "enabled: true") {
		t.Fatalf("generated scenario should enable heartbeat by default:\n%s", scenario)
	}
}

func TestWukongIMThreeNodeBenchScriptCanDisableHeartbeat(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-1000ch.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--qps", "100",
		"--channels", "10",
		"--users", "20",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--heartbeat", "false",
		"--gateway", gatewayAddr,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	scenario := readFile(t, filepath.Join(outDir, "scenario-000100.yaml"))
	for _, want := range []string{
		"heartbeat:",
		"enabled: false",
		"recv_ack: true",
	} {
		if !strings.Contains(scenario, want) {
			t.Fatalf("generated scenario missing %q:\n%s", want, scenario)
		}
	}
	env := readFile(t, filepath.Join(outDir, "env.txt"))
	if !strings.Contains(env, "HEARTBEAT_ENABLED=false") {
		t.Fatalf("env metadata should record disabled heartbeat:\n%s", env)
	}
}

func TestWukongIMDeliveryBenchScriptIsLocalThreeNodeOnly(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-delivery.sh"))

	for _, want := range []string{
		"scripts/start-wukongim-three-nodes.sh",
		`SCENARIO="${WK_BENCH_DELIVERY_SCENARIO:-group}"`,
		`DELIVERY_ENABLE="${WK_DELIVERY_ENABLE:-true}"`,
		`DELIVERY_EVENT_QUEUE_SIZE="${WK_DELIVERY_EVENT_QUEUE_SIZE:-1024}"`,
		`DELIVERY_FANOUT_PAGE_SIZE="${WK_DELIVERY_FANOUT_PAGE_SIZE:-512}"`,
		`DELIVERY_PUSH_BATCH_SIZE="${WK_DELIVERY_PUSH_BATCH_SIZE:-512}"`,
		`DELIVERY_PENDING_ACK_MAX_PER_SESSION="${WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION:-1024}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_DELIVERY_PHASE_POLL_TIMEOUT:-120s}"`,
		"write_delivery_summary",
		"delivery-summary.tsv",
		"wukongim_delivery_event_queue_total",
		"wukongim_delivery_retry_total",
		"wukongim_delivery_ack_bindings",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("delivery bench script missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"bench-wukongim-three-nodes-real-qps.sh",
		"docker compose",
		"dev-sim",
		"--start-script",
		"--api LIST",
		"--gateway LIST",
		"--metrics LIST",
		"WK_BENCH_API_ADDRS",
		"WK_BENCH_GATEWAY_ADDRS",
		"WK_BENCH_METRICS_ADDRS",
		"WK_BENCH_THREE_NODE_START_SCRIPT",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("delivery bench script should not contain %q", forbidden)
		}
	}
}

func TestWukongIMDeliveryBenchScriptGeneratesGroupScenarioAndSummary(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeDeliveryWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeDeliveryCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-delivery.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--scenario", "group",
		"--qps", "100",
		"--channels", "20",
		"--users", "200",
		"--members", "50",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--phase-poll-timeout", "45s",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("delivery script failed: %v\n%s", err, output)
	}

	scenario := readFile(t, filepath.Join(outDir, "scenarios", "scenario-000100.yaml"))
	for _, want := range []string{
		"id: delivery-group-000100",
		"channel_type: group",
		"count: 20",
		"members:",
		"count: 50",
		"rate_per_channel: 5/s",
		"recv_ack: true",
		"mode: none",
	} {
		if !strings.Contains(scenario, want) {
			t.Fatalf("delivery scenario missing %q:\n%s", want, scenario)
		}
	}

	wkbenchCalls := readFile(t, filepath.Join(callsDir, "wkbench.calls"))
	for _, want := range []string{
		"run --target " + filepath.Join(outDir, "target.yaml"),
		"--scenario " + filepath.Join(outDir, "scenarios", "scenario-000100.yaml"),
		"--workers " + filepath.Join(outDir, "workers.yaml"),
		"--phase-poll-timeout 45s",
	} {
		if !strings.Contains(wkbenchCalls, want) {
			t.Fatalf("wkbench calls missing %q:\n%s", want, wkbenchCalls)
		}
	}

	delivery := readFile(t, filepath.Join(outDir, "delivery-summary.tsv"))
	for _, want := range []string{
		"tag\tnode\tadmission_ok\tadmission_overflow\tadmission_error\tdelivery_errors\tretry_drop\tretry_overflow\tretry_queue_depth\tack_bindings\tresolve_routes\tpush_routes",
		"000100\t127_0_0_1_5011\t20\t0\t0\t0\t0\t0\t0\t0\t1000\t1000",
	} {
		if !strings.Contains(delivery, want) {
			t.Fatalf("delivery summary missing %q:\n%s", want, delivery)
		}
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- scenario: group",
		"- delivery_summary: delivery-summary.tsv",
		"- status: passed",
		"- reports: reports/",
		"- metrics: metrics/",
		"- pprof: pprof/",
		"- logs: logs/",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("top-level summary missing %q:\n%s", want, topSummary)
		}
	}
}

func TestWukongIMDeliveryBenchScriptInjectsDeliveryEnvWhenStartingCluster(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-delivery.sh"))
	for _, want := range []string{
		`WK_DELIVERY_ENABLE="$DELIVERY_ENABLE"`,
		`WK_DELIVERY_EVENT_QUEUE_SIZE="$DELIVERY_EVENT_QUEUE_SIZE"`,
		`WK_DELIVERY_FANOUT_PAGE_SIZE="$DELIVERY_FANOUT_PAGE_SIZE"`,
		`WK_DELIVERY_PUSH_BATCH_SIZE="$DELIVERY_PUSH_BATCH_SIZE"`,
		`WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION="$DELIVERY_PENDING_ACK_MAX_PER_SESSION"`,
		`WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("delivery script missing start env %q", want)
		}
	}
}

func TestWukongIMDeliveryBenchScriptDefaultOutDirUsesFinalScenario(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	timestamp := "test-delivery-person-default"
	outDir := filepath.Join(root, "docs", "development", "perf-runs", timestamp+"-delivery-person")
	wrongOutDir := filepath.Join(root, "docs", "development", "perf-runs", timestamp+"-delivery-group")
	t.Cleanup(func() {
		_ = os.RemoveAll(outDir)
		_ = os.RemoveAll(wrongOutDir)
	})
	_ = os.RemoveAll(outDir)
	_ = os.RemoveAll(wrongOutDir)
	writeFakeDeliveryWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeDeliveryCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-delivery.sh",
		"--no-start",
		"--no-worker",
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--scenario", "person",
		"--qps", "1",
		"--channels", "1",
		"--users", "10",
		"--duration", "0s",
		"--warmup", "0s",
		"--cooldown", "0s",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_DELIVERY_TIMESTAMP="+timestamp,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("delivery script failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(outDir, "summary.md")); err != nil {
		t.Fatalf("expected default person out dir summary: %v", err)
	}
	if _, err := os.Stat(wrongOutDir); !os.IsNotExist(err) {
		t.Fatalf("wrong default group out dir should not exist, stat err=%v", err)
	}
}

func TestWukongIMThreeNodePresenceScriptSetsPresenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-presence.sh"))

	for _, want := range []string{
		`USERS="${WK_BENCH_PRESENCE_USERS:-1000}"`,
		`CONNECT_RATE="${WK_BENCH_PRESENCE_CONNECT_RATE:-500}"`,
		`HEARTBEAT_INTERVAL="${WK_BENCH_PRESENCE_HEARTBEAT_INTERVAL:-1s}"`,
		`SAMPLE_INTERVAL="${WK_BENCH_PRESENCE_SAMPLE_INTERVAL:-1}"`,
		`STABLE_SAMPLES="${WK_BENCH_PRESENCE_STABLE_SAMPLES:-2}"`,
		`CLEANUP_TIMEOUT="${WK_BENCH_PRESENCE_CLEANUP_TIMEOUT:-0}"`,
		`PHASE_POLL_TIMEOUT="${WK_BENCH_PRESENCE_PHASE_POLL_TIMEOUT:-30s}"`,
		`REQUIRE_TOUCH="${WK_BENCH_PRESENCE_REQUIRE_TOUCH:-1}"`,
		`validate_presence_report`,
		`wait_for_presence_cleanup`,
		`cleanup_zero_status`,
		`presence-samples.jsonl`,
		`presence-summary.tsv`,
		`METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"`,
		`RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"`,
		`collect_node_logs`,
		`capture_node_pprof`,
		`sample_server_resources`,
		`server-process-summary.tsv`,
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

func TestWukongIMThreeNodePresenceScriptRecordsCleanupToZero(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--users", "10",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--cleanup-timeout", "1",
		"--sample-interval", "0.02",
		"--stable-samples", "1",
		"--resource-interval", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_PRESENCE_CLEANUP_ZERO=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script failed: %v\n%s", err, output)
	}

	summary := readFile(t, filepath.Join(outDir, "presence-summary.tsv"))
	for _, want := range []string{
		"presence_status\tpassed",
		"cleanup_zero_status\tpassed",
		"cleanup_zero_sample_count\t",
		"cleanup_zero_last_owner_routes_active\t0",
		"cleanup_zero_last_authority_routes_active\t0",
		"cleanup_zero_last_authority_routes_by_hash_slot_total\t0",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("cleanup summary missing %q:\n%s", want, summary)
		}
	}

	samples := readFile(t, filepath.Join(outDir, "presence-samples.jsonl"))
	if !strings.Contains(samples, `"sample_phase":"cleanup"`) {
		t.Fatalf("cleanup samples missing cleanup phase:\n%s", samples)
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(topSummary, "- cleanup_zero_status: passed") {
		t.Fatalf("top-level summary should include cleanup status:\n%s", topSummary)
	}
}

func TestWukongIMThreeNodePresenceScriptIgnoresCleanupExpiredForLiveGate(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--users", "10",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--cleanup-timeout", "1",
		"--sample-interval", "0.02",
		"--stable-samples", "1",
		"--resource-interval", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_PRESENCE_CLEANUP_ZERO=1",
		"WK_FAKE_PRESENCE_CLEANUP_EXPIRED=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script should pass when only cleanup samples expire authority routes: %v\n%s", err, output)
	}

	summary := readFile(t, filepath.Join(outDir, "presence-summary.tsv"))
	for _, want := range []string{
		"presence_status\tpassed",
		"max_expired_routes_total\t0",
		"cleanup_zero_status\tpassed",
		"cleanup_zero_last_expired_routes_total\t3",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("cleanup-expired summary missing %q:\n%s", want, summary)
		}
	}
}

func TestWukongIMThreeNodePresenceScriptRebuildsStaleWkbenchBinary(t *testing.T) {
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
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
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
		"--resource-interval", "0",
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

func TestWukongIMThreeNodePresenceScriptRunsBenchAndValidatesSnapshot(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
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
		"--resource-interval", "0",
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
		"--phase-poll-timeout 30s",
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
		"- metrics: metrics/cluster/",
		"- pprof: pprof/",
		"- resources: resources/",
		"- logs: logs/",
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

	for _, phase := range []string{"before", "after"} {
		for _, node := range []string{"127_0_0_1_5011", "127_0_0_1_5012", "127_0_0_1_5013"} {
			for _, rel := range []string{
				filepath.Join("metrics", "cluster", node+"-"+phase+".prom"),
				filepath.Join("pprof", phase, node+"-goroutine.txt"),
				filepath.Join("pprof", phase, node+"-heap.pb.gz"),
			} {
				requireNonEmptyFile(t, filepath.Join(outDir, rel))
			}
		}
	}
	requireNonEmptyFile(t, filepath.Join(outDir, "resources", "server-process.jsonl"))
	requireNonEmptyFile(t, filepath.Join(outDir, "resources", "server-process-summary.tsv"))

	resources := readFile(t, filepath.Join(outDir, "resources", "server-process.jsonl"))
	for _, want := range []string{
		`"node":"node1"`,
		`"phase":"before"`,
		`"phase":"after"`,
	} {
		if !strings.Contains(resources, want) {
			t.Fatalf("resource samples missing %q:\n%s", want, resources)
		}
	}
}

func TestWukongIMThreeNodePresenceScriptKeepsValidationWhenEvidenceCurlFails(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	staleEvidence := []string{
		"metrics/cluster/127_0_0_1_5011-before.prom",
		"pprof/before/127_0_0_1_5011-goroutine.txt",
	}
	for _, rel := range staleEvidence {
		path := filepath.Join(outDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, "stale evidence")
	}

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
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
		"--resource-interval", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_PRESENCE_EVIDENCE_FAIL=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script should keep validating when best-effort evidence curl fails: %v\n%s", err, output)
	}

	summary := readFile(t, filepath.Join(outDir, "presence-summary.tsv"))
	if !strings.Contains(summary, "presence_status\tpassed") {
		t.Fatalf("presence validation should still pass:\n%s", summary)
	}
	for _, rel := range []string{
		"metrics/cluster/127_0_0_1_5011-before.prom.error",
		"metrics/cluster/127_0_0_1_5011-after.prom.error",
		"pprof/before/127_0_0_1_5011-goroutine.txt.error",
		"pprof/after/127_0_0_1_5011-goroutine.txt.error",
	} {
		text := readFile(t, filepath.Join(outDir, rel))
		if !strings.Contains(text, "capture_failed") {
			t.Fatalf("evidence error marker %s missing capture failure:\n%s", rel, text)
		}
	}
	for _, rel := range staleEvidence {
		if _, err := os.Stat(filepath.Join(outDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("stale evidence file %s should be removed after capture failure, stat err=%v", rel, err)
		}
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	for _, want := range []string{
		"--connect-timeout 2 --max-time 5 http://127.0.0.1:5011/metrics",
		"--connect-timeout 2 --max-time 5 http://127.0.0.1:5011/debug/pprof/goroutine?debug=2",
	} {
		if !strings.Contains(curlCalls, want) {
			t.Fatalf("evidence curl calls should include bounded timeout %q:\n%s", want, curlCalls)
		}
	}
}

func TestWukongIMThreeNodePresenceScriptSamplesServerResourcesPeriodically(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", filepath.Join(binDir, "wkbench"),
		"--users", "10",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--sample-interval", "0.02",
		"--stable-samples", "1",
		"--resource-interval", "0.02",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_PRESENCE_RUN_SLEEP=0.08",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script failed: %v\n%s", err, output)
	}

	resources := readFile(t, filepath.Join(outDir, "resources", "server-process.jsonl"))
	if !strings.Contains(resources, `"phase":"interval"`) {
		t.Fatalf("periodic resource samples missing interval phase:\n%s", resources)
	}
}

func TestWukongIMThreeNodePresenceScriptKeepsValidationWhenResourceSampleIsInvalid(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
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
		"--resource-interval", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_ACTIVATE_PS_BAD=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script should keep validating when resource sampling is invalid: %v\n%s", err, output)
	}

	summary := readFile(t, filepath.Join(outDir, "presence-summary.tsv"))
	if !strings.Contains(summary, "presence_status\tpassed") {
		t.Fatalf("presence validation should still pass:\n%s", summary)
	}
	resources := readFile(t, filepath.Join(outDir, "resources", "server-process.jsonl"))
	if !strings.Contains(resources, `"error":"invalid_ps_sample"`) {
		t.Fatalf("resource samples should mark invalid ps output:\n%s", resources)
	}
}

func TestWukongIMThreeNodePresenceScriptFailsOnTransientPeak(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-presence.sh",
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
		"--resource-interval", "0",
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

func TestChannelRuntimeMetricsSummaryAwkSummarizesBeforeAfterPrometheus(t *testing.T) {
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
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 2
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 8
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 1
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 3
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 6
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 12
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 4
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 10
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 2
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="scheduler",queue="scheduler",priority="rpc",result="busy"} 1
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="dirty"} 4
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="requeued"} 0
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
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 6
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull",result="ok"} 26
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 3
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="rpc_pull_hint",result="ok"} 11
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_append",result="ok"} 16
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_append",result="ok"} 58
wukongim_channelv2_worker_batch_items_count{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 9
wukongim_channelv2_worker_batch_items_sum{node_id="1",node_name="node-1",kind="store_apply",result="ok"} 31
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 7
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 10
wukongim_runtime_pool_queue_bytes{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 200
wukongim_runtime_pool_queue_bytes_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 400
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="channelv2",pool="reactor_0",queue="mailbox",priority="high"} 3
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="channelv2",pool="reactor_0",queue="mailbox",priority="high"} 6
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_auth"} 8
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_auth"} 16
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="transportv2",pool="service_9"} 3
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="transportv2",pool="service_9"} 4
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 5
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="scheduler",queue="scheduler",priority="rpc",result="busy"} 3
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="dirty"} 5
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="slot",pool="scheduler",queue="scheduler",priority="none",result="requeued"} 5
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

	want := "qps_1000\tnode1\t17\t8\t9\t4\t12\t6\t7\t0.700\t200\t0.500\t8\t0.750\t3\t2\t1\t5\t2\t5\t5\t1\t15\t2\t3\t10\t1\t1.100\t60\t10\t3\t10\t6.000\t3\t4.000\t200.000\t4.000\t15\t8.000\t4\t18\t4.500\t2\t8\t4.000\t10\t46\t4.600\t5\t21\t4.200\n"
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

func TestRuntimePoolPressureSummaryAwkReportsTimeoutAdmissions(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="db",pool="message_commit",queue="commit",priority="none",result="timeout"} 2
`)
	writeFile(t, after, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="db",pool="message_commit",queue="commit",priority="none",result="timeout"} 5
`)

	cmd := exec.Command("awk",
		"-v", "tag=000100",
		"-v", "node=node1",
		"-f", filepath.Join(root, "scripts", "runtime-pool-pressure-summary.awk"),
		before,
		after,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime pool pressure awk failed: %v\n%s", err, output)
	}
	summary := string(output)
	for _, want := range []string{
		"000100\tnode1\tdb\tmessage_commit\tcommit\tnone",
		"admission_timeout",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("runtime pool pressure awk missing %q:\n%s", want, summary)
		}
	}
}

func TestAntsPoolUsageSummaryAwkReportsDedicatedAntsPoolsOnly(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 1
`)
	writeFile(t, after, `wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 7
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 10
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_channelappend_writer_pool_running{node_id="1",node_name="node-1"} 6
wukongim_channelappend_writer_pool_capacity{node_id="1",node_name="node-1"} 12
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 3
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 4
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 2
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 0.750
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 5
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 64
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 0
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 0.078
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 1
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 2
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 0
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 4
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 8
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 1
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} 4
 wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="full"} 2
`)
	sample := filepath.Join(t.TempDir(), "sample.prom")
	writeFile(t, sample, `wukongim_ants_pool_running{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 10
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 20
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 9
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 0.500
`)

	cmd := exec.Command("awk",
		"-v", "tag=000100",
		"-v", "node=node1",
		"-f", filepath.Join(root, "scripts", "ants-pool-usage-summary.awk"),
		before,
		after,
		sample,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ants pool usage awk failed: %v\n%s", err, output)
	}
	summary := string(output)
	for _, want := range []string{
		"000100\tnode1\ttransportv2\tservice_executor\t3\t4\t2\t0.750",
		"000100\tnode1\tchannelappend\tadvance\t1\t2\t0\t0.500",
		"000100\tnode1\tchannelappend\teffect\t4\t8\t1\t0.500",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("ants pool usage awk missing %q:\n%s", want, summary)
		}
	}
	for _, unwanted := range []string{
		"gateway\tasync_send",
		"gateway\tasync_auth",
		"channelappend\twriter",
		"effect_prepare",
	} {
		if strings.Contains(summary, unwanted) {
			t.Fatalf("ants pool usage awk should exclude non-ants runtime pool %q:\n%s", unwanted, summary)
		}
	}
}

func writeFakeRealQPSBenchBase(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
out_dir=""
qps=""
all_args="$*"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      out_dir="$2"
      shift 2
      ;;
    --qps)
      qps="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ -z "$out_dir" || -z "$qps" ]]; then
  echo "missing --out-dir or --qps" >&2
  exit 2
fi
mkdir -p "$out_dir"
printf '%s\n' "$all_args" > "$out_dir/base.args"
echo 'BENCH RESULT'
echo 'child summary should stay in attempt console'
cat >"$out_dir/summary.tsv" <<'OUT'
tag	offered_qps	status	exit_status	actual_qps	send_success	send_errors	connect_error_rate	sendack_error_rate	p50_seconds	p95_seconds	p99_seconds	max_seconds
000100	100	passed	0	99	99	0	0	0	0.001	0.002	0.003	0.004
OUT
cat >"$out_dir/channelv2_metrics_summary.tsv" <<'OUT'
tag	node	runtime_pool_queue_depth_max	runtime_pool_queue_fill_max	runtime_pool_queue_bytes_max	runtime_pool_queue_bytes_fill_max	runtime_pool_inflight_max	runtime_pool_inflight_util_max	runtime_pool_admission_full_delta	runtime_pool_admission_busy_delta	runtime_pool_admission_dirty_delta	runtime_pool_admission_requeued_delta
000100	node1	7	0.700	200	0.500	8	0.750	3	2	1	5
000100	node2	4	0.400	300	0.250	12	0.600	2	1	0	1
OUT
cat >"$out_dir/runtime_pool_pressure_summary.tsv" <<'OUT'
tag	node	component	pool	queue	priority	queue_depth_max	queue_capacity	queue_fill_max	queue_bytes_max	queue_bytes_capacity	queue_bytes_fill_max	inflight_max	workers	inflight_util_max	admission_full_delta	admission_busy_delta	admission_dirty_delta	admission_requeued_delta	reason
000100	node1	gateway	async_send	send	none	7	10	0.700	200	400	0.500	8	16	0.500	3	2	1	5	queue_backlog,admission_full
OUT
cat >"$out_dir/ants_pool_usage_summary.tsv" <<'OUT'
tag	node	component	pool	running	capacity	waiting	utilization_max
000100	node1	transportv2	service_executor	3	4	2	0.750
000100	node1	channelv2	store_append	5	64	0	0.078
000100	node1	channelappend	advance	1	2	0	0.500
000100	node1	channelappend	effect	4	8	1	0.500
OUT
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeThreeNode1000Wkbench(t *testing.T, path string, callsDir string, label string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '` + label + ` %s\n' "$*" >> "` + callsDir + `/wkbench.calls"
if [[ "${1:-}" == "metrics" && "${2:-}" == "classify" ]]; then
  echo 'classification: ` + label + `'
  exit 0
fi
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
  if [[ -z "$report_dir" ]]; then
    echo "missing report_dir in scenario" >&2
    exit 2
  fi
  mkdir -p "$report_dir"
  if [[ -n "${WK_FAKE_WKBENCH_RUN_SLEEP:-}" ]]; then
    sleep "$WK_FAKE_WKBENCH_RUN_SLEEP"
  fi
  cat > "$report_dir/report.json" <<'JSON'
{"status":"passed","summary":{"connect_error_rate":0,"sendack_error_rate":0},"metrics":{"counters":{"group_send_success_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":1,"group_send_error_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":0},"histograms":{"group_send_latency_seconds{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":{"p50_seconds":0.001,"p95_seconds":0.002,"p99_seconds":0.003,"max_seconds":0.004}}}}
JSON
  exit 0
fi
echo "unexpected wkbench args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeThreeNode1000GoBuildWkbench(t *testing.T, path string, callsDir string) {
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
if [[ "${1:-}" == "metrics" && "${2:-}" == "classify" ]]; then
  echo 'classification: rebuilt'
  exit 0
fi
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
  if [[ -z "$report_dir" ]]; then
    echo "missing report_dir in scenario" >&2
    exit 2
  fi
  mkdir -p "$report_dir"
  cat > "$report_dir/report.json" <<'JSON'
{"status":"passed","summary":{"connect_error_rate":0,"sendack_error_rate":0},"metrics":{"counters":{"group_send_success_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":1,"group_send_error_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":0},"histograms":{"group_send_latency_seconds{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":{"p50_seconds":0.001,"p95_seconds":0.002,"p99_seconds":0.003,"max_seconds":0.004}}}}
JSON
  exit 0
fi
echo "unexpected wkbench args: $*" >&2
exit 2
WKFAKE
chmod +x "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeThreeNode1000Curl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
echo "$*" >> "` + callsDir + `/curl.calls"
url="${@: -1}"
case "$url" in
  http://127.0.0.1:501*/readyz|http://127.0.0.1:19130/healthz|http://127.0.0.1:19130/v1/stop)
    echo 'ok'
    ;;
	  http://127.0.0.1:501*/metrics)
	    if [[ "${WK_FAKE_RUNTIME_POOL_PRESSURE:-0}" == "1" ]]; then
	      count_file="` + callsDir + `/runtime-metrics-count"
	      count=0
	      if [[ -f "$count_file" ]]; then
	        count="$(cat "$count_file")"
	      fi
	      count=$((count + 1))
	      printf '%s\n' "$count" > "$count_file"
	      cat <<OUT
wukongim_channelv2_rpc_pull_total 1
go_goroutines 1111
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 7
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 10
wukongim_runtime_pool_queue_bytes{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 512
wukongim_runtime_pool_queue_bytes_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 1024
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_auth"} 2
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_auth"} 16
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 2
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 8
wukongim_runtime_pool_queue_bytes{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 80
wukongim_runtime_pool_queue_bytes_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 160
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="transportv2",pool="service_9"} 3
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="transportv2",pool="service_9"} 4
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} $count
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="full"} $count
wukongim_channelappend_writer_pool_running{node_id="1",node_name="node-1"} 6
wukongim_channelappend_writer_pool_capacity{node_id="1",node_name="node-1"} 12
wukongim_channelappend_effect_pool_submit_total{node_id="1",node_name="node-1",stage="prepare",result="submitted"} $((count * 2))
wukongim_channelappend_effect_pool_submit_total{node_id="1",node_name="node-1",stage="prepare",result="full"} $count
wukongim_channelappend_effect_pool_submit_total{node_id="1",node_name="node-1",stage="prepare",result="error"} 0
wukongim_channelappend_effect_pool_inflight{node_id="1",node_name="node-1",stage="prepare"} 10
wukongim_channelappend_effect_pool_capacity{node_id="1",node_name="node-1",stage="prepare"} 10
wukongim_channelappend_effect_pool_saturated{node_id="1",node_name="node-1",stage="prepare"} 1
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 3
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 4
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 2
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="transportv2",pool="service_executor"} 0.750
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 5
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 64
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 0
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelv2",pool="store_append"} 0.078
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 2
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 4
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 0
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelappend",pool="advance"} 0.500
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 10
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 10
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 1
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelappend",pool="effect"} 1
wukongim_transport_sent_bytes_total{node_id="1",node_name="node-1",msg_type="rpc_request"} $((count * 1048576))
wukongim_transport_received_bytes_total{node_id="1",node_name="node-1",msg_type="rpc_response"} $((count * 524288))
OUT
	      exit 0
	    fi
	    cat <<'OUT'
wukongim_channelv2_rpc_pull_total 1
go_goroutines 1111
OUT
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
  *wukongim-node1.conf*) echo 111 ;;
  *wukongim-node2.conf*) echo 222 ;;
  *wukongim-node3.conf*) echo 333 ;;
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
if [[ "${WK_FAKE_ACTIVATE_PS_BAD:-0}" == "1" ]]; then
  echo ' cpu mem rss vsz elapsed command'
  exit 0
fi
case "$pid" in
  111) echo ' 12.5  1.2 123456 789000 00:10 /tmp/wukongim-node1' ;;
  222) echo ' 25.0  2.3 234567 890000 00:20 /tmp/wukongim-node2' ;;
  333) echo '  3.5  0.7 345678 901000 00:30 /tmp/wukongim-node3' ;;
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

func writeFakeDeliveryWkbench(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" > "` + callsDir + `/wkbench.args"
printf '%s\n' "$*" >> "` + callsDir + `/wkbench.calls"
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
  if [[ -z "$report_dir" ]]; then
    echo "missing report_dir in scenario" >&2
    exit 2
  fi
  mkdir -p "$report_dir"
  cat > "$report_dir/report.json" <<'JSON'
{"run_id":"delivery-group-000100","status":"passed","summary":{"connect_error_rate":0,"sendack_error_rate":0,"recv_verify_error_rate":0},"metrics":{"counters":{"group_send_success_total{channel_type=group,phase=run,profile=delivery-group,traffic=delivery-group-send}":20,"group_send_error_total{channel_type=group,phase=run,profile=delivery-group,traffic=delivery-group-send}":0},"histograms":{"group_send_latency_seconds{channel_type=group,phase=run,profile=delivery-group,traffic=delivery-group-send}":{"p50_seconds":0.01,"p95_seconds":0.02,"p99_seconds":0.03,"max_seconds":0.04}}}}
JSON
  echo '# fake delivery report' > "$report_dir/summary.md"
  exit 0
fi
if [[ "${1:-}" == "worker" ]]; then
  while true; do sleep 60; done
fi
echo "unexpected delivery wkbench args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeDeliveryCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
echo "$*" >> "` + callsDir + `/curl.calls"
url="${@: -1}"
delivery_call_index() {
  local key
  key="$(printf '%s' "$1" | sed 's/[^a-zA-Z0-9]/_/g')"
  local file="` + callsDir + `/delivery-${key}.count"
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
  http://127.0.0.1:501*/metrics)
    idx="$(delivery_call_index "$url")"
    if [[ "$idx" -eq 0 ]]; then
      cat <<'PROM'
wukongim_delivery_event_queue_total{result="ok"} 0
wukongim_delivery_event_queue_total{result="overflow"} 0
wukongim_delivery_event_queue_total{result="error"} 0
wukongim_delivery_errors_total{class="queue_full"} 0
wukongim_delivery_retry_total{event="drop",result="max_attempts"} 0
wukongim_delivery_retry_total{event="enqueue",result="overflow"} 0
wukongim_delivery_retry_queue_depth 0
wukongim_delivery_ack_bindings 0
wukongim_delivery_resolve_routes_total{channel_type="group",result="ok"} 0
wukongim_delivery_push_rpc_routes_total{target_node="1",result="ok"} 0
PROM
      exit 0
    fi
    case "$url" in
      http://127.0.0.1:5011/metrics)
        cat <<'PROM'
wukongim_delivery_event_queue_total{result="ok"} 20
wukongim_delivery_event_queue_total{result="overflow"} 0
wukongim_delivery_event_queue_total{result="error"} 0
wukongim_delivery_errors_total{class="queue_full"} 0
wukongim_delivery_retry_total{event="drop",result="max_attempts"} 0
wukongim_delivery_retry_total{event="enqueue",result="overflow"} 0
wukongim_delivery_retry_queue_depth 0
wukongim_delivery_ack_bindings 0
wukongim_delivery_resolve_routes_total{channel_type="group",result="ok"} 1000
wukongim_delivery_push_rpc_routes_total{target_node="1",result="ok"} 1000
PROM
        ;;
      *)
        cat <<'PROM'
wukongim_delivery_event_queue_total{result="ok"} 0
wukongim_delivery_event_queue_total{result="overflow"} 0
wukongim_delivery_event_queue_total{result="error"} 0
wukongim_delivery_errors_total{class="queue_full"} 0
wukongim_delivery_retry_total{event="drop",result="max_attempts"} 0
wukongim_delivery_retry_total{event="enqueue",result="overflow"} 0
wukongim_delivery_retry_queue_depth 0
wukongim_delivery_ack_bindings 0
wukongim_delivery_resolve_routes_total{channel_type="group",result="ok"} 0
wukongim_delivery_push_rpc_routes_total{target_node="1",result="ok"} 0
PROM
        ;;
    esac
    ;;
  http://127.0.0.1:501*/debug/pprof/goroutine?debug=2)
    echo 'goroutine profile'
    ;;
  http://127.0.0.1:501*/debug/pprof/heap)
    echo 'heap profile'
    ;;
  *)
    echo "unexpected delivery curl url: $url" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeDeliveryStartScript(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
env | LC_ALL=C sort | grep -E '^(WK_DELIVERY_|WK_PPROF_)' > "` + callsDir + `/start.env"
printf '%s\n' "$*" > "` + callsDir + `/start.args"
if [[ "${1:-}" == "--dry-run" ]]; then
  echo 'fake delivery start dry-run'
  exit 0
fi
while true; do sleep 60; done
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
  http://127.0.0.1:501*/metrics)
    if [[ "${WK_FAKE_PRESENCE_EVIDENCE_FAIL:-0}" == "1" ]]; then
      echo 'metrics unavailable' >&2
      exit 28
    fi
    echo 'metric 1'
    ;;
  http://127.0.0.1:501*/debug/pprof/goroutine?debug=2)
    if [[ "${WK_FAKE_PRESENCE_EVIDENCE_FAIL:-0}" == "1" ]]; then
      echo 'goroutine unavailable' >&2
      exit 28
    fi
    echo 'goroutine profile'
    ;;
  http://127.0.0.1:501*/debug/pprof/heap)
    if [[ "${WK_FAKE_PRESENCE_EVIDENCE_FAIL:-0}" == "1" ]]; then
      echo 'heap unavailable' >&2
      exit 28
    fi
    echo 'heap profile'
    ;;
  http://127.0.0.1:5011/bench/v1/presence/snapshot)
    idx="$(presence_call_index "$url")"
    if [[ "${WK_BENCH_PRESENCE_SAMPLE_PHASE:-}" == "cleanup" && "${WK_FAKE_PRESENCE_CLEANUP_ZERO:-0}" == "1" && "${WK_FAKE_PRESENCE_CLEANUP_EXPIRED:-0}" == "1" ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":1,"owner_routes_active":0,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":0,"authority_routes_by_hash_slot":{},"touch_routes_total":2,"expired_routes_total":1}
JSON
      exit 0
    fi
    if [[ "${WK_BENCH_PRESENCE_SAMPLE_PHASE:-}" == "cleanup" && "${WK_FAKE_PRESENCE_CLEANUP_ZERO:-0}" == "1" ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":1,"owner_routes_active":0,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":0,"authority_routes_by_hash_slot":{},"touch_routes_total":2,"expired_routes_total":0}
JSON
      exit 0
    fi
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
    if [[ "${WK_BENCH_PRESENCE_SAMPLE_PHASE:-}" == "cleanup" && "${WK_FAKE_PRESENCE_CLEANUP_ZERO:-0}" == "1" && "${WK_FAKE_PRESENCE_CLEANUP_EXPIRED:-0}" == "1" ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":2,"owner_routes_active":0,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":0,"authority_routes_by_hash_slot":{},"touch_routes_total":2,"expired_routes_total":1}
JSON
      exit 0
    fi
    if [[ "${WK_BENCH_PRESENCE_SAMPLE_PHASE:-}" == "cleanup" && "${WK_FAKE_PRESENCE_CLEANUP_ZERO:-0}" == "1" ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":2,"owner_routes_active":0,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":0,"authority_routes_by_hash_slot":{},"touch_routes_total":2,"expired_routes_total":0}
JSON
      exit 0
    fi
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
    if [[ "${WK_BENCH_PRESENCE_SAMPLE_PHASE:-}" == "cleanup" && "${WK_FAKE_PRESENCE_CLEANUP_ZERO:-0}" == "1" && "${WK_FAKE_PRESENCE_CLEANUP_EXPIRED:-0}" == "1" ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":3,"owner_routes_active":0,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":0,"authority_routes_by_hash_slot":{},"touch_routes_total":2,"expired_routes_total":1}
JSON
      exit 0
    fi
    if [[ "${WK_BENCH_PRESENCE_SAMPLE_PHASE:-}" == "cleanup" && "${WK_FAKE_PRESENCE_CLEANUP_ZERO:-0}" == "1" ]]; then
      cat <<'JSON'
{"version":"bench/v1","node_id":3,"owner_routes_active":0,"owner_routes_pending":0,"owner_touched_dirty":0,"authority_routes_active":0,"authority_routes_by_hash_slot":{},"touch_routes_total":2,"expired_routes_total":0}
JSON
      exit 0
    fi
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

func requireNonEmptyFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected non-empty file %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty file %s", path)
	}
}

func listenLocalTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return ln.Addr().String()
}
