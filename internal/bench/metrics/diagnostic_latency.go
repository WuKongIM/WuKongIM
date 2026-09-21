package metrics

import (
	"math"
	"sort"
	"time"
)

// Diagnostic stage histograms retain fixed bucket counts, never per-message
// samples. Their percentile estimates are upper bounds, not SLO inputs.
var diagnosticLatencyBounds = [...]time.Duration{
	0, time.Microsecond, 2 * time.Microsecond, 5 * time.Microsecond, 10 * time.Microsecond, 20 * time.Microsecond, 50 * time.Microsecond,
	100 * time.Microsecond, 200 * time.Microsecond, 500 * time.Microsecond, time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond,
	10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond, 200 * time.Millisecond,
	250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, time.Duration(1<<63 - 1),
}

func isDiagnosticLatency(name string) bool {
	switch name {
	case "workload_dispatch_lag_seconds", "workload_send_submit_seconds", "workload_sendack_wait_seconds", "workload_operation_seconds":
		return true
	default:
		return false
	}
}

type diagnosticLatency struct {
	buckets [len(diagnosticLatencyBounds)]uint64
	summary HistogramSummary
}

// observe updates fixed buckets and exact totals; callers serialize access.
func (h *diagnosticLatency) observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	i := sort.Search(len(diagnosticLatencyBounds), func(i int) bool { return d <= diagnosticLatencyBounds[i] })
	h.buckets[i]++
	v := d.Seconds()
	if h.summary.Count == 0 || v < h.summary.MinSeconds {
		h.summary.MinSeconds = v
	}
	h.summary.Count++
	h.summary.SumSeconds += v
	h.summary.MaxSeconds = max(h.summary.MaxSeconds, v)
}

// collect estimates quantiles with bucket upper bounds, capped by the observed maximum.
func (h *diagnosticLatency) collect() HistogramSummary {
	s := h.summary
	quantile := func(q float64) float64 {
		target := uint64(math.Ceil(float64(s.Count) * q))
		var count uint64
		for i, n := range h.buckets {
			count += n
			if count >= target {
				return min(diagnosticLatencyBounds[i].Seconds(), s.MaxSeconds)
			}
		}
		return s.MaxSeconds
	}
	s.P50Seconds = quantile(.50)
	s.P95Seconds = quantile(.95)
	s.P99Seconds = quantile(.99)
	s.PercentilesAreUpperBounds = true
	return s
}
