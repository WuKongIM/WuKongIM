//go:build integration

package raftlog

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

func TestRaftStorageMetricsOpenCloseAndConcurrentCallbacks(t *testing.T) {
	var absent *DB
	if absent.MetricsSnapshot().Available {
		t.Fatal("nil DB is available")
	}
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fresh := db.MetricsSnapshot()
	if !fresh.Available || fresh.DiskSpaceUsageBytes == 0 || fresh.WALFsyncCount == 0 {
		t.Fatalf("open DB lacks its real manifest sync observation: %+v", fresh)
	}
	if fresh.WriteStallActive || fresh.WriteStallMemTableCount != 0 || fresh.WriteStallL0Count != 0 || fresh.WriteStallOtherCount != 0 || fresh.DiskSlowWALEvents != 0 || fresh.DiskSlowOtherEvents != 0 {
		t.Fatalf("fresh DB reported unobserved events: %+v", fresh)
	}
	// Synthetic callback values exercise recorder lifetime without creating a
	// real five-second disk operation. The actual DB is concurrently closed.
	listener := db.storageEvents.listener()
	var workers sync.WaitGroup
	started := make(chan struct{})
	workers.Add(2)
	go func() {
		defer workers.Done()
		close(started)
		for i := 0; i < 100; i++ {
			_ = db.MetricsSnapshot()
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			listener.DiskSlow(pebble.DiskSlowInfo{Path: "raft.log", Duration: 6 * time.Second})
		}
	}()
	<-started
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if db.MetricsSnapshot().Available {
		t.Fatal("closed DB is available")
	}
	if got := db.storageEvents.snapshot().DiskSlowWALEvents; got != 100 {
		t.Fatalf("callbacks overlapping Close = %d, want 100", got)
	}
}

func TestRaftStorageMetricsNativeFsyncHistogramUnitsAndBuckets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	before := db.MetricsSnapshot()
	// The same native histogram written by Pebble accepts deterministic latency
	// samples, so this integration check needs no deliberately slow filesystem.
	histogram := db.db.Metrics().LogWriter.FsyncLatency
	for _, duration := range []time.Duration{50 * time.Microsecond, 200 * time.Millisecond, 1500 * time.Millisecond, 6 * time.Second} {
		histogram.Observe(float64(duration))
	}
	after := db.MetricsSnapshot()
	if after.WALFsyncCount-before.WALFsyncCount != 4 || after.WALFsyncSumNanos-before.WALFsyncSumNanos != int64(7700050*time.Microsecond) {
		t.Fatalf("native histogram count/sum changed units: before=%+v after=%+v", before, after)
	}
	if after.WALFsyncOver100ms-before.WALFsyncOver100ms != 3 || after.WALFsyncOver1s-before.WALFsyncOver1s != 2 || after.WALFsyncOver5s-before.WALFsyncOver5s != 1 {
		t.Fatalf("native slow buckets: before=%+v after=%+v", before, after)
	}
}
