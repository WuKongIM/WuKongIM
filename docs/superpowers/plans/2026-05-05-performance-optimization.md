# Performance Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce idle CPU, goroutine/timer churn, fanout latency, and storage read/write amplification on the current cluster-first WuKongIM runtime.

**Architecture:** Implement the work as independent, benchmark-backed tracks: gateway hot-path fixes first, then delivery fanout isolation, Multi-Raft idle reductions, channel-store scan improvements, and observability hot-path aggregation. Each track must preserve cluster semantics: a deployment with one node remains a 单节点集群, and no optimization may introduce a non-cluster bypass.

**Tech Stack:** Go, gnet gateway transport, Pebble, etcd/raft RawNode, Prometheus metrics, existing `go test` benchmark tooling.

---

## Scope And Guardrails

- Follow `AGENTS.md`: before reading a package, read its `FLOW.md` if present; keep comments on key structs/methods in English; do not introduce standalone non-cluster semantics.
- Treat existing worktree changes as user-owned; never revert unrelated changes.
- Do not tune by only increasing timeouts or disabling correctness checks. The target is structural CPU/allocation reduction.
- Use unit/benchmark tests for development. Run integration tests only when a task explicitly changes multi-node behavior.
- If any config key changes, update `wukongim.conf.example` in the same task.
- Prefer small commits after each task. Commit messages should mention the subsystem, for example `perf(gateway): avoid full idle scans`.

## Relevant Existing Design Context

- `docs/superpowers/specs/2026-04-27-cluster-idle-cpu-code-optimization-design.md`
- `docs/superpowers/plans/2026-04-27-cluster-idle-cpu-code-optimization.md`
- `docs/superpowers/specs/2026-04-29-message-delivery-performance-optimization-design.md`
- `docs/superpowers/plans/2026-04-29-message-delivery-performance-optimization.md`
- `docs/superpowers/specs/2026-04-24-channel-structured-message-table-storage-design.md`
- `docs/superpowers/plans/2026-04-24-channel-structured-message-table-storage.md`

## File Map

### Gateway P0

- Modify: `internal/gateway/core/server.go`
  - Replace full-session idle scan with deadline-driven tracking.
  - Make async SEND queue submit non-blocking.
  - Avoid goroutine-per-write timeout fallback for transport-owned non-blocking writers.
- Create: `internal/gateway/core/idle_tracker.go`
  - Owns idle deadline buckets/min-heap and reschedule validation.
- Create: `internal/gateway/core/idle_tracker_test.go`
  - Unit tests for reschedule, stale deadline skip, close due sessions, and no O(N) steady tick.
- Modify: `internal/gateway/core/server_benchmark_test.go`
  - Add benchmarks for idle tick cost and async dispatch queue-full behavior.
- Modify: `internal/gateway/transport/transport.go`
  - Add a small optional interface for non-blocking write timeout policy.
- Modify: `internal/gateway/transport/gnet/conn.go`
  - Implement the optional non-blocking write policy marker.
- Modify: `internal/gateway/testkit/fake_transport.go`
  - Add test support for blocking/non-blocking write policy.

### Delivery P1

- Modify: `internal/runtime/delivery/actor.go`
  - Split state mutation from resolver/pusher effects.
  - Avoid holding actor lock while calling `Resolver.ResolvePage` and `Pusher.Push`.
- Modify: `internal/runtime/delivery/shard.go`
  - Drive actor effects and apply fenced results.
  - Replace full actor resolve-retry scan with scheduled retry entries.
- Modify: `internal/runtime/delivery/manager.go`
  - Add effect execution dependencies and metrics if needed.
- Modify: `internal/runtime/delivery/types.go`
  - Add English comments for any new effect/result structs.
- Modify: `internal/runtime/delivery/actor_test.go`
  - Add lock/I/O decoupling and ordering tests.
- Modify: `internal/app/deliveryrouting.go`
  - Reuse group-channel frame construction and allow remote node pushes to run with bounded parallelism.
- Modify: `internal/app/deliveryrouting_benchmark_test.go`
  - Add group fanout frame-reuse benchmark.

### Multi-Raft And Transport P1

- Modify: `pkg/slot/multiraft/slot.go`
  - Introduce a lightweight status refresh path that avoids `RawNode.Status()` unless voter config changed or full status is requested.
- Modify: `pkg/slot/multiraft/runtime.go`
  - Remove duplicate `refreshStatus()` after `processReady()` and avoid refreshing when the slot did no useful work.
- Modify: `pkg/slot/multiraft/types.go`
  - Add English comments for any new status structs.
