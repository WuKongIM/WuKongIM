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
		"channel_metrics_summary",
		"scripts/channel-metrics-summary.awk",
		"channel_metrics_summary.tsv",
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
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"`,
		`THREE_NODE_DATA_ROOT="${WK_WUKONGIM_THREE_NODES_DATA_ROOT:-$ROOT_DIR/data}"`,
		`THREE_NODE_DATA_ROOT_SOURCE="default"`,
		`if [[ -n "${WK_WUKONGIM_THREE_NODES_DATA_ROOT:-}" ]]; then`,
		`STORAGE_FREE_WARN_PERCENT="${WK_BENCH_STORAGE_FREE_WARN_PERCENT:-5}"`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`THREE_NODE_DATA_ROOT=$THREE_NODE_DATA_ROOT`,
		"write_storage_preflight",
		"storage-preflight.tsv",
		"external_cluster_data_root_unverified",
		`df -Pk "$probe_path" | awk 'NR == 2 { print; exit }'`,
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
		`WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`WK_TOP_API_ENABLE="${WK_TOP_API_ENABLE:-false}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}"`,
		`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-1}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SYNC="${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}"`,
		`WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}"`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}`,
		`CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}`,
		`CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}`,
		`TOP_API_ENABLE=${WK_TOP_API_ENABLE:-false}`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}`,
		`CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-1}`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"gateway_ready()",
		`>/dev/tcp/"$host"/"$port"`,
		`for gateway in "${GATEWAY_VALUES[@]}"`,
		"/debug/pprof/goroutine?debug=2",
		"/debug/pprof/heap",
		"start_run_pprof_sampler",
		"wait_run_pprof_sampler",
		"capture_node_pprof run",
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

func TestWukongIMThreeNodeBenchScriptUsesUniqueClientMessagePrefixesByDefaultAndAllowsOverride(t *testing.T) {
	root := repoRoot(t)
	firstOut := t.TempDir()
	secondOut := t.TempDir()

	for _, outDir := range []string{firstOut, secondOut} {
		output, err := runFakeThreeNode1000Bench(t, root, outDir, nil)
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, output)
		}
	}

	firstScenario := readFile(t, filepath.Join(firstOut, "scenario-000001.yaml"))
	secondScenario := readFile(t, filepath.Join(secondOut, "scenario-000001.yaml"))
	firstPrefix := yamlScalar(firstScenario, "client_msg_prefix:")
	secondPrefix := yamlScalar(secondScenario, "client_msg_prefix:")
	if firstPrefix == "" || secondPrefix == "" {
		t.Fatalf("generated scenarios should contain client_msg_prefix:\nfirst:\n%s\nsecond:\n%s", firstScenario, secondScenario)
	}
	if firstPrefix == secondPrefix {
		t.Fatalf("default client message prefixes should differ across runs, both were %q", firstPrefix)
	}
	if !strings.Contains(firstScenario, "fail_on_soft: true") {
		t.Fatalf("capacity scenario should fail on the soft p99 gate:\n%s", firstScenario)
	}

	overrideOut := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, overrideOut, nil, "--client-msg-prefix", "repeatable-msg")
	if err != nil {
		t.Fatalf("script with explicit client message prefix failed: %v\n%s", err, output)
	}
	overrideScenario := readFile(t, filepath.Join(overrideOut, "scenario-000001.yaml"))
	if got := yamlScalar(overrideScenario, "client_msg_prefix:"); got != "repeatable-msg" {
		t.Fatalf("explicit client message prefix = %q, want repeatable-msg:\n%s", got, overrideScenario)
	}
}

func TestWukongIMThreeNodeBenchScriptKeepsSamplersAsChildProcesses(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-1000ch.sh"))
	for _, forbidden := range []string{
		`$(start_runtime_pool_sampler`,
		`$(start_run_pprof_sampler`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("sampler started through command substitution cannot be waited by the parent shell: %s", forbidden)
		}
	}
	for _, want := range []string{
		`RUNTIME_POOL_SAMPLER_PID="$!"`,
		`RUN_PPROF_PID="$!"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bench script missing parent-owned sampler pid %q", want)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptWaitsForRuntimeQueuesAndRecordsEvidence(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir, nil)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	evidence := readFile(t, filepath.Join(outDir, "quiescence", "000001.tsv"))
	for _, want := range []string{
		"node\tdelivery_queue_depth\tdelivery_worker_inflight\tdelivery_worker_capacity\tpost_commit_backlog\tgateway_queue_depth\tstate",
		"127_0_0_1_5011\t0\t0\t100\t0\t0\tdrained",
		"# result=passed",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("quiescence evidence missing %q:\n%s", want, evidence)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptFailsWhenRuntimeQueuesDoNotConverge(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_QUIESCENCE_BUSY=1"},
		"--quiescence-timeout", "1",
		"--quiescence-poll-interval", "0.05",
	)
	if err == nil {
		t.Fatalf("script should fail when runtime queues never converge:\n%s", output)
	}
	if !strings.Contains(string(output), "runtime queues did not converge") {
		t.Fatalf("timeout should explain the convergence failure:\n%s", output)
	}

	evidence := readFile(t, filepath.Join(outDir, "quiescence", "000001.tsv"))
	for _, want := range []string{
		"127_0_0_1_5011\t3\t0\t100\t2\t1\tpending",
		"# result=timeout",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("timeout evidence missing %q:\n%s", want, evidence)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptWaitsForRecipientWorkerInflight(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_QUIESCENCE_INFLIGHT=1"},
		"--quiescence-timeout", "1",
		"--quiescence-poll-interval", "0.05",
	)
	if err == nil {
		t.Fatalf("script should fail while recipient delivery work remains inflight:\n%s", output)
	}
	evidence := readFile(t, filepath.Join(outDir, "quiescence", "000001.tsv"))
	if !strings.Contains(evidence, "127_0_0_1_5011\t0\t1\t100\t0\t0\tpending") {
		t.Fatalf("quiescence evidence should include inflight delivery work:\n%s", evidence)
	}
}

func TestWukongIMThreeNodeBenchScriptRejectsRecipientWorkerCapacityMismatch(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_DELIVERY_WORKER_CAPACITY=200"},
	)
	if err == nil {
		t.Fatalf("script should fail when live recipient worker capacity differs from benchmark evidence:\n%s", output)
	}
	if !strings.Contains(string(output), "recipient worker capacity mismatch") {
		t.Fatalf("capacity mismatch should be explicit:\n%s", output)
	}
	evidence := readFile(t, filepath.Join(outDir, "quiescence", "000001.tsv"))
	if !strings.Contains(evidence, "127_0_0_1_5011\t0\t0\t200\t0\t0\tcapacity_mismatch") {
		t.Fatalf("quiescence evidence should record the live mismatched capacity:\n%s", evidence)
	}
}

func TestWukongIMThreeNodeBenchScriptRejectsAppendEffectMismatchAsInvalidSample(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_APPEND_EFFECT_MISMATCH=1"},
	)
	if err == nil {
		t.Fatalf("script should fail when successful appends do not match worker successes:\n%s", output)
	}

	validity := readFile(t, filepath.Join(outDir, "sample-validity.tsv"))
	if !strings.Contains(validity, "000001\t1\t0\tfalse\tappend_effect_mismatch") {
		t.Fatalf("sample validity evidence should identify append effect mismatch:\n%s", validity)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
	if !strings.Contains(summary, "\tfalse\t1\t0\tappend_effect_mismatch") {
		t.Fatalf("benchmark summary should mark the sample invalid:\n%s", summary)
	}
}

func TestWukongIMThreeNodeBenchScriptRejectsMissingAppendEffectMetricOnOneNode(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_APPEND_EFFECT_MISSING_NODE=1"},
		"--metrics", "http://127.0.0.1:5011,http://127.0.0.1:5012",
	)
	if err == nil {
		t.Fatalf("script should fail when any node is missing append-effect evidence:\n%s", output)
	}

	validity := readFile(t, filepath.Join(outDir, "sample-validity.tsv"))
	if !strings.Contains(validity, "000001\t1\t0\tfalse\tmissing_append_effect_metrics") {
		t.Fatalf("sample validity should reject partial-node append evidence:\n%s", validity)
	}
}

func TestWukongIMThreeNodeBenchScriptRejectsEmptySampleAsInvalid(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_WKBENCH_SUCCESS_TOTAL=0", "WK_FAKE_APPEND_EFFECT_MISMATCH=1"},
	)
	if err == nil {
		t.Fatalf("script should fail when a sample has no successful messages:\n%s", output)
	}

	validity := readFile(t, filepath.Join(outDir, "sample-validity.tsv"))
	if !strings.Contains(validity, "000001\t0\t0\tfalse\tno_successful_messages") {
		t.Fatalf("sample validity evidence should reject a vacuous zero-equals-zero sample:\n%s", validity)
	}
}

