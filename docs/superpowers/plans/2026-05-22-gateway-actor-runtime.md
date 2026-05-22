# Gateway Actor Runtime Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework gateway transport to a gnet-only actor runtime, remove stdnet and blocking writer fallback, and delete obsolete session outbound queue code.

**Architecture:** gnet callbacks enqueue connection events into fixed actor shards. Each connection has a single actor owner that serializes open/data/close processing. Core writes directly to gnet-managed asynchronous transport writes; session stores metadata only.

**Tech Stack:** Go 1.23, gnet v2, existing gateway core/session/protocol packages, `go test`, `go test -race`, benchmark tests.

---

## File Structure

| Path | Responsibility |
| --- | --- |
| `internal/gateway/transport/gnet/actor.go` | New actor pool, actor shard, scheduling, lifecycle, queue drain loop. |
| `internal/gateway/transport/gnet/conn.go` | `connState` event queue, outbound accounting, async write ownership, no per-conn goroutine. |
| `internal/gateway/transport/gnet/group.go` | Start/stop actor pool with engine group; route gnet callbacks into actor events. |
| `internal/gateway/transport/gnet/*_test.go` | Actor scheduling, no per-conn goroutine, websocket/tcp behavior, backpressure. |
| `internal/gateway/core/server.go` | Remove encoded queue writer path and write timeout fallback; direct async transport writes only. |
| `internal/gateway/core/*_test.go` | Replace writer/queue tests with direct async outbound tests. |
| `internal/gateway/session/session.go` | Remove encoded queue and writer state; keep session ID, addresses, values, lifecycle. |
| `internal/gateway/session/*_test.go` | Remove queue tests; keep metadata/value/lifecycle tests. |
| `internal/gateway/gateway.go` | Register only gnet transport. |
| `internal/gateway/gateway_internal_test.go` | Expect only gnet built-in transport. |
| `internal/gateway/gateway_test.go` | Replace stdnet integration cases with gnet or delete redundant stdnet-only tests. |
| `internal/gateway/options_test.go` | Remove explicit stdnet-valid expectations; use gnet listener options. |
| `internal/gateway/transport/stdnet/` | Delete package and tests. |
| `cmd/wukongim/config.go` | Remove obsolete gateway session config parsing and assignment. |
| `cmd/wukongim/config_test.go` | Replace stdnet listeners with gnet; remove obsolete config key tests. |
| `wukongim.conf.example` | Remove obsolete session keys; keep gnet listener examples. |
| `internal/gateway/FLOW.md` | Update gateway flow to gnet-only actor runtime. |
| `AGENTS.md` | Update directory structure if stdnet directory removal changes documented tree. |

---

## Task 1: Lock in gnet-only transport expectations

**Files:**
- Modify: `internal/gateway/gateway_internal_test.go`
- Modify: `internal/gateway/options_test.go`
- Modify: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Write failing test for built-in registry**

Update `TestBuildRegistryRegistersBuiltinTransports` or equivalent to assert gnet exists and stdnet does not exist:

```go
func TestBuildRegistryRegistersOnlyGnetTransport(t *testing.T) {
    registry, err := buildRegistry()
    require.NoError(t, err)

    factory, err := registry.Transport(gnettransport.Name)
    require.NoError(t, err)
    require.IsType(t, (*gnettransport.Factory)(nil), factory)

    _, err = registry.Transport("stdnet")
    require.ErrorIs(t, err, core.ErrTransportFactoryNotFound)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway -run TestBuildRegistryRegistersOnlyGnetTransport -count=1`

Expected: FAIL because stdnet is still registered or old test still expects stdnet.

- [ ] **Step 3: Update option/config tests to use gnet listeners**

Replace listener JSON strings in `cmd/wukongim/config_test.go` from `"transport":"stdnet"` to `"transport":"gnet"`.

Replace `internal/gateway/options_test.go` expectations that say explicit stdnet listeners remain valid with gnet-only expectations. Listener option validation should still trim transport names, but examples should use `gnet`.

- [ ] **Step 4: Run targeted tests**

Run: `go test ./internal/gateway ./cmd/wukongim -run 'Gateway|Config|Options|BuildRegistry' -count=1`

Expected: tests fail only where production still registers stdnet or parses removed keys.

---

## Task 2: Remove stdnet from production registration

