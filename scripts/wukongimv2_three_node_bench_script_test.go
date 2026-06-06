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
		"runtime_pool_queue_depth_max",
		"runtime_pool_admission_full_delta",
		"- runtime_pool_metrics: channelv2_metrics_summary.tsv runtime_pool_* columns",
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
		"--concurrency N        wkbench send concurrency. Default: 2800.",
		"ack_timeout: $ACK_TIMEOUT",
		"recv_ack: $RECV_ACK",
		"enabled: $HEARTBEAT_ENABLED",
		`--phase-poll-timeout "$PHASE_POLL_TIMEOUT"`,
		"--recv-ack BOOL",
		"--heartbeat BOOL",
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-128}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-0}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-0}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1100us}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SYNC="${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}"`,
		`WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}"`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-0}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-0}`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1100us}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_DISPATCH_WORKERS=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS:-1024}`,
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

func TestWukongIMV2ThreeNodeRealQPSScriptUses15KTunedDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-three-nodes-real-qps.sh"))

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
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1100us}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-0}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-0}`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_DISPATCH_WORKERS=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS:-1024}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"runtime_pool_attempt_summary",
		"runtime_pool_queue_fill_max",
		"runtime_pool_admission_busy_delta",
		"- runtime_pool_metrics: each attempt's channelv2_metrics_summary.tsv runtime_pool_* columns",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("real-qps script missing high-concurrency default %q", want)
		}
	}
}

func TestWukongIMV2ThreeNodeRealQPSScriptAggregatesRuntimePoolMetrics(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	outDir := t.TempDir()
	baseScript := filepath.Join(binDir, "fake-base.sh")
	writeFakeRealQPSBenchBase(t, baseScript)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-real-qps.sh",
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

	summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
	for _, want := range []string{
		"runtime_pool_queue_depth_max",
		"runtime_pool_queue_fill_max",
		"runtime_pool_admission_full_delta",
		"\t7\t0.700\t300\t0.500\t12\t0.750\t5\t3\t1\t6\tPASS\tok\t",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("real-qps summary missing %q:\n%s", want, summary)
		}
	}

	display := readFile(t, filepath.Join(outDir, "summary.txt"))
	for _, want := range []string{
		"poolfill",
		"poolinflight",
		"0.700",
		"# runtime pool pressure",
		"entries=1",
		"max_fill=0.700",
		"full=3",
		"busy=2",
		"worst=offered=100",
		"gateway",
		"async_send",
		"queue_backlog",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("real-qps display summary missing %q:\n%s", want, display)
		}
	}
	if strings.Contains(display, "component        pool") {
		t.Fatalf("real-qps display summary should not print per-pool table rows:\n%s", display)
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- runtime_pool_pressure: runtime_pool_pressure_summary.tsv",
		"entries=1",
		"max_fill=0.700",
		"worst=offered=100",
		"gateway",
		"async_send",
		"admission_full",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("real-qps markdown summary missing %q:\n%s", want, topSummary)
		}
	}
	if strings.Contains(topSummary, "component\tpool\tqueue\tpriority") {
		t.Fatalf("real-qps markdown summary should not print every pressure row:\n%s", topSummary)
	}
}

func TestWukongIMV2ThreeNodeBenchScriptPrintsRuntimePoolPressureSummary(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-1000ch.sh",
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

	display := readFile(t, filepath.Join(outDir, "summary.txt"))
	for _, want := range []string{
		"# runtime pool pressure",
		"entries=",
		"max_fill=0.700",
		"full=",
		"worst=tag=000100",
		"gateway",
		"async_send",
		"queue_backlog",
		"admission_full",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("display summary missing runtime pool pressure %q:\n%s", want, display)
		}
	}
	if strings.Contains(display, "component        pool") {
		t.Fatalf("display summary should not print per-pool table rows:\n%s", display)
	}

	topSummary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- runtime_pool_pressure: runtime_pool_pressure_summary.tsv",
		"entries=",
		"max_fill=0.700",
		"worst=tag=000100",
		"gateway",
		"async_send",
		"admission_full",
	} {
		if !strings.Contains(topSummary, want) {
			t.Fatalf("markdown summary missing runtime pool pressure %q:\n%s", want, topSummary)
		}
	}
	if strings.Contains(topSummary, "component\tpool\tqueue\tpriority") {
		t.Fatalf("markdown summary should not print every pressure row:\n%s", topSummary)
	}
}

func TestWukongIMV2ThreeNodeBenchScriptRebuildsStaleWkbenchBinary(t *testing.T) {
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

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-1000ch.sh",
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

func TestWukongIMV2ThreeNodeBenchScriptCanDisableHeartbeat(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNode1000Wkbench(t, filepath.Join(binDir, "wkbench"), callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-1000ch.sh",
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

func TestWukongIMV2DeliveryBenchScriptIsLocalThreeNodeOnly(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-delivery.sh"))

	for _, want := range []string{
		"scripts/start-wukongimv2-three-nodes.sh",
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
		"bench-wukongimv2-three-nodes-real-qps.sh",
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

func TestWukongIMV2DeliveryBenchScriptGeneratesGroupScenarioAndSummary(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeDeliveryWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakeDeliveryCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-delivery.sh",
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

func TestWukongIMV2DeliveryBenchScriptInjectsDeliveryEnvWhenStartingCluster(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-delivery.sh"))
	for _, want := range []string{
		`WK_DELIVERY_ENABLE="$DELIVERY_ENABLE"`,
		`WK_DELIVERY_EVENT_QUEUE_SIZE="$DELIVERY_EVENT_QUEUE_SIZE"`,
		`WK_DELIVERY_FANOUT_PAGE_SIZE="$DELIVERY_FANOUT_PAGE_SIZE"`,
		`WK_DELIVERY_PUSH_BATCH_SIZE="$DELIVERY_PUSH_BATCH_SIZE"`,
		`WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION="$DELIVERY_PENDING_ACK_MAX_PER_SESSION"`,
		`WK_PPROF_ENABLE="${WK_PPROF_ENABLE:-true}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("delivery script missing start env %q", want)
		}
	}
}

