package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingleNodeBaselineArtifactFixturesUseBoundedParallelGate(t *testing.T) {
	source := readFile(t, filepath.Join(repoRoot(t), "scripts", "wukongim_single_node_baseline_artifact_integration_test.go"))
	if strings.Contains(source, "runTimingSensitiveShellScriptTestExclusively(t)") {
		t.Fatal("isolated fake single-node artifact fixtures must not serialize the complete scripts integration suite")
	}
	if got, want := strings.Count(source, "runHeavyShellScriptTestInParallel(t)"), 13; got != want {
		t.Fatalf("bounded parallel artifact gates = %d, want %d", got, want)
	}
}

func TestSingleNodeBaselineAuthorizationRequiresReviewedContractAndFinalSeal(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))

	for _, want := range []string{
		`report local-single-node-step`,
		`--group-members "$GROUP_MEMBERS"`,
		`--scenario "$report_dir/scenario.yaml"`,
		`--plan "$report_dir/plan.json"`,
		`--run-report "$report_dir/report.json"`,
		`traffic:(.lifecycle.traffic // {})`,
		`recvack_successes:(.lifecycle.receive_drain.recvack_successes // 0)`,
		`fanout_proof:(if (.lifecycle.receive_drain.fanout_proof | type) == "object" then {`,
		`logical_sendacks:(.lifecycle.receive_drain.fanout_proof.logical_sendacks // 0)`,
		`(.lifecycle.receive_drain.fanout_proof.version == "wukongim/group-fanout-proof/v1")`,
		`report local-single-node-baseline`,
		`lifecycle-status.jsonl`,
		`# wkbench_local_single_node_cut `,
		`capture-local-storage-overlap.sh`,
		`storage-overlap.tsv`,
		`snapshot-inventory`,
		`--storage-overlap`,
		`process_start_token`,
		`detect-local-workload-overlap.sh`,
		`host-overlap.detected`,
		`typed-step-evidence.json`,
		`local-baseline-authorization.json`,
		`retry:
        enabled: true`,
		`token: "\${WK_BENCH_API_TOKEN}"`,
		`ensure_local_bench_api_token`,
		`od -An -N32 -tx1 /dev/urandom`,
		`report local-single-node-step-closure`,
		`report local-single-node-publish`,
		`report local-single-node-completion`,
		`[[ "$QPS_LIST" == "250,500,750,1000" ]]`,
		`[[ "$USERS" -eq 2500 ]]`,
		`[[ "$DURATION" == 5m ]]`,
		`[[ "$WARMUP" == 60s ]]`,
		`[[ "$COOLDOWN" == 90s ]]`,
		`TERMINAL_CUT_ACK_SAFETY_SECONDS="${WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS:-15}"`,
		`WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS must be at least 15 seconds`,
		`[[ "$TERMINAL_CUT_ACK_SAFETY_SECONDS" -eq 15 ]]`,
		`[[ "$STORAGE_OVERLAP_SAMPLE_INTERVAL" == 20 ]]`,
		`[[ "$START_CLUSTER" -eq 1 ]]`,
		`[[ "$START_WORKER" -eq 1 ]]`,
		`[[ "$CLEAN_CLUSTER" -eq 1 ]]`,
		`"reviewed_contract_satisfied": %s`,
		`"authorizes_three_node_diagnostic": %s`,
		`write_local_baseline_result`,
		`write_local_artifact_checksums`,
		`verify_local_artifact_checksums`,
		`discard_owned_worker_runtime_state`,
		`rm -f -- "$state_file"`,
		`rmdir "$state_dir"`,
		`initialize_baseline_invocation_id`,
		`od -An -N16 -tx1 /dev/urandom`,
		`baseline_invocation_id`,
		`single-node-${BASELINE_INVOCATION_ID}-fixed-`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node authorization contract missing %q", want)
		}
	}

	main := strings.LastIndex(script, "\nmain() {")
	if main < 0 {
		t.Fatal("single-node baseline main function missing")
	}
	mainBody := script[main:]
	validateOutDir := strings.Index(mainBody, "\n  validate_and_prepare_out_dir")
	invocationID := strings.Index(mainBody, "\n  initialize_baseline_invocation_id")
	preflight := strings.Index(mainBody, "\n  local_baseline_preflight")
	if validateOutDir < 0 || invocationID < 0 || preflight < 0 || !(validateOutDir < invocationID && invocationID < preflight) {
		t.Fatal("baseline invocation identity must be created once after OUT_DIR validation and before preflight publication")
	}
	result := strings.LastIndex(mainBody, "\n  write_local_baseline_result ")
	seal := strings.LastIndex(mainBody, "write_local_artifact_checksums")
	verify := strings.LastIndex(mainBody, "verify_local_artifact_checksums")
	if result < 0 || seal < 0 || verify < 0 || !(seal < verify && verify < result) {
		t.Fatal("atomic local-baseline completion marker must be published after the final checksum manifest is verified")
	}

	for _, stop := range []string{
		"stop_runtime_pool_sampler",
		"stop_server_resource_sampler",
		"stop_host_metrics_writer",
		"stop_worker_writer",
		"discard_owned_worker_runtime_state",
		"stop_cluster_writer",
	} {
		index := strings.Index(mainBody, "\n  "+stop)
		if index < 0 || index > result {
			t.Fatalf("artifact writer %q must be joined before the final result and seal", stop)
		}
	}
	lastCollect := strings.LastIndex(mainBody, "\n  collect_node_logs after")
	lastSummary := strings.LastIndex(mainBody, "\n  print_summary")
	if lastCollect < 0 || lastSummary < 0 || lastCollect > result || lastSummary > result {
		t.Fatal("after logs and summaries must be complete before the final result and checksum manifest")
	}
	if strings.Contains(mainBody[seal:], "collect_node_logs") || strings.Contains(mainBody[seal:], "print_summary") {
		t.Fatal("no artifact-producing collector may run after checksums are written")
	}
	for _, want := range []string{
		`"completion_marker": true`, `"completion_generation": "%s"`,
		`"artifact_manifest_sha256": "%s"`, `"typed_authorization_sha256": "%s"`,
		`--draft "$temporary" --output "$OUT_DIR/local-baseline.json"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("atomic completion publication missing %q", want)
		}
	}
}

func TestSingleNodeBaselineRejectsUsersBeyondTerminalFenceCapacityBeforeBuildOrProcess(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	buildMarker := filepath.Join(t.TempDir(), "go-invoked")
	startMarker := filepath.Join(t.TempDir(), "product-started")
	outDir := filepath.Join(t.TempDir(), "must-not-be-created")
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\n: > \"$WK_TEST_BUILD_MARKER\"\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeStart := filepath.Join(t.TempDir(), "start-single-node.sh")
	if err := os.WriteFile(fakeStart, []byte("#!/usr/bin/env bash\n: > \"$WK_TEST_START_MARKER\"\nexit 98\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh",
		"--users", "2501", "--out-dir", outDir)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_USERS",
		"WK_BENCH_SINGLE_NODE_OUT_DIR",
		"WK_BENCH_SINGLE_NODE_START_SCRIPT",
		"WK_TEST_BUILD_MARKER",
		"WK_TEST_START_MARKER",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_SINGLE_NODE_START_SCRIPT="+fakeStart,
		"WK_TEST_BUILD_MARKER="+buildMarker,
		"WK_TEST_START_MARKER="+startMarker,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("users beyond the terminal fence capacity unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(string(output), "--users must not exceed terminal fence capacity 2500: 2501") {
		t.Fatalf("unexpected capacity failure: err=%v output=%s", err, output)
	}
	for name, path := range map[string]string{
		"build":         buildMarker,
		"product start": startMarker,
		"output":        outDir,
	} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rejected users caused %s side effect at %s: %v", name, path, statErr)
		}
	}
}

func TestSingleNodeBaselineRejectsTopologyEnvironmentOverrideWithoutSideEffects(t *testing.T) {
	root := repoRoot(t)
	fakeBench := filepath.Join(t.TempDir(), "wkbench")
	if err := os.WriteFile(fakeBench, []byte("#!/usr/bin/env bash\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"WK_NODE_ID":                               "2",
		"WK_CLUSTER_SEEDS":                         `["127.0.0.1:7002"]`,
		"WK_CLUSTER_NODES":                         `[{"id":1,"addr":"127.0.0.1:7001"}]`,
		"WK_API_LISTEN_ADDR":                       "127.0.0.1:5002",
		"WK_EXTERNAL_TCPADDR":                      "127.0.0.1:5101",
		"WK_GATEWAY_LISTENERS":                     `[{"name":"tcp","addr":"127.0.0.1:5101"}]`,
		"WK_METRICS_ENABLE":                        "false",
		"WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES":  "1",
		"WK_CHANNEL_APPEND_SHARD_COUNT":            "2",
		"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT": "1s",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":    "1",
		"WK_BENCH_API_ENABLE":                      "false",
		"WK_WUKONGIM_SINGLE_NODE_READY_URL":        "http://127.0.0.1:5002/readyz",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "must-not-be-created")
			command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh",
				"--no-worker", "--no-start", "--qps", "250", "--wkbench-bin", fakeBench, "--out-dir", outDir)
			command.Dir = root
			command.Env = append(envWithout(
				"WK_NODE_ID", "WK_CLUSTER_ID", "WK_CLUSTER_LISTEN_ADDR", "WK_CLUSTER_ADVERTISE_ADDR",
				"WK_CLUSTER_SEEDS", "WK_CLUSTER_JOIN_TOKEN", "WK_CLUSTER_NODES",
				"WK_API_LISTEN_ADDR", "WK_EXTERNAL_TCPADDR", "WK_GATEWAY_LISTENERS", "WK_METRICS_ENABLE", "WK_BENCH_API_ENABLE",
				"WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES", "WK_CHANNEL_APPEND_SHARD_COUNT",
				"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT", "WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS",
				"WK_WUKONGIM_SINGLE_NODE_READY_URL",
				"WK_BENCH_SINGLE_NODE_OUT_DIR", "WK_BENCH_BIN",
			), name+"="+value)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("topology override unexpectedly passed:\n%s", output)
			}
			if !strings.Contains(string(output), name) || !strings.Contains(string(output), "topology override") {
				t.Fatalf("unexpected topology override failure: err=%v output=%s", err, output)
			}
			if _, statErr := os.Lstat(outDir); !os.IsNotExist(statErr) {
				t.Fatalf("rejected topology override wrote OUT_DIR: %v", statErr)
			}
		})
	}
}

func TestSingleNodeBaselineRejectsDisabledRecvAckBeforeBuildOrProcess(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	buildMarker := filepath.Join(t.TempDir(), "go-invoked")
	startMarker := filepath.Join(t.TempDir(), "product-started")
	outDir := filepath.Join(t.TempDir(), "must-not-be-created")
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\n: > \"$WK_TEST_BUILD_MARKER\"\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeStart := filepath.Join(t.TempDir(), "start-single-node.sh")
	if err := os.WriteFile(fakeStart, []byte("#!/usr/bin/env bash\n: > \"$WK_TEST_START_MARKER\"\nexit 98\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh",
		"--recv-ack", "false", "--out-dir", outDir)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_RECV_ACK",
		"WK_BENCH_SINGLE_NODE_OUT_DIR",
		"WK_BENCH_SINGLE_NODE_START_SCRIPT",
		"WK_TEST_BUILD_MARKER",
		"WK_TEST_START_MARKER",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_SINGLE_NODE_START_SCRIPT="+fakeStart,
		"WK_TEST_BUILD_MARKER="+buildMarker,
		"WK_TEST_START_MARKER="+startMarker,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("disabled recv_ack unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(string(output), "--recv-ack must be true for the reviewed external terminal cut") {
		t.Fatalf("unexpected recv_ack failure: err=%v output=%s", err, output)
	}
	for name, path := range map[string]string{
		"build":         buildMarker,
		"product start": startMarker,
		"output":        outDir,
	} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rejected recv_ack caused %s side effect at %s: %v", name, path, statErr)
		}
	}
}

func TestSingleNodeBaselineRejectsNoStartMultiRateBeforeBuildOrProcess(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	buildMarker := filepath.Join(t.TempDir(), "go-invoked")
	outDir := filepath.Join(t.TempDir(), "must-not-be-created")
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\n: > \"$WK_TEST_BUILD_MARKER\"\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh",
		"--no-start", "--qps", "250,500", "--out-dir", outDir)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_BENCH_SINGLE_NODE_QPS",
		"WK_BENCH_SINGLE_NODE_OUT_DIR",
		"WK_TEST_BUILD_MARKER",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_TEST_BUILD_MARKER="+buildMarker,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("--no-start with multiple rates unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(string(output), "--no-start cannot be combined with multiple --qps values; use one rate per invocation") {
		t.Fatalf("unexpected no-start multi-rate failure: err=%v output=%s", err, output)
	}
	for name, path := range map[string]string{
		"build":  buildMarker,
		"output": outDir,
	} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rejected no-start multi-rate invocation caused %s side effect at %s: %v", name, path, statErr)
		}
	}
}

func TestSingleNodeBaselineClosesAndTypesEachStepBeforeAdvancing(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		"write_local_step_checksums",
		"verify_local_step_checksums",
		"typed-step-result.json",
		`--payload-manifest "$step_manifest"`,
		`--result-output "$result_output"`,
		`--closure-output "$closure_output"`,
		`--closure "$closure" --output "$consumer"`,
		"read_typed_local_step_result",
		`step_closures:[]`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node typed step control missing %q", want)
		}
	}
	main := script[strings.LastIndex(script, "\nmain() {"):]
	run := strings.Index(main, `run_attempt "$qps"`)
	checksum := strings.Index(main, `write_local_step_checksums "$qps"`)
	verify := strings.Index(main, `verify_local_step_checksums "$qps"`)
	typed := strings.Index(main, `write_typed_local_step_evidence "$qps"`)
	decision := strings.Index(main, `read_typed_local_step_result "$qps"`)
	if run < 0 || checksum < 0 || verify < 0 || typed < 0 || decision < 0 ||
		!(run < checksum && checksum < verify && verify < typed && typed < decision) {
		t.Fatalf("step closure order is not run -> checksums -> verify -> typed result -> decision")
	}
	if strings.Contains(main, `classify_latest_local_step "$qps"`) {
		t.Fatal("legacy summary classifier still controls staircase progression")
	}
}

func TestSingleNodeBaselineUsesOneTerminalProductGenerationPerRateStep(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		"start_cluster_generation",
		"stop_cluster_generation",
		`--no-build`,
		`cluster-generations/${tag}`,
		`if [[ "$CLUSTER_GENERATION_INDEX" -eq 0 && "$CLEAN_CLUSTER" -eq 1 ]]`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node terminal product generation contract missing %q", want)
		}
	}

	main := script[strings.LastIndex(script, "\nmain() {"):]
	loop := strings.Index(main, `for qps in "${QPS_VALUES[@]}"; do`)
	start := strings.Index(main, `start_cluster_generation "$qps"`)
	run := strings.Index(main, `run_attempt "$qps"`)
	stop := strings.Index(main, `stop_cluster_generation "$qps"`)
	checksums := strings.Index(main, `write_local_step_checksums "$qps"`)
	if loop < 0 || start < 0 || run < 0 || stop < 0 || checksums < 0 ||
		!(loop < start && start < run && run < stop && stop < checksums) {
		t.Fatal("each rate step must start, exercise, stop, and seal one independent product generation")
	}

	prefix := main[:loop]
	if strings.Contains(prefix, "\n  start_cluster\n") {
		t.Fatal("single-node product must not be started outside the per-rate generation loop")
	}
}

func TestSingleNodeBaselineBindsQueueAndStorageCutsToWorkerTimeline(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		`external_terminal_cut: true`,
		`product_queue_cut_metadata`,
		`(.phase == "warmup") and (.active_phase == "run")`,
		`(.phase == "run") and (.active_phase == "cooldown")`,
		`report local-single-node-queue-convergence`,
		`product_failure_counter_increased`,
		`terminal_cut_ready`,
		`terminal_cut_deadline_at`,
		`/v1/terminal-cut`,
		`capture_storage_overlap_cut`,
		`--snapshot-root "$SINGLE_NODE_DATA_DIR/slotraft-snapshots"`,
		`--node node-1`,
		`terminal-pre-close.prom`,
		`stop_terminal_cut_observer`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node timeline cut contract missing %q", want)
		}
	}
	runStart := strings.Index(script, "run_attempt() {")
	runEnd := strings.Index(script[runStart:], "\n}\n")
	if runStart < 0 || runEnd < 0 {
		t.Fatal("run_attempt function missing")
	}
	body := script[runStart : runStart+runEnd]
	startObserver := strings.Index(body, "start_terminal_cut_observer")
	run := strings.Index(body, `"$WK_BENCH_BIN" run`)
	joinObserver := strings.Index(body, "stop_terminal_cut_observer")
	stopSampler := strings.Index(body, "stop_runtime_pool_sampler")
	terminalMetrics := strings.Index(body, "scrape_metrics \"$tag\" after")
	if startObserver < 0 || run < 0 || joinObserver < 0 || stopSampler < 0 || terminalMetrics < 0 ||
		!(startObserver < run && run < joinObserver && joinObserver < stopSampler && stopSampler < terminalMetrics) {
		t.Fatal("external pre-close observer must surround wkbench and join before samplers and stopped metrics")
	}
}

func TestSingleNodeBaselineSealRequiresConfigLogsAndBinaryIdentity(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		`config/effective-wukongim.toml`,
		`logs/after/node1.log`,
		`bin/wukongim`,
		`bin/wkbench`,
		`artifact-identity.tsv`,
		`wukongim_binary_sha256`,
		`wkbench_binary_sha256`,
		`local-baseline.json`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node artifact seal missing %q", want)
		}
	}
	main := script[strings.LastIndex(script, "\nmain() {"):]
	prepare := strings.Index(main, "prepare_sealed_test_binaries")
	start := strings.Index(main, "start_cluster")
	if prepare < 0 || start < 0 || prepare > start {
		t.Fatal("tested binaries must be prepared under OUT_DIR/bin before any cluster or worker process starts")
	}
	for _, want := range []string{
		`WK_BENCH_BIN="$OUT_DIR/$SEALED_WKBENCH_RELATIVE"`,
		`WUKONGIM_BIN="$OUT_DIR/$SEALED_WUKONGIM_RELATIVE"`,
		`WK_WUKONGIM_SINGLE_NODE_BIN="$WUKONGIM_BIN"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("actual tested binary seam missing %q", want)
		}
	}
}