- Modify: `pkg/slot/multiraft/runtime_test.go`
  - Cover status freshness after tick, proposal, ready, and config change.
- Modify: `pkg/cluster/codec.go`
  - Add raft batch message type and binary batch codec.
- Modify: `pkg/cluster/transport.go`
  - Group raft envelopes by target node and send batch frames.
- Modify: `pkg/cluster/cluster.go`
  - Register/handle raft batch frames and step each envelope.
- Modify: `pkg/cluster/transport_glue_test.go`
  - Cover single-frame compatibility and batch-frame receive.
- Modify: `pkg/transport/server.go`
  - Reduce per-RPC goroutine overhead after transport benchmark confirms impact.
- Modify: `pkg/transport/pool.go`
  - Verify RPC enqueue priority remains `PriorityRPC`; keep tests guarding it.

### Channel Store P1

- Modify: `pkg/channel/store/message_table.go`
  - Avoid N+1 payload `Get` during forward/reverse scan by reading primary/payload families in iterator order.
  - Avoid full compatibility encoding only to compute `SizeBytes`.
- Modify: `pkg/channel/store/compatibility_record.go`
  - Add a direct size calculation helper matching `encodeCompatibilityRecord` layout.
- Modify: `pkg/channel/store/message_table_test.go`
  - Cover scan corruption detection when primary/payload family is missing or out of order.
- Modify: `pkg/channel/store/message_table_benchmark_test.go` or create it if absent.
  - Benchmark append and scan allocations for 1, 32, 256 message batches.

### Observability And Cache P2

- Modify: `internal/app/network_observability.go`
  - Reduce global mutex pressure with sharded buckets or per-kind atomics.
- Modify: `internal/app/network_observability_test.go`
  - Keep exact bucket/window behavior tests passing.
- Modify: `pkg/metrics/transport.go`
  - Cache `Counter`/`Observer` handles for stable label values.
- Modify: `internal/runtime/channelmeta/cache.go`
  - Shard activation cache and add capacity/prune policy.
- Modify: `internal/runtime/channelmeta/cache_test.go`
  - Cover TTL prune, capacity eviction, invalidate, and singleflight generation behavior.
- Modify: `internal/runtime/channelmeta/resolver_benchmark_test.go`
  - Benchmark cache hit/miss contention.

---

## Task 0: Establish Baseline Before Changes

**Files:**
- Create: `docs/superpowers/reports/2026-05-05-performance-baseline.md`
- Modify: no production files

- [ ] **Step 1: Record environment metadata**

Run:

```bash
go version
GOMAXPROCS=$(go env GOMAXPROCS 2>/dev/null || true) env | rg '^(GO|GOMAXPROCS|WK_)'
git status --short
```

Expected: output captured in the report. Note any dirty files as pre-existing user changes.

- [ ] **Step 2: Run focused gateway benchmarks**

Run:

```bash
go test -run '^$' -bench 'BenchmarkServer(OpenIdleSessionBatch|SendDispatch)' -benchmem ./internal/gateway/core
```

Expected: PASS. Copy ns/op, B/op, allocs/op into the report.

- [ ] **Step 3: Run focused delivery benchmarks**

Run:

```bash
go test -run '^$' -bench . -benchmem ./internal/runtime/delivery ./internal/app
```

Expected: PASS. If packages have no benchmarks, record that and add benchmark creation to the relevant task.

- [ ] **Step 4: Run focused storage and transport benchmarks**

Run:

```bash
go test -run '^$' -bench . -benchmem ./pkg/channel/store ./pkg/transport ./pkg/slot/multiraft
```

Expected: PASS. If a benchmark is missing, record the gap.

- [ ] **Step 5: Write the baseline report**

Add sections:

```markdown
# Performance Baseline - 2026-05-05

## Environment

## Gateway

## Delivery

## Channel Store

## Multi-Raft / Transport

## Known Gaps
```

- [ ] **Step 6: Commit baseline report**

```bash
git add docs/superpowers/reports/2026-05-05-performance-baseline.md
git commit -m "docs: record performance baseline"
```

---

## Task 1: Gateway Idle Tracker Without Full Session Scans

**Files:**
- Create: `internal/gateway/core/idle_tracker.go`
- Create: `internal/gateway/core/idle_tracker_test.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_benchmark_test.go`

- [ ] **Step 1: Write failing idle tracker tests**

Create tests covering:

```go
func TestIdleTrackerClosesOnlyExpiredSessions(t *testing.T)
func TestIdleTrackerSkipsStaleDeadlineAfterTouch(t *testing.T)
func TestIdleTrackerNextWaitDoesNotScanAllSessions(t *testing.T)
```

