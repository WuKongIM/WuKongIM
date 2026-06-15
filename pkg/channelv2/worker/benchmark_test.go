package worker

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

const benchmarkWorkerBurst = 512

func BenchmarkWorkerPoolSubmitAndRun(b *testing.B) {
	for _, tc := range []struct {
		name    string
		workers int
	}{
		{name: "workers1", workers: 1},
		{name: "workers16", workers: 16},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkWorkerPoolSubmitAndRun(b, tc.workers, nil)
		})
	}
}

func BenchmarkWorkerPoolObserverOverhead(b *testing.B) {
	b.Run("none", func(b *testing.B) {
		benchmarkWorkerPoolSubmitAndRun(b, 16, nil)
	})
	b.Run("recording", func(b *testing.B) {
		benchmarkWorkerPoolSubmitAndRun(b, 16, &benchmarkWorkerObserver{})
	})
}

func BenchmarkWorkerPoolFullReject(b *testing.B) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "bench-full", Workers: 1, QueueSize: 1}, Deps{}, sink)
	if err != nil {
		b.Fatalf("NewPool() error = %v", err)
	}
	block := make(chan struct{})
	started := make(chan struct{})
	release := sync.Once{}
	defer func() {
		release.Do(func() { close(block) })
		_ = pool.Close()
	}()

	if err := pool.Submit(context.Background(), benchmarkWorkerTask(1, func(context.Context) Result {
		close(started)
		<-block
		return Result{Kind: TaskFunc}
	})); err != nil {
		b.Fatalf("Submit(blocking) error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		b.Fatal("timed out waiting for blocking worker task")
	}
	if err := pool.Submit(context.Background(), benchmarkWorkerTask(2, nil)); err != nil {
		b.Fatalf("Submit(queued) error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := pool.Submit(context.Background(), benchmarkWorkerTask(3, nil)); !errors.Is(err, ch.ErrBackpressured) {
			b.Fatalf("Submit(full) error = %v, want ErrBackpressured", err)
		}
	}
	b.StopTimer()
	release.Do(func() { close(block) })
}

func BenchmarkWorkerPoolStoreAppendBatch(b *testing.B) {
	stores := &batchAppendStoreFactory{}
	sink := &benchmarkWorkerSink{}
	pool, err := NewPool(PoolConfig{Name: "bench-store-append", Workers: 1, QueueSize: 64 * 1024, BatchMaxWait: time.Nanosecond}, Deps{Stores: stores}, sink)
	if err != nil {
		b.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkWorkerMin(benchmarkWorkerBurst, b.N-submitted)
		sink.expect(burst)
		for i := 0; i < burst; i++ {
			idx := submitted + i
			if err := pool.Submit(context.Background(), benchmarkStoreAppendTask(idx)); err != nil {
				b.Fatalf("Submit(%d) error = %v", idx, err)
			}
		}
		sink.wait(b)
		submitted += burst
	}
	b.StopTimer()
	batchCalls := stores.BatchCalls()
	singleAppendCalls := stores.SingleAppendCalls()
	b.ReportMetric(float64(batchCalls)/float64(b.N), "batch-calls/op")
	b.ReportMetric(float64(singleAppendCalls)/float64(b.N), "single-append-calls/op")
	// Go benchmark calibration may run with b.N == 1, which cannot form a multi-item batch.
	if b.N > 1 && batchCalls == 0 {
		b.Fatalf("StoreAppend benchmark did not exercise batch path: batch calls = %d, single append calls = %d", batchCalls, singleAppendCalls)
	}
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func benchmarkWorkerPoolSubmitAndRun(b *testing.B, workers int, observer QueueObserver) {
	b.Helper()
	sink := &benchmarkWorkerSink{}
	pool, err := NewPool(PoolConfig{Name: "bench", Workers: workers, QueueSize: 64 * 1024}, Deps{}, sink)
	if err != nil {
		b.Fatalf("NewPool() error = %v", err)
	}
	if observer != nil {
		pool.SetQueueObserver(observer)
	}
	defer pool.Close()
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkWorkerMin(benchmarkWorkerBurst, b.N-submitted)
		sink.expect(burst)
		for i := 0; i < burst; i++ {
			if err := pool.Submit(context.Background(), benchmarkWorkerTask(submitted+i, nil)); err != nil {
				b.Fatalf("Submit(%d) error = %v", submitted+i, err)
			}
		}
		sink.wait(b)
		submitted += burst
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func benchmarkWorkerTask(i int, run func(context.Context) Result) Task {
	fence := ch.Fence{ChannelKey: ch.ChannelKey("bench:" + strconv.Itoa(i+1)), OpID: ch.OpID(i + 1)}
	if run == nil {
		run = func(context.Context) Result {
			return Result{Kind: TaskFunc, Fence: fence}
		}
	}
	return Task{Kind: TaskFunc, Fence: fence, RunFunc: run}
}

func benchmarkStoreAppendTask(i int) Task {
	channel := "bench-store-" + strconv.Itoa(i+1)
	return Task{
		Kind:  TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: ch.ChannelKey("1:" + channel), OpID: ch.OpID(i + 1)},
		StoreAppend: &StoreAppendTask{
			ChannelID: ch.ChannelID{ID: channel, Type: 1},
			Records:   []ch.Record{{ID: uint64(i + 1), Payload: []byte("bench")}},
		},
	}
}

func benchmarkWorkerMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type benchmarkWorkerSink struct {
	wg sync.WaitGroup
}

func (s *benchmarkWorkerSink) expect(count int) {
	s.wg.Add(count)
}

func (s *benchmarkWorkerSink) Complete(Result) {
	s.wg.Done()
}

func (s *benchmarkWorkerSink) wait(b *testing.B) {
	b.Helper()
	s.wg.Wait()
}

type benchmarkWorkerObserver struct {
	count atomic.Uint64
}

func (o *benchmarkWorkerObserver) SetWorkerQueueDepth(string, int) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) SetWorkerQueueCapacity(string, int) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) SetWorkerWorkers(string, int) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) ObserveWorkerAdmission(string, string) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) ObserveWorkerWait(string, TaskKind, time.Duration) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) ObserveWorkerTask(string, TaskKind, error, time.Duration) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) ObserveWorkerBatch(string, TaskKind, int, error) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) SetWorkerInflight(string, int) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) SetWorkerInflightPeak(string, int) {
	o.count.Add(1)
}

func (o *benchmarkWorkerObserver) SetWorkerAntsPoolUsage(string, int, int, int) {
	o.count.Add(1)
}
