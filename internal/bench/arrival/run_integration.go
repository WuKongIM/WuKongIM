//go:build integration

package arrival

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run schedules a finite open-loop load. Operation must honor its deadline;
// workers are joined before returning and no measured request retries.
func Run(count, rate, workers int, operation func(context.Context, int) error) Result {
	if count <= 0 || count > 1_000_000 || rate <= 0 || rate > 100_000 || workers <= 0 || workers > 4096 {
		panic("invalid bounded arrival configuration")
	}
	r := Result{Samples: make([]Sample, count), Rate: rate}
	// Queue at most one 400ms latency budget of arrivals, independent of workers.
	jobs := make(chan int, max(1, rate*400/1000))
	var wg sync.WaitGroup
	var start time.Time
	ready := make(chan struct{})
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			<-ready
			for index := range jobs {
				planned := start.Add(time.Duration(index) * time.Second / time.Duration(rate))
				began := time.Now()
				sample := &r.Samples[index]
				sample.Started = true
				sample.StartedAt = began.Sub(start)
				sample.Queue = began.Sub(planned)
				ctx, cancel := context.WithDeadline(context.Background(), planned.Add(5*time.Second))
				err := ctx.Err()
				if err == nil {
					err = operation(ctx, index)
				}
				cancel()
				sample.Service = time.Since(began)
				sample.Failed = err != nil
				switch {
				case errors.Is(err, context.DeadlineExceeded):
					sample.FailureKind = "deadline"
				case errors.Is(err, context.Canceled):
					sample.FailureKind = "canceled"
				case err != nil:
					sample.FailureKind = "operation"
				}
				sample.Completed = true
			}
		}()
	}
	start = time.Now()
	close(ready)
	for index := range count {
		due := start.Add(time.Duration(index) * time.Second / time.Duration(rate))
		if delay := time.Until(due); delay > 0 {
			time.Sleep(delay)
		}
		select {
		case jobs <- index:
		default:
			r.Samples[index].Dropped = true
		}
	}
	if delay := time.Until(start.Add(time.Duration(count) * time.Second / time.Duration(rate))); delay > 0 {
		time.Sleep(delay)
	}
	close(jobs)
	wg.Wait()
	r.Duration = time.Since(start)
	return r
}

// QualificationWarmup requires the exact long profile; ordinary diagnostic
// benchmarks keep their requested count and cannot masquerade as qualification.
func QualificationWarmup(b *testing.B, rate int) int {
	if os.Getenv("WK_BENCH_QUALIFY") != "1" {
		return 0
	}
	if rate != 500 || b.N != 90_000 {
		b.Fatalf("500 QPS qualification requires exactly 90000 measured operations, got rate=%d count=%d", rate, b.N)
	}
	return 30_000
}

// Report saves all windows before asserting. The legacy service metrics remain
// separately reported by callers during the scheduled-latency migration.
func Report(b *testing.B, r Result) {
	b.Helper()
	windows := r.Windows(60 * time.Second)
	qualification := os.Getenv("WK_BENCH_QUALIFY") == "1"
	if path := os.Getenv("WK_BENCH_ARRIVAL_REPORT"); path != "" {
		if err := writeReport(path, "measurement", qualification, r); err != nil {
			b.Fatal(err)
		}
	}
	var totalP99, queueP99 float64
	for i, w := range windows {
		totalP99 = max(totalP99, w.TotalP99MS)
		queueP99 = max(queueP99, w.QueueP99MS)
		b.Logf("arrival window %d: %+v", i+1, w)
		// The existing 500 QPS budget also gates the more complete timing definition.
		// Other capacity benchmarks retain their own latency budgets.
		budget := 400.0
		if r.Rate != 500 {
			budget = 1e9
		}
		if failures := w.Failures(budget); len(failures) > 0 {
			b.Errorf("arrival window %d failed: %v", i+1, failures)
		}
	}
	if qualification && len(windows) != 3 {
		b.Error("qualification requires all three 60-second windows")
	}
	b.ReportMetric(totalP99, "scheduled-p99-ms")
	b.ReportMetric(queueP99, "arrival-queue-p99-ms")
	if len(windows) == 0 {
		b.Error(fmt.Errorf("arrival report has no windows"))
	}
}

// ReportWarmup preserves the whole pre-measurement phase even when it fails.
// Warmup is never qualification and is not checked against the measured latency budget.
func ReportWarmup(b testing.TB, r Result) bool {
	b.Helper()
	if path := os.Getenv("WK_BENCH_ARRIVAL_REPORT"); path != "" {
		if err := writeReport(strings.TrimSuffix(path, ".json")+".warmup.json", "warmup", false, r); err != nil {
			b.Fatal(err)
		}
	}
	for i, w := range r.Windows(60 * time.Second) {
		b.Logf("warmup window %d: %+v", i+1, w)
	}
	for _, sample := range r.Samples {
		if sample.Failed || sample.Dropped || !sample.Started || !sample.Completed {
			return false
		}
	}
	return len(r.Samples) > 0
}