Use a small fake state type or package-private `sessionState` helpers. The stale-deadline test must touch a session after scheduling and verify the old heap entry does not close it.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/gateway/core -run 'TestIdleTracker' -count=1
```

Expected: FAIL because `idle_tracker.go` does not exist.

- [ ] **Step 3: Implement `idleTracker`**

Create `internal/gateway/core/idle_tracker.go` with these responsibilities:

```go
// idleTracker tracks session read deadlines without scanning every session on every tick.
type idleTracker struct {
    mu      sync.Mutex
    timeout time.Duration
    heap    idleDeadlineHeap
    seq     uint64
}
```

Required operations:

- `touch(state *sessionState, now time.Time)` schedules `now + timeout` and stores a monotonic token on the state.
- `remove(state *sessionState)` invalidates the state token when closing.
- `nextWait(now time.Time) time.Duration` returns time until the next deadline.
- `popExpired(now time.Time) []*sessionState` returns only states whose latest token is expired.

Add fields to `sessionState` only if needed, for example:

```go
idleSeq atomic.Uint64
```

- [ ] **Step 4: Wire server idle monitor to tracker**

Modify `server.go`:

- Initialize tracker when idle timeout is enabled.
- Call tracker touch from `sessionState.touchReadActivity()` or directly after successful reads.
- Call tracker remove when session closes.
- Replace fixed `time.NewTicker(idleMonitorInterval(timeout))` with timer reset based on `nextWait()`.
- Delete or stop using `sessionStateSnapshot()` for idle closure.

Do not remove `sessionStateSnapshot()` if other callers still use it; otherwise delete it with tests updated.

- [ ] **Step 5: Add benchmark for idle tick cost**

Update `server_benchmark_test.go` with:

```go
func BenchmarkServerIdleMonitorTickCost(b *testing.B)
```

The benchmark should create at least 10k fake states or fake transport sessions and repeatedly trigger idle checks without expiring them. Report allocations.

- [ ] **Step 6: Run gateway tests**

Run:

```bash
go test ./internal/gateway/core ./internal/gateway/session ./internal/gateway/transport/gnet ./internal/gateway/transport/stdnet -count=1
```

Expected: PASS.

- [ ] **Step 7: Run gateway benchmarks**

Run:

```bash
go test -run '^$' -bench 'BenchmarkServer(OpenIdleSessionBatch|IdleMonitorTickCost)' -benchmem ./internal/gateway/core
```

Expected: PASS and idle monitor tick cost no longer scales with all live sessions per 50ms tick.

- [ ] **Step 8: Commit gateway idle tracker**

```bash
git add internal/gateway/core/server.go internal/gateway/core/idle_tracker.go internal/gateway/core/idle_tracker_test.go internal/gateway/core/server_benchmark_test.go
git commit -m "perf(gateway): avoid full idle session scans"
```

---

## Task 2: Gateway Write Timeout Policy For Non-Blocking Transports

**Files:**
- Modify: `internal/gateway/transport/transport.go`
- Modify: `internal/gateway/transport/gnet/conn.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/testkit/fake_transport.go`
- Modify: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Write failing tests for non-blocking writer policy**

Add tests:

```go
func TestServerWritePayloadDoesNotSpawnTimeoutGoroutineForTransportManagedWrites(t *testing.T)
func TestServerWritePayloadStillTimesOutBlockingConn(t *testing.T)
```

The first test should use a fake conn that reports transport-managed non-blocking writes and blocks if called through the old goroutine path. The second test keeps existing deadline behavior for blocking conns.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/gateway/core -run 'TestServerWritePayload' -count=1
```

Expected: FAIL until policy is implemented.

- [ ] **Step 3: Add optional transport write policy interface**

Modify `internal/gateway/transport/transport.go`:

```go
// WriteTimeoutPolicy lets transports state whether Conn.Write already has non-blocking timeout/backpressure semantics.
type WriteTimeoutPolicy interface {
    TransportManagedWriteTimeout() bool
}
```

Keep it optional; do not change the base `Conn` interface.

- [ ] **Step 4: Implement policy in gnet conn**

Modify `internal/gateway/transport/gnet/conn.go`:

```go
func (c *stateConn) TransportManagedWriteTimeout() bool { return true }
```

Rationale: `stateConn.Write` delegates to gnet `AsyncWrite`; `core.Server` should not wrap it in an extra goroutine/timer per write.

- [ ] **Step 5: Update `writePayload`**

In `internal/gateway/core/server.go`, before the fallback goroutine path:

```go
if policy, ok := state.conn.(transport.WriteTimeoutPolicy); ok && policy.TransportManagedWriteTimeout() {
    return state.conn.Write(payload)
}
```

Preserve `SetWriteDeadline` behavior for stdnet conns.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/gateway/core ./internal/gateway/transport/gnet ./internal/gateway/transport/stdnet -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit write timeout policy**

```bash
git add internal/gateway/transport/transport.go internal/gateway/transport/gnet/conn.go internal/gateway/core/server.go internal/gateway/testkit/fake_transport.go internal/gateway/core/server_test.go
git commit -m "perf(gateway): avoid timeout goroutines for gnet writes"
```

---

## Task 3: Non-Blocking Async SEND Dispatch Queue

**Files:**
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/core/server_benchmark_test.go`

- [ ] **Step 1: Add failing queue-full test**

Add:

```go
func TestServerAsyncSendDispatchFallsBackWhenQueueFull(t *testing.T)
```

Arrange a server with `AsyncSendDispatch: true`, a blocked worker, and a tiny queue capacity. Submit enough SEND frames to fill the queue. The test should complete without blocking and verify fallback synchronous dispatch or a recorded handler error.

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/gateway/core -run TestServerAsyncSendDispatchFallsBackWhenQueueFull -count=1
```

Expected: FAIL or hang before the implementation. Use a short test timeout if needed:

```bash
go test ./internal/gateway/core -run TestServerAsyncSendDispatchFallsBackWhenQueueFull -count=1 -timeout=5s
```

- [ ] **Step 3: Make `asyncDispatchQueue.submit` non-blocking**

Change `submit()` in `internal/gateway/core/server.go` to:

```go
select {
case q.tasks <- task:
    return true
default:
    return false
}
```

Keep the existing closed-state lock. Do not send on a closed channel.

- [ ] **Step 4: Add queue-full benchmark**

Add a benchmark that measures async submit under full queue without blocking:

```go
func BenchmarkServerAsyncSendDispatchQueueFullFallback(b *testing.B)
```

- [ ] **Step 5: Run gateway test suite**

```bash
go test ./internal/gateway/core -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit async queue fix**

```bash
git add internal/gateway/core/server.go internal/gateway/core/server_test.go internal/gateway/core/server_benchmark_test.go
git commit -m "perf(gateway): avoid blocking async send submitters"
```

---

## Task 4: Delivery Actor Effects Outside Locks

**Files:**
- Modify: `internal/runtime/delivery/actor.go`
- Modify: `internal/runtime/delivery/shard.go`
- Modify: `internal/runtime/delivery/manager.go`
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/runtime/delivery/actor_test.go`
- Modify: `internal/runtime/delivery/manager_test.go`

- [ ] **Step 1: Add failing lock-decoupling test**

Add a test where fake `Resolver.ResolvePage` blocks. While it is blocked, send `AckRoute` or `SessionClosed` for the same actor. The test should expect ack/offline handling not to block behind resolver I/O.

Suggested test name:

```go
func TestDeliveryActorDoesNotHoldLockDuringResolveIO(t *testing.T)
```

- [ ] **Step 2: Add failing push-decoupling test**

Add a fake `Pusher.Push` that blocks. While it is blocked, send a route offline event. The test should expect route state to process without waiting for the push call to return.

Suggested test name:

```go
func TestDeliveryActorDoesNotHoldLockDuringPushIO(t *testing.T)
```

- [ ] **Step 3: Run failing tests**

```bash
go test ./internal/runtime/delivery -run 'TestDeliveryActorDoesNotHoldLockDuring(Resolve|Push)IO' -count=1 -timeout=5s
```

Expected: FAIL or timeout before implementation.

- [ ] **Step 4: Introduce actor effect structs**

Add English-commented internal structs in `types.go` or a new package-private file:

```go
// actorEffect describes I/O that must run outside the actor lock.
type actorEffect struct { ... }

// actorEffectResult carries a fenced I/O result back into actor state.
type actorEffectResult struct { ... }
```

Effects needed:

- Begin/continue resolve page.
- Push route batch.
- Notify route expired.

Each effect must carry message ID, message sequence, resolve attempt, and enough token/fence data to ignore stale results.

- [ ] **Step 5: Refactor actor methods to emit effects**

Refactor `handleStartDispatch`, `resumeResolvable`, and `applyPush` so locked code only mutates maps/slices and returns effects. Do not call `Resolver` or `Pusher` while holding `actor.mu`.

- [ ] **Step 6: Execute effects in shard**

