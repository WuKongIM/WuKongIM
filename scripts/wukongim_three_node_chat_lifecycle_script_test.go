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
		"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=5ms", "WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=1",
		"WK_CLUSTER_COMMIT_COORDINATOR_SYNC=true",
		"worker --mode chat-lifecycle", "host-metrics",
		"--process-metrics-path", "start_process_metrics_collector", "process-metrics-collector",
		"WK_PLUGIN_SOCKET_PATH",
		"soak chat-lifecycle", "request_coordinator_stop", "handle_signal", "GRACEFUL_STOP_DEADLINE",
		"OPERATOR_SIGNAL_STATUS", "request_pending_operator_stop", "--operator-interrupted",
		"detect-local-workload-overlap.sh", "check_measured_host_overlap", "--host-confounded",
		`local -a owned_pids=("$$" "${PIDS[@]}")`,
		"finalize_source_rebuildability_after_builds",
		"DRAIN_BOUNDARY_RECORDED",
		"coordinator_graceful_stop_timeout", "capture_graceful_stop_timeout_evidence",
		"coordinator_exited_before_stop_request", "record_coordinator_stop_request_failure",
		"finalize_unmeasured_harness_failure",
		"artifact_roots", `[[ -e "$path" ]] && artifact_roots+=("$path")`,
		"force_stop_timed_out_coordinator", "--harness-failure-reason",
		"graceful-stop-status.json", "wait_child_uninterrupted",
		"capture_service_metrics", "storage-metrics-summary.awk", "storage_metrics_summary.tsv",
		"capture-local-storage-overlap.sh", "storage-overlap.tsv", "--storage-overlap",
		"record_timeline_boundary warmup_end", "record_timeline_boundary measurement_end",
		"capture_service_metrics warmup-before",
		"record_timeline_boundary drain_start", "record_timeline_boundary_at \"$terminal_at\" drain_end",
		"write_phase_state warmup", "write_phase_state measurement", "write_phase_state drain", "write_phase_state shutdown",
		"report chat-lifecycle-cut-query", "report chat-lifecycle-timeline",
		"unified-timeline.json", "unified-timeline.tsv", "threshold-pprof-status.json",
		`WK_BENCH_API_TOKEN="$WK_BENCH_API_TOKEN"`,
		"capture-wukongim-local-threshold-pprof.sh", "join_threshold_pprof_capture",
		"stop_process_metrics_collector", "actual_offered_ratio", "terminal_product_failure",
		`overall_first_attempt_failure: {max_failures: 1, per_attempts: 1, operator: "<="}`,
		`any_minute_first_attempt_failure: {max_failures: 1, per_attempts: 1, operator: "<="}`,
		"host-io-summary.awk", "host_io_summary.tsv",
		"product-queue-snapshot.awk", "product_queue_summary.tsv", "wait_for_product_queue_convergence",
		"qualification evidence was not reached; capturing one non-converged product queue cut",
		"--host-io-summary", "--product-queue-summary", "process-continuity.tsv", "report local-chat-lifecycle-step", "local-step.json",
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
	if strings.LastIndex(script, "record_process_continuity") > strings.LastIndex(script, "stop_process_metrics_collector") ||
		strings.LastIndex(script, "stop_process_metrics_collector") > strings.LastIndex(script, "write_artifact_checksums") {
		t.Fatal("process continuity must be captured before the mutable collector is joined and checksums are written")
	}
	if strings.Index(script, "write_phase_state drain") > strings.Index(script, "request_coordinator_stop 'measured interval elapsed'") {
		t.Fatal("the measured phase must close before the coordinator stop request")
	}
	terminalBoundary := strings.LastIndex(script, "if ! close_terminal_drain_boundary; then")
	hostOverlapJoin := strings.LastIndex(script, "  check_measured_host_overlap\n")
	if terminalBoundary < 0 || hostOverlapJoin < 0 || terminalBoundary > hostOverlapJoin {
		t.Fatal("measured phase must close before joining the phase-scoped host-overlap monitor")
	}
	finalServiceSample := strings.LastIndex(script, `capture_service_metrics "sample-$metrics_sequence"`)
	afterServiceSample := strings.LastIndex(script, "capture_service_metrics after")
	if terminalBoundary < 0 || finalServiceSample < terminalBoundary ||
		afterServiceSample < 0 || finalServiceSample > afterServiceSample {
		t.Fatal("the final service sample must follow the exact terminal boundary and precede the after sample")
	}
	terminalSampleWait := strings.LastIndex(script, "wait_for_service_sample_after_terminal_boundary")
	if terminalSampleWait < terminalBoundary || terminalSampleWait > finalServiceSample ||
		!strings.Contains(script, `TERMINAL_BOUNDARY_AT="$terminal_at"`) {
		t.Fatal("the final second-resolution service sample must wait beyond the exact terminal boundary second")
	}
	if !strings.Contains(script, "if (( DRAIN_BOUNDARY_RECORDED == 0 )); then") {
		t.Fatal("measured finalization must not duplicate already-recorded drain boundaries after a stop-request race")
	}
	unmeasuredFinalize := strings.Index(script, "finalize_unmeasured_harness_failure()")
	if unmeasuredFinalize < 0 {
		t.Fatal("legacy stop-request race must have a typed artifact-only finalizer")
	}
	unmeasuredBody := script[unmeasuredFinalize:]
	for _, want := range []string{"stop_recorded_processes", "write_artifact_checksums", "exit 6"} {
		if !strings.Contains(unmeasuredBody, want) {
			t.Fatalf("legacy stop-request race finalizer missing %q", want)
		}
	}
	if strings.Contains(script, `die "coordinator did not finish graceful stop`) {
		t.Fatal("graceful-stop timeout must enter typed evidence finalization instead of die/EXIT cleanup")
	}
	loopStart := strings.Index(script, `while kill -0 "$COORDINATOR_PID" 2>/dev/null; do`)
	loopEnd := -1
	if loopStart >= 0 {
		loopEnd = strings.Index(script[loopStart:], "\ndone\n")
	}
	if loopStart < 0 || loopEnd < 0 || !strings.Contains(script[loopStart:loopStart+loopEnd], "check_measured_host_overlap") {
		t.Fatal("measured coordinator loop must periodically check for foreign WuKongIM workloads")
	}
	buildEnd := strings.LastIndex(script, `(cd "$ROOT_DIR" && GOWORK=off go build`)
	identityWrite := strings.Index(script, "\nrecord_evidence_identity\n")
	sourceFinalize := strings.Index(script, "\nfinalize_source_rebuildability_after_builds\n")
	if buildEnd < 0 || identityWrite < 0 || sourceFinalize <= buildEnd || sourceFinalize >= identityWrite {
		t.Fatal("source rebuildability must be finalized after both builds and before identity is recorded")
	}
	for _, forbidden := range []string{
		`request_coordinator_stop 'measured interval elapsed' || die`,
		`request_coordinator_stop '--stop-after elapsed' || die`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("coordinator stop-request race must enter typed evidence finalization instead of die: %q", forbidden)
		}
	}
	timeoutCapture := strings.Index(script, "capture_graceful_stop_timeout_evidence")
	timeoutForceStop := strings.Index(script, "force_stop_timed_out_coordinator")
	if timeoutCapture < 0 || timeoutForceStop < 0 || timeoutCapture > timeoutForceStop {
		t.Fatal("graceful-stop timeout must close worker evidence before forcing and joining the coordinator")
	}
	if output, err := exec.Command("bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash syntax failed: %v\n%s", err, output)
	}
}

