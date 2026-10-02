//go:build e2e

package send_ban

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPermissionCallerFixedLoad supplements the historical characterization
// with predeclared arrival, observation and CPU windows. The historical scaffold
// first produced a retained real-process red receipt before the opt-in fixed
// driver was implemented; historical characterization remains separate.
func TestPermissionCallerFixedLoad(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_FIXED_LOAD") != "1" {
		t.Skip("opt-in fixed permission arrival and observation diagnostic")
	}
	path := os.Getenv("WK_E2E_PERMISSION_FIXED_REPORT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "permission-fixed-load.json")
	}
	cohorts := os.Getenv("WK_E2E_PERMISSION_FIXED_COHORTS") == "1"
	if cohorts {
		t.Setenv("WK_E2E_PERMISSION_COHORT_REPORT", path)
	} else {
		t.Setenv("WK_E2E_PERMISSION_BASELINE_REPORT", path)
	}
	runPermissionCallerExperiment(t, cohorts)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var report map[string]any
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Equal(t, "permission-fixed-load/v1", report["fixed_load_protocol"], "historical elapsed-length windows do not implement the fixed protocol")
	require.EqualValues(t, 32000, report["fixed_cpu_window_ms"])
	require.EqualValues(t, 30000, report["fixed_arrival_window_ms"])
	cases, ok := report["cases"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, cases)
	for _, rawCase := range cases {
		one := rawCase.(map[string]any)
		for _, rawWindow := range one["windows"].([]any) {
			window := rawWindow.(map[string]any)
			want := 750
			if window["concurrency"].(float64) == 32 {
				want = 14976
			}
			require.EqualValues(t, want, window["planned_messages"])
			require.Len(t, window["acks"], want)
			require.Len(t, window["cohort_ownership_samples"], 32)
			require.EqualValues(t, 0, window["late_arrivals"])
			require.EqualValues(t, 0, window["dropped_arrivals"])
			require.EqualValues(t, 0, window["unfinished_arrivals"])
			require.Equal(t, true, window["fixed_protocol_passed"])
		}
	}
}
