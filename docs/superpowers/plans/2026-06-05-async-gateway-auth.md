# Async Gateway Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move gateway CONNECT authentication and activation onto a bounded async worker pool while rejecting any frame received before auth completes.

**Architecture:** Keep decode and protocol gating in `pkg/gateway/core/server.go`, then enqueue the first CONNECT into a dedicated async auth queue. Auth workers run the existing authenticate/activate/CONNACK/open sequence, while `sessionState.authPending` prevents duplicate CONNECT or business frames from being accepted during the handshake.

**Tech Stack:** Go, `pkg/gateway/core`, existing fake transport/protocol testkit, `go test`.

---

## File Structure

- Modify `pkg/gateway/types/errors.go`: add auth queue overflow error and close reason.
- Modify `pkg/gateway/errors.go`: re-export the new public error and close reason.
- Modify `pkg/gateway/core/server.go`: add async auth constants, server queue field, auth pending state, queue implementation, worker lifecycle, and extracted auth execution.
- Modify `pkg/gateway/core/server_test.go`: add black-box auth async behavior tests and adjust existing auth tests to wait for async completion.
- Create `pkg/gateway/core/async_auth_test.go`: add package-internal tests for queue full and worker sizing helpers.
- Modify `pkg/gateway/FLOW.md`: document async CONNECT auth and pending-frame rejection.

The implementation deliberately stays in `server.go` because existing async dispatch, auth failure attribution, and session state are already local to that file. A later split can be considered only if `server.go` becomes harder to navigate after this change.

---

### Task 1: Public Error Surface

**Files:**
- Modify: `pkg/gateway/types/errors.go`
- Modify: `pkg/gateway/errors.go`
- Test: `pkg/gateway/options_test.go`

- [ ] **Step 1: Write the failing public alias test**

Add this test near the existing gateway option/error tests in `pkg/gateway/options_test.go`:

```go
func TestGatewayExportsAsyncAuthQueueFullError(t *testing.T) {
	if gateway.ErrAsyncAuthQueueFull == nil {
		t.Fatal("ErrAsyncAuthQueueFull is nil")
	}
	if gateway.CloseReasonAsyncAuthQueueFull != "async_auth_queue_full" {
		t.Fatalf("CloseReasonAsyncAuthQueueFull = %q", gateway.CloseReasonAsyncAuthQueueFull)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
go test ./pkg/gateway -run TestGatewayExportsAsyncAuthQueueFullError -count=1
```

Expected: FAIL because `gateway.ErrAsyncAuthQueueFull` and `gateway.CloseReasonAsyncAuthQueueFull` are not defined.

- [ ] **Step 3: Add typed error and close reason**

In `pkg/gateway/types/errors.go`, add the new error beside `ErrAsyncDispatchQueueFull` and the new close reason beside `CloseReasonAsyncDispatchQueueFull`:

```go
ErrAsyncAuthQueueFull       = errors.New("gateway: async auth queue is full")
```

```go
CloseReasonAsyncAuthQueueFull     CloseReason = "async_auth_queue_full"
```

In `pkg/gateway/errors.go`, re-export both values beside the existing async dispatch exports:

```go
ErrAsyncAuthQueueFull       = gatewaytypes.ErrAsyncAuthQueueFull
```

```go
CloseReasonAsyncAuthQueueFull     = gatewaytypes.CloseReasonAsyncAuthQueueFull
```

- [ ] **Step 4: Run the public gateway test**

Run:

```bash
go test ./pkg/gateway -run TestGatewayExportsAsyncAuthQueueFullError -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Stage only the files from this task:

```bash
git add pkg/gateway/types/errors.go pkg/gateway/errors.go pkg/gateway/options_test.go
git commit -m "feat: add async auth queue error"
```

---

### Task 2: Failing Async Auth Behavior Tests

**Files:**
- Modify: `pkg/gateway/core/server_test.go`

- [ ] **Step 1: Add helper methods for close reasons on the observer**

Add this method after `recordingObserver.closeCount` in `pkg/gateway/core/server_test.go`:

```go
func (r *recordingObserver) closeReasons() []gateway.CloseReason {
	r.mu.Lock()
	defer r.mu.Unlock()

	reasons := make([]gateway.CloseReason, len(r.closes))
	for i := range r.closes {
		reasons[i] = r.closes[i].CloseReason
	}
	return reasons
}
```

- [ ] **Step 2: Add the non-blocking CONNECT test**

Add this subtest inside `TestServer`, near the existing authentication subtests:

```go
t.Run("connect authentication dispatches asynchronously and does not block data return", func(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("wkproto")
	proto.encodedBytes = []byte("connack-success")
	proto.pushDecode(decodeResult{
		frames: []frame.Frame{&frame.ConnectPacket{
			UID:        "u1",
			DeviceID:   "d-1",
			DeviceFlag: frame.APP,
		}},
		consumed: 1,
	})

	authStarted := make(chan struct{})
	releaseAuth := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseAuth) }) }
	authenticator := gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
		close(authStarted)
		<-releaseAuth
		return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
	})

	srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, authenticator)
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	t.Cleanup(release)

	conn := transportFactory.MustOpen("listener-a", 1)
	dataReturned := make(chan error, 1)
	go func() {
		dataReturned <- conn.EmitData([]byte("c"))
	}()

	waitFor(t, func() bool {
		select {
		case <-authStarted:
			return true
		default:
			return false
		}
	})

	select {
	case err := <-dataReturned:
		if err != nil {
			t.Fatalf("emit data failed: %v", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected CONNECT auth not to block EmitData")
	}

	release()
	waitFor(t, func() bool { return len(conn.Writes()) == 1 && len(handler.callOrder()) == 1 })
})
```

- [ ] **Step 3: Add pending auth protocol violation tests**

Add these subtests inside `TestServer`, after the non-blocking test:

```go
t.Run("same decode batch frame after connect is rejected while auth is pending", func(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("wkproto")
	proto.pushDecode(decodeResult{
		frames: []frame.Frame{
			&frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP},
			&frame.PingPacket{},
		},
		consumed: 1,
	})

	authStarted := make(chan struct{})
	releaseAuth := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseAuth) }) }
	authenticator := gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
		close(authStarted)
		<-releaseAuth
		return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
	})

	observer := &recordingObserver{}
	srv, transportFactory := newTestServerWithObserver(t, handler, proto, gateway.SessionOptions{}, authenticator, observer)
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	t.Cleanup(release)

	conn := transportFactory.MustOpen("listener-a", 1)
	transportFactory.MustData("listener-a", 1, []byte("cp"))

	waitFor(t, func() bool { return connClosed(conn) && observer.closeCount() == 1 })
	release()

	if got := handler.frameCount(); got != 0 {
		t.Fatalf("handler frames = %d, want 0", got)
	}
	reasons := observer.closeReasons()
	if got := reasons[len(reasons)-1]; got != gateway.CloseReasonPolicyViolation {
		t.Fatalf("close reason = %q, want %q", got, gateway.CloseReasonPolicyViolation)
	}
})

