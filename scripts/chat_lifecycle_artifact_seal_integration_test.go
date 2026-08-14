//go:build integration

package scripts_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleLocalBaselineRejectsIncompleteReproductionSeal(t *testing.T) {
	typedResult := `{
  "schema": "wukongim/chat-lifecycle-local-step/v1",
  "outcome": "product_failure",
  "reason": "terminal_product_failure_before_qualification",
  "offered_rate_per_second": 100,
  "actual_rate_per_second": 0,
  "minimum_throughput_percent": 90,
  "measured_duration_seconds": 120,
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
	for _, mode := range []string{"omit-effective-config", "omit-log", "omit-binary", "omit-identity"} {
		t.Run(mode, func(t *testing.T) {
			runDir, output, err := runLocalBaselineWithFakeStep(t, typedResult, 3, mode)
			requireLocalBaselineExitCode(t, err, output, 6)
			assertLocalBaselineRuntimeState(t, runDir, output, false)
		})
	}
}

func TestChatLifecycleLocalBaselineChecksumCoversFinalVerdict(t *testing.T) {
	runDir, output, err := runLocalBaselineWithFakeStep(t, "{", 0, "valid")
	requireLocalBaselineExitCode(t, err, output, 6)

	resultPath := filepath.Join(runDir, "local-baseline.json")
	result, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := fmt.Sprintf("%x  local-baseline.json", sha256.Sum256(result))
	manifest := readFile(t, filepath.Join(runDir, "checksums.sha256"))
	if !strings.Contains(manifest, want) {
		t.Fatalf("baseline checksum does not cover its final verdict; want %q in:\n%s", want, manifest)
	}
}

func TestChatLifecycleLocalBaselineAcceptsSealedGracefulStopTimeoutEvidenceAndStops(t *testing.T) {
	typedResult := `{
  "schema": "wukongim/chat-lifecycle-local-step/v1",
  "outcome": "insufficient_evidence",
  "reason": "coordinator_graceful_stop_timeout",
  "offered_rate_per_second": 100,
  "actual_rate_per_second": 0,
  "minimum_throughput_percent": 90,
  "measured_duration_seconds": 120,
  "qualification_reached": true,
  "target_connections": 2500,
  "online_connections": 2500,
  "sent": 101,
  "acknowledged": 97,
  "expected": 0,
  "minimum_filesystem_free_percent": 90,
  "storage_evidence_complete": false,
  "host_io_evidence_complete": false,
  "product_metrics_complete": false,
  "product_queue_evidence_complete": true,
  "product_queues_converged": false,
  "process_continuity_complete": true,
  "timeline_evidence_complete": false,
  "profile_evidence_complete": true,
  "operator_interrupted": false,
  "harness_failure_reason": "coordinator_graceful_stop_timeout"
}`
	runDir, output, err := runLocalBaselineWithFakeStep(t, typedResult, 6, "valid-timeout")
	requireLocalBaselineExitCode(t, err, output, 6)
	result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
	if !strings.Contains(result, `"outcome": "insufficient_evidence"`) ||
		!strings.Contains(result, `"validated_step_artifact_seals": 1`) {
		t.Fatalf("sealed timeout baseline result = %s\n%s", result, output)
	}
	steps := readFile(t, filepath.Join(runDir, "steps.tsv"))
	if strings.Count(strings.TrimSpace(steps), "\n") != 1 {
		t.Fatalf("timeout baseline continued its staircase:\n%s", steps)
	}
}

func TestChatLifecycleLocalBaselineRejectsTimeoutSnapshotThatContradictsTypedStatus(t *testing.T) {
	typedResult := `{
  "schema":"wukongim/chat-lifecycle-local-step/v1","outcome":"insufficient_evidence",
  "reason":"coordinator_graceful_stop_timeout","offered_rate_per_second":100,
  "actual_rate_per_second":0,"minimum_throughput_percent":90,"measured_duration_seconds":120,
  "qualification_reached":true,"target_connections":2500,"online_connections":2500,
  "sent":101,"acknowledged":97,"expected":0,"minimum_filesystem_free_percent":90,
  "storage_evidence_complete":false,"host_io_evidence_complete":false,"product_metrics_complete":false,
  "product_queue_evidence_complete":true,"product_queues_converged":false,
  "process_continuity_complete":true,"timeline_evidence_complete":false,"profile_evidence_complete":true,
  "operator_interrupted":false,"harness_failure_reason":"coordinator_graceful_stop_timeout"
}`
	runDir, output, err := runLocalBaselineWithFakeStep(t, typedResult, 6, "invalid-timeout-snapshot")
	requireLocalBaselineExitCode(t, err, output, 6)
	result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
	if !strings.Contains(result, `"validated_step_artifact_seals": 0`) {
		t.Fatalf("contradictory timeout snapshot was accepted: %s\n%s", result, output)
	}
	assertLocalBaselineRuntimeState(t, runDir, output, false)
}

func TestChatLifecycleLocalBaselineRetainsDirtySourceBinary(t *testing.T) {
	typedResult := `{
  "schema": "wukongim/chat-lifecycle-local-step/v1",
  "outcome": "product_failure",
  "reason": "terminal_product_failure_before_qualification",
  "offered_rate_per_second": 100,
  "actual_rate_per_second": 0,
  "minimum_throughput_percent": 90,
  "measured_duration_seconds": 120,
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
	runDir, output, err := runLocalBaselineWithFakeStep(t, typedResult, 3, "valid-dirty")
	requireLocalBaselineExitCode(t, err, output, 3)
	assertLocalBaselineRuntimeState(t, runDir, output, false)
	result := readFile(t, filepath.Join(runDir, "local-baseline.json"))
	if !strings.Contains(result, `"source_rebuildable_from_revision": false`) {
		t.Fatalf("dirty-source baseline claimed revision rebuildability: %s", result)
	}
}
