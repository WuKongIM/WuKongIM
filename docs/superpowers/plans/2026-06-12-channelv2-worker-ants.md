# ChannelV2 Worker Ants Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `pkg/channelv2/worker`'s fixed worker goroutines with an ants-backed executor while preserving explicit worker admission queues, batching, observations, and fenced completions.

**Architecture:** `worker.Pool` keeps the public API and owns the bounded admission queue. A single dispatcher forms the same task groups as today, then submits each group to a non-blocking ants executor. The worker package, not ants, remains the source of backpressure truth.

**Tech Stack:** Go, `github.com/panjf2000/ants/v2`, existing `pkg/channelv2/worker` tests, `testify/require`, `go test -race`.

---

## File Structure

- Modify `pkg/channelv2/worker/pool_test.go`: add focused tests for shutdown completion, executor saturation retry, and observer replacement race coverage.
- Create `pkg/channelv2/worker/executor.go`: ants wrapper for non-blocking group execution.
- Modify `pkg/channelv2/worker/pool.go`: keep exported API, replace fixed worker goroutines with queue, dispatcher, executor, and safe observer access.
- Create `pkg/channelv2/worker/dispatcher.go`: dispatcher loop, executor retry, closed completion helpers, and panic recovery.
- Create `pkg/channelv2/worker/batch.go`: move batch collection, grouping, and batch execution helpers out of `pool.go`.
- Create `pkg/channelv2/worker/FLOW.md`: document worker package flow.
- Modify `pkg/channelv2/FLOW.md`: update root package description to mention ants as executor only.
- Modify `pkg/channelv2/reactor/FLOW.md`: update worker batching section to mention explicit admission queue plus ants execution.

## Task 1: Add Worker Semantics Tests

**Files:**
- Modify: `pkg/channelv2/worker/pool_test.go`

- [ ] **Step 1: Add shutdown and executor saturation tests**

Append these tests after `TestPoolCloseCancelsDequeuedTaskContext` in `pkg/channelv2/worker/pool_test.go`:

```go
func TestPoolCloseCompletesQueuedTaskAsClosed(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 2}, Deps{}, sink)
	require.NoError(t, err)

	blockingStarted := make(chan struct{})
	blockingFence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	queuedFence := ch.Fence{ChannelKey: ch.ChannelKey("1:b"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 2}

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: blockingFence,
		RunFunc: func(ctx context.Context) Result {
			close(blockingStarted)
			<-ctx.Done()
			return Result{Kind: TaskFunc, Fence: blockingFence, Err: ctx.Err()}
		},
	}))
	require.Eventually(t, func() bool {
		select {
		case <-blockingStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: queuedFence,
		RunFunc: func(context.Context) Result {
			return Result{Kind: TaskFunc, Fence: queuedFence}
		},
	}))

	closed := make(chan error, 1)
	go func() { closed <- pool.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("pool Close did not return")
	}

	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	results := sink.Results()
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(results))
	require.ErrorIs(t, resultByOpID(t, results, 2).Err, ch.ErrClosed)
}

func TestPoolCompletesAcceptedTaskAfterExecutorSaturation(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 2)}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 4}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	release := sync.Once{}
	defer release.Do(func() { close(block) })

	firstFence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	secondFence := ch.Fence{ChannelKey: ch.ChannelKey("1:b"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 2}
	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: firstFence,
		RunFunc: func(context.Context) Result {
			close(started)
			<-block
			return Result{Kind: TaskFunc, Fence: firstFence}
		},
	}))
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	require.NoError(t, pool.Submit(context.Background(), Task{
		Kind:  TaskFunc,
		Fence: secondFence,
		RunFunc: func(context.Context) Result {
			return Result{Kind: TaskFunc, Fence: secondFence}
		},
	}))

	require.Never(t, func() bool {
		return sink.Len() > 0 && resultContainsOpID(sink.Results(), 2)
	}, 20*time.Millisecond, time.Millisecond)

	release.Do(func() { close(block) })
	require.Eventually(t, func() bool { return sink.Len() == 2 }, time.Second, time.Millisecond)
	require.ElementsMatch(t, []ch.OpID{1, 2}, resultOpIDs(sink.Results()))
}
```