t.Run("second connect while auth is pending is rejected", func(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("wkproto")
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP}},
		consumed: 1,
	})
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP}},
		consumed: 1,
	})

	authStarted := make(chan struct{})
	releaseAuth := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseAuth) }) }
	authenticator := gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
		close(authStarted)
		<-releaseAuth
		return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
	})

	observer := &recordingObserver{}
	srv, transportFactory := newTestServerWithObserver(t, handler, proto, gateway.SessionOptions{}, authenticator, observer)
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	t.Cleanup(release)

	conn := transportFactory.MustOpen("listener-a", 1)
	transportFactory.MustData("listener-a", 1, []byte("c"))
	waitFor(t, func() bool {
		select {
		case <-authStarted:
			return true
		default:
			return false
		}
	})

	transportFactory.MustData("listener-a", 1, []byte("c"))

	waitFor(t, func() bool { return connClosed(conn) && observer.closeCount() == 1 })
	release()

	reasons := observer.closeReasons()
	if got := reasons[len(reasons)-1]; got != gateway.CloseReasonPolicyViolation {
		t.Fatalf("close reason = %q, want %q", got, gateway.CloseReasonPolicyViolation)
	}
})
```

- [ ] **Step 4: Add peer-close during auth test**

Add this subtest inside `TestServer` after the pending tests:

```go
t.Run("peer close while auth is running does not dispatch open", func(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("wkproto")
	proto.encodedBytes = []byte("connack-success")
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP}},
		consumed: 1,
	})

	authStarted := make(chan struct{})
	releaseAuth := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseAuth) }) }
	authenticator := gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
		close(authStarted)
		<-releaseAuth
		return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
	})

	srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, authenticator)
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	t.Cleanup(release)

	conn := transportFactory.MustOpen("listener-a", 1)
	transportFactory.MustData("listener-a", 1, []byte("c"))
	waitFor(t, func() bool {
		select {
		case <-authStarted:
			return true
		default:
			return false
		}
	})

	conn.EmitClose(nil)
	release()

	waitFor(t, func() bool { return connClosed(conn) })
	if got := handler.callOrder(); len(got) != 0 {
		t.Fatalf("handler call order = %v, want no open after peer close", got)
	}
	if got := len(conn.Writes()); got != 0 {
		t.Fatalf("writes = %d, want 0 after peer close before auth result", got)
	}
})
```

- [ ] **Step 5: Run the new behavior tests and verify they fail**

Run:

```bash
go test ./pkg/gateway/core -run 'TestServer/(connect authentication dispatches asynchronously|same decode batch frame after connect|second connect while auth is pending|peer close while auth is running)' -count=1
```

Expected: FAIL. The first test should time out because `EmitData` is blocked by synchronous auth. Pending-frame tests may fail because synchronous auth has no pending state.

- [ ] **Step 6: Commit the failing tests**

Stage only `server_test.go`:

```bash
git add pkg/gateway/core/server_test.go
git commit -m "test: cover async gateway auth behavior"
```

---

### Task 3: Async Auth Queue Internals

**Files:**
- Modify: `pkg/gateway/core/server.go`
- Create: `pkg/gateway/core/async_auth_test.go`

- [ ] **Step 1: Write queue helper tests**

Create `pkg/gateway/core/async_auth_test.go` with:

```go
package core

import (
	"testing"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAsyncAuthQueueSubmitRejectsWhenFull(t *testing.T) {
	queue := newAsyncAuthQueueWithCapacity(1)
	state := &sessionState{}

	if !queue.submit(asyncAuthTask{
		state:      state,
		replyToken: "r1",
		connect:    &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("first auth submit rejected")
	}
	if queue.submit(asyncAuthTask{
		state:      state,
		replyToken: "r2",
		connect:    &frame.ConnectPacket{UID: "u2"},
	}) {
		t.Fatal("second auth submit accepted when queue is full")
	}
	queue.close()
}

func TestAsyncAuthWorkerCountBounds(t *testing.T) {
	if got, want := adaptiveAsyncAuthWorkerCount(0), minAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with zero GOMAXPROCS = %d, want %d", got, want)
	}
	if got, want := adaptiveAsyncAuthWorkerCount(1), minAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with one GOMAXPROCS = %d, want %d", got, want)
	}
	if got, want := adaptiveAsyncAuthWorkerCount(128), maxAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with large GOMAXPROCS = %d, want %d", got, want)
	}
}

func TestCloseReasonForAsyncAuthQueueFull(t *testing.T) {
	if got := closeReasonForError(gatewaytypes.ErrAsyncAuthQueueFull, gatewaytypes.CloseReasonPolicyViolation); got != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonAsyncAuthQueueFull)
	}
}
```

- [ ] **Step 2: Run queue tests and verify they fail**

Run:

```bash
go test ./pkg/gateway/core -run 'TestAsyncAuth|TestCloseReasonForAsyncAuthQueueFull' -count=1
```

Expected: FAIL because queue types and helper constants do not exist.

- [ ] **Step 3: Add async auth constants and server field**

In `pkg/gateway/core/server.go`, add constants near the async dispatch constants:

```go
asyncAuthQueuePerWorker = 128
asyncAuthWorkersPerCPU  = 4
minAsyncAuthWorkers     = 16
maxAsyncAuthWorkers     = 64
asyncAuthMaxBufferedTasks = 8 * 1024
```

Add the failure class near the auth failure constants:

```go
authFailureQueueFull = "auth_queue_full"
```

Add a queue pointer to `Server` beside `asyncDispatch`:

```go
// asyncAuth bounds CONNECT authentication and activation off the transport event loop.
asyncAuth atomic.Pointer[asyncAuthQueue]
```

- [ ] **Step 4: Add task, queue, worker count, and clone helpers**

Add these helpers near the existing async dispatch queue implementation in `pkg/gateway/core/server.go`:

```go
type asyncAuthTask struct {
	state      *sessionState
	replyToken string
	connect    *frame.ConnectPacket
	enqueuedAt time.Time
	queue      *asyncAuthQueue
}

