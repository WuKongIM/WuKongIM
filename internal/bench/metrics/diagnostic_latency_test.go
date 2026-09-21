package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiagnosticLatencyRetainsBoundedBucketsAndExactTotals(t *testing.T) {
	r := NewRegistry()
	labels := Labels{"phase": "run"}
	for i := 0; i < 10000; i++ {
		r.ObserveLatency("workload_send_submit_seconds", labels, 3*time.Millisecond)
	}
	r.ObserveLatency("workload_send_submit_seconds", labels, 7*time.Millisecond)
	snap := r.Collect()
	h := snap.Histograms["workload_send_submit_seconds{phase=run}"]
	require.Equal(t, uint64(10001), h.Count)
	require.InDelta(t, 30.007, h.SumSeconds, 1e-8)
	require.InDelta(t, .003, h.MinSeconds, 1e-9)
	require.InDelta(t, .007, h.MaxSeconds, 1e-9)
	require.InDelta(t, .005, h.P99Seconds, 1e-9)
	require.True(t, h.PercentilesAreUpperBounds)
	require.Empty(t, r.latencies, "diagnostics must not retain per-message samples")
	require.Len(t, r.diagnosticLatencies, 1)
	clean := SanitizeSnapshot(snap)
	require.Equal(t, h, clean.Histograms["workload_send_submit_seconds{phase=run}"])
	merged := mergeHistogram(h, HistogramSummary{Count: 1, P99Seconds: .006})
	require.True(t, merged.PercentilesAreUpperBounds)
	require.InDelta(t, .006, merged.P99Seconds, 1e-9)
}

func TestDiagnosticLatencyDoesNotChangeLegacyPercentiles(t *testing.T) {
	r := NewRegistry()
	r.ObserveLatency("group_send_latency_seconds", nil, 3*time.Millisecond)
	h := r.Collect().Histograms["group_send_latency_seconds"]
	require.False(t, h.PercentilesAreUpperBounds)
	require.InDelta(t, .003, h.P99Seconds, 1e-9)
	for _, d := range []time.Duration{-time.Second, 0, time.Duration(1<<63 - 1)} {
		r.ObserveLatency("workload_operation_seconds", nil, d)
	}
	h = r.Collect().Histograms["workload_operation_seconds"]
	require.Zero(t, h.MinSeconds)
	require.Equal(t, uint64(3), h.Count)
	require.Equal(t, h.MaxSeconds, h.P99Seconds)
}

// BenchmarkDiagnosticLatency records hot-path cost separately from retained
// histogram size. Registry observations still construct sanitized series keys.
func BenchmarkDiagnosticLatency(b *testing.B) {
	b.Run("Buckets", func(b *testing.B) {
		var h diagnosticLatency
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			h.observe(3 * time.Millisecond)
		}
	})
	b.Run("Registry", func(b *testing.B) {
		r := NewRegistry()
		labels := Labels{"channel_type": "group", "phase": "run", "profile": "distributed-group", "traffic": "group-send"}
		r.ObserveLatency("workload_send_submit_seconds", labels, 3*time.Millisecond)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			r.ObserveLatency("workload_send_submit_seconds", labels, 3*time.Millisecond)
		}
	})
	b.Run("RegistryParallel", func(b *testing.B) {
		r := NewRegistry()
		labels := Labels{"channel_type": "group", "phase": "run", "profile": "distributed-group", "traffic": "group-send"}
		r.ObserveLatency("workload_send_submit_seconds", labels, 3*time.Millisecond)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r.ObserveLatency("workload_send_submit_seconds", labels, 3*time.Millisecond)
			}
		})
	})
}
