# internalv2 Delivery Slice 1A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bounded asynchronous `internalv2/runtime/delivery.Manager` execution mode around the existing fanout runner, with explicit admission, lifecycle, and terminal observations.

**Architecture:** This slice does not build coordinator/fanout reactors yet. It keeps the current planner plus runner behavior, then wraps it in a runtime-owned bounded queue and worker set when configured by app wiring. Sync mode remains the default for existing unit tests and compatibility; app delivery enables async mode and starts the manager before the app async committed sink.

**Tech Stack:** Go, `context`, `sync`, `sync/atomic`, `time`, existing `internalv2/runtime/delivery` ports, existing `internalv2/app` lifecycle worker group.

---

## Scope

This plan implements only Slice 1A from:

`docs/superpowers/specs/2026-06-02-internalv2-adaptive-delivery-runtime-design.md`

It does not implement coordinator reactors, fanout reactors, owner lanes, online indexes, delivery tags, or durable fanout jobs.

## File Structure

- Create `internalv2/runtime/delivery/manager_async.go`
  - Async admission types, manager lifecycle state, queue worker loop, and terminal observation helpers.
- Create `internalv2/runtime/delivery/manager_async_test.go`
  - Runtime tests for admission, queue overflow, start/stop, terminal observation, and ordering with one worker.
- Modify `internalv2/runtime/delivery/manager.go`
  - Add async options and route `SubmitCommitted` to sync or async mode.
- Modify `internalv2/runtime/delivery/observability.go`
  - Add manager admission/terminal event types and observer interface.
- Modify `internalv2/runtime/delivery/FLOW.md`
  - Record sync compatibility mode and async Slice 1A flow.
- Modify `internalv2/app/app.go`
  - Enable async manager mode in delivery wiring and include manager in the delivery worker group.
- Modify `internalv2/app/observability.go`
  - Adapt manager admission/terminal observations into existing delivery metrics/error counters.
- Modify `internalv2/app/app_test.go`
  - Verify delivery wiring starts retry scheduler, async manager, then committed sink; verify runtime fanout errors are still observed.
- Modify `internalv2/app/FLOW.md`
  - Update delivery construction and lifecycle wording.

## Task 1: Add Manager Admission Contracts

**Files:**
- Modify: `internalv2/runtime/delivery/observability.go`
- Modify: `internalv2/runtime/delivery/manager.go`
- Test: `internalv2/runtime/delivery/manager_async_test.go`

- [ ] **Step 1: Write failing admission contract tests**

Add this test file:

```go
package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

func TestManagerAsyncSubmitRequiresStart(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 1,
		AsyncWorkers:   1,
	})

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerAsyncQueueFullReportsAdmission(t *testing.T) {
	observer := &recordingManagerObserver{}
	started := make(chan struct{}, 1)
	block := make(chan struct{})
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         blockingManagerRunner{started: started, block: block},
		AsyncQueueSize: 1,
		AsyncWorkers:   1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer manager.Stop(context.Background())
	defer close(block)

	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1}); err != nil {
		t.Fatalf("first SubmitCommitted() error = %v", err)
	}
	<-started
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 2}); err != nil {
		t.Fatalf("second SubmitCommitted() error = %v", err)
	}
	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 3})
	if !errors.Is(err, ErrManagerQueueFull) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerQueueFull", err)
	}
	if !observer.sawAdmission(DeliveryResultOverflow) {
		t.Fatalf("admissions = %#v, want overflow admission", observer.admissions)
	}
}
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsyncSubmitRequiresStart|TestManagerAsyncQueueFullReportsAdmission' -count=1
```

Expected: fail because `AsyncQueueSize`, `AsyncWorkers`, `ManagerObserver`, `ErrManagerClosed`, and `ErrManagerQueueFull` do not exist.

- [ ] **Step 3: Add admission result types, errors, and observer DTOs**

In `internalv2/runtime/delivery/observability.go`, add:

