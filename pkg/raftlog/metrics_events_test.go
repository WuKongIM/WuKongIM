package raftlog

import (
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// Deterministic callback input avoids imposing Pebble's real multi-second
// slow-disk threshold on tests. The failure matrix is frozen before this seam.
func TestRaftStorageEventsPreserveReasonsDurationsAndFileClasses(t *testing.T) {
	now := time.Unix(100, 0)
	events := newStorageEvents(func() time.Time { return now })
	listener := events.listener()
	listener.WriteStallEnd()
	listener.WriteStallBegin(pebble.WriteStallBeginInfo{Reason: "memtable count limit reached"})
	now = now.Add(2 * time.Second)
	listener.WriteStallBegin(pebble.WriteStallBeginInfo{Reason: "L0 file count limit exceeded"})
	now = now.Add(time.Second)
	listener.WriteStallEnd()
	listener.WriteStallBegin(pebble.WriteStallBeginInfo{Reason: "L0 file count limit exceeded"})
	now = now.Add(500 * time.Millisecond)
	listener.WriteStallEnd()
	listener.WriteStallBegin(pebble.WriteStallBeginInfo{Reason: "future reason"})
	now = now.Add(4 * time.Second)
	listener.DiskSlow(pebble.DiskSlowInfo{Path: "/raft/000001.log", Duration: 6 * time.Second})
	listener.DiskSlow(pebble.DiskSlowInfo{Path: "/raft/000001.log", Duration: 7 * time.Second})
	listener.DiskSlow(pebble.DiskSlowInfo{Path: "/raft/000002.sst", Duration: 8 * time.Second})

	got := events.snapshot()
	if got.WriteStallMemTableCount != 1 || got.WriteStallL0Count != 1 || got.WriteStallOtherCount != 1 {
		t.Fatalf("stall counts = %+v; want one per reason", got)
	}
	if !got.WriteStallActive || got.WriteStallTotalNanos != int64(7500*time.Millisecond) || got.WriteStallMaxNanos != int64(4*time.Second) {
		t.Fatalf("active stall = %+v; want 7.5s total and 4s max", got)
	}
	if got.DiskSlowWALEvents != 2 || got.DiskSlowWALMaxNanos != int64(7*time.Second) || got.DiskSlowOtherEvents != 1 || got.DiskSlowOtherMaxNanos != int64(8*time.Second) {
		t.Fatalf("slow events = %+v; want WAL 2/7s, other 1/8s", got)
	}
	listener.WriteStallEnd()
	listener.WriteStallEnd()
	closed := events.snapshot()
	if closed.WriteStallActive || closed.WriteStallTotalNanos != got.WriteStallTotalNanos || closed.WriteStallMaxNanos != got.WriteStallMaxNanos {
		t.Fatalf("closed stall changed cumulative time: %+v", closed)
	}
	if events.snapshot() != closed {
		t.Fatal("snapshot consumed events")
	}
}

func TestRaftStorageEventsConcurrentCallbacksAndSnapshots(t *testing.T) {
	events := newStorageEvents(time.Now)
	listener := events.listener()
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				listener.DiskSlow(pebble.DiskSlowInfo{Path: "raft.log", Duration: time.Second})
				_ = events.snapshot()
			}
		}()
	}
	workers.Wait()
	if got := events.snapshot(); got.DiskSlowWALEvents != 400 || got.DiskSlowWALMaxNanos != int64(time.Second) {
		t.Fatalf("concurrent reports lost: %+v", got)
	}
}
