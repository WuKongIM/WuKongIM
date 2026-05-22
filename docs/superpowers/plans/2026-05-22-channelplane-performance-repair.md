# Channelplane Performance Repair Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the main throughput and memory risks found in `internal/runtime/channelplane`: remote RPCs occupying global effect workers, canceled requests consuming per-channel queue slots, unbounded idle channel state, peer flush goroutine buildup, and hot-path allocation overhead.

**Architecture:** Keep the public `Plane.AppendBatch` API synchronous, but make remote peer appends internally completion-driven so effect workers only run route/local blocking work. Reactors remain the single owner of channel cell state; pending cleanup, idle eviction, and cell lifecycle are reactor-local. Peer lanes use fixed RPC workers and bounded RPC queues instead of creating a goroutine per flush.

**Tech Stack:** Go 1.23, standard `context`/`sync`/`time`, existing `pkg/channel` DTOs, existing `internal/runtime/channelplane` tests with `testify/require`, `go test`/`go test -bench` for verification.

---

## Review Inputs

- Existing package flow: `internal/runtime/channelplane/FLOW.md`
- Main findings:
  - Remote append waits inside `effectExecutor` via `channel_cell.go -> PeerReactor.AppendRemoteBatch`.
  - Canceled queued commands remain in `channelCell.pending` until they reach the head.
  - Reactor `cells` and cached `ChannelRoute` state have no idle eviction.
  - Peer lane creates one goroutine per flush while waiting on `rpcSem`.
  - Hot path allocates command/future/key/task objects per append.

## File Structure

- Modify: `internal/runtime/channelplane/options.go` - internal runtime defaults for idle cell eviction and peer RPC queue limits.
- Modify: `internal/runtime/channelplane/plane.go` - wire new options and keep lifecycle behavior unchanged.
- Modify: `internal/runtime/channelplane/reactor.go` - add channel ID keyed cells, optional idle sweep, completion handling, and cleanup hooks.
- Modify: `internal/runtime/channelplane/channel_cell.go` - compact canceled pending commands, start remote appends without occupying effect workers, track idle state.
- Modify: `internal/runtime/channelplane/peer_reactor.go` - add async append API and replace per-flush goroutine waiting with bounded RPC workers/queue.
- Modify: `internal/runtime/channelplane/scheduler.go` - switch scheduler key from encoded `channel.ChannelKey` to `channel.ChannelID` if Task 5 is implemented.
- Modify: `internal/runtime/channelplane/event.go` - carry `channel.ChannelID` or explicit internal key in completions after Task 5.
- Modify: `internal/runtime/channelplane/FLOW.md` - document async remote completion, idle eviction, cancellation compaction, and peer RPC worker model.
- Test: `internal/runtime/channelplane/plane_test.go` - regression tests for remote worker isolation, canceled pending compaction, idle eviction, and key allocation behavior.
- Test: `internal/runtime/channelplane/peer_reactor_test.go` - regression tests for async callback, bounded RPC workers, queued RPC batch backpressure, and sync wrapper compatibility.
- Test: `internal/runtime/channelplane/scheduler_test.go` - update key type if scheduler changes in Task 5.

## Task 1: Baseline And Guard Tests

This task records the current performance bugs as failing tests before implementation. Do not change production code in this task.

- [ ] **Step 1: Add remote-effect-worker isolation test**

Add this test to `internal/runtime/channelplane/plane_test.go`. It should fail before Task 2 because the blocked remote append occupies the only effect worker and prevents the local append from completing.

```go
func TestChannelPlaneRemoteAppendDoesNotOccupyEffectWorker(t *testing.T) {
	peer := newBlockingPeerClient()
	resolver := routeByIDResolver{
		routes: map[string]ChannelRoute{
			"remote-worker-blocked": remoteRoute("remote-worker-blocked", 2),
			"local-worker-free":     localRoute("local-worker-free"),
		},
	}
	owner := newSequenceOwner()
	p, err := New(Options{
		ReactorCount:      2,
		EffectWorkerCount: 1,
		EffectQueueSize:   1,
		LocalNode:         1,
		Resolver:          resolver,
		LocalOwner:        owner,
		PeerClient:        peer,
		PeerBatchMaxWait:  time.Millisecond,
	})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	remoteDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(ctx, appendReq("remote-worker-blocked", 1))
		remoteDone <- err
	}()
	peer.waitCall(t)

	localCtx, localCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer localCancel()
	res, err := p.AppendBatch(localCtx, appendReq("local-worker-free", 1))
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Items[0].MessageSeq)

	peer.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{
		Status: RemoteAppendStatusOK,
		Result: batchResult(1),
	}}})
	require.NoError(t, <-remoteDone)
}
```

