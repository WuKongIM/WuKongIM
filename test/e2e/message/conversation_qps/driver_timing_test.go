package conversation_qps

// Failure inventory, before implementation: merged driver waiting hides producer
// delay; a reversed timestamp is silently clamped; component percentiles are
// added; disabled diagnostics change the release report or arrival accounting.
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCPUPlacementDriverTimingKeepsDistinctWaitingComponents(t *testing.T) {
	at := time.Unix(100, 0)
	producer, queued, err := splitDriverWaiting(at, at.Add(3*time.Millisecond), at.Add(11*time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, 3.0, producer)
	require.Equal(t, 8.0, queued)
	for _, times := range [][3]time.Time{
		{at, at.Add(-time.Nanosecond), at},
		{at, at.Add(time.Millisecond), at},
	} {
		_, _, err := splitDriverWaiting(times[0], times[1], times[2])
		require.Error(t, err, "impossible chronology must remain an evidence failure")
	}
	producer, queued, err = splitDriverWaiting(at, at, at)
	require.NoError(t, err)
	require.Zero(t, producer)
	require.Zero(t, queued)
}

func TestCPUPlacementDriverTimingIsAbsentFromDefaultGate(t *testing.T) {
	p, _, err := loadProfile()
	require.NoError(t, err)
	require.False(t, p.captureDriverTiming)
	r := phaseResult{Completed: 3, Scheduled: 3}
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "driver_timing")
	r.DriverTiming = &driverTimingSummary{Observed: 3, ProducerP99MS: 2, QueuedWorkerP99MS: 7}
	raw, err = json.Marshal(r)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"driver_timing"`)
	require.Equal(t, 3, r.Completed)
}
