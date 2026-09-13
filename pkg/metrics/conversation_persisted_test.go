package metrics

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPersistedReadMetricsAccountForRejectedAndFinishedBatches(t *testing.T) {
	reg := New(1, "node")
	m := reg.Conversation
	m.ObservePersistedReadAdmission("heads", true, 1, 16)
	m.ObservePersistedReadAdmission("heads", true, 2, 16)
	m.ObservePersistedReadAdmission("recents", false, 16, 16)
	m.ObservePersistedReadCompletion("heads", "error", 2, time.Millisecond)
	families, err := reg.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(1), findMetricByLabels(t, requireMetricFamily(t, families, "wukongim_conversation_persisted_inflight"), map[string]string{"kind": "heads"}).GetGauge().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, requireMetricFamily(t, families, "wukongim_conversation_persisted_admission_total"), map[string]string{"kind": "recents", "result": "rejected"}).GetCounter().GetValue())
	m.ObservePersistedReadCompletion("heads", "ok", 3, 2*time.Millisecond)
	m.ObservePersistedReadAdmission("unbounded-channel-identity", true, -1, 16)
	m.ObservePersistedReadCompletion("unbounded-channel-identity", "unbounded-error", -1, -time.Second)
	families, err = reg.Gather()
	require.NoError(t, err)
	for _, sample := range requireMetricFamily(t, families, "wukongim_conversation_persisted_inflight").Metric {
		require.Zero(t, sample.GetGauge().GetValue())
	}
	require.Equal(t, uint64(1), findMetricByLabels(t, requireMetricFamily(t, families, "wukongim_conversation_persisted_hold_seconds"), map[string]string{"kind": "heads", "result": "error"}).GetHistogram().GetSampleCount())
	for _, family := range families {
		for _, sample := range family.Metric {
			for _, label := range sample.Label {
				require.NotEqual(t, "unbounded-channel-identity", label.GetValue())
				require.NotEqual(t, "unbounded-error", label.GetValue())
			}
		}
	}
	var disabled *ConversationMetrics
	disabled.ObservePersistedReadAdmission("heads", true, 1, 16)
	disabled.ObservePersistedReadCompletion("heads", "ok", 1, time.Millisecond)
}