- [ ] **Step 2: Add helper functions for result lookup**

Append these helpers after `resultOpIDs` in `pkg/channelv2/worker/pool_test.go`:

```go
func resultByOpID(t *testing.T, results []Result, opID ch.OpID) Result {
	t.Helper()
	for _, result := range results {
		if result.Fence.OpID == opID {
			return result
		}
	}
	t.Fatalf("missing result for op id %d in %#v", opID, results)
	return Result{}
}

func resultContainsOpID(results []Result, opID ch.OpID) bool {
	for _, result := range results {
		if result.Fence.OpID == opID {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Add observer replacement race test**

Append this test after `TestPoolReportsCapacityWorkersAdmissionWaitAndTaskDuration`:

```go
func TestPoolSetQueueObserverConcurrentWithExecution(t *testing.T) {
	sink := &captureSink{ch: make(chan Result, 64)}
	pool, err := NewPool(PoolConfig{Name: "race", Workers: 2, QueueSize: 64}, Deps{}, sink)
	require.NoError(t, err)
	defer pool.Close()

	stopObservers := make(chan struct{})
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		for {
			select {
			case <-stopObservers:
				return
			default:
				pool.SetQueueObserver(&recordingPoolPressureObserver{})
			}
		}
	}()

	for i := 0; i < 32; i++ {
		fence := ch.Fence{ChannelKey: ch.ChannelKey("1:race"), OpID: ch.OpID(i + 1)}
		require.NoError(t, pool.Submit(context.Background(), Task{
			Kind:  TaskFunc,
			Fence: fence,
			RunFunc: func(context.Context) Result {
				time.Sleep(100 * time.Microsecond)
				return Result{Kind: TaskFunc, Fence: fence}
			},
		}))
	}
	require.Eventually(t, func() bool { return sink.Len() == 32 }, time.Second, time.Millisecond)
	close(stopObservers)
	select {
	case <-observerDone:
	case <-time.After(time.Second):
		t.Fatal("observer setter did not stop")
	}
}
```

- [ ] **Step 4: Run the focused tests and confirm current failures**

Run:

```bash
go test ./pkg/channelv2/worker -run 'TestPoolCloseCompletesQueuedTaskAsClosed|TestPoolCompletesAcceptedTaskAfterExecutorSaturation' -count=1
```

Expected before implementation: `TestPoolCloseCompletesQueuedTaskAsClosed` fails because the fixed worker loop exits on `stop` without completing queued work. `TestPoolCompletesAcceptedTaskAfterExecutorSaturation` may pass before implementation; it is a regression guard for the ants retry path.

Run:

```bash
go test -race ./pkg/channelv2/worker -run TestPoolSetQueueObserverConcurrentWithExecution -count=1
```

Expected before implementation: FAIL with a data race between `SetQueueObserver` and worker observation paths.

- [ ] **Step 5: Commit the failing tests**

```bash
git add pkg/channelv2/worker/pool_test.go
git commit -m "test(channelv2): cover worker ants migration semantics"
```

## Task 2: Add Ants Executor Wrapper

**Files:**
- Create: `pkg/channelv2/worker/executor.go`

- [ ] **Step 1: Create executor wrapper**

Create `pkg/channelv2/worker/executor.go`:

```go
package worker

import (
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/panjf2000/ants/v2"
)

const workerExecutorStopGrace = 100 * time.Millisecond

var errExecutorOverloaded = errors.New("channelv2 worker executor overloaded")

// executor runs prepared worker task groups on an ants pool.
type executor struct {
	pool *ants.Pool
}

func newExecutor(size int) (*executor, error) {
	if size <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	pool, err := ants.NewPool(
		size,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(any) {}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: create worker executor: %v", ch.ErrInvalidConfig, err)
	}
	return &executor{pool: pool}, nil
}

