package raftlog

import (
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// storageEvents retains only bounded aggregates. Callbacks never touch the
// Pebble handle, so they remain safe while the owning DB closes.
type storageEvents struct {
	mu    sync.Mutex
	now   func() time.Time
	start time.Time
	stats MetricsSnapshot
}

func newStorageEvents(now func() time.Time) *storageEvents {
	return &storageEvents{now: now}
}

// listener uses Pebble's existing disk-health checks and write-stall events;
// installing it does not alter filesystem options or the default threshold.
func (r *storageEvents) listener() *pebble.EventListener {
	return &pebble.EventListener{
		WriteStallBegin: r.begin,
		WriteStallEnd:   r.end,
		DiskSlow:        r.diskSlow,
	}
}

func (r *storageEvents) begin(info pebble.WriteStallBeginInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats.WriteStallActive {
		return
	}
	switch info.Reason {
	case "memtable count limit reached":
		r.stats.WriteStallMemTableCount++
	case "L0 file count limit exceeded":
		r.stats.WriteStallL0Count++
	default:
		r.stats.WriteStallOtherCount++
	}
	r.stats.WriteStallActive = true
	r.start = r.now()
}

func (r *storageEvents) end() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stats.WriteStallActive {
		return
	}
	d := int64(r.now().Sub(r.start))
	r.stats.WriteStallTotalNanos += d
	r.stats.WriteStallMaxNanos = max(r.stats.WriteStallMaxNanos, d)
	r.stats.WriteStallActive = false
}

func (r *storageEvents) diskSlow(info pebble.DiskSlowInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := int64(info.Duration)
	if strings.HasSuffix(info.Path, ".log") {
		r.stats.DiskSlowWALEvents++
		r.stats.DiskSlowWALMaxNanos = max(r.stats.DiskSlowWALMaxNanos, d)
		return
	}
	r.stats.DiskSlowOtherEvents++
	r.stats.DiskSlowOtherMaxNanos = max(r.stats.DiskSlowOtherMaxNanos, d)
}

func (r *storageEvents) snapshot() MetricsSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := r.stats
	if snapshot.WriteStallActive {
		d := int64(r.now().Sub(r.start))
		snapshot.WriteStallTotalNanos += d
		snapshot.WriteStallMaxNanos = max(snapshot.WriteStallMaxNanos, d)
	}
	return snapshot
}

func (r *storageEvents) populate(snapshot *MetricsSnapshot) {
	if r == nil {
		return
	}
	events := r.snapshot()
	snapshot.WriteStallMemTableCount = events.WriteStallMemTableCount
	snapshot.WriteStallL0Count = events.WriteStallL0Count
	snapshot.WriteStallOtherCount = events.WriteStallOtherCount
	snapshot.WriteStallTotalNanos = events.WriteStallTotalNanos
	snapshot.WriteStallMaxNanos = events.WriteStallMaxNanos
	snapshot.WriteStallActive = events.WriteStallActive
	snapshot.DiskSlowWALEvents = events.DiskSlowWALEvents
	snapshot.DiskSlowWALMaxNanos = events.DiskSlowWALMaxNanos
	snapshot.DiskSlowOtherEvents = events.DiskSlowOtherEvents
	snapshot.DiskSlowOtherMaxNanos = events.DiskSlowOtherMaxNanos
}