func TestSingleNodeBaselineUsesOnlyTypedFirstThresholdProfileCapture(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		`PROFILE_SECONDS="${WK_BENCH_PROFILE_SECONDS:-10}"`,
		`report local-single-node-profile-threshold`,
		`capture-wukongim-local-threshold-pprof.sh`,
		`.triggered == true`,
		`.evidence_complete == true`,
		`actual_offered_ratio`,
		`terminal_product_failure`,
		`--trigger-observed-phase measurement`,
		`--profile-status "$report_dir/evidence/threshold-pprof-status.json"`,
		`terminal_pre_close:(.lifecycle.terminal_pre_close // false)`,
		`receive_drain:(if (.lifecycle.receive_drain | type) == "object" then {`,
		`stable_zero_observations:(.lifecycle.receive_drain.stable_zero_observations // 0)`,
		`stop_threshold_profile_watcher`,
		`wait "$watcher_child_pid"`,
		`write_threshold_profile_status "$tag"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node threshold profile contract missing %q", want)
		}
	}
	if strings.Contains(script, "capture_node_pprof") || strings.Contains(script, "/debug/pprof/") {
		t.Fatal("single-node wrapper must not perform unconditional or manually requested pprof capture")
	}
	watcherStart := strings.Index(script, "threshold_profile_watcher_loop() {")
	watcherEnd := strings.Index(script[watcherStart:], "\n}\n")
	if watcherStart < 0 || watcherEnd < 0 {
		t.Fatal("typed threshold profile watcher function missing")
	}
	watcher := script[watcherStart : watcherStart+watcherEnd]
	query := strings.Index(watcher, "report local-single-node-profile-threshold")
	trigger := strings.Index(watcher, ".triggered == true")
	helper := strings.Index(watcher, `"$THRESHOLD_PROFILE_HELPER"`)
	if query < 0 || trigger < 0 || helper < 0 || !(query < trigger && trigger < helper) {
		t.Fatal("profile helper is not gated by the typed first-threshold query")
	}
	runStart := strings.Index(script, "run_attempt() {")
	runEnd := strings.Index(script[runStart:], "\n}\n")
	if runStart < 0 || runEnd < 0 {
		t.Fatal("run_attempt function missing")
	}
	body := script[runStart : runStart+runEnd]
	run := strings.Index(body, `"$WK_BENCH_BIN" run`)
	drain := strings.Index(body, `write_threshold_profile_phase "$tag" drain`)
	join := strings.Index(body, `stop_threshold_profile_watcher "$tag"`)
	if run < 0 || drain < 0 || join < 0 || !(run < drain && drain < join) {
		t.Fatal("profile phase close/join must occur after SEND admission and before run_attempt returns")
	}
	outDir := filepath.Join(t.TempDir(), "must-not-be-created")
	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh", "--profile-seconds", "31", "--out-dir", outDir)
	command.Dir = repoRoot(t)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "--profile-seconds must be an integer from 1 through 30") {
		t.Fatalf("unbounded profile duration was not rejected: err=%v output=%s", err, output)
	}
	if _, err := os.Lstat(outDir); !os.IsNotExist(err) {
		t.Fatalf("rejected profile duration wrote OUT_DIR: %v", err)
	}
}

func TestSingleNodeBaselineUsesPrivateArtifactsAndRedactedConfig(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		"umask 077",
		"write_redacted_effective_config",
		"original_config_sha256",
		`report redact-config`,
		`[local_single_node_runtime]`,
		`topology_environment_overrides_rejected = true`,
		`endpoint_environment_overrides_rejected = true`,
		`product_environment_hermetic = true`,
		`freeze_runtime_config`,
		`verify_runtime_config_snapshot`,
		`discard_runtime_config_snapshot`,
		`env -i`,
		`wukongim-single-node-config.XXXXXX`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("private artifact contract missing %q", want)
		}
	}
	if strings.Contains(script, `cp "$WUKONGIM_CONFIG" "$OUT_DIR/config/effective-wukongim.toml"`) {
		t.Fatal("custom config must never be copied into evidence without redaction")
	}
	redactorStart := strings.Index(script, "write_redacted_effective_config() {")
	redactorEnd := strings.Index(script[redactorStart:], "\n}\n")
	if redactorStart < 0 || redactorEnd < 0 {
		t.Fatal("write_redacted_effective_config function missing")
	}
	redactorBody := script[redactorStart : redactorStart+redactorEnd]
	if strings.Contains(redactorBody, "awk") {
		t.Fatal("config redaction must use the project TOML parser, not a line-oriented awk parser")
	}
}

func TestSingleNodeBaselineFreezesConfigBeforeProductAndSealsNoPlaintextSnapshot(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	main := script[strings.LastIndex(script, "\nmain() {"):]
	freeze := strings.Index(main, "freeze_runtime_config")
	start := strings.Index(main, `start_cluster_generation "$qps"`)
	discard := strings.LastIndex(main, "discard_runtime_config_snapshot")
	seal := strings.LastIndex(main, "write_local_artifact_checksums")
	if freeze < 0 || start < 0 || discard < 0 || seal < 0 || !(freeze < start && start < discard && discard < seal) {
		t.Fatal("runtime config must be frozen before product startup and deleted before the final artifact manifest")
	}
	for _, want := range []string{
		`before_sha="$(sha256_file "$canonical_source")"`,
		`after_sha="$(sha256_file "$canonical_source")"`,
		`snapshot_sha="$(sha256_file "$temporary")"`,
		`"$before_sha" == "$after_sha" && "$before_sha" == "$snapshot_sha"`,
		`WUKONGIM_CONFIG="$RUNTIME_CONFIG_SNAPSHOT"`,
		`WUKONGIM_CONFIG_SOURCE_REVIEWED=true`,
		`[[ "$WUKONGIM_CONFIG_SOURCE_REVIEWED" == true ]]`,
		`git -C "$ROOT_DIR" show HEAD:scripts/wukongim/wukongim.toml`,
		`--argjson canonical_source_config "$WUKONGIM_CONFIG_SOURCE_REVIEWED"`,
		`canonical_source_config:$canonical_source_config`,
		`product-executable.tsv`,
		`source_config_sha256`,
		`pre_spawn_sha256`,
		`post_stop_sha256`,
		`sealed_binary_sha256`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("runtime config/executable seal contract missing %q", want)
		}
	}
}

func TestSingleNodeBaselineFailsClosedWhenConfigChangesDuringSnapshot(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	tempDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "run")
	config := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(config, []byte("stable-before-copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeCP := filepath.Join(binDir, "cp")
	if err := os.WriteFile(fakeCP, []byte(`#!/usr/bin/env bash
set -euo pipefail
/bin/cp "$@"
if [[ "${@: -1}" == */config.toml.next ]]; then
  printf 'mutated-during-copy\n' >> "$WK_TEST_UNSTABLE_CONFIG_SOURCE"
fi
`), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh", "--out-dir", outDir)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_WUKONGIM_SINGLE_NODE_CONFIG", "WK_BENCH_SINGLE_NODE_OUT_DIR",
		"WK_TEST_UNSTABLE_CONFIG_SOURCE", "TMPDIR",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_WUKONGIM_SINGLE_NODE_CONFIG="+config,
		"WK_TEST_UNSTABLE_CONFIG_SOURCE="+config,
		"TMPDIR="+tempDir,
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "failed to create a stable private runtime config snapshot") {
		t.Fatalf("unstable config copy did not fail closed: err=%v\n%s", err, output)
	}
	entries, readErr := os.ReadDir(tempDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed snapshot left private config orphan: %v", entries)
	}
	if entries, readErr = os.ReadDir(outDir); readErr != nil || len(entries) != 0 {
		t.Fatalf("unstable config wrote retained evidence: entries=%v err=%v", entries, readErr)
	}
}

func TestSingleNodeBaselineRejectsPlaintextSnapshotInsideArtifactRoot(t *testing.T) {
	root := repoRoot(t)
	parent := t.TempDir()
	outDir := filepath.Join(parent, "run")
	config := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(config, []byte("secret-canary=must-stay-outside-artifacts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh", "--out-dir", outDir)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_WUKONGIM_SINGLE_NODE_CONFIG", "WK_BENCH_SINGLE_NODE_OUT_DIR", "TMPDIR",
	),
		"WK_WUKONGIM_SINGLE_NODE_CONFIG="+config,
		"TMPDIR="+outDir,
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "failed to create a stable private runtime config snapshot") {
		t.Fatalf("artifact-root TMPDIR did not fail closed: err=%v\n%s", err, output)
	}
	entries, readErr := os.ReadDir(outDir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("artifact-root TMPDIR retained plaintext material: entries=%v err=%v", entries, readErr)
	}
}

func TestSingleNodeBaselineValidatesDedicatedOutputDirectoryBeforeWriting(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	main := script[strings.LastIndex(script, "\nmain() {"):]
	validate := strings.Index(main, "validate_and_prepare_out_dir")
	firstMkdir := strings.Index(main, "mkdir")
	if validate < 0 || firstMkdir < 0 || validate > firstMkdir {
		t.Fatal("OUT_DIR must be canonicalized and validated before main performs its first mkdir")
	}
	for _, want := range []string{"HOME", "ROOT_DIR", "OUT_DIR must not be a symlink", "directory_not_empty"} {
		if !strings.Contains(script, want) {
			t.Fatalf("OUT_DIR safety contract missing %q", want)
		}
	}
}

func TestSingleNodeBaselineRejectsDangerousOutputDirectoriesWithoutMutation(t *testing.T) {
	root := repoRoot(t)
	nonEmpty := t.TempDir()
	sentinel := filepath.Join(nonEmpty, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkParent := t.TempDir()
	symlinkTarget := t.TempDir()
	symlink := filepath.Join(symlinkParent, "run")
	if err := os.Symlink(symlinkTarget, symlink); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()

	for _, tt := range []struct {
		name string
		path string
		home string
	}{
		{name: "filesystem-root", path: "/"},
		{name: "repository-root", path: root},
		{name: "home", path: home, home: home},
		{name: "symlink", path: symlink},
		{name: "non-empty", path: nonEmpty},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh", "--out-dir", tt.path)
			command.Dir = root
			command.Env = envWithout("WK_BENCH_SINGLE_NODE_OUT_DIR")
			if tt.home != "" {
				command.Env = append(command.Env, "HOME="+tt.home)
			}
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("dangerous OUT_DIR unexpectedly accepted: %s", output)
			}
		})
	}
	if got := readFile(t, sentinel); got != "preserve" {
		t.Fatalf("non-empty OUT_DIR was mutated: %q", got)
	}
	if info, err := os.Lstat(symlink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("OUT_DIR symlink was mutated: info=%v err=%v", info, err)
	}
}

func TestSingleNodeBaselineSourceIdentitySpansBuildAndSeal(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		"capture_source_state initial", "capture_source_state post_build", "capture_source_state final",
		"WKBENCH_BUILT_FROM_CURRENT_SOURCE", "WUKONGIM_BUILT_FROM_CURRENT_SOURCE",
		"binary_identity_only", "revision_and_binary_identity", `SOURCE_STATE_DIR="$OUT_DIR/source-state"`, `output="$SOURCE_STATE_DIR/$label.tsv"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("source rebuildability contract missing %q", want)
		}
	}
}

