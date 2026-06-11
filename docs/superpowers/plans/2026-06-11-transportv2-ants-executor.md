# TransportV2 Ants Executor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move `pkg/transportv2` server-side service handler execution onto `github.com/panjf2000/ants/v2` while preserving existing service admission, queue, payload ownership, and connection write semantics.

**Architecture:** `Server` owns one shared `rpc.Executor` backed by `ants.PoolWithFuncGeneric[*serviceTask]`. Each `rpc.Service` keeps its own bounded mailbox, queued byte accounting, timeout handling, and per-service concurrency tokens; a single lightweight pump per service submits accepted requests into the shared executor. `conn` and `sched` are intentionally unchanged.

**Tech Stack:** Go 1.25, `github.com/panjf2000/ants/v2`, existing `pkg/transportv2/internal/rpc`, `pkg/transportv2/internal/core`, and Go unit/benchmark tests.

---

## File Structure

- Modify `pkg/transportv2/internal/rpc/service.go`
  - Keep `Request`, `Response`, service admission, queue accounting, handler timeout, and observer helpers.
  - Replace fixed worker goroutines with a service pump plus executor-submitted handler tasks.
  - Add panic recovery in handler task execution.
- Create `pkg/transportv2/internal/rpc/executor.go`
  - Own the ants pool.
  - Provide `NewExecutor`, `Tune`, `Submit`, and `Stop`.
  - Map ants errors to transport errors.
- Modify `pkg/transportv2/internal/rpc/service_test.go`
  - Keep existing tests passing.
  - Add panic recovery and concurrency-bound tests.
- Create `pkg/transportv2/internal/rpc/executor_test.go`
  - Test executor error mapping and stop behavior.
- Modify `pkg/transportv2/server.go`
  - Add server-owned executor and total registered service concurrency.
  - Create/tune executor during `Handle`.
  - Pass shared executor into each service.
  - Stop executor after services stop.
- Modify `pkg/transportv2/client_server_test.go`
  - Add a small integration test that proves multiple services can share the server executor without changing RPC behavior.

## Task 1: Characterize Service Semantics Before Refactor

**Files:**
- Modify: `pkg/transportv2/internal/rpc/service_test.go`

- [ ] **Step 1: Add a panic recovery test before changing implementation**

Append this test near the other service behavior tests in `pkg/transportv2/internal/rpc/service_test.go`:

```go
func TestServiceHandlerPanicRepliesAndReleasesPayload(t *testing.T) {
	var released atomic.Int32
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		panic("boom")
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, nil)
	defer svc.Stop()

	reply := make(chan Response, 1)
	err := svc.Enqueue(Request{
		Payload: core.NewOwnedBuffer([]byte("panic"), func([]byte) {
			released.Add(1)
		}),
		Reply: reply,
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	resp := waitResponse(t, reply)
	if resp.Err == nil {
		t.Fatal("reply err is nil, want panic error")
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}
```

- [ ] **Step 2: Run the panic test and verify it fails on current code**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run TestServiceHandlerPanicRepliesAndReleasesPayload -count=1
```

Expected: FAIL because the current service worker does not recover handler panics and the panic escapes the worker goroutine.

- [ ] **Step 3: Add a concurrency-bound regression test**

Append this test to `pkg/transportv2/internal/rpc/service_test.go`:

```go
func TestServiceConcurrencyRemainsBounded(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32

	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		call := calls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return nil, nil
		}
		close(secondStarted)
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 1024}, nil)
	defer svc.Stop()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("first"))}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	waitClosed(t, firstStarted)

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("second"))}); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}

	select {
	case <-secondStarted:
		t.Fatal("second handler started while first handler still holds concurrency slot")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseFirst)
	waitClosed(t, secondStarted)
}
```

- [ ] **Step 4: Run the bounded concurrency test**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run TestServiceConcurrencyRemainsBounded -count=1
```

Expected: PASS on current code. This test must stay green after the ants refactor.

- [ ] **Step 5: Commit the characterization tests**

```sh
git add pkg/transportv2/internal/rpc/service_test.go
git commit -m "test: characterize transportv2 service execution"
```

## Task 2: Add the Ants-Backed Service Executor

