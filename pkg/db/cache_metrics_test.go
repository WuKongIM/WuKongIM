package db

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func TestNodeStoreCacheMetricsPreserveSnapshotPopulations(t *testing.T) {
	cases := map[string]StorageEngineMetrics{
		"message": storageEngineMetricsFromMessageEngine(message.EngineMetricsSnapshot{
			BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3,
			MemTableSizeBytes: 524288, MemTableReservedSizeBytes: 262144,
		}),
		"meta": storageEngineMetricsFromEngine(engine.MetricsSnapshot{
			BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3,
			MemTableSizeBytes: 524288, MemTableReservedSizeBytes: 262144,
		}),
	}
	for store, got := range cases {
		if got.BlockCacheSizeBytes != 1024 || got.BlockCacheHits != 11 || got.BlockCacheMisses != 3 || got.MemTableReservedSizeBytes != 262144 {
			t.Fatalf("%s cache population lost or substituted: %+v", store, got)
		}
	}
}