func TestSingleNodeBaselineBuildsOwnedWorkerFromCurrentSourceUnconditionally(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	start := strings.Index(script, "ensure_wkbench_binary() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("ensure_wkbench_binary function missing")
	}
	body := script[start : start+end]
	for _, forbidden := range []string{"find ", "-newer", `if [[ -x "$WK_BENCH_BIN" ]]`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("owned worker build still relies on incomplete stale-binary detection %q", forbidden)
		}
	}
	for _, want := range []string{`[[ "$START_WORKER" -eq 1 ]]`, `mktemp -d "$OUT_DIR/.wkbench-build.XXXXXX"`, "go build", "WKBENCH_BUILT_FROM_CURRENT_SOURCE=true"} {
		if !strings.Contains(body, want) {
			t.Fatalf("owned worker source build contract missing %q", want)
		}
	}
}

func TestSingleNodeBaselineStartsWorkerFromTheSealedOwnedBinary(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	start := strings.Index(script, "ensure_worker() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("ensure_worker function missing")
	}
	body := script[start : start+end]
	if strings.Contains(body, "ensure_wkbench_binary") {
		t.Fatal("ensure_worker must not rebuild after the tested wkbench binary was copied into the sealed bin directory")
	}
	for _, want := range []string{
		`prepare_sealed_test_binaries || die`,
		`WK_BENCH_BIN="$OUT_DIR/$SEALED_WKBENCH_RELATIVE"`,
		`"$WK_BENCH_BIN" worker`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("sealed owned worker binary contract missing %q", want)
		}
	}
}