func TestWukongIMV2DeliveryBenchScriptDefaultOutDirUsesFinalScenario(t *testing.T) {
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

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-delivery.sh",
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

func TestWukongIMV2ThreeNodePresenceScriptSetsPresenceDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-three-nodes-presence.sh"))

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

func TestWukongIMV2ThreeNodePresenceScriptRecordsCleanupToZero(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-presence.sh",
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

func TestWukongIMV2ThreeNodePresenceScriptIgnoresCleanupExpiredForLiveGate(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-presence.sh",
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
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

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

func TestWukongIMV2ThreeNodePresenceScriptRunsBenchAndValidatesSnapshot(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

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

func TestWukongIMV2ThreeNodePresenceScriptKeepsValidationWhenEvidenceCurlFails(t *testing.T) {
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

func TestWukongIMV2ThreeNodePresenceScriptSamplesServerResourcesPeriodically(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

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

func TestWukongIMV2ThreeNodePresenceScriptKeepsValidationWhenResourceSampleIsInvalid(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

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

func TestWukongIMV2ThreeNodePresenceScriptFailsOnTransientPeak(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
	writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

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

	want := "qps_1000\tnode1\t17\t8\t9\t4\t12\t6\t7\t0.700\t200\t0.500\t8\t0.750\t3\t2\t1\t5\t2\t5\t5\t1\t15\t2\t3\t10\t1\t1.100\t60\t10\t3\t10\t6.000\t3\t4.000\t200.000\t4.000\t15\t8.000\n"
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

func writeFakeRealQPSBenchBase(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
out_dir=""
qps=""
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
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 7
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 10
wukongim_runtime_pool_queue_bytes{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 512
wukongim_runtime_pool_queue_bytes_capacity{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none"} 1024
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="gateway",pool="async_send"} 16
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="gateway",pool="async_send",queue="send",priority="none",result="full"} $count
OUT
	      exit 0
	    fi
	    cat <<'OUT'
wukongim_channelv2_rpc_pull_total 1
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
if [[ "${WK_FAKE_ACTIVATE_PS_BAD:-0}" == "1" ]]; then
  echo ' cpu mem rss vsz elapsed command'
  exit 0
fi
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