In `shard.submit`, `routeAcked`, `routeOffline`, and retry processing:

1. Lock actor and collect effects.
2. Unlock actor.
3. Execute I/O effects.
4. Lock actor and apply fenced results.
5. Drain expired events and notify outside lock.

- [ ] **Step 7: Preserve ordering guarantees**

Run existing actor ordering tests. Add a regression test that message sequence order still advances correctly when a stale effect result returns after a newer actor state.

- [ ] **Step 8: Run delivery tests**

```bash
go test ./internal/runtime/delivery -count=1
```

Expected: PASS.

- [ ] **Step 9: Run app delivery routing tests**

```bash
go test ./internal/app -run 'Test.*Delivery|Test.*Committed' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit actor effect refactor**

```bash
git add internal/runtime/delivery/actor.go internal/runtime/delivery/shard.go internal/runtime/delivery/manager.go internal/runtime/delivery/types.go internal/runtime/delivery/actor_test.go internal/runtime/delivery/manager_test.go
git commit -m "perf(delivery): run actor io outside locks"
```

---

## Task 5: Delivery Retry Scheduling Without Full Actor Scan

**Files:**
- Modify: `internal/runtime/delivery/actor.go`
- Modify: `internal/runtime/delivery/shard.go`
- Modify: `internal/runtime/delivery/retry_wheel.go`
- Modify: `internal/runtime/delivery/actor_test.go`
- Modify: `internal/runtime/delivery/manager_test.go`

- [ ] **Step 1: Add failing retry-scan benchmark/test**

Add a benchmark with thousands of idle actors and no due retry work:

```go
func BenchmarkDeliveryProcessRetryTicksManyIdleActors(b *testing.B)
```

It should report allocations and make the current O(actor_count) scan visible.

- [ ] **Step 2: Add resolve retry entries to retry wheel**

Extend retry wheel entries to distinguish route retry and resolve retry. Preserve route retry behavior.

- [ ] **Step 3: Schedule resolve retry when resolve fails**

When `resumeMessage` sets `ResolveRetryAt`, add a wheel entry for that actor/message instead of relying on scanning all actors.

- [ ] **Step 4: Change `processRetryTicks`**

Remove the full actor snapshot loop from `shard.processRetryTicks`. Only process due wheel entries.

- [ ] **Step 5: Run delivery tests and benchmark**

```bash
go test ./internal/runtime/delivery -count=1
go test -run '^$' -bench BenchmarkDeliveryProcessRetryTicksManyIdleActors -benchmem ./internal/runtime/delivery
```

Expected: PASS and benchmark cost no longer grows linearly with idle actor count for empty due work.

- [ ] **Step 6: Commit retry scheduling change**

```bash
git add internal/runtime/delivery/actor.go internal/runtime/delivery/shard.go internal/runtime/delivery/retry_wheel.go internal/runtime/delivery/actor_test.go internal/runtime/delivery/manager_test.go
git commit -m "perf(delivery): schedule resolve retries in retry wheel"
```

---

## Task 6: Reuse Group Fanout Frames And Bound Remote Push Parallelism

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/app/deliveryrouting_benchmark_test.go`

- [ ] **Step 1: Add benchmark for group fanout frame construction**

Create or update benchmark:

```go
func BenchmarkDistributedDeliveryPushGroupFanoutFrameReuse(b *testing.B)
```

Use a group message with many local routes and non-empty payload. Report allocations.

- [ ] **Step 2: Reuse frame for group channel local push**

Change `localDeliveryPush.pushEnvelope` so non-person channels build one `RecvPacket` per message, not per UID. Preserve person-channel recipient view behavior.

- [ ] **Step 3: Reuse encoded frame for group channel remote push**

In `distributedDeliveryPush.deliveryPushItems`, ensure non-person channels still build exactly one item per target node and copy payload only at the final ownership boundary.

- [ ] **Step 4: Add bounded remote node parallelism**

In `distributedDeliveryPush.Push`, run remote node push calls with a small bounded concurrency, for example `min(4, len(remoteRoutes))`. Preserve deterministic result merging by protecting shared result with a local mutex or collecting per-node results first.

Do not create one goroutine per route.

- [ ] **Step 5: Run routing tests and benchmark**

```bash
go test ./internal/app -run 'Test.*DeliveryPush|Test.*DeliveryRouting|Test.*Committed' -count=1
go test -run '^$' -bench 'BenchmarkDistributedDeliveryPush' -benchmem ./internal/app
```

Expected: PASS and lower B/op/allocs for group fanout.

- [ ] **Step 6: Commit fanout optimization**

