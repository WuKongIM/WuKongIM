# ChannelV2 Worker Workqueue Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate `pkg/channelv2/worker` onto reusable `pkg/workqueue` primitives with mandatory benchmark comparison.

**Architecture:** Add benchmarks first, then add a first-class batched bounded pool primitive to `pkg/workqueue` because the existing single-item `BoundedPool` cannot preserve worker-side coalescing without retaining the old dispatcher. Refactor `pkg/channelv2/worker` to delete its local queue/slot/dispatcher/executor mechanics and keep only typed worker semantics: task grouping, batching, fences, completion sink, and observer mapping.

**Tech Stack:** Go 1.25, `pkg/workqueue`, `pkg/channelv2/worker`, `github.com/panjf2000/ants/v2`, `go test`, `benchstat` when available.

---

## File Structure

- Create `pkg/channelv2/worker/benchmark_test.go`: worker baseline and post-migration benchmarks.
- Create `pkg/workqueue/bounded_batch_pool.go`: reusable batched bounded worker primitive.
- Create `pkg/workqueue/bounded_batch_pool_test.go`: generic primitive tests.
- Modify `pkg/channelv2/worker/pool.go`: replace local queue/executor fields with batched workqueue runtime and observer adapter.
- Modify `pkg/channelv2/worker/batch.go`: remove queue-draining logic; make grouping operate on already collected batches.
- Delete `pkg/channelv2/worker/dispatcher.go`: old dispatcher mechanics.
- Delete `pkg/channelv2/worker/executor.go`: old ants wrapper.
- Modify `pkg/channelv2/worker/pool_test.go`: update tests that construct `Pool` internals directly or assert old close mechanics.
- Modify `pkg/channelv2/worker/FLOW.md`: document the new workqueue-backed flow.
- Create `docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md`: baseline and post-migration benchmark report.

---

### Task 1: Add Worker Benchmarks Before Any Migration

**Files:**
- Create: `pkg/channelv2/worker/benchmark_test.go`

- [ ] **Step 1: Add benchmark file**

Create `pkg/channelv2/worker/benchmark_test.go`:

```go
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
	ctx := context.Background()
	rejectedTask := benchmarkWorkerTask(3, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := pool.Submit(ctx, rejectedTask); !errors.Is(err, ch.ErrBackpressured) {
			b.Fatalf("Submit(full) error = %v, want ErrBackpressured", err)
		}
	}
	b.StopTimer()
	release.Do(func() { close(block) })
}

func BenchmarkWorkerPoolStoreAppendBatch(b *testing.B) {
	stores := &batchAppendStoreFactory{}
	sink := &benchmarkWorkerSink{}
	pool, err := NewPool(PoolConfig{
		Name:         "bench-store-append",
		Workers:      1,
		QueueSize:    64 * 1024,
		BatchMaxWait: time.Nanosecond,
	}, Deps{Stores: stores}, sink)
	if err != nil {
		b.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	tasks := benchmarkStoreAppendTasks(benchmarkWorkerBurst)
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkWorkerMin(benchmarkWorkerBurst, b.N-submitted)
		sink.expect(burst)
		for i := 0; i < burst; i++ {
			idx := submitted + i
			if err := pool.Submit(ctx, tasks[i]); err != nil {
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

	ctx := context.Background()
	tasks := benchmarkWorkerTasks(benchmarkWorkerBurst)
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkWorkerMin(benchmarkWorkerBurst, b.N-submitted)
		sink.expect(burst)
		for i := 0; i < burst; i++ {
			if err := pool.Submit(ctx, tasks[i]); err != nil {
				b.Fatalf("Submit(%d) error = %v", submitted+i, err)
			}
		}
		sink.wait(b)
		submitted += burst
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func benchmarkWorkerTasks(count int) []Task {
	tasks := make([]Task, count)
	for i := range tasks {
		tasks[i] = benchmarkWorkerTask(i, nil)
	}
	return tasks
}

func benchmarkWorkerTask(i int, run func(context.Context) Result) Task {
	fence := ch.Fence{ChannelKey: ch.ChannelKey("bench:" + strconv.Itoa(i+1)), OpID: ch.OpID(i + 1)}
	if run == nil {
		run = func(context.Context) Result { return Result{Kind: TaskFunc, Fence: fence} }
	}
	return Task{Kind: TaskFunc, Fence: fence, RunFunc: run}
}

func benchmarkStoreAppendTasks(count int) []Task {
	tasks := make([]Task, count)
	for i := range tasks {
		tasks[i] = benchmarkStoreAppendTask(i)
	}
	return tasks
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

func (o *benchmarkWorkerObserver) SetWorkerQueueDepth(string, int) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) SetWorkerQueueCapacity(string, int) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) SetWorkerWorkers(string, int) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) ObserveWorkerAdmission(string, string) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) ObserveWorkerWait(string, TaskKind, time.Duration) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) ObserveWorkerTask(string, TaskKind, error, time.Duration) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) ObserveWorkerBatch(string, TaskKind, int, error) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) SetWorkerInflight(string, int) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) SetWorkerInflightPeak(string, int) { o.count.Add(1) }
func (o *benchmarkWorkerObserver) SetWorkerAntsPoolUsage(string, int, int, int) { o.count.Add(1) }
```