```go
const (
	// ManagerEventAdmission reports manager queue admission decisions.
	ManagerEventAdmission = "admission"
	// ManagerEventTerminal reports terminal outcomes for accepted manager work.
	ManagerEventTerminal = "terminal"
)

// ManagerObserver receives bounded manager admission and terminal observations.
type ManagerObserver interface {
	ObserveManagerAdmission(ManagerAdmissionEvent)
	ObserveManagerTerminal(ManagerTerminalEvent)
}

// ManagerAdmissionEvent describes one manager admission decision.
type ManagerAdmissionEvent struct {
	// Result is accepted, overflow, or closed.
	Result string
	// QueueDepth is the current async manager queue depth after the decision.
	QueueDepth int
}

// ManagerTerminalEvent describes the final outcome for accepted manager work.
type ManagerTerminalEvent struct {
	// Result is ok, error, cancelled, or dropped.
	Result string
	// ErrorClass is the normalized delivery error class.
	ErrorClass string
	// QueueDepth is the current async manager queue depth after the event.
	QueueDepth int
}
```

In `internalv2/runtime/delivery/manager.go`, update `ManagerOptions`:

```go
	// AsyncQueueSize enables bounded asynchronous execution when greater than zero.
	AsyncQueueSize int
	// AsyncWorkers controls asynchronous fanout worker count; values <= 0 use one worker when async is enabled.
	AsyncWorkers int
	// ManagerObserver receives async manager admission and terminal observations.
	ManagerObserver ManagerObserver
```

Create `internalv2/runtime/delivery/manager_async.go` with:

```go
package delivery

import (
	"errors"
)

const defaultManagerAsyncWorkers = 1

// ErrManagerQueueFull reports that committed delivery work could not enter the bounded manager queue.
var ErrManagerQueueFull = errors.New("internalv2/runtime/delivery: manager queue full")

// ErrManagerClosed reports that the manager is not accepting async work.
var ErrManagerClosed = errors.New("internalv2/runtime/delivery: manager closed")

type managerState uint8

const (
	managerStateClosed managerState = iota
	managerStateOpen
	managerStateClosing
)

type managerCommand struct {
	envelope Envelope
}
```

- [ ] **Step 4: Add test helper types**

Append to `internalv2/runtime/delivery/manager_async_test.go`:

```go
type recordingManagerRunner struct {
	tasks []FanoutTask
	err   error
}

func (r recordingManagerRunner) RunTask(context.Context, FanoutTask) error {
	return r.err
}

type blockingManagerRunner struct {
	started chan<- struct{}
	block   <-chan struct{}
}

func (r blockingManagerRunner) RunTask(context.Context, FanoutTask) error {
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	<-r.block
	return nil
}

type recordingManagerObserver struct {
	admissions []ManagerAdmissionEvent
	terminals  []ManagerTerminalEvent
}

func (o *recordingManagerObserver) ObserveManagerAdmission(event ManagerAdmissionEvent) {
	o.admissions = append(o.admissions, event)
}

func (o *recordingManagerObserver) ObserveManagerTerminal(event ManagerTerminalEvent) {
	o.terminals = append(o.terminals, event)
}

func (o *recordingManagerObserver) sawAdmission(result string) bool {
	for _, event := range o.admissions {
		if event.Result == result {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run the focused tests and verify they still fail on behavior**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsyncSubmitRequiresStart|TestManagerAsyncQueueFullReportsAdmission' -count=1
```

Expected: compile succeeds further than Step 2, then fails because async behavior is not implemented.

- [ ] **Step 6: Commit the failing tests and contracts**

```bash
git add internalv2/runtime/delivery/observability.go internalv2/runtime/delivery/manager.go internalv2/runtime/delivery/manager_async.go internalv2/runtime/delivery/manager_async_test.go
git commit -m "test: define delivery manager async admission contracts"
```

## Task 2: Implement Bounded Async Manager Lifecycle

**Files:**
- Modify: `internalv2/runtime/delivery/manager.go`
- Modify: `internalv2/runtime/delivery/manager_async.go`
- Test: `internalv2/runtime/delivery/manager_async_test.go`

- [ ] **Step 1: Add lifecycle tests**

Add these tests to `internalv2/runtime/delivery/manager_async_test.go`:

```go
func TestManagerAsyncStartStopIdempotent(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 2,
		AsyncWorkers:   1,
	})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestManagerAsyncStopRejectsNewSubmits(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         recordingManagerRunner{},
		AsyncQueueSize: 2,
		AsyncWorkers:   1,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerClosed", err)
	}
}
```

