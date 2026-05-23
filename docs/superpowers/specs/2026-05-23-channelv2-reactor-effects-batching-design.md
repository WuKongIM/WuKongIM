# Channelv2 Reactor Effects And Batching Design

**Date:** 2026-05-23
**Status:** Proposed

## Overview

`pkg/channelv2` v0 proved a minimal end-to-end loop for `ApplyMeta`, append, fetch, follower apply, ACK, and HW commit. It also created the package boundaries needed for a cleaner implementation than the current `pkg/channel` runtime. However, v0 is still a functional prototype, not yet a real validation of the intended **Multiple Reactors With Goroutine Pool** architecture.

The main v0 limitation is that core store and RPC calls still happen synchronously inside reactor handlers. Append also writes one request at a time, and follower replication is driven from `service.Tick` using `Fetch` to infer the next offset. This makes the current benchmarks useful only as a smoke test. They do not measure the target architecture.

This Phase 2 design upgrades `pkg/channelv2` to the intended concurrency model:

- Reactors only mutate local state, enqueue work, and apply fenced completions.
- Store and RPC operations run in typed bounded worker pools.
- Worker results route back to the owning reactor as high-priority completion events.
- Appends use per-channel pending queues and batch flush rules.
- Follower replication state is owned by the reactor/channel runtime, not the service layer.
- Benchmark hooks expose batch size, queue depth, latency, and worker result metrics.

Phase 2 intentionally keeps the replication protocol as short-poll `Pull` + explicit `Ack`. Long-poll lanes, retention, snapshot, migration, and leader repair remain out of scope.

## Goals

- Remove blocking store and RPC calls from reactor event handlers.
- Use typed worker tasks and results for store append, store read, store apply, RPC pull, and RPC ACK.
- Route worker completions back to the owning reactor by `Fence.ChannelKey`.
- Enforce `Fence` validation before applying any asynchronous completion.
- Add per-channel append pending queues with `maxRecords`, `maxBytes`, and `maxWait` batching.
- Keep one store append inflight per channel while allowing different channels to use worker pools concurrently.
- Move follower pull/apply/ack state into the reactor-owned channel runtime.
- Use follower local `LEO + 1` as pull offset instead of deriving it through `Fetch`.
- Add typed backpressure behavior for mailbox, append queue, worker queue, and replication queue pressure.
- Add lightweight observer hooks and benchmark cases that can meaningfully compare memory store, old store adapter, and existing `pkg/channel` hot paths.

## Non-Goals

- No `internal/app` integration or replacement of existing `pkg/channel`.
- No migration write fence, fence-and-drain, learner promotion, or cutover proof.
- No retention boundary adoption or physical trim.
- No snapshot install or snapshot catch-up path.
- No leader repair, promotion dry-run, reconcile, or minority tail truncation.
- No long-poll lane session protocol, membership version reset, or cursor-delta batching.
- No storage format rewrite.
- No single-node bypass branch. Single-node remains `MinISR=1` quorum behavior.

## Current V0 Limitations

### 1. Reactor handlers still block

`reactor.handleAppend`, `handleFetch`, `handlePull`, and `handleApplyRecords` call the store directly. This means a slow Pebble append or read blocks all other channels assigned to the same reactor.

### 2. Worker pools do not carry the hot path

`pkg/channelv2/worker` exists, but the core append/fetch/replication path bypasses it. This prevents measuring worker queue pressure, effect completion latency, or reactor result priority.

### 3. Append does not batch

Each append request builds records and immediately attempts one store append. A hot channel therefore has no per-channel group commit behavior in `channelv2`, and many-channel benchmarks do not validate batch fairness.

### 4. Replication state sits above reactors

`service.Tick` iterates metadata, calls `Fetch`, computes `next = committed + 1`, calls transport pull, applies records, and sends ACK. This puts replication scheduling outside the channel runtime and uses committed state rather than local `LEO`.

### 5. Benchmarks are smoke tests only

The current benchmark results include synchronous reactor blocking and lack batch/queue metrics. They cannot answer whether multiple reactors plus worker pools improve throughput or tail latency.

## Architecture Summary

