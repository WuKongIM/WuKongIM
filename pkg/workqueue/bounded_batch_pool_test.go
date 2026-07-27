package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestBoundedBatchPoolPublishesOwnedPoolState(t *testing.T) {
	registry := goruntimeregistry.New()
	started := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:       "webhook",
		Goroutines: registry,
		Task:       goruntimeregistry.TaskWebhookNotify,
		Workers:    1,
		QueueSize:  1,
	}, func(context.Context, []int) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "owned batch start")

	task := waitForOwnedPoolSnapshot(t, registry, goruntimeregistry.TaskWebhookNotify)
	if task.Active != 3 {
		t.Fatalf("Active = %d, want dispatcher + ants worker + ants maintenance = 3", task.Active)
	}
	if task.PoolCapacity != 1 {
		t.Fatalf("PoolCapacity = %d, want 1", task.PoolCapacity)
	}

	close(release)
	if err := pool.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := registry.Group(goruntimeregistry.ModuleWebhook).Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestBoundedBatchPoolBatchesAdjacentItems(t *testing.T) {
	batches := make(chan []int, 2)
	collecting := make(chan struct{})
	var collectingOnce sync.Once
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   1,
		QueueSize: 8,
		Policy: func(first int) BatchOptions {
			if first == 1 {
				collectingOnce.Do(func() { close(collecting) })
			}
			return BatchOptions{MaxItems: 2, MaxWait: 200 * time.Millisecond}
		},
	}, func(ctx context.Context, items []int) error {
		batches <- append([]int(nil), items...)
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	defer func() {
		closeBatchPoolForTest(t, pool)
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, collecting, "first item collecting window")
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}

	select {
	case got := <-batches:
		if len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("batch = %#v, want [1 2]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for batch")
	}
}

func TestBoundedBatchPoolFlushesPartialBatchAfterMaxWait(t *testing.T) {
	batches := make(chan []int, 1)
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   1,
		QueueSize: 4,
		Policy: func(first int) BatchOptions {
			return BatchOptions{MaxItems: 2, MaxWait: 10 * time.Millisecond}
		},
	}, func(ctx context.Context, items []int) error {
		batches <- append([]int(nil), items...)
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	defer func() {
		closeBatchPoolForTest(t, pool)
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(single item) error = %v", err)
	}

	select {
	case got := <-batches:
		if len(got) != 1 || got[0] != 1 {
			t.Fatalf("batch = %#v, want [1]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for partial batch flush")
	}
}

func TestBoundedBatchPoolRejectsFullAdmission(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   1,
		QueueSize: 1,
		Policy:    func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		if items[0] == 1 {
			close(started)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	defer func() {
		close(release)
		closeBatchPoolForTest(t, pool)
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "first batch start")
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	if err := pool.Submit(context.Background(), 3); !errors.Is(err, ErrFull) {
		t.Fatalf("Submit(third) error = %v, want ErrFull", err)
	}
}

func TestBoundedBatchPoolCancelAcceptedOnCloseCancelsQueuedItemsWithoutCancelingRunningBatch(t *testing.T) {
	type canceledItem struct {
		item int
		err  error
	}

	var (
		canceledMu  sync.Mutex
		canceled    []canceledItem
		signalOnce  sync.Once
		releaseOnce sync.Once
	)
	started := make(chan struct{})
	release := make(chan struct{})
	runningErr := make(chan error, 1)
	cancelAccepted := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:                  "batch",
		Workers:               1,
		QueueSize:             2,
		CancelAcceptedOnClose: true,
		CancelAccepted: func(item int, err error) {
			canceledMu.Lock()
			canceled = append(canceled, canceledItem{item: item, err: err})
			canceledMu.Unlock()
			if item == 2 && errors.Is(err, ErrClosed) {
				signalOnce.Do(func() { close(cancelAccepted) })
			}
		},
		Policy: func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		if items[0] == 1 {
			close(started)
			<-release
			runningErr <- ctx.Err()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	releaseHandler := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseHandler()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "first batch start")
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- pool.Close(ctx)
	}()

	waitForBatchPoolSignal(t, cancelAccepted, "queued item cancellation")
	releaseHandler()

	if err := waitForBatchPoolClose(t, closeDone); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-runningErr:
		if err != nil {
			t.Fatalf("running batch context error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for running batch context state")
	}
	canceledMu.Lock()
	got := append([]canceledItem(nil), canceled...)
	canceledMu.Unlock()
	if len(got) != 1 {
		t.Fatalf("canceled items = %#v, want only item 2", got)
	}
	if got[0].item != 2 || !errors.Is(got[0].err, ErrClosed) {
		t.Fatalf("canceled item = %#v, want item 2 with ErrClosed", got[0])
	}
}

func TestBoundedBatchPoolCancelRunningOnCloseCancelsRunningBatch(t *testing.T) {
	started := make(chan struct{})
	runningErr := make(chan error, 1)
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:                 "batch",
		Workers:              1,
		QueueSize:            1,
		CancelRunningOnClose: true,
		Policy:               func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		close(started)
		<-ctx.Done()
		runningErr <- ctx.Err()
		return ctx.Err()
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "first batch start")

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- pool.Close(context.Background())
	}()

	select {
	case err := <-runningErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("running batch context error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for running batch context cancellation")
	}
	if err := waitForBatchPoolClose(t, closeDone); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestBoundedBatchPoolCloseDrainsAcceptedWorkAndRejectsNewSubmissions(t *testing.T) {
	var processed atomic.Int64
	var releaseOnce sync.Once
	started := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   1,
		QueueSize: 3,
		Policy:    func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		if items[0] == 1 {
			close(started)
			<-release
		}
		processed.Add(int64(len(items)))
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	releaseHandler := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseHandler()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "first batch start")
	for i := 2; i <= 4; i++ {
		if err := pool.Submit(context.Background(), i); err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- pool.Close(ctx)
	}()

	waitForBatchPoolAdmissionClosed(t, closeDone, func() error {
		return pool.Submit(context.Background(), 5)
	})

	releaseHandler()
	if err := waitForBatchPoolClose(t, closeDone); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := processed.Load(); got != 4 {
		t.Fatalf("processed items = %d, want 4", got)
	}
	if err := pool.Submit(context.Background(), 5); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit(after close) error = %v, want ErrClosed", err)
	}
}