- [ ] **Step 2: Add canceled-pending queue-slot test**

Add this test to `plane_test.go`. It should fail before Task 3 because the canceled queued command is still counted by `MaxPendingPerChannel`.

```go
func TestChannelCellDoesNotCountCanceledPendingAgainstLimit(t *testing.T) {
	owner := newBlockingOwner()
	p, err := New(Options{
		ReactorCount:          1,
		LocalNode:             1,
		Resolver:              staticResolver{route: localRoute("cancel-slot")},
		LocalOwner:            owner,
		MaxPendingPerChannel:  1,
	})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Second)
	defer firstCancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(firstCtx, appendReq("cancel-slot", 1))
		firstDone <- err
	}()
	owner.waitStarted(t, 1)

	queuedCtx, queuedCancel := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(queuedCtx, appendReq("cancel-slot", 2))
		queuedDone <- err
	}()
	require.Eventually(t, func() bool {
		cell := p.reactors[0].cellForTest(appendReq("cancel-slot", 1).ChannelID)
		return cell != nil && len(cell.pending) == 1
	}, time.Second, 10*time.Millisecond)
	queuedCancel()
	require.ErrorIs(t, <-queuedDone, context.Canceled)

	thirdCtx, thirdCancel := context.WithTimeout(context.Background(), time.Second)
	defer thirdCancel()
	thirdDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(thirdCtx, appendReq("cancel-slot", 3))
		thirdDone <- err
	}()

	owner.releaseOne()
	require.NoError(t, <-firstDone)
	require.NoError(t, <-thirdDone)
}
```

- [ ] **Step 3: Add peer RPC worker bound test**

Extend `internal/runtime/channelplane/peer_reactor_test.go` with a test that starts one blocked RPC, queues one additional batch, and proves a third batch is backpressured or queued within the configured bound without creating extra RPC calls.

```go
func TestPeerReactorBoundsQueuedRPCBatchesWhenWorkerBlocked(t *testing.T) {
	client := newBlockingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    time.Millisecond,
		MaxBatchRecords: 1,
		MaxPending:      3,
		MaxInflightRPC:  1,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done1 := startPeerAppendForTest(t, peer, ctx, "rpc-worker-1")
	client.waitCall(t)

	done2 := startPeerAppendForTest(t, peer, ctx, "rpc-worker-2")
	require.Never(t, func() bool { return client.callCount() > 1 }, 30*time.Millisecond, 5*time.Millisecond)

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	_, err := peer.AppendRemoteBatch(shortCtx, 2, appendReq("rpc-worker-3", 1), remoteRoute("rpc-worker-3", 2))
	require.Error(t, err)

	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{
		Status: RemoteAppendStatusOK,
		Result: batchResult(1),
	}}})
	require.NoError(t, <-done1)
	client.waitCall(t)
	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{
		Status: RemoteAppendStatusOK,
		Result: batchResult(2),
	}}})
	require.NoError(t, <-done2)
}
```

- [ ] **Step 4: Add idle cell eviction test**

Add a direct reactor test in `plane_test.go` or a new `reactor_test.go` that advances a fake clock and calls a sweep helper. This should fail before Task 4.

```go
func TestReactorEvictsIdleChannelCells(t *testing.T) {
	now := time.Unix(100, 0)
	p, err := New(Options{
		ReactorCount:     1,
		LocalNode:        1,
		Resolver:         staticResolver{route: localRoute("idle-cell")},
		LocalOwner:       noopOwner{},
		CellIdleTTL:      time.Minute,
		CellSweepEvery:   time.Second,
		Now:              func() time.Time { return now },
	})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	_, err = p.AppendBatch(context.Background(), appendReq("idle-cell", 1))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return p.reactors[0].cellCountForTest() == 1
	}, time.Second, 10*time.Millisecond)

	now = now.Add(2 * time.Minute)
	p.reactors[0].sweepIdleCellsForTest(now)
	require.Zero(t, p.reactors[0].cellCountForTest())
}
```

