//go:build e2e

package send_ban

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPermissionSequentialPrefixDiagnostics preserves all original placements
// before capturing only the two-Slot, same-remote-Leader sequential case.
func TestPermissionSequentialPrefixDiagnostics(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_PREFIX_DIAGNOSTICS") != "1" {
		t.Skip("opt-in full-prefix sequential diagnostics")
	}
	profiles := os.Getenv("WK_E2E_PERMISSION_SEQUENTIAL_PROFILES") == "1"
	timeline := os.Getenv("WK_E2E_PERMISSION_TIMELINE_SEQUENTIAL") == "1"
	require.NotEqual(t, profiles, timeline)
	t.Setenv("WK_E2E_PERMISSION_DIAGNOSTIC_PLACEMENT", "two-slots-one-remote-leader")
	if timeline {
		t.Setenv("WK_E2E_PERMISSION_TIMELINE", "1")
	}
	cohorts := os.Getenv("WK_E2E_PERMISSION_COHORTS") == "1"
	path := os.Getenv("WK_E2E_PERMISSION_BASELINE_REPORT")
	if cohorts {
		path = os.Getenv("WK_E2E_PERMISSION_COHORT_REPORT")
	}
	require.NotEmpty(t, path)
	runPermissionCallerExperiment(t, cohorts)
	// Coverage is checked after the fixture writes its independent functional,
	// policy and exact-history receipt, even if diagnostics are unavailable.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var report struct {
		Cases []struct {
			Name    string `json:"name"`
			Windows []struct {
				Concurrency int               `json:"concurrency"`
				Timelines   []json.RawMessage `json:"request_timelines"`
			} `json:"windows"`
			History  []string                      `json:"exact_history"`
			Acks     []permissionBaselineAck       `json:"sequential_profile_acks"`
			Profiles []permissionSequentialProfile `json:"sequential_profiles"`
			Samples  []json.RawMessage             `json:"sequential_profile_ownership_samples"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &report))
	names := []string{"same-slot-remote", "two-slots-one-remote-leader", "two-remote-leaders", "two-slots-local-leader"}
	require.Len(t, report.Cases, len(names), "retain the complete original placement prefix")
	for i, c := range report.Cases {
		require.Equal(t, names[i], c.Name)
		require.Len(t, c.Windows, 2)
		require.Equal(t, 1, c.Windows[0].Concurrency)
		require.Equal(t, 32, c.Windows[1].Concurrency)
		target := c.Name == "two-slots-one-remote-leader"
		history := 161
		if profiles && target {
			history = 417
			require.Len(t, c.Acks, 256)
			require.Len(t, c.Profiles, 6)
			seen := make(map[uint64]map[string]bool)
			for _, p := range c.Profiles {
				require.Contains(t, []uint64{1, 2, 3}, p.NodeID)
				require.Contains(t, []string{"cpu", "allocs"}, p.Kind)
				require.Equal(t, 4, p.Seconds)
				require.Empty(t, p.ErrorCode)
				require.False(t, p.Requested.IsZero())
				require.True(t, p.Returned.After(p.Requested))
				require.LessOrEqual(t, p.Returned.Sub(p.Requested), 8*time.Second)
				if seen[p.NodeID] == nil {
					seen[p.NodeID] = make(map[string]bool)
				}
				require.False(t, seen[p.NodeID][p.Kind])
				seen[p.NodeID][p.Kind] = true
				info, err := os.Stat(p.Path)
				require.NoError(t, err)
				require.Positive(t, info.Size())
				require.LessOrEqual(t, info.Size(), int64(8<<20))
			}
			if os.Getenv("WK_E2E_PERMISSION_PROFILE_OWNERSHIP") == "1" {
				require.NotEmpty(t, c.Samples)
				require.LessOrEqual(t, len(c.Samples), 200)
			} else {
				require.Empty(t, c.Samples)
			}
		} else {
			require.Empty(t, c.Profiles)
			require.Empty(t, c.Acks)
		}
		require.Len(t, c.History, history)
		if timeline && target {
			require.Len(t, c.Windows[0].Timelines, 64)
		} else {
			require.Empty(t, c.Windows[0].Timelines)
		}
		require.Empty(t, c.Windows[1].Timelines)
	}
}
