# Channelplane Reactor Runtime Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade `internal/runtime/channelplane` into a bounded reactor runtime with fair channel scheduling, explicit effect execution, robust peer RPC batching, and reliable shutdown.

**Architecture:** `Plane` stays API-compatible while reactors own per-channel state and use a scheduler to advance ready cells. Blocking route/local/peer work moves behind bounded executors and returns completions to reactors. Peer lanes collect batches while separate bounded RPC workers execute `AppendBatches` with timeout and backpressure.

**Tech Stack:** Go 1.23, standard `context`/`sync`/`time`, existing `pkg/channel` DTOs, existing `channelplane` tests with `testify/require`.

---

## File Structure

- Modify: `internal/runtime/channelplane/plane.go` - admission, lifecycle, wiring executors and peer runtime.
- Modify: `internal/runtime/channelplane/reactor.go` - reactor event loop, submit/stop gates, scheduler drain.
- Modify: `internal/runtime/channelplane/channel_cell.go` - explicit cell FSM, actions, retries, unified completion.
- Modify: `internal/runtime/channelplane/scheduler.go` - round-robin ready channel scheduler.
- Create: `internal/runtime/channelplane/effect_executor.go` - bounded async effect runner.
- Modify: `internal/runtime/channelplane/peer_reactor.go` - async lane flush and bounded RPC workers.
- Modify: `internal/runtime/channelplane/options.go` - internal/default runtime budgets and peer RPC timeout.
- Modify: `internal/runtime/channelplane/resolver.go` - invalidation generation fencing for in-flight lookups.
- Modify: `internal/runtime/channelplane/metrics.go` - balanced queued/completed observation helpers.
- Modify: `internal/runtime/channelplane/FLOW.md` - document scheduler, executors, peer async RPC, shutdown.
- Test: `internal/runtime/channelplane/plane_test.go` - plane/cell/resolver lifecycle and retry tests.
- Test: `internal/runtime/channelplane/peer_reactor_test.go` - peer lane timeout/backpressure/async flush tests.
- Create: `internal/runtime/channelplane/scheduler_test.go` - scheduler unit tests.
- Create: `internal/runtime/channelplane/effect_executor_test.go` - executor unit tests.

## Task 1: Safety Baseline For Current Bugs

- [ ] **Step 1: Add stop/submit regression tests**

Add tests in `internal/runtime/channelplane/plane_test.go`:

```go
func TestChannelPlaneStopCompletesAcceptedAppendEvents(t *testing.T) { /* submit directly, stop, future gets ErrClosed */ }
func TestChannelPlaneStopRejectsNewAppendWithoutHanging(t *testing.T) { /* race append around Stop with timeout, no goroutine waits forever */ }
```

- [ ] **Step 2: Add write-fenced route retry tests**

Add local and remote tests in `plane_test.go` where first append returns `channel.ErrWriteFenced`, resolver returns a newer route, and second attempt succeeds.

- [ ] **Step 3: Run tests and confirm failures**

Run: `GOWORK=off go test ./internal/runtime/channelplane -run 'TestChannelPlaneStop|TestChannelCell.*WriteFenced' -count=1`

Expected: FAIL before implementation.

- [ ] **Step 4: Implement minimal lifecycle gates and write-fenced retry**

Modify `reactor.go`, `channel_cell.go`, and `metrics.go` so accepted commands always complete and `channel.ErrWriteFenced` invalidates route once.

- [ ] **Step 5: Verify task tests pass**

Run: `GOWORK=off go test ./internal/runtime/channelplane -run 'TestChannelPlaneStop|TestChannelCell.*WriteFenced' -count=1`

Expected: PASS.

## Task 2: Scheduler-Driven Reactor Progress

- [ ] **Step 1: Add scheduler unit tests**

Create `internal/runtime/channelplane/scheduler_test.go` covering dedupe, FIFO/round-robin pop, and requeue after pop.

