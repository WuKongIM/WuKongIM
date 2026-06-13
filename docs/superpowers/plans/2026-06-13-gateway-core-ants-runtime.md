# Gateway Core Ants Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `pkg/gateway/core` hand-written async auth and SEND worker pools with an ants-backed async runtime while preserving gateway ordering, batching, backpressure, close reasons, and observations.

**Architecture:** Add a package-local `asyncRuntime` owned by `Server`. `authExecutor` keeps a bounded CONNECT admission queue and schedules auth tasks on `ants.PoolWithFuncGeneric[asyncAuthTask]`. `sendExecutor` keeps bounded session-sharded SEND mailboxes and schedules active shard drain tasks on `ants.PoolWithFuncGeneric[*asyncSendShard]`.

**Tech Stack:** Go, `github.com/panjf2000/ants/v2`, `pkg/gateway/core`, `pkg/gateway/types`, existing fake transport/protocol tests, `go test`.

---

## File Structure

- Modify `pkg/gateway/types/options.go`: add `RuntimeOptions`, default/normalize helpers, and move worker tuning out of `SessionOptions`.
- Modify `pkg/gateway/options.go`: re-export `RuntimeOptions`, `DefaultRuntimeOptions`, and `NormalizeRuntimeOptions`.
- Modify `pkg/gateway/options_test.go`: cover runtime defaults and normalization.
- Modify `pkg/gateway/core/server.go`: replace `asyncAuth`/`asyncDispatch` fields and lifecycle calls with `asyncRuntime`; keep protocol/session orchestration.
- Create `pkg/gateway/core/async_runtime.go`: shared runtime lifecycle, ants construction helpers, release timeout, and common queue stats.
- Create `pkg/gateway/core/async_auth.go`: auth task, auth executor, bounded admission queue, drain scheduler, ants execution, and auth queue stats.
- Create `pkg/gateway/core/async_send.go`: SEND task, sharded mailbox executor, shard drain scheduling, batch collection helpers, and SEND queue stats.
- Modify `pkg/gateway/core/async_auth_test.go`: update queue/executor tests for the new auth executor.
- Modify `pkg/gateway/core/async_dispatch_test.go`: update queue/executor tests for the new SEND executor and keep batch helper tests.
- Modify `pkg/gateway/core/server_test.go`: move worker-count tests to `RuntimeOptions` and keep black-box auth/SEND behavior.
- Modify `pkg/gateway/core/server_benchmark_test.go`: update goroutine-count guard for ants worker behavior.

The implementation deliberately avoids `LegacyQueue`, `AntsQueueAdapter`, or any wrapper that keeps the old fixed worker loops alive.

---

### Task 1: Runtime Options API

**Files:**
- Modify: `pkg/gateway/types/options.go`
- Modify: `pkg/gateway/options.go`
- Modify: `pkg/gateway/options_test.go`

- [ ] **Step 1: Write failing runtime option tests**

Add these tests to `pkg/gateway/options_test.go` after `TestDefaultSessionOptions`:

```go
func TestDefaultRuntimeOptions(t *testing.T) {
	opts := gateway.DefaultRuntimeOptions()
	if opts.AsyncSendWorkers <= 0 {
		t.Fatalf("AsyncSendWorkers = %d, want > 0", opts.AsyncSendWorkers)
	}
	if opts.AsyncSendQueueCapacity <= 0 {
		t.Fatalf("AsyncSendQueueCapacity = %d, want > 0", opts.AsyncSendQueueCapacity)
	}
	if opts.AsyncAuthWorkers <= 0 {
		t.Fatalf("AsyncAuthWorkers = %d, want > 0", opts.AsyncAuthWorkers)
	}
	if opts.AsyncAuthQueueCapacity <= 0 {
		t.Fatalf("AsyncAuthQueueCapacity = %d, want > 0", opts.AsyncAuthQueueCapacity)
	}
	if opts.AsyncPoolReleaseTimeout != 100*time.Millisecond {
		t.Fatalf("AsyncPoolReleaseTimeout = %s, want 100ms", opts.AsyncPoolReleaseTimeout)
	}
}

func TestOptionsValidateNormalizesRuntimeOptions(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Runtime: gateway.RuntimeOptions{
			AsyncSendWorkers:        7,
			AsyncSendQueueCapacity:  17,
			AsyncAuthWorkers:        3,
			AsyncAuthQueueCapacity:  11,
			AsyncPoolReleaseTimeout: 250 * time.Millisecond,
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if opts.Runtime.AsyncSendWorkers != 7 {
		t.Fatalf("AsyncSendWorkers = %d, want 7", opts.Runtime.AsyncSendWorkers)
	}
	if opts.Runtime.AsyncSendQueueCapacity != 17 {
		t.Fatalf("AsyncSendQueueCapacity = %d, want 17", opts.Runtime.AsyncSendQueueCapacity)
	}
	if opts.Runtime.AsyncAuthWorkers != 3 {
		t.Fatalf("AsyncAuthWorkers = %d, want 3", opts.Runtime.AsyncAuthWorkers)
	}
	if opts.Runtime.AsyncAuthQueueCapacity != 11 {
		t.Fatalf("AsyncAuthQueueCapacity = %d, want 11", opts.Runtime.AsyncAuthQueueCapacity)
	}
	if opts.Runtime.AsyncPoolReleaseTimeout != 250*time.Millisecond {
		t.Fatalf("AsyncPoolReleaseTimeout = %s, want 250ms", opts.Runtime.AsyncPoolReleaseTimeout)
	}
}

func TestNormalizeRuntimeOptionsUsesDefaultsForInvalidValues(t *testing.T) {
	opts := gateway.NormalizeRuntimeOptions(gateway.RuntimeOptions{
		AsyncSendWorkers:        -1,
		AsyncSendQueueCapacity:  -1,
		AsyncAuthWorkers:        -1,
		AsyncAuthQueueCapacity:  -1,
		AsyncPoolReleaseTimeout: -1,
	})
	def := gateway.DefaultRuntimeOptions()
	if opts != def {
		t.Fatalf("normalized runtime options = %+v, want %+v", opts, def)
	}
}
```

- [ ] **Step 2: Run runtime option tests and verify they fail**

Run:

```bash
go test ./pkg/gateway -run 'Test(DefaultRuntimeOptions|OptionsValidateNormalizesRuntimeOptions|NormalizeRuntimeOptionsUsesDefaultsForInvalidValues)' -count=1
```

Expected: FAIL because `gateway.RuntimeOptions`, `gateway.DefaultRuntimeOptions`, and `gateway.NormalizeRuntimeOptions` are not defined.

