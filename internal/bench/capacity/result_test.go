package capacity

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteResultWritesJSONAndSummary(t *testing.T) {
	dir := t.TempDir()
	result := Result{
		Status:       StatusPassed,
		Profile:      ProfileMixed,
		MaxStableQPS: 500,
		Attempts: []AttemptResult{{
			Attempt:    Attempt{Index: 0, OfferedQPS: 500},
			Passed:     true,
			ActualQPS:  498,
			SendackP50: 10 * time.Millisecond,
			SendackP95: 20 * time.Millisecond,
			SendackP99: 30 * time.Millisecond,
		}},
	}

	require.NoError(t, WriteResult(dir, result))
	require.FileExists(t, filepath.Join(dir, "result.json"))
	require.FileExists(t, filepath.Join(dir, "summary.md"))
}

func TestCapacityResultSummaryIncludesAttemptMessageCounts(t *testing.T) {
	result := Result{
		Status:       StatusFailed,
		Profile:      "hot-channel",
		MaxStableQPS: 100,
		Attempts: []AttemptResult{{
			Attempt:           Attempt{Index: 1, OfferedQPS: 200},
			ActualQPS:         90,
			ScheduledMessages: 2000,
			SendSuccess:       900,
			SendErrors:        100,
			BacklogMessages:   1000,
			FailureReason:     "actual_qps_below_min_ratio",
		}},
	}

	require.Contains(t, SummaryMarkdown(result), "# wkbench capacity hot-channel")
	require.Contains(t, SummaryMarkdown(result), "scheduled=2000 success=900 errors=100 backlog=1000")
	require.Contains(t, ConsoleSummary(result), "wkbench capacity hot-channel")
	require.Contains(t, ConsoleSummary(result), "scheduled=2000\tsuccess=900\terrors=100\tbacklog=1000")
}
