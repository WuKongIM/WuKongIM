//go:build e2e

package send_ban

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPermissionCallerFixedLoadV2 first requires the new receipt contract from
// the original real-process V1 path; root retains that red before implementation.
func TestPermissionCallerFixedLoadV2(t *testing.T) {
	red := os.Getenv("WK_E2E_PERMISSION_FIXED_V2_RED") == "1"
	if !red && os.Getenv("WK_E2E_PERMISSION_FIXED_LOAD_V2") != "1" {
		t.Skip("opt-in fixed-load v2 process receipt contract")
	}
	path := os.Getenv("WK_E2E_PERMISSION_FIXED_V2_REPORT")
	require.True(t, filepath.IsAbs(path), "use a new absolute v2 report path")
	for _, artifact := range []string{path, path + ".v2-contract.json"} {
		_, err := os.Stat(artifact)
		require.True(t, os.IsNotExist(err), "preserve existing receipt: %s", artifact)
	}
	cohorts := os.Getenv("WK_E2E_PERMISSION_FIXED_COHORTS") == "1"
	t.Setenv("WK_E2E_PERMISSION_FIXED_LOAD", "1")
	if red {
		t.Setenv("WK_E2E_PERMISSION_FIXED_LOAD_V2", "")
	}
	t.Setenv("WK_E2E_PERMISSION_FIXED_REPORT", path)
	if cohorts {
		t.Setenv("WK_E2E_PERMISSION_COHORT_REPORT", path)
	} else {
		t.Setenv("WK_E2E_PERMISSION_BASELINE_REPORT", path)
	}
	// This defer also runs after fatal process/CPU evidence failures, after the
	// scaffold's own deferred receipt, and preserves every v2 schema rejection.
	defer permissionFixedV2ReceiptContract(t, path, red)
	runPermissionCallerExperiment(t, cohorts)
}

func permissionFixedV2ReceiptContract(t *testing.T, path string, red bool) {
	t.Helper()
	var failures []string
	check := func(ok bool, reason string) {
		if !ok {
			failures = append(failures, reason)
		}
	}
	raw, readErr := os.ReadFile(path)
	check(readErr == nil, fmt.Sprintf("source_receipt_read: %v", readErr))
	var report map[string]any
	parseErr := json.Unmarshal(raw, &report)
	check(parseErr == nil, fmt.Sprintf("source_receipt_json: %v", parseErr))
	check(report["fixed_load_protocol"] == "permission-fixed-load/v2", "v2_protocol_missing")
	configs, _ := report["node_configs"].(map[string]any)
	check(len(configs) == 3, "three_node_configs_required")
	for node, value := range configs {
		config, _ := value.(map[string]any)
		for _, pair := range [][2]string{{"retained_config_path", "retained_config_sha256"}, {"canonical_config_path", "canonical_config_sha256"}} {
			file, _ := config[pair[0]].(string)
			body, err := os.ReadFile(file)
			check(err == nil && len(body) > 0 && fmt.Sprintf("%x", sha256.Sum256(body)) == config[pair[1]], "retained_config_bytes_missing_or_invalid: node="+node+" "+pair[0])
		}
		rules, _ := config["normalization_rules"].([]any)
		check(len(rules) > 0, "explicit_config_normalization_required: node="+node)
	}
	cases, _ := report["cases"].([]any)
	check(len(cases) > 0, "real_process_case_required")
	for _, value := range cases {
		one, _ := value.(map[string]any)
		windows, _ := one["windows"].([]any)
		check(len(windows) == 2, "two_real_windows_required")
		for _, value := range windows {
			window, _ := value.(map[string]any)
			label := fmt.Sprintf("case=%v concurrency=%v", one["name"], window["concurrency"])
			check(window["caller_queue_capacity"] == float64(8) && window["caller_active_limit"] == float64(1), "bounded_v2_fifo_required: "+label)
			check(window["scheduled_to_worker_limit_ns"] == float64(400000000), "scheduled_expiry_400ms_required: "+label)
			check(window["cpu_valid"] == true, "actual_monotonic_native_cpu_valid_required: "+label)
			for _, field := range []string{"cpu_before_call_started_offset_ns", "cpu_before_call_finished_offset_ns", "cpu_after_call_started_offset_ns", "cpu_after_call_finished_offset_ns", "queue_wait_p99_ns", "scheduled_to_completed_p99_ns"} {
				_, ok := window[field].(float64)
				check(ok, "v2_monotonic_field_required: "+label+" "+field)
			}
			arrivals, _ := window["arrival_schedule"].([]any)
			missing := 0
			for _, value := range arrivals {
				arrival, _ := value.(map[string]any)
				for _, field := range []string{"scheduled_offset_ns", "offered_offset_ns", "worker_started_offset_ns", "completed_offset_ns", "queue_wait_ns", "scheduled_to_completed_ns"} {
					if _, ok := arrival[field].(float64); !ok {
						missing++
						break
					}
				}
			}
			check(len(arrivals) > 0 && missing == 0, fmt.Sprintf("all_arrival_monotonic_phases_required: %s missing=%d", label, missing))
			samples, _ := window["cohort_ownership_samples"].([]any)
			check(len(samples) == 32, "exact_32_full_raw_samples_required: "+label)
			for _, value := range samples {
				sample, _ := value.(map[string]any)
				for _, field := range []string{"scheduled_offset_ns", "started_offset_ns", "network_finished_offset_ns", "analysis_started_offset_ns", "analysis_finished_offset_ns", "retention_started_offset_ns", "retention_finished_offset_ns"} {
					_, ok := sample[field].(float64)
					check(ok, "sample_monotonic_phase_required: "+label+" "+field)
				}
				response, _ := sample["response"].(map[string]any)
				check(sample["valid"] == true && response["network_completed"] == true && response["validated"] == true && response["wire_retained"] == true, "full_raw_sample_validation_required: "+label)
			}
		}
	}
	companion := map[string]any{"expected_protocol": "permission-fixed-load/v2", "observed_protocol": report["fixed_load_protocol"], "red_against_v1": red,
		"source_report": path, "source_report_sha256": fmt.Sprintf("%x", sha256.Sum256(raw)), "schema_errors": failures, "passed": len(failures) == 0 && !t.Failed()}
	body, err := json.MarshalIndent(companion, "", "  ")
	if err == nil {
		err = os.WriteFile(path+".v2-contract.json", append(body, '\n'), 0644)
	}
	if err != nil {
		t.Errorf("v2 contract companion: %v", err)
	}
	for _, reason := range failures {
		t.Error(reason)
	}
}