- [ ] **Step 3: Add runtime options in gateway types**

In `pkg/gateway/types/options.go`, update `Options`, add `RuntimeOptions`, add defaults, and update normalization:

```go
type Options struct {
	Handler        Handler
	Authenticator  Authenticator
	Observer       Observer
	DefaultSession SessionOptions
	Runtime        RuntimeOptions
	Transport      TransportOptions
	Listeners      []ListenerOptions
	Logger         wklog.Logger
}
```

```go
type RuntimeOptions struct {
	// AsyncSendWorkers sets the ants-backed SEND executor capacity.
	AsyncSendWorkers int
	// AsyncSendQueueCapacity caps queued SEND tasks across all shards.
	AsyncSendQueueCapacity int
	// AsyncAuthWorkers sets the ants-backed CONNECT auth executor capacity.
	AsyncAuthWorkers int
	// AsyncAuthQueueCapacity caps queued CONNECT auth tasks.
	AsyncAuthQueueCapacity int
	// AsyncPoolReleaseTimeout bounds graceful ants pool shutdown.
	AsyncPoolReleaseTimeout time.Duration
}
```

Remove this field from `SessionOptions`:

```go
AsyncSendDispatchWorkers int
```

Add constants beside the existing async batch defaults:

```go
const (
	defaultAsyncSendBatchMaxWait    = time.Millisecond
	defaultAsyncSendBatchMaxRecords = 512
	defaultAsyncSendBatchMaxBytes   = 512 * 1024

	defaultAsyncSendWorkers        = 128
	defaultAsyncSendQueueCapacity  = 128 * 1024
	defaultAsyncAuthWorkers        = 16
	defaultAsyncAuthQueueCapacity  = 8 * 1024
	defaultAsyncPoolReleaseTimeout = 100 * time.Millisecond
)
```

Add helpers below `DefaultSessionOptions`:

```go
func DefaultRuntimeOptions() RuntimeOptions {
	return RuntimeOptions{
		AsyncSendWorkers:        defaultAsyncSendWorkers,
		AsyncSendQueueCapacity:  defaultAsyncSendQueueCapacity,
		AsyncAuthWorkers:        defaultAsyncAuthWorkers,
		AsyncAuthQueueCapacity:  defaultAsyncAuthQueueCapacity,
		AsyncPoolReleaseTimeout: defaultAsyncPoolReleaseTimeout,
	}
}
```

In `Validate`, normalize runtime options after session options:

```go
o.DefaultSession = NormalizeSessionOptions(o.DefaultSession)
o.Runtime = NormalizeRuntimeOptions(o.Runtime)
```

Add:

```go
func NormalizeRuntimeOptions(opt RuntimeOptions) RuntimeOptions {
	def := DefaultRuntimeOptions()
	if opt == (RuntimeOptions{}) {
		return def
	}
	if opt.AsyncSendWorkers <= 0 {
		opt.AsyncSendWorkers = def.AsyncSendWorkers
	}
	if opt.AsyncSendQueueCapacity <= 0 {
		opt.AsyncSendQueueCapacity = def.AsyncSendQueueCapacity
	}
	if opt.AsyncAuthWorkers <= 0 {
		opt.AsyncAuthWorkers = def.AsyncAuthWorkers
	}
	if opt.AsyncAuthQueueCapacity <= 0 {
		opt.AsyncAuthQueueCapacity = def.AsyncAuthQueueCapacity
	}
	if opt.AsyncPoolReleaseTimeout <= 0 {
		opt.AsyncPoolReleaseTimeout = def.AsyncPoolReleaseTimeout
	}
	return opt
}
```

- [ ] **Step 4: Re-export runtime options from public gateway package**

In `pkg/gateway/options.go`, add:

```go
type RuntimeOptions = gatewaytypes.RuntimeOptions
```

Add:

```go
func DefaultRuntimeOptions() RuntimeOptions {
	return gatewaytypes.DefaultRuntimeOptions()
}

func NormalizeRuntimeOptions(opt RuntimeOptions) RuntimeOptions {
	return gatewaytypes.NormalizeRuntimeOptions(opt)
}
```

- [ ] **Step 5: Run runtime option tests and verify they pass**

Run:

```bash
go test ./pkg/gateway -run 'Test(DefaultRuntimeOptions|OptionsValidateNormalizesRuntimeOptions|NormalizeRuntimeOptionsUsesDefaultsForInvalidValues)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit runtime options**

```bash
git add pkg/gateway/types/options.go pkg/gateway/options.go pkg/gateway/options_test.go
git commit -m "feat: add gateway runtime options"
```

---

### Task 2: Async Runtime Skeleton

**Files:**
- Create: `pkg/gateway/core/async_runtime.go`
- Modify: `pkg/gateway/core/server.go`
- Test: `pkg/gateway/core/async_runtime_test.go`

- [ ] **Step 1: Write failing async runtime lifecycle tests**

Create `pkg/gateway/core/async_runtime_test.go`:

```go
package core

import (
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

func TestNewAsyncRuntimeUsesNormalizedOptions(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        2,
				AsyncSendQueueCapacity:  8,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  4,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
		},
	}
	runtime, err := newAsyncRuntime(srv)
	if err != nil {
		t.Fatalf("newAsyncRuntime failed: %v", err)
	}
	defer runtime.stop()

	if runtime.auth == nil {
		t.Fatal("auth executor is nil")
	}
	if runtime.send == nil {
		t.Fatal("send executor is nil")
	}
	if got := runtime.auth.workers; got != 1 {
		t.Fatalf("auth workers = %d, want 1", got)
	}
	if got := runtime.send.workers; got != 2 {
		t.Fatalf("send workers = %d, want 2", got)
	}
}

func TestAsyncRuntimeStopIsIdempotent(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  1,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
		},
	}
	runtime, err := newAsyncRuntime(srv)
	if err != nil {
		t.Fatalf("newAsyncRuntime failed: %v", err)
	}
	runtime.stop()
	runtime.stop()
}
```

- [ ] **Step 2: Run the skeleton tests and verify they fail**

Run:

```bash
go test ./pkg/gateway/core -run 'TestNewAsyncRuntimeUsesNormalizedOptions|TestAsyncRuntimeStopIsIdempotent' -count=1
```

Expected: FAIL because `newAsyncRuntime` and executor fields do not exist.

- [ ] **Step 3: Add async runtime skeleton**

Create `pkg/gateway/core/async_runtime.go`:

```go
package core

import (
	"errors"
	"fmt"
	"sync"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/panjf2000/ants/v2"
)