type asyncAuthQueue struct {
	mu       sync.RWMutex
	tasks    chan asyncAuthTask
	capacity int
	queued   atomic.Int64
	closed   bool
}

func newAsyncAuthQueue(workers int) *asyncAuthQueue {
	return newAsyncAuthQueueWithCapacity(asyncAuthQueueCapacity(workers))
}

func newAsyncAuthQueueWithCapacity(capacity int) *asyncAuthQueue {
	if capacity <= 0 {
		capacity = asyncAuthQueuePerWorker
	}
	return &asyncAuthQueue{
		tasks:    make(chan asyncAuthTask, capacity),
		capacity: capacity,
	}
}

func asyncAuthQueueCapacity(workers int) int {
	if workers <= 0 {
		workers = 1
	}
	capacity := workers * asyncAuthQueuePerWorker
	if capacity > asyncAuthMaxBufferedTasks {
		return asyncAuthMaxBufferedTasks
	}
	return capacity
}

func asyncAuthWorkerCount() int {
	return adaptiveAsyncAuthWorkerCount(runtime.GOMAXPROCS(0))
}

func adaptiveAsyncAuthWorkerCount(gomaxprocs int) int {
	if gomaxprocs <= 0 {
		return minAsyncAuthWorkers
	}
	workers := gomaxprocs * asyncAuthWorkersPerCPU
	if workers < minAsyncAuthWorkers {
		return minAsyncAuthWorkers
	}
	if workers > maxAsyncAuthWorkers {
		return maxAsyncAuthWorkers
	}
	return workers
}

func (q *asyncAuthQueue) submit(task asyncAuthTask) bool {
	if q == nil || task.state == nil || task.connect == nil {
		return false
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return false
	}
	task.connect = cloneAuthConnectPacket(task.connect)
	task.enqueuedAt = time.Now()
	task.queue = q
	select {
	case q.tasks <- task:
		q.queued.Add(1)
		return true
	default:
		return false
	}
}

func (q *asyncAuthQueue) consume(count int) {
	if q == nil || count <= 0 {
		return
	}
	remaining := q.queued.Add(-int64(count))
	if remaining >= 0 {
		return
	}
	q.queued.Add(-remaining)
}

func (q *asyncAuthQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.tasks)
}

func cloneAuthConnectPacket(connect *frame.ConnectPacket) *frame.ConnectPacket {
	if connect == nil {
		return nil
	}
	cloned := *connect
	return &cloned
}
```

- [ ] **Step 5: Map async auth overflow errors to close reasons**

In `closeReasonForError`, add the async auth case before the default:

```go
case errors.Is(err, gatewaytypes.ErrAsyncAuthQueueFull):
	return gatewaytypes.CloseReasonAsyncAuthQueueFull
```

- [ ] **Step 6: Run queue tests**

Run:

```bash
go test ./pkg/gateway/core -run 'TestAsyncAuth|TestCloseReasonForAsyncAuthQueueFull' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit queue internals**

Stage only files from this task:

```bash
git add pkg/gateway/core/server.go pkg/gateway/core/async_auth_test.go
git commit -m "feat: add async auth queue internals"
```

---

### Task 4: Auth Pending State And Worker Lifecycle

**Files:**
- Modify: `pkg/gateway/core/server.go`
- Test: `pkg/gateway/core/server_test.go`

- [ ] **Step 1: Add auth pending state helpers**