```bash
git add internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/app/deliveryrouting_benchmark_test.go
git commit -m "perf(delivery): reuse group fanout frames"
```

---

## Task 7: Multi-Raft Lightweight Status And Reduced Idle Refresh

**Files:**
- Modify: `pkg/slot/multiraft/types.go`
- Modify: `pkg/slot/multiraft/slot.go`
- Modify: `pkg/slot/multiraft/runtime.go`
- Modify: `pkg/slot/multiraft/runtime_test.go`
- Modify: `pkg/slot/multiraft/step_test.go`

- [ ] **Step 1: Add status refresh regression tests**

Cover:

```go
func TestRuntimeRefreshesBasicStatusAfterTick(t *testing.T)
func TestRuntimeRefreshesVotersAfterConfigChange(t *testing.T)
func TestRuntimeDoesNotDoubleRefreshAfterReady(t *testing.T)
```

The double-refresh test can use a package-private counter injected into slot for tests if needed.

- [ ] **Step 2: Add lightweight status snapshot**

Add an English-commented internal helper that reads only Lead, Term, Commit, Applied, and RaftState without cloning `Progress` unless voter config changed. If etcd/raft does not expose enough fields directly, cache the last full voter set and only call `RawNode.Status()` on config-change Ready or explicit `Status()` requests.

- [ ] **Step 3: Remove duplicate refresh in `Runtime.processSlot`**

`slot.processReady()` already refreshes status at `pkg/slot/multiraft/slot.go`. Do not call `g.refreshStatus()` again immediately after it returns true.

- [ ] **Step 4: Skip status refresh when slot did no work**

If `processRequests`, `processControls`, `processTick`, and `processReady` all did no state-changing work, avoid status refresh. Preserve leader transition detection when raft state actually changes.

- [ ] **Step 5: Run multiraft tests**

```bash
go test ./pkg/slot/multiraft -count=1
```

Expected: PASS.

- [ ] **Step 6: Run idle CPU benchmark if available**

```bash
go test -run '^$' -bench . -benchmem ./pkg/slot/multiraft
```

Expected: PASS. Record before/after in the baseline report or a new follow-up report.

- [ ] **Step 7: Commit status optimization**

```bash
git add pkg/slot/multiraft/types.go pkg/slot/multiraft/slot.go pkg/slot/multiraft/runtime.go pkg/slot/multiraft/runtime_test.go pkg/slot/multiraft/step_test.go
git commit -m "perf(multiraft): reduce idle status refresh cost"
```

---

## Task 8: Batch Raft Transport Frames By Target Node

**Files:**
- Modify: `pkg/cluster/codec.go`
- Modify: `pkg/cluster/transport.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/transport_glue_test.go`
- Modify: `pkg/cluster/transport_test.go`

- [ ] **Step 1: Add failing codec tests**

Add tests for:

```go
func TestEncodeDecodeRaftBatchBodyRoundTrip(t *testing.T)
func TestDecodeRaftBatchBodyRejectsTruncatedPayload(t *testing.T)
```

Batch body should include repeated `(slotID, raftMessageBytes)` pairs.

- [ ] **Step 2: Add new message type**

In `pkg/cluster/codec.go`, add:

```go
msgTypeRaftBatch uint8 = 3
```

Ensure it does not conflict with existing message types.

- [ ] **Step 3: Implement batch codec**

Add:

```go
func encodeRaftBatchBody(items []raftBatchItem) []byte
func decodeRaftBatchBody(body []byte) ([]raftBatchItem, error)
```

Use length-prefixed binary encoding and reject overflow/trailing bytes.

- [ ] **Step 4: Group sends in raft transport**

Modify `raftTransport.Send` to group envelopes by `env.Message.To`. For one item, keep existing `msgTypeRaft` path. For multiple items to the same target, send one `msgTypeRaftBatch`.

- [ ] **Step 5: Register and handle batch frames**

In `Cluster.startServer`, register `msgTypeRaftBatch`. Add handler that decodes each item, unmarshals the raft message, and calls `runtime.Step` for each envelope.

- [ ] **Step 6: Run cluster transport tests**

```bash
go test ./pkg/cluster -run 'Test.*Transport|Test.*Raft' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run broader cluster unit tests if time allows**

```bash
go test ./pkg/cluster ./pkg/slot/multiraft -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit raft batching**

```bash
git add pkg/cluster/codec.go pkg/cluster/transport.go pkg/cluster/cluster.go pkg/cluster/transport_glue_test.go pkg/cluster/transport_test.go
git commit -m "perf(cluster): batch raft transport frames"
```

