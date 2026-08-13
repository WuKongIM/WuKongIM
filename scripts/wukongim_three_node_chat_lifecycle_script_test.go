package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleShakeoutScriptStaticContract(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh")
	script := readFile(t, scriptPath)

	for _, want := range []string{
		"#!/usr/bin/env bash", "set -euo pipefail", "umask 077", "--run-dir", "--dry-run", "--stop-after",
		"--send-rate", "--measure-seconds", "--warmup-seconds", "--drain-timeout",
		"go build", "./cmd/wukongim", "./cmd/wkbench", "WK_CLUSTER_INITIAL_SLOT_COUNT=12",
		"WK_CLUSTER_HASH_SLOT_COUNT=256", "WK_CLUSTER_SLOT_REPLICA_N=3",
		"WK_CLUSTER_CHANNEL_REPLICA_N=3", "WK_CLUSTER_MAX_CHANNELS=50000",
		"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=200us", "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=1",
		"WK_CLUSTER_COMMIT_COORDINATOR_SYNC=true",
		"worker --mode chat-lifecycle", "host-metrics",
		"--process-metrics-path", "start_process_metrics_collector", "process-metrics-collector",
		"WK_PLUGIN_SOCKET_PATH",
		"soak chat-lifecycle", "request_coordinator_stop", "handle_signal", "GRACEFUL_STOP_DEADLINE",
		"capture_service_metrics", "storage-metrics-summary.awk", "storage_metrics_summary.tsv",
		"record_timeline_boundary warmup_end", "record_timeline_boundary measurement_end",
		"record_timeline_boundary drain_start", "record_timeline_boundary drain_end",
		"host-io-summary.awk", "host_io_summary.tsv",
		"--host-io-summary", "process-continuity.tsv", "report local-chat-lifecycle-step", "local-step.json",
		"kill -TERM", "kill -KILL", "pids", "final.json",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("shakeout script missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(script), "docker") {
		t.Fatal("shakeout script must not reference container tooling")
	}
	if strings.Contains(script, "RUN_ID=$(date") {
		t.Fatal("shakeout script must retain the fixed local run ID for reproducible workload decisions")
	}
	if output, err := exec.Command("bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash syntax failed: %v\n%s", err, output)
	}
}

func TestChatLifecycleShakeoutScriptDryRunIsReadOnlyAndRejectsBroadRunDirs(t *testing.T) {
	root := repoRoot(t)
	runDir := filepath.Join(t.TempDir(), "shakeout")
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(runDir))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRunDir := filepath.Join(canonicalParent, filepath.Base(runDir))
	cmd := exec.Command("bash", "scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh",
		"--dry-run", "--run-dir", runDir, "--base-port", "24000", "--send-rate", "400", "--measure-seconds", "120")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"run_dir=" + canonicalRunDir, "logical_slot_groups=12", "hash_slots=256", "replicas=3/3",
		"online_connections=2500", "offered_send_rate_per_second=400", "measured_duration_seconds=120",
		"commit_coordinator_flush_window=200us", "commit_coordinator_shards=1", "sync_commit=true",
		"service_1=http://127.0.0.1:24001", "worker_3=http://127.0.0.1:24053",
		"host_metrics_2=http://127.0.0.1:24062", "host_metrics_load=http://127.0.0.1:24060",
		"coordinator_config=" + filepath.Join(canonicalRunDir, "chat-lifecycle.yaml"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if _, statErr := os.Stat(runDir); !os.IsNotExist(statErr) {
		t.Fatalf("dry run created %s", runDir)
	}

	for _, broad := range []string{"/", root} {
		cmd = exec.Command("bash", "scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh", "--dry-run", "--run-dir", broad)
		cmd.Dir = root
		if rejected, rejectErr := cmd.CombinedOutput(); rejectErr == nil || !strings.Contains(string(rejected), "unsafe --run-dir") {
			t.Fatalf("broad run dir %q not rejected: %v\n%s", broad, rejectErr, rejected)
		}
	}
}