func TestWukongIMThreeNodeBenchScriptFailsWhenSoftP99ExceedsLimit(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{"WK_FAKE_WKBENCH_P99_SECONDS=0.5"},
	)
	if err == nil {
		t.Fatalf("script should fail when soft p99 exceeds the configured limit:\n%s", output)
	}
	if !strings.Contains(readFile(t, filepath.Join(outDir, "summary.txt")), "p99") {
		t.Fatalf("summary should explain the p99 gate failure")
	}
}

func TestWukongIMThreeNodeBenchScriptGatesRunPprofOnActiveRunPhase(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	violationFile := filepath.Join(outDir, "pprof-phase-violation")
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{
			"WK_FAKE_WKBENCH_PHASED_RUN=1",
			"WK_BENCH_PROFILE_PHASE_TIMEOUT=3",
			"WK_FAKE_PROFILE_VIOLATION_FILE=" + violationFile,
		},
		"--profile-seconds", "1",
	)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(violationFile); !os.IsNotExist(err) {
		t.Fatalf("run pprof was captured outside the active run phase")
	}

	runDir := filepath.Join(outDir, "pprof", "run", "000001")
	sampler := readFile(t, filepath.Join(runDir, "sampler.tsv"))
	if !strings.Contains(sampler, "three-node-fixed-1ch-000001-qps") || !strings.Contains(sampler, "\ttrue\tok") {
		t.Fatalf("run pprof sampler should record a valid active-run capture:\n%s", sampler)
	}
	requireNonEmptyFile(t, filepath.Join(runDir, "worker-status.json"))
	endStatus := readFile(t, filepath.Join(runDir, "worker-status-end.json"))
	if !strings.Contains(endStatus, `"active_phase":"run"`) {
		t.Fatalf("run pprof sampler should preserve active-run status at capture completion:\n%s", endStatus)
	}
	requireNonEmptyFile(t, filepath.Join(runDir, "127_0_0_1_5011-cpu.pb.gz"))
	for _, phase := range []string{"before", "after"} {
		cpuPath := filepath.Join(outDir, "pprof", phase, "127_0_0_1_5011-cpu.pb.gz")
		if _, err := os.Stat(cpuPath); !os.IsNotExist(err) {
			t.Fatalf("%s CPU profile should not be captured outside the active run phase", phase)
		}
	}
}

func TestWukongIMThreeNodeBenchScriptRejectsRunPprofThatEndsInCooldown(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{
			"WK_FAKE_WKBENCH_PHASED_RUN=1",
			"WK_BENCH_PROFILE_PHASE_TIMEOUT=3",
			"WK_FAKE_PROFILE_TRANSITION_TO_COOLDOWN=1",
		},
		"--profile-seconds", "1",
	)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	runDir := filepath.Join(outDir, "pprof", "run", "000001")
	sampler := readFile(t, filepath.Join(runDir, "sampler.tsv"))
	if !strings.Contains(sampler, "\tfalse\tactive_run_ended_during_capture") {
		t.Fatalf("run pprof sampler should reject a capture that crossed into cooldown:\n%s", sampler)
	}
	endStatus := readFile(t, filepath.Join(runDir, "worker-status-end.json"))
	if !strings.Contains(endStatus, `"active_phase":"cooldown"`) {
		t.Fatalf("run pprof sampler should preserve cooldown status at capture completion:\n%s", endStatus)
	}
}

func TestWukongIMThreeNodeBenchScriptKeepsRunPprofPerQPSAttempt(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	output, err := runFakeThreeNode1000Bench(t, root, outDir,
		[]string{
			"WK_FAKE_WKBENCH_PHASED_RUN=1",
			"WK_BENCH_PROFILE_PHASE_TIMEOUT=3",
		},
		"--qps", "1,2",
		"--profile-seconds", "1",
	)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	for _, tag := range []string{"000001", "000002"} {
		runDir := filepath.Join(outDir, "pprof", "run", tag)
		sampler := readFile(t, filepath.Join(runDir, "sampler.tsv"))
		expectedRunID := "three-node-fixed-1ch-" + tag + "-qps"
		if !strings.Contains(sampler, expectedRunID) || !strings.Contains(sampler, "\ttrue\tok") {
			t.Fatalf("run pprof evidence for %s was not preserved:\n%s", tag, sampler)
		}
		requireNonEmptyFile(t, filepath.Join(runDir, "127_0_0_1_5011-cpu.pb.gz"))
	}
}

