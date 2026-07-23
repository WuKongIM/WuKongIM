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

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim.sh",
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
		"go run ./cmd/wukongim",
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

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
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
		"aggregate_send_rate=18/s",
		"channel_reactor_count=32",
		"channel_store_append_workers=500",
		"channel_store_apply_workers=500",
		"channel_rpc_workers=500",
		"gateway_send_timeout=14s",
		"sim_concurrency=64",
		"sim_ack_timeout=15s",
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
		"start_cmd=env WK_DEBUG_API_ENABLE=true WK_CLUSTER_CHANNEL_REACTOR_COUNT=32 WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 WK_CLUSTER_CHANNEL_RPC_WORKERS=500 WK_GATEWAY_SEND_TIMEOUT=14s " + startScript + " --clean --ready-timeout 7 --bin " + filepath.Join(outDir, "wukongim") + " --log-dir " + filepath.Join(outDir, "node-logs"),
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013 --gateway 127.0.0.1:5111 --gateway 127.0.0.1:5112 --gateway 127.0.0.1:5113 --users 12 --groups 3 --group-members 4 --rate 6/s --max-runtime 4s --payload-size 128B --status-listen 127.0.0.1:19109 --status-interval 1s --concurrency 64 --ack-timeout 15s --json",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptDryRunPrintsFaultKillPlan(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	startScript := filepath.Join(t.TempDir(), "start-three.sh")

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--fault-kill-node",
		"--fault-node-id", "2",
		"--fault-after", "3",
		"--fault-signal", "KILL",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"fault_kill_node=true",
		"fault_node_id=2",
		"fault_after_secs=3",
		"fault_signal=KILL",
		"fault_pid_dir=" + filepath.Join(outDir, "node-pids"),
		"fault_event_file=" + filepath.Join(outDir, "fault-node2-kill.env"),
		"fault_cmd=kill -s KILL $(cat " + filepath.Join(outDir, "node-pids", "node2.pid") + ")",
		"start_cmd=env WK_DEBUG_API_ENABLE=true WK_CLUSTER_CHANNEL_REACTOR_COUNT=32 WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 WK_CLUSTER_CHANNEL_RPC_WORKERS=500 WK_GATEWAY_SEND_TIMEOUT=14s " + startScript + " --clean --ready-timeout 90 --bin " + filepath.Join(outDir, "wukongim") + " --log-dir " + filepath.Join(outDir, "node-logs") + " --pid-dir " + filepath.Join(outDir, "node-pids") + " --allow-node-exit 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptDryRunPrintsFaultDrillTuning(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	startScript := filepath.Join(t.TempDir(), "start-three.sh")

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--fault-kill-node",
		"--fault-node-id", "2",
		"--fault-health-report-interval", "1s",
		"--fault-health-report-ttl", "6s",
		"--fault-channel-migration-scan-interval", "500ms",
		"--fault-channel-migration-scan-limit", "128",
		"--fault-channel-migration-max-pages-per-tick", "10",
		"--fault-channel-migration-max-tasks-per-tick", "10",
		"--fault-channel-migration-task-limit", "10",
		"--fault-gateway-send-timeout", "15s",
		"--fault-sim-ack-timeout", "35s",
		"--fault-max-send-errors", "6",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"fault_health_report_interval=1s",
		"fault_health_report_ttl=6s",
		"fault_channel_migration_scan_interval=500ms",
		"fault_channel_migration_scan_limit=128",
		"fault_channel_migration_max_pages_per_tick=10",
		"fault_channel_migration_max_tasks_per_tick=10",
		"fault_channel_migration_task_limit=10",
		"fault_gateway_send_timeout=15s",
		"fault_sim_ack_timeout=35s",
		"fault_max_send_errors=6",
		"start_cmd=env WK_DEBUG_API_ENABLE=true WK_CLUSTER_CHANNEL_REACTOR_COUNT=32 WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 WK_CLUSTER_CHANNEL_RPC_WORKERS=500 WK_GATEWAY_SEND_TIMEOUT=15s WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=1s WK_CLUSTER_NODE_HEALTH_REPORT_TTL=6s WK_CHANNEL_MIGRATION_SCAN_INTERVAL=500ms WK_CHANNEL_MIGRATION_SCAN_LIMIT=128 WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK=10 WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK=10 WK_CHANNEL_MIGRATION_TASK_LIMIT=10 " + startScript,
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013 --gateway 127.0.0.1:5111 --gateway 127.0.0.1:5112 --gateway 127.0.0.1:5113 --users 30 --groups 6 --group-members 10 --rate 10/s --max-runtime 10s --payload-size 128B --status-listen 127.0.0.1:19109 --status-interval 1s --concurrency 64 --ack-timeout 35s --json",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptDryRunAllowsGatewaySubsetForFaultDrill(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	startScript := filepath.Join(t.TempDir(), "start-three.sh")

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--fault-kill-node",
		"--fault-node-id", "2",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"api_addrs=http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"gateway_addrs=127.0.0.1:5111",
		"fault_kill_node=true",
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013 --gateway 127.0.0.1:5111",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptDryRunPrintsAutoJoinNodePlan(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	startScript := filepath.Join(t.TempDir(), "start-three.sh")

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--auto-join-node",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"auto_join_node=true",
		"auto_join_after_secs=2",
		"auto_join_node_id=4",
		"auto_join_api=http://127.0.0.1:5014",
		"auto_join_gateway=127.0.0.1:5114",
		"auto_join_cluster=127.0.0.1:7014",
		"auto_join_seeds=127.0.0.1:7011,127.0.0.1:7012,127.0.0.1:7013",
		"auto_join_config=" + filepath.Join(outDir, "wukongim-node4.toml"),
		"auto_join_data_dir=" + filepath.Join(outDir, "node4-data"),
		"auto_join_log=" + filepath.Join(outDir, "node-logs", "node4.log"),
		"auto_join_cmd=env WK_DEBUG_API_ENABLE=true " + filepath.Join(outDir, "wukongim") + " -config " + filepath.Join(outDir, "wukongim-node4.toml"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptDryRunPrintsAutoPromotePlan(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	startScript := filepath.Join(t.TempDir(), "start-three.sh")

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--auto-join-node",
		"--auto-promote-controller-voter",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"auto_promote_controller_voter=true",
		"auto_promote_node_id=4",
		"auto_promote_manager_api=http://127.0.0.1:5311",
		"auto_promote_manager_auth=true",
		"auto_promote_login_response=" + filepath.Join(outDir, "manager-login.json"),
		"auto_promote_activate_response=" + filepath.Join(outDir, "node4-activate.json"),
		"auto_promote_nodes_response=" + filepath.Join(outDir, "nodes-after-node4-activate.json"),
		"auto_promote_response=" + filepath.Join(outDir, "controller-voter-promotion-node4.json"),
		"auto_promote_controller_raft_status=" + filepath.Join(outDir, "controller-raft-node4.json"),
		"auto_promote_login_cmd=curl -fsS",
		"http://127.0.0.1:5311/manager/login",
		"auto_promote_activate_cmd=curl -fsS -H Authorization: Bearer <token> -X POST http://127.0.0.1:5311/manager/nodes/4/activate",
		"auto_promote_cmd=curl -fsS -H Authorization: Bearer <token> -X POST http://127.0.0.1:5311/manager/nodes/4/controller-voter/promote",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptStartsAutoJoinNodeDuringSimulation(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	staleDataDir := filepath.Join(outDir, "node4-data")
	staleFile := filepath.Join(staleDataDir, "stale")
	startScript := filepath.Join(binDir, "start-three.sh")
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeThreeNodeSimStartScript(t, startScript, callsDir)
	writeFakeThreeNodeDynamicBinary(t, filepath.Join(outDir, "wukongim"), callsDir)
	if err := os.MkdirAll(staleDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleFile, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		terminateRecordedProcess(t, filepath.Join(callsDir, "start.pid"))
		terminateRecordedProcess(t, filepath.Join(callsDir, "dynamic.pid"))
	})

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--auto-join-node",
		"--auto-join-after", "0",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		"--ready-timeout", "5",
		"--poll", "1",
	)
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE=true",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "auto join node4 ready") {
		t.Fatalf("script output missing auto join readiness:\n%s", output)
	}
	dynamicCalls := readFile(t, filepath.Join(callsDir, "dynamic.calls"))
	if !strings.Contains(dynamicCalls, "-config "+filepath.Join(outDir, "wukongim-node4.toml")) {
		t.Fatalf("dynamic node command missing config path:\n%s", dynamicCalls)
	}
	dynamicDebugAPI := strings.TrimSpace(readFile(t, filepath.Join(callsDir, "dynamic.debug_api")))
	if dynamicDebugAPI != "true" {
		t.Fatalf("dynamic node WK_DEBUG_API_ENABLE=%q, want true", dynamicDebugAPI)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(callsDir, "start.control_env"))); got != "<unset>" {
		t.Fatalf("start script inherited smoke-only environment: %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(callsDir, "dynamic.control_env"))); got != "<unset>" {
		t.Fatalf("dynamic node inherited smoke-only environment: %q", got)
	}
	if !waitForFile(filepath.Join(callsDir, "dynamic.term")) {
		t.Fatalf("dynamic node did not receive TERM; calls dir: %s", callsDir)
	}
	if _, err := os.Stat(staleFile); err == nil {
		t.Fatalf("stale auto-join data file was not removed: %s", staleFile)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat stale auto-join data file: %v", err)
	}
	config := readFile(t, filepath.Join(outDir, "wukongim-node4.toml"))
	for _, want := range []string{
		"[node]",
		"id = 4",
		`data_dir = "` + staleDataDir + `"`,
		"[cluster]",
		`id = "wukongim-dev-three"`,
		`seeds = ["127.0.0.1:7011","127.0.0.1:7012","127.0.0.1:7013"]`,
		`join_token = "change-me"`,
		`advertise_addr = "127.0.0.1:7014"`,
		`listeners = [{ name = "tcp-wkproto", network = "tcp", address = "127.0.0.1:5114", transport = "gnet", protocol = "wkproto" }]`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("node4 config missing %q:\n%s", want, config)
		}
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- auto_join_node: true",
		"- auto_join_after_secs: 0",
		"- auto_join_api: http://127.0.0.1:5014",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	for _, path := range []string{
		filepath.Join(outDir, "bench-capabilities", "node4.json"),
		filepath.Join(outDir, "bench-capacity-targets", "node4.json"),
		filepath.Join(outDir, "bench-snapshots", "node4.json"),
		filepath.Join(outDir, "metrics", "node4-after.prom"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected auto join evidence file %s: %v", path, err)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptPromotesAutoJoinNodeDuringSimulation(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	startScript := filepath.Join(binDir, "start-three.sh")
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeThreeNodeSimStartScript(t, startScript, callsDir)
	writeFakeThreeNodeDynamicBinary(t, filepath.Join(outDir, "wukongim"), callsDir)
	t.Cleanup(func() {
		terminateRecordedProcess(t, filepath.Join(callsDir, "start.pid"))
		terminateRecordedProcess(t, filepath.Join(callsDir, "dynamic.pid"))
	})

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--auto-join-node",
		"--auto-promote-controller-voter",
		"--auto-join-after", "0",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		"--ready-timeout", "2",
		"--poll", "1",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"auto-promote controller voter node4 promotable",
		"auto-promote controller voter node4 accepted",
		"auto-promote controller raft node4 voters include node4",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("script output missing %q:\n%s", want, text)
		}
	}
	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	for _, want := range []string{
		"-d {\"username\":\"admin\",\"password\":\"a1234567\"} http://127.0.0.1:5311/manager/login",
		"-H Authorization: Bearer test-token -X POST http://127.0.0.1:5311/manager/nodes/4/activate",
		"-H Authorization: Bearer test-token http://127.0.0.1:5311/manager/nodes",
		"-H Authorization: Bearer test-token -X POST http://127.0.0.1:5311/manager/nodes/4/controller-voter/promote",
		"-H Authorization: Bearer test-token http://127.0.0.1:5311/manager/nodes/4/controller-raft",
	} {
		if !strings.Contains(curlCalls, want) {
			t.Fatalf("curl calls missing %q:\n%s", want, curlCalls)
		}
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- auto_promote_controller_voter: true",
		"- auto_promote_node_id: 4",
		"- auto_promote_manager_api: http://127.0.0.1:5311",
		"- auto_promote_manager_auth: true",
		"- auto_promote_login_response: manager-login.json",
		"- auto_promote_activate_response: node4-activate.json",
		"- auto_promote_response: controller-voter-promotion-node4.json",
		"- auto_promote_controller_raft_status: controller-raft-node4.json",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	for _, path := range []string{
		filepath.Join(outDir, "manager-login.json"),
		filepath.Join(outDir, "node4-activate.json"),
		filepath.Join(outDir, "nodes-after-node4-activate.json"),
		filepath.Join(outDir, "controller-voter-promotion-node4.json"),
		filepath.Join(outDir, "controller-raft-node4.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected auto-promote evidence file %s: %v", path, err)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptKillsConfiguredNodeDuringSimulation(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	startScript := filepath.Join(binDir, "start-three.sh")
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeThreeNodeSimStartScript(t, startScript, callsDir)
	t.Cleanup(func() {
		terminateRecordedProcess(t, filepath.Join(callsDir, "start.pid"))
		for _, name := range []string{"node1.pid", "node2.pid", "node3.pid"} {
			terminateRecordedProcess(t, filepath.Join(callsDir, name))
		}
	})

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--fault-kill-node",
		"--fault-node-id", "2",
		"--fault-after", "0",
		"--fault-signal", "TERM",
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
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Keep the fake simulator running until the node trap proves the
		// scheduled fault happened. A zero-delay timer alone can lose the race
		// against an instant fake process on fast Linux runners.
		"WK_FAKE_THREE_NODE_SIM_WAIT_FOR_FAULT=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"fault kill node2",
		"fault survivor node1 ready",
		"fault survivor node3 ready",
		"smoke passed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("script output missing %q:\n%s", want, text)
		}
	}
	if !waitForFile(filepath.Join(callsDir, "node2.term")) {
		t.Fatalf("node2 did not receive TERM; calls dir: %s", callsDir)
	}
	startCalls := readFile(t, filepath.Join(callsDir, "start.calls"))
	for _, want := range []string{
		"--pid-dir " + filepath.Join(outDir, "node-pids"),
		"--allow-node-exit 2",
	} {
		if !strings.Contains(startCalls, want) {
			t.Fatalf("start call missing %q:\n%s", want, startCalls)
		}
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- fault_kill_node: true",
		"- fault_node_id: 2",
		"- fault_after_secs: 0",
		"- fault_signal: TERM",
		"- fault_event_file: fault-node2-kill.env",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	event := readFile(t, filepath.Join(outDir, "fault-node2-kill.env"))
	for _, want := range []string{
		"node_id=2",
		"signal=TERM",
		"pid=",
	} {
		if !strings.Contains(event, want) {
			t.Fatalf("fault event missing %q:\n%s", want, event)
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

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
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

func TestWkcliSimThreeNodeSmokeScriptWaitsForSimOutputBeforeVerification(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeDelayedTee(t, filepath.Join(binDir, "tee"))

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
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
		t.Fatalf("script should wait for sim output before verification: %v\n%s", err, output)
	}
	if !strings.Contains(readFile(t, filepath.Join(outDir, "sim.jsonl")), `"state":"stopped"`) {
		t.Fatalf("sim output missing final stopped snapshot:\n%s", readFile(t, filepath.Join(outDir, "sim.jsonl")))
	}
}

func TestWkcliSimThreeNodeSmokeScriptFailsOnConversationActiveMetricGate(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
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

func TestWkcliSimThreeNodeSmokeScriptCapturesFailureMetricsWhenSimReportsSendErrors(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
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
		"WK_FAKE_THREE_NODE_SIM_SEND_ERRORS=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when wkcli sim reports send errors:\n%s", output)
	}
	text := string(output)
	for _, want := range []string{
		"send_errors=1, want 0",
		"failure metrics after-failure:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure output missing %q:\n%s", want, text)
		}
	}
	for _, path := range []string{
		filepath.Join(outDir, "metrics", "node1-after-failure.prom"),
		filepath.Join(outDir, "metrics", "node2-after-failure.prom"),
		filepath.Join(outDir, "metrics", "node3-after-failure.prom"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected failure metrics file %s: %v", path, err)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptAllowsConfiguredFaultSendErrors(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	writeFakeThreeNodeSimGo(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeThreeNodeSimCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--no-start",
		"--out-dir", outDir,
		"--fault-max-send-errors", "1",
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
		"WK_FAKE_THREE_NODE_SIM_SEND_ERRORS=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script should allow configured fault send errors: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "wkcli sim passed: messages_sent=9 send_errors=1") {
		t.Fatalf("script output missing bounded send-error success:\n%s", output)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- send_errors: 1",
		"- fault_max_send_errors: 1",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptStopsClusterAndPrintsEvidenceWhenSimFails(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
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

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
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
		"failure metrics after-failure:",
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
	for _, path := range []string{
		filepath.Join(outDir, "metrics", "node1-after-failure.prom"),
		filepath.Join(outDir, "metrics", "node2-after-failure.prom"),
		filepath.Join(outDir, "metrics", "node3-after-failure.prom"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected failure metrics file %s: %v", path, err)
		}
	}
}

func TestWkcliSimThreeNodeSmokeScriptReportsSimFailureBeforeDelayedAutoJoin(t *testing.T) {
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

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--out-dir", outDir,
		"--start-script", startScript,
		"--auto-join-node",
		"--auto-join-after", "2",
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113",
		"--users", "12",
		"--groups", "3",
		"--members", "4",
		"--rate", "6/s",
		"--duration", "4s",
		// The fake start process publishes readiness asynchronously; keep this
		// tolerant of repository-wide parallel test scheduling while the
		// delayed auto-join assertion remains two seconds.
		"--ready-timeout", "10",
		"--poll", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when wkcli sim fails before auto-join starts:\n%s", output)
	}
	text := string(output)
	if !strings.Contains(text, "wkcli sim failed with status 3") {
		t.Fatalf("failure output should keep wkcli sim failure as the primary cause:\n%s", text)
	}
	if strings.Contains(text, "auto-join node4 did not start before wkcli sim completed") {
		t.Fatalf("failure output should not mask the sim failure with delayed auto-join:\n%s", text)
	}
	if !waitForFile(filepath.Join(callsDir, "start.term")) {
		t.Fatalf("start script did not receive TERM; calls dir: %s", callsDir)
	}
}

func writeFakeThreeNodeSimGo(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/go.calls"
if [[ "${1:-}" == "run" && "${2:-}" == "./cmd/wkcli" && "${3:-}" == "sim" ]]; then
  send_errors=0
  last_error=""
  if [[ "${WK_FAKE_THREE_NODE_SIM_SEND_ERRORS:-}" == "1" ]]; then
    send_errors=1
    last_error="fake send error"
  fi
  cat <<'JSON'
{"state":"running","run_id":"test-run","target_servers":["http://127.0.0.1:5011","http://127.0.0.1:5012","http://127.0.0.1:5013"],"gateway_tcp_addrs":["127.0.0.1:5111","127.0.0.1:5112","127.0.0.1:5113"],"users":12,"active_users":12,"groups":3,"group_members":4,"messages_sent":3,"send_errors":0,"recv_messages":0,"recv_dropped":0,"reconnects":0,"last_error":"","last_transition_at":"2026-06-17T00:00:00Z"}
JSON
	if [[ "${WK_FAKE_THREE_NODE_SIM_WAIT_FOR_FAULT:-}" == "1" ]]; then
	  attempts=0
	  while [[ ! -f "` + callsDir + `/node2.term" && "$attempts" -lt 200 ]]; do
	    sleep 0.01
	    attempts=$((attempts + 1))
	  done
	  [[ -f "` + callsDir + `/node2.term" ]] || {
	    printf '%s\n' 'fake simulator did not observe node2 fault' >&2
	    exit 98
	  }
	fi
  printf '{"state":"stopped","run_id":"test-run","target_servers":["http://127.0.0.1:5011","http://127.0.0.1:5012","http://127.0.0.1:5013"],"gateway_tcp_addrs":["127.0.0.1:5111","127.0.0.1:5112","127.0.0.1:5113"],"users":12,"active_users":12,"groups":3,"group_members":4,"messages_sent":9,"send_errors":%s,"recv_messages":0,"recv_dropped":0,"reconnects":0,"last_error":"%s","last_transition_at":"2026-06-17T00:00:04Z"}\n' "$send_errors" "$last_error"
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

func writeFakeThreeNodeDynamicBinary(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$$" > "` + callsDir + `/dynamic.pid"
printf '%s\n' "${WK_DEBUG_API_ENABLE-}" > "` + callsDir + `/dynamic.debug_api"
printf '%s\n' "${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE-<unset>}" > "` + callsDir + `/dynamic.control_env"
printf '%s\n' "$*" >> "` + callsDir + `/dynamic.calls"
trap 'printf term > "` + callsDir + `/dynamic.term"; exit 0' TERM INT
echo fake dynamic node running
while true; do
  sleep 1
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeDelayedTee(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
out="${1:?output path required}"
tmp="${out}.tmp"
cat > "$tmp"
sleep 0.5
cat "$tmp"
mv "$tmp" "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeThreeNodeSimStartScript(t *testing.T, path string, callsDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(callsDir, "start.expected"), []byte("expected"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$$" > "` + callsDir + `/start.pid"
printf '%s\n' "${WK_DEBUG_API_ENABLE-}" > "` + callsDir + `/start.debug_api"
printf '%s\n' "${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE-<unset>}" > "` + callsDir + `/start.control_env"
printf '%s\n' "$*" >> "` + callsDir + `/start.calls"
log_dir=""
bin_path=""
pid_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --log-dir)
      log_dir="$2"
      shift 2
      ;;
    --bin)
      bin_path="$2"
      shift 2
      ;;
    --pid-dir)
      pid_dir="$2"
      shift 2
      ;;
    --ready-timeout|--poll|--allow-node-exit)
      shift 2
      ;;
    --clean|--no-build)
      shift
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
node_pids=()
if [[ -n "$pid_dir" ]]; then
  mkdir -p "$pid_dir"
  for node in 1 2 3; do
    (
      case "$node" in
        1) trap 'printf term > "` + callsDir + `/node1.term"; exit 0' TERM INT ;;
        2) trap 'printf term > "` + callsDir + `/node2.term"; exit 0' TERM INT ;;
        3) trap 'printf term > "` + callsDir + `/node3.term"; exit 0' TERM INT ;;
      esac
      while true; do
        sleep 1
      done
    ) &
    pid="$!"
    node_pids+=("$pid")
    printf '%s\n' "$pid" > "$pid_dir/node${node}.pid"
    printf '%s\n' "$pid" > "` + callsDir + `/node${node}.pid"
  done