```text
service facade
  -> reactor.Group.Submit(event)
  -> owning Reactor mailbox
  -> runtimeChannel queues and machine state
  -> worker.Pools.Submit(task)
  -> worker executes store/RPC with deps
  -> group.Complete(result)
  -> owning Reactor EventWorkerResult (high priority)
  -> runtimeChannel applies fenced completion
  -> futures complete / replication dirty / next task
```

Ownership rules:

- `machine.ChannelState` is modified only by its owning reactor.
- `runtimeChannel` queue and replication state are modified only by its owning reactor.
- Worker tasks carry copies of request data and never mutate reactor state.
- Worker results are ignored if their fence does not match the current channel generation, epoch, leader epoch, and operation id.

## Package Changes

Add files:

```text
pkg/channelv2/reactor/
  effect.go
  append_queue.go
  replication_state.go
  metrics.go

pkg/channelv2/worker/
  deps.go
  pools.go
```

Adjust existing files:

```text
pkg/channelv2/reactor/reactor.go
  stop direct store/RPC calls; handle task submission and worker completions

pkg/channelv2/reactor/group.go
  own worker pools and implement worker.CompletionSink

pkg/channelv2/reactor/event.go
  add typed worker result and tick events

pkg/channelv2/machine/append.go
  support batch append completion with multiple request waiters

pkg/channelv2/service/service.go
  Tick submits low-priority tick events instead of performing replication

pkg/channelv2/worker/task.go and result.go
  replace RunFunc-only prototype with typed task and result payloads
```

## Worker Task And Result Contract

Task shape:

```go
type Task struct {
    Kind  TaskKind
    Fence channelv2.Fence

    StoreAppend        *StoreAppendTask
    StoreReadCommitted *StoreReadCommittedTask
    StoreReadLog       *StoreReadLogTask
    StoreApply         *StoreApplyTask
    RPCPull            *RPCPullTask
    RPCAck             *RPCAckTask
}
```

Result shape:

```go
type Result struct {
    Kind  TaskKind
    Fence channelv2.Fence
    Err   error

    StoreAppend        *StoreAppendResult
    StoreReadCommitted *StoreReadCommittedResult
    StoreReadLog       *StoreReadLogResult
    StoreApply         *StoreApplyResult
    RPCPull            *RPCPullResult
    RPCAck             *RPCAckResult
}
```

Worker dependencies:

```go
type Deps struct {
    Stores    store.Factory
    Transport transport.Client
}
```

Pool group:

```go
type Pools struct {
    StoreAppend *Pool
    StoreRead   *Pool
    StoreApply  *Pool
    RPC         *Pool
}
```

Routing:

```text
TaskStoreAppend -> StoreAppend
TaskStoreReadCommitted -> StoreRead
TaskStoreReadLog -> StoreRead
TaskStoreApply -> StoreApply
TaskRPCPull -> RPC
TaskRPCAck -> RPC
```

The pool group owns task execution. `reactor.Group` implements `worker.CompletionSink`:

```text
worker result
  -> group.Complete(result)
  -> router.Pick(result.Fence.ChannelKey)
  -> reactor.Submit(PriorityHigh, EventWorkerResult)
```

## Append Batching

### Runtime queue state

```go
type appendQueue struct {
    pending []appendRequest
    bytes int
    flushDue time.Time
    timerArmed bool
    storeBlocked bool
}

type appendRequest struct {
    opID ch.OpID
    req ch.AppendBatchRequest
    future *Future
    enqueuedAt time.Time
    records []ch.Record
    commitMode ch.CommitMode
}
```

`runtimeChannel` gains:

```go
type runtimeChannel struct {
    state *machine.ChannelState
    store store.ChannelStore

    appendQ appendQueue
    appendInflight *appendBatch
    waiters map[ch.OpID]*Future
    replication replicationState
}
```

### Flush rules

Flush when any condition is true:

- pending record count reaches `AppendBatchMaxRecords`.
- pending bytes reaches `AppendBatchMaxBytes`.
- `AppendBatchMaxWait` has elapsed for the oldest pending request.
- a tick observes an expired `flushDue`.

Do not flush when:

- `appendInflight != nil`.
- store append pool is blocked and retry is not due.
- local role is not leader.
- channel is not commit-ready.

### Store append batch