func TestFakeThreeNodeStatusSnapshotCannotAcknowledgeAnotherRun(t *testing.T) {
	callsDir := t.TempDir()
	curlPath := filepath.Join(t.TempDir(), "curl")
	writeFakeThreeNode1000Curl(t, curlPath, callsDir)

	const firstRunID = "three-node-fixed-1ch-000001-qps"
	const secondRunID = "three-node-fixed-1ch-000002-qps"
	statePath := filepath.Join(callsDir, "wkbench.state")
	publishFakeState := func(runID, phase string) {
		t.Helper()
		tmp := statePath + ".tmp"
		if err := os.WriteFile(tmp, []byte(runID+"\t"+phase+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, statePath); err != nil {
			t.Fatal(err)
		}
	}
	publishFakeState(firstRunID, "done")
	if err := os.WriteFile(filepath.Join(callsDir, "profile."+firstRunID+".finished"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	readyFile := filepath.Join(callsDir, "delayed-status.ready")
	releaseFile := filepath.Join(callsDir, "delayed-status.release")
	cmd := exec.Command(curlPath, "-fsS", "http://127.0.0.1:19130/v1/status")
	cmd.Env = append(os.Environ(),
		"WK_FAKE_STATUS_DELAY_RUN_ID="+firstRunID,
		"WK_FAKE_STATUS_DELAY_PHASE=done",
		"WK_FAKE_STATUS_DELAY_READY_FILE="+readyFile,
		"WK_FAKE_STATUS_DELAY_RELEASE_FILE="+releaseFile,
	)
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(readyFile); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("fake status did not expose its delayed first-run snapshot: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	publishFakeState(secondRunID, "run")
	if err := os.WriteFile(filepath.Join(callsDir, "profile."+secondRunID+".finished"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releaseFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("delayed fake status failed: %v\n%s", err, output.String())
	}

	if !strings.Contains(output.String(), `"run_id":"`+firstRunID+`"`) {
		t.Fatalf("status should return its atomic first-run snapshot:\n%s", output.String())
	}
	if _, err := os.Stat(filepath.Join(callsDir, "profile."+firstRunID+".end_checked")); err != nil {
		t.Fatalf("first-run status should acknowledge only its own profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(callsDir, "profile."+secondRunID+".end_checked")); !os.IsNotExist(err) {
		t.Fatalf("delayed first-run status must not acknowledge the second run: %v", err)
	}
}

func TestWukongIMBenchDefaultEvidenceAssertionsUsePrimaryChannelSummary(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "wukongim_three_node_bench_script_test.go"))
	legacySummary := "channelv2" + "_metrics_summary.tsv"
	for _, testName := range []string{
		"TestWukongIMThreeNodeBenchScriptCollectsLocalEvidence",
		"TestWukongIMThreeNodeRealQPSScriptUses15KTunedDefaults",
	} {
		start := strings.Index(source, "func "+testName)
		if start < 0 {
			t.Fatalf("test %s not found", testName)
		}
		end := strings.Index(source[start+1:], "\nfunc ")
		body := source[start:]
		if end >= 0 {
			body = source[start : start+1+end]
		}
		if strings.Contains(body, legacySummary) {
			t.Fatalf("%s should assert channel_metrics_summary.tsv as the default; legacy alias belongs in dedicated compatibility tests", testName)
		}
	}
}

func TestWukongIMBenchScriptsKeepChannelSummaryLegacyAliasCompatibility(t *testing.T) {
	root := repoRoot(t)
	legacySummary := "channelv2" + "_metrics_summary.tsv"
	for _, scriptPath := range []string{
		"scripts/bench-wukongim-three-nodes-1000ch.sh",
		"scripts/bench-wukongim-single-node-1000ch.sh",
	} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readFile(t, filepath.Join(root, scriptPath))
			for _, want := range []string{
				`local out="$OUT_DIR/channel_metrics_summary.tsv"`,
				`local legacy_out="$OUT_DIR/` + legacySummary + `"`,
				`cp "$out" "$legacy_out"`,
				`cp "$OUT_DIR/channel_metrics_summary.tsv" "$OUT_DIR/` + legacySummary + `"`,
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("bench script missing channel summary legacy alias compatibility %q", want)
				}
			}
		})
	}
}

func TestWukongIMBenchScriptsStopOnlyExactWorkerAssignment(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		path        string
		cleanupCall string
	}{
		{path: "scripts/bench-wukongim-three-nodes-1000ch.sh", cleanupCall: `stop_worker_exact_from_status "before qps=$qps"`},
		{path: "scripts/bench-wukongim-single-node-1000ch.sh", cleanupCall: `stop_worker_exact_from_status "before qps=$qps"`},
		{path: "scripts/bench-wukongim-delivery.sh", cleanupCall: `stop_worker_exact_from_status "script cleanup"`},
		{path: "scripts/bench-wukongim-three-nodes-presence.sh", cleanupCall: `stop_worker_exact_from_status "script cleanup"`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			script := readFile(t, filepath.Join(root, tc.path))
			for _, want := range []string{
				"stop_worker_exact_from_status()",
				`.assignment.assignment_id // ""`,
				`{run_id:$run_id,assignment_id:$assignment_id}`,
				`--data "$payload" "${WORKER_ADDR%/}/v1/stop"`,
				`.phase == "stopped"`,
				tc.cleanupCall,
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("bench script missing exact worker cleanup contract %q", want)
				}
			}
			if strings.Contains(script, `curl -fsS -X POST "${WORKER_ADDR%/}/v1/stop"`) {
				t.Fatal("bench script must not fall back to an empty or run-only worker stop")
			}
		})
	}
}

func TestWukongIMRealQPSScriptsFallbackToLegacyChannelSummaryAlias(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
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
				"WK_FAKE_LEGACY_CHANNEL_SUMMARY_ONLY=1",
				"GOWORK=off",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("real-qps script failed: %v\n%s", err, output)
			}

			summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
			if !strings.Contains(summary, "\t9\t0.900\t900\t0.800\t18\t0.950\t8\t7\t6\t5\t") {
				t.Fatalf("real-qps summary should fall back to legacy channel summary alias:\n%s", summary)
			}
		})
	}
}

func TestWukongIMThreeNodeRealQPSScriptUses15KTunedDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-real-qps.sh"))

	for _, want := range []string{
		`CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"`,
		`CLUSTER_CHANNEL_REACTOR_COUNT=${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}`,
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
		`TOP_API_ENABLE=${WK_TOP_API_ENABLE:-false}`,
		`CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}`,
		`CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}`,
		`CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-1}`,
		`CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}`,
		`CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}`,
		`CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}`,
		`WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}"`,
		`WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"`,
		`WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"`,
		`DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY`,
		`WK_TOP_API_ENABLE="${WK_TOP_API_ENABLE:-false}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}"`,
		`WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-1}"`,
		`CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}`,
		`GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}`,
		`GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}`,
		`GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}`,
		"runtime_pool_attempt_summary",
		"channel_metrics_summary.tsv",
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

func TestWukongIMThreeNodeRealQPSScriptForwardsValidityControls(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	baseScript := filepath.Join(t.TempDir(), "fake-base.sh")
	writeFakeRealQPSBenchBase(t, baseScript)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-real-qps.sh",
		"--qps", "100",
		"--out-dir", outDir,
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--client-msg-prefix", "repeatable-real-qps",
		"--quiescence-timeout", "23",
		"--quiescence-poll-interval", "0.25",
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

	args := readFile(t, filepath.Join(outDir, "000100-qps", "base.args"))
	for _, want := range []string{
		"--client-msg-prefix repeatable-real-qps",
		"--quiescence-timeout 23",
		"--quiescence-poll-interval 0.25",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("real-qps child args missing %q:\n%s", want, args)
		}
	}
	envText := readFile(t, filepath.Join(outDir, "env.txt"))
	if !strings.Contains(envText, "CLIENT_MSG_PREFIX=repeatable-real-qps") {
		t.Fatalf("real-qps evidence should record explicit client prefix:\n%s", envText)
	}
}