---

## Task 9: Channel Store Scan Without N+1 Payload Gets

**Files:**
- Modify: `pkg/channel/store/message_table.go`
- Modify: `pkg/channel/store/compatibility_record.go`
- Modify: `pkg/channel/store/message_table_test.go`
- Create or modify: `pkg/channel/store/message_table_benchmark_test.go`

- [ ] **Step 1: Add scan benchmarks**

Benchmark forward and reverse scans:

```go
func BenchmarkMessageTableScanBySeq(b *testing.B)
func BenchmarkMessageTableScanBySeqReverse(b *testing.B)
```

Cases: 1, 32, 256 rows; payload sizes 128B and 4KB.

- [ ] **Step 2: Add corruption tests for paired family scanning**

Cover missing payload, payload-before-primary, and mismatched seq family. Existing tests may cover some cases; add only missing cases.

- [ ] **Step 3: Implement paired iterator scan**

Refactor `scanBySeq` and `scanBySeqReverse` so they consume primary and payload families from the same iterator rather than calling `getValue()` per row.

Forward scan logic:

1. Seek primary lower bound.
2. Read primary family.
3. Move to expected payload family for same seq.
4. Decode row from copied primary/payload bytes.
5. Continue at next seq primary.

Reverse scan logic mirrors this safely.

- [ ] **Step 4: Add direct compatibility size helper**

Add helper that computes encoded compatibility size without allocating the full record:

```go
func compatibilityRecordPayloadSize(row messageRow) (int, error)
```

It must match `encodeCompatibilityRecord(row).SizeBytes` in a test.

- [ ] **Step 5: Replace `compatibilityEncodedRecordSize` implementation**

Change it to call direct size helper rather than `toCompatibilityRecord()`.

- [ ] **Step 6: Run channel store tests**

```bash
go test ./pkg/channel/store -count=1
```

Expected: PASS.

- [ ] **Step 7: Run channel store benchmarks**

```bash
go test -run '^$' -bench 'BenchmarkMessageTableScan' -benchmem ./pkg/channel/store
```

Expected: PASS and fewer allocations per scanned row.

- [ ] **Step 8: Commit store scan optimization**

```bash
git add pkg/channel/store/message_table.go pkg/channel/store/compatibility_record.go pkg/channel/store/message_table_test.go pkg/channel/store/message_table_benchmark_test.go
git commit -m "perf(channel/store): scan message families without n+1 gets"
```

---

## Task 10: Transport RPC Goroutine Reduction

**Files:**
- Modify: `pkg/transport/server.go`
- Modify: `pkg/transport/conn.go`
- Modify: `pkg/transport/server_test.go`
- Modify: `pkg/transport/benchmark_test.go`

- [ ] **Step 1: Benchmark current RPC server allocations**

Run existing benchmarks and add one if missing:

```go
func BenchmarkTransportRPCServer(b *testing.B)
```

- [ ] **Step 2: Add test for bounded goroutine behavior**

Add a test that sends many RPC requests with a blocked handler and verifies goroutine growth is bounded by configured workers, not request count.

- [ ] **Step 3: Introduce RPC worker pool**

Add a server-owned bounded worker pool for RPC requests. The dispatch path should enqueue copied request bodies to the pool. If the pool is full, return a remote error response or close according to existing transport semantics.

- [ ] **Step 4: Replace per-request cancel goroutine**

Use connection-level cancellation or a shared done channel so `handleRPCRequest` does not create a watcher goroutine per request.

- [ ] **Step 5: Run transport tests and benchmarks**

```bash
go test ./pkg/transport -count=1
go test -run '^$' -bench 'BenchmarkTransportRPC' -benchmem ./pkg/transport
```

Expected: PASS and reduced goroutine/alloc overhead.

- [ ] **Step 6: Commit transport RPC change**

```bash
git add pkg/transport/server.go pkg/transport/conn.go pkg/transport/server_test.go pkg/transport/benchmark_test.go
git commit -m "perf(transport): bound rpc server goroutines"
```

---

## Task 11: Observability Hot-Path Aggregation

**Files:**
- Modify: `pkg/metrics/transport.go`
- Modify: `internal/app/network_observability.go`
- Modify: `internal/app/network_observability_test.go`
- Modify: `internal/app/observability_test.go`

- [ ] **Step 1: Add metrics observer benchmark**

Add benchmark for repeated `ObserveSentBytes` / `ObserveReceivedBytes` on stable msg types.

- [ ] **Step 2: Cache Prometheus handles**

In `pkg/metrics/transport.go`, cache stable label handles for known message types. For unknown labels, fall back to current path.