In `sessionState`, add the field near `authenticated`:

```go
authPending      bool
```

Add these methods near `setAuthenticated` and `isAuthenticated`:

```go
func (st *sessionState) beginAuth() bool {
	if st == nil {
		return false
	}

	st.metaMu.Lock()
	defer st.metaMu.Unlock()
	if st.authenticated || st.authPending {
		return false
	}
	st.authPending = true
	return true
}

func (st *sessionState) setAuthPending(pending bool) {
	if st == nil {
		return
	}

	st.metaMu.Lock()
	st.authPending = pending
	st.metaMu.Unlock()
}

func (st *sessionState) isAuthPending() bool {
	if st == nil {
		return false
	}

	st.metaMu.RLock()
	defer st.metaMu.RUnlock()
	return st.authPending
}
```

- [ ] **Step 2: Start and stop auth workers**

In `Start`, call the auth worker startup before `startAsyncDispatcher`:

```go
s.startIdleMonitor()
s.startAsyncAuthenticator()
s.startAsyncDispatcher()
return nil
```

In `Stop`, swap and close the auth queue beside async dispatch:

```go
asyncAuth := s.asyncAuth.Swap(nil)
asyncDispatch := s.asyncDispatch.Swap(nil)
```

Close it before waiting for workers:

```go
if asyncAuth != nil {
	asyncAuth.close()
}
if asyncDispatch != nil {
	asyncDispatch.close()
}
```

- [ ] **Step 3: Add worker lifecycle functions**

Add these functions near `startAsyncDispatcher`:

```go
// startAsyncAuthenticator starts a bounded worker pool for CONNECT authentication.
func (s *Server) startAsyncAuthenticator() {
	if s == nil || s.options.Authenticator == nil {
		return
	}

	workers := asyncAuthWorkerCount()
	queue := newAsyncAuthQueue(workers)

	s.mu.Lock()
	if s.stopped || s.asyncAuth.Load() != nil {
		s.mu.Unlock()
		return
	}
	s.asyncAuth.Store(queue)
	s.workerWG.Add(workers)
	s.mu.Unlock()

	for i := 0; i < workers; i++ {
		go s.runAsyncAuthWorker(queue)
	}
}

func (s *Server) runAsyncAuthWorker(queue *asyncAuthQueue) {
	defer s.workerWG.Done()
	if queue == nil {
		return
	}
	for task := range queue.tasks {
		queue.consume(1)
		s.runAuthTask(task)
	}
}

func (s *Server) asyncAuthenticator() *asyncAuthQueue {
	if s == nil {
		return nil
	}
	return s.asyncAuth.Load()
}
```

- [ ] **Step 4: Replace synchronous auth gating with enqueue gating**

Rewrite the top-level `handleAuthFrame` so it only gates protocol state and enqueues CONNECT:

```go
func (s *Server) handleAuthFrame(state *sessionState, replyToken string, f frame.Frame) (bool, error) {
	if state == nil || !state.requiresAuth() {
		return false, nil
	}
	if state.isAuthPending() {
		s.observeAuth(state, authStatusFail, authFailureProtocolViolation, 0)
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return true, nil
	}
	if state.isAuthenticated() {
		return false, nil
	}

	start := time.Now()
	connect, ok := f.(*frame.ConnectPacket)
	if !ok {
		s.observeAuth(state, authStatusFail, authFailureProtocolViolation, time.Since(start))
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return true, nil
	}
	if !state.beginAuth() {
		s.observeAuth(state, authStatusFail, authFailureProtocolViolation, time.Since(start))
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return true, nil
	}

	queue := s.asyncAuthenticator()
	if queue != nil && queue.submit(asyncAuthTask{state: state, replyToken: replyToken, connect: connect}) {
		return true, nil
	}

	state.setAuthPending(false)
	s.handleAuthQueueFull(state, replyToken, start)
	return true, nil
}
```

- [ ] **Step 5: Extract the existing auth body into `runAuthTask`**

Move the current body from `handleAuthFrame` into a new `runAuthTask`. Preserve the existing status/failure handling, with `start` coming from `task.enqueuedAt`:

```go
func (s *Server) runAuthTask(task asyncAuthTask) {
	state := task.state
	connect := task.connect
	if state == nil || connect == nil || state.isClosed() {
		if state != nil {
			state.setAuthPending(false)
		}
		return
	}

	start := task.enqueuedAt
	if start.IsZero() {
		start = time.Now()
	}
	status := authStatusFail
	failure := authFailureUnknown
	observe := true
	defer func() {
		state.setAuthPending(false)
		if observe {
			s.observeAuth(state, status, failure, time.Since(start))
		}
	}()

	ctx := s.dispatcher.context(state, task.replyToken, state.closeReason(), nil)
	result, err := s.options.Authenticator.Authenticate(&ctx, connect)
	if state.isClosed() {
		observe = false
		return
	}
	if err != nil {
		failure = authFailureAuthenticatorError
		s.logAuthFailure(state, connect, failure, err)
		if writeErr := s.writeImmediateFrame(state, task.replyToken, &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError}); writeErr != nil {
			state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPolicyViolation), writeErr)
			return
		}
		state.close(gatewaytypes.CloseReasonPolicyViolation, err)
		return
	}
	if result == nil {
		result = &gatewaytypes.AuthResult{}
	}

	connack := authConnackFromResult(result)
	if connack.ReasonCode != frame.ReasonSuccess {
		failure = authFailureForConnack(connack.ReasonCode)
		if writeErr := s.writeImmediateFrame(state, task.replyToken, connack); writeErr != nil {
			state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPeerClosed), writeErr)
			return
		}
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return
	}

	if result.SessionValues == nil {
		result.SessionValues = make(map[string]any, 1)
	}
	result.SessionValues[gatewaytypes.SessionValueDeviceID] = connect.DeviceID
	for key, value := range result.SessionValues {
		state.session.SetValue(key, value)
	}

	ctx = s.dispatcher.context(state, task.replyToken, state.closeReason(), nil)
	activated := false
	if activator, ok := s.options.Handler.(gatewaytypes.SessionActivator); ok {
		override, err := activator.OnSessionActivate(&ctx)
		if state.isClosed() {
			observe = false
			return
		}
		if err != nil {
			failure = authFailureForActivationError(err)
			s.logAuthFailure(state, connect, failure, err)
			if writeErr := s.writeImmediateFrame(state, task.replyToken, &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError}); writeErr != nil {
				state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPolicyViolation), writeErr)
				return
			}
			state.close(gatewaytypes.CloseReasonPolicyViolation, err)
			return
		}
		activated = true
		if override != nil {
			connack = override
		}
	}
	normalizeAuthConnackReason(connack)
	if connack.ReasonCode != frame.ReasonSuccess {
		failure = authFailureForConnack(connack.ReasonCode)
	}

	if state.isClosed() {
		observe = false
		return
	}
	if writeErr := s.writeImmediateFrame(state, task.replyToken, connack); writeErr != nil {
		if connack.ReasonCode == frame.ReasonSuccess {
			failure = authFailureConnackWriteError
		}
		if activated && connack.ReasonCode == frame.ReasonSuccess {
			if rollbacker, ok := s.options.Handler.(gatewaytypes.SessionActivationRollbacker); ok {
				rollbacker.OnSessionActivateRollback(ctx, writeErr)
			}
		}
		state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonPeerClosed), writeErr)
		return
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		state.close(gatewaytypes.CloseReasonPolicyViolation, nil)
		return
	}

	state.setAuthenticated(true)
	status = authStatusOK
	failure = authFailureNone
	if !state.openWasDispatched() {
		if err := s.dispatchSessionOpen(state); err != nil {
			s.handleHandlerError(state, err)
		}
	}
}
```

- [ ] **Step 6: Add queue-full rejection helper**

Add this helper near `runAuthTask`:

```go
func (s *Server) handleAuthQueueFull(state *sessionState, replyToken string, start time.Time) {
	if start.IsZero() {
		start = time.Now()
	}
	if state == nil || state.isClosed() {
		return
	}
	if writeErr := s.writeImmediateFrame(state, replyToken, &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError}); writeErr != nil {
		s.observeAuth(state, authStatusFail, authFailureQueueFull, time.Since(start))
		state.close(closeReasonForError(writeErr, gatewaytypes.CloseReasonAsyncAuthQueueFull), writeErr)
		return
	}
	s.observeAuth(state, authStatusFail, authFailureQueueFull, time.Since(start))
	state.close(gatewaytypes.CloseReasonAsyncAuthQueueFull, gatewaytypes.ErrAsyncAuthQueueFull)
}
```

