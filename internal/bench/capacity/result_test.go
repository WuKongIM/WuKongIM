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