func TestChatLifecycleLocalBaselineStaircaseContract(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-local-baseline.sh")
	script := readFile(t, scriptPath)
	for _, want := range []string{
		"100,150,250,400,500,750,1000", "SEARCH_MEASURE_SECONDS=120", "REPEAT_MEASURE_SECONDS=600",
		"WARMUP_SECONDS=60", "DRAIN_TIMEOUT=90", "MINIMUM_FREE_PERCENT=10",
		"run-wukongim-three-node-chat-lifecycle-shakeout.sh", "--send-rate", "--measure-seconds",
		"first_failing_rate", "highest_clean_rate", "storage_confounded", "host_confounded",
		"local-baseline.json", "steps.tsv", "refine", "filesystem-preflight.txt", "checksums.sha256",
		`"online_connections": 2500`, `"logical_slot_groups": 12`, `"hash_slots": 256`,
		`"slot_replicas": 3`, `"channel_replicas": 3`, `"sync_commit": true`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("local baseline script missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(script), "docker") || strings.Contains(script, "workflow") || strings.Contains(script, "aliyun") {
		t.Fatal("local baseline must not invoke container or cloud operations")
	}
	if output, err := exec.Command("bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash syntax failed: %v\n%s", err, output)
	}

	runDir := filepath.Join(t.TempDir(), "baseline")
	cmd := exec.Command("bash", scriptPath, "--dry-run", "--run-dir", runDir, "--base-port", "25000")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		"rates=100,150,250,400,500,750,1000", "search_measure_seconds=120", "repeat_measure_seconds=600",
		"warmup_seconds=60", "drain_timeout_seconds=90", "base_port=25000",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("dry run missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", runDir)
	}
}

func TestHostIOSummaryPreservesAvailabilityInsteadOfFabricatingZero(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	available := filepath.Join(directory, "available.prom")
	unavailable := filepath.Join(directory, "unavailable.prom")
	availableBody := `wkbench_host_block_io_schema_info{physical_device="disk0",version="v1"} 1
wkbench_host_block_io_available{field="iops",physical_device="disk0"} 1
wkbench_host_block_io_available{field="bytes_per_second",physical_device="disk0"} 1
wkbench_host_block_io_available{field="utilization",physical_device="disk0"} 0
wkbench_host_block_io_available{field="service_time",physical_device="disk0"} 0
wkbench_host_block_io_available{field="read_write_split",physical_device="disk0"} 0
wkbench_host_block_io_iops{operation="total",physical_device="disk0"} 208
wkbench_host_block_io_bytes_per_second{operation="total",physical_device="disk0"} 1593835.52
`
	unavailableBody := `wkbench_host_block_io_schema_info{physical_device="unavailable",version="v1"} 1
wkbench_host_block_io_available{field="iops",physical_device="unavailable"} 0
wkbench_host_block_io_available{field="bytes_per_second",physical_device="unavailable"} 0
wkbench_host_block_io_available{field="utilization",physical_device="unavailable"} 0
wkbench_host_block_io_available{field="service_time",physical_device="unavailable"} 0
wkbench_host_block_io_available{field="read_write_split",physical_device="unavailable"} 0
`
	if err := os.WriteFile(available, []byte(availableBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unavailable, []byte(unavailableBody), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("awk", "-v", "tag=rate-100", "-v", "host=service-host", "-f",
		filepath.Join(root, "scripts", "host-io-summary.awk"), available)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("available summary failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "rate-100\tservice-host\tcomplete\tdisk0\t1\t208.000000\t1\t1593835.520000\t0\tunavailable\t0\tunavailable\t0") {
		t.Fatalf("available summary = %q", output)
	}
	command = exec.Command("awk", "-v", "tag=rate-100", "-v", "host=other-host", "-f",
		filepath.Join(root, "scripts", "host-io-summary.awk"), unavailable)
	output, err = command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "rate-100\tother-host\tunavailable\tunavailable\t0\tunavailable") {
		t.Fatalf("unavailable summary = %q/%v", output, err)
	}

	invalidVersion := filepath.Join(directory, "invalid-version.prom")
	if err := os.WriteFile(invalidVersion, []byte(strings.Replace(availableBody, `version="v1"`, `version="v2"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("awk", "-v", "tag=rate-100", "-v", "host=service-host", "-f",
		filepath.Join(root, "scripts", "host-io-summary.awk"), invalidVersion)
	output, err = command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "rate-100\tservice-host\tmissing\tdisk0") {
		t.Fatalf("invalid-version summary = %q/%v", output, err)
	}
}
