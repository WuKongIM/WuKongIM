//go:build integration

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/cockroachdb/pebble/v2"
)

// This uses real flush/compaction boundaries to verify that repeatedly reading
// one SST-backed negative lookup can reuse the configured block cache after
// memtables reach their steady active-and-recycled sizes.
func TestEngineNegativeReadsReuseCacheAfterMemtableRecycling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		memtable int64
	}{
		{name: "metadata_32MiB", memtable: 32 << 20},
		{name: "messages_64MiB", memtable: 64 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir()
			opts := Options{CacheSize: 128 << 20, MemTableSize: tc.memtable}
			db, err := Open(path, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			})
			first, last, missing := []byte("key/0000"), []byte("key/9999"), []byte("key/5000")
			value := bytes.Repeat([]byte("durable value---"), 256)
			settled := false
			var payloadWritten uint64
			for rotation := 1; rotation <= 15; rotation++ {
				current := db.pdb.Metrics()
				if current.MemTable.Count != 1 || current.MemTable.Size == 0 || current.MemTable.Size > uint64(tc.memtable) {
					t.Fatal("setup: expected one bounded active memtable before filling")
				}
				// Pebble deliberately preserves the small arena size when a forced
				// flush finds it less than half used. Real payload fills at least
				// three quarters first; each <=1MiB batch remains below either
				// configured large-batch threshold, so ordinary memtables grow.
				fillBytes := current.MemTable.Size * 3 / 4
				if payloadWritten+fillBytes > uint64(tc.memtable)*12 {
					t.Fatal("setup: bounded memtable fill payload exhausted")
				}
				payloadWritten += fillCacheReservationMemtable(t, db, first, last, value, fillBytes)
				if err := db.pdb.Flush(); err != nil {
					t.Fatal(err)
				}
				m := db.pdb.Metrics()
				logCacheReservationEvidence(t, "rotation", rotation, m)
				if m.MemTable.Count == 1 && m.MemTable.ZombieCount == 1 &&
					m.MemTable.Size == uint64(tc.memtable) && m.MemTable.ZombieSize == uint64(tc.memtable) {
					settled = true
					break
				}
			}
			if !settled {
				t.Fatal("setup: bounded real flushes did not establish one full active and one recycled memtable")
			}
			// Compact the bounding keys into stable SST storage so a table change
			// cannot masquerade as a repeated-read cache miss during the probe.
			if err := db.pdb.Compact(context.Background(), first, []byte("key/:"), false); err != nil {
				t.Fatal(err)
			}
			// Default point reads deliberately skip L6 filters. After compaction
			// settles the old history, one final bounded write/flush leaves an L0
			// table whose Bloom filter participates in the ordinary Get path.
			fillCacheReservationMemtable(t, db, first, last, value, uint64(2*len(value)))
			if err := db.pdb.Flush(); err != nil {
				t.Fatal(err)
			}
			beforeWarm := db.pdb.Metrics()
			if beforeWarm.Levels[0].TablesCount == 0 {
				t.Fatal("setup: final flush did not retain an L0 table for the Bloom probe")
			}
			checkCacheReservationNegative(t, db, missing)
			before := db.pdb.Metrics()
			if before.BlockCache.Hits+before.BlockCache.Misses <= beforeWarm.BlockCache.Hits+beforeWarm.BlockCache.Misses {
				t.Fatal("setup: in-range negative lookup did not access an SST block")
			}
			if before.Filter.Hits+before.Filter.Misses <= beforeWarm.Filter.Hits+beforeWarm.Filter.Misses {
				t.Fatal("setup: in-range negative lookup did not check an SST Bloom filter")
			}
			if before.Compact.NumInProgress != 0 {
				t.Fatal("setup: compaction still running before repeated-read probe")
			}
			for i := 0; i < 32; i++ {
				checkCacheReservationNegative(t, db, missing)
			}
			after := db.pdb.Metrics()
			logCacheReservationEvidence(t, "before_repeat", 0, before)
			logCacheReservationEvidence(t, "after_repeat", 0, after)
			if after.Compact.NumInProgress != 0 || before.Compact.Count != after.Compact.Count {
				t.Fatal("setup: compaction overlapped the repeated-read cache probe")
			}
			if before.MemTable.Size != uint64(tc.memtable) || before.MemTable.ZombieSize != uint64(tc.memtable) ||
				after.MemTable.Size != before.MemTable.Size || after.MemTable.ZombieSize != before.MemTable.ZombieSize {
				t.Fatal("setup: active/recycled memtable accounting changed across cache probe")
			}
			if got := after.Filter.Hits + after.Filter.Misses - before.Filter.Hits - before.Filter.Misses; got < 32 {
				t.Fatalf("setup: 32 repeated negative reads checked only %d Bloom filters", got)
			}
			if got := after.BlockCache.Hits - before.BlockCache.Hits; got < 32 {
				t.Errorf("32 repeated negative reads added %d cache hits, want at least 32 after warmup", got)
			}
			if got := after.BlockCache.Misses - before.BlockCache.Misses; got != 0 {
				t.Errorf("32 repeated negative reads added %d cache misses, want 0 after warmup", got)
			}
			for _, key := range [][]byte{first, last} {
				got, found, err := db.Get(key)
				if err != nil || !found || !bytes.Equal(got, value) {
					t.Fatalf("durable value before close: found=%t err=%v", found, err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(path, opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range [][]byte{first, last} {
				got, found, err := db.Get(key)
				if err != nil || !found || !bytes.Equal(got, value) {
					t.Fatalf("durable value after reopen: found=%t err=%v", found, err)
				}
			}
			checkCacheReservationNegative(t, db, missing)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			t.Log("durable reads, close and reopen completed")
		})
	}
}

// fillCacheReservationMemtable grows ordinary memtables through real writes,
// reusing a fixed source value and closing every bounded synchronous batch.
func fillCacheReservationMemtable(t *testing.T, db *DB, first, last, value []byte, bytesToWrite uint64) uint64 {
	t.Helper()
	var written uint64
	for written < bytesToWrite {
		func() {
			batch := db.NewBatch()
			defer func() {
				if err := batch.Close(); err != nil {
					t.Fatal(err)
				}
			}()
			var batchBytes uint64
			for batchBytes < 1<<20 && written < bytesToWrite {
				for _, key := range [][]byte{first, last} {
					if err := batch.Set(key, value); err != nil {
						t.Fatal(err)
					}
					batchBytes += uint64(len(value))
					written += uint64(len(value))
				}
			}
			if err := batch.Commit(true); err != nil {
				t.Fatal(err)
			}
		}()
	}
	return written
}

func checkCacheReservationNegative(t *testing.T, db *DB, key []byte) {
	t.Helper()
	value, found, err := db.Get(key)
	if err != nil || found || value != nil {
		t.Fatalf("negative Get: found=%t value_len=%d err=%v", found, len(value), err)
	}
}

func logCacheReservationEvidence(t *testing.T, stage string, rotation int, m *pebble.Metrics) {
	t.Helper()
	raw, err := json.Marshal(struct {
		Stage        string `json:"stage"`
		Rotation     int    `json:"rotation"`
		ActiveBytes  uint64 `json:"active_bytes"`
		ActiveCount  int64  `json:"active_count"`
		ZombieBytes  uint64 `json:"zombie_bytes"`
		ZombieCount  int64  `json:"zombie_count"`
		CacheBytes   int64  `json:"cache_bytes"`
		CacheCount   int64  `json:"cache_count"`
		Hits         int64  `json:"hits"`
		Misses       int64  `json:"misses"`
		FilterHits   int64  `json:"filter_hits"`
		FilterMisses int64  `json:"filter_misses"`
	}{stage, rotation, m.MemTable.Size, m.MemTable.Count, m.MemTable.ZombieSize, m.MemTable.ZombieCount, m.BlockCache.Size, m.BlockCache.Count, m.BlockCache.Hits, m.BlockCache.Misses, m.Filter.Hits, m.Filter.Misses})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cache_lifecycle_evidence=%s", raw)
}