func (e *executor) submit(fn func()) error {
	if e == nil || e.pool == nil {
		return ch.ErrClosed
	}
	err := e.pool.Submit(fn)
	if err == nil {
		return nil
	}
	if errors.Is(err, ants.ErrPoolOverload) {
		return errExecutorOverloaded
	}
	if errors.Is(err, ants.ErrPoolClosed) {
		return ch.ErrClosed
	}
	return err
}

func (e *executor) running() int {
	if e == nil || e.pool == nil {
		return 0
	}
	return e.pool.Running()
}

func (e *executor) capacity() int {
	if e == nil || e.pool == nil {
		return 0
	}
	return e.pool.Cap()
}

func (e *executor) waiting() int {
	if e == nil || e.pool == nil {
		return 0
	}
	return e.pool.Waiting()
}

func (e *executor) close() error {
	if e == nil || e.pool == nil {
		return nil
	}
	err := e.pool.ReleaseTimeout(workerExecutorStopGrace)
	if errors.Is(err, ants.ErrPoolClosed) {
		return nil
	}
	return err
}
```

- [ ] **Step 2: Run package tests to see compile failures from unused code state**

Run:

```bash
go test ./pkg/channelv2/worker -count=1
```

Expected: PASS or compile PASS with test failures from Task 1 still present. The new file is not wired yet.

- [ ] **Step 3: Commit executor wrapper**

```bash
git add pkg/channelv2/worker/executor.go
git commit -m "feat(channelv2): add worker ants executor"
```

## Task 3: Refactor Pool Construction, Submission, Close, And Observation

**Files:**
- Modify: `pkg/channelv2/worker/pool.go`

- [ ] **Step 1: Update imports**

In `pkg/channelv2/worker/pool.go`, remove unused imports after later steps. The final import block should be:

```go
import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)
```

- [ ] **Step 2: Replace Pool fields**

Replace the `Pool` struct in `pkg/channelv2/worker/pool.go` with:

```go
// Pool runs blocking tasks with bounded admission and ants-backed execution.
type Pool struct {
	cfg    PoolConfig
	deps   Deps
	sink   CompletionSink
	queue  chan queuedTask
	stop   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	exec   *executor

	obsMu sync.RWMutex
	obs   QueueObserver

	inflight     atomic.Int64
	inflightPeak atomic.Int64
	once         sync.Once
	closeErr     error
	dispatchWG   sync.WaitGroup
	taskWG       sync.WaitGroup
}
```

- [ ] **Step 3: Replace NewPool**

Replace `NewPool` with:

```go
// NewPool starts a bounded worker pool.
func NewPool(cfg PoolConfig, deps Deps, sink CompletionSink) (*Pool, error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	exec, err := newExecutor(cfg.Workers)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		cfg:    cfg,
		deps:   deps,
		sink:   sink,
		queue:  make(chan queuedTask, cfg.QueueSize),
		stop:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		exec:   exec,
		obs:    noopQueueObserver{},
	}
	p.dispatchWG.Add(1)
	go p.dispatch()
	return p, nil
}
```

- [ ] **Step 4: Replace SetQueueObserver and observer access**

Replace `SetQueueObserver` with:

```go
// SetQueueObserver replaces the queue pressure observer; nil restores the no-op observer.
func (p *Pool) SetQueueObserver(observer QueueObserver) {
	if p == nil {
		return
	}
	if observer == nil {
		observer = noopQueueObserver{}
	}
	p.obsMu.Lock()
	p.obs = observer
	p.obsMu.Unlock()
	p.observeQueueCapacity()
	p.observeWorkers()
	p.observeQueueDepth()
}

