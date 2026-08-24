//go:build integration

package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestChatLifecycleLocalBaselinePreservesRuntimeStateWhenTypedResultIsInvalid(t *testing.T) {
	runDir, output, err := runLocalBaselineWithFakeStep(t, "{", 0, "valid")
	requireLocalBaselineExitCode(t, err, output, 6)
	assertLocalBaselineRuntimeState(t, runDir, output, false)
	result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
	if !strings.Contains(result, `"outcome": "insufficient_evidence"`) {
		t.Fatalf("invalid typed result baseline = %s", result)
	}
}

func TestChatLifecycleLocalBaselinePrunesOnlyAfterValidatedStepEvidence(t *testing.T) {
	validTypedResult := `{
  "schema": "wukongim/chat-lifecycle-local-step/v1",
  "outcome": "product_failure",
  "reason": "terminal_product_failure_before_qualification",
  "offered_rate_per_second": 250,
  "actual_rate_per_second": 0,
  "minimum_throughput_percent": 90,
  "measured_duration_seconds": 300,
  "qualification_reached": false,
  "target_connections": 2500,
  "online_connections": 0,
  "sent": 100,
  "acknowledged": 0,
  "expected": 0,
  "minimum_filesystem_free_percent": 90,
  "storage_evidence_complete": false,
  "host_io_evidence_complete": false,
  "product_metrics_complete": false,
  "product_queue_evidence_complete": false,
  "product_queues_converged": false,
  "process_continuity_complete": true,
  "timeline_evidence_complete": true,
  "profile_evidence_complete": true,
  "operator_interrupted": false,
  "harness_failure_reason": ""
}`
	tests := []struct {
		name         string
		status       int
		checksumMode string
		wantExit     int
		wantPruned   bool
	}{
		{name: "validated result and every checksum", status: 3, checksumMode: "valid", wantExit: 3, wantPruned: true},
		{name: "outcome status mismatch", status: 0, checksumMode: "valid", wantExit: 6},
		{name: "missing checksum manifest", status: 3, checksumMode: "missing", wantExit: 6},
		{name: "later evidence checksum mismatch", status: 3, checksumMode: "corrupt-evidence", wantExit: 6},
		{name: "checksum manifest omits typed result", status: 3, checksumMode: "omit-result", wantExit: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runDir, output, err := runLocalBaselineWithFakeStep(t, validTypedResult, test.status, test.checksumMode)
			requireLocalBaselineExitCode(t, err, output, test.wantExit)
			assertLocalBaselineRuntimeState(t, runDir, output, test.wantPruned)
			result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
			wantOutcome := `"outcome": "insufficient_evidence"`
			if test.wantPruned {
				wantOutcome = `"outcome": "product_failure"`
			}
			if !strings.Contains(result, wantOutcome) {
				t.Fatalf("baseline result missing %s: %s", wantOutcome, result)
			}
		})
	}
}

func TestChatLifecycleLocalBaselineRunsFixedStaircaseThenRequired1000Soak(t *testing.T) {
	typedResult := `{
  "schema": "wukongim/chat-lifecycle-local-step/v1",
  "outcome": "clean",
  "reason": "clean",
  "offered_rate_per_second": 250,
  "actual_rate_per_second": 250,
  "minimum_throughput_percent": 90,
  "measured_duration_seconds": 300,
  "qualification_reached": true,
  "target_connections": 2500,
  "online_connections": 2500,
  "sent": 75000,
  "acknowledged": 75000,
  "expected": 75000,
  "minimum_filesystem_free_percent": 10,
  "storage_evidence_complete": true,
  "host_io_evidence_complete": true,
  "product_metrics_complete": true,
  "product_queue_evidence_complete": true,
  "product_queues_converged": true,
  "process_continuity_complete": true,
  "timeline_evidence_complete": true,
  "profile_evidence_complete": true,
  "operator_interrupted": false,
  "harness_failure_reason": ""
}`
	callLog := filepath.Join(t.TempDir(), "calls.tsv")
	runDir, output, err := runLocalBaselineWithFakeStepEnv(t, typedResult, 0, "valid", []string{
		"FAKE_DYNAMIC_TYPED_RESULT=1",
		"FAKE_CALL_LOG=" + callLog,
	})
	if err != nil {
		t.Fatalf("local baseline error = %v\n%s", err, output)
	}
	if got, want := readFile(t, callLog), "250\t300\n500\t300\n750\t300\n1000\t300\n1000\t600\n"; got != want {
		t.Fatalf("shakeout calls = %q, want %q", got, want)
	}
	result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
	for _, want := range []string{`"outcome": "clean"`, `"reason": "required_1000_soak_passed"`, `"highest_clean_rate": 1000`, `"soak_rate": 1000`} {
		if !strings.Contains(result, want) {
			t.Fatalf("baseline result missing %s: %s", want, result)
		}
	}
}

