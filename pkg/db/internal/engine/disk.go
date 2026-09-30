package engine

import (
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// DiskSlowSnapshot aggregates Pebble disk-slow reports since open with O(1)
// memory. Pebble re-reports one in-flight operation on every health tick, so
// events count reports, not distinct operations; max is the longest report.
type DiskSlowSnapshot struct {
	// WALEvents counts slow-disk reports on WAL (.log) files.
	WALEvents int64
	// WALMaxNanos is the longest reported WAL operation duration.
	WALMaxNanos int64
	// OtherEvents counts slow-disk reports on SST, manifest, and other files.
	OtherEvents int64
	// OtherMaxNanos is the longest reported non-WAL operation duration.
	OtherMaxNanos int64
}

// WALFsyncSnapshot summarizes Pebble's WAL fsync latency histogram.
type WALFsyncSnapshot struct {
	// Count is the number of WAL fsyncs observed since open.
	Count uint64
	// SumNanos is the cumulative WAL fsync duration.
	SumNanos int64
	// Over100ms, Over1s and Over5s count fsyncs above each threshold, resolved
	// at Pebble's histogram bucket boundaries.
	Over100ms uint64
	Over1s    uint64
	Over5s    uint64
}

// diskSlowRecorder receives Pebble DiskSlow callbacks, which may arrive from
// several health-check goroutines concurrently with snapshots.
type diskSlowRecorder struct {
	mu    sync.Mutex
	stats DiskSlowSnapshot
}

func newDiskSlowRecorder() *diskSlowRecorder {
	return &diskSlowRecorder{}
}

func (r *diskSlowRecorder) observe(info vfs.DiskSlowInfo) {
	d := int64(info.Duration)
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.HasSuffix(info.Path, ".log") {
		r.stats.WALEvents++
		r.stats.WALMaxNanos = max(r.stats.WALMaxNanos, d)
		return
	}
	r.stats.OtherEvents++
	r.stats.OtherMaxNanos = max(r.stats.OtherMaxNanos, d)
}

func (r *diskSlowRecorder) snapshot() DiskSlowSnapshot {
	if r == nil {
		return DiskSlowSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// walFsyncSummary reads Pebble's nanosecond fsync histogram without retaining
// per-bucket data in callers.
func walFsyncSummary(h prometheus.Histogram) WALFsyncSnapshot {
	if h == nil {
		return WALFsyncSnapshot{}
	}
	var m dto.Metric
	if err := h.Write(&m); err != nil || m.Histogram == nil {
		return WALFsyncSnapshot{}
	}
	hist := m.Histogram
	total := hist.GetSampleCount()
	if total == 0 {
		return WALFsyncSnapshot{}
	}
	// atMost returns observations in buckets whose upper bound <= limit.
	atMost := func(limit time.Duration) uint64 {
		var cumulative uint64
		for _, b := range hist.GetBucket() {
			if b.GetUpperBound() > float64(limit) {
				break
			}
			cumulative = b.GetCumulativeCount()
		}
		return cumulative
	}
	return WALFsyncSnapshot{
		Count:     total,
		SumNanos:  int64(hist.GetSampleSum()),
		Over100ms: total - atMost(100*time.Millisecond),
		Over1s:    total - atMost(time.Second),
		Over5s:    total - atMost(5*time.Second),
	}
}