**Files:**
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/gateway_internal_test.go`

- [ ] **Step 1: Remove stdnet import and registration**

Delete the stdnet import and this registration block from `buildRegistry`:

```go
if err := registry.RegisterTransport(stdnet.NewFactory()); err != nil {
    return nil, err
}
```

Keep gnet registration and protocol registrations.

- [ ] **Step 2: Run registry tests**

Run: `go test ./internal/gateway -run 'TestBuildRegistry|TestGateway' -count=1`

Expected: gnet-only registry tests pass; stdnet integration tests still fail until updated/deleted.

---

## Task 3: Delete stdnet package and stdnet-only tests

**Files:**
- Delete: `internal/gateway/transport/stdnet/`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/FLOW.md`

- [ ] **Step 1: Delete stdnet package**

Remove all files under `internal/gateway/transport/stdnet/`.

- [ ] **Step 2: Remove stdnet integration cases**

In `internal/gateway/gateway_test.go`, delete tests whose only purpose is stdnet TCP/WebSocket behavior. If the same behavior is not covered by gnet tests, convert the test to use `Transport: "gnet"` and rename it.

- [ ] **Step 3: Update FLOW docs**

Remove stdnet from transport tables and flow notes. Replace `gnet | stdnet` with `gnet`.

- [ ] **Step 4: Run search guard**

Run: `rg -n 'stdnet' internal cmd wukongim.conf.example AGENTS.md`

Expected: no production/test references remain, except historical docs under `docs/superpowers/` if we decide not to rewrite old historical plans/specs.

- [ ] **Step 5: Run gateway transport tests**

Run: `go test ./internal/gateway/... -count=1`

Expected: failures only from actor work not implemented yet or obsolete writer/session queue tests.

---

## Task 4: Introduce gnet actor pool skeleton

**Files:**
- Create: `internal/gateway/transport/gnet/actor.go`
- Modify: `internal/gateway/transport/gnet/group.go`
- Test: `internal/gateway/transport/gnet/actor_test.go`

- [ ] **Step 1: Write failing actor scheduling test**

Create `actor_test.go` with a fake `connState` that records processed events. Test that scheduling the same connection multiple times only queues it once while it is already scheduled.

```go
func TestActorShardSchedulesConnectionOnceWhilePending(t *testing.T) {
    shard := newActorShard(0)
    state := &connState{}

    require.True(t, shard.schedule(state))
    require.False(t, shard.schedule(state))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/transport/gnet -run TestActorShardSchedulesConnectionOnceWhilePending -count=1`

Expected: FAIL because actor types do not exist.

- [ ] **Step 3: Implement minimal actor types**

Add `actor.go`:

```go
type actorPool struct {
    shards []*actorShard
    stopCh chan struct{}
    wg     sync.WaitGroup
}

type actorShard struct {
    id     int
    ready  chan *connState
    stopCh <-chan struct{}
}
```

Add `newActorPool(shards int)`, `start()`, `stop()`, `shardForConn(id uint64)`, and `actorShard.schedule(state *connState) bool`.

- [ ] **Step 4: Run actor tests**

Run: `go test ./internal/gateway/transport/gnet -run TestActor -count=1`

Expected: actor skeleton tests pass.

---

## Task 5: Move connState event loop into actor shards

**Files:**
- Modify: `internal/gateway/transport/gnet/conn.go`
- Modify: `internal/gateway/transport/gnet/group.go`
- Modify: `internal/gateway/transport/gnet/actor.go`
- Test: `internal/gateway/transport/gnet/tcp_listener_test.go`
- Test: `internal/gateway/transport/gnet/ws_listener_test.go`

- [ ] **Step 1: Write failing no-per-connection-goroutine test**

Add a unit test that creates many `connState` values through gnet test helpers and verifies actor pool goroutines are fixed. Prefer unit-level fake gnet conns over real sockets.

Expected assertion shape:

```go
base := runtime.NumGoroutine()
for i := 0; i < 1000; i++ { openFakeConn(i) }
eventuallyNoMoreThan(t, base+actorShardCount+constant)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/transport/gnet -run TestGnetActorRuntimeDoesNotSpawnPerConnectionGoroutine -count=1`

Expected: FAIL because `connState.start()` still launches per-connection goroutines.

- [ ] **Step 3: Add actor ownership to connState**

Add fields to `connState`:

```go
owner     *actorShard
scheduled atomic.Bool
```

Keep the existing `mu`, `queue`, and pending byte accounting while moving processing out of the per-conn goroutine.

- [ ] **Step 4: Replace `start()` with actor assignment**

Remove `func (s *connState) start() { go s.run() }`.

In `engineGroup.OnOpen`, after `newConnState`, assign:

```go
state.owner = g.actors.shardForConn(state.id)
```

Do not start a per-conn goroutine.

- [ ] **Step 5: Change signal to schedule actor**

Replace `wake chan struct{}` signaling with:

```go
func (s *connState) signal() {
    if s.owner != nil {
        s.owner.schedule(s)
    }
}
```

- [ ] **Step 6: Move `run` logic to `processReady`**

Replace `run()` with:

```go
func (s *connState) processReady() bool {
    for {
        event, ok := s.nextEvent()
        if !ok {
            s.scheduled.Store(false)
            return s.hasEventsAndRescheduleNeeded()
        }
        if done := s.handleEvent(event); done {
            s.scheduled.Store(false)
            return false
        }
    }
}
```

The exact helper names can differ, but the behavior must avoid missing events appended between queue-empty and scheduled=false.

- [ ] **Step 7: Make actor shard drain ready conns**

In `actorShard.run`, call `state.processReady()`. If it returns true, reschedule the state.

- [ ] **Step 8: Run transport tests**

Run: `go test ./internal/gateway/transport/gnet -count=1`

Expected: TCP/WebSocket behavior passes and no-per-connection-goroutine test passes.

---

## Task 6: Preserve gnet lifecycle and stop behavior

**Files:**
- Modify: `internal/gateway/transport/gnet/group.go`
- Modify: `internal/gateway/transport/gnet/actor.go`
- Test: `internal/gateway/transport/gnet/tcp_listener_test.go`

- [ ] **Step 1: Write failing actor stop test**

Test that stopping a listener/engine stops actor shards and does not leave goroutines behind.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/transport/gnet -run TestActorPoolStopsWithEngineGroup -count=1`

Expected: FAIL until actor pool lifecycle is wired.

- [ ] **Step 3: Start actor pool with engine cycle**

In `engineGroup.startEngine`, create and start `actorPool` before `gnet.Rotate` begins accepting connections.

- [ ] **Step 4: Stop actor pool after engine stops**

In `engineGroup.stopEngine` or the surrounding stop path, stop actor pool after gnet engine shutdown has completed and close events have been enqueued. The pool should drain already queued close events before returning, or tests should explicitly allow skipped handler callbacks only for connections that never opened.

- [ ] **Step 5: Run lifecycle tests**

Run: `go test ./internal/gateway/transport/gnet -run 'Test.*Stop|Test.*Close|TestActor' -count=1`

Expected: lifecycle tests pass.

---

## Task 7: Remove core writer goroutine path

**Files:**
- Modify: `internal/gateway/core/server.go`
- Delete or rewrite: `internal/gateway/core/server_encode_queue_test.go`
- Delete or rewrite: `internal/gateway/core/server_write_payload_test.go`
- Modify: `internal/gateway/testkit/fake_transport.go`

- [ ] **Step 1: Write failing direct async write test**

Add/adjust a core test that writes a frame and asserts the fake conn receives it without allocating session write queue or starting a writer goroutine.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/core -run TestServerWriteFrameUsesTransportAsyncWriteDirectly -count=1`

Expected: FAIL while core still enqueues and starts writer.

- [ ] **Step 3: Simplify `encodeAndQueue`**

Rename to `encodeAndWrite` or keep the name only temporarily. The implementation should:

```go
encoded, err := state.listener.adapter.Encode(state.session, f, meta)
if err != nil { return err }
s.observeFrameOut(state, f, len(encoded))
return s.writePayloadDirect(state, encoded)
```

No session queue, no direct/queued branch, no `startWriter`.

- [ ] **Step 4: Delete writer helpers**

Remove `canDirectWrite`, `startWriter`, `encodedQueueWithTimeout`, `dequeueEncodedWithTimeout`, `writePayload` timeout wrapper, `writeDeadlineConn`, `isTimeoutError`, and writer idle constants if unused.

- [ ] **Step 5: Update close reason mapping**

Remove mappings for `session.ErrWriteQueueFull` and session outbound queue errors if those errors are deleted. Keep `transport.ErrOutboundBytesExceeded -> CloseReasonOutboundOverflow`.

- [ ] **Step 6: Run core tests**

Run: `go test ./internal/gateway/core -count=1`

Expected: core tests pass after writer-specific tests are removed or rewritten around transport outbound overflow.

---

## Task 8: Simplify session package to metadata only