func TestWukongIMThreeNodeRealQPSScriptReturnsFailureWhenP99GateFails(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	baseScript := filepath.Join(t.TempDir(), "fake-base.sh")
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
		"WK_FAKE_REAL_QPS_P99_SECONDS=0.5",
		"GOWORK=off",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("real-qps capacity script should return failure when p99 gate fails:\n%s", output)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
	if !strings.Contains(summary, "\tFAIL\tp99\t") {
		t.Fatalf("real-qps summary should identify the p99 gate:\n%s\noutput:\n%s", summary, output)
	}
}

func TestWukongIMThreeNodeRealQPSScriptPropagatesChildFailureWithoutSummaryRow(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	baseScript := filepath.Join(t.TempDir(), "fake-base.sh")
	writeFakeRealQPSBenchBase(t, baseScript)

	cmd := exec.Command("bash", "scripts/bench-wukongim-three-nodes-real-qps.sh",
		"--qps", "100,200",
		"--out-dir", outDir,
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WK_BENCH_REAL_QPS_BASE_SCRIPT="+baseScript,
		"WK_FAKE_REAL_QPS_EMPTY_SUMMARY_QPS=200",
		"GOWORK=off",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("real-qps wrapper should propagate a later child failure without a summary row:\n%s", output)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.tsv"))
	if !strings.Contains(summary, "000200\t200\t") || !strings.Contains(summary, "\t7\tmissing_summary_row\t") {
		t.Fatalf("parent summary should retain the failed child attempt:\n%s\noutput:\n%s", summary, output)
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
		"transport/service_executor",
		"3/4",
		"channel/store_append",
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
		"channelv2/store_append",
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
		"100\t" + filepath.Join(outDir, "000100-qps") + "\t000100\tnode1\ttransport\tservice_executor\t3\t4\t2\t0.750",
		"100\t" + filepath.Join(outDir, "000100-qps") + "\t000100\tnode1\tchannel\tstore_append\t5\t64\t0\t0.078",
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
		"transport/service_executor",
		"channel/store_append",
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
		"channelv2/store_append",
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
		"channel\tstore_apply\tapply\tnormal",
		"queue_backlog",
		"admission_full",
	} {
		if !strings.Contains(pressure, want) {
			t.Fatalf("runtime pool pressure summary missing %q:\n%s", want, pressure)
		}
	}
	for _, unwanted := range []string{
		"channelv2",
		"channelv2-store-apply",
		"transportv2",
	} {
		if strings.Contains(pressure, unwanted) {
			t.Fatalf("runtime pool pressure summary should not expose legacy label %q:\n%s", unwanted, pressure)
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
		"transport/service_executor",
		"3/4",
		"channel/store_append",
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
		"channelv2/store_append",
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
		"000100\t127_0_0_1_5011\ttransport\tservice_executor\t3\t4\t2\t0.750",
		"000100\t127_0_0_1_5011\tchannel\tstore_append\t5\t64\t0\t0.078",
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
		"transport/service_executor",
		"channel/store_append",
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
		"channelv2/store_append",
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
		`SOURCE_IPS="${WK_BENCH_PRESENCE_SOURCE_IPS:-}"`,
		`SOURCE_PORT_MIN="${WK_BENCH_PRESENCE_SOURCE_PORT_MIN:-}"`,
		`SOURCE_PORT_MAX="${WK_BENCH_PRESENCE_SOURCE_PORT_MAX:-}"`,
		`--source-ips`,
		`--source-port-min`,
		`--source-port-max`,
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

func TestWukongIMThreeNodePresenceScriptWritesExplicitTCPSourcePoolFromCLIAndEnv(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		name string
		args []string
		env  []string
	}{
		{
			name: "cli",
			args: []string{"--source-ips", "127.0.0.1,192.0.2.1", "--source-port-min", "2000", "--source-port-max", "2004"},
		},
		{
			name: "env",
			env: []string{
				"WK_BENCH_PRESENCE_SOURCE_IPS=127.0.0.1,192.0.2.1",
				"WK_BENCH_PRESENCE_SOURCE_PORT_MIN=2000",
				"WK_BENCH_PRESENCE_SOURCE_PORT_MAX=2004",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			callsDir := t.TempDir()
			outDir := t.TempDir()
			writeFakePresenceWkbench(t, filepath.Join(binDir, "wkbench"), callsDir)
			writeFakePresenceCurl(t, filepath.Join(binDir, "curl"), callsDir)
			writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
			writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)

			args := []string{
				"scripts/bench-wukongim-three-nodes-presence.sh",
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
			}
			args = append(args, tt.args...)
			cmd := exec.Command("bash", args...)
			cmd.Dir = root
			cmd.Env = presenceTestEnv("PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"))
			cmd.Env = append(cmd.Env, tt.env...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("presence script failed: %v\n%s", err, output)
			}

			workersYAML := readFile(t, filepath.Join(outDir, "workers.yaml"))
			for _, want := range []string{
				"tcp_source:",
				"ipv4_addrs:",
				"- 127.0.0.1",
				"- 192.0.2.1",
				"port_min: 2000",
				"port_max: 2004",
			} {
				if !strings.Contains(workersYAML, want) {
					t.Fatalf("workers source pool missing %q:\n%s", want, workersYAML)
				}
			}

			envText := readFile(t, filepath.Join(outDir, "env.txt"))
			for _, want := range []string{
				"TCP_SOURCE_IPV4_ADDRS=127.0.0.1,192.0.2.1",
				"TCP_SOURCE_PORT_MIN=2000",
				"TCP_SOURCE_PORT_MAX=2004",
				"TCP_SOURCE_CAPACITY=10",
			} {
				if !strings.Contains(envText, want) {
					t.Fatalf("env source pool missing %q:\n%s", want, envText)
				}
			}

			summary := readFile(t, filepath.Join(outDir, "summary.md"))
			if !strings.Contains(summary, "- tcp_source_pool: ipv4_addrs=127.0.0.1,192.0.2.1 port_min=2000 port_max=2004 capacity=10") {
				t.Fatalf("summary source pool missing:\n%s", summary)
			}
		})
	}
}

func TestWukongIMThreeNodePresenceScriptOmitsTCPSourcePoolByDefault(t *testing.T) {
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
	cmd.Env = presenceTestEnv("PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("presence script failed: %v\n%s", err, output)
	}

	workersYAML := readFile(t, filepath.Join(outDir, "workers.yaml"))
	if strings.Contains(workersYAML, "tcp_source:") || strings.Contains(workersYAML, "127.0.0.2") {
		t.Fatalf("default workers config must omit invented source pool:\n%s", workersYAML)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(summary, "- tcp_source_pool: disabled") {
		t.Fatalf("default summary should record disabled source pool:\n%s", summary)
	}
}

func TestWukongIMThreeNodePresenceScriptRejectsInvalidTCPSourcePoolBeforeClusterStart(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "partial", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "2000"}, want: "must be configured together"},
		{name: "invalid ipv4", args: []string{"--source-ips", "127.0.0.999", "--source-port-min", "2000", "--source-port-max", "2009"}, want: "valid IPv4"},
		{name: "duplicate ipv4", args: []string{"--source-ips", "127.0.0.1,127.0.0.1", "--source-port-min", "2000", "--source-port-max", "2009"}, want: "unique IPv4"},
		{name: "unspecified ipv4", args: []string{"--source-ips", "0.0.0.0", "--source-port-min", "2000", "--source-port-max", "2009"}, want: "must not include unspecified"},
		{name: "low port", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "1023", "--source-port-max", "2009"}, want: "must be at least 1024"},
		{name: "reversed ports", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "2010", "--source-port-max", "2009"}, want: "must not exceed"},
		{name: "high port", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "2000", "--source-port-max", "65536"}, want: "must not exceed 65535"},
		{name: "leading zero port", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "02000", "--source-port-max", "2009"}, want: "canonical positive integer"},
		{name: "oversized min", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "18446744073709552640", "--source-port-max", "2000"}, want: "at most 5 decimal digits"},
		{name: "oversized max", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "2000", "--source-port-max", "18446744073709617151"}, want: "at most 5 decimal digits"},
		{name: "capacity", args: []string{"--source-ips", "127.0.0.1", "--source-port-min", "2000", "--source-port-max", "2008"}, want: "capacity 9 is smaller than users 10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "cluster-started")
			startScript := filepath.Join(t.TempDir(), "start.sh")
			script := "#!/usr/bin/env bash\nset -euo pipefail\ntouch " + marker + "\nexit 99\n"
			if err := os.WriteFile(startScript, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{
				"scripts/bench-wukongim-three-nodes-presence.sh",
				"--start-script", startScript,
				"--out-dir", t.TempDir(),
				"--users", "10",
			}
			args = append(args, tt.args...)
			cmd := exec.Command("bash", args...)
			cmd.Dir = root
			cmd.Env = presenceTestEnv()

			output, err := cmd.CombinedOutput()

			if err == nil {
				t.Fatalf("presence script should reject %s source pool:\n%s", tt.name, output)
			}
			if !strings.Contains(string(output), tt.want) {
				t.Fatalf("presence script error missing %q:\n%s", tt.want, output)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("cluster start script ran before source validation, stat error = %v", statErr)
			}
		})
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
	cmd.Env = presenceTestEnv(
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
	cmd.Env = presenceTestEnv(
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
	cmd.Env = presenceTestEnv(
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
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
	cmd.Env = presenceTestEnv(
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
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

	workersYAML := readFile(t, filepath.Join(outDir, "workers.yaml"))
	for _, want := range []string{
		"client:",
		"send_queue_capacity: 16",
		"max_inflight: 1",
		"read_buffer_size: 1024",
		"frame_buffer_size: 4",
	} {
		if !strings.Contains(workersYAML, want) {
			t.Fatalf("workers profile missing %q:\n%s", want, workersYAML)
		}
	}
	env := readFile(t, filepath.Join(outDir, "env.txt"))
	for _, want := range []string{
		"CLIENT_SEND_QUEUE_CAPACITY=16",
		"CLIENT_MAX_INFLIGHT=1",
		"CLIENT_READ_BUFFER_SIZE=1024",
		"CLIENT_FRAME_BUFFER_SIZE=4",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env profile missing %q:\n%s", want, env)
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
		"- client_profile: send_queue_capacity=16 max_inflight=1 read_buffer_size=1024 frame_buffer_size=4",
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
	cmd.Env = presenceTestEnv(
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
	cmd.Env = presenceTestEnv(
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
	cmd.Env = presenceTestEnv(
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
	cmd.Env = presenceTestEnv(
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
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
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

func TestChannelRuntimeMetricsSummaryAwkTestsUsePromotedEntrypointByDefault(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "wukongim_three_node_bench_script_test.go"))
	legacyEntrypoint := "channelv2" + "-metrics-summary.awk"
	if strings.Contains(source, legacyEntrypoint) {
		t.Fatalf("channel runtime summary tests should use scripts/channel-metrics-summary.awk by default")
	}
}

func TestChannelRuntimeMetricsSummaryAwkAcceptsPromotedPrometheusNamesAndDedupesLegacyAliases(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	writeFile(t, before, `wukongim_channel_active_runtimes{role="leader"} 1
wukongim_channel_active_runtimes{role="follower"} 1
wukongim_channelv2_active_runtimes{role="leader"} 1
wukongim_channel_activation_rejected_total{reason="max_channels"} 1
wukongim_channel_recovery_probe_total{result="submitted"} 1
wukongim_channel_pull_total{result="ok",empty="false"} 1
wukongim_channel_rpc_pull_total{result="ok"} 1
wukongim_channel_meta_cache_total{result="hit"} 1
wukongim_channel_append_duration_seconds_count{commit_mode="local"} 10
wukongim_channelv2_append_duration_seconds_count{commit_mode="local"} 10
wukongim_channel_append_duration_seconds_sum{commit_mode="local"} 0.010
wukongim_channel_append_batch_records_count 2
wukongim_channel_append_batch_records_sum 8
wukongim_channel_append_batch_bytes_sum 200
wukongim_channel_append_batch_wait_duration_seconds_sum 0.002
wukongim_channel_worker_task_duration_seconds_count{kind="store_append",result="ok"} 4
wukongim_channel_worker_task_duration_seconds_sum{kind="store_append",result="ok"} 0.020
`)
	writeFile(t, after, `wukongim_channel_active_runtimes{role="leader"} 2
wukongim_channel_active_runtimes{role="follower"} 3
wukongim_channelv2_active_runtimes{role="leader"} 2
wukongim_channel_follower_parked{reactor_id="0"} 1
wukongim_channel_reactor_mailbox_depth{reactor_id="0",priority="normal"} 4
wukongim_channel_worker_queue_depth{pool="store_append"} 6
wukongim_channel_activation_rejected_total{reason="max_channels"} 2
wukongim_channel_recovery_probe_total{result="submitted"} 3
wukongim_channel_recovery_probe_total{result="ok"} 1
wukongim_channel_recovery_probe_total{result="err"} 1
wukongim_channel_pull_total{result="ok",empty="false"} 4
wukongim_channel_pull_total{result="ok",empty="true"} 2
wukongim_channel_pull_total{result="err",empty="false"} 1
wukongim_channel_rpc_pull_total{result="ok"} 5
wukongim_channel_rpc_pull_total{result="err"} 1
wukongim_channel_meta_cache_total{result="hit"} 4
wukongim_channel_meta_cache_total{result="miss"} 2
wukongim_channel_meta_cache_total{result="invalidate"} 1
wukongim_channel_append_duration_seconds_count{commit_mode="local"} 15
wukongim_channelv2_append_duration_seconds_count{commit_mode="local"} 15
wukongim_channel_append_duration_seconds_sum{commit_mode="local"} 0.035
wukongim_channel_append_batch_records_count 4
wukongim_channel_append_batch_records_sum 20
wukongim_channel_append_batch_bytes_sum 600
wukongim_channel_append_batch_wait_duration_seconds_sum 0.008
wukongim_channel_worker_task_duration_seconds_count{kind="store_append",result="ok"} 9
wukongim_channel_worker_task_duration_seconds_sum{kind="store_append",result="ok"} 0.070
`)

	cmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("summary awk failed: %v\n%s", err, output)
	}

	fields := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(fields) < 39 {
		t.Fatalf("summary fields = %d, want at least 39: %q", len(fields), output)
	}
	for index, want := range map[int]string{
		2:  "5",
		3:  "2",
		4:  "3",
		5:  "1",
		6:  "4",
		7:  "6",
		18: "1",
		19: "2",
		22: "3",
		25: "4",
		27: "1.000",
		31: "5",
		32: "5.000",
		33: "2",
		34: "6.000",
		35: "200.000",
		36: "3.000",
		37: "5",
		38: "10.000",
	} {
		if fields[index] != want {
			t.Fatalf("field[%d] = %q, want %q; row=%q", index, fields[index], want, output)
		}
	}
}

func TestChannelRuntimeMetricsSummaryAwkPromotedEntrypoint(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	writeFile(t, before, "")
	writeFile(t, after, `wukongim_channel_active_runtimes{role="leader"} 1
`)

	cmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("summary awk failed: %v\n%s", err, output)
	}

	fields := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(fields) < 5 {
		t.Fatalf("summary fields = %d, want at least 5: %q", len(fields), output)
	}
	for index, want := range map[int]string{
		2: "1",
		3: "1",
		4: "0",
	} {
		if fields[index] != want {
			t.Fatalf("field[%d] = %q, want %q; row=%q", index, fields[index], want, output)
		}
	}
}

func TestChannelRuntimeMetricsSummaryAwkLegacyEntrypointMatchesPromotedEntrypoint(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	writeFile(t, before, "")
	writeFile(t, after, `wukongim_channel_active_runtimes{role="leader"} 1
wukongim_channel_worker_queue_depth{pool="store_append"} 3
`)

	promotedCmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", "channel-metrics-summary.awk"),
		before,
		after,
	)
	promotedOutput, err := promotedCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("promoted summary awk failed: %v\n%s", err, promotedOutput)
	}

	legacyEntrypoint := "channelv2" + "-metrics-summary.awk"
	legacyCmd := exec.Command("awk",
		"-v", "tag=qps_1000",
		"-v", "node=node1",
		"-v", "duration=5",
		"-f", filepath.Join(root, "scripts", legacyEntrypoint),
		before,
		after,
	)
	legacyOutput, err := legacyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy summary awk failed: %v\n%s", err, legacyOutput)
	}
	if string(legacyOutput) != string(promotedOutput) {
		t.Fatalf("legacy entrypoint output differs from promoted entrypoint:\npromoted=%q\nlegacy=%q", promotedOutput, legacyOutput)
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

func TestRuntimePoolPressureSummaryAwkPromotesLegacyRuntimePoolLabels(t *testing.T) {
	root := repoRoot(t)
	before := filepath.Join(t.TempDir(), "before.prom")
	after := filepath.Join(t.TempDir(), "after.prom")
	writeFile(t, before, `wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal",result="full"} 1
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="busy"} 1
`)
	writeFile(t, after, `wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal"} 9
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal"} 10
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 8
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 8
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal",result="full"} 3
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 2
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc"} 8
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="transportv2",pool="service",queue="service_9",priority="rpc",result="busy"} 4
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
		"000100\tnode1\tchannel\tstore_apply\tapply\tnormal",
		"000100\tnode1\ttransport\tservice\tservice_9\trpc",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("runtime pool pressure awk missing promoted label %q:\n%s", want, summary)
		}
	}
	for _, unwanted := range []string{
		"channelv2",
		"channelv2-store-apply",
		"transportv2",
	} {
		if strings.Contains(summary, unwanted) {
			t.Fatalf("runtime pool pressure awk should not expose legacy label %q:\n%s", unwanted, summary)
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
wukongim_ants_pool_running{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 6
wukongim_ants_pool_capacity{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 10
wukongim_ants_pool_waiting{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 1
wukongim_ants_pool_utilization{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 0.600
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
		"000100\tnode1\ttransport\tservice_executor\t3\t4\t2\t0.750",
		"000100\tnode1\tchannel\tstore_append\t5\t64\t0\t0.078",
		"000100\tnode1\tchannel\tstore_apply\t6\t10\t1\t0.600",
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
		"channelv2",
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
if [[ "${WK_FAKE_REAL_QPS_EMPTY_SUMMARY_QPS:-}" == "$qps" ]]; then
  cat >"$out_dir/summary.tsv" <<'OUT'
tag\toffered_qps\tstatus\texit_status\tactual_qps\tsend_success\tsend_errors\tconnect_error_rate\tsendack_error_rate\tp50_seconds\tp95_seconds\tp99_seconds\tmax_seconds
OUT
  exit 7
fi
p99="${WK_FAKE_REAL_QPS_P99_SECONDS:-0.003}"
max="${WK_FAKE_REAL_QPS_MAX_SECONDS:-0.004}"
cat >"$out_dir/summary.tsv" <<OUT
tag	offered_qps	status	exit_status	actual_qps	send_success	send_errors	connect_error_rate	sendack_error_rate	p50_seconds	p95_seconds	p99_seconds	max_seconds
000100	100	passed	0	99	99	0	0	0	0.001	0.002	$p99	$max
OUT
cat >"$out_dir/channel_metrics_summary.tsv" <<'OUT'
tag	node	runtime_pool_queue_depth_max	runtime_pool_queue_fill_max	runtime_pool_queue_bytes_max	runtime_pool_queue_bytes_fill_max	runtime_pool_inflight_max	runtime_pool_inflight_util_max	runtime_pool_admission_full_delta	runtime_pool_admission_busy_delta	runtime_pool_admission_dirty_delta	runtime_pool_admission_requeued_delta
000100	node1	7	0.700	200	0.500	8	0.750	3	2	1	5
000100	node2	4	0.400	300	0.250	12	0.600	2	1	0	1
OUT
cat >"$out_dir/channelv2_metrics_summary.tsv" <<'OUT'
tag	node	runtime_pool_queue_depth_max	runtime_pool_queue_fill_max	runtime_pool_queue_bytes_max	runtime_pool_queue_bytes_fill_max	runtime_pool_inflight_max	runtime_pool_inflight_util_max	runtime_pool_admission_full_delta	runtime_pool_admission_busy_delta	runtime_pool_admission_dirty_delta	runtime_pool_admission_requeued_delta
000100	node1	1	0.100	100	0.100	1	0.100	1	1	1	1
OUT
if [[ "${WK_FAKE_LEGACY_CHANNEL_SUMMARY_ONLY:-0}" == "1" ]]; then
  rm -f "$out_dir/channel_metrics_summary.tsv"
  cat >"$out_dir/channelv2_metrics_summary.tsv" <<'OUT'
tag	node	runtime_pool_queue_depth_max	runtime_pool_queue_fill_max	runtime_pool_queue_bytes_max	runtime_pool_queue_bytes_fill_max	runtime_pool_inflight_max	runtime_pool_inflight_util_max	runtime_pool_admission_full_delta	runtime_pool_admission_busy_delta	runtime_pool_admission_dirty_delta	runtime_pool_admission_requeued_delta
000100	node1	9	0.900	900	0.800	18	0.950	8	7	6	5
OUT
fi
cat >"$out_dir/runtime_pool_pressure_summary.tsv" <<'OUT'
tag	node	component	pool	queue	priority	queue_depth_max	queue_capacity	queue_fill_max	queue_bytes_max	queue_bytes_capacity	queue_bytes_fill_max	inflight_max	workers	inflight_util_max	admission_full_delta	admission_busy_delta	admission_dirty_delta	admission_requeued_delta	reason
000100	node1	gateway	async_send	send	none	7	10	0.700	200	400	0.500	8	16	0.500	3	2	1	5	queue_backlog,admission_full
OUT
cat >"$out_dir/ants_pool_usage_summary.tsv" <<'OUT'
tag	node	component	pool	running	capacity	waiting	utilization_max
000100	node1	transport	service_executor	3	4	2	0.750
000100	node1	channel	store_append	5	64	0	0.078
000100	node1	channelappend	advance	1	2	0	0.500
000100	node1	channelappend	effect	4	8	1	0.500
OUT
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runFakeThreeNode1000Bench(t *testing.T, root string, outDir string, extraEnv []string, extraArgs ...string) ([]byte, error) {
	t.Helper()
	binDir := t.TempDir()
	callsDir := t.TempDir()
	wkbenchPath := filepath.Join(binDir, "wkbench")
	writeFakeThreeNode1000Wkbench(t, wkbenchPath, callsDir, "fake")
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	gatewayAddr := listenLocalTCP(t)

	args := []string{
		"scripts/bench-wukongim-three-nodes-1000ch.sh",
		"--no-start",
		"--no-worker",
		"--out-dir", outDir,
		"--wkbench-bin", wkbenchPath,
		"--qps", "1",
		"--channels", "1",
		"--users", "2",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--resource-interval", "0",
		"--api", "http://127.0.0.1:5011",
		"--metrics", "http://127.0.0.1:5011",
		"--gateway", gatewayAddr,
	}
	args = append(args, extraArgs...)
	cmd := exec.Command("bash", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, extraEnv...)
	return cmd.CombinedOutput()
}

func yamlScalar(text string, key string) string {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == key {
			return fields[1]
		}
	}
	return ""
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
  run_id="$(awk '$1 == "id:" { print $2; exit }' "$scenario")"
	mkdir -p "$report_dir"
	if [[ "${WK_FAKE_WKBENCH_PHASED_RUN:-0}" == "1" ]]; then
		state_file="` + callsDir + `/wkbench.state"
		publish_state() {
			local tmp="${state_file}.tmp.$$.$RANDOM"
			printf '%s\t%s\n' "$run_id" "$1" > "$tmp"
			mv -f "$tmp" "$state_file"
		}
		profile_finished="` + callsDir + `/profile.${run_id}.finished"
		profile_end_checked="` + callsDir + `/profile.${run_id}.end_checked"
		rm -f "$profile_finished" "$profile_end_checked"
		publish_state prepare
		publish_state run
		for ((attempt = 0; attempt < 300; attempt++)); do
			[[ -f "$profile_end_checked" ]] && break
			sleep 0.01
		done
		if [[ ! -f "$profile_end_checked" ]]; then
			echo 'timed out waiting for profile end-status handshake' >&2
			exit 2
		fi
		publish_state done
	fi
  if [[ -n "${WK_FAKE_WKBENCH_RUN_SLEEP:-}" ]]; then
    sleep "$WK_FAKE_WKBENCH_RUN_SLEEP"
  fi
	  success="${WK_FAKE_WKBENCH_SUCCESS_TOTAL:-1}"
	  p99="${WK_FAKE_WKBENCH_P99_SECONDS:-0.003}"
	  max="${WK_FAKE_WKBENCH_MAX_SECONDS:-0.004}"
	  cat > "$report_dir/report.json" <<JSON
{"status":"passed","summary":{"connect_error_rate":0,"sendack_error_rate":0},"metrics":{"counters":{"group_send_success_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":$success,"group_send_error_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":0},"histograms":{"group_send_latency_seconds{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}":{"p50_seconds":0.001,"p95_seconds":0.002,"p99_seconds":$p99,"max_seconds":$max}}}}
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
state_file="` + callsDir + `/wkbench.state"
read_fake_state() {
	run_id="three-node-fixed-1ch-000001-qps"
	state="prepare"
	if [[ -f "$state_file" ]]; then
		local snapshot_run_id="" snapshot_phase=""
		IFS=$'\t' read -r snapshot_run_id snapshot_phase < "$state_file" || true
		if [[ -n "$snapshot_run_id" && -n "$snapshot_phase" ]]; then
			run_id="$snapshot_run_id"
			state="$snapshot_phase"
		fi
	fi
}
publish_fake_state() {
	local state_run_id="$1"
	local state_phase="$2"
	local tmp="${state_file}.tmp.$$.$RANDOM"
	printf '%s\t%s\n' "$state_run_id" "$state_phase" > "$tmp"
	mv -f "$tmp" "$state_file"
}
case "$url" in
	  http://127.0.0.1:501*/readyz|http://127.0.0.1:19130/healthz)
	    echo 'ok'
	    ;;
	  http://127.0.0.1:19130/v1/stop)
	    if [[ "$*" != *'"assignment_id":"fake-assignment"'* ]]; then
	      echo 'missing exact assignment_id' >&2
	      exit 22
	    fi
	    read_fake_state
	    printf '{"phase":"stopped","assignment":{"run_id":"%s","assignment_id":"fake-assignment","worker_id":"w1"}}\n' "$run_id"
	    ;;
		http://127.0.0.1:19130/v1/status)
		read_fake_state
		if [[ -n "${WK_FAKE_STATUS_DELAY_RUN_ID:-}" && "$run_id" == "$WK_FAKE_STATUS_DELAY_RUN_ID" && "$state" == "${WK_FAKE_STATUS_DELAY_PHASE:-}" ]]; then
			: "${WK_FAKE_STATUS_DELAY_READY_FILE:?missing delayed status ready file}"
			: "${WK_FAKE_STATUS_DELAY_RELEASE_FILE:?missing delayed status release file}"
			touch "$WK_FAKE_STATUS_DELAY_READY_FILE"
			for ((attempt = 0; attempt < 200; attempt++)); do
				[[ -f "$WK_FAKE_STATUS_DELAY_RELEASE_FILE" ]] && break
				sleep 0.01
			done
			[[ -f "$WK_FAKE_STATUS_DELAY_RELEASE_FILE" ]] || exit 28
		fi
			case "$state" in
				run)
					printf '{"phase":"warmup","active_phase":"run","completed_phase":"warmup","last_error":"","assignment":{"run_id":"%s","assignment_id":"fake-assignment","worker_id":"w1"}}\n' "$run_id"
					;;
				cooldown)
					printf '{"phase":"run","active_phase":"cooldown","completed_phase":"run","last_error":"","assignment":{"run_id":"%s","assignment_id":"fake-assignment","worker_id":"w1"}}\n' "$run_id"
					;;
				done)
					printf '{"phase":"run","completed_phase":"run","last_error":"","assignment":{"run_id":"%s","assignment_id":"fake-assignment","worker_id":"w1"}}\n' "$run_id"
					;;
				*)
					printf '{"phase":"prepare","active_phase":"prepare","last_error":"","assignment":{"run_id":"%s","assignment_id":"fake-assignment","worker_id":"w1"}}\n' "$run_id"
				;;
		esac
		if [[ -f "` + callsDir + `/profile.${run_id}.finished" ]]; then
			touch "` + callsDir + `/profile.${run_id}.end_checked"
		fi
		;;
	  http://127.0.0.1:501*/metrics)
	    if [[ "$*" == *"X-WK-Bench-Evidence: append-effect-"* ]]; then
	      if [[ "${WK_FAKE_APPEND_EFFECT_MISSING_NODE:-0}" == "1" && "$url" == "http://127.0.0.1:5012/metrics" ]]; then
	        exit 22
	      fi
	      append_items=0
	      if [[ "$*" == *"append-effect-after"* && "$url" == "http://127.0.0.1:5011/metrics" && "${WK_FAKE_APPEND_EFFECT_MISMATCH:-0}" != "1" ]]; then
	        append_items=1
	      fi
	      echo "wukongim_channelappend_effect_items_sum{result=\"ok\",stage=\"append\"} $append_items"
	      exit 0
	    fi
	    if [[ "${WK_FAKE_QUIESCENCE_BUSY:-0}" == "1" ]]; then
	      cat <<'OUT'
wukongim_delivery_recipient_worker_queue_depth 3
wukongim_channelappend_writer_state_items{kind="post_commit_backlog"} 2
wukongim_gateway_async_send_queue_depth 1
OUT
	    else
	      cat <<'OUT'
wukongim_delivery_recipient_worker_queue_depth 0
wukongim_channelappend_writer_state_items{kind="post_commit_backlog"} 0
wukongim_gateway_async_send_queue_depth 0
OUT
	    fi
	    echo "wukongim_delivery_recipient_worker_inflight ${WK_FAKE_QUIESCENCE_INFLIGHT:-0}"
	    echo "wukongim_delivery_recipient_worker_capacity ${WK_FAKE_DELIVERY_WORKER_CAPACITY:-100}"
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
wukongim_runtime_pool_queue_depth{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal"} 9
wukongim_runtime_pool_queue_capacity{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal"} 10
wukongim_runtime_pool_inflight{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 8
wukongim_runtime_pool_workers{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply"} 8
wukongim_runtime_pool_admission_total{node_id="1",node_name="node-1",component="channelv2",pool="channelv2-store-apply",queue="apply",priority="normal",result="full"} $count
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
	http://127.0.0.1:501*/debug/pprof/profile?seconds=*)
		read_fake_state
		if [[ "$*" == *"X-WK-Bench-Evidence: pprof-run"* ]]; then
			if [[ "$state" != "run" ]]; then
				if [[ -n "${WK_FAKE_PROFILE_VIOLATION_FILE:-}" ]]; then
					touch "$WK_FAKE_PROFILE_VIOLATION_FILE"
				fi
				exit 22
			fi
			if [[ "${WK_FAKE_PROFILE_TRANSITION_TO_COOLDOWN:-0}" == "1" ]]; then
				publish_fake_state "$run_id" cooldown
			fi
		fi
		echo 'cpu profile'
		touch "` + callsDir + `/profile.${run_id}.finished"
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
channel_pending_meta_current_max: 1
channel_pending_meta_released_count: 0
channel_need_meta_pull_submitted_count: 3
channel_need_meta_pull_ok_count: 2
channel_need_meta_pull_retry_count: 1
channel_need_meta_pull_err_count: 0
channel_pull_hint_err_count: 1
channel_pull_hint_receive_err_count: 0
OUT
    exit 0
  fi
  cat <<'OUT'
classification: fake
channel_pending_meta_current_max: 0
channel_pending_meta_released_count: 0
channel_need_meta_pull_submitted_count: 3
channel_need_meta_pull_ok_count: 3
channel_need_meta_pull_retry_count: 0
channel_need_meta_pull_err_count: 0
channel_pull_hint_err_count: 0
channel_pull_hint_receive_err_count: 0
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
channel_pending_meta_current_max: 0
channel_pending_meta_released_count: 0
channel_need_meta_pull_submitted_count: 3
channel_need_meta_pull_ok_count: 3
channel_need_meta_pull_retry_count: 0
channel_need_meta_pull_err_count: 0
channel_pull_hint_err_count: 0
channel_pull_hint_receive_err_count: 0
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
  http://127.0.0.1:501*/debug/config)
    case "$url" in
      http://127.0.0.1:5011/*) echo '{"node_id":1}' ;;
      http://127.0.0.1:5012/*) echo '{"node_id":2}' ;;
      http://127.0.0.1:5013/*) echo '{"node_id":3}' ;;
      *) echo '{"node_id":0}' ;;
    esac
    ;;
  http://127.0.0.1:501*/debug/cluster)
    echo '{"initial_slot_count":10,"hash_slot_count":256}'
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
  *wukongim-node1.toml*) echo 111 ;;
  *wukongim-node2.toml*) echo 222 ;;
  *wukongim-node3.toml*) echo 333 ;;
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

func presenceTestEnv(extra ...string) []string {
	env := envWithout(
		"WK_BENCH_PRESENCE_SOURCE_IPS",
		"WK_BENCH_PRESENCE_SOURCE_PORT_MIN",
		"WK_BENCH_PRESENCE_SOURCE_PORT_MAX",
	)
	return append(env, extra...)
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