func (p *Pool) observer() QueueObserver {
	if p == nil {
		return noopQueueObserver{}
	}
	p.obsMu.RLock()
	observer := p.obs
	p.obsMu.RUnlock()
	if observer == nil {
		return noopQueueObserver{}
	}
	return observer
}
```

- [ ] **Step 5: Update observer methods to use a copied observer**

Replace `observeQueueDepth`, `observeQueueCapacity`, `observeWorkers`,
`observeAdmission`, `observeWait`, `observeTask`, `observeBatch`, and
`observeInflight` with this code:

```go
func (p *Pool) observeQueueDepth() {
	p.observer().SetWorkerQueueDepth(p.cfg.Name, len(p.queue))
}

func (p *Pool) observeQueueCapacity() {
	obs, ok := p.observer().(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerQueueCapacity(p.cfg.Name, p.cfg.QueueSize)
}

func (p *Pool) observeWorkers() {
	obs, ok := p.observer().(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerWorkers(p.cfg.Name, p.cfg.Workers)
}

func (p *Pool) observeAdmission(result string) {
	obs, ok := p.observer().(AdmissionObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerAdmission(p.cfg.Name, result)
}

func (p *Pool) observeWait(kind TaskKind, d time.Duration) {
	obs, ok := p.observer().(WaitObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerWait(p.cfg.Name, kind, nonNegativeDuration(d))
}

func (p *Pool) observeTask(kind TaskKind, err error, d time.Duration) {
	obs, ok := p.observer().(TaskObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerTask(p.cfg.Name, kind, err, nonNegativeDuration(d))
}

func (p *Pool) observeBatch(kind TaskKind, items int, err error) {
	obs, ok := p.observer().(BatchObserver)
	if !ok || items <= 0 {
		return
	}
	obs.ObserveWorkerBatch(p.cfg.Name, kind, items, err)
}

func (p *Pool) observeInflight(inflight int) {
	obs, ok := p.observer().(InflightObserver)
	if !ok {
		return
	}
	obs.SetWorkerInflight(p.cfg.Name, inflight)
	peak := p.updateInflightPeak(inflight)
	obs.SetWorkerInflightPeak(p.cfg.Name, peak)
}
```

- [ ] **Step 6: Replace Close**

Replace `Close` with:

```go
// Close cancels running tasks, completes queued tasks as closed, and releases the executor.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		p.cancel()
		close(p.stop)
		p.dispatchWG.Wait()
		p.completeQueuedClosed()
		p.taskWG.Wait()
		p.closeErr = p.exec.close()
	})
	return p.closeErr
}
```

- [ ] **Step 7: Remove the old run method from pool.go**

Delete `func (p *Pool) run()` from `pkg/channelv2/worker/pool.go`. The replacement dispatcher is added in Task 4.

- [ ] **Step 8: Run the package tests and capture expected compile failures**

Run:

```bash
go test ./pkg/channelv2/worker -count=1
```

Expected: compile fails because `dispatch`, `completeQueuedClosed`, and related dispatcher helpers are not defined yet. This is the checkpoint before Task 4.

## Task 4: Add Dispatcher, Retry, Closed Completion, And Panic Recovery

**Files:**
- Create: `pkg/channelv2/worker/dispatcher.go`
- Modify: `pkg/channelv2/worker/pool.go`

- [ ] **Step 1: Create dispatcher.go**

Create `pkg/channelv2/worker/dispatcher.go`:

```go
package worker

import (
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

const executorSubmitRetryDelay = 10 * time.Microsecond

func (p *Pool) dispatch() {
	defer p.dispatchWG.Done()
	for {
		select {
		case queued := <-p.queue:
			p.observeQueueDepth()
			for _, group := range p.taskGroups(queued) {
				if !p.submitGroup(group) {
					return
				}
			}
		case <-p.stop:
			return
		}
	}
}

func (p *Pool) submitGroup(group []queuedTask) bool {
	if len(group) == 0 {
		return true
	}
	for {
		select {
		case <-p.stop:
			p.completeGroupWithErr(group, ch.ErrClosed)
			return false
		default:
		}

		p.taskWG.Add(1)
		err := p.exec.submit(func() {
			defer p.taskWG.Done()
			p.runTaskGroupSafely(group)
		})
		if err == nil {
			return true
		}
		p.taskWG.Done()

		if errors.Is(err, errExecutorOverloaded) {
			if p.waitExecutorRetry() {
				continue
			}
			p.completeGroupWithErr(group, ch.ErrClosed)
			return false
		}
		if errors.Is(err, ch.ErrClosed) {
			p.completeGroupWithErr(group, ch.ErrClosed)
			return false
		}
		p.completeGroupWithErr(group, fmt.Errorf("channelv2 worker executor submit: %w", err))
		return true
	}
}

func (p *Pool) waitExecutorRetry() bool {
	timer := time.NewTimer(executorSubmitRetryDelay)
	defer timer.Stop()
	select {
	case <-p.stop:
		return false
	case <-timer.C:
		return true
	}
}

func (p *Pool) runTaskGroupSafely(group []queuedTask) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.completeGroupWithErr(group, fmt.Errorf("channelv2 worker panic: %v", recovered))
		}
	}()
	p.runTaskGroup(group)
}

