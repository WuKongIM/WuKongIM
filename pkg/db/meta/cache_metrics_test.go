package meta

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestCacheMetricsPreserveSnapshotPopulations(t *testing.T) {
	got := metaMetricsFromSnapshot(engine.MetricsSnapshot{
		BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3,
		MemTableSizeBytes: 524288, MemTableReservedSizeBytes: 262144,
	})
	if got.BlockCacheSizeBytes != 1024 || got.BlockCacheHits != 11 || got.BlockCacheMisses != 3 || got.MemTableReservedSizeBytes != 262144 {
		t.Fatalf("cache population lost or substituted: %+v", got)
	}
}
