package store

import (
	"testing"

	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func TestCacheMetricsPreserveSnapshotPopulations(t *testing.T) {
	got := messageFactoryMetricsFromSnapshot(messagedb.EngineMetricsSnapshot{
		BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3,
		MemTableSizeBytes: 524288, MemTableReservedSizeBytes: 262144,
	})
	if got.BlockCacheSizeBytes != 1024 || got.BlockCacheHits != 11 || got.BlockCacheMisses != 3 || got.MemTableReservedSizeBytes != 262144 {
		t.Fatalf("cache population lost or substituted: %+v", got)
	}
}