func (p *Pool) completeQueuedClosed() {
	for {
		select {
		case queued := <-p.queue:
			p.completeGroupWithErr([]queuedTask{queued}, ch.ErrClosed)
		default:
			p.observeQueueDepth()
			return
		}
	}
}

func (p *Pool) completeGroupWithErr(group []queuedTask, err error) {
	for _, queued := range group {
		p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
		p.observeTask(queued.task.Kind, err, 0)
		p.sink.Complete(Result{Kind: queued.task.Kind, Fence: queued.task.Fence, Err: err})
	}
}
```

- [ ] **Step 2: Run worker tests**

Run:

```bash
go test ./pkg/channelv2/worker -count=1
```

Expected: PASS after Task 3 and Task 4 are complete, except possible failures caused by batch helpers still living in `pool.go`. If the package compiles, continue to Task 5 for file split.

- [ ] **Step 3: Run the observer race test**

Run:

```bash
go test -race ./pkg/channelv2/worker -run TestPoolSetQueueObserverConcurrentWithExecution -count=1
```

Expected: PASS with no race report because observer access is guarded by `obsMu`.

- [ ] **Step 4: Commit pool dispatcher migration**

```bash
git add pkg/channelv2/worker/pool.go pkg/channelv2/worker/dispatcher.go
git commit -m "feat(channelv2): dispatch worker groups through ants"
```

## Task 5: Move Batch Helpers Out Of pool.go

**Files:**
- Create: `pkg/channelv2/worker/batch.go`
- Modify: `pkg/channelv2/worker/pool.go`

- [ ] **Step 1: Create batch.go with existing batch helpers**

Create `pkg/channelv2/worker/batch.go` and move these functions from `pool.go` into it without changing their bodies:

```go
package worker

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)
```

The moved functions are:

```text
taskGroups
canCollectRPCBatch
canCollectStoreAppendBatch
canCollectStoreApplyBatch
collectBatchItems
drainReadyBatchItems
groupRPCBatchItems
groupStoreBatchItems
rpcBatchKeyFor
runQueuedGroup
runStoreAppendBatch
runStoreApplyBatch
runRPCPullBatch
runRPCPullHintBatch
firstStoreAppendBatchErr
firstStoreApplyBatchErr
batchTaskContext
batchContextErr
taskContextDoneErr
```

Keep the method receivers exactly as they are today. Do not change batching behavior in this task.

- [ ] **Step 2: Shrink pool.go imports**

After moving the helpers, update `pkg/channelv2/worker/pool.go` imports to remove `store` and `transport` if they are no longer used there. The final import block should be:

```go
import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)
```

- [ ] **Step 3: Run gofmt**

Run:

```bash
gofmt -w pkg/channelv2/worker/pool.go pkg/channelv2/worker/dispatcher.go pkg/channelv2/worker/executor.go pkg/channelv2/worker/batch.go pkg/channelv2/worker/pool_test.go
```

Expected: command exits with status 0.

- [ ] **Step 4: Run focused worker tests**

Run:

```bash
go test ./pkg/channelv2/worker -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit batch split**

