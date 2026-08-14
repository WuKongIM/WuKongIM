package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRuntimePressureRevisionedQueueRejectsOlderAbsoluteGauge(t *testing.T) {
	m := newRuntimePressureMetrics(prometheus.NewRegistry(), nil)
	m.SetQueueRevisioned("gateway", "actor_ready", "ready", "none", 2, RuntimePressureQueueObservation{Depth: 0, Capacity: 1024})
	m.SetQueueRevisioned("gateway", "actor_ready", "ready", "none", 1, RuntimePressureQueueObservation{Depth: 2, Capacity: 1024})

	series := m.boundQueueSeries("gateway", "actor_ready", "ready", "none")
	if got := testutil.ToFloat64(series.depth); got != 0 {
		t.Fatalf("revisioned queue depth = %v, want latest depth 0", got)
	}
}

func TestRuntimePressureUpdatesReuseBoundSeries(t *testing.T) {
	m := newRuntimePressureMetrics(prometheus.NewRegistry(), nil)
	update := func() {
		m.SetPoolWorkers("transport", "scheduler", 4)
		m.SetPoolInflight("transport", "scheduler", 2)
		m.SetQueue("transport", "scheduler", "scheduler", "rpc", RuntimePressureQueueObservation{
			Depth:         3,
			Capacity:      1024,
			Bytes:         384,
			BytesCapacity: 1 << 20,
		})
		m.ObserveAdmission("transport", "scheduler", "scheduler", "rpc", "ok")
		m.ObserveQueueWait("transport", "scheduler", "scheduler", "rpc", "ok", time.Millisecond)
		m.ObserveTaskDuration("transport", "scheduler", "write", "ok", time.Millisecond)
	}

	update()
	allocs := testing.AllocsPerRun(1000, update)
	if allocs != 0 {
		t.Fatalf("allocs per warmed runtime pressure update = %.2f, want 0", allocs)
	}
}

func TestRuntimePressureUpdatesReuseBoundSeriesConcurrently(t *testing.T) {
	const (
		goroutines = 16
		iterations = 100
	)
	m := newRuntimePressureMetrics(prometheus.NewRegistry(), nil)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				m.SetPoolWorkers("transport", "scheduler", 4)
				m.SetPoolInflight("transport", "scheduler", 2)
				m.SetQueue("transport", "scheduler", "scheduler", "rpc", RuntimePressureQueueObservation{
					Depth:         3,
					Capacity:      1024,
					Bytes:         384,
					BytesCapacity: 1 << 20,
				})
				m.ObserveAdmission("transport", "scheduler", "scheduler", "rpc", "ok")
				m.ObserveQueueWait("transport", "scheduler", "scheduler", "rpc", "ok", time.Millisecond)
				m.ObserveTaskDuration("transport", "scheduler", "write", "ok", time.Millisecond)
			}
		}()
	}
	wg.Wait()

	m.seriesMu.RLock()
	defer m.seriesMu.RUnlock()
	if len(m.poolSeries) != 1 || len(m.queueSeries) != 1 || len(m.admissionSeries) != 1 || len(m.waitSeries) != 1 || len(m.taskSeries) != 1 {
		t.Fatalf("bound series cache sizes = pool:%d queue:%d admission:%d wait:%d task:%d, want one each",
			len(m.poolSeries), len(m.queueSeries), len(m.admissionSeries), len(m.waitSeries), len(m.taskSeries))
	}
}

func BenchmarkRuntimePressureWarmedSeries(b *testing.B) {
	m := newRuntimePressureMetrics(prometheus.NewRegistry(), nil)
	observation := RuntimePressureQueueObservation{
		Depth:         3,
		Capacity:      1024,
		Bytes:         384,
		BytesCapacity: 1 << 20,
	}
	update := func() {
		m.SetPoolWorkers("transport", "scheduler", 4)
		m.SetPoolInflight("transport", "scheduler", 2)
		m.SetQueue("transport", "scheduler", "scheduler", "rpc", observation)
		m.ObserveAdmission("transport", "scheduler", "scheduler", "rpc", "ok")
		m.ObserveQueueWait("transport", "scheduler", "scheduler", "rpc", "ok", time.Millisecond)
		m.ObserveTaskDuration("transport", "scheduler", "write", "ok", time.Millisecond)
	}
	update()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		update()
	}
}