- [ ] **Step 5: Run guard tests and confirm failures**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run 'TestChannelPlaneRemoteAppendDoesNotOccupyEffectWorker|TestChannelCellDoesNotCountCanceledPendingAgainstLimit|TestPeerReactorBoundsQueuedRPCBatchesWhenWorkerBlocked|TestReactorEvictsIdleChannelCells' \
  -count=1
```

Expected: FAIL. Keep the failure output in the task notes before implementing fixes.

## Task 2: Make Remote Append Completion-Driven

Remote append must not occupy `effectExecutor` workers while waiting on peer RPC results.

- [ ] **Step 1: Add async peer append API**

Modify `internal/runtime/channelplane/peer_reactor.go`:

```go
type peerAppendCallback func(channel.AppendBatchResult, error)

func (p *PeerReactor) AppendRemoteBatchAsync(
	ctx context.Context,
	nodeID channel.NodeID,
	req channel.AppendBatchRequest,
	route ChannelRoute,
	onComplete peerAppendCallback,
) error {
	if onComplete == nil {
		return channel.ErrInvalidConfig
	}
	// Same validation, lane lookup, reserve, and submit path as AppendRemoteBatch.
	// The task stores onComplete instead of forcing the caller to wait.
	return nil
}
```

Keep `AppendRemoteBatch` as a synchronous compatibility wrapper used by current tests and callers:

```go
func (p *PeerReactor) AppendRemoteBatch(ctx context.Context, nodeID channel.NodeID, req channel.AppendBatchRequest, route ChannelRoute) (channel.AppendBatchResult, error) {
	done := make(chan peerAppendResult, 1)
	err := p.AppendRemoteBatchAsync(ctx, nodeID, req, route, func(res channel.AppendBatchResult, err error) {
		done <- peerAppendResult{result: res, err: err}
	})
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	select {
	case result := <-done:
		return result.result, result.err
	case <-ctx.Done():
		return channel.AppendBatchResult{}, ctx.Err()
	}
}
```

- [ ] **Step 2: Update peer task completion**

Change `peerAppendTask` in `peer_reactor.go` from a mandatory result channel to a callback:

```go
type peerAppendTask struct {
	ctx        context.Context
	envelope   AppendBatchEnvelope
	onComplete peerAppendCallback
	once       sync.Once
}
```

Update `complete`:

```go
func (l *peerLane) complete(task *peerAppendTask, result channel.AppendBatchResult, err error) {
	task.once.Do(func() {
		l.release()
		task.onComplete(result, err)
	})
}
```

- [ ] **Step 3: Split local and remote append start paths**

Modify `internal/runtime/channelplane/channel_cell.go` so `startAppend` only uses `effectExecutor` for local append. Remote append should enqueue directly and post completion from the peer callback.

```go
func (c *channelCell) startAppend(cmd *appendCommand, route ChannelRoute) {
	if route.IsLocal(c.reactor.plane.opts.LocalNode) {
		c.startLocalAppend(cmd, route)
		return
	}
	c.startRemoteAppend(cmd, route)
}
```

Remote path:

```go
func (c *channelCell) startRemoteAppend(cmd *appendCommand, route ChannelRoute) {
	if c.reactor.plane.peer == nil {
		c.handleAppendComplete(effectCompletion{key: c.key, cmd: cmd, route: route, err: ErrNoRemoteAppender})
		return
	}
	req := route.applyTo(cmd.req)
	err := c.reactor.plane.peer.AppendRemoteBatchAsync(effectContext(cmd), route.Leader, req, route, func(res channel.AppendBatchResult, err error) {
		c.reactor.post(reactorEvent{
			kind: reactorEventAppendComplete,
			completion: effectCompletion{key: c.key, cmd: cmd, route: route, res: res, err: err},
		})
	})
	if err != nil {
		c.handleAppendComplete(effectCompletion{key: c.key, cmd: cmd, route: route, err: err})
	}
}
```

- [ ] **Step 4: Verify remote worker isolation**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run 'TestChannelPlaneRemoteAppendDoesNotOccupyEffectWorker|TestChannelPlaneUsesPeerReactorForRemoteLeader|TestPeerReactor' \
  -count=1
```

