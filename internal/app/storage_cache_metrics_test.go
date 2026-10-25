package app

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func TestTopCollectorPreservesStorageCacheSnapshots(t *testing.T) {
	reg := obsmetrics.New(52, "cache-observer")
	var snapshot cluster.StorageMetricsSnapshot
	collector := newTopCollector(topCollectorOptions{
		StorageMetrics:         reg.Storage,
		StorageMetricsSnapshot: func() cluster.StorageMetricsSnapshot { return snapshot },
	})
	stores := []string{"channel_log", "meta", "raft"}
	for pass := 0; pass < 3; pass++ {
		snapshot.Stores = nil
		for i, store := range stores {
			scale := int64(i + 1)
			if pass == 2 {
				scale = 0
			}
			snapshot.Stores = append(snapshot.Stores, cluster.StorageStoreMetricsSnapshot{
				Store: store,
				Engine: cluster.StorageEngineMetrics{
					BlockCacheSizeBytes: 1024 * scale, BlockCacheHits: 11 * scale,
					BlockCacheMisses: 3 * scale, MemTableReservedSizeBytes: uint64(262144 * scale),
				},
			})
		}
		collector.observeStorageMetrics()
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, store := range snapshot.Stores {
			for name, want := range map[string]float64{
				"wukongim_storage_pebble_block_cache_size_bytes":       float64(store.Engine.BlockCacheSizeBytes),
				"wukongim_storage_pebble_block_cache_hits":             float64(store.Engine.BlockCacheHits),
				"wukongim_storage_pebble_block_cache_misses":           float64(store.Engine.BlockCacheMisses),
				"wukongim_storage_pebble_memtable_reserved_size_bytes": float64(store.Engine.MemTableReservedSizeBytes),
			} {
				family := requireAppMetricFamily(t, families, name)
				if got := findAppMetricByLabels(t, family, map[string]string{"store": store.Store}).GetGauge().GetValue(); got != want {
					t.Fatalf("pass=%d %s store=%s got=%v want=%v", pass, name, store.Store, got, want)
				}
			}
		}
	}
}
