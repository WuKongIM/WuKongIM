package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Read barrier waits use only the fixed result label; unknown results fold
// into "error" so callers cannot create new series.
func TestSlotReadBarrierMetricsUseFixedResults(t *testing.T) {
	r := New(1, "node")
	r.Slot.ObserveReadBarrier("ok", 3*time.Millisecond)
	r.Slot.ObserveReadBarrier("busy", time.Millisecond)
	r.Slot.ObserveReadBarrier("client-secret", time.Millisecond)
	families, err := r.Gather()
	require.NoError(t, err)
	var found bool
	for _, family := range families {
		if family.GetName() != "wukongim_slot_read_barrier_duration_seconds" {
			continue
		}
		found = true
		require.Len(t, family.Metric, 3)
		for _, m := range family.Metric {
			requireNoMetricLabel(t, m, "slot_id")
		}
		require.Equal(t, uint64(1), findMetricByLabels(t, family, map[string]string{"result": "ok"}).GetHistogram().GetSampleCount())
		require.Equal(t, uint64(1), findMetricByLabels(t, family, map[string]string{"result": "busy"}).GetHistogram().GetSampleCount())
		require.Equal(t, uint64(1), findMetricByLabels(t, family, map[string]string{"result": "error"}).GetHistogram().GetSampleCount())
	}
	require.True(t, found)
}