Expected: PASS.

## Task 3: Compact Canceled Pending Commands

Canceled callers should not consume `MaxPendingPerChannel` capacity after their context is done.

- [ ] **Step 1: Reject already-canceled commands before counting them**

Modify `channelCell.enqueue`:

```go
func (c *channelCell) enqueue(cmd *appendCommand) bool {
	if err := effectContext(cmd).Err(); err != nil {
		c.complete(cmd, channel.AppendBatchResult{}, err, ChannelRoute{})
		return false
	}
	c.compactCanceledPending()
	if len(c.pending) >= c.reactor.plane.opts.MaxPendingPerChannel {
		c.complete(cmd, channel.AppendBatchResult{}, ErrOverloaded, ChannelRoute{})
		return false
	}
	c.pending = append(c.pending, cmd)
	return true
}
```

- [ ] **Step 2: Add pending compaction helper**

Add this helper to `channel_cell.go`:

```go
func (c *channelCell) compactCanceledPending() {
	if len(c.pending) == 0 {
		return
	}
	write := 0
	for _, cmd := range c.pending {
		if cmd == nil {
			continue
		}
		if err := effectContext(cmd).Err(); err != nil {
			c.complete(cmd, channel.AppendBatchResult{}, err, ChannelRoute{})
			continue
		}
		c.pending[write] = cmd
		write++
	}
	for i := write; i < len(c.pending); i++ {
		c.pending[i] = nil
	}
	c.pending = c.pending[:write]
}
```

- [ ] **Step 3: Compact before scheduling follow-up work**

Modify `scheduleIfPending`:

```go
func (c *channelCell) scheduleIfPending() {
	c.compactCanceledPending()
	if len(c.pending) > 0 {
		c.reactor.markReady(c.key)
	}
}
```

- [ ] **Step 4: Verify cancellation behavior**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run 'TestChannelCellDoesNotCountCanceledPendingAgainstLimit|TestChannelPlaneCancelsQueuedFutureOnContextDone|TestChannelPlaneObserverCompletesOverloadedAcceptedCommand' \
  -count=1
```

Expected: PASS.

## Task 4: Add Idle Channel Cell Eviction

The reactor should not retain channel cells and cached routes forever after a channel becomes idle.

- [ ] **Step 1: Add internal idle eviction options**

Modify `internal/runtime/channelplane/options.go`:

```go
const (
	defaultCellIdleTTL    = 10 * time.Minute
	defaultCellSweepEvery = time.Minute
)

type Options struct {
	// CellIdleTTL bounds how long an idle channel cell keeps cached routing state.
	CellIdleTTL time.Duration
	// CellSweepEvery controls how often reactors scan for idle channel cells.
	CellSweepEvery time.Duration
}
```

Set defaults in `setDefaults`. Keep these internal runtime options only; do not add user config keys in `cmd/wukongim/config.go` unless product tuning is explicitly requested later.

- [ ] **Step 2: Track channel cell activity**

Modify `channelCell`:

```go
type channelCell struct {
	reactor    *reactor
	key        channel.ChannelID // or current key until Task 5
	route      *ChannelRoute
	pending    []*appendCommand
	inflight   *appendCommand
	lastActive time.Time
}

func (c *channelCell) touch() {
	c.lastActive = c.reactor.plane.opts.Now()
}

func (c *channelCell) idleExpired(now time.Time, ttl time.Duration) bool {
	return c.inflight == nil && len(c.pending) == 0 && ttl > 0 && !c.lastActive.IsZero() && now.Sub(c.lastActive) >= ttl
}
```

Call `touch` on enqueue, start, resolve completion, append completion, and route refresh retry.

- [ ] **Step 3: Add reactor sweep**

Modify `reactor.run` to create a ticker when `CellIdleTTL > 0` and `CellSweepEvery > 0`:

```go
var sweepC <-chan time.Time
var sweepTicker *time.Ticker
if r.plane.opts.CellIdleTTL > 0 && r.plane.opts.CellSweepEvery > 0 {
	sweepTicker = time.NewTicker(r.plane.opts.CellSweepEvery)
	sweepC = sweepTicker.C
}
defer func() {
	if sweepTicker != nil {
		sweepTicker.Stop()
	}
	close(r.done)
}()
```

Add select case:

```go
case now := <-sweepC:
	r.sweepIdleCells(now)
