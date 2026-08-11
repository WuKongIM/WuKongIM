package scripts_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		"--connect-rate", "200",
		"--concurrency", "512",
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
		"gateway_async_send_workers=4096",
		"delivery_recipient_workers=800",
		"gateway_send_timeout=14s",
		"sim_connect_rate=200",
		"sim_concurrency=512",
		"sim_ack_timeout=15s",
		"cluster_log=" + filepath.Join(outDir, "cluster.log"),
		"node_log_dir=" + filepath.Join(outDir, "node-logs"),
		"sim_output=" + filepath.Join(outDir, "sim.jsonl"),
		"snapshot_output_dir=" + filepath.Join(outDir, "bench-snapshots"),
		"metrics_output_dir=" + filepath.Join(outDir, "metrics"),
		"max_conversation_directory_error_total=0",
		"max_goroutines=2000",
		"max_heap_alloc_bytes=4294967296",
		"start_cmd=env WK_DEBUG_API_ENABLE=true WK_CLUSTER_CHANNEL_REACTOR_COUNT=32 WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 WK_CLUSTER_CHANNEL_RPC_WORKERS=500 WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=4096 WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY=800 WK_GATEWAY_SEND_TIMEOUT=14s " + startScript + " --clean --ready-timeout 7 --bin " + filepath.Join(outDir, "wukongim") + " --log-dir " + filepath.Join(outDir, "node-logs"),
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013 --gateway 127.0.0.1:5111 --gateway 127.0.0.1:5112 --gateway 127.0.0.1:5113 --users 12 --groups 3 --group-members 4 --rate 6/s --max-runtime 4s --payload-size 128B --status-listen 127.0.0.1:19109 --status-interval 1s --connect-rate 200 --concurrency 512 --ack-timeout 15s --json",
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
		"start_cmd=env WK_DEBUG_API_ENABLE=true WK_CLUSTER_CHANNEL_REACTOR_COUNT=32 WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 WK_CLUSTER_CHANNEL_RPC_WORKERS=500 WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=4096 WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY=800 WK_GATEWAY_SEND_TIMEOUT=14s " + startScript + " --clean --ready-timeout 90 --bin " + filepath.Join(outDir, "wukongim") + " --log-dir " + filepath.Join(outDir, "node-logs") + " --pid-dir " + filepath.Join(outDir, "node-pids") + " --allow-node-exit 2",
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
		"start_cmd=env WK_DEBUG_API_ENABLE=true WK_CLUSTER_CHANNEL_REACTOR_COUNT=32 WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 WK_CLUSTER_CHANNEL_RPC_WORKERS=500 WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=4096 WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY=800 WK_GATEWAY_SEND_TIMEOUT=15s WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=1s WK_CLUSTER_NODE_HEALTH_REPORT_TTL=6s WK_CHANNEL_MIGRATION_SCAN_INTERVAL=500ms WK_CHANNEL_MIGRATION_SCAN_LIMIT=128 WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK=10 WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK=10 WK_CHANNEL_MIGRATION_TASK_LIMIT=10 " + startScript,
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013 --gateway 127.0.0.1:5111 --gateway 127.0.0.1:5112 --gateway 127.0.0.1:5113 --users 30 --groups 6 --group-members 10 --rate 10/s --max-runtime 10s --payload-size 128B --status-listen 127.0.0.1:19109 --status-interval 1s --connect-rate 500 --concurrency 64 --ack-timeout 35s --json",
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