```bash
git add pkg/channelv2/worker/pool.go pkg/channelv2/worker/batch.go
git commit -m "refactor(channelv2): split worker batch helpers"
```

## Task 6: Verify Reactor And Service Integration

**Files:**
- Test only

- [ ] **Step 1: Run worker race tests**

Run:

```bash
go test -race ./pkg/channelv2/worker -count=1
```

Expected: PASS.

- [ ] **Step 2: Run ChannelV2 integration-facing unit tests**

Run:

```bash
go test ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/channelv2 -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit if verification changes forced fixes**

If Step 1 or Step 2 required code changes, commit those focused fixes:

```bash
git add pkg/channelv2/worker pkg/channelv2/reactor pkg/channelv2/service pkg/channelv2
git commit -m "fix(channelv2): preserve worker ants integration semantics"
```

If no files changed, do not create an empty commit.

## Task 7: Document Worker Flow

**Files:**
- Create: `pkg/channelv2/worker/FLOW.md`
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`

- [ ] **Step 1: Create worker FLOW.md**

Create `pkg/channelv2/worker/FLOW.md`:

````markdown
# pkg/channelv2/worker Flow

## Responsibility

`worker` owns ChannelV2 blocking effects. Reactors submit typed store and RPC
tasks through bounded admission queues, and workers return one fenced
`Result` per accepted task.

The package uses `github.com/panjf2000/ants/v2` only as an execution primitive.
Backpressure, queue depth, batch formation, shutdown completion, and observer
events are owned by this package.

## Pool Flow

```text
Submit(ctx, Task)
  -> validate pool open and caller context
  -> enqueue queuedTask into pool-owned bounded queue
  -> dispatcher receives queuedTask
  -> dispatcher collects eligible batch peers
  -> dispatcher submits task group to ants executor
  -> executor runs blocking store or transport call
  -> CompletionSink receives one Result per original task
```

`PoolConfig.QueueSize` is the admission queue capacity. `QueueDepth` reports
only this explicit queue. `PoolConfig.Workers` is the ants executor capacity.

## Batching

RPC pull and pull-hint tasks can batch when they have the same task kind and
target node. Store append and store apply tasks can batch across different
channel keys when the store factory exposes the optional batch interface.

Batching changes only the blocking dependency call. Reactors still observe one
fenced completion per original task.

## Shutdown

`Close` cancels the pool context, stops dispatcher admission, completes queued
tasks that never reached the executor with `ErrClosed`, waits for submitted task
groups, and releases the ants pool.

Running tasks receive the canceled pool context through `Task.Run` and exit
cooperatively when their dependency honors context cancellation.

## Observability

Worker queue, capacity, admission, wait, task, batch, and inflight observers
retain their existing meanings. Inflight is the number of running task groups,
not the number of original tasks inside those groups.
````

- [ ] **Step 2: Update root ChannelV2 FLOW.md**

In `pkg/channelv2/FLOW.md`, replace the worker directory row:

```text
`-- worker/           - Typed bounded worker pools for store append/read/apply, RPC pull/ack/PullHint, checkpoint, and result delivery.
```

with:

```text
`-- worker/           - Typed bounded worker admission queues plus ants-backed execution for store append/read/apply, RPC pull/ack/PullHint, checkpoint, and result delivery.
```

In the append/replication prose, replace:

```text
The RPC worker pool may coalesce queued `TaskRPCPull` or `TaskRPCPullHint`
items that target the same remote node into one transport batch.
```

with:

```text
The RPC worker admission queue may coalesce queued `TaskRPCPull` or
`TaskRPCPullHint` items that target the same remote node into one transport
batch before executing the group on the ants-backed worker executor.
```

Replace:

```text
Store worker pools may coalesce queued `TaskStoreAppend` or `TaskStoreApply`
items when the store factory implements the optional leader-append or
follower-apply batch surfaces.
```