type asyncRuntime struct {
	server         *Server
	auth           *authExecutor
	send           *sendExecutor
	releaseTimeout time.Duration
	stopOnce       sync.Once
}

type asyncQueueStats interface {
	depth() int
	totalCapacity() int
}

func newAsyncRuntime(s *Server) (*asyncRuntime, error) {
	if s == nil {
		return nil, fmt.Errorf("gateway/core: nil server for async runtime")
	}
	runtimeOpts := gatewaytypes.NormalizeRuntimeOptions(s.options.Runtime)
	auth, err := newAuthExecutor(s, runtimeOpts)
	if err != nil {
		return nil, err
	}
	send, err := newSendExecutor(s, runtimeOpts)
	if err != nil {
		auth.stop()
		return nil, err
	}
	return &asyncRuntime{
		server:         s,
		auth:           auth,
		send:           send,
		releaseTimeout: runtimeOpts.AsyncPoolReleaseTimeout,
	}, nil
}

func (r *asyncRuntime) stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		if r.auth != nil {
			r.auth.stop()
		}
		if r.send != nil {
			r.send.stop()
		}
	})
}

func (r *asyncRuntime) submitAuth(task asyncAuthTask) bool {
	return r != nil && r.auth != nil && r.auth.submit(task)
}

func (r *asyncRuntime) submitSend(state *sessionState, replyToken string, sendFrame *frame.SendPacket) bool {
	return r != nil && r.send != nil && r.send.submit(state, replyToken, sendFrame)
}

func isAntsStopped(err error) bool {
	return errors.Is(err, ants.ErrPoolClosed)
}

func isAntsBusy(err error) bool {
	return errors.Is(err, ants.ErrPoolOverload)
}
```

Add `frame` to the imports:

```go
"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
```

- [ ] **Step 4: Add temporary executor constructors**

Create `pkg/gateway/core/async_auth.go` with a minimal auth executor:

```go
package core

import (
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type asyncAuthTask struct {
	state      *sessionState
	replyToken string
	connect    *frame.ConnectPacket
	enqueuedAt time.Time
	queue      *authExecutor
}

type authExecutor struct {
	server   *Server
	workers  int
	capacity int
	queued   atomic.Int64
	closed   atomic.Bool
}

func newAuthExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*authExecutor, error) {
	return &authExecutor{
		server:   s,
		workers:  opts.AsyncAuthWorkers,
		capacity: opts.AsyncAuthQueueCapacity,
	}, nil
}

func (e *authExecutor) submit(asyncAuthTask) bool { return false }
func (e *authExecutor) stop()                     { e.closed.Store(true) }
func (e *authExecutor) depth() int                { return int(e.queued.Load()) }
func (e *authExecutor) totalCapacity() int        { return e.capacity }
func cloneAuthConnectPacket(connect *frame.ConnectPacket) *frame.ConnectPacket {
	if connect == nil {
		return nil
	}
	cloned := *connect
	return &cloned
}
```

Create `pkg/gateway/core/async_send.go` with a minimal SEND executor:

```go
package core

import (
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type asyncDispatchTask struct {
	state      *sessionState
	replyToken string
	frame      frame.Frame
	enqueuedAt time.Time
	queue      *sendExecutor
}

type sendExecutor struct {
	server   *Server
	workers  int
	capacity int
	queued   atomic.Int64
	closed   atomic.Bool
}

func newSendExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*sendExecutor, error) {
	return &sendExecutor{
		server:   s,
		workers:  opts.AsyncSendWorkers,
		capacity: opts.AsyncSendQueueCapacity,
	}, nil
}

func (e *sendExecutor) submit(*sessionState, string, *frame.SendPacket) bool { return false }
func (e *sendExecutor) stop()                                               { e.closed.Store(true) }
func (e *sendExecutor) depth() int                                          { return int(e.queued.Load()) }
func (e *sendExecutor) totalCapacity() int                                  { return e.capacity }
```

- [ ] **Step 5: Move duplicate task/clone definitions out of server.go**

Remove `asyncAuthTask`, `asyncDispatchTask`, and `cloneAuthConnectPacket` from `pkg/gateway/core/server.go`. Keep `runAuthTask`, `dispatchSendBatch`, and helper methods in `server.go` for now.

- [ ] **Step 6: Run skeleton tests and verify they pass**

Run:

```bash
go test ./pkg/gateway/core -run 'TestNewAsyncRuntimeUsesNormalizedOptions|TestAsyncRuntimeStopIsIdempotent' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit runtime skeleton**

```bash
git add pkg/gateway/core/async_runtime.go pkg/gateway/core/async_runtime_test.go pkg/gateway/core/async_auth.go pkg/gateway/core/async_send.go pkg/gateway/core/server.go
git commit -m "feat: add gateway async runtime skeleton"
```

---

### Task 3: Auth Executor On Ants

**Files:**
- Modify: `pkg/gateway/core/async_auth.go`
- Modify: `pkg/gateway/core/async_auth_test.go`
- Modify: `pkg/gateway/core/server.go`

- [ ] **Step 1: Replace old auth queue tests with executor tests**

In `pkg/gateway/core/async_auth_test.go`, replace `TestAsyncAuthQueueSubmitRejectsWhenFull`, `TestAsyncAuthQueueConsumeBalancesQueuedDepth`, and `TestAsyncAuthQueueCloseStopsWorker` with:

```go
func TestAuthExecutorSubmitRejectsWhenFull(t *testing.T) {
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
			Handler: handler,
		},
		dispatcher: newDispatcher(handler),
	}
	exec, err := newAuthExecutor(srv, gatewaytypes.NormalizeRuntimeOptions(srv.options.Runtime))
	if err != nil {
		t.Fatalf("newAuthExecutor failed: %v", err)
	}
	defer exec.stop()

	state := &sessionState{}
	if !exec.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1"}}) {
		t.Fatal("first auth submit rejected")
	}
	if exec.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u2"}}) {
		t.Fatal("second auth submit accepted when queue is full")
	}
}

func TestAuthExecutorRunsQueuedTaskOnAnts(t *testing.T) {
	done := make(chan struct{})
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Second,
			},
			Handler: handler,
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				close(done)
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
		},
		dispatcher: newDispatcher(handler),
	}
	exec, err := newAuthExecutor(srv, gatewaytypes.NormalizeRuntimeOptions(srv.options.Runtime))
	if err != nil {
		t.Fatalf("newAuthExecutor failed: %v", err)
	}
	defer exec.stop()

	state := &sessionState{
		server:   srv,
		closedCh: make(chan struct{}),
	}
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.setAuthRequired(true)
	state.setAuthPending(true)

	if !exec.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP}}) {
		t.Fatal("auth submit rejected")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("auth task did not run")
	}
	if got := exec.depth(); got != 0 {
		t.Fatalf("auth queue depth = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run auth executor tests and verify they fail**

Run:

```bash
go test ./pkg/gateway/core -run 'TestAuthExecutorSubmitRejectsWhenFull|TestAuthExecutorRunsQueuedTaskOnAnts' -count=1
```

Expected: FAIL because `authExecutor.submit` is still a stub.

- [ ] **Step 3: Implement ants-backed auth executor**

Replace `pkg/gateway/core/async_auth.go` with:

```go
package core

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/panjf2000/ants/v2"
)