func TestSingleNodeBaselineReusedWorkerCannotClaimCurrentSourceIdentity(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	start := strings.Index(script, "ensure_worker() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("ensure_worker function missing")
	}
	body := script[start : start+end]
	reused := strings.Index(body, "if worker_ready; then")
	identityOnly := strings.Index(body, "WKBENCH_BUILT_FROM_CURRENT_SOURCE=false")
	returned := strings.Index(body, "return")
	if reused < 0 || identityOnly < reused || returned < identityOnly {
		t.Fatal("a reused worker must force source identity to binary_identity_only before ensure_worker returns")
	}
}

func TestSingleNodeBaselinePreflightUsesFailClosedSharedOverlapDetector(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	start := strings.Index(script, "local_baseline_preflight() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("local_baseline_preflight function missing")
	}
	body := script[start : start+end]
	for _, want := range []string{"$HOST_OVERLAP_DETECTOR", "host_overlap_observation_failed", "overlapping_wukongim_workload"} {
		if !strings.Contains(body, want) {
			t.Fatalf("fail-closed shared overlap preflight missing %q", want)
		}
	}
	if strings.Contains(body, "ps -axo") {
		t.Fatal("single-node preflight must not duplicate the shared process detector")
	}
	disk := strings.Index(body, `capture_data_filesystem_observation "$OUT_DIR/filesystem-preflight.txt"`)
	overlap := strings.Index(body, `"$HOST_OVERLAP_DETECTOR"`)
	if disk < 0 || overlap < 0 || disk > overlap {
		t.Fatal("filesystem evidence must be captured before a host-overlap outcome is selected")
	}
}