func TestChatLifecycleShakeoutIntegrationRunsBeforeParallelPhase(t *testing.T) {
	root := repoRoot(t)
	source := readFile(t, filepath.Join(root, "scripts", "wukongim_three_node_chat_lifecycle_script_integration_test.go"))
	start := strings.Index(source, "func TestChatLifecycleShakeoutScriptIntegration(t *testing.T) {")
	if start < 0 {
		t.Fatal("real shakeout integration test is missing")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("real shakeout integration test body is not closed")
	}
	body := source[start : start+end]
	exclusive := strings.Index(body, "runTimingSensitiveShellScriptTestBeforeParallelPhase(t)")
	repository := strings.Index(body, "repoRoot(t)")
	if exclusive < 0 || repository < 0 || exclusive > repository {
		t.Fatal("real shakeout must stay in the serial phase before repoRoot installs the ordinary parallel gate")
	}
	helperSource := readFile(t, filepath.Join(root, "scripts", "script_test_helpers_integration_test.go"))
	helperStart := strings.Index(helperSource, "func runTimingSensitiveShellScriptTestBeforeParallelPhase(t *testing.T) {")
	if helperStart < 0 {
		t.Fatal("timing-sensitive serial gate is missing")
	}
	helperEnd := strings.Index(helperSource[helperStart:], "\n}")
	if helperEnd < 0 {
		t.Fatal("timing-sensitive serial gate body is not closed")
	}
	helperBody := helperSource[helperStart : helperStart+helperEnd]
	if strings.Contains(helperBody, "t.Parallel()") || !strings.Contains(helperBody, "parallelizedShellTests.LoadOrStore(t") {
		t.Fatal("timing-sensitive serial gate must mark the test without entering Go's parallel phase")
	}
}