- [ ] **Step 7: Run async behavior tests**

Run:

```bash
go test ./pkg/gateway/core -run 'TestServer/(connect authentication dispatches asynchronously|same decode batch frame after connect|second connect while auth is pending|peer close while auth is running)' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run existing auth regression tests**

Run:

```bash
go test ./pkg/gateway/core -run 'TestServer/(failed wkproto authentication|authentication connack encode receives reply token|wkproto authenticator error|successful wkproto activation|wkproto activation failure|wkproto activation normalizes|successful wkproto authentication replies)' -count=1
```

Expected: PASS. If a test reads `handler.callOrder()` immediately after one write, change its wait condition to include the expected call order because `OnSessionOpen` is now asynchronous.

- [ ] **Step 9: Commit auth worker behavior**

Stage only files from this task:

```bash
git add pkg/gateway/core/server.go pkg/gateway/core/server_test.go
git commit -m "feat: authenticate gateway connects asynchronously"
```

---

### Task 5: Queue Full And Lifecycle Tests

**Files:**
- Modify: `pkg/gateway/core/async_auth_test.go`
- Modify: `pkg/gateway/core/server.go`

- [ ] **Step 1: Add a queue-full no-sync-fallback test**

Append this test to `pkg/gateway/core/async_auth_test.go`:

```go
func TestServerAsyncAuthRejectsWhenQueueFull(t *testing.T) {
	var authCalls atomic.Uint64
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
				authCalls.Add(1)
				return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler: handler,
		},
		dispatcher: newDispatcher(handler),
	}
	queue := newAsyncAuthQueueWithCapacity(1)
	srv.asyncAuth.Store(queue)
	queue.tasks <- asyncAuthTask{state: &sessionState{}, connect: &frame.ConnectPacket{UID: "queued"}}
	queue.queued.Add(1)

	state := &sessionState{
		server:   srv,
		closedCh: make(chan struct{}),
	}
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.setAuthRequired(true)

	srv.handleAuthFrame(state, "", &frame.ConnectPacket{UID: "u1"})

	if got := authCalls.Load(); got != 0 {
		t.Fatalf("auth calls = %d, want 0 when queue is full", got)
	}
	if !state.isClosed() {
		t.Fatal("state was not closed after auth queue overflow")
	}
	if got := state.closeReason(); got != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonAsyncAuthQueueFull)
	}
	queue.close()
}
```

Add required imports and a local handler to `async_auth_test.go`:

```go
import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type asyncAuthNoopHandler struct{}

func (asyncAuthNoopHandler) OnListenerError(string, error) {}
func (asyncAuthNoopHandler) OnSessionOpen(gatewaytypes.Context) error { return nil }
func (asyncAuthNoopHandler) OnFrame(gatewaytypes.Context, frame.Frame) error { return nil }
func (asyncAuthNoopHandler) OnSessionClose(gatewaytypes.Context) error { return nil }
func (asyncAuthNoopHandler) OnSessionError(gatewaytypes.Context, error) {}
```

- [ ] **Step 2: Run the queue-full test and verify any setup failures**

Run:

```bash
go test ./pkg/gateway/core -run TestServerAsyncAuthRejectsWhenQueueFull -count=1
```

Expected: PASS after imports and helper setup compile.

- [ ] **Step 3: Add worker shutdown test**

Append this test to `pkg/gateway/core/async_auth_test.go`:

```go
func TestAsyncAuthQueueCloseStopsWorker(t *testing.T) {
	srv := &Server{}
	queue := newAsyncAuthQueueWithCapacity(1)
	done := make(chan struct{})

	srv.workerWG.Add(1)
	go func() {
		srv.runAsyncAuthWorker(queue)
		close(done)
	}()

	queue.close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("auth worker did not stop after queue close")
	}
	srv.workerWG.Wait()
}
```

The `time` import is already listed in Step 1.

- [ ] **Step 4: Run lifecycle tests**

Run:

```bash
go test ./pkg/gateway/core -run 'TestServerAsyncAuthRejectsWhenQueueFull|TestAsyncAuthQueueCloseStopsWorker' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit lifecycle and overflow tests**