- [ ] **Step 2: Implement scheduler**

Replace empty `scheduler` in `scheduler.go` with ready-key queue and dedupe map.

- [ ] **Step 3: Add reactor fairness test**

Add `TestChannelPlaneSchedulerGivesColdChannelProgressUnderHotChannel` in `plane_test.go` using one blocked hot channel and one cold channel on the same reactor.

- [ ] **Step 4: Wire scheduler into reactor**

Modify `reactor.go`: append events enqueue cell and mark ready; completions mark the cell ready if pending remains; reactor drains a bounded number of ready cells per loop.

- [ ] **Step 5: Verify scheduler tests**

Run: `GOWORK=off go test ./internal/runtime/channelplane -run 'TestScheduler|TestChannelPlaneScheduler' -count=1`

Expected: PASS.

## Task 3: Bounded Effect Executor

- [ ] **Step 1: Add executor tests**

Create `effect_executor_test.go` for start/stop, bounded queue backpressure, task completion, and cancellation.

- [ ] **Step 2: Implement `effect_executor.go`**

Implement a small worker pool with `Submit(ctx, task) error`, `Start()`, and `Stop(ctx)`.

- [ ] **Step 3: Convert route/local append effects**

Modify `channel_cell.go` so route resolve and local append submit executor tasks instead of spawning ad-hoc goroutines.

- [ ] **Step 4: Preserve same-channel ordering tests**

Run: `GOWORK=off go test ./internal/runtime/channelplane -run 'TestChannelCellSerializes|TestChannelPlaneCompletesFutures' -count=1`

Expected: PASS.

## Task 4: Async Peer Lane RPC Runtime

- [ ] **Step 1: Add peer blocked-RPC tests**

Add tests in `peer_reactor_test.go` proving a blocked first RPC does not prevent the lane from accepting or timing out later tasks, and pending budget is released on RPC timeout.

- [ ] **Step 2: Add peer RPC timeout option**

Modify `options.go`, `peer_reactor.go`, and `internal/app/build.go` to use existing `cfg.Cluster.DataPlaneRPCTimeout` as `PeerRPCTimeout`.

- [ ] **Step 3: Refactor lane flush**

Modify `peer_reactor.go` so lane loops build batches and dispatch RPC work to bounded workers. Completion maps results back to original tasks.

- [ ] **Step 4: Verify peer tests**

Run: `GOWORK=off go test ./internal/runtime/channelplane -run 'TestPeerReactor' -count=1`

Expected: PASS.

## Task 5: Resolver Invalidation Fencing

- [ ] **Step 1: Add stale singleflight test**

Add `TestRouteResolverInvalidationDoesNotJoinOlderInFlightLookup` in `plane_test.go` or a new `resolver_test.go`.

- [ ] **Step 2: Implement invalidation serial**

Modify `resolver.go` so invalidation increments a per-channel serial, new lookups do not join older calls, and older calls cannot repopulate cache after invalidation.

- [ ] **Step 3: Verify resolver tests**

Run: `GOWORK=off go test ./internal/runtime/channelplane -run 'TestRouteResolver' -count=1`

Expected: PASS.

## Task 6: Documentation And Full Verification

- [ ] **Step 1: Update FLOW**

Update `internal/runtime/channelplane/FLOW.md` with scheduler, executors, async peer RPC, route retry, and shutdown semantics.

- [ ] **Step 2: Run channelplane tests**

Run: `GOWORK=off go test ./internal/runtime/channelplane -count=1`

Expected: PASS.

- [ ] **Step 3: Run race tests**

Run: `GOWORK=off go test -race ./internal/runtime/channelplane -count=1`

Expected: PASS.

- [ ] **Step 4: Run affected app tests**

Run: `GOWORK=off go test ./internal/app -run 'TestConfig|TestAppLifecycle|TestBuild' -count=1`

Expected: PASS or no matching tests for some patterns; any compile failure must be fixed.