func runLocalBaselineWithFakeStep(t *testing.T, typedResult string, status int, checksumMode string) (string, []byte, error) {
	return runLocalBaselineWithFakeStepEnv(t, typedResult, status, checksumMode, nil)
}

func runLocalBaselineWithFakeStepEnv(t *testing.T, typedResult string, status int, checksumMode string, extraEnv []string) (string, []byte, error) {
	t.Helper()
	root := repoRoot(t)
	testRoot := t.TempDir()
	scriptsDir := filepath.Join(testRoot, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	baselinePath := filepath.Join(scriptsDir, "run-wukongim-three-node-chat-lifecycle-local-baseline.sh")
	if err := os.WriteFile(baselinePath, []byte(readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-local-baseline.sh"))), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeShakeout := `#!/usr/bin/env bash
set -euo pipefail
run_dir=""
send_rate=""
measure_seconds=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-dir) run_dir="$2"; shift 2 ;;
	--send-rate) send_rate="$2"; shift 2 ;;
	--measure-seconds) measure_seconds="$2"; shift 2 ;;
    *) shift ;;
  esac
done
	if [[ -n "${FAKE_CALL_LOG:-}" ]]; then
	  printf '%s\t%s\n' "$send_rate" "$measure_seconds" >>"$FAKE_CALL_LOG"
	fi
	mkdir -p "$run_dir/bin" "$run_dir/config" "$run_dir/data" "$run_dir/workers" "$run_dir/evidence" "$run_dir/logs"