const asyncAuthDrainRetry = time.Millisecond

type asyncAuthTask struct {
	state      *sessionState
	replyToken string
	connect    *frame.ConnectPacket
	enqueuedAt time.Time
	queue      *authExecutor
}

type authExecutor struct {
	server   *Server
	pool     *ants.PoolWithFuncGeneric[asyncAuthTask]
	tasks    chan asyncAuthTask
	wake     chan struct{}
	done     chan struct{}
	workers  int
	capacity int
	queued   atomic.Int64

	mu     sync.Mutex
	closed bool
}

func newAuthExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*authExecutor, error) {
	if opts.AsyncAuthWorkers <= 0 {
		opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	}
	exec := &authExecutor{
		server:   s,
		tasks:    make(chan asyncAuthTask, opts.AsyncAuthQueueCapacity),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		workers:  opts.AsyncAuthWorkers,
		capacity: opts.AsyncAuthQueueCapacity,
	}
	pool, err := ants.NewPoolWithFuncGeneric[asyncAuthTask](
		exec.workers,
		func(task asyncAuthTask) {
			exec.consume(1)
			if exec.server != nil {
				exec.server.observeAsyncAuthQueue(exec)
				exec.server.observeAsyncAuthWait(task)
				exec.server.runAuthTask(task)
			}
			exec.notify()
		},
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(any) {
			if exec.server != nil && exec.server.options.Logger != nil {
				exec.server.options.Logger.Warn("gateway async auth task panic")
			}
			exec.notify()
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("gateway/core: create async auth executor: %w", err)
	}
	exec.pool = pool
	go exec.run()
	return exec, nil
}

func (e *authExecutor) submit(task asyncAuthTask) bool {
	if e == nil || task.state == nil || task.connect == nil {
		return false
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return false
	}
	task.connect = cloneAuthConnectPacket(task.connect)
	task.enqueuedAt = time.Now()
	task.queue = e
	select {
	case e.tasks <- task:
		e.queued.Add(1)
		e.mu.Unlock()
		e.notify()
		return true
	default:
		e.mu.Unlock()
		return false
	}
}

func (e *authExecutor) run() {
	defer close(e.done)
	var pending asyncAuthTask
	hasPending := false
	for {
		var task asyncAuthTask
		if hasPending {
			task = pending
		} else {
			next, ok := e.nextTask()
			if !ok {
				return
			}
			task = next
		}
		err := e.pool.Invoke(task)
		if err == nil {
			hasPending = false
			pending = asyncAuthTask{}
			continue
		}
		if isAntsStopped(err) || e.isClosed() {
			e.consume(1)
			hasPending = false
			pending = asyncAuthTask{}
			continue
		}
		if isAntsBusy(err) {
			hasPending = true
			pending = task
			e.waitForRetry()
			continue
		}
		e.consume(1)
		hasPending = false
		pending = asyncAuthTask{}
	}
}

func (e *authExecutor) nextTask() (asyncAuthTask, bool) {
	for {
		select {
		case task, ok := <-e.tasks:
			return task, ok
		case <-e.wake:
			if e.isClosed() {
				return asyncAuthTask{}, false
			}
		}
	}
}

func (e *authExecutor) waitForRetry() {
	timer := time.NewTimer(asyncAuthDrainRetry)
	defer timer.Stop()
	select {
	case <-e.wake:
	case <-timer.C:
	}
}

func (e *authExecutor) notify() {
	if e == nil {
		return
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *authExecutor) isClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

func (e *authExecutor) consume(count int) {
	if e == nil || count <= 0 {
		return
	}
	remaining := e.queued.Add(-int64(count))
	if remaining >= 0 {
		return
	}
	e.queued.Add(-remaining)
}

func (e *authExecutor) depth() int {
	if e == nil {
		return 0
	}
	depth := e.queued.Load()
	if depth < 0 {
		return 0
	}
	return int(depth)
}

func (e *authExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}

func (e *authExecutor) stop() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.tasks)
	e.mu.Unlock()
	e.notify()
	<-e.done
	if e.pool != nil {
		_ = e.pool.ReleaseTimeout(gatewaytypes.NormalizeRuntimeOptions(e.server.options.Runtime).AsyncPoolReleaseTimeout)
	}
}

func cloneAuthConnectPacket(connect *frame.ConnectPacket) *frame.ConnectPacket {
	if connect == nil {
		return nil
	}
	cloned := *connect
	return &cloned
}
```

- [ ] **Step 4: Update auth observation signatures**

In `pkg/gateway/core/server.go`, change:

```go
func (s *Server) observeAsyncAuthQueue(queue *asyncAuthQueue)
func (s *Server) observeAsyncAuthAdmission(_ *asyncAuthQueue, accepted bool)
```

to:

```go
func (s *Server) observeAsyncAuthQueue(queue *authExecutor) {
	observer := s.asyncAuthObserver()
	if observer == nil || queue == nil {
		return
	}
	observer.OnAsyncAuthQueue(gatewaytypes.AsyncAuthQueueEvent{
		Depth:    queue.depth(),
		Capacity: queue.totalCapacity(),
		Workers:  queue.workers,
	})
}