Stage only files from this task:

```bash
git add pkg/gateway/core/async_auth_test.go pkg/gateway/core/server.go
git commit -m "test: cover async auth queue overflow"
```

---

### Task 6: Documentation And Package Verification

**Files:**
- Modify: `pkg/gateway/FLOW.md`
- Test: `pkg/gateway/...`

- [ ] **Step 1: Update `pkg/gateway/FLOW.md` type summaries**

In section 5, change the `core.Server` and queue rows to mention async auth:

```text
| `core.Server` | `core/server.go` | 网关核心状态机，管理 listener、session、decode/dispatch/write、idle monitor、async CONNECT auth、async frame worker pool |
| `asyncAuthQueue` | `core/server.go` | CONNECT 认证与激活的有界异步队列，满队列时写 retryable CONNACK 并按 auth queue overflow 关闭 |
| `asyncDispatchQueue` | `core/server.go` | 认证后业务 frame 异步分发队列，容量有界，满队列时关闭当前 session 形成背压 |
```

- [ ] **Step 2: Update startup flow**

In section 6.1, replace startup steps 7 and 8 with:

```text
  ⑦ 启动共享 idle monitor
  ⑧ 若配置 Authenticator，启动有界 async CONNECT auth worker pool
  ⑨ 启动有界 async frame worker pool
```

- [ ] **Step 3: Update inbound dispatch flow**

In section 6.3, replace the auth dispatch bullets with:

```text
  ⑥ handleAuthFrame 优先处理 CONNECT gating:
     - 未认证且未 pending auth 时只接受 ConnectPacket，并把认证/激活任务提交到 async auth queue
     - auth pending 期间收到任何后续 frame，包括同批 decode 出来的业务 frame 或重复 CONNECT，都按 policy_violation 关闭
     - async auth queue 满时尽量写出 ReasonSystemError CONNACK，再按 async_auth_queue_full 关闭
  ⑦ 认证完成后的业务 frame:
```

- [ ] **Step 4: Update CONNECT auth section**

In section 6.4, rewrite the intro and first steps:

```text
入口: `core.Server.handleAuthFrame` → `asyncAuthQueue` worker

CONNECT:
  ① 未认证时只接受首个 ConnectPacket；其他 frame 或 auth pending 期间的重复 frame 按 policy_violation 关闭
  ② 首个 ConnectPacket 只在 transport callback 路径完成 clone 和入队；Authenticator.Authenticate 与 SessionActivator.OnSessionActivate 在 async auth worker 中执行
  ③ Authenticator.Authenticate:
```

Keep the existing authenticate, activation, CONNACK, rollback, and `OnSessionOpen` semantics below those new lines.

- [ ] **Step 5: Run gateway package tests**

Run:

```bash
go test ./pkg/gateway/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Check worktree and commit docs**

Run:

```bash
git status --short
```

Expected: only intended files for this feature should be modified, plus any pre-existing unrelated dirty files that were already present before implementation.

Commit the FLOW update:

```bash
git add pkg/gateway/FLOW.md
git commit -m "docs: document async gateway auth"
```

---

## Final Verification

- [ ] **Step 1: Run focused gateway tests**

Run:

```bash
go test ./pkg/gateway/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader related tests**

Run:

```bash
go test ./cmd/wukongim ./cmd/wukongimv2 ./internal/app/... ./internalv2/app/... -count=1
```

Expected: PASS. If this command takes too long or exposes unrelated pre-existing failures, record the exact package and failure line in the final report.

- [ ] **Step 3: Inspect final diff**

Run:

```bash
git diff --stat HEAD~6..HEAD
git status --short
```

Expected: the committed diff contains only async auth design/implementation/test/doc work. The working tree may still show pre-existing uncommitted files that were present before this plan started; do not revert them.