printf 'binary\n' >"$run_dir/bin/wukongim"
printf 'benchmark binary\n' >"$run_dir/bin/wkbench"
printf 'database\n' >"$run_dir/data/node.db"
printf 'worker\n' >"$run_dir/workers/state"
	local_step_json="$FAKE_LOCAL_STEP_JSON"
	if [[ "${FAKE_DYNAMIC_TYPED_RESULT:-0}" == 1 ]]; then
	  local_step_json="$(printf '%s\n' "$local_step_json" | jq -c --argjson rate "$send_rate" --argjson measured "$measure_seconds" '
	    .offered_rate_per_second = $rate |
	    .actual_rate_per_second = $rate |
	    .measured_duration_seconds = $measured |
	    .sent = ($rate * $measured) |
	    .acknowledged = .sent |
	    .expected = .sent
	  ')"
	fi
	printf '%s\n' "$local_step_json" >"$run_dir/local-step.json"
	printf 'run_id: local-chat-lifecycle-shakeout\n' >"$run_dir/chat-lifecycle.yaml"
	for node in 1 2 3; do
	  printf 'node_id = %s\n' "$node" >"$run_dir/config/node$node.toml"
	  printf 'service log %s\n' "$node" >"$run_dir/logs/service-$node.log"
	  printf 'worker log %s\n' "$node" >"$run_dir/logs/worker-$node.log"
	  printf 'host log %s\n' "$node" >"$run_dir/logs/host-metrics-$node.log"
	done
	printf 'coordinator log\n' >"$run_dir/logs/coordinator.log"
	printf 'load host log\n' >"$run_dir/logs/host-metrics-load.log"
	printf 'process log\n' >"$run_dir/logs/process-metrics.log"
	printf 'diagnostic\n' >"$run_dir/evidence/diagnostic.txt"
	printf '{"schema":"wukongim/chat-lifecycle-unified-timeline/v1"}\n' >"$run_dir/evidence/unified-timeline.json"
	printf 'observed_at_utc\tphase\n' >"$run_dir/evidence/unified-timeline.tsv"
	printf 'observed_at_utc\trun_id\tsample\tnode\tstatus\tcompaction_count\tcompactions_in_progress\tsnapshot_files\tsnapshot_bytes\tsnapshot_identity\tsnapshot_inventory\n' >"$run_dir/evidence/storage-overlap.tsv"
	printf '{"event":"wkbench.chat_lifecycle.worker_status_cut"}\n' >"$run_dir/evidence/coordinator-worker-cuts.log"
	printf '{"schema":"wukongim/chat-lifecycle-threshold-pprof-status/v1","status":"not_triggered"}\n' >"$run_dir/evidence/threshold-pprof-status.json"
	harness_failure_reason="$(printf '%s\n' "$local_step_json" | jq -r '.harness_failure_reason // ""' 2>/dev/null || true)"
	if [[ "$harness_failure_reason" == coordinator_graceful_stop_timeout ]]; then
	  mkdir -p "$run_dir/evidence/graceful-stop-timeout"
	  for node in 1 2 3; do
	    worker_id=$((node - 1))
	    printf '{"run_id":"local-chat-lifecycle-shakeout","worker_id":%s,"phase":"stopping","sessions":{"online":17,"starting":2,"closing":3},"messages":{"sent":101,"send_acknowledged":97,"retry_attempts":4,"terminal":1},"correlation":{"pending_unfinished":4,"outstanding":3},"queues":{"work_current":6,"retry_current":4,"inflight_current":3,"transport_current":2}}\n' "$worker_id" \
	      >"$run_dir/evidence/graceful-stop-timeout/node-$node.json"
	  done
	  printf '%s\n' '{"schema":"wukongim/chat-lifecycle-graceful-stop-status/v1","status":"timeout","reason":"coordinator_graceful_stop_timeout","observed_at_utc":"2026-08-13T00:00:00Z","timeout_seconds":90,"terminal_cut_present":false,"evidence_complete":true,"nodes":[{"node":"node-1","capture_status":"complete","snapshot":"graceful-stop-timeout/node-1.json","phase":"stopping","sessions":{"online":17,"starting":2,"closing":3},"messages":{"sent":101,"send_acknowledged":97,"retry_attempts":4,"terminal":1},"remaining_work":{"pending_unfinished":4,"outstanding":3,"work_current":6,"retry_current":4,"inflight_current":3,"transport_current":2}},{"node":"node-2","capture_status":"complete","snapshot":"graceful-stop-timeout/node-2.json","phase":"stopping","sessions":{"online":17,"starting":2,"closing":3},"messages":{"sent":101,"send_acknowledged":97,"retry_attempts":4,"terminal":1},"remaining_work":{"pending_unfinished":4,"outstanding":3,"work_current":6,"retry_current":4,"inflight_current":3,"transport_current":2}},{"node":"node-3","capture_status":"complete","snapshot":"graceful-stop-timeout/node-3.json","phase":"stopping","sessions":{"online":17,"starting":2,"closing":3},"messages":{"sent":101,"send_acknowledged":97,"retry_attempts":4,"terminal":1},"remaining_work":{"pending_unfinished":4,"outstanding":3,"work_current":6,"retry_current":4,"inflight_current":3,"transport_current":2}}]}' \
	    >"$run_dir/evidence/graceful-stop-status.json"
	  if [[ "$FAKE_CHECKSUM_MODE" == invalid-timeout-snapshot ]]; then
	    jq '.messages.sent = 999' "$run_dir/evidence/graceful-stop-timeout/node-1.json" \
	      >"$run_dir/evidence/graceful-stop-timeout/node-1.json.next"
	    mv "$run_dir/evidence/graceful-stop-timeout/node-1.json.next" \
	      "$run_dir/evidence/graceful-stop-timeout/node-1.json"
	  fi
	else
	  printf '{"schema":"wukongim/chat-lifecycle-graceful-stop-status/v1","status":"not_triggered","reason":"","observed_at_utc":"","timeout_seconds":0,"terminal_cut_present":false,"evidence_complete":true,"nodes":[]}\n' >"$run_dir/evidence/graceful-stop-status.json"
	fi
	sha256_file() {
	  if command -v sha256sum >/dev/null 2>&1; then
	    sha256sum "$1" | awk '{print $1}'
	  else
	    shasum -a 256 "$1" | awk '{print $1}'
	  fi
	}
	config_digest="$(sha256_file "$run_dir/chat-lifecycle.yaml")"
	wukongim_digest="$(sha256_file "$run_dir/bin/wukongim")"
	wkbench_digest="$(sha256_file "$run_dir/bin/wkbench")"
	source_dirty=false
	source_rebuildable=true
	source_capture=git_revision
	if [[ "$FAKE_CHECKSUM_MODE" == valid-dirty ]]; then
	  source_dirty=true
	  source_rebuildable=false
	  source_capture=binary_identity_only
	fi
	printf 'schema\twukongim/chat-lifecycle-local-evidence/v1\nsource_revision\t0123456789abcdef0123456789abcdef01234567\nsource_dirty\t%s\nsource_rebuildable_from_revision\t%s\nsource_capture\t%s\nconfig_sha256\t%s\nwukongim_binary_sha256\t%s\nwkbench_binary_sha256\t%s\n' \
	  "$source_dirty" "$source_rebuildable" "$source_capture" "$config_digest" "$wukongim_digest" "$wkbench_digest" >"$run_dir/evidence/identity.tsv"
	append_checksum() {
	  local relative="$1" digest
	  digest="$(sha256_file "$run_dir/$relative")"
	  if [[ "$FAKE_CHECKSUM_MODE" == corrupt-evidence && "$relative" == evidence/diagnostic.txt ]]; then
	    digest="$(printf '0%.0s' {1..64})"
	  fi
	  printf '%s  %s\n' "$digest" "$relative" >>"$run_dir/evidence/checksums.sha256"
	}
