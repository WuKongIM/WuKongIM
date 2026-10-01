//go:build e2e

package send_ban

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPermissionQuorumTracePrefix preserves the complete original fixture and
// requires three joined, bounded public traces around its selected window.
func TestPermissionQuorumTracePrefix(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_QUORUM_TRACE") != "1" {
		t.Skip("opt-in quorum boundary diagnosis")
	}
	cohorts := os.Getenv("WK_E2E_PERMISSION_COHORTS") == "1"
	path := os.Getenv("WK_E2E_PERMISSION_BASELINE_REPORT")
	if cohorts {
		path = os.Getenv("WK_E2E_PERMISSION_COHORT_REPORT")
	}
	require.NotEmpty(t, path)
	runPermissionCallerExperiment(t, cohorts)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var report struct {
		Cases []struct {
			Name    string   `json:"name"`
			History []string `json:"exact_history"`
			Windows []struct {
				Traces []permissionSequentialProfile `json:"quorum_traces"`
			} `json:"windows"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Len(t, report.Cases, 4)
	for i, c := range report.Cases {
		require.Equal(t, []string{"same-slot-remote", "two-slots-one-remote-leader", "two-remote-leaders", "two-slots-local-leader"}[i], c.Name)
		require.Len(t, c.History, 161)
		require.Len(t, c.Windows, 2)
		for w, window := range c.Windows {
			if i != 1 || w != 0 {
				require.Empty(t, window.Traces)
				continue
			}
			require.Len(t, window.Traces, 3)
			seen := map[uint64]bool{}
			for _, p := range window.Traces {
				require.Contains(t, []uint64{1, 2, 3}, p.NodeID)
				require.False(t, seen[p.NodeID])
				seen[p.NodeID] = true
				require.Equal(t, "trace", p.Kind)
				require.Equal(t, 4, p.Seconds)
				require.Empty(t, p.ErrorCode)
				require.True(t, p.Returned.After(p.Requested))
				require.LessOrEqual(t, p.Returned.Sub(p.Requested), 6*time.Second)
				info, err := os.Stat(p.Path)
				require.NoError(t, err)
				require.Positive(t, info.Size())
				require.LessOrEqual(t, info.Size(), int64(16<<20))
			}
		}
	}
}
