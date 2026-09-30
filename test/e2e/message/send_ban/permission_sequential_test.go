//go:build e2e

package send_ban

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPermissionSequentialDiagnostics requires actual public observations for
// the sequential window. It never substitutes profiled timings for acceptance.
func TestPermissionSequentialDiagnostics(t *testing.T) {
	timeline := os.Getenv("WK_E2E_PERMISSION_TIMELINE_SEQUENTIAL") == "1"
	profiles := os.Getenv("WK_E2E_PERMISSION_SEQUENTIAL_PROFILES") == "1"
	if !timeline && !profiles {
		t.Skip("opt-in sequential permission diagnostics")
	}
	require.NotEqual(t, timeline, profiles, "collect timeline and profiles in separate runs")
	cohorts := os.Getenv("WK_E2E_PERMISSION_COHORTS") == "1"
	path := os.Getenv("WK_E2E_PERMISSION_BASELINE_REPORT")
	if cohorts {
		path = os.Getenv("WK_E2E_PERMISSION_COHORT_REPORT")
	}
	require.NotEmpty(t, path)
	if timeline {
		t.Setenv("WK_E2E_PERMISSION_TIMELINE", "1")
	}
	runPermissionCallerExperiment(t, cohorts)
	// The original experiment writes functional/policy/history evidence even
	// when this independent diagnostics coverage assertion fails afterward.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var report struct {
		Cases []struct {
			Windows []struct {
				Concurrency int               `json:"concurrency"`
				Timelines   []json.RawMessage `json:"request_timelines"`
			} `json:"windows"`
			Profiles []struct {
				NodeID uint64 `json:"node_id"`
				Kind   string `json:"kind"`
				Path   string `json:"path"`
				Error  string `json:"error_code"`
			} `json:"sequential_profiles"`
			History []json.RawMessage `json:"exact_history"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Len(t, report.Cases, 1, "select only same-slot-remote")
	if timeline {
		require.Len(t, report.Cases[0].Windows, 2)
		require.Equal(t, 1, report.Cases[0].Windows[0].Concurrency)
		require.Len(t, report.Cases[0].Windows[0].Timelines, 64)
		require.Empty(t, report.Cases[0].Windows[1].Timelines)
	}
	if profiles {
		require.Len(t, report.Cases[0].History, 417, "profile traffic must retain exact committed history")
		require.Len(t, report.Cases[0].Profiles, 6, "CPU and allocation profiles for three owned nodes")
		identities := make(map[string]bool, 6)
		for _, p := range report.Cases[0].Profiles {
			require.Contains(t, []uint64{1, 2, 3}, p.NodeID)
			require.Contains(t, []string{"cpu", "allocs"}, p.Kind)
			require.Empty(t, p.Error)
			require.NotEmpty(t, p.Path)
			key := fmt.Sprintf("%d/%s", p.NodeID, p.Kind)
			require.False(t, identities[key], "profiles must not overwrite each other")
			identities[key] = true
			info, err := os.Stat(p.Path)
			require.NoError(t, err)
			require.Greater(t, info.Size(), int64(0))
			require.LessOrEqual(t, info.Size(), int64(8<<20))
		}
	}
}