func (s *Server) observeAsyncAuthAdmission(_ *authExecutor, accepted bool) {
	observer := s.asyncAuthObserver()
	if observer == nil {
		return
	}
	result := "full"
	if accepted {
		result = "ok"
	}
	observer.OnAsyncAuthAdmission(gatewaytypes.AsyncAuthAdmissionEvent{Result: result})
}
```

- [ ] **Step 5: Run auth executor tests and verify they pass**

Run:

```bash
go test ./pkg/gateway/core -run 'TestAuthExecutorSubmitRejectsWhenFull|TestAuthExecutorRunsQueuedTaskOnAnts|TestAsyncAuthReportsQueueAndAdmission|TestAsyncAuthReportsWaitWithNonNegativeDuration' -count=1
```

Expected: PASS after updating remaining test references from `asyncAuthQueue` to `authExecutor`.

- [ ] **Step 6: Commit auth executor**

```bash
git add pkg/gateway/core/async_auth.go pkg/gateway/core/async_auth_test.go pkg/gateway/core/server.go
git commit -m "feat: run gateway auth on ants executor"
```

---

### Task 4: SEND Executor On Ants

**Files:**
- Modify: `pkg/gateway/core/async_send.go`
- Modify: `pkg/gateway/core/async_dispatch_test.go`
- Modify: `pkg/gateway/core/server.go`

- [ ] **Step 1: Write failing SEND executor tests**

In `pkg/gateway/core/async_dispatch_test.go`, replace `TestServerAsyncSendDispatchUsesConfiguredWorkerCount` and `TestAsyncDispatchQueueBoundsBufferedCapacityForManyWorkers` with:

```go
func TestSendExecutorUsesRuntimeWorkerCountAndCapacity(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        4,
				AsyncSendQueueCapacity:  16,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
		},
	}
	exec, err := newSendExecutor(srv, gatewaytypes.NormalizeRuntimeOptions(srv.options.Runtime))
	if err != nil {
		t.Fatalf("newSendExecutor failed: %v", err)
	}
	defer exec.stop()

	if got := exec.workers; got != 4 {
		t.Fatalf("send workers = %d, want 4", got)
	}
	if got := len(exec.shards); got != 4 {
		t.Fatalf("send shards = %d, want 4", got)
	}
	if got := exec.totalCapacity(); got != 16 {
		t.Fatalf("send total capacity = %d, want 16", got)
	}
}

func TestSendExecutorRejectsWhenShardMailboxFullBeforePayloadClone(t *testing.T) {
	srv := &Server{dispatcher: newDispatcher(&countingAsyncFrameHandler{})}
	exec, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("newSendExecutor failed: %v", err)
	}
	defer exec.stop()

	shard := exec.shards[0]
	shard.tasks <- asyncDispatchTask{}
	exec.queued.Add(1)

	payload := make([]byte, 64*1024)
	packet := &frame.SendPacket{Payload: payload}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	baseline := testing.AllocsPerRun(1000, func() {
		state.close(gatewaytypes.CloseReasonAsyncDispatchQueueFull, gatewaytypes.ErrAsyncDispatchQueueFull)
	})
	actual := testing.AllocsPerRun(1000, func() {
		_ = exec.submit(&sessionState{
			server:         srv,
			closedCh:       make(chan struct{}),
			requestContext: context.Background(),
		}, "", packet)
	})
	if actual > baseline+0.5 {
		t.Fatalf("allocs = %.2f, want near close baseline %.2f", actual, baseline)
	}
}
```

- [ ] **Step 2: Run SEND executor tests and verify they fail**

Run:

```bash
go test ./pkg/gateway/core -run 'TestSendExecutorUsesRuntimeWorkerCountAndCapacity|TestSendExecutorRejectsWhenShardMailboxFullBeforePayloadClone' -count=1
```

Expected: FAIL because `sendExecutor` does not yet have shards or real submission.

- [ ] **Step 3: Implement sharded SEND executor**

Replace the minimal `sendExecutor` in `pkg/gateway/core/async_send.go` with:

```go
type sendExecutor struct {
	server   *Server
	pool     *ants.PoolWithFuncGeneric[*asyncSendShard]
	shards   []*asyncSendShard
	workers  int
	capacity int
	queued   atomic.Int64
	closed   atomic.Bool
}