func TestProductQueueSnapshotSummarizerRequiresBothMetricFamilies(t *testing.T) {
	root := repoRoot(t)
	metrics := filepath.Join(t.TempDir(), "metrics.prom")
	body := `wukongim_runtime_pool_queue_depth{component="gateway",pool="send"} 7
wukongim_runtime_pool_queue_depth{component="slot",pool="scheduler"} 3
wukongim_runtime_pool_inflight{component="gateway",pool="send"} 5
wukongim_runtime_pool_inflight{component="slot",pool="scheduler"} 2
`
	if err := os.WriteFile(metrics, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("awk", "-f", "scripts/product-queue-snapshot.awk", metrics)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "complete\t10\t7\n" {
		t.Fatalf("queue summary = %q, %v", output, err)
	}
	if err := os.WriteFile(metrics, []byte(`wukongim_runtime_pool_queue_depth{component="gateway"} 7
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("awk", "-f", "scripts/product-queue-snapshot.awk", metrics)
	cmd.Dir = root
	output, err = cmd.CombinedOutput()
	if err == nil || string(output) != "missing\t0\t0\n" {
		t.Fatalf("incomplete queue summary = %q, %v", output, err)
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
		"raw_metrics_sample_seconds=30",
		"commit_coordinator_flush_window=5ms", "commit_coordinator_shards=1", "sync_commit=true",
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
		"250,500,750,1000", "STEP_MEASURE_SECONDS=300", "SOAK_RATE=1000", "SOAK_MEASURE_SECONDS=600",
		"WARMUP_SECONDS=60", "DRAIN_TIMEOUT=90", "MINIMUM_FREE_PERCENT=10",
		"run-wukongim-three-node-chat-lifecycle-shakeout.sh", "--send-rate", "--measure-seconds",
		"first_failing_rate", "highest_clean_rate", "storage_confounded", "host_confounded",
		"local-baseline.json", "steps.tsv", "required_1000_soak", "filesystem-preflight.txt", "checksums.sha256",
		"prune_step_runtime_state", "runtime-state-pruned.txt", `find "$path" -xdev -depth -delete`,
		`"online_connections": 2500`, `"logical_slot_groups": 12`, `"hash_slots": 256`,
		`"slot_replicas": 3`, `"channel_replicas": 3`, `"commit_coordinator_flush_window": "5ms"`, `"sync_commit": true`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("local baseline script missing %q", want)
		}
	}
	for _, forbidden := range []string{"100,150,250,400,500,750,1000", "run_step refine", "refine_increment", "repeat_rate=\"$highest_clean_rate\""} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("local baseline retains obsolete adaptive staircase contract %q", forbidden)
		}
	}
	if strings.Contains(strings.ToLower(script), "docker") || strings.Contains(script, "workflow") || strings.Contains(script, "aliyun") {
		t.Fatal("local baseline must not invoke container or cloud operations")
	}
	if strings.Contains(script, `local phase="$1" rate="$2" measured="$3" step_dir=`) {
		t.Fatal("run_step must bind positional parameters before expanding them under set -u")
	}
	for _, want := range []string{
		`local phase="$1" rate="$2" measured="$3"`,
		`local step_dir="$RUN_DIR/steps/${phase}-rate-$rate"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("local baseline run_step missing safe declaration %q", want)
		}
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
		"rates=250,500,750,1000", "step_measure_seconds=300", "soak_rate=1000", "soak_measure_seconds=600",
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