**Files:**
- Modify: `internal/gateway/session/session.go`
- Modify: `internal/gateway/session/manager_test.go`
- Modify: `internal/gateway/session/session_benchmark_test.go`
- Modify: `internal/gateway/testkit/fake_session.go`

- [ ] **Step 1: Write failing metadata-only compile test**

Update tests to construct `session.Config` without `WriteQueueSize` and `MaxOutboundBytes`.

- [ ] **Step 2: Run tests to verify compile failure**

Run: `go test ./internal/gateway/session -count=1`

Expected: FAIL until session config and implementation are simplified.

- [ ] **Step 3: Remove encoded queue interfaces and fields**

Delete from `session.go`:

- `EncodedQueue`
- `OwnedEncodedQueue`
- `WriteQueueSize`
- `MaxOutboundBytes`
- `writeCh`
- `writerRunning`
- outbound byte accounting
- enqueue/dequeue/release methods
- writer state methods

Keep `Session`, `WriteFrame`, `Close`, `SetValue`, `Value`, and hot value logic.

- [ ] **Step 4: Update session constructor**

`session.Config` should only include ID, listener, addresses, and `WriteFrameFn`.

- [ ] **Step 5: Run session tests**

Run: `go test ./internal/gateway/session -count=1`

Expected: metadata/value/lifecycle tests pass.

---

## Task 9: Update core session construction and tests