- [ ] **Step 2: Run benchmark compile check**

Run:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=100ms -count=1 ./pkg/channelv2/worker
```

Expected: benchmark command exits 0 and reports the worker benchmarks. The StoreAppend benchmark must use a positive `BatchMaxWait` override, report `batch-calls/op` and `single-append-calls/op`, and fail when multi-item runs do not exercise the batch path.

- [ ] **Step 3: Commit benchmark-only baseline code**

Run:

```sh
git add pkg/channelv2/worker/benchmark_test.go
git commit -m "bench: add channelv2 worker baselines"
```

---

### Task 2: Capture Baseline Benchmark Results

**Files:**
- Create: `docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md`

- [ ] **Step 1: Run baseline benchmark**

Run:

```sh
mkdir -p /tmp/wukongim-bench
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker | tee /tmp/wukongim-bench/channelv2-worker-baseline.txt
```

Expected: command exits 0.

- [ ] **Step 2: Create baseline report**

Create `docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md` from the captured output:

```sh
{
cat <<'EOF'
# ChannelV2 Worker Workqueue Migration Report

## Baseline

Command:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

Raw output:

```text
EOF
cat /tmp/wukongim-bench/channelv2-worker-baseline.txt
cat <<'EOF'
```

## Post-Migration

The post-migration benchmark output will be added after implementation.

## Gate

- `ns/op` regression must be no more than 5%.
- `allocs/op` must not increase.
- Any material `B/op` increase requires explanation before proceeding.
EOF
} > docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md
```

- [ ] **Step 3: Commit baseline report**

Run:

```sh
git add docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md
git commit -m "bench: record channelv2 worker baseline"
```

---

### Task 3: Add Batched Workqueue Primitive Tests

**Files:**
- Create: `pkg/workqueue/bounded_batch_pool_test.go`

- [ ] **Step 1: Write failing tests for `BoundedBatchPool`**

Create `pkg/workqueue/bounded_batch_pool_test.go`:

```go
package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedBatchPoolBatchesAdjacentItems(t *testing.T) {
	batches := make(chan []int, 1)
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   1,
		QueueSize: 8,
		Policy: func(first int) BatchOptions {
			return BatchOptions{MaxItems: 2, MaxWait: 50 * time.Millisecond}
		},
	}, func(ctx context.Context, items []int) error {
		batches <- append([]int(nil), items...)
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	defer pool.Close(context.Background())

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
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
		_ = pool.Close(context.Background())
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	<-started
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	if err := pool.Submit(context.Background(), 3); !errors.Is(err, ErrFull) {
		t.Fatalf("Submit(third) error = %v, want ErrFull", err)
	}
}

