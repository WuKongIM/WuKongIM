package cluster

import (
	"testing"

	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
)

func TestStorageCacheSnapshotsPreserveNativePopulations(t *testing.T) {
	cases := map[string]StorageEngineMetrics{
		"channel_log": storageMetricsFromChannelStore(channelstore.MessageDBFactoryMetricsSnapshot{
			BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3, MemTableReservedSizeBytes: 262144,
		}),
		"meta": storageMetricsFromMetaDB(metadb.EngineMetricsSnapshot{
			BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3, MemTableReservedSizeBytes: 262144,
		}),
		"raft": storageMetricsFromRaftLog(raftlog.MetricsSnapshot{
			Available: true, BlockCacheSizeBytes: 1024, BlockCacheHits: 11, BlockCacheMisses: 3, MemTableReservedSizeBytes: 262144,
		}),
	}
	for store, got := range cases {
		if got.BlockCacheSizeBytes != 1024 || got.BlockCacheHits != 11 || got.BlockCacheMisses != 3 || got.MemTableReservedSizeBytes != 262144 {
			t.Fatalf("%s cache population lost: %+v", store, got)
		}
	}
}
