package metrics

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectProgressOwnsScalarsWithoutConsumingReportEvidence(t *testing.T) {
	r := NewRegistry()
	r.AddCounter("send_attempt_total", nil, 3)
	r.SetGauge("logical_remaining", nil, 2)
	values := []time.Duration{3 * time.Millisecond, time.Millisecond, 2 * time.Millisecond}
	for _, value := range values {
		r.ObserveLatency("sendack_latency_seconds", nil, value)
		r.ObserveLatency("workload_operation_seconds", nil, value)
	}
	r.RecordErrorSample("send", errors.New("timeout"))
	before := r.Collect()
	progress := r.CollectProgress()
	require.Equal(t, before.Counters, progress.Counters)
	require.Equal(t, before.Gauges, progress.Gauges)
	require.Empty(t, progress.Histograms)
	require.Empty(t, progress.Errors)
	progress.Counters["send_attempt_total"] = 99
	progress.Gauges["logical_remaining"] = 99
	require.Equal(t, before, r.Collect(), "polling must preserve complete report evidence")
	require.Equal(t, values, r.LatencyValues("sendack_latency_seconds", nil))
	require.Empty(t, (*Registry)(nil).CollectProgress().Counters)
}

func TestCollectProgressDuringReportAggregation(t *testing.T) {
	r := NewRegistry()
	r.ObserveLatency("sendack_latency_seconds", nil, time.Millisecond)
	r.collect(func(values []time.Duration) HistogramSummary {
		// collectMu remains held while producers and progress readers proceed.
		r.IncCounter("send_attempt_total", nil)
		require.Equal(t, uint64(1), r.CollectProgress().Counters["send_attempt_total"])
		return summarizeDurations(values)
	})
}

func TestCollectProgressCapturesCounterAndGaugeAtOneCut(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// Model a coherent producer update; the public recording methods are
			// independently atomic and do not promise a multi-call transaction.
			r.mu.Lock()
			r.counters["send_attempt_total"] = uint64(i)
			r.gauges["logical_remaining"] = float64(i)
			r.mu.Unlock()
		}
	}()
	for i := 0; i < 1000; i++ {
		progress := r.CollectProgress()
		require.Equal(t, float64(progress.Counters["send_attempt_total"]), progress.Gauges["logical_remaining"])
	}
	wg.Wait()
}
