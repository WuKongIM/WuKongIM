//go:build integration

package engine

import (
	"bytes"
	"testing"

	"github.com/cockroachdb/pebble/v2"
)

// Replaying one real WAL batch under a smaller memtable threshold retains a
// flushable batch in read-only state. No sleeps, private native fields, or
// callbacks held under Pebble's mutex are needed to observe its accounting.
func TestNativeCacheMetricsReportArenaReservationAcrossLargeWALReplay(t *testing.T) {
	path := t.TempDir()
	writer, err := Open(path, Options{MemTableSize: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if writer != nil {
			_ = writer.Close()
		}
	})
	payload := bytes.Repeat([]byte("x"), 1<<20)
	key := []byte("large-wal-batch")
	if err := writer.pdb.Set(key, payload, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	// A later small batch produces an ordinary arena during replay as well.
	if err := writer.pdb.Set([]byte("small-tail"), []byte("tail"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer = nil
	reader, err := Open(path, Options{MemTableSize: 1 << 20, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	value, closer, err := reader.pdb.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	same := bytes.Equal(value, payload)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Fatal("replayed durable batch payload changed")
	}
	native := reader.pdb.Metrics()
	reserved := native.MemTable.Size + native.MemTable.ZombieSize
	t.Logf("native active_and_flushable=%d zombie=%d restored_arena_reserved=%d", native.MemTable.Size, native.MemTable.ZombieSize, reserved)
	// The pinned Pebble version starts the ordinary tail arena at 256 KiB.
	const wantReserved = uint64(256 << 10)
	if native.MemTable.ZombieSize < 1<<63 || reserved != wantReserved {
		t.Fatal("fixture did not establish large-batch subtraction underflow with a bounded tail arena")
	}
	got := reader.MetricsSnapshot()
	if got.MemTableReservedSizeBytes != wantReserved {
		t.Fatalf("fourth cache accounting signal=%d, want exact arena reservation=%d; native zombie=%d is invalid retired-byte evidence", got.MemTableReservedSizeBytes, wantReserved, native.MemTable.ZombieSize)
	}
}