**Files:**
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/core/server_benchmark_test.go`
- Modify: `internal/gateway/protocol/jsonrpc/*_test.go`
- Modify: `internal/gateway/testkit/fake_session.go`

- [ ] **Step 1: Remove encoded queue assertion**

Delete this style of check from `onOpen`:

```go
queue, ok := sess.(session.EncodedQueue)
if !ok { ... }
state.queue = queue
```

- [ ] **Step 2: Remove `queue` from `sessionState`**

Delete `queue session.EncodedQueue` from `sessionState`.

- [ ] **Step 3: Update `session.New` config usage**

Remove `WriteQueueSize` and `MaxOutboundBytes` from `session.Config` construction.

- [ ] **Step 4: Rewrite queue/timeout tests**

Delete tests whose only assertion is session queue size, writer restart, writer timeout goroutine, or direct-vs-queued bypass. Replace with transport outbound overflow tests against gnet/testkit if behavior remains required.

- [ ] **Step 5: Run core and protocol tests**

Run: `go test ./internal/gateway/core ./internal/gateway/protocol/jsonrpc ./internal/gateway/protocol/wkproto -count=1`

Expected: tests pass.

---

## Task 10: Remove obsolete gateway session config keys

**Files:**
- Modify: `internal/gateway/types/options.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write failing config test for removed keys**

Adjust tests so default session no longer exposes:

- `ReadBufferSize`
- `WriteQueueSize`
- `WriteTimeout`

If a compatibility test exists for parsing these keys, delete it or replace it with a test that they are ignored only if the config loader intentionally allows unknown keys. Prefer removal from public config.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./cmd/wukongim ./internal/gateway -run 'Config|Options' -count=1`

Expected: FAIL until structs and parser are updated.

- [ ] **Step 3: Update `SessionOptions`**

Remove fields from `internal/gateway/types/options.go`:

```go
ReadBufferSize int
WriteQueueSize int
WriteTimeout time.Duration
```

Keep:

```go
MaxInboundBytes int
MaxOutboundBytes int
IdleTimeout time.Duration
AsyncSendDispatchWorkers int
AsyncSendBatchMaxWait time.Duration
AsyncSendBatchMaxRecords int
AsyncSendBatchMaxBytes int
CloseOnHandlerError *bool
```

- [ ] **Step 4: Update config loader**

Remove parsing and assignment of:

- `WK_GATEWAY_DEFAULT_SESSION_READ_BUFFER_SIZE`
- `WK_GATEWAY_DEFAULT_SESSION_WRITE_QUEUE_SIZE`
- `WK_GATEWAY_DEFAULT_SESSION_WRITE_TIMEOUT`

- [ ] **Step 5: Update example config**

Remove those keys from `wukongim.conf.example` and keep comments for max inbound/outbound and gnet caps.

- [ ] **Step 6: Run config tests**

Run: `go test ./cmd/wukongim ./internal/gateway -run 'Config|Options' -count=1`

Expected: config/options tests pass.

---

## Task 11: Preserve outbound overflow through gnet transport

**Files:**
- Modify: `internal/gateway/transport/gnet/conn.go`
- Modify: `internal/gateway/transport/gnet/conn_backpressure_test.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Write failing core-to-gnet overflow test**

Add a test that sets `MaxOutboundBytes`, writes a payload that exceeds it, and expects `transport.ErrOutboundBytesExceeded` to map to `CloseReasonOutboundOverflow`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/core ./internal/gateway/transport/gnet -run 'OutboundOverflow|Backpressure' -count=1`

Expected: FAIL until old session outbound errors are removed and gnet accounting is the only source.

- [ ] **Step 3: Keep gnet outbound accounting authoritative**

Ensure `stateConn.Write`, `writeWebSocketCompact`, and `writeWebSocketVector` enforce `maxOutboundBytes` and return `transport.ErrOutboundBytesExceeded`.

- [ ] **Step 4: Run overflow tests**

Run: `go test ./internal/gateway/core ./internal/gateway/transport/gnet -run 'OutboundOverflow|Backpressure' -count=1`

Expected: tests pass.

---

## Task 12: Update actor-safe close behavior

**Files:**
- Modify: `internal/gateway/transport/gnet/conn.go`
- Modify: `internal/gateway/transport/gnet/group.go`
- Modify: `internal/gateway/transport/gnet/*_test.go`

- [ ] **Step 1: Write failing close-once test under actor runtime**

Test concurrent close sources:

- gnet `OnClose`
- inbound protocol failure
- outbound overflow
- listener stop

Expected `ConnHandler.OnClose` at most once.

- [ ] **Step 2: Run test to verify it fails if close is not guarded**

Run: `go test ./internal/gateway/transport/gnet -run TestActorRuntimeCloseFiresOnce -count=1`

Expected: FAIL until close state is actor-safe.

- [ ] **Step 3: Add close guard**

Use existing `closing` plus actor ownership to ensure only one close event is queued for handler notification. Keep `notifyClose` semantics for connections that successfully opened.

- [ ] **Step 4: Run close tests with race detector**

Run: `go test -race ./internal/gateway/transport/gnet -run 'Close|Stop|Actor' -count=1`

Expected: pass without races.

---

## Task 13: Update docs and project structure

**Files:**
- Modify: `internal/gateway/FLOW.md`
- Modify: `AGENTS.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` if a durable rule is discovered
- Modify: `docs/development/CODE_QUALITY.md` only for unrelated issues found during work

- [ ] **Step 1: Update FLOW**

Document:

- gnet is the only built-in gateway transport.
- gnet uses actor shards instead of per-connection goroutines.
- core writes directly to transport-managed async writes.
- session no longer owns encoded outbound queues.

- [ ] **Step 2: Update AGENTS directory tree**

Remove `internal/gateway/transport/stdnet` from the directory structure if present. Add actor runtime note under `internal/gateway/transport/gnet` if useful.

- [ ] **Step 3: Run documentation search guard**

Run: `rg -n 'stdnet|WriteQueueSize|WriteTimeout|EncodedQueue|startWriter' AGENTS.md internal/gateway docs/development wukongim.conf.example cmd/wukongim/config.go`

Expected: no stale active docs or production references. Historical `docs/superpowers` may still contain old specs/plans.

---

## Task 14: Full verification and benchmarks

**Files:**
- No production file changes unless verification reveals issues.

- [ ] **Step 1: Run focused tests**

Run:

```bash
go test -count=1 ./internal/gateway/...
go test -count=1 ./cmd/wukongim
```

Expected: pass.

- [ ] **Step 2: Run race tests**

Run:

```bash
go test -race -count=1 ./internal/gateway/core ./internal/gateway/transport/gnet ./internal/gateway/session
```

Expected: pass without races.

- [ ] **Step 3: Run benchmark set**

Run:

```bash
go test ./internal/gateway/core ./internal/gateway/transport/gnet -run '^$' \
  -bench 'Benchmark(ServerSendDispatch|ServerEncodeAndQueue|Gnet|Actor|HotPath)' \
  -benchmem -benchtime=1s -count=5
```

Expected: no benchmark hangs; compare against pre-refactor baseline with `benchstat` if available.

- [ ] **Step 4: Run goroutine regression tests**

Run:

```bash
go test ./internal/gateway/transport/gnet -run 'Goroutine|ActorRuntimeDoesNotSpawnPerConnection' -count=1
```

Expected: actor goroutine count is bounded by actor shard count plus fixed overhead.

- [ ] **Step 5: Run broader unit tests if time permits**

Run: `go test ./...`

Expected: pass, unless unrelated existing changes outside gateway fail. If unrelated failures appear, record them and keep gateway evidence separate.
