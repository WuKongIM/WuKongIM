package engine

import (
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/prometheus/client_golang/prometheus"
)

// Failure modes covered before implementation:
//   - WAL (.log) and other files must be classified separately; a slow SST sync
//     must not be reported as a WAL stall and vice versa.
//   - Repeated observations of one in-flight operation grow its duration; the
//     max must keep the largest value and events must count every report.
//   - A nil recorder snapshot is zero, not a panic.
//   - WAL fsync buckets: values below 100ms must not count as slow, and each
//     threshold counts only observations above its nearest lower bucket bound.
//   - A nil or empty histogram yields a zero summary.
//   - Open with a disk-slow threshold must still record WAL fsyncs and close
//     the health-check FS on Close.
func TestDiskSlowRecorderSeparatesWALAndOther(t *testing.T) {
	var nilRecorder *diskSlowRecorder
	if got := nilRecorder.snapshot(); got != (DiskSlowSnapshot{}) {
		t.Fatalf("nil snapshot = %+v, want zero", got)
	}
	r := newDiskSlowRecorder()
	r.observe(vfs.DiskSlowInfo{Path: "/data/message/000123.log", OpType: vfs.OpTypeSyncData, Duration: 2 * time.Second})
	r.observe(vfs.DiskSlowInfo{Path: "/data/message/000123.log", OpType: vfs.OpTypeSyncData, Duration: 3 * time.Second})
	r.observe(vfs.DiskSlowInfo{Path: "/data/message/000456.sst", OpType: vfs.OpTypeSync, Duration: 1500 * time.Millisecond})

	got := r.snapshot()
	want := DiskSlowSnapshot{
		WALEvents:     2,
		WALMaxNanos:   int64(3 * time.Second),
		OtherEvents:   1,
		OtherMaxNanos: int64(1500 * time.Millisecond),
	}
	if got != want {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
}

func TestWALFsyncSummaryCountsSlowBuckets(t *testing.T) {
	if got := walFsyncSummary(nil); got != (WALFsyncSnapshot{}) {
		t.Fatalf("nil histogram summary = %+v, want zero", got)
	}
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Buckets: pebble.FsyncLatencyBuckets})
	if got := walFsyncSummary(h); got != (WALFsyncSnapshot{}) {
		t.Fatalf("empty histogram summary = %+v, want zero", got)
	}
	for _, d := range []time.Duration{50 * time.Microsecond, 200 * time.Millisecond, 1500 * time.Millisecond, 6 * time.Second} {
		h.Observe(float64(d))
	}
	got := walFsyncSummary(h)
	wantSum := int64(50*time.Microsecond + 200*time.Millisecond + 1500*time.Millisecond + 6*time.Second)
	if got.Count != 4 || got.SumNanos != wantSum {
		t.Fatalf("count/sum = %d/%d, want 4/%d", got.Count, got.SumNanos, wantSum)
	}
	if got.Over100ms != 3 || got.Over1s != 2 || got.Over5s != 1 {
		t.Fatalf("slow buckets = %+v, want 3/2/1", got)
	}
}

func TestOpenWithDiskSlowThresholdRecordsWALFsync(t *testing.T) {
	db, err := Open(t.TempDir(), Options{DiskSlowThreshold: time.Second})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	b := db.NewBatch()
	b.Set([]byte("k"), []byte("v"))
	if err := b.Commit(true); err != nil {
		t.Fatalf("Commit(sync): %v", err)
	}
	b.Close()
	if got := db.MetricsSnapshot().WALFsync.Count; got == 0 {
		t.Fatalf("WALFsync.Count = 0 after a synced commit")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if db.diskHealth != nil {
		t.Fatalf("disk health FS not released on Close")
	}
}