- [ ] **Step 3: Shard network observability buckets**

Replace one global `networkObservability.mu` hot path for traffic buckets with sharded locks or atomic counters. Keep snapshot behavior exactly the same.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/app -run 'TestNetworkObservability|TestMergeTransportObserver|TestTransport' -count=1
go test ./pkg/metrics -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit observability optimization**

```bash
git add pkg/metrics/transport.go internal/app/network_observability.go internal/app/network_observability_test.go internal/app/observability_test.go
git commit -m "perf(observability): reduce transport hook overhead"
```

---

## Task 12: Channel Meta Cache Contention And Capacity

**Files:**
- Modify: `internal/runtime/channelmeta/cache.go`
- Modify: `internal/runtime/channelmeta/cache_test.go`
- Modify: `internal/runtime/channelmeta/resolver_benchmark_test.go`

- [ ] **Step 1: Add contention benchmark**

Benchmark cache hit under high parallelism and many channel keys:

```go
func BenchmarkActivationCacheLoadPositiveParallel(b *testing.B)
func BenchmarkChannelMetaResolverBusinessCacheHitParallel(b *testing.B)
```

- [ ] **Step 2: Add capacity/prune tests**

Tests:

```go
func TestActivationCachePrunesExpiredEntries(t *testing.T)
func TestActivationCacheEnforcesPositiveCapacity(t *testing.T)
func TestActivationCacheInvalidatePreservesGeneration(t *testing.T)
```

- [ ] **Step 3: Shard activation cache**

Split positive/negative/calls/generations maps by hash shard. Keep singleflight behavior per key and preserve generation invalidation semantics.

- [ ] **Step 4: Add bounded capacity**

Add conservative defaults, for example 16k positive entries and 4k negative entries, or derive from config only if a config change is explicitly desired. Prefer no new config unless benchmarks show need.

- [ ] **Step 5: Run channelmeta tests**

```bash
go test ./internal/runtime/channelmeta -count=1
go test -run '^$' -bench 'Benchmark.*ChannelMeta|BenchmarkActivationCache' -benchmem ./internal/runtime/channelmeta
```

Expected: PASS and lower lock contention on cache hit.

- [ ] **Step 6: Commit cache optimization**

```bash
git add internal/runtime/channelmeta/cache.go internal/runtime/channelmeta/cache_test.go internal/runtime/channelmeta/resolver_benchmark_test.go
git commit -m "perf(channelmeta): shard activation cache"
```

---

## Final Verification

- [ ] **Step 1: Run focused unit tests**

```bash
go test ./internal/gateway/... ./internal/runtime/delivery ./internal/runtime/channelmeta ./internal/app ./pkg/transport ./pkg/slot/multiraft ./pkg/channel/store ./pkg/cluster -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full unit tests if time allows**

```bash
go test ./...
```

Expected: PASS. If too slow or failing due unrelated existing changes, record the reason and focused test evidence.

- [ ] **Step 3: Re-run benchmark subset**

```bash
go test -run '^$' -bench 'BenchmarkServer|BenchmarkDelivery|BenchmarkMessageTable|BenchmarkTransport|BenchmarkActivationCache' -benchmem ./internal/gateway/core ./internal/runtime/delivery ./internal/app ./pkg/channel/store ./pkg/transport ./internal/runtime/channelmeta
```

Expected: PASS. Compare against `docs/superpowers/reports/2026-05-05-performance-baseline.md`.

- [ ] **Step 4: Optional process-level idle pprof**

For a local 单节点集群:

```bash
go run ./cmd/wukongim -config ./wukongim.conf
```

Capture CPU/heap profile according to the app's pprof/observability setup if enabled. Use exact profile commands available in this repo/environment and add the result to a follow-up report.

- [ ] **Step 5: Update project knowledge only if new durable rules are discovered**

If implementation discovers a new important project rule, update:

```text
docs/development/PROJECT_KNOWLEDGE.md
```

Keep the entry short and precise.

---

## Execution Order Recommendation

1. Task 0 baseline.
2. Tasks 1-3 gateway P0.
3. Tasks 4-6 delivery P1.
4. Tasks 7-8 Multi-Raft/raft transport P1.
5. Task 9 channel store P1.
6. Tasks 10-12 transport/observability/cache P2.
7. Final verification.

## Plan Review Note

The standard skill review loop asks for a plan-document-reviewer subagent. The current Codex harness instructions only allow spawning subagents when the user explicitly asks for subagents, so this plan should be manually reviewed or reviewed in a later turn if subagent review is explicitly requested.
