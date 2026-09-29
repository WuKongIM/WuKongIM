package engine

import (
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// Failure modes covered before implementation:
//   - End without Begin must not count a stall or produce negative duration.
//   - A repeated Begin must not restart the open stall or double count it.
//   - Unknown reasons still count as stalls but under "other".
//   - A snapshot during an open stall must include its elapsed time in total and max.
func TestStallRecorderCountsReasonsAndDurations(t *testing.T) {
	now := time.Unix(100, 0)
	r := newStallRecorder(func() time.Time { return now })

	r.end() // End without Begin.
	r.begin(pebble.WriteStallBeginInfo{Reason: "memtable count limit reached"})
	now = now.Add(2 * time.Second)
	r.begin(pebble.WriteStallBeginInfo{Reason: "memtable count limit reached"}) // repeated Begin.
	now = now.Add(time.Second)
	r.end()

	r.begin(pebble.WriteStallBeginInfo{Reason: "L0 file count limit exceeded"})
	now = now.Add(500 * time.Millisecond)
	r.end()

	r.begin(pebble.WriteStallBeginInfo{Reason: "something new"})
	now = now.Add(4 * time.Second)
	got := r.snapshot() // open stall.

	if got.MemTableStalls != 1 || got.L0Stalls != 1 || got.OtherStalls != 1 {
		t.Fatalf("stall counts = %+v, want one per reason", got)
	}
	if got.TotalNanos != int64(7500*time.Millisecond) {
		t.Fatalf("TotalNanos = %v, want 3.5s closed plus 4s open", time.Duration(got.TotalNanos))
	}
	if got.MaxNanos != int64(4*time.Second) {
		t.Fatalf("MaxNanos = %v, want open 4s stall", time.Duration(got.MaxNanos))
	}
	if !got.Active {
		t.Fatalf("Active = false, want open stall reported")
	}

	r.end()
	got = r.snapshot()
	if got.Active || got.TotalNanos != int64(7500*time.Millisecond) || got.MaxNanos != int64(4*time.Second) {
		t.Fatalf("after close = %+v, want inactive 7.5s total 4s max", got)
	}
}

func TestPebbleOptionsInstallStallListener(t *testing.T) {
	r := newStallRecorder(time.Now)
	popts := pebbleOptions(Options{}, r)
	if popts.EventListener == nil || popts.EventListener.WriteStallBegin == nil || popts.EventListener.WriteStallEnd == nil {
		t.Fatalf("pebble options missing write stall listener")
	}
}
