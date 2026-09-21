package metrics

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectAllowsWritesDuringAggregationAndKeepsOneSnapshot(t *testing.T) {
	r := NewRegistry()
	r.IncCounter("send_attempt_total", nil)
	r.SetGauge("active_connections", nil, 1)
	r.ObserveLatency("sendack_latency_seconds", nil, 3*time.Millisecond)
	r.ObserveLatency("workload_operation_seconds", nil, 5*time.Millisecond)
	r.RecordErrorSample("send", errors.New("timeout"))
	snapshot := r.collect(func(values []time.Duration) HistogramSummary {
		// Probe before writing to avoid turning a regression into a deadlock.
		// Writers must progress even while the collector is still aggregating.
		if !r.mu.TryLock() {
			t.Error("snapshot aggregation blocks metric producers by holding the registry lock")
			return summarizeDurations(values)
		}
		r.mu.Unlock()
		r.IncCounter("send_attempt_total", nil)
		r.SetGauge("active_connections", nil, 2)
		r.ObserveLatency("sendack_latency_seconds", nil, time.Second)
		r.ObserveLatency("workload_operation_seconds", nil, time.Second)
		r.RecordErrorSample("send", errors.New("timeout"))
		return summarizeDurations(values)
	})
	require.Equal(t, uint64(1), snapshot.Counters["send_attempt_total"])
	require.Equal(t, float64(1), snapshot.Gauges["active_connections"])
	require.Equal(t, uint64(1), snapshot.Histograms["sendack_latency_seconds"].Count)
	require.Equal(t, .003, snapshot.Histograms["sendack_latency_seconds"].P99Seconds)
	require.Equal(t, uint64(1), snapshot.Histograms["workload_operation_seconds"].Count)
	require.Len(t, snapshot.Errors, 1)
	if t.Failed() {
		return
	}
	later := r.Collect()
	require.Equal(t, uint64(2), later.Counters["send_attempt_total"])
	require.Equal(t, uint64(2), later.Histograms["sendack_latency_seconds"].Count)
	require.Equal(t, uint64(2), later.Histograms["workload_operation_seconds"].Count)
	require.Len(t, later.Errors, 2)
}

func TestCollectOwnsSamplesAcrossConcurrentCollectorsAndWrites(t *testing.T) {
	r := NewRegistry()
	// Spare capacity exercises appends to an existing backing array while a
	// detached snapshot is being sorted, rather than relying on reallocation.
	r.latencies["sendack_latency_seconds"] = make([]time.Duration, 3, 2048)
	copy(r.latencies["sendack_latency_seconds"], []time.Duration{3 * time.Millisecond, time.Millisecond, 2 * time.Millisecond})
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			r.ObserveLatency("sendack_latency_seconds", nil, 4*time.Millisecond)
		}
	}()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 32; j++ {
				h := r.Collect().Histograms["sendack_latency_seconds"]
				require.GreaterOrEqual(t, h.Count, uint64(3))
				require.LessOrEqual(t, h.Count, uint64(1003))
				require.InDelta(t, .006+float64(h.Count-3)*.004, h.SumSeconds, 1e-9)
				require.Equal(t, .001, h.MinSeconds)
			}
		}()
	}
	close(start)
	wg.Wait()
	h := r.Collect().Histograms["sendack_latency_seconds"]
	require.Equal(t, uint64(1003), h.Count)
	require.Equal(t, .004, h.P99Seconds)
	require.Equal(t, []time.Duration{3 * time.Millisecond, time.Millisecond, 2 * time.Millisecond}, r.LatencyValues("sendack_latency_seconds", nil)[:3], "aggregation must not reorder registry-owned samples")
}