case "$FAKE_CHECKSUM_MODE" in
  valid|valid-dirty|valid-timeout|invalid-timeout-snapshot|corrupt-evidence|omit-effective-config|omit-log|omit-binary|omit-identity)
	    : >"$run_dir/evidence/checksums.sha256"
	    for relative in \
	      local-step.json chat-lifecycle.yaml \
	      bin/wukongim bin/wkbench \
	      config/node1.toml config/node2.toml config/node3.toml \
	      logs/coordinator.log logs/service-1.log logs/service-2.log logs/service-3.log \
	      logs/worker-1.log logs/worker-2.log logs/worker-3.log \
	      logs/host-metrics-1.log logs/host-metrics-2.log logs/host-metrics-3.log \
	      logs/host-metrics-load.log logs/process-metrics.log \
	      evidence/identity.tsv evidence/diagnostic.txt evidence/unified-timeline.json \
	      evidence/unified-timeline.tsv evidence/storage-overlap.tsv evidence/threshold-pprof-status.json evidence/graceful-stop-status.json \
	      evidence/coordinator-worker-cuts.log; do
	      case "$FAKE_CHECKSUM_MODE:$relative" in
	        omit-effective-config:chat-lifecycle.yaml|omit-log:logs/coordinator.log|omit-binary:bin/wukongim|omit-identity:evidence/identity.tsv) continue ;;
	      esac
	      append_checksum "$relative"
	    done
	    if [[ "$FAKE_CHECKSUM_MODE" == valid-timeout || "$FAKE_CHECKSUM_MODE" == invalid-timeout-snapshot ]]; then
	      for node in 1 2 3; do
	        append_checksum "evidence/graceful-stop-timeout/node-$node.json"
	      done
	    fi
	    ;;
  omit-result)
	    : >"$run_dir/evidence/checksums.sha256"
	    append_checksum evidence/diagnostic.txt
    ;;
  missing) ;;
  *) exit 99 ;;
esac
exit "${FAKE_SHAKEOUT_STATUS:-0}"
`
	shakeoutPath := filepath.Join(scriptsDir, "run-wukongim-three-node-chat-lifecycle-shakeout.sh")
	if err := os.WriteFile(shakeoutPath, []byte(fakeShakeout), 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(testRoot, "test-bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "df"), []byte("#!/usr/bin/env bash\nprintf 'Filesystem 1024-blocks Used Available Capacity Mounted on\\nfake 100000 10000 90000 10%% /\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(testRoot, "baseline-run")
	command := exec.Command("bash", baselinePath, "--run-dir", runDir, "--base-port", "25000")
	command.Dir = testRoot
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_API_TOKEN=test-api-token",
		"WK_BENCH_WORKER_TOKEN=test-worker-token",
		"FAKE_LOCAL_STEP_JSON="+typedResult,
		"FAKE_SHAKEOUT_STATUS="+strconv.Itoa(status),
		"FAKE_CHECKSUM_MODE="+checksumMode,
	)
	command.Env = append(command.Env, extraEnv...)
	output, err := command.CombinedOutput()
	return runDir, output, err
}

func requireLocalBaselineExitCode(t *testing.T, err error, output []byte, want int) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != want {
		t.Fatalf("local baseline exit = %v, want %d\n%s", err, want, output)
	}
}

func assertLocalBaselineRuntimeState(t *testing.T, runDir string, output []byte, wantPruned bool) {
	t.Helper()
	stepDir := filepath.Join(runDir, "steps", "step-rate-250")
	for _, path := range []string{"bin/wukongim", "data/node.db", "workers/state"} {
		_, statErr := os.Stat(filepath.Join(stepDir, path))
		if wantPruned && !os.IsNotExist(statErr) {
			t.Fatalf("validated step retained %s: %v\n%s", path, statErr, output)
		}
		if !wantPruned && statErr != nil {
			t.Fatalf("invalid step did not preserve %s: %v\n%s", path, statErr, output)
		}
	}
	_, markerErr := os.Stat(filepath.Join(stepDir, "runtime-state-pruned.txt"))
	if wantPruned && markerErr != nil {
		t.Fatalf("validated step missing prune marker: %v\n%s", markerErr, output)
	}
	if !wantPruned && !os.IsNotExist(markerErr) {
		t.Fatalf("invalid step wrote prune marker: %v\n%s", markerErr, output)
	}
}