**Files:**
- Create: `pkg/transportv2/internal/rpc/executor.go`
- Create: `pkg/transportv2/internal/rpc/executor_test.go`

- [ ] **Step 1: Write executor tests**

Create `pkg/transportv2/internal/rpc/executor_test.go`:

```go
package rpc

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestExecutorSubmitAfterStopReturnsStopped(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	if err := executor.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	err = executor.Submit(&serviceTask{})
	if !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Submit(after stop) error = %v, want %v", err, core.ErrStopped)
	}
}

func TestExecutorRunsSubmittedTask(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	defer func() {
		if err := executor.Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	done := make(chan struct{})
	err = executor.Submit(&serviceTask{
		runFunc: func() {
			close(done)
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for executor task")
	}
}
```

- [ ] **Step 2: Run executor tests and verify they fail**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run 'TestExecutor' -count=1
```

Expected: FAIL because `NewExecutor`, `Executor`, and `serviceTask` do not exist yet.

- [ ] **Step 3: Implement `executor.go`**

Create `pkg/transportv2/internal/rpc/executor.go`:

```go
package rpc

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/panjf2000/ants/v2"
)

const serviceExecutorStopGrace = 100 * time.Millisecond

// Executor runs service handler tasks on a bounded ants pool.
type Executor struct {
	mu       sync.Mutex
	pool     *ants.PoolWithFuncGeneric[*serviceTask]
	capacity int
}