fi
if [[ -n "$bin_path" ]]; then
  mkdir -p "$(dirname "$bin_path")"
  bin_tmp="${bin_path}.tmp.$$"
  cat > "$bin_tmp" <<'BIN'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$$" > "` + callsDir + `/dynamic.pid"
printf '%s\n' "${WK_DEBUG_API_ENABLE-}" > "` + callsDir + `/dynamic.debug_api"
printf '%s\n' "${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE-<unset>}" > "` + callsDir + `/dynamic.control_env"
printf '%s\n' "$*" >> "` + callsDir + `/dynamic.calls"
trap 'printf term > "` + callsDir + `/dynamic.term"; exit 0' TERM INT
echo fake dynamic node running
while true; do
  sleep 1
done
BIN
  chmod 0755 "$bin_tmp"
  mv -f "$bin_tmp" "$bin_path"
fi
trap 'printf term > "` + callsDir + `/start.term"; for pid in "${node_pids[@]}"; do kill "$pid" 2>/dev/null || true; done; exit 0' TERM INT
printf ready > "` + callsDir + `/start.ready"
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
  http://127.0.0.1:5011/readyz|http://127.0.0.1:5012/readyz|http://127.0.0.1:5013/readyz)
    if [[ -f "` + callsDir + `/start.expected" && ! -f "` + callsDir + `/start.ready" ]]; then
      exit 1
    fi
    echo ok
    ;;
  http://127.0.0.1:5014/readyz)
    if [[ -f "` + callsDir + `/dynamic.calls" ]]; then
      echo ok
      exit 0
    fi
    exit 1
    ;;
  http://127.0.0.1:5311/manager/login)
    echo '{"username":"admin","token_type":"Bearer","access_token":"test-token","expires_in":86400,"expires_at":"2026-07-02T00:00:00Z","permissions":[{"resource":"*","actions":["*"]}]}'
    ;;
  http://127.0.0.1:5311/manager/nodes/4/activate)
    echo '{"changed":true,"node_id":4,"join_state":"active"}'
    ;;
  http://127.0.0.1:5311/manager/nodes)
    echo '{"nodes":[{"node_id":1,"join_state":"active","controller_voter":true},{"node_id":2,"join_state":"active","controller_voter":true},{"node_id":3,"join_state":"active","controller_voter":true},{"node_id":4,"join_state":"active","controller_voter":false,"can_promote_controller_voter":true}]}'
    ;;
  http://127.0.0.1:5311/manager/nodes/4/controller-voter/promote)
    echo '{"changed":true,"node_id":4,"state_revision":12,"previous_voters":[1,2,3],"next_voters":[1,2,3,4]}'
    ;;
  http://127.0.0.1:5311/manager/nodes/4/controller-raft)
    echo '{"node_id":4,"role":"follower","leader_id":1,"voters":[1,2,3,4],"learners":[],"applied_index":42}'
    ;;
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
  http://127.0.0.1:5014/bench/v1/capacity-target)
    echo '{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:5114"}}'
    ;;
  http://127.0.0.1:5011/bench/v1/snapshot)
    echo '{"version":"bench/v1","counts":{"accepted_channels":3,"accepted_subscriber_items":3,"accepted_subscribers":12}}'
    ;;
  http://127.0.0.1:5012/bench/v1/snapshot|http://127.0.0.1:5013/bench/v1/snapshot|http://127.0.0.1:5014/bench/v1/snapshot)
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