- [ ] **Step 2: Run lifecycle tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsyncStartStopIdempotent|TestManagerAsyncStopRejectsNewSubmits' -count=1
```

Expected: fail because `Manager.Start` and `Manager.Stop` do not exist.

- [ ] **Step 3: Add async fields and constructor wiring**

In `internalv2/runtime/delivery/manager.go`, update `Manager`:

```go
type Manager struct {
	planner *Planner
	runner  FanoutTaskRunner
	acks    *AckTracker
	async   *managerAsync
}
```

Update `NewManager`:

```go
	manager := &Manager{
		planner: opts.Planner,
		runner:  runner,
		acks:    acks,
	}
	if opts.AsyncQueueSize > 0 {
		workers := opts.AsyncWorkers
		if workers <= 0 {
			workers = defaultManagerAsyncWorkers
		}
		manager.async = newManagerAsync(manager, opts.AsyncQueueSize, workers, opts.ManagerObserver)
	}
	return manager
```

- [ ] **Step 4: Implement lifecycle in `manager_async.go`**

Replace the temporary contents of `manager_async.go` with:

```go
package delivery

import (
	"context"
	"errors"
	"sync"
)

const defaultManagerAsyncWorkers = 1

var ErrManagerQueueFull = errors.New("internalv2/runtime/delivery: manager queue full")
var ErrManagerClosed = errors.New("internalv2/runtime/delivery: manager closed")

type managerState uint8

const (
	managerStateClosed managerState = iota
	managerStateOpen
	managerStateClosing
)

type managerCommand struct {
	envelope Envelope
}

type managerAsync struct {
	manager  *Manager
	queue    chan managerCommand
	workers  int
	observer ManagerObserver

	mu     sync.Mutex
	state  managerState
	cancel context.CancelFunc
	done   chan struct{}
}

func newManagerAsync(manager *Manager, capacity int, workers int, observer ManagerObserver) *managerAsync {
	return &managerAsync{
		manager:  manager,
		queue:    make(chan managerCommand, capacity),
		workers:  workers,
		observer: observer,
		state:    managerStateClosed,
	}
}

func (m *Manager) Start(context.Context) error {
	if m == nil || m.async == nil {
		return nil
	}
	return m.async.start()
}

func (m *Manager) Stop(ctx context.Context) error {
	if m == nil || m.async == nil {
		return nil
	}
	return m.async.stop(ctx)
}

