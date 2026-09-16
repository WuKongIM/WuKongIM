package metrics

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestConversationReadStagesBoundLabelsAndClampDuration(t *testing.T) {
	reg := New(1, "node")
	m := reg.Conversation
	m.ObserveReadStage("edit_slot", "barrier", "ok", time.Second)
	m.ObserveReadStage("edit_slot", "storage", "private-error", -time.Second)
	m.ObserveReadStage("private-uid", "barrier", "ok", time.Second)
	m.ObserveReadStage("list", "private-channel", "ok", time.Second)
	m.ObserveReadStage("list", "barrier", "ok", time.Second)
	families, err := reg.Gather()
	require.NoError(t, err)
	family := requireMetricFamily(t, families, "wukongim_conversation_read_stage_duration_seconds")
	require.Len(t, family.Metric, 24)
	barrier := findMetricByLabels(t, family, map[string]string{"scope": "edit_slot", "stage": "barrier", "result": "ok"}).GetHistogram()
	require.Equal(t, uint64(1), barrier.GetSampleCount())
	require.Equal(t, float64(1), barrier.GetSampleSum())
	storage := findMetricByLabels(t, family, map[string]string{"scope": "edit_slot", "stage": "storage", "result": "error"}).GetHistogram()
	require.Equal(t, uint64(1), storage.GetSampleCount())
	require.Zero(t, storage.GetSampleSum())
	for _, metric := range family.Metric {
		for _, label := range metric.Label {
			require.NotContains(t, label.GetValue(), "private-")
		}
	}
	var disabled *ConversationMetrics
	disabled.ObserveReadStage("list", "handler", "ok", time.Second)
	require.Zero(t, testing.AllocsPerRun(100, func() { m.ObserveReadStage("list", "handler", "ok", time.Second) }))
	require.Zero(t, testing.AllocsPerRun(100, func() { disabled.ObserveReadStage("list", "handler", "ok", time.Second) }))
}
func BenchmarkConversationReadStage(b *testing.B) {
	m := New(1, "node").Conversation
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ObserveReadStage("edit_slot", "barrier", "ok", time.Millisecond)
	}
}