type asyncSendShard struct {
	executor *sendExecutor
	tasks    chan asyncDispatchTask
	mu       sync.Mutex
	scheduled bool
	closed    bool
}
```

Use this constructor body:

```go
func newSendExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*sendExecutor, error) {
	opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	exec := &sendExecutor{
		server:   s,
		workers:  opts.AsyncSendWorkers,
		capacity: opts.AsyncSendQueueCapacity,
	}
	pool, err := ants.NewPoolWithFuncGeneric[*asyncSendShard](
		exec.workers,
		func(shard *asyncSendShard) {
			if shard != nil {
				shard.drain()
			}
		},
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(any) {
			if exec.server != nil && exec.server.options.Logger != nil {
				exec.server.options.Logger.Warn("gateway async send shard panic")
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("gateway/core: create async send executor: %w", err)
	}
	exec.pool = pool
	shards := exec.workers
	if shards <= 0 {
		shards = 1
	}
	perShard := asyncSendCapacityPerShard(shards, exec.capacity)
	exec.shards = make([]*asyncSendShard, shards)
	for i := range exec.shards {
		exec.shards[i] = &asyncSendShard{
			executor: exec,
			tasks:    make(chan asyncDispatchTask, perShard),
		}
	}
	return exec, nil
}
```

Add:

```go
func asyncSendCapacityPerShard(shards int, total int) int {
	if shards <= 0 {
		shards = 1
	}
	if total <= 0 {
		total = asyncDispatchMaxBufferedTasks
	}
	capacity := (total + shards - 1) / shards
	if capacity < asyncDispatchMinQueuePerWorker {
		return asyncDispatchMinQueuePerWorker
	}
	if capacity > asyncDispatchQueuePerWorker {
		return asyncDispatchQueuePerWorker
	}
	return capacity
}

func (e *sendExecutor) submit(state *sessionState, replyToken string, send *frame.SendPacket) bool {
	if e == nil || send == nil || len(e.shards) == 0 || e.closed.Load() {
		return false
	}
	shard := e.shardFor(state, send)
	shard.mu.Lock()
	if shard.closed || len(shard.tasks) >= cap(shard.tasks) {
		shard.mu.Unlock()
		return false
	}
	task := asyncDispatchTask{
		state:      state,
		replyToken: replyToken,
		frame:      cloneAsyncSendFrame(send, stateOwnsDecodedFrames(state)),
		enqueuedAt: time.Now(),
		queue:      e,
	}
	shard.tasks <- task
	e.queued.Add(1)
	shouldSchedule := !shard.scheduled
	if shouldSchedule {
		shard.scheduled = true
	}
	shard.mu.Unlock()
	if shouldSchedule {
		e.schedule(shard)
	}
	return true
}
```

Add scheduling and drain:

```go
func (e *sendExecutor) schedule(shard *asyncSendShard) {
	if e == nil || shard == nil || e.pool == nil {
		return
	}
	err := e.pool.Invoke(shard)
	if err == nil {
		return
	}
	if isAntsStopped(err) || e.closed.Load() {
		shard.markUnscheduled()
		return
	}
	time.AfterFunc(time.Millisecond, func() {
		e.schedule(shard)
	})
}

func (s *asyncSendShard) drain() {
	if s == nil || s.executor == nil || s.executor.server == nil {
		return
	}
	collector := newAsyncSendBatchCollector(s.tasks, asyncSendBatchLimitsFromOptions(s.executor.server.options.DefaultSession))
	for {
		batch, ok := collector.nextBatch()
		if !ok {
			s.markUnscheduled()
			return
		}
		s.executor.consume(len(batch))
		s.executor.server.observeAsyncSendQueue(s.executor)
		s.executor.server.observeAsyncSendBatch(batch)
		if s.executor.server.dispatchSendBatch(batch) {
			continue
		}
		for _, task := range batch {
			s.executor.server.recordAsyncDispatchWait(task)
			if err := s.executor.server.dispatchFrame(task.state, task.replyToken, task.frame); err != nil {
				s.executor.server.handleHandlerError(task.state, err)
			}
		}
		if len(s.tasks) == 0 {
			s.markUnscheduled()
			if len(s.tasks) == 0 {
				return
			}
			if s.tryScheduleAgain() {
				return
			}
		}
	}
}
```

Add shard helpers:

```go
func (s *asyncSendShard) markUnscheduled() {
	s.mu.Lock()
	s.scheduled = false
	s.mu.Unlock()
}

func (s *asyncSendShard) tryScheduleAgain() bool {
	s.mu.Lock()
	if s.scheduled || len(s.tasks) == 0 {
		s.mu.Unlock()
		return false
	}
	s.scheduled = true
	s.mu.Unlock()
	s.executor.schedule(s)
	return true
}
```

Add executor helpers:

```go
func (e *sendExecutor) shardFor(state *sessionState, send frame.Frame) *asyncSendShard {
	if e == nil || len(e.shards) == 0 {
		return nil
	}
	return e.shards[asyncDispatchShardIndex(state, send, len(e.shards))]
}

func (e *sendExecutor) consume(count int) {
	if e == nil || count <= 0 {
		return
	}
	remaining := e.queued.Add(-int64(count))
	if remaining >= 0 {
		return
	}
	e.queued.Add(-remaining)
}

func (e *sendExecutor) depth() int {
	if e == nil {
		return 0
	}
	depth := e.queued.Load()
	if depth < 0 {
		return 0
	}
	return int(depth)
}

func (e *sendExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}

func (e *sendExecutor) stop() {
	if e == nil || e.closed.Swap(true) {
		return
	}
	for _, shard := range e.shards {
		shard.mu.Lock()
		shard.closed = true
		close(shard.tasks)
		shard.mu.Unlock()
	}
	if e.pool != nil {
		_ = e.pool.ReleaseTimeout(gatewaytypes.NormalizeRuntimeOptions(e.server.options.Runtime).AsyncPoolReleaseTimeout)
	}
}
```

- [ ] **Step 4: Update SEND observation signatures**

In `pkg/gateway/core/server.go`, change:

```go
func (s *Server) observeAsyncSendQueue(queue *asyncDispatchQueue)
func (s *Server) observeAsyncSendAdmission(_ *asyncDispatchQueue, accepted bool)
func asyncSendBatchQueue(batch []asyncDispatchTask) *asyncDispatchQueue
```

to:

```go
func (s *Server) observeAsyncSendQueue(queue *sendExecutor) {
	observer := s.asyncSendObserver()
	if observer == nil || queue == nil {
		return
	}
	observer.OnAsyncSendQueue(gatewaytypes.AsyncSendQueueEvent{
		Depth:    queue.depth(),
		Capacity: queue.totalCapacity(),
	})
}

func (s *Server) observeAsyncSendAdmission(_ *sendExecutor, accepted bool) {
	observer := s.asyncSendAdmissionObserver()
	if observer == nil {
		return
	}
	result := "full"
	if accepted {
		result = "ok"
	}
	observer.OnAsyncSendAdmission(gatewaytypes.AsyncSendAdmissionEvent{Result: result})
}
```

Delete `observeAsyncSendDequeued` and `asyncSendBatchQueue` after shard drains call `queue.consume` directly.

- [ ] **Step 5: Run SEND executor tests and verify they pass**

Run:

```bash
go test ./pkg/gateway/core -run 'TestSendExecutor|TestAsyncSendBatch|TestAsyncSendDispatch|TestRecordAsyncDispatchWait' -count=1
```

Expected: PASS after updating remaining test references from `asyncDispatchQueue` to `sendExecutor`.

- [ ] **Step 6: Commit SEND executor**

```bash
git add pkg/gateway/core/async_send.go pkg/gateway/core/async_dispatch_test.go pkg/gateway/core/server.go
git commit -m "feat: run gateway send dispatch on ants executor"
```

---

### Task 5: Wire Server Lifecycle To Async Runtime

**Files:**
- Modify: `pkg/gateway/core/server.go`
- Modify: `pkg/gateway/core/server_test.go`
- Modify: `pkg/gateway/core/server_benchmark_test.go`

- [ ] **Step 1: Write failing server runtime worker-count test**

In `pkg/gateway/core/server_test.go`, replace any use of `AsyncSendDispatchWorkers` with `Runtime.AsyncSendWorkers`. Add this test near the SEND dispatch tests:

```go
t.Run("runtime async send workers control dispatch concurrency", func(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("wkproto")
	srv, transportFactory := newTestServerWithRuntime(t, handler, proto, gateway.SessionOptions{}, gateway.RuntimeOptions{
		AsyncSendWorkers:        2,
		AsyncSendQueueCapacity:  8,
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  4,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	if srv.TestAsyncRuntimeSendWorkers() != 2 {
		t.Fatalf("send workers = %d, want 2", srv.TestAsyncRuntimeSendWorkers())
	}
	_ = transportFactory
})
```

Add test-only helpers in `pkg/gateway/core/server_export_test.go`:

```go
func (s *Server) TestAsyncRuntimeSendWorkers() int {
	runtime := s.async.Load()
	if runtime == nil || runtime.send == nil {
		return 0
	}
	return runtime.send.workers
}
```

Add a new helper in `pkg/gateway/core/server_test.go`:

```go
func newTestServerWithRuntime(t *testing.T, handler gateway.Handler, proto *scriptedProtocol, sessOpts gateway.SessionOptions, runtimeOpts gateway.RuntimeOptions) (*core.Server, *testkit.FakeTransportFactory) {
	t.Helper()
	transportFactory := testkit.NewFakeTransportFactory("fake-transport")
	registry := core.NewRegistry()
	if err := registry.RegisterTransport(transportFactory); err != nil {
		t.Fatalf("register transport failed: %v", err)
	}
	if err := registry.RegisterProtocol(proto); err != nil {
		t.Fatalf("register protocol failed: %v", err)
	}
	srv, err := core.NewServer(registry, &gateway.Options{
		Handler:        handler,
		DefaultSession: sessOpts,
		Runtime:        runtimeOpts,
		Listeners: []gateway.ListenerOptions{{
			Name:      "listener-a",
			Network:   "tcp",
			Address:   "127.0.0.1:9000",
			Transport: transportFactory.Name(),
			Protocol:  proto.Name(),
		}},
	})
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}
	return srv, transportFactory
}
```

- [ ] **Step 2: Run the lifecycle test and verify it fails**

Run:

```bash
go test ./pkg/gateway/core -run 'runtime async send workers control dispatch concurrency' -count=1
```

Expected: FAIL because `Server` is not wired to `asyncRuntime`.

- [ ] **Step 3: Replace Server async fields**

In `pkg/gateway/core/server.go`, replace:

```go
// asyncDispatch bounds SEND frame concurrency off the transport event loop.
asyncDispatch atomic.Pointer[asyncDispatchQueue]
// asyncAuth bounds CONNECT authentication and activation off the transport event loop.
asyncAuth atomic.Pointer[asyncAuthQueue]
workerWG  sync.WaitGroup
```

with:

```go
// async owns ants-backed asynchronous gateway auth and SEND execution.
async atomic.Pointer[asyncRuntime]
workerWG sync.WaitGroup
```

Keep `workerWG` for the idle monitor only.

- [ ] **Step 4: Replace start and rollback lifecycle**

In `Start`, replace:

```go
s.startAsyncAuthenticator()
s.startAsyncDispatcher()
```

with:

```go
async, err := newAsyncRuntime(s)
if err != nil {
	s.rollbackRuntimeListeners(runtimes)
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
	return err
}
s.async.Store(async)
```

In the listener start failure branch, replace:

```go
s.stopAsyncWorkers()
```

with:

```go
s.stopAsyncRuntime()
```

- [ ] **Step 5: Replace Stop lifecycle**

In `Stop`, replace async queue swaps:

```go
asyncAuth := s.asyncAuth.Swap(nil)
asyncDispatch := s.asyncDispatch.Swap(nil)
```

with:

```go
async := s.async.Swap(nil)
```

Replace:

```go
s.closeAsyncWorkers(asyncAuth, asyncDispatch)
```

with:

```go
s.closeAsyncRuntime(async)
s.workerWG.Wait()
```

Replace `stopAsyncWorkers` and `closeAsyncWorkers` with:

```go
func (s *Server) stopAsyncRuntime() {
	if s == nil {
		return
	}
	async := s.async.Swap(nil)
	s.closeAsyncRuntime(async)
}

func (s *Server) closeAsyncRuntime(async *asyncRuntime) {
	if async != nil {
		async.stop()
	}
}
```

- [ ] **Step 6: Wire auth and SEND submissions**

Replace `asyncAuthenticator()` and `asyncDispatcher()` with:

```go
func (s *Server) asyncRuntime() *asyncRuntime {
	if s == nil {
		return nil
	}
	return s.async.Load()
}
```

In `handleAuthFrame`, replace:

```go
queue := s.asyncAuthenticator()
accepted := queue != nil && queue.submit(asyncAuthTask{state: state, replyToken: replyToken, connect: connect})
s.observeAsyncAuthAdmission(queue, accepted)
s.observeAsyncAuthQueue(queue)
```

with:

```go
runtime := s.asyncRuntime()
var auth *authExecutor
if runtime != nil {
	auth = runtime.auth
}
accepted := runtime != nil && runtime.submitAuth(asyncAuthTask{state: state, replyToken: replyToken, connect: connect})
s.observeAsyncAuthAdmission(auth, accepted)
s.observeAsyncAuthQueue(auth)
```

In `dispatchSendFrameAsync`, replace:

```go
queue := s.asyncDispatcher()
accepted := queue != nil && queue.submitSend(state, replyToken, send)
s.observeAsyncSendAdmission(queue, accepted)
```

with:

```go
runtime := s.asyncRuntime()
var sendExec *sendExecutor
if runtime != nil {
	sendExec = runtime.send
}
accepted := runtime != nil && runtime.submitSend(state, replyToken, send)
s.observeAsyncSendAdmission(sendExec, accepted)
```

Keep the existing close-on-rejection behavior.

- [ ] **Step 7: Run server lifecycle tests**

Run:

```bash
go test ./pkg/gateway/core -run 'runtime async send workers control dispatch concurrency|TestServerAsyncSendDispatchUsesBoundedWorkers|TestServerIdleMonitorUsesSharedWorker' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit lifecycle wiring**

```bash
git add pkg/gateway/core/server.go pkg/gateway/core/server_test.go pkg/gateway/core/server_export_test.go pkg/gateway/core/server_benchmark_test.go
git commit -m "feat: wire gateway server to async runtime"
```

---

### Task 6: Remove Old Queue Model And Update Tests

**Files:**
- Modify: `pkg/gateway/core/server.go`
- Modify: `pkg/gateway/core/async_auth_test.go`
- Modify: `pkg/gateway/core/async_dispatch_test.go`
- Modify: `pkg/gateway/core/server_test.go`

- [ ] **Step 1: Remove old worker and queue code from server.go**

Delete these declarations from `pkg/gateway/core/server.go`:

```text
Server.startAsyncAuthenticator
Server.runAsyncAuthWorker
Server.startAsyncDispatcher
Server.runAsyncDispatchWorker
asyncAuthQueue
asyncDispatchQueue
asyncDispatchShard
newAsyncAuthQueue
newAsyncAuthQueueWithCapacity
asyncAuthQueueCapacity
newAsyncDispatchQueue
newAsyncDispatchQueueWithCapacity
asyncDispatchQueueCapacityPerShard
asyncAuthQueue.submit
asyncAuthQueue.consume
asyncAuthQueue.depth
asyncAuthQueue.totalCapacity
asyncAuthQueue.close
asyncDispatchQueue.submitSend
asyncDispatchQueue.consume
asyncDispatchQueue.depth
asyncDispatchQueue.totalCapacity
asyncDispatchQueue.shardForSend
asyncDispatchQueue.close
```

Keep these helpers by moving them to `async_send.go` if they still live in `server.go`:

```go
func asyncSendBatchLimitsFromOptions(opt gatewaytypes.SessionOptions) asyncSendBatchLimits
func asyncDispatchTaskByteCount(task asyncDispatchTask) int
func recordAsyncDispatchWait(task asyncDispatchTask) time.Duration
func asyncDispatchShardIndex(state *sessionState, _ frame.Frame, shards int) int
func cloneAsyncSendFrame(send *frame.SendPacket, ownsDecodedFrames bool) frame.Frame
func stateOwnsDecodedFrames(state *sessionState) bool
```

- [ ] **Step 2: Update tests to stop constructing old queues**

In `pkg/gateway/core/async_auth_test.go`:

- Use `newAuthExecutor` instead of `newAsyncAuthQueueWithCapacity`.
- Use `srv.async.Store(&asyncRuntime{auth: exec})` for tests that call `handleAuthFrame` directly.
- Remove direct reads from `queue.tasks`.
- Assert `exec.depth()` and `exec.totalCapacity()`.

In `pkg/gateway/core/async_dispatch_test.go`:

- Use `newSendExecutor` instead of `newAsyncDispatchQueueWithCapacity`.
- Use `srv.async.Store(&asyncRuntime{send: exec})` for tests that call `dispatchSendFrameAsync` directly.
- Replace `queuedAsyncSendPacket(t, queue)` with:

```go
func queuedAsyncSendPacket(t *testing.T, exec *sendExecutor) *frame.SendPacket {
	t.Helper()
	if exec == nil || len(exec.shards) == 0 {
		t.Fatal("send executor has no shards")
	}
	select {
	case task := <-exec.shards[0].tasks:
		send, ok := task.frame.(*frame.SendPacket)
		if !ok {
			t.Fatalf("queued frame = %T, want *frame.SendPacket", task.frame)
		}
		return send
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued send")
		return nil
	}
}
```

- [ ] **Step 3: Run package tests and fix stale references**

Run:

```bash
go test ./pkg/gateway/core -count=1
```

Expected: FAIL only for stale references to removed names. Replace each stale reference with the new runtime/executor helper from this task.

- [ ] **Step 4: Re-run package tests and verify they pass**

Run:

```bash
go test ./pkg/gateway/core -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit queue model removal**

```bash
git add pkg/gateway/core/server.go pkg/gateway/core/async_auth.go pkg/gateway/core/async_send.go pkg/gateway/core/async_auth_test.go pkg/gateway/core/async_dispatch_test.go pkg/gateway/core/server_test.go
git commit -m "refactor: remove gateway async worker queues"
```

---

### Task 7: Public Gateway Test Sweep

**Files:**
- Modify: `pkg/gateway/core/server_test.go`
- Modify: `pkg/gateway/core/server_benchmark_test.go`
- Modify: `pkg/gateway/options_test.go`

- [ ] **Step 1: Run targeted gateway packages**

Run:

```bash
go test ./pkg/gateway/core ./pkg/gateway/types ./pkg/gateway -count=1
```

Expected: PASS or compile failures pointing to `AsyncSendDispatchWorkers` references.

- [ ] **Step 2: Replace remaining session worker option references**

For every remaining test or helper using:

```go
gateway.SessionOptions{AsyncSendDispatchWorkers: N}
```

replace it with a runtime option on `gateway.Options`:

```go
gateway.Options{
	DefaultSession: sessOpts,
	Runtime: gateway.RuntimeOptions{
		AsyncSendWorkers:        N,
		AsyncSendQueueCapacity:  gateway.DefaultRuntimeOptions().AsyncSendQueueCapacity,
		AsyncAuthWorkers:        gateway.DefaultRuntimeOptions().AsyncAuthWorkers,
		AsyncAuthQueueCapacity:  gateway.DefaultRuntimeOptions().AsyncAuthQueueCapacity,
		AsyncPoolReleaseTimeout: gateway.DefaultRuntimeOptions().AsyncPoolReleaseTimeout,
	},
}
```

For helper functions that currently accept only `gateway.SessionOptions`, add a sibling helper that also accepts `gateway.RuntimeOptions` instead of widening every call site.

- [ ] **Step 3: Re-run targeted gateway packages**

Run:

```bash
go test ./pkg/gateway/core ./pkg/gateway/types ./pkg/gateway -count=1
```

Expected: PASS.

- [ ] **Step 4: Run benchmark compile checks**

Run:

```bash
go test ./pkg/gateway/core -run '^$' -bench 'ServerAsyncSend|Gateway|Async' -benchmem -count=1
```

Expected: PASS. Record benchmark output in the task notes for comparison; do not tune based on one local run.

- [ ] **Step 5: Commit gateway test sweep**

```bash
git add pkg/gateway/core pkg/gateway/options_test.go
git commit -m "test: update gateway async runtime coverage"
```

---

### Task 8: Final Verification

**Files:**
- No code changes expected in this task.

- [ ] **Step 1: Run targeted tests**

Run:

```bash
go test ./pkg/gateway/core ./pkg/gateway/types ./pkg/gateway -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader gateway tests**

Run:

```bash
go test ./pkg/gateway/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run final repository unit tests if time allows**

Run:

```bash
go test ./...
```

Expected: PASS. If this is too slow or unrelated dirty worktree changes make it unreliable, record the exact reason and keep the targeted gateway results as the completion gate for this refactor.

- [ ] **Step 4: Inspect final diff**

Run:

```bash
git diff --stat HEAD
git status --short
```

Expected: only intentional gateway async runtime files are modified or staged by this work. Existing unrelated `internalv2`, config, and `webv2` changes from the starting worktree remain outside this refactor.

- [ ] **Step 5: Commit any verification-only cleanups**

If final verification required tiny test-only adjustments, commit them:

```bash
git add pkg/gateway/core pkg/gateway/types pkg/gateway/options.go pkg/gateway/options_test.go
git commit -m "test: verify gateway ants runtime"
```

If no files changed, do not create an empty commit.

---

## Self-Review Checklist

- Spec coverage: runtime options, auth executor, SEND executor, server lifecycle, observations, shutdown, and tests are all covered by tasks.
- No old worker-loop compatibility layer remains after Task 6.
- Ants is used only as execution capacity; explicit gateway queues own backpressure.
- Idle monitor remains outside ants.
- Public config impact is limited to `pkg/gateway/types.Options.Runtime`; application config is not surfaced in this plan.
- Test commands avoid integration tags.
