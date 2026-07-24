package metrics

import (
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestPresenceMetricsExposeExpiryAndTouchFlush(t *testing.T) {
	reg := New(7, "node-7")
	require.NotNil(t, reg.Presence)

	reg.Presence.ObserveExpiry("ok", 250*time.Millisecond, 3, 11, 5, 17, 4)
	reg.Presence.ObserveExpiry("ok", 500*time.Millisecond, 2, 7, 3, 14, 2)
	reg.Presence.ObserveTouchFlush("ok", 40*time.Millisecond, 10, 9, 8, 2, 3, 4, true)
	reg.Presence.ObserveTouchFlush("ok", 20*time.Millisecond, 4, 4, 3, 1, 1, 2, true)
	reg.Presence.ObserveEndpointLookup("remote_bulk", "partial", true, 30*time.Millisecond, 7, 3)
	reg.Presence.ObserveEndpointLookup("uid-derived path", "raw error", false, -time.Second, -1, -1)

	families, err := reg.Gather()
	require.NoError(t, err)

	expiryTotal := requirePresenceMetricFamily(t, families, "wukongim_presence_expiry_total", "node_id", "node_name", "result")
	require.Equal(t, float64(2), presenceMetricByLabels(t, expiryTotal, map[string]string{
		"node_id": "7", "node_name": "node-7", "result": "ok",
	}).GetCounter().GetValue())

	expiryDuration := requirePresenceMetricFamily(t, families, "wukongim_presence_expiry_duration_seconds", "node_id", "node_name", "result")
	expiryDurationMetric := presenceMetricByLabels(t, expiryDuration, map[string]string{
		"node_id": "7", "node_name": "node-7", "result": "ok",
	})
	require.Equal(t, uint64(2), expiryDurationMetric.GetHistogram().GetSampleCount())
	require.InDelta(t, 0.75, expiryDurationMetric.GetHistogram().GetSampleSum(), 1e-9)

	expiryGauges := map[string]float64{
		"wukongim_presence_expiry_due_buckets":     2,
		"wukongim_presence_expiry_examined_routes": 7,
		"wukongim_presence_expired_routes":         3,
		"wukongim_presence_expiry_index_routes":    14,
		"wukongim_presence_expiry_index_buckets":   2,
	}
	for name, want := range expiryGauges {
		family := requirePresenceMetricFamily(t, families, name, "node_id", "node_name")
		metric := presenceMetricByLabels(t, family, map[string]string{
			"node_id": "7", "node_name": "node-7",
		})
		require.Equal(t, want, metric.GetGauge().GetValue(), name)
	}

	touchTotal := requirePresenceMetricFamily(t, families, "wukongim_presence_touch_flush_total", "budget_reached", "node_id", "node_name", "result")
	require.Equal(t, float64(2), presenceMetricByLabels(t, touchTotal, map[string]string{
		"budget_reached": "true", "node_id": "7", "node_name": "node-7", "result": "ok",
	}).GetCounter().GetValue())

	touchDuration := requirePresenceMetricFamily(t, families, "wukongim_presence_touch_flush_duration_seconds", "budget_reached", "node_id", "node_name", "result")
	touchDurationMetric := presenceMetricByLabels(t, touchDuration, map[string]string{
		"budget_reached": "true", "node_id": "7", "node_name": "node-7", "result": "ok",
	})
	require.Equal(t, uint64(2), touchDurationMetric.GetHistogram().GetSampleCount())
	require.InDelta(t, 0.06, touchDurationMetric.GetHistogram().GetSampleSum(), 1e-9)

	touchRoutes := requirePresenceMetricFamily(t, families, "wukongim_presence_touch_flush_routes", "node_id", "node_name", "stage")
	for stage, want := range map[string]float64{
		"drained":  14,
		"resolved": 13,
		"sent":     11,
		"requeued": 3,
	} {
		require.Equal(t, want, presenceMetricByLabels(t, touchRoutes, map[string]string{
			"node_id": "7", "node_name": "node-7", "stage": stage,
		}).GetCounter().GetValue(), stage)
	}
	require.Len(t, touchRoutes.GetMetric(), 4)

	touchChunks := requirePresenceMetricFamily(t, families, "wukongim_presence_touch_flush_chunks", "node_id", "node_name")
	require.Equal(t, float64(4), presenceMetricByLabels(t, touchChunks, map[string]string{
		"node_id": "7", "node_name": "node-7",
	}).GetCounter().GetValue())

	touchTargetGroups := requirePresenceMetricFamily(t, families, "wukongim_presence_touch_flush_target_groups", "node_id", "node_name")
	require.Equal(t, float64(6), presenceMetricByLabels(t, touchTargetGroups, map[string]string{
		"node_id": "7", "node_name": "node-7",
	}).GetCounter().GetValue())

	lookupTotal := requirePresenceMetricFamily(t, families, "wukongim_presence_endpoint_lookup_total", "node_id", "node_name", "outcome", "path", "stale_retry")
	require.Equal(t, float64(1), presenceMetricByLabels(t, lookupTotal, map[string]string{
		"node_id": "7", "node_name": "node-7", "path": "remote_bulk", "outcome": "partial", "stale_retry": "true",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), presenceMetricByLabels(t, lookupTotal, map[string]string{
		"node_id": "7", "node_name": "node-7", "path": "unknown", "outcome": "unknown", "stale_retry": "false",
	}).GetCounter().GetValue())
	lookupItems := requirePresenceMetricFamily(t, families, "wukongim_presence_endpoint_lookup_items_total", "node_id", "node_name", "outcome", "path", "stale_retry")
	require.Equal(t, float64(7), presenceMetricByLabels(t, lookupItems, map[string]string{
		"node_id": "7", "node_name": "node-7", "path": "remote_bulk", "outcome": "partial", "stale_retry": "true",
	}).GetCounter().GetValue())
	lookupGroups := requirePresenceMetricFamily(t, families, "wukongim_presence_endpoint_lookup_groups_total", "node_id", "node_name", "outcome", "path", "stale_retry")
	require.Equal(t, float64(3), presenceMetricByLabels(t, lookupGroups, map[string]string{
		"node_id": "7", "node_name": "node-7", "path": "remote_bulk", "outcome": "partial", "stale_retry": "true",
	}).GetCounter().GetValue())
	lookupDuration := requirePresenceMetricFamily(t, families, "wukongim_presence_endpoint_lookup_duration_seconds", "node_id", "node_name", "outcome", "path", "stale_retry")
	unknownDuration := presenceMetricByLabels(t, lookupDuration, map[string]string{
		"node_id": "7", "node_name": "node-7", "path": "unknown", "outcome": "unknown", "stale_retry": "false",
	}).GetHistogram()
	require.Equal(t, uint64(1), unknownDuration.GetSampleCount())
	require.Zero(t, unknownDuration.GetSampleSum())
}

func TestPresenceEndpointLookupSteadyStateAllocationBudget(t *testing.T) {
	reg := New(1, "n1")
	reg.Presence.ObserveEndpointLookup("local_bulk", "ok", false, time.Millisecond, 512, 221)
	reg.Presence.ObserveEndpointLookup("remote_bulk", "partial", true, time.Millisecond, 512, 221)

	allocs := testing.AllocsPerRun(1000, func() {
		reg.Presence.ObserveEndpointLookup("local_bulk", "ok", false, time.Millisecond, 512, 221)
		reg.Presence.ObserveEndpointLookup("remote_bulk", "partial", true, time.Millisecond, 512, 221)
	})
	require.Zero(t, allocs, "steady-state bounded presence endpoint metrics must not allocate")
}

func requirePresenceMetricFamily(t *testing.T, families []*dto.MetricFamily, name string, labelNames ...string) *dto.MetricFamily {
	t.Helper()
	family := requireMetricFamily(t, families, name)
	require.NotEmpty(t, family.GetMetric(), name)
	for _, metric := range family.GetMetric() {
		got := make([]string, 0, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			labelName := label.GetName()
			got = append(got, labelName)
			requirePresenceLabelIsBounded(t, name, labelName)
		}
		require.ElementsMatch(t, labelNames, got, name)
	}
	return family
}

func requirePresenceLabelIsBounded(t *testing.T, metricName, labelName string) {
	t.Helper()
	lower := strings.ToLower(labelName)
	for _, forbidden := range []string{"uid", "session", "hash_slot", "target", "channel"} {
		require.NotContains(t, lower, forbidden, "%s exposes forbidden label %q", metricName, labelName)
	}
}

func presenceMetricByLabels(t *testing.T, family *dto.MetricFamily, labels map[string]string) *dto.Metric {
	t.Helper()
	return findMetricByLabels(t, family, labels)
}