```

Add helper:

```go
func (r *reactor) sweepIdleCells(now time.Time) {
	ttl := r.plane.opts.CellIdleTTL
	for key, cell := range r.cells {
		cell.compactCanceledPending()
		if cell.idleExpired(now, ttl) {
			cell.failAll(ErrClosed) // no-op for empty idle cell, clears references defensively
			delete(r.cells, key)
		}
	}
}
```

Do not evict cells with pending or inflight work.

- [ ] **Step 4: Add test-only helpers**

Add small unexported helpers in `reactor.go` guarded by normal test usage:

```go
func (r *reactor) cellCountForTest() int { return len(r.cells) }
func (r *reactor) sweepIdleCellsForTest(now time.Time) { r.sweepIdleCells(now) }
```

If Task 5 changes key type, also add:

```go
func (r *reactor) cellForTest(id channel.ChannelID) *channelCell { return r.cells[id] }
```

- [ ] **Step 5: Verify idle eviction**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run 'TestReactorEvictsIdleChannelCells|TestChannelCellSerializesSameChannelAppends|TestChannelPlaneCompletesFuturesInRequestOrder' \
  -count=1
```

Expected: PASS.

## Task 5: Bound Peer RPC Workers And Flush Queue

Replace one goroutine per flushed batch with fixed per-lane RPC workers and a bounded queue.

- [ ] **Step 1: Add lane RPC queue fields**

Modify `peerLane` in `peer_reactor.go`:

```go
type peerLane struct {
	parent   *PeerReactor
	key      peerLaneKey
	inbox    chan *peerAppendTask
	rpcQueue chan []*peerAppendTask
	stopc    chan struct{}
	done     chan struct{}
	rpcWG    sync.WaitGroup

	submitMu sync.Mutex
	stopped  bool
	pending  atomic.Int64
}
```

In `newPeerLane`, size `rpcQueue` with `parent.opts.MaxInflightRPC`. This allows each worker to run one batch and one additional batch per worker to wait without unbounded goroutines.

- [ ] **Step 2: Start fixed RPC workers**

Modify `peerLane.start`:

```go
func (l *peerLane) start() {
	l.rpcWG.Add(l.parent.opts.MaxInflightRPC)
	for i := 0; i < l.parent.opts.MaxInflightRPC; i++ {
		go l.runRPCWorker()
	}
	go l.run()
}
```

Add worker:

```go
func (l *peerLane) runRPCWorker() {
	defer l.rpcWG.Done()
	for {
		select {
		case tasks := <-l.rpcQueue:
			l.flush(tasks)
		case <-l.stopc:
			return
		}
	}
}
```

- [ ] **Step 3: Queue flushes without spawning goroutines**

Replace `go l.flushWhenRPCSlotAvailable(tasks)` with:

```go
select {
case l.rpcQueue <- tasks:
case <-l.stopc:
	for _, task := range tasks {
		l.complete(task, channel.AppendBatchResult{}, ErrClosed)
	}
default:
	for _, task := range tasks {
		l.complete(task, channel.AppendBatchResult{}, ErrPeerBackpressured)
	}
}
```

Remove `rpcSem` and `flushWhenRPCSlotAvailable`.

- [ ] **Step 4: Stop workers cleanly**

Modify `peerLane.run` stop path so it drains `inbox`, completes current `batch`, and returns. Modify `PeerReactor.Stop` to wait for both `lane.done` and `lane.rpcWG` before returning. Ensure queued RPC batches are completed with `ErrClosed` if workers exit before flushing them.

- [ ] **Step 5: Verify peer lane behavior**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run 'TestPeerReactorBatches|TestPeerReactorReturnsTypedBackpressure|TestPeerReactorReleasesCanceledQueuedTaskWhileRPCBlocked|TestPeerReactorBoundsBlockedRPCWithTimeout|TestPeerReactorDoesNotStartUnboundedRPCFlushes|TestPeerReactorBoundsQueuedRPCBatchesWhenWorkerBlocked' \
  -count=1
