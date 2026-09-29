package engine

import (
	"sync"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// Pebble write-stall reasons from pebble v2 db.go makeRoomForWrite.
const (
	stallReasonMemTable = "memtable count limit reached"
	stallReasonL0       = "L0 file count limit exceeded"
)

// StallSnapshot is a bounded aggregate of Pebble write stalls since open.
type StallSnapshot struct {
	// MemTableStalls counts stalls caused by the memtable stop-writes threshold.
	MemTableStalls int64
	// L0Stalls counts stalls caused by the L0 stop-writes threshold.
	L0Stalls int64
	// OtherStalls counts stalls with any other reason.
	OtherStalls int64
	// TotalNanos is the cumulative duration of closed stalls plus the open one.
	TotalNanos int64
	// MaxNanos is the longest single stall, including an open stall.
	MaxNanos int64
	// Active reports whether writes are stalled at snapshot time.
	Active bool
}

// stallRecorder aggregates WriteStallBegin/End events with O(1) memory.
// Pebble serializes these callbacks, but snapshots run concurrently.
type stallRecorder struct {
	now   func() time.Time
	mu    sync.Mutex
	stats StallSnapshot
	start time.Time
}

func newStallRecorder(now func() time.Time) *stallRecorder {
	return &stallRecorder{now: now}
}

func (r *stallRecorder) begin(info pebble.WriteStallBeginInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats.Active {
		return
	}
	switch info.Reason {
	case stallReasonMemTable:
		r.stats.MemTableStalls++
	case stallReasonL0:
		r.stats.L0Stalls++
	default:
		r.stats.OtherStalls++
	}
	r.stats.Active = true
	r.start = r.now()
}

func (r *stallRecorder) end() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stats.Active {
		return
	}
	d := int64(r.now().Sub(r.start))
	r.stats.TotalNanos += d
	r.stats.MaxNanos = max(r.stats.MaxNanos, d)
	r.stats.Active = false
}

func (r *stallRecorder) snapshot() StallSnapshot {
	if r == nil {
		return StallSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stats
	if s.Active {
		d := int64(r.now().Sub(r.start))
		s.TotalNanos += d
		s.MaxNanos = max(s.MaxNanos, d)
	}
	return s
}