func (a *managerAsync) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == managerStateOpen {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(a.workers)
	for i := 0; i < a.workers; i++ {
		go func() {
			defer wg.Done()
			a.runWorker(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	a.cancel = cancel
	a.done = done
	a.state = managerStateOpen
	return nil
}

func (a *managerAsync) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if a.state == managerStateClosed {
		a.mu.Unlock()
		return nil
	}
	cancel := a.cancel
	done := a.done
	a.state = managerStateClosing
	a.mu.Unlock()

	cancel()
	select {
	case <-done:
		a.mu.Lock()
		a.state = managerStateClosed
		a.cancel = nil
		a.done = nil
		a.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

- [ ] **Step 5: Run lifecycle tests**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsyncStartStopIdempotent|TestManagerAsyncStopRejectsNewSubmits' -count=1
```

Expected: pass after `SubmitCommitted` is updated in the next step. If `SubmitCommitted` still always runs synchronously, proceed to Step 6 before expecting pass.

- [ ] **Step 6: Route async submissions through admission**

In `internalv2/runtime/delivery/manager.go`, change `SubmitCommitted`:

```go
func (m *Manager) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if m == nil || m.planner == nil || m.runner == nil {
		return nil
	}
	env := envelopeFromEvent(event)
	if m.async != nil {
		return m.async.submit(ctx, env)
	}
	return m.runEnvelope(ctx, env)
}

func (m *Manager) runEnvelope(ctx context.Context, env Envelope) error {
	tasks, err := m.planner.Plan(ctx, env)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := m.runner.RunTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}
```

In `manager_async.go`, add:

```go
func (a *managerAsync) submit(ctx context.Context, env Envelope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	open := a.state == managerStateOpen
	a.mu.Unlock()
	if !open {
		a.observeAdmission(DeliveryResultError)
		return ErrManagerClosed
	}
	cmd := managerCommand{envelope: cloneEnvelope(env)}
	select {
	case a.queue <- cmd:
		a.observeAdmission(DeliveryResultOK)
		return nil
	default:
		a.observeAdmission(DeliveryResultOverflow)
		return ErrManagerQueueFull
	}
}

func (a *managerAsync) observeAdmission(result string) {
	if a != nil && a.observer != nil {
		a.observer.ObserveManagerAdmission(ManagerAdmissionEvent{Result: result, QueueDepth: len(a.queue)})
	}
}
```

- [ ] **Step 7: Run admission and lifecycle tests**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsyncSubmitRequiresStart|TestManagerAsyncQueueFullReportsAdmission|TestManagerAsyncStartStopIdempotent|TestManagerAsyncStopRejectsNewSubmits' -count=1
```

Expected: pass.

- [ ] **Step 8: Commit lifecycle implementation**

```bash
git add internalv2/runtime/delivery/manager.go internalv2/runtime/delivery/manager_async.go internalv2/runtime/delivery/manager_async_test.go
git commit -m "feat: add bounded async delivery manager lifecycle"
```

## Task 3: Execute Accepted Work And Emit Terminal Observations

**Files:**
- Modify: `internalv2/runtime/delivery/manager_async.go`
- Modify: `internalv2/runtime/delivery/manager_async_test.go`
- Test: `internalv2/runtime/delivery/manager_async_test.go`

- [ ] **Step 1: Add terminal observation tests**

Add:

```go
func TestManagerAsyncRunsAcceptedWorkAndReportsTerminal(t *testing.T) {
	observer := &recordingManagerObserver{}
	runner := &countingManagerRunner{}
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          runner,
		AsyncQueueSize:  4,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if runner.count != 1 {
		t.Fatalf("runner count = %d, want 1", runner.count)
	}
	if !observer.sawTerminal(DeliveryResultOK) {
		t.Fatalf("terminals = %#v, want ok terminal", observer.terminals)
	}
}

func TestManagerAsyncReportsRunnerErrorTerminal(t *testing.T) {
	observer := &recordingManagerObserver{}
	wantErr := errors.New("runner failed")
	manager := NewManager(ManagerOptions{
		Planner:         NewPlanner(PlannerOptions{}),
		Runner:          recordingManagerRunner{err: wantErr},
		AsyncQueueSize:  4,
		AsyncWorkers:    1,
		ManagerObserver: observer,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1, MessageScopedUIDs: []string{"u1"}}); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !observer.sawTerminal(DeliveryResultError) {
		t.Fatalf("terminals = %#v, want error terminal", observer.terminals)
	}
}
```

Add helper:

```go
type countingManagerRunner struct {
	count int
}

func (r *countingManagerRunner) RunTask(context.Context, FanoutTask) error {
	r.count++
	return nil
}

func (o *recordingManagerObserver) sawTerminal(result string) bool {
	for _, event := range o.terminals {
		if event.Result == result {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run terminal tests and verify they fail**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsyncRunsAcceptedWorkAndReportsTerminal|TestManagerAsyncReportsRunnerErrorTerminal' -count=1
```

Expected: fail because the worker loop does not execute queued commands yet.

- [ ] **Step 3: Implement worker execution and terminal observation**

In `manager_async.go`, add:

```go
func (a *managerAsync) runWorker(ctx context.Context) {
	for {
		select {
		case cmd := <-a.queue:
			a.runCommand(cmd)
		case <-ctx.Done():
			a.drain()
			return
		}
	}
}

func (a *managerAsync) drain() {
	for {
		select {
		case cmd := <-a.queue:
			a.runCommand(cmd)
		default:
			return
		}
	}
}

func (a *managerAsync) runCommand(cmd managerCommand) {
	err := a.manager.runEnvelope(context.Background(), cmd.envelope)
	result := deliveryResultForError(err)
	class := DeliveryErrorClass(err)
	a.observeTerminal(result, class)
}

func (a *managerAsync) observeTerminal(result, class string) {
	if a != nil && a.observer != nil {
		a.observer.ObserveManagerTerminal(ManagerTerminalEvent{Result: result, ErrorClass: class, QueueDepth: len(a.queue)})
	}
}
```

- [ ] **Step 4: Run all manager async tests**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManagerAsync' -count=1
```

Expected: pass.

- [ ] **Step 5: Run existing manager tests**

Run:

```bash
go test ./internalv2/runtime/delivery -run 'TestManager' -count=1
```

Expected: pass, proving sync compatibility is preserved when async options are not set.

- [ ] **Step 6: Commit worker execution**

```bash
git add internalv2/runtime/delivery/manager_async.go internalv2/runtime/delivery/manager_async_test.go
git commit -m "feat: execute async delivery manager work"
```

## Task 4: Wire Async Manager Into internalv2 App

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/observability.go`

- [ ] **Step 1: Add app wiring test expectations**

In `internalv2/app/app_test.go`, update `TestNewWiresDeliveryWhenEnabled` by adding:

```go
	if app.deliveryManager == nil || app.deliveryManager.PendingAckCount() != 0 {
		t.Fatal("delivery manager was not initialized for async runtime")
	}
	group, ok := app.deliveryWorker.(deliveryWorkerGroup)
	if !ok {
		t.Fatalf("delivery worker = %T, want deliveryWorkerGroup", app.deliveryWorker)
	}
	if len(group) != 3 {
		t.Fatalf("delivery worker count = %d, want retry scheduler, manager, async sink", len(group))
	}
```

- [ ] **Step 2: Run app wiring test and verify it fails**

Run:

```bash
go test ./internalv2/app -run TestNewWiresDeliveryWhenEnabled -count=1
```

Expected: fail because the delivery worker group currently contains only retry scheduler and async sink.

- [ ] **Step 3: Pass async options to Manager**

In `internalv2/app/app.go`, update manager construction:

```go
		var managerObserver runtimedelivery.ManagerObserver
		if observer, ok := deliveryObserver.(runtimedelivery.ManagerObserver); ok {
			managerObserver = observer
		}
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Planner:        runtimedelivery.NewPlanner(runtimedelivery.PlannerOptions{Partitioner: partitioner}),
			Runner:         retryScheduler,
			AsyncQueueSize: app.cfg.Delivery.EventQueueSize,
			AsyncWorkers:   1,
			ManagerObserver: managerObserver,
			Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
				MaxPendingPerSession: app.cfg.Delivery.PendingAckMaxPerSession,
			}),
		})
```

Update delivery worker group:

```go
		if app.deliveryWorker == nil {
			app.deliveryWorker = deliveryWorkerGroup{retryScheduler, manager, app.deliverySink}
		}
```

- [ ] **Step 4: Add app metrics adapter methods**

In `internalv2/app/observability.go`, add:

```go
func (o deliveryMetricsObserver) ObserveManagerAdmission(event runtimedelivery.ManagerAdmissionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Delivery.ObserveEventQueue(event.Result)
}

func (o deliveryMetricsObserver) ObserveManagerTerminal(event runtimedelivery.ManagerTerminalEvent) {
	if o.metrics == nil {
		return
	}
	o.observeError(event.ErrorClass)
}
```

- [ ] **Step 5: Run focused app wiring test**

Run:

```bash
go test ./internalv2/app -run TestNewWiresDeliveryWhenEnabled -count=1
```

Expected: pass.

- [ ] **Step 6: Commit app wiring**

```bash
git add internalv2/app/app.go internalv2/app/app_test.go internalv2/app/observability.go
git commit -m "feat: wire async delivery manager lifecycle"
```

## Task 5: Preserve Delivery Behavior In App Integration Tests

**Files:**
- Modify: `internalv2/app/app_test.go`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Add deterministic pending-ack wait helper**

Add this helper near the app delivery tests:

```go
func waitAppDeliveryPendingAckCount(t *testing.T, app *App, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		got = app.deliveryManager.PendingAckCount()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending ack count = %d, want %d", got, want)
}
```

Replace direct pending ack count checks after delivery with:

```go
	waitAppDeliveryPendingAckCount(t, app, 1, time.Second)
```

Replace direct zero checks immediately after `Recvack` with:

```go
	waitAppDeliveryPendingAckCount(t, app, 0, time.Second)
```

- [ ] **Step 2: Run delivery integration tests**

Run:

```bash
go test ./internalv2/app -run 'TestDeliveryEnabledPersonSendWritesRecvAndRecvackClearsPending|TestDeliveryEnabledGroupSendUsesSubscriberSource' -count=1
```

Expected: pass.

- [ ] **Step 3: Commit test compatibility updates**

```bash
git add internalv2/app/app_test.go
git commit -m "test: stabilize internalv2 async delivery assertions"
```

## Task 6: Update FLOW Documentation

**Files:**
- Modify: `internalv2/runtime/delivery/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Update runtime delivery FLOW**

In `internalv2/runtime/delivery/FLOW.md`, replace the manager sentence with:

```markdown
`AckTracker` keeps owner-local recvack state and can enforce a per UID/session
pending limit. `Manager`, `Planner`, and `FanoutWorker` form the runtime facade
used by app adapters. `Manager` supports a sync compatibility mode for focused
unit tests and a bounded async mode for app wiring. Runtime `Observer` and
`ManagerObserver` events describe fanout routing, UID route resolution, owner
push attempts, manager admission, and terminal async outcomes with bounded
result and error-class labels; concrete metrics remain an app concern.
```

Add this flow after committed-message fanout flow:

```markdown
Async manager flow:

1. `Manager.Start` opens a bounded queue and launches a small worker set.
2. `Manager.SubmitCommitted` clones the committed event and tries bounded
   admission.
3. Accepted work is later planned and run through the existing runner.
4. Queue overflow and closed admission return typed errors to the caller.
5. Every accepted command emits exactly one terminal observation.
6. `Manager.Stop` rejects new admission and drains accepted work until the
   caller context expires.
```

- [ ] **Step 2: Update app FLOW**

In `internalv2/app/FLOW.md`, update the delivery construction bullet:

```markdown
       wrap the fanout runner with a bounded in-memory retry scheduler
       create runtime/delivery Manager in bounded async mode around the runner
```

Update lifecycle wording:

```markdown
  -> delivery worker group Start(ctx): retry scheduler starts before async
     manager, and async manager starts before committed-event sink
```

And:

```markdown
  -> delivery worker group Stop(ctx): committed-event sink drains before async
     manager, and async manager drains before retry scheduler
```

- [ ] **Step 3: Run doc consistency scan**

Run:

```bash
rg -n "synchronous delivery runtime facade|delivery worker group Start|delivery worker group Stop|ManagerObserver" internalv2/runtime/delivery/FLOW.md internalv2/app/FLOW.md
```

Expected: no stale “synchronous delivery runtime facade” statement; updated lifecycle lines mention async manager.

- [ ] **Step 4: Commit FLOW updates**

```bash
git add internalv2/runtime/delivery/FLOW.md internalv2/app/FLOW.md
git commit -m "docs: describe async internalv2 delivery manager flow"
```

## Task 7: Final Verification

**Files:**
- Verify only.

- [ ] **Step 1: Run runtime delivery tests**

Run:

```bash
go test ./internalv2/runtime/delivery -count=1
```

Expected: pass.

- [ ] **Step 2: Run app tests touched by delivery**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresDeliveryWhenEnabled|TestDeliveryEnabledPersonSendWritesRecvAndRecvackClearsPending|TestDeliveryEnabledGroupSendUsesSubscriberSource|TestDeliveryAsyncSinkReportsQueueFull|TestDeliveryAsyncSinkRecordsRuntimeError' -count=1
```

Expected: pass.

- [ ] **Step 3: Run race test for runtime delivery**

Run:

```bash
go test -race ./internalv2/runtime/delivery -count=1
```

Expected: pass.

- [ ] **Step 4: Run targeted internalv2 packages**

Run:

```bash
go test ./internalv2/runtime/delivery ./internalv2/usecase/delivery ./internalv2/app -count=1
```

Expected: pass.

- [ ] **Step 5: Check git status**

Run:

```bash
git status --short
```

Expected: only intentional committed changes, or a clean tree if no unrelated user changes are present. Existing unrelated user changes must not be reverted.
