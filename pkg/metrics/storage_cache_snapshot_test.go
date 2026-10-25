package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestStorageCacheMetricsReplaceAbsoluteSnapshots(t *testing.T) {
	reg := New(51, "cache-snapshot")
	names := []string{
		"wukongim_storage_pebble_block_cache_size_bytes",
		"wukongim_storage_pebble_block_cache_hits",
		"wukongim_storage_pebble_block_cache_misses",
		"wukongim_storage_pebble_memtable_reserved_size_bytes",
	}
	assertSnapshot := func(want []float64) {
		t.Helper()
		families, err := reg.Gather()
		require.NoError(t, err)
		for i, name := range names {
			family := requireMetricFamily(t, families, name)
			require.Equal(t, dto.MetricType_GAUGE, family.GetType(), name)
			require.Len(t, family.GetMetric(), 5, name)
			for _, store := range []string{"channel_log", "meta", "raft", "controller_meta", "controller_raft"} {
				metric := findMetricByLabels(t, family, map[string]string{"store": store})
				expected := float64(0)
				if store == "channel_log" {
					expected = want[i]
				}
				require.Equal(t, expected, metric.GetGauge().GetValue(), "%s store=%s", name, store)
				for _, label := range metric.GetLabel() {
					require.Contains(t, []string{"node_id", "node_name", "store"}, label.GetName(), name)
				}
			}
		}
	}
	assertSnapshot([]float64{0, 0, 0, 0})
	obs := StoragePebbleObservation{BlockCacheSizeBytes: 4096, BlockCacheHits: 17, BlockCacheMisses: 3, MemTableReservedSizeBytes: 262144}
	for i := 0; i < 2; i++ {
		reg.Storage.SetPebbleMetrics("channel_log", obs)
		assertSnapshot([]float64{4096, 17, 3, 262144})
	}
	// A reopened native store starts a new counter population. Absolute snapshot
	// gauges must replace old totals and arena reservations, including true zero.
	reg.Storage.SetPebbleMetrics("channel_log", StoragePebbleObservation{})
	assertSnapshot([]float64{0, 0, 0, 0})
}