func TestBoundedBatchPoolCancelAcceptedOnCloseCompletesQueuedItems(t *testing.T) {
	var canceled atomic.Int64
	started := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:                  "batch",
		Workers:               1,
		QueueSize:             2,
		CancelAcceptedOnClose: true,
		CancelAccepted: func(item int, err error) {
			if item == 2 && errors.Is(err, ErrClosed) {
				canceled.Add(1)
			}
		},
		Policy: func(int) BatchOptions { return BatchOptions{MaxItems: 1} },
	}, func(ctx context.Context, items []int) error {
		if items[0] == 1 {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	<-started
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := canceled.Load(); got != 1 {
		t.Fatalf("canceled queued items = %d, want 1", got)
	}
}

func TestBoundedBatchPoolSerializesOneBatchPerWorkerItem(t *testing.T) {
	var running atomic.Int64
	var peak atomic.Int64
	var processed atomic.Int64
	done := make(chan struct{})
	pool, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{
		Name:      "batch",
		Workers:   4,
		QueueSize: 32,
		Policy:    func(int) BatchOptions { return BatchOptions{MaxItems: 4} },
	}, func(ctx context.Context, items []int) error {
		current := running.Add(1)
		updateBatchPoolPeak(&peak, current)
		time.Sleep(time.Millisecond)
		running.Add(-1)
		if processed.Add(int64(len(items))) == 16 {
			close(done)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedBatchPool() error = %v", err)
	}
	defer pool.Close(context.Background())

	for i := 0; i < 16; i++ {
		if err := pool.Submit(context.Background(), i); err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for batches")
	}
	if got := peak.Load(); got > int64(pool.Workers()) {
		t.Fatalf("peak running batches = %d, workers = %d", got, pool.Workers())
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

type batchPoolObserver struct {
	mu    sync.Mutex
	kinds map[string]int
}

func (o *batchPoolObserver) ObserveBoundedPool(obs BoundedPoolObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.kinds == nil {
		o.kinds = make(map[string]int)
	}
	o.kinds[obs.Kind+":"+obs.Result]++
}
```

- [ ] **Step 2: Verify tests fail before implementation**

Run:

```sh
go test -run 'TestBoundedBatchPool' -count=1 ./pkg/workqueue
```

Expected: FAIL because `NewBoundedBatchPool`, `BoundedBatchPoolConfig`, and `BatchOptions` are undefined.

- [ ] **Step 3: Keep the failing tests uncommitted**

Do not commit the red tests yet. Leave `pkg/workqueue/bounded_batch_pool_test.go`
uncommitted until Task 4 makes the package pass.

---

### Task 4: Implement `workqueue.BoundedBatchPool`

**Files:**
- Create: `pkg/workqueue/bounded_batch_pool.go`

- [ ] **Step 1: Add public batch pool API and struct**

Create `pkg/workqueue/bounded_batch_pool.go` with these public types at the top:

```go
package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
)

// BatchOptions controls one batch collection decision for a first item.
type BatchOptions struct {
	// MaxItems is the maximum number of adjacent items in one handler call. Values <= 1 disable batching.
	MaxItems int
	// MaxWait bounds how long collection waits for one adjacent peer after ready items are drained.
	MaxWait time.Duration
}

// BoundedBatchPolicy returns batch collection options for a first item.
type BoundedBatchPolicy[T any] func(first T) BatchOptions

// BoundedBatchPoolHandler processes one collected batch admitted by a BoundedBatchPool.
type BoundedBatchPoolHandler[T any] func(context.Context, []T) error

// CancelAcceptedFunc handles accepted items that will not run because close canceled the pool.
type CancelAcceptedFunc[T any] func(T, error)

// BoundedBatchPoolConfig defines worker, admission, and batch limits.
type BoundedBatchPoolConfig[T any] struct {
	Name string
	Workers int
	QueueSize int
	ReleaseTimeout time.Duration
	Observer BoundedPoolObserver
	Policy BoundedBatchPolicy[T]
	CancelAcceptedOnClose bool
	CancelAccepted CancelAcceptedFunc[T]
}
```

- [ ] **Step 2: Add internal fields**

Use this struct shape:

```go
// BoundedBatchPool admits items into a bounded queue and executes adjacent batches on an ants pool.
type BoundedBatchPool[T any] struct {
	cfg     BoundedBatchPoolConfig[T]
	handler BoundedBatchPoolHandler[T]

	queue chan boundedBatchPoolTask[T]
	slots chan struct{}
	stop  chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	pool   *ants.PoolWithFuncGeneric[[]boundedBatchPoolTask[T]]

	closed  atomic.Bool
	running atomic.Int64

	closeOnce  sync.Once
	closeErr   error
	dispatchWG sync.WaitGroup
	taskWG     sync.WaitGroup
}

type boundedBatchPoolTask[T any] struct {
	item       T
	enqueuedAt time.Time
}
```

- [ ] **Step 3: Implement constructor, submit, close, and accessors**

Implement these methods by adapting `pkg/workqueue/bounded_pool.go` rather than importing worker code:

```go
func NewBoundedBatchPool[T any](cfg BoundedBatchPoolConfig[T], handler BoundedBatchPoolHandler[T]) (*BoundedBatchPool[T], error)
func (p *BoundedBatchPool[T]) Submit(ctx context.Context, item T) error
func (p *BoundedBatchPool[T]) Close(ctx context.Context) error
func (p *BoundedBatchPool[T]) QueueDepth() int
func (p *BoundedBatchPool[T]) Workers() int
func (p *BoundedBatchPool[T]) QueueCapacity() int
```

Constructor requirements:

```go
if cfg.Workers <= 0 || cfg.QueueSize <= 0 || handler == nil {
	return nil, ErrInvalidConfig
}
if cfg.ReleaseTimeout <= 0 {
	cfg.ReleaseTimeout = defaultReleaseTimeout
}
if cfg.Policy == nil {
	cfg.Policy = func(T) BatchOptions { return BatchOptions{MaxItems: 1} }
}
```

Close behavior:

```go
p.closed.Store(true)
close(p.stop)
if p.cfg.CancelAcceptedOnClose {
	p.cancel()
	p.drainQueueCanceled(ErrClosed)
}
wait for dispatcher and task wait groups until ctx expires
release ants pool with ReleaseTimeout
cancel runtime context after wait
```

The `drainQueueCanceled` helper must release one slot per drained item and call
`cfg.CancelAccepted(item, ErrClosed)` when the callback is non-nil.

- [ ] **Step 4: Implement batch collection and execution**

Use this flow:

```go
func (p *BoundedBatchPool[T]) dispatch() {
	defer p.dispatchWG.Done()
	for {
		select {
		case first := <-p.queue:
			p.observeDepth()
			batch := p.collectBatch(first)
			if !p.submitBatchToExecutor(batch) {
				return
			}
		case <-p.stop:
			if p.cfg.CancelAcceptedOnClose {
				p.drainQueueCanceled(ErrClosed)
				return
			}
			p.drainQueue()
			return
		}
	}
}
```

`collectBatch` rules:

- Start with the first item.
- Read `opts := cfg.Policy(first.item)`.
- If `opts.MaxItems <= 1`, return the single-item batch.
- Drain immediately ready queue items up to `MaxItems`.
- If fewer than `MaxItems` and `opts.MaxWait > 0`, wait once for a peer until `MaxWait`, `stop`, or runtime cancellation.
- Do not wait when `opts.MaxWait <= 0`.

`runBatch` must:

- increment/decrement `running`;
- call `handler(p.ctx, items)`;
- recover panics, observe `resultPanic`, then re-panic so ants handles worker recovery;
- observe generic task result and duration.

- [ ] **Step 5: Run workqueue tests**

Run:

```sh
go test ./pkg/workqueue
go test -race -count=1 ./pkg/workqueue
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit workqueue primitive**

Run:

```sh
git add pkg/workqueue/bounded_batch_pool.go pkg/workqueue/bounded_batch_pool_test.go
git commit -m "Add bounded batch workqueue primitive"
```

---

### Task 5: Refactor Worker Pool Onto Batched Workqueue

**Files:**
- Modify: `pkg/channelv2/worker/pool.go`
- Modify: `pkg/channelv2/worker/batch.go`
- Delete: `pkg/channelv2/worker/dispatcher.go`
- Delete: `pkg/channelv2/worker/executor.go`
- Modify: `pkg/channelv2/worker/pool_test.go`

- [ ] **Step 1: Replace `Pool` fields**

In `pkg/channelv2/worker/pool.go`, replace queue/executor fields with:

```go
type Pool struct {
	cfg  PoolConfig
	deps Deps
	sink CompletionSink

	runtime *workqueue.BoundedBatchPool[queuedTask]

	obsMu sync.RWMutex
	obs   QueueObserver

	inflight     atomic.Int64
	inflightPeak atomic.Int64
}
```

Add import:

```go
"github.com/WuKongIM/WuKongIM/pkg/workqueue"
```

Remove imports that only supported old mechanics: `sync/atomic` stays, `sync`
stays, but local queue/dispatcher wait group dependencies should disappear.

- [ ] **Step 2: Update `NewPool`**

Construct `Pool` first, then create the runtime:

```go
p := &Pool{
	cfg:  cfg,
	deps: deps,
	sink: sink,
	obs:  noopQueueObserver{},
}
runtime, err := workqueue.NewBoundedBatchPool[queuedTask](workqueue.BoundedBatchPoolConfig[queuedTask]{
	Name:                  cfg.Name,
	Workers:               cfg.Workers,
	QueueSize:             cfg.QueueSize,
	ReleaseTimeout:        workerExecutorStopGrace,
	Observer:              workerWorkqueueObserver{pool: p},
	Policy:                p.batchPolicy,
	CancelAcceptedOnClose: true,
	CancelAccepted:        p.completeQueuedClosed,
}, p.runQueuedBatch)
if err != nil {
	if errors.Is(err, workqueue.ErrInvalidConfig) {
		return nil, ch.ErrInvalidConfig
	}
	return nil, err
}
p.runtime = runtime
return p, nil
```

Keep `workerExecutorStopGrace` by moving the constant from deleted
`executor.go` into `pool.go`:

```go
const workerExecutorStopGrace = 100 * time.Millisecond
```

- [ ] **Step 3: Update `Submit`, `Close`, and `QueueDepth`**

Use:

```go
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if p == nil || p.runtime == nil {
		return ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	queued := queuedTask{task: task, enqueuedAt: time.Now()}
	err := p.runtime.Submit(ctx, queued)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workqueue.ErrFull):
		return ch.ErrBackpressured
	case errors.Is(err, workqueue.ErrClosed):
		return ch.ErrClosed
	default:
		return err
	}
}

func (p *Pool) Close() error {
	if p == nil || p.runtime == nil {
		return nil
	}
	return p.runtime.Close(context.Background())
}

func (p *Pool) QueueDepth() int {
	if p == nil || p.runtime == nil {
		return 0
	}
	return p.runtime.QueueDepth()
}
```

- [ ] **Step 4: Add worker batch policy and queued close completion**

In `pool.go`:

```go
func (p *Pool) batchPolicy(first queuedTask) workqueue.BatchOptions {
	switch {
	case p.canCollectRPCBatch(first.task):
		return workqueue.BatchOptions{MaxItems: rpcBatchMaxItems, MaxWait: p.batchMaxWait(rpcBatchMaxWait)}
	case p.canCollectStoreAppendBatch(first.task):
		return workqueue.BatchOptions{MaxItems: storeAppendBatchMaxItems, MaxWait: p.batchMaxWait(storeAppendBatchMaxWait)}
	case p.canCollectStoreApplyBatch(first.task):
		return workqueue.BatchOptions{MaxItems: storeApplyBatchMaxItems, MaxWait: p.batchMaxWait(storeApplyBatchMaxWait)}
	default:
		return workqueue.BatchOptions{MaxItems: 1}
	}
}

func (p *Pool) completeQueuedClosed(queued queuedTask, err error) {
	if err == nil {
		err = ch.ErrClosed
	}
	p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
	p.observeTask(queued.task.Kind, err, 0)
	p.sink.Complete(Result{Kind: queued.task.Kind, Fence: queued.task.Fence, Err: err})
}
```

- [ ] **Step 5: Replace queue-based grouping in `batch.go`**

Change:

```go
func (p *Pool) taskGroups(first queuedTask) [][]queuedTask
```

to:

```go
func (p *Pool) taskGroups(items []queuedTask) [][]queuedTask {
	if len(items) == 0 {
		return nil
	}
	first := items[0]
	switch {
	case p.canCollectRPCBatch(first.task):
		return groupRPCBatchItems(items)
	case p.canCollectStoreAppendBatch(first.task):
		return groupStoreBatchItems(items, TaskStoreAppend)
	case p.canCollectStoreApplyBatch(first.task):
		return groupStoreBatchItems(items, TaskStoreApply)
	default:
		return singleTaskGroups(items)
	}
}

func singleTaskGroups(items []queuedTask) [][]queuedTask {
	groups := make([][]queuedTask, 0, len(items))
	for _, item := range items {
		groups = append(groups, []queuedTask{item})
	}
	return groups
}
```

Delete `collectBatchItems` and `drainReadyBatchItems`; collection is now owned
by `pkg/workqueue`.

- [ ] **Step 6: Add runtime handler**

In `batch.go` or `pool.go`:

```go
func (p *Pool) runQueuedBatch(ctx context.Context, items []queuedTask) error {
	_ = ctx
	for _, group := range p.taskGroups(items) {
		p.runTaskGroup(group)
	}
	return nil
}
```

`runTaskGroup` already uses `p.ctx` today. Replace `p.ctx` usage inside
`runQueuedGroup` with a helper:

```go
func (p *Pool) runtimeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
```

Then pass the handler context through `runQueuedBatch`, `runTaskGroup`, and
`runQueuedGroup`:

```go
func (p *Pool) runTaskGroup(ctx context.Context, group []queuedTask)
func (p *Pool) runQueuedGroupSafely(ctx context.Context, group []queuedTask) ([]Result, bool)
func (p *Pool) runQueuedGroup(ctx context.Context, group []queuedTask) []Result
```

Use `queued.task.Run(ctx, p.deps)` in the single-task fallback.

- [ ] **Step 7: Add workqueue observer adapter**

In `pool.go`, add:

```go
type workerWorkqueueObserver struct {
	pool *Pool
}

func (o workerWorkqueueObserver) ObserveBoundedPool(obs workqueue.BoundedPoolObservation) {
	p := o.pool
	if p == nil {
		return
	}
	switch obs.Kind {
	case "capacity":
		p.observeQueueCapacity()
		p.observeWorkers()
	case "depth":
		p.observer().SetWorkerQueueDepth(p.cfg.Name, obs.QueueDepth)
	case "admission":
		p.observeAdmission(workerAdmissionResultFromWorkqueue(obs.Result))
	case "worker":
		if antsObs, ok := p.observer().(AntsPoolObserver); ok {
			antsObs.SetWorkerAntsPoolUsage(p.cfg.Name, obs.Running, obs.Workers, obs.Waiting)
		}
	}
}

func workerAdmissionResultFromWorkqueue(result string) string {
	switch result {
	case "ok", "full", "closed", "canceled", "timeout":
		return result
	default:
		return "other"
	}
}
```

Keep existing typed wait, task, batch, and inflight observations in worker code
because they need `TaskKind` and result details.

- [ ] **Step 8: Delete old mechanics files**

Run:

```sh
git rm pkg/channelv2/worker/dispatcher.go pkg/channelv2/worker/executor.go
```

- [ ] **Step 9: Update direct-internal tests**

In `pkg/channelv2/worker/pool_test.go`, replace tests that construct `Pool`
with internal `queue`, `slots`, `stop`, or `exec` fields.

For `TestPoolRunsCollectedRPCSubgroupsSerially`, use public `NewPool` and
submit two RPC tasks with different nodes while the first transport call is
blocked. Keep the expected result order assertion if the new design still
serializes subgroups inside one collected batch; otherwise replace it with an
assertion that both results complete and each node is called once.

For `TestPoolUsesConfiguredStoreAppendBatchMaxWait`, replace direct queue
manipulation with a policy-level test:

```go
func TestPoolUsesConfiguredStoreAppendBatchMaxWait(t *testing.T) {
	pool := &Pool{
		cfg:  PoolConfig{Name: "store-append", BatchMaxWait: time.Hour},
		deps: Deps{Stores: &batchAppendStoreFactory{}},
	}
	opts := pool.batchPolicy(queuedStoreAppendTask("a", 1))
	require.Equal(t, storeAppendBatchMaxItems, opts.MaxItems)
	require.Equal(t, time.Hour, opts.MaxWait)
}
```

- [ ] **Step 10: Run worker tests**

Run:

```sh
go test ./pkg/channelv2/worker
```

Expected: exits 0.

- [ ] **Step 11: Commit worker migration**

Run:

```sh
git add pkg/channelv2/worker
git commit -m "Refactor channelv2 worker onto workqueue"
```

---

### Task 6: Verify Behavior Broadly

**Files:**
- No file changes expected

- [ ] **Step 1: Run worker package tests**

Run:

```sh
go test ./pkg/channelv2/worker
```

Expected: exits 0.

- [ ] **Step 2: Run ChannelV2 package tests**

Run:

```sh
go test ./pkg/channelv2/...
```

Expected: exits 0.

- [ ] **Step 3: Run race test for worker**

Run:

```sh
go test -race -count=1 ./pkg/channelv2/worker
```

Expected: exits 0.

---

### Task 7: Run Post-Migration Benchmark and Gate

**Files:**
- Modify: `docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md`

- [ ] **Step 1: Run post-migration benchmark**

Run:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker | tee /tmp/wukongim-bench/channelv2-worker-workqueue.txt
```

Expected: exits 0.

- [ ] **Step 2: Compare with benchstat when available**

Run:

```sh
if command -v benchstat >/dev/null 2>&1; then
  benchstat /tmp/wukongim-bench/channelv2-worker-baseline.txt /tmp/wukongim-bench/channelv2-worker-workqueue.txt | tee /tmp/wukongim-bench/channelv2-worker-benchstat.txt
else
  printf 'benchstat not available; compare raw benchmark outputs manually.\n' | tee /tmp/wukongim-bench/channelv2-worker-benchstat.txt
fi
```

Expected: exits 0.

- [ ] **Step 3: Enforce performance gate**

Review `/tmp/wukongim-bench/channelv2-worker-benchstat.txt`.

Acceptance:

- every comparable `ns/op` regression is <= 5%;
- no comparable `allocs/op` increases;
- material `B/op` increases are explained in the report.

If the gate fails, stop implementation and investigate before changing any
other packages.

- [ ] **Step 4: Update migration report**

Append to `docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md` using the captured outputs:

```sh
{
cat <<'EOF'

## Post-Migration

Command:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

Raw output:

```text
EOF
cat /tmp/wukongim-bench/channelv2-worker-workqueue.txt
cat <<'EOF'
```

## Comparison

```text
EOF
cat /tmp/wukongim-bench/channelv2-worker-benchstat.txt
cat <<'EOF'
```

## Gate Result

The migration passes only if every comparable `ns/op` regression is no more
than 5%, `allocs/op` does not increase, and any material `B/op` increase is
explained above before proceeding.
EOF
} >> docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md
```

- [ ] **Step 5: Commit benchmark report update**

Run:

```sh
git add docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md
git commit -m "bench: compare channelv2 worker workqueue migration"
```

---

### Task 8: Update Flow Documentation

**Files:**
- Modify: `pkg/channelv2/worker/FLOW.md`

- [ ] **Step 1: Update flow document**

Edit `pkg/channelv2/worker/FLOW.md` so the Pool Flow becomes:

```text
Submit(ctx, Task)
  -> validate pool open and caller context through workqueue admission
  -> admit queuedTask into workqueue bounded batch pool
  -> workqueue collects adjacent items using worker batch policy
  -> worker handler splits collected items into compatible task groups
  -> worker handler runs grouped blocking store or transport calls serially
  -> CompletionSink receives one Result per original task
```

Also update Responsibility to say:

```markdown
The package uses `pkg/workqueue` for bounded admission, batching windows,
executor scheduling, queue depth, and shutdown mechanics. It still owns
ChannelV2-specific task grouping, fences, completion results, and typed worker
observations.
```

- [ ] **Step 2: Run doc diff check**

Run:

```sh
git diff --check -- pkg/channelv2/worker/FLOW.md
```

Expected: exits 0.

- [ ] **Step 3: Commit flow update**

Run:

```sh
git add pkg/channelv2/worker/FLOW.md
git commit -m "docs: update channelv2 worker flow"
```

---

### Task 9: Final Verification

**Files:**
- No file changes expected unless fixing verification failures

- [ ] **Step 1: Run final relevant tests**

Run:

```sh
go test ./pkg/workqueue ./pkg/channelv2/worker ./pkg/channelv2/...
go test -race -count=1 ./pkg/workqueue ./pkg/channelv2/worker
```

Expected: both commands exit 0.

- [ ] **Step 2: Confirm benchmark gate is recorded**

Run:

```sh
rg -n "Gate Result|ns/op|allocs/op|BenchmarkWorkerPool" docs/superpowers/reports/2026-06-15-channelv2-worker-workqueue-migration-report.md
```

Expected: report contains baseline, post-migration, comparison, and gate result.

- [ ] **Step 3: Check git status**

Run:

```sh
git status --short
```

Expected: no unstaged or staged changes from this migration.
