package metrics

import (
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestPresenceRecoveryCensusPublishesOneImmutableFixedLabelCut(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newPresenceMetrics(reg, prometheus.Labels{"node_id": "7", "node_name": "test"})
	var cuts atomic.Uint64
	m.SetRecoverySnapshot(func() PresenceRecoverySnapshot {
		cuts.Add(1)
		return PresenceRecoverySnapshot{ResultCensusEnabled: true, QueuedCalls: 2, ActiveBatches: 1, ResultCalls: 16, Members: 18, FencedUIDs: 32, OwnedUIDBytes: 1024, ResultLimited: true,
			BatchesTotal: 5, BatchCallsTotal: 20, BatchUIDsTotal: 40, BatchCallsHighWater: 16, BatchUIDsHighWater: 32,
			ResultRows: 3, ResultDecodedBytes: 500, PageRows: 2, PageDecodedBytes: 300,
			ResultRowsHighWater: 6, ResultDecodedBytesHighWater: 700, PageRowsHighWater: 4, PageDecodedBytesHighWater: 400, DecodedBytesHighWater: 1100}
	})
	families, err := reg.Gather()
	require.NoError(t, err)
	require.Equal(t, uint64(1), cuts.Load(), "all fields must share one ownership cut")
	want := map[string]float64{"result_census_enabled": 1, "queued_calls": 2, "active_batches": 1, "result_calls": 16, "members": 18, "fenced_uids": 32, "owned_uid_bytes": 1024, "result_limited": 1, "execution_limited": 0,
		"batches_total": 5, "batch_calls_total": 20, "batch_uids_total": 40, "batch_calls_high_water": 16, "batch_uids_high_water": 32,
		"result_rows": 3, "result_decoded_bytes": 500, "page_rows": 2, "page_decoded_bytes": 300, "result_rows_high_water": 6, "result_decoded_bytes_high_water": 700, "page_rows_high_water": 4, "page_decoded_bytes_high_water": 400, "decoded_bytes_high_water": 1100}
	for suffix, value := range want {
		f := requirePresenceMetricFamily(t, families, "wukongim_presence_recovery_"+suffix, "node_id", "node_name")
		require.Len(t, f.Metric, 1)
		got := f.Metric[0].GetGauge().GetValue()
		if f.Metric[0].Counter != nil {
			got = f.Metric[0].GetCounter().GetValue()
		}
		require.Equal(t, value, got, suffix)
	}
	// Replacing a drained runtime detaches the old snapshot source and publishes
	// real zeros, including counters for the new runtime generation.
	m.SetRecoverySnapshot(nil)
	families, err = reg.Gather()
	require.NoError(t, err)
	require.Equal(t, uint64(1), cuts.Load())
	for suffix := range want {
		f := requirePresenceMetricFamily(t, families, "wukongim_presence_recovery_"+suffix, "node_id", "node_name")
		require.Zero(t, f.Metric[0].GetGauge().GetValue())
		require.Zero(t, f.Metric[0].GetCounter().GetValue())
	}
}

// Resource ownership must remain visible when expensive diagnostic census is off.
func TestPresenceRecoveryBudgetPublishesEnforcedOwnershipWithoutCensus(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newPresenceMetrics(reg, prometheus.Labels{"node_id": "7", "node_name": "test"})
	m.SetRecoverySnapshot(func() PresenceRecoverySnapshot {
		return PresenceRecoverySnapshot{ResultBudgetEnabled: true, OwnedResultRows: 12, OwnedResultBytes: 8192, OwnedResultRowsHighWater: 20, OwnedResultBytesHighWater: 16384, ResultBudgetRejected: 2}
	})
	families, err := reg.Gather()
	require.NoError(t, err)
	for suffix, want := range map[string]float64{"result_budget_enabled": 1, "owned_result_rows": 12, "owned_result_bytes": 8192, "owned_result_rows_high_water": 20, "owned_result_bytes_high_water": 16384, "result_budget_rejected_total": 2} {
		f := requirePresenceMetricFamily(t, families, "wukongim_presence_recovery_"+suffix, "node_id", "node_name")
		require.Len(t, f.Metric, 1)
		got := f.Metric[0].GetGauge().GetValue()
		if f.Metric[0].Counter != nil {
			got = f.Metric[0].GetCounter().GetValue()
		}
		require.Equal(t, want, got, suffix)
	}
	f := requirePresenceMetricFamily(t, families, "wukongim_presence_recovery_result_census_enabled", "node_id", "node_name")
	require.Zero(t, f.Metric[0].GetGauge().GetValue())
}