A flush concatenates pending records in request order and creates one store append task. The store append fence uses a batch operation id:

```text
batchOpID = group.NextOpID()
client request OpIDs remain separate
InflightAppend stores batchOpID and request ranges
```

Recommended machine model:

- `AppendWaiters` remain keyed by client request `OpID`.
- `InflightAppend` stores one `BatchOpID` plus record ranges per waiter.
- `StoreAppendResult.Fence.OpID` matches the batch op id, not any single client op id.

This lets the reactor complete many client futures from one durable append while still rejecting stale batch completions.

### Completion

```text
StoreAppendResult
  -> validate batch fence
  -> assign base offsets to all records
  -> publish LEO
  -> Progress[local].Match = LEO
  -> AdvanceHW
  -> complete waiters whose target <= HW or whose mode is CommitModeLocal
  -> mark replication dirty
  -> if pending queue is not empty, schedule another flush attempt
```

A hot channel may start only one store append per reactor turn. This keeps other channels on the same reactor from starving.

## Async Fetch

Fetch flow:

```text
FetchEvent
  -> machine.BuildFetch captures HW
  -> immediate empty reply if FromSeq > HW
  -> submit StoreReadCommitted task
  -> WorkerResult(StoreReadCommitted)
  -> machine.ApplyReadCommitted
  -> complete future
```

Fetch backpressure:

- Mailbox full: service returns `ErrBackpressured` before acceptance.
- StoreRead pool full: complete fetch future with `ErrBackpressured`.
- Fetch futures are not queued indefinitely in `runtimeChannel`.

## Reactor-Owned Replication State

Remove replication scheduling from `service.Tick`. `Tick` becomes a low-priority reactor event:

```go
func (c *cluster) Tick(ctx context.Context) error {
    return c.group.Tick(ctx)
}
```

Follower state:

```go
type replicationState struct {
    pullInflight bool
    ackInflight bool
    dirty bool

    nextPullAt time.Time
    backoff time.Duration
    lastLeaderHW uint64
    lastError error

    pendingPull *transport.PullResponse
    applyBlocked bool
}
```

### Pull scheduling

On `ApplyMeta` to follower role:

```text
replication.dirty = true
nextPullAt = now
```

On tick:

```text
if role != follower: skip
if pullInflight: skip
if now < nextPullAt: skip
if transport missing: skip
nextOffset = state.LEO + 1
submit RPCPullTask
pullInflight = true
```

### Pull result

```text
error:
  pullInflight=false
  backoff=nextBackoff(backoff)
  nextPullAt=now+backoff

records empty:
  pullInflight=false
  state.HW=min(state.LEO, leaderHW)
  nextPullAt=now+idlePollInterval

records non-empty:
  pullInflight=false
  lastLeaderHW=leaderHW
  pendingPull=response
  submit StoreApplyTask
```

### Apply result

```text
StoreApplyResult:
  state.LEO=result.LEO
  state.HW=min(state.LEO, lastLeaderHW)
  pendingPull=nil
  submit RPCAckTask{MatchOffset: state.LEO}
  dirty=true
```

If `StoreApply` pool is full:

```text
pendingPull=response
applyBlocked=true
retry on next tick
```

There is no unbounded pull-response queue in Phase 2.

### ACK result

```text
success:
  ackInflight=false
  reset backoff

error:
  ackInflight=false
  backoff=nextBackoff(backoff)
  nextPullAt=now+backoff
```

Leader-side ACK still enters the leader reactor and calls `machine.ApplyFollowerAck` to advance HW and complete append waiters.

## Backpressure Semantics

Append path:

```text
service -> reactor mailbox full:
  ErrBackpressured before accepted

per-channel append queue count/bytes full:
  ErrBackpressured before accepted

StoreAppend pool full after request accepted:
  keep request in appendQ
  mark storeBlocked
  retry on tick
  if caller context cancels before commit, complete future with ctx.Err
```

Fetch path:

```text
StoreRead pool full:
  complete fetch future with ErrBackpressured
```

Replication path:

```text
RPC pool full:
  do not fail channel
  nextPullAt=now+shortBackoff

StoreApply pool full:
  keep one pendingPull response
  retry on tick
```

Close behavior:

- Stop accepting new service requests.
- Fail pending append and fetch futures with `ErrClosed`.
- Ignore stale worker results after close.
- Close worker pools after reactors stop accepting new work.

## Fairness

- Worker results use high-priority mailbox events.
- Ticks are low-priority and coalesced.
- A channel may flush at most one append batch per reactor turn.
- A channel may submit at most one replication pull per tick while `pullInflight == false`.
- Reactor drain remains bounded by `MaxEventsPerTurn`.

This keeps a hot channel from monopolizing its reactor while still giving it batching efficiency.

## Metrics And Benchmark Hooks

Add a no-op-by-default observer:

```go
type Observer interface {
    SetReactorMailboxDepth(reactorID int, priority string, depth int)
    SetWorkerQueueDepth(pool string, depth int)
    ObserveAppendBatch(records int, bytes int, wait time.Duration)
    ObserveAppendLatency(mode ch.CommitMode, d time.Duration)
    ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration)
}
```

Benchmarks should call `b.ReportAllocs()` and capture:

- messages per second
- p50/p95/p99 append latency in custom benchmark helpers
- append batch size distribution
- reactor mailbox depth snapshots
- worker queue depth snapshots
- goroutine count before and after benchmark

## Test Plan

Reactor tests:

- Append events batch by `AppendBatchMaxRecords`.
- Append events batch after `AppendBatchMaxWait` tick.
- One store append result completes multiple futures.
- Stale store append result is ignored after epoch or generation change.
- StoreAppend pool full keeps accepted append pending and retries on tick.
- Fetch uses worker result path and does not block reactor.
- Worker result priority prevents completion starvation under append pressure.

Replication tests:

- Follower pull uses `LEO + 1`, not `Fetch` committed sequence.
- Pull inflight suppresses duplicate pull.
- Pull error backs off.
- Store apply result sends ACK.
- ACK result resets backoff.
- Old ACK after leader epoch bump is ignored.
- StoreApply pool full keeps one pending pull response and retries.

Worker tests:

- Typed store append task runs through store deps.
- Typed read committed task runs through store deps.
- Typed RPC pull and ACK tasks run through transport deps.
- Completion sink routes results back to the owning reactor.
- Queue depth and backpressure are observable.

Benchmark tests:

- `BenchmarkAppendSingleNodeHotChannelBatched`.
- `BenchmarkAppendSingleNodeManyChannelsAsync`.
- `BenchmarkAppendThreeNodeManyChannelsAsync`.
- `BenchmarkAppendOldStoreAdapterAsync`.
- Existing v0 benchmarks remain for smoke comparison.

## Implementation Phases

### Phase 2.1: Worker typed tasks and pool group

Replace the `RunFunc`-only prototype with typed task payloads, typed results, deps, and pool group routing. Keep compatibility with tests by allowing a test-only function task if useful.

### Phase 2.2: Reactor completion routing and async fetch

Make `reactor.Group` a completion sink. Submit `StoreReadCommitted` tasks for fetch and apply `EventWorkerResult` completions back in the reactor.

### Phase 2.3: Async append with per-channel batching

Add append queues, flush rules, batch op fencing, multi-future completion, and retry on StoreAppend pool backpressure.

### Phase 2.4: Reactor-owned follower replication

Move follower pull/apply/ack scheduling from `service.Tick` into reactor tick handling. Use local `LEO + 1` as pull offset.

### Phase 2.5: Backpressure and fairness hardening

Add queue count/byte limits, blocked-store retry, fetch fail-fast, apply pending response retry, and max one append flush per channel per reactor turn.

### Phase 2.6: Metrics and benchmark report

Add observer hooks, expanded benchmarks, and a short report in `docs/superpowers/reports/` comparing v0 and Phase 2 behavior.

## Open Questions

- Should accepted append futures be cancelled by context after admission, or should context only bound admission and wait? Phase 2 should prefer cancellation-aware futures, but this needs careful cleanup tests.
- Should `StoreAppend` and `StoreApply` share one store write pool for old store adapter fairness, or stay separate to observe independent pressure?
- Should tick be explicit for tests only, with production reactors owning internal timers, or should explicit tick remain part of the experimental facade?
- Should observer hooks be pull-based snapshots instead of callback methods to reduce hot-path overhead?