```

Expected: PASS.

## Task 6: Reduce Hot-Path Key Allocation

This task is optional if Task 1-5 already solve the measured performance problem. Only do it after benchmarks show GC/allocation pressure in channelplane.

- [ ] **Step 1: Add microbenchmarks**

Create or extend `internal/runtime/channelplane/plane_benchmark_test.go`:

```go
func BenchmarkChannelPlaneLocalAppendHotPath(b *testing.B) {
	p, err := New(Options{
		ReactorCount: 1,
		LocalNode:    1,
		Resolver:     staticResolver{route: localRoute("bench-local")},
		LocalOwner:   noopOwner{},
	})
	require.NoError(b, err)
	require.NoError(b, p.Start())
	defer stopPlane(b, p)

	ctx := context.Background()
	req := appendReq("bench-local", 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.AppendBatch(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}
```

- [ ] **Step 2: Capture baseline allocations**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run '^$' -bench 'BenchmarkChannelPlaneLocalAppendHotPath' -benchmem -count=5
```

Expected: Benchmark output recorded in task notes.

- [ ] **Step 3: Switch reactor/scheduler internal key to `channel.ChannelID`**

Modify `reactor.cells`, `scheduler`, `effectCompletion.key`, and `channelCell.key` to use `channel.ChannelID` directly. This removes the per-append `channelhandler.KeyFromChannelID` allocation in `reactor.handle`.

Key changes:

```go
type scheduler struct {
	ready  []channel.ChannelID
	queued map[channel.ChannelID]struct{}
}

type reactor struct {
	cells map[channel.ChannelID]*channelCell
}
```

Then change append handling:

```go
case reactorEventAppend:
	key := event.cmd.req.ChannelID
	cell := r.cell(key)
	if cell.enqueue(event.cmd) {
		r.markReady(key)
	}
```

- [ ] **Step 4: Re-run scheduler and plane tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane -run 'TestScheduler|TestChannelPlane|TestChannelCell|TestRouteResolver' -count=1
```

Expected: PASS.

- [ ] **Step 5: Compare benchmark allocations**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane \
  -run '^$' -bench 'BenchmarkChannelPlaneLocalAppendHotPath' -benchmem -count=5
```

Expected: `allocs/op` and `B/op` are lower than Task 6 Step 2. If not lower, revert this task and keep the simpler key model.

## Task 7: Documentation And Verification

- [ ] **Step 1: Update package flow**

Update `internal/runtime/channelplane/FLOW.md`:

- Remote append effects are completion-driven and do not occupy global effect workers.
- Canceled pending commands are compacted before queue-capacity checks and rescheduling.
- Idle channel cells are swept by reactors after `CellIdleTTL`.
- Peer lanes use bounded RPC workers and bounded queued RPC batches.
- If Task 6 is implemented, reactor-local scheduling keys are `channel.ChannelID`, not encoded runtime channel keys.

- [ ] **Step 2: Run focused package tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelplane -count=1
```

Expected: PASS.

- [ ] **Step 3: Run race tests**

Run:

```bash
GOWORK=off go test -race ./internal/runtime/channelplane -count=1
```

Expected: PASS.

- [ ] **Step 4: Run affected app compile/tests**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/access/node -run 'TestConfig|TestAppLifecycle|TestBuild|TestChannelPlane' -count=1
```

Expected: PASS, or explicit "no tests to run" for packages where the regex has no matches. Any compile failure must be fixed.

- [ ] **Step 5: Run performance triage only if benchmark behavior is still unclear**

If unit tests pass but throughput remains suspicious, follow `docs/development/PERF_TRIAGE.md` before changing more code. Start with a healthy `smoke-default`, then isolate `person-hotpath` or `group-fanout`, and collect pprof/metrics before changing another variable.

## Risk Notes

- Do not add a local-only deployment branch; single-node deployment remains a single-node cluster.
- Do not add user-facing `WK_` config keys unless this repair demonstrably needs runtime tuning. If config keys are added later, update `wukongim.conf.example` and add detailed English comments.
- Keep same-channel append ordering: one channel cell still has only one inflight append command.
- Keep typed backpressure behavior: reactor overload returns `ErrOverloaded`; peer lane pressure returns `ErrPeerBackpressured`.
- Avoid pooling objects that retain `[]byte` payloads unless the reset path is proven safe with tests and `-race`.