// NewExecutor creates a bounded service task executor.
func NewExecutor(capacity int, observer core.Observer) (*Executor, error) {
	if capacity <= 0 {
		capacity = 1
	}
	executor := &Executor{capacity: capacity}
	pool, err := ants.NewPoolWithFuncGeneric[*serviceTask](
		capacity,
		func(task *serviceTask) {
			if task == nil {
				return
			}
			task.run()
		},
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(recovered any) {
			if observer != nil {
				observer.ObserveTransport(core.Event{
					Name:   "service_executor_pool",
					Result: "panic",
				})
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: create service executor: %v", core.ErrInvalidConfig, err)
	}
	executor.pool = pool
	return executor, nil
}

// Tune raises executor capacity when more service concurrency is registered.
func (e *Executor) Tune(capacity int) {
	if e == nil || capacity <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if capacity <= e.capacity || e.pool == nil || e.pool.IsClosed() {
		return
	}
	e.pool.Tune(capacity)
	e.capacity = capacity
}

// Submit schedules task for execution without blocking on ants capacity.
func (e *Executor) Submit(task *serviceTask) error {
	if e == nil || e.pool == nil {
		return core.ErrStopped
	}
	err := e.pool.Invoke(task)
	if err == nil {
		return nil
	}
	if errors.Is(err, ants.ErrPoolOverload) {
		return core.ErrBusy
	}
	if errors.Is(err, ants.ErrPoolClosed) {
		return core.ErrStopped
	}
	return err
}

// Stop releases executor workers and waits briefly for cooperative tasks.
func (e *Executor) Stop() error {
	if e == nil || e.pool == nil {
		return nil
	}
	err := e.pool.ReleaseTimeout(serviceExecutorStopGrace)
	if errors.Is(err, ants.ErrPoolClosed) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Add a temporary `serviceTask` shim so executor tests compile**

At the bottom of `pkg/transportv2/internal/rpc/service.go`, add this temporary shape. Task 3 will replace it with the real service handler task.

```go
type serviceTask struct {
	runFunc func()
}

func (t *serviceTask) run() {
	if t.runFunc != nil {
		t.runFunc()
	}
}
```

- [ ] **Step 5: Run executor tests**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run 'TestExecutor' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the executor**

```sh
git add pkg/transportv2/internal/rpc/executor.go pkg/transportv2/internal/rpc/executor_test.go pkg/transportv2/internal/rpc/service.go
git commit -m "feat: add transportv2 service executor"
```

## Task 3: Convert `rpc.Service` to Pump into the Executor

**Files:**
- Modify: `pkg/transportv2/internal/rpc/service.go`
- Modify: `pkg/transportv2/internal/rpc/service_test.go`

- [ ] **Step 1: Update `Service` fields and constructors**

In `pkg/transportv2/internal/rpc/service.go`, update imports to include `fmt`, then replace the worker-related fields and constructor with this shape:

```go
// Service owns bounded admission and execution for a registered transport service.
type Service struct {
	// ID is the registered service identifier.
	ID uint16

	// handler processes each dequeued request payload.
	handler core.Handler
	// opts stores normalized service limits used by enqueue and workers.
	opts core.ServiceOptions
	// observer receives bounded service pressure events.
	observer core.Observer
	// executor runs admitted handler tasks on a bounded ants pool.
	executor *Executor
	// ownExecutor is true when NewService created a private executor for tests.
	ownExecutor bool

	// ctx is canceled by Stop to interrupt workers and cooperative handlers.
	ctx context.Context
	// cancel stops the service root context.
	cancel context.CancelFunc

	// mu protects stopped, queuedItems, and queuedBytes while Stop races with Enqueue and the pump.
	mu sync.Mutex
	// stopped rejects new requests and causes the pump to release late dequeues.
	stopped bool
	// queuedItems is the item count currently waiting in queue.
	queuedItems int
	// queuedBytes is the byte cost currently waiting in queue.
	queuedBytes int64
	// queue stores requests waiting for service execution capacity.
	queue chan Request
	// tokens limits handler execution concurrency for this service.
	tokens chan struct{}
	// inflight is the current number of handlers running for this service.
	inflight atomic.Int32

	// stopOnce makes Stop idempotent.
	stopOnce sync.Once
	// pumpWG waits for the service pump goroutine to exit.
	pumpWG sync.WaitGroup
	// taskWG waits for executor tasks submitted by this service to exit.
	taskWG sync.WaitGroup
	// done closes after the pump exits and submitted tasks finish.
	done chan struct{}
}

// NewService starts a bounded service using a private executor.
func NewService(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer) *Service {
	normalized := normalizeServiceOptions(opts)
	executor, err := NewExecutor(normalized.Concurrency, observer)
	if err != nil {
		panic(err)
	}
	return newService(id, handler, normalized, observer, executor, true)
}

// NewServiceWithExecutor starts a bounded service using a shared executor.
func NewServiceWithExecutor(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer, executor *Executor) *Service {
	return newService(id, handler, normalizeServiceOptions(opts), observer, executor, false)
}

func normalizeServiceOptions(opts core.ServiceOptions) core.ServiceOptions {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1
	}
	if opts.MaxQueueBytes <= 0 {
		opts.MaxQueueBytes = 1
	}
	return opts
}

func newService(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer, executor *Executor, ownExecutor bool) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		ID:          id,
		handler:     handler,
		opts:        opts,
		observer:    observer,
		executor:    executor,
		ownExecutor: ownExecutor,
		ctx:         ctx,
		cancel:      cancel,
		queue:       make(chan Request, opts.QueueSize),
		tokens:      make(chan struct{}, opts.Concurrency),
		done:        make(chan struct{}),
	}
	s.pumpWG.Add(1)
	go s.pump()
	go func() {
		s.pumpWG.Wait()
		s.taskWG.Wait()
		close(s.done)
	}()
	return s
}
```

- [ ] **Step 2: Replace `worker` with `pump`, `acquireToken`, and `releaseToken`**

Remove the old `worker` method and add:

```go
func (s *Service) pump() {
	defer s.pumpWG.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case req := <-s.queue:
			active, event := s.markDequeued(req)
			s.observe(event)
			if !active {
				trySendResponse(req.Reply, Response{Err: core.ErrStopped})
				req.Payload.Release()
				continue
			}
			if !s.acquireToken(req) {
				continue
			}

			task := &serviceTask{service: s, req: req}
			s.taskWG.Add(1)
			if err := s.executor.Submit(task); err != nil {
				s.taskWG.Done()
				s.releaseToken()
				trySendResponse(req.Reply, Response{Err: err})
				payloadLen := req.Payload.Len()
				req.Payload.Release()
				s.observeTask(taskResult(err), payloadLen, 0)
			}
		}
	}
}

func (s *Service) acquireToken(req Request) bool {
	select {
	case s.tokens <- struct{}{}:
		return true
	case <-s.ctx.Done():
		trySendResponse(req.Reply, Response{Err: core.ErrStopped})
		req.Payload.Release()
		return false
	}
}

func (s *Service) releaseToken() {
	select {
	case <-s.tokens:
	default:
	}
}
```

- [ ] **Step 3: Replace the temporary `serviceTask` shim with real task execution**

Replace the temporary `serviceTask` at the bottom of `service.go` with:

```go
type serviceTask struct {
	service *Service
	req     Request
	runFunc func()
}

func (t *serviceTask) run() {
	if t.runFunc != nil {
		t.runFunc()
		return
	}
	if t == nil || t.service == nil {
		return
	}
	s := t.service
	defer s.taskWG.Done()
	defer s.releaseToken()

	s.observeInflight(int(s.inflight.Add(1)))
	payloadLen := t.req.Payload.Len()
	started := time.Now()
	result := "ok"
	defer func() {
		if recovered := recover(); recovered != nil {
			result = "panic"
			trySendResponse(t.req.Reply, Response{
				Err: fmt.Errorf("transportv2: service handler panic: %v", recovered),
			})
		}
		s.observeTask(result, payloadLen, nonNegativeSince(started))
		s.observeInflight(int(s.inflight.Add(-1)))
	}()

	err := s.handle(t.req)
	result = taskResult(err)
}
```

- [ ] **Step 4: Update `Stop` to wait for pump/tasks and private executor**

Replace `Stop` with:

```go
// Stop requests service shutdown, drains queued payloads, and waits only briefly for cooperative handlers.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()
		for {
			select {
			case req := <-s.queue:
				if s.queuedItems > 0 {
					s.queuedItems--
				}
				s.queuedBytes -= int64(req.Payload.Len())
				if s.queuedBytes < 0 {
					s.queuedBytes = 0
				}
				trySendResponse(req.Reply, Response{Err: core.ErrStopped})
				req.Payload.Release()
			default:
				s.mu.Unlock()
				select {
				case <-s.done:
				case <-time.After(serviceStopGrace):
				}
				if s.ownExecutor {
					_ = s.executor.Stop()
				}
				return
			}
		}
	})
}
```

- [ ] **Step 5: Run service tests**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -count=1
```

Expected: PASS.

- [ ] **Step 6: Run service tests with race detector**

Run:

```sh
go test -race ./pkg/transportv2/internal/rpc -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the service refactor**

```sh
git add pkg/transportv2/internal/rpc/service.go pkg/transportv2/internal/rpc/service_test.go
git commit -m "refactor: run transportv2 services on ants executor"
```

## Task 4: Wire the Shared Executor into `Server`

**Files:**
- Modify: `pkg/transportv2/server.go`
- Modify: `pkg/transportv2/client_server_test.go`

- [ ] **Step 1: Add executor fields to `Server`**

In `pkg/transportv2/server.go`, add these fields to `Server` below `services`:

```go
	// executor runs registered service handlers on a shared bounded ants pool.
	executor *rpc.Executor
	// serviceConcurrency is the total registered service handler concurrency.
	serviceConcurrency int
```

- [ ] **Step 2: Add helper for shared executor registration**

Add this helper near `Handle`:

```go
func (s *Server) serviceExecutorLocked(concurrency int) (*rpc.Executor, error) {
	if concurrency <= 0 {
		concurrency = 1
	}
	if s.executor == nil {
		executor, err := rpc.NewExecutor(concurrency, s.cfg.Observer)
		if err != nil {
			return nil, err
		}
		s.executor = executor
		s.serviceConcurrency = concurrency
		return executor, nil
	}
	s.serviceConcurrency += concurrency
	s.executor.Tune(s.serviceConcurrency)
	return s.executor, nil
}
```

- [ ] **Step 3: Update `Handle` to use the shared executor**

Replace the service creation line in `Handle`:

```go
s.services[serviceID] = rpc.NewService(serviceID, handler, opts, s.cfg.Observer)
```

with:

```go
executor, err := s.serviceExecutorLocked(opts.Concurrency)
if err != nil {
	return err
}
s.services[serviceID] = rpc.NewServiceWithExecutor(serviceID, handler, opts, s.cfg.Observer, executor)
```

- [ ] **Step 4: Stop the executor after services stop**

In `Server.Stop`, capture `executor := s.executor` while holding `s.mu`, then stop it after all services stop:

```go
executor := s.executor
```

and after:

```go
for _, service := range services {
	service.Stop()
}
```

add:

```go
if executor != nil {
	_ = executor.Stop()
}
```

- [ ] **Step 5: Add a shared executor integration test**

Append this test to `pkg/transportv2/client_server_test.go`:

```go
func TestServerSharedExecutorHandlesMultipleServices(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2, Limits: DefaultLimits()})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()

	if err := server.Handle(7, func(context.Context, []byte) ([]byte, error) {
		return []byte("seven"), nil
	}, ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 1024}); err != nil {
		t.Fatalf("Handle(7) error = %v", err)
	}
	if err := server.Handle(8, func(context.Context, []byte) ([]byte, error) {
		return []byte("eight"), nil
	}, ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 1024}); err != nil {
		t.Fatalf("Handle(8) error = %v", err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatalf("ListenAndServe() error = %v", err)
	}

	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: server.Addr()},
		PoolSize:  1,
		Limits:    DefaultLimits(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()

	resp7, err := client.Call(context.Background(), 2, 0, PriorityRPC, 7, []byte("x"))
	if err != nil {
		t.Fatalf("Call(service 7) error = %v", err)
	}
	if string(resp7) != "seven" {
		t.Fatalf("service 7 response = %q, want seven", resp7)
	}

	resp8, err := client.Call(context.Background(), 2, 0, PriorityRPC, 8, []byte("x"))
	if err != nil {
		t.Fatalf("Call(service 8) error = %v", err)
	}
	if string(resp8) != "eight" {
		t.Fatalf("service 8 response = %q, want eight", resp8)
	}
}
```

- [ ] **Step 6: Run transportv2 tests**

Run:

```sh
go test ./pkg/transportv2/... -count=1
```

Expected: PASS.

- [ ] **Step 7: Run the broader targeted package tests**

Run:

```sh
go test ./pkg/transportv2/... ./pkg/clusterv2/... ./internalv2/... -count=1
```

Expected: PASS. If this is too slow in the local loop, run `go test ./pkg/transportv2/... -count=1` first and record that broader tests were deferred.

- [ ] **Step 8: Commit server wiring**

```sh
git add pkg/transportv2/server.go pkg/transportv2/client_server_test.go
git commit -m "feat: share ants executor across transportv2 services"
```

## Task 5: Benchmark and Final Verification

**Files:**
- No source edits expected.

- [ ] **Step 1: Run focused benchmarks**

Run:

```sh
go test -run '^$' -bench 'TransportV2|Service|Scheduler' -benchmem ./pkg/transportv2/...
```

Expected: benchmarks complete without errors. Record notable allocation, ns/op, and B/op changes in the implementation summary.

- [ ] **Step 2: Run final unit tests**

Run:

```sh
go test ./pkg/transportv2/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Check final diff**

Run:

```sh
git diff --stat HEAD
git diff -- pkg/transportv2/internal/rpc pkg/transportv2/server.go pkg/transportv2/client_server_test.go
```

Expected: diff only includes the transportv2 executor refactor and tests.

- [ ] **Step 4: Final implementation commit if benchmark notes or small fixes were added**

If Task 5 required source or test changes, commit them:

```sh
git add pkg/transportv2
git commit -m "test: verify transportv2 ants executor"
```

If Task 5 only ran verification commands and no files changed, do not create an empty commit.

## Self-Review Notes

- Spec coverage: the plan keeps `conn` and `sched` unchanged, uses ants only for service execution, keeps service admission and queue ownership in `rpc.Service`, wires executor ownership through `Server`, adds panic/stop/concurrency tests, and runs targeted benchmarks.
- Placeholder scan: no `TBD`, `TODO`, or open-ended implementation placeholders remain.
- Type consistency: the plan defines `Executor`, `NewExecutor`, `NewServiceWithExecutor`, `serviceTask`, and `serviceExecutorLocked` before later tasks use them.
