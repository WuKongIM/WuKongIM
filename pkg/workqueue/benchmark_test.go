package workqueue

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const benchmarkBurst = 512

func BenchmarkBoundedPoolSubmitAndRun(b *testing.B) {
	for _, tc := range []struct {
		name    string
		workers int
	}{
		{name: "workers1", workers: 1},
		{name: "workers16", workers: 16},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkBoundedPoolSubmitAndRun(b, tc.workers, nil)
		})
	}
}

func BenchmarkBoundedPoolSubmitWaitParallel(b *testing.B) {
	var processed atomic.Uint64
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:      "bench",
		Workers:   maxBenchmarkInt(4, runtime.GOMAXPROCS(0)),
		QueueSize: 64 * 1024,
	}, func(context.Context, int) error {
		processed.Add(1)
		return nil
	})
	if err != nil {
		b.Fatalf("NewBoundedPool() error = %v", err)
	}
	ctx := context.Background()
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := pool.SubmitWait(ctx, 1); err != nil {
				b.Errorf("SubmitWait() error = %v", err)
				return
			}
		}
	})
	benchmarkWaitUntil(b, func() bool { return processed.Load() >= uint64(b.N) })
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
	if err := pool.Close(context.Background()); err != nil {
		b.Fatalf("Close() error = %v", err)
	}
}

func BenchmarkBoundedPoolFullReject(b *testing.B) {
	entered := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:      "bench",
		Workers:   1,
		QueueSize: 1,
	}, func(ctx context.Context, item int) error {
		if item == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	if err != nil {
		b.Fatalf("NewBoundedPool() error = %v", err)
	}
	ctx := context.Background()
	if err := pool.Submit(ctx, 1); err != nil {
		b.Fatalf("Submit(blocking) error = %v", err)
	}
	<-entered
	if err := pool.Submit(ctx, 2); err != nil {
		b.Fatalf("Submit(queued) error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := pool.Submit(ctx, 3); !errors.Is(err, ErrFull) {
			b.Fatalf("Submit(full) error = %v, want ErrFull", err)
		}
	}
	b.StopTimer()
	close(release)
	if err := pool.Close(context.Background()); err != nil {
		b.Fatalf("Close() error = %v", err)
	}
}

func BenchmarkBoundedBatchPoolFullReject(b *testing.B) {
	entered := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "bench-batch",
		Workers:   1,
		QueueSize: 1,
		Policy:    func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		if items[0] == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	if err != nil {
		b.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	ctx := context.Background()
	if err := pool.Submit(ctx, 1); err != nil {
		b.Fatalf("Submit(blocking) error = %v", err)
	}
	<-entered
	if err := pool.Submit(ctx, 2); err != nil {
		b.Fatalf("Submit(queued) error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := pool.Submit(ctx, 3); !errors.Is(err, ErrFull) {
			b.Fatalf("Submit(full) error = %v, want ErrFull", err)
		}
	}
	b.StopTimer()
	close(release)
	if err := pool.Close(context.Background()); err != nil {
		b.Fatalf("Close() error = %v", err)
	}
}

func BenchmarkBoundedPoolObserverOverhead(b *testing.B) {
	b.Run("none", func(b *testing.B) {
		benchmarkBoundedPoolSubmitAndRun(b, 16, nil)
	})
	b.Run("atomic", func(b *testing.B) {
		benchmarkBoundedPoolSubmitAndRun(b, 16, &benchmarkWorkqueueObserver{})
	})
}

func BenchmarkShardedMailboxSubmitAndDrain(b *testing.B) {
	for _, tc := range []struct {
		name       string
		shards     int
		workers    int
		batchItems int
		hotShard   bool
	}{
		{name: "hot_shard_batch1", shards: 1, workers: 1, batchItems: 1, hotShard: true},
		{name: "hot_shard_batch64", shards: 1, workers: 1, batchItems: 64, hotShard: true},
		{name: "many_shards_batch1", shards: 64, workers: 16, batchItems: 1},
		{name: "many_shards_batch64", shards: 64, workers: 16, batchItems: 64},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkShardedMailboxSubmitAndDrain(b, shardedMailboxBenchConfig{
				shards:     tc.shards,
				workers:    tc.workers,
				batchItems: tc.batchItems,
				hotShard:   tc.hotShard,
			}, nil)
		})
	}
}

func BenchmarkShardedMailboxSubmitHashVsSubmitString(b *testing.B) {
	b.Run("submit_hash", func(b *testing.B) {
		benchmarkShardedMailboxSubmitAndDrain(b, shardedMailboxBenchConfig{
			shards:     64,
			workers:    16,
			batchItems: 32,
			useHash:    true,
		}, nil)
	})
	b.Run("submit_string", func(b *testing.B) {
		benchmarkShardedMailboxSubmitAndDrain(b, shardedMailboxBenchConfig{
			shards:     64,
			workers:    16,
			batchItems: 32,
			useString:  true,
		}, nil)
	})
}