func TestSingleNodeBaselineMarkerProjectsSealedFilesystemCompleteness(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	start := strings.Index(script, "write_local_baseline_result() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("write_local_baseline_result function missing")
	}
	body := script[start : start+end]
	for _, want := range []string{
		`.filesystem_observation_complete | select(type == "boolean")`,
		`"filesystem_observation_complete": %s`,
		`"canonical_data_dir": %s`,
		`"data_filesystem_device": %s`,
		`"data_filesystem_total_blocks": %s`,
		`"data_filesystem_block_size": %s`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sealed filesystem-completeness projection missing %q", want)
		}
	}
	if strings.Contains(body, "df -P") {
		t.Fatal("completion marker must not re-read live filesystem state")
	}
}

func TestSingleNodeTerminalLifecycleCaptureJoinsBackgroundWriterFirst(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	start := strings.Index(script, "run_attempt() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("run_attempt function missing")
	}
	body := script[start : start+end]
	stop := strings.Index(body, `stop_lifecycle_sampler`)
	terminalCapture := strings.Index(body, `capture_lifecycle_sample "$tag"`)
	if stop < 0 || terminalCapture < 0 || stop > terminalCapture {
		t.Fatal("background lifecycle sampler must be joined before the foreground stopped capture")
	}
}

func TestSingleNodeLifecycleProjectionRetainsReceiveHandoffCounters(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		`inner_recv_handoffs:(.lifecycle.receive_drain.inner_recv_handoffs // 0)`,
		`adapter_handoffs:(.lifecycle.receive_drain.adapter_handoffs // 0)`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("lifecycle projection missing receive ownership counter %q", want)
		}
	}
}

