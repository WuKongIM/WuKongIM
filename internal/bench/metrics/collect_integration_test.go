//go:build integration

package metrics

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// BenchmarkCollectWithConcurrentWriter measures real lock contention while
// snapshots sort a fixed history. The paced writer keeps bounded statistics.
func BenchmarkCollectWithConcurrentWriter(b *testing.B) {
	for _, samples := range []int{30000, 270000, 810000} {
		b.Run(fmt.Sprintf("Samples%d", samples), func(b *testing.B) {
			r := NewRegistry()
			rng := rand.New(rand.NewPCG(42, 17))
			values := make([]time.Duration, samples)
			for i := range values {
				values[i] = time.Duration(rng.Int64N(int64(time.Second)))
			}
			r.latencies["group_recv_latency_seconds{phase=run}"] = values
			type observation struct {
				count, slow uint64
				total, max  time.Duration
			}
			stop := make(chan struct{})
			done := make(chan observation, 1)
			ready := make(chan struct{})
			defer func() {
				select {
				case <-stop:
				default:
					close(stop)
					<-done
				}
			}()
			go func() {
				var obs observation
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				close(ready)
				for {
					select {
					case <-stop:
						done <- obs
						return
					case <-ticker.C:
						start := time.Now()
						r.IncCounter("send_attempt_total", nil)
						elapsed := time.Since(start)
						obs.count++
						obs.total += elapsed
						obs.max = max(obs.max, elapsed)
						if elapsed >= time.Millisecond {
							obs.slow++
						}
					}
				}
			}()
			<-ready
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				snapshot := r.Collect()
				if snapshot.Histograms["group_recv_latency_seconds{phase=run}"].Count != uint64(samples) {
					b.Fatal("lost latency samples")
				}
			}
			b.StopTimer()
			close(stop)
			obs := <-done
			if obs.count == 0 {
				b.Fatal("no concurrent writer observations")
			}
			b.ReportMetric(float64(obs.max)/float64(time.Microsecond), "writer-max-us")
			b.ReportMetric(float64(obs.total)/float64(obs.count)/float64(time.Microsecond), "writer-mean-us")
			b.ReportMetric(float64(obs.slow), "writer-over-1ms")
			b.ReportMetric(float64(obs.count), "writer-observations")
		})
	}
}