with:

```text
Store worker admission queues may coalesce queued `TaskStoreAppend` or
`TaskStoreApply` items when the store factory implements the optional
leader-append or follower-apply batch surfaces; ants only runs the prepared
blocking group.
```

- [ ] **Step 3: Update reactor FLOW.md**

In `pkg/channelv2/reactor/FLOW.md`, replace:

```text
RPC worker pools may batch same-target `TaskRPCPull` and `TaskRPCPullHint`
items across different channels before handing them to transport.
```

with:

```text
RPC worker admission queues may batch same-target `TaskRPCPull` and
`TaskRPCPullHint` items across different channels before an ants-backed worker
executes the transport call.
```

Replace:

```text
Store worker pools may batch queued `TaskStoreAppend` or `TaskStoreApply` items
across different channels when the store factory supports leader-append or
follower-apply batching.
```

with:

```text
Store worker admission queues may batch queued `TaskStoreAppend` or
`TaskStoreApply` items across different channels when the store factory supports
leader-append or follower-apply batching. The ants executor only runs prepared
blocking groups; it is not the source of worker backpressure.
```

- [ ] **Step 4: Run documentation grep checks**

Run:

```bash
rg -n "worker pools may batch|fixed worker|long-lived goroutine per configured worker" pkg/channelv2 pkg/channelv2/reactor pkg/channelv2/worker
```

Expected: no stale descriptions of the old fixed worker loop or old batching wording in the touched docs.

- [ ] **Step 5: Commit docs**

```bash
git add pkg/channelv2/worker/FLOW.md pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md
git commit -m "docs(channelv2): describe worker ants execution flow"
```

## Task 8: Final Verification

**Files:**
- Test only

- [ ] **Step 1: Run worker tests**

Run:

```bash
go test ./pkg/channelv2/worker -count=1
```

Expected: PASS.

- [ ] **Step 2: Run worker race tests**

Run:

```bash
go test -race ./pkg/channelv2/worker -count=1
```

Expected: PASS.

- [ ] **Step 3: Run ChannelV2 package tests**

Run:

```bash
go test ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/channelv2 -count=1
```

Expected: PASS.

- [ ] **Step 4: Run broader targeted tests**

Run:

```bash
go test ./internal/... ./internalv2/... ./pkg/... -count=1
```

Expected: PASS. If this command is too slow for the current development loop, run at least the command from Step 3 and record the broader command as not run.

- [ ] **Step 5: Inspect final diff**

Run:

```bash
git status --short
git diff --stat HEAD
```

Expected: only the intended worker implementation and documentation files are changed after the last commit.

- [ ] **Step 6: Final commit for any verification fixes**

If verification required small fixes, commit them:

```bash
git add pkg/channelv2/worker pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md
git commit -m "fix(channelv2): finalize worker ants executor"
```

If no files changed, do not create an empty commit.

## Self-Review

Spec coverage:

- Explicit admission queue and `QueueDepth` semantics are covered by Tasks 3, 4, and 8.
- Ants executor with `WithNonblocking(true)` and `WithDisablePurge(true)` is covered by Task 2.
- Batch preservation is covered by Task 5 and existing batch tests rerun in Tasks 5 and 8.
- Closed completions for queued tasks are covered by Task 1 and implemented in Task 4.
- Observer data-race cleanup is covered by Task 1 and Task 3.
- Documentation updates are covered by Task 7.

Marker scan:

- This plan contains no unresolved markers or unnamed implementation work.

Type consistency:

- `executor`, `newExecutor`, `submit`, `close`, `dispatch`, `submitGroup`, `completeQueuedClosed`, and `completeGroupWithErr` are introduced before use.
- Tests use existing `captureSink`, `Result`, `TaskFunc`, `PoolConfig`, and `ch.Fence` types.
- The new helpers `resultByOpID` and `resultContainsOpID` are defined in the same test file before any future reuse.