func BenchmarkShardedMailboxFullReject(b *testing.B) {
	entered := make(chan struct{})
	release := make(chan struct{})
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "bench",
		Shards:            1,
		Workers:           1,
		QueueSizePerShard: 1,
		BatchMaxItems:     1,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		if batch.Items[0] == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	if err != nil {
		b.Fatalf("NewShardedMailbox() error = %v", err)
	}
	ctx := context.Background()
	if err := mailbox.SubmitHash(ctx, 1, 1); err != nil {
		b.Fatalf("Submit(blocking) error = %v", err)
	}
	<-entered
	if err := mailbox.SubmitHash(ctx, 1, 2); err != nil {
		b.Fatalf("Submit(queued) error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mailbox.SubmitHash(ctx, 1, 3); !errors.Is(err, ErrFull) {
			b.Fatalf("Submit(full) error = %v, want ErrFull", err)
		}
	}
	b.StopTimer()
	close(release)
	if err := mailbox.Close(context.Background()); err != nil {
		b.Fatalf("Close() error = %v", err)
	}
}

func BenchmarkShardedMailboxObserverOverhead(b *testing.B) {
	b.Run("none", func(b *testing.B) {
		benchmarkShardedMailboxSubmitAndDrain(b, shardedMailboxBenchConfig{
			shards:     64,
			workers:    16,
			batchItems: 32,
		}, nil)
	})
	b.Run("atomic", func(b *testing.B) {
		benchmarkShardedMailboxSubmitAndDrain(b, shardedMailboxBenchConfig{
			shards:     64,
			workers:    16,
			batchItems: 32,
		}, &benchmarkWorkqueueObserver{})
	})
}

func BenchmarkShardedMailboxOverloadedScheduler(b *testing.B) {
	benchmarkShardedMailboxSubmitAndDrain(b, shardedMailboxBenchConfig{
		shards:     128,
		workers:    4,
		batchItems: 1,
	}, nil)
}

func benchmarkBoundedPoolSubmitAndRun(b *testing.B, workers int, observer BoundedPoolObserver) {
	b.Helper()
	var wg sync.WaitGroup
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:      "bench",
		Workers:   workers,
		QueueSize: 64 * 1024,
		Observer:  observer,
	}, func(context.Context, int) error {
		wg.Done()
		return nil
	})
	if err != nil {
		b.Fatalf("NewBoundedPool() error = %v", err)
	}
	ctx := context.Background()
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkMin(benchmarkBurst, b.N-submitted)
		wg.Add(burst)
		for i := 0; i < burst; i++ {
			if err := pool.Submit(ctx, submitted+i); err != nil {
				b.Fatalf("Submit(%d) error = %v", submitted+i, err)
			}
		}
		wg.Wait()
		submitted += burst
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
	if err := pool.Close(context.Background()); err != nil {
		b.Fatalf("Close() error = %v", err)
	}
}

type shardedMailboxBenchConfig struct {
	shards     int
	workers    int
	batchItems int
	hotShard   bool
	useHash    bool
	useString  bool
}

func benchmarkShardedMailboxSubmitAndDrain(b *testing.B, cfg shardedMailboxBenchConfig, observer ShardedMailboxObserver) {
	b.Helper()
	var wg sync.WaitGroup
	if cfg.shards <= 0 {
		cfg.shards = 1
	}
	if cfg.workers <= 0 {
		cfg.workers = 1
	}
	if cfg.batchItems <= 0 {
		cfg.batchItems = 1
	}
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "bench",
		Shards:            cfg.shards,
		Workers:           cfg.workers,
		QueueSizePerShard: 64 * 1024,
		BatchMaxItems:     cfg.batchItems,
		BatchMaxWait:      -time.Nanosecond,
		Observer:          observer,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		for range batch.Items {
			wg.Done()
		}
		return nil
	})
	if err != nil {
		b.Fatalf("NewShardedMailbox() error = %v", err)
	}
	ctx := context.Background()
	keys := benchmarkMailboxKeys(cfg.shards)
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkMin(benchmarkBurst, b.N-submitted)
		wg.Add(burst)
		for i := 0; i < burst; i++ {
			idx := submitted + i
			hash := uint64(idx)
			if cfg.hotShard {
				hash = 1
			}
			var err error
			switch {
			case cfg.useString:
				err = mailbox.Submit(ctx, keys[idx%len(keys)], idx)
			case cfg.useHash:
				err = mailbox.SubmitHash(ctx, hash, idx)
			default:
				err = mailbox.SubmitHash(ctx, hash, idx)
			}
			if err != nil {
				b.Fatalf("Submit(%d) error = %v", idx, err)
			}
		}
		wg.Wait()
		submitted += burst
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
	if err := mailbox.Close(context.Background()); err != nil {
		b.Fatalf("Close() error = %v", err)
	}
}

func benchmarkMailboxKeys(count int) []string {
	keys := make([]string, count)
	for i := range keys {
		keys[i] = "bench-key-" + strconv.Itoa(i)
	}
	return keys
}

func benchmarkWaitUntil(b *testing.B, condition func() bool) {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Microsecond)
	}
	b.Fatal("benchmark condition not met before timeout")
}

func benchmarkMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxBenchmarkInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type benchmarkWorkqueueObserver struct {
	count atomic.Uint64
}

func (o *benchmarkWorkqueueObserver) ObserveBoundedPool(BoundedPoolObservation) {
	o.count.Add(1)
}

func (o *benchmarkWorkqueueObserver) ObserveShardedMailbox(ShardedMailboxObservation) {
	o.count.Add(1)
}