func TestSingleNodeBaselinePreflightUsesCommonSealedFinalizer(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		"finalize_local_preflight_result", "seal_local_binaries", "write_local_artifact_identity",
		"write_local_artifact_checksums", "verify_local_artifact_checksums",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("sealed preflight path missing %q", want)
		}
	}
	if strings.Contains(script, "log \"local baseline preflight result: $OUT_DIR/local-baseline.json\"\n    exit \"$preflight_status\"") {
		t.Fatal("preflight must not return before identity and checksum sealing")
	}
}

func TestSingleNodeBaselineBindsImmutableStepSummariesAndDataFilesystemIdentity(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "bench-wukongim-single-node-1000ch.sh"))
	for _, want := range []string{
		"write_immutable_step_summaries",
		`--storage-summary "$report_dir/evidence/storage-summary.tsv"`,
		`--host-io-summary "$report_dir/evidence/host-io-summary.tsv"`,
		"canonical_data_dir",
		"data_filesystem_device",
		"data_filesystem_total_blocks",
		"data_filesystem_block_size",
		`df -Pk "$observed_path"`,
		`WK_NODE_DATA_DIR="$SINGLE_NODE_DATA_DIR"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("single-node publication/data-filesystem contract missing %q", want)
		}
	}

	main := script[strings.LastIndex(script, "\nmain() {"):]
	run := strings.Index(main, `run_attempt "$qps"`)
	summaries := strings.Index(main, `write_immutable_step_summaries "$qps"`)
	checksums := strings.Index(main, `write_local_step_checksums "$qps"`)
	if run < 0 || summaries < 0 || checksums < 0 || !(run < summaries && summaries < checksums) {
		t.Fatal("each rate step must materialize immutable summaries before its checksum manifest")
	}
}