func TestBoundedBatchPoolLimitsConcurrentBatchesToWorkers(t *testing.T) {
	const workers = 4

	var running atomic.Int64
	var peak atomic.Int64
	var processed atomic.Int64
	var doneOnce sync.Once
	var releaseOnce sync.Once
	entered := make(chan int, 5)
	release := make(chan struct{})
	done := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   workers,
		QueueSize: 32,
		Policy:    func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		current := running.Add(1)
		updateBatchPoolPeak(&peak, current)
		entered <- items[0]
		<-release
		running.Add(-1)
		if processed.Add(int64(len(items))) == 5 {
			doneOnce.Do(func() { close(done) })
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	releaseBatches := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer closeBatchPoolForTest(t, pool)
	defer releaseBatches()

	for i := 1; i <= 5; i++ {
		if err := pool.Submit(context.Background(), i); err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
	}
	for i := 0; i < workers; i++ {
		waitForBatchPoolItem(t, entered, "worker batch entry")
	}

	if got := peak.Load(); got != workers {
		t.Fatalf("peak running batches = %d, want %d", got, workers)
	}
	select {
	case item := <-entered:
		t.Fatalf("item %d entered before releasing workers; want fifth item blocked", item)
	case <-time.After(20 * time.Millisecond):
	}
	releaseBatches()
	waitForBatchPoolSignal(t, done, "all batches processed")
}

func TestBoundedBatchPoolDefaultsToSingleItemPolicy(t *testing.T) {
	batches := make(chan []int, 2)
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   1,
		QueueSize: 4,
	}, func(ctx context.Context, items []int) error {
		batches <- append([]int(nil), items...)
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	defer func() {
		closeBatchPoolForTest(t, pool)
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}

	first := waitForBatchPoolBatch(t, batches, "first default batch")
	second := waitForBatchPoolBatch(t, batches, "second default batch")
	for i, batch := range [][]int{first, second} {
		if len(batch) != 1 {
			t.Fatalf("batch %d = %#v, want single item batch", i+1, batch)
		}
	}
	if first[0]+second[0] != 3 {
		t.Fatalf("default batches = %#v and %#v, want items 1 and 2", first, second)
	}
}

func TestBoundedBatchPoolRejectsInvalidConfig(t *testing.T) {
	handler := func(ctx context.Context, items []int) error {
		return nil
	}
	tests := []struct {
		name    string
		cfg     BoundedBatchPoolConfig[int]
		handler func(context.Context, []int) error
	}{
		{
			name:    "workers zero",
			cfg:     BoundedBatchPoolConfig[int]{Name: "batch", Workers: 0, QueueSize: 1},
			handler: handler,
		},
		{
			name:    "queue size zero",
			cfg:     BoundedBatchPoolConfig[int]{Name: "batch", Workers: 1, QueueSize: 0},
			handler: handler,
		},
		{
			name:    "nil handler",
			cfg:     BoundedBatchPoolConfig[int]{Name: "batch", Workers: 1, QueueSize: 1},
			handler: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewBoundedBatchPool[int](tt.cfg, tt.handler)
			if pool != nil {
				t.Fatalf("NewBoundedBatchPool() pool = %#v, want nil", pool)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewBoundedBatchPool() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func updateBatchPoolPeak(peak *atomic.Int64, value int64) {
	for {
		current := peak.Load()
		if value <= current {
			return
		}
		if peak.CompareAndSwap(current, value) {
			return
		}
	}
}

type batchPoolCloser interface {
	Close(context.Context) error
}

func closeBatchPoolForTest(t *testing.T, pool batchPoolCloser) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func waitForBatchPoolClose(t *testing.T, closeDone <-chan error) error {
	t.Helper()

	select {
	case err := <-closeDone:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for batch pool close")
	}
	return nil
}

func waitForBatchPoolAdmissionClosed(t *testing.T, closeDone <-chan error, submit func() error) {
	t.Helper()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case err := <-closeDone:
			t.Fatalf("Close() returned before handler release while checking admission closure: %v", err)
		default:
		}

		err := submit()
		if errors.Is(err, ErrClosed) {
			return
		}
		if err == nil {
			t.Fatal("Submit(during close) accepted new item, want ErrClosed")
		}
		if !errors.Is(err, ErrFull) {
			t.Fatalf("Submit(during close) error = %v, want ErrClosed", err)
		}

		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for admission to close while handler was blocked")
		case <-tick.C:
		}
	}
}

func waitForBatchPoolSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForBatchPoolItem(t *testing.T, ch <-chan int, name string) int {
	t.Helper()

	select {
	case item := <-ch:
		return item
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
	return 0
}

func waitForBatchPoolBatch(t *testing.T, ch <-chan []int, name string) []int {
	t.Helper()

	select {
	case batch := <-ch:
		return batch
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
	return nil
}
