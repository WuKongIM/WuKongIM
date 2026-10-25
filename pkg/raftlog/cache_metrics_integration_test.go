//go:build integration

package raftlog

import (
	"testing"

	"github.com/cockroachdb/pebble/v2"
)

func TestNativeCacheMetricsReportRetainedBlocksAndRecycledArena(t *testing.T) {
	db, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key := []byte("cache-metrics-probe")
	if err := db.db.Set(key, []byte("durable value"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := db.db.Flush(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		_, closer, err := db.db.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	native := db.db.Metrics()
	if native.BlockCache.Size == 0 || native.BlockCache.Hits == 0 || native.BlockCache.Misses == 0 || native.MemTable.ZombieSize == 0 {
		t.Fatalf("fixture lacks cache/recycled observations: size=%d hits=%d misses=%d zombie=%d", native.BlockCache.Size, native.BlockCache.Hits, native.BlockCache.Misses, native.MemTable.ZombieSize)
	}
	got := db.MetricsSnapshot()
	if got.BlockCacheSizeBytes != native.BlockCache.Size || got.BlockCacheHits != native.BlockCache.Hits || got.BlockCacheMisses != native.BlockCache.Misses || got.MemTableReservedSizeBytes != native.MemTable.Size+native.MemTable.ZombieSize {
		t.Fatalf("native cache population lost: snapshot=%+v native_size=%d native_hits=%d native_misses=%d native_zombie=%d", got, native.BlockCache.Size, native.BlockCache.Hits, native.BlockCache.Misses, native.MemTable.ZombieSize)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := db.MetricsSnapshot(); got.BlockCacheSizeBytes != 0 || got.BlockCacheHits != 0 || got.BlockCacheMisses != 0 || got.MemTableReservedSizeBytes != 0 {
		t.Fatalf("closed engine retained observations: %+v", got)
	}
}
