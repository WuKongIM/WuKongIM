# internalv2 Plugin PersistAfter Phase 1 Design

- Date: 2026-06-22
- Scope: migrate the minimum plugin subsystem needed for real `.wkp`
  `PersistAfter` delivery in `internalv2`.
- Decision: build a v2 plugin usecase around v2 committed events, reuse the
  existing PDK-compatible runtime and wire protocol, and trigger hooks from the
  channel authority post-commit path.

## Context

The legacy `internal` stack already has a PDK-compatible plugin subsystem:

- node-local `.wkp` process management and Unix-socket `wkrpc`
- plugin manifest/config persistence
- host RPCs such as `/plugin/start`, `/message/send`, and
  `/channel/messages`
- hooks for `Send`, `PersistAfter`, `Receive`, `Route`, and `ConfigUpdate`
- owner-routed `PersistAfter` invocation for committed messages

`internalv2` currently has no plugin subsystem. Its send path is centered on
`internalv2/runtime/channelappend`: the channel authority node admits sends,
appends durable messages, returns `SENDACK` after append success, then runs
post-commit delivery and conversation effects.

Phase 1 must migrate plugin capability carefully without importing the old
message usecase or old committed-event model into v2.

## Goals

1. Start real `.wkp` plugins in the v2 app when plugin config is enabled.
2. Preserve the existing PDK wire protocol for `PersistAfter`.
3. Invoke `PersistAfter` for v2 durable committed messages after successful
   channel append.
4. Keep plugin failures out of the `SENDACK`, append, delivery, and
   conversation success path.
5. Keep the default plugin config disabled.
6. Add enough tests to prove a real `.wkp` can receive a v2 `PersistAfter`
   event.

## Non-Goals

Phase 1 does not migrate:

- `Send` hooks
- `Receive` hooks
- plugin HTTP `Route`
- `ConfigUpdate`
- manager plugin APIs
- plugin user bindings
- plugin-origin `/message/send`
- stream APIs
- committed replay side effects
- the legacy plugin committed owner RPC

These capabilities should be migrated in later phases after the
`PersistAfter` runtime boundary is proven.

## Architecture

`channelappend` remains the durable commit source of truth. The plugin path is a
post-commit side effect:

```text
Gateway/API/Node ingress
  -> internalv2 message usecase
  -> channelappend authority group
  -> durable append success
  -> delivery/conversation post-commit effects
  -> plugin PersistAfter enqueue
  -> bounded plugin worker
  -> PDK-compatible .wkp PersistAfter hook
```

The v2 code should not directly reuse `internal/usecase/plugin`. That package is
coupled to the legacy message usecase, legacy committed event type, and
`pkg/channel.Message` mapping. Phase 1 creates a v2 plugin usecase with v2
contracts and ports.

The existing `internal/runtime/plugin` package can be reused because it owns
node-local runtime mechanics only: scanning `.wkp` files, launching processes,
maintaining a registry, exposing the Unix socket server, and invoking byte
RPCs. The v2 usecase may also reuse `internal/usecase/plugin/pluginproto` to
preserve PDK compatibility.

If a later cleanup wants stricter directory symmetry, the runtime and proto
compatibility pieces can be moved to a shared package. That move is not needed
for Phase 1.

## Components

### `internalv2/app.PluginConfig`

Add plugin config to the v2 app:

- `Enable`
- `Dir`
- `SocketPath`
- `SandboxDir`
- `StateDir`
- `Timeout`
- `HotReload`
- `FailOpen`
- `PersistAfterQueueSize`
- `PersistAfterWorkers`

Defaults:

- disabled by default
- `Dir = ${WK_NODE_DATA_DIR}/plugins`
- `SocketPath = ${WK_NODE_DATA_DIR}/run/plugin.sock`
- `SandboxDir = ${WK_NODE_DATA_DIR}/plugin-sandbox`
- `StateDir = ${WK_NODE_DATA_DIR}/plugin-state`
- `Timeout = 5s`
- `HotReload = true`
- `FailOpen` is retained for later `Send` hook compatibility and does not
  change Phase 1 `PersistAfter` behavior
- bounded queue and worker defaults sized for side effects, not the main send
  path

Config parsing must keep `WK_` key conventions and update
`wukongim.conf.example` with English comments.

### `internalv2/contracts/pluginevents`

Introduce a small v2 event type for plugin hooks:

```text
PersistAfterCommitted
  MessageID
  MessageSeq
  ChannelID
  ChannelType
  FromUID
  SenderNodeID
  SenderSessionID
  ClientMsgNo
  ServerTimestampMS
  Payload
  RedDot
  SyncOnce
  MessageScopedUIDs
```

The event is copied from `channelappend.CommittedEnvelope`. It must clone
payload and scoped UID slices before crossing the channelappend boundary.

### `internalv2/usecase/plugin`

Create a v2 plugin usecase with Phase 1 methods only:

- load node-local desired plugin state
- read observed plugins from the runtime registry
- select running plugins that advertise `PersistAfter`
- order candidates by priority descending
- map `PersistAfterCommitted` into `pluginproto.MessageBatch`
- invoke each candidate through the runtime invoker
- log and return joined hook errors to the worker, not to the send path

The mapping should fill the PDK fields already supported by the legacy mapper:

- `MessageId`
- `MessageSeq`
- `ClientMsgNo`
- `Timestamp`
- `From`
- `ChannelId`
- `ChannelType`
- `Payload`

`Timestamp` should be derived from `ServerTimestampMS` using the same effective
seconds semantics as the legacy plugin message representation.

### `internalv2/runtime/pluginhook`

Add a node-local bounded worker for `PersistAfter` side effects.

Responsibilities:

- accept cloned `PersistAfterCommitted` events
- detach from caller cancellation with a bounded hook timeout
- invoke the v2 plugin usecase asynchronously
- bound queued payload memory
- expose queue-full and hook-failure metrics/logs
- stop cleanly during app shutdown

The enqueue path must not wait for plugin completion. If the queue is full, the
worker should use a short bounded wait. If it still cannot enqueue, it drops the
plugin side effect and records an explicit warning/metric.

This preserves the main invariant: plugin availability cannot block durable
append, `SENDACK`, delivery, or conversation projection.

### `internalv2/runtime/channelappend`

Add a narrow post-commit port:

```text
PersistAfterEnqueuer.EnqueuePersistAfter(context.Context, CommittedEnvelope)
```

The writer/effect stage calls this port only after a durable append succeeds.
It does not call the plugin usecase directly.

The port is nil-safe when plugins are disabled.

### `internalv2/app`

Wire the plugin subsystem only when `Config.Plugin.Enable` is true:

- runtime socket server
- invoker
- desired state store
- runtime lifecycle
- v2 plugin usecase
- `PersistAfter` worker
- channelappend `PersistAfterEnqueuer`

Plugin runtime should start before traffic-serving gateway/API components and
stop after ingress has stopped. That prevents new sends from queueing plugin
work after the plugin worker begins shutdown.

## Owner Semantics

Phase 1 should not migrate the legacy plugin committed owner RPC.

In the v2 send path, durable append and post-commit effects already run on the
channel authority node. Triggering `PersistAfter` from channelappend's
authority-local post-commit point preserves the old semantic that
`PersistAfter` runs on the committed owner.

If future v2 replay or external committed-event paths can produce events away
from the channel authority, they should add a v2 owner routing adapter at that
time.

## Hook Semantics

`PersistAfter` fires for durable committed messages only.

It is always fail-open in Phase 1. The existing plugin `FailOpen` option is for
future send-path hooks where plugin failures could reject or mutate a send.

It does not fire for:

- plain `NoPersist`
- request-scoped `NoPersist` realtime delivery
- append failures
- committed replay
- messages accepted only into transient delivery paths

`SyncOnce` durable command messages may still fire `PersistAfter`, matching the
simple durable-commit rule. `Receive` has stricter offline-recipient
eligibility, but `Receive` is outside this phase.

`PersistAfterSync` keeps the existing PDK meaning inside the plugin worker:

- true: call `/plugin/persist_after` and wait for the plugin response within
  the hook timeout
- false: send the legacy one-way `MsgTypePersistAfter` message

It does not make `SENDACK` wait.

## Backpressure And Memory

Large groups, many active channels, and many online users require bounded
plugin side effects.

Rules:

- The committed event crossing into pluginhook must be cloned once.
- The pluginhook queue must be bounded.
- Queue size and worker count must be configurable.
- Queue-full behavior is fail-open with logs and metrics.
- Hook timeout must bound each plugin invocation.
- Plugin failures must not retry indefinitely in Phase 1.

The worker should prefer observability over retries. Retrying plugin side
effects without a durable cursor can duplicate external effects and can retain
payload memory under pressure.

## Observability

Add lightweight metrics or diagnostics events for:

- `PersistAfter` events enqueued
- queue-full drops
- hook invocations
- hook failures
- hook latency
- running plugin count at startup

Logs should include:

- plugin number
- channel id/type
- message id
- message sequence
- queue/drop/failure stage

Payload bytes must not be logged.

## Testing

Focused tests:

1. `channelappend` unit tests:
   - durable append success enqueues one plugin event
   - `NoPersist` realtime path does not enqueue
   - append failure does not enqueue
   - nil enqueuer is a no-op
2. v2 plugin usecase tests:
   - selects only running `PersistAfter` plugins
   - sorts by priority descending
   - maps v2 committed fields into `pluginproto.MessageBatch`
   - sync and async invocation modes call the expected invoker method
   - hook errors are returned to the worker and logged
3. pluginhook tests:
   - caller cancellation is detached
   - hook timeout is applied
   - queue is bounded
   - shutdown cancels workers
4. app wiring tests:
   - plugin disabled wires no plugin runtime or enqueuer
   - plugin enabled wires runtime, worker, and channelappend enqueuer
5. real `.wkp` compatibility test:
   - start a minimal plugin
   - send one v2 durable message
   - assert the plugin receives a `PersistAfter` message

## Benchmark Strategy

Phase 1 must include benchmark coverage before it is treated as complete. Unit
tests prove semantics; benchmarks prove the plugin side effect does not add
unbounded allocation, queue, or latency cost to the hot send path.

Benchmark coverage:

1. Mapping and allocation benchmarks in `internalv2/usecase/plugin`:
   - `PersistAfterCommitted` to `pluginproto.MessageBatch`
   - payload sizes such as 128 B, 1 KiB, and 16 KiB
   - report `allocs/op` and bytes copied per event
2. Event boundary clone benchmarks in `internalv2/contracts/pluginevents`:
   - payload sizes such as 128 B, 1 KiB, and 16 KiB
   - scoped UID counts such as 0, 10, and 1000
   - report the cost of cloning payloads and scoped UID slices before
     asynchronous plugin worker ownership
3. Candidate selection benchmarks in `internalv2/usecase/plugin`:
   - running plugin counts such as 1, 16, 128, and 1024
   - mixed enabled, disabled, non-running, and non-`PersistAfter` plugins
   - priority sorting cost must stay outside the channelappend hot path where
     possible through cached or prefiltered views
4. Pluginhook enqueue benchmarks in `internalv2/runtime/pluginhook`:
   - plugin disabled no-op path
   - enabled enqueue path with available capacity
   - queue-full fail-open path
   - `b.RunParallel` with many producers to model many active channels
5. Pluginhook worker benchmarks in `internalv2/runtime/pluginhook`:
   - async one-way fake invoker
   - sync no-op fake invoker with timeout context creation
   - slow invoker pressure that proves bounded queue behavior
6. Channelappend post-commit benchmarks:
   - plugin disabled overhead compared with the current baseline
   - plugin enabled enqueue-only overhead after durable append
   - no benchmark should require full delivery fanout to measure the plugin
     nil-check/enqueue cost
7. Real `.wkp` compatibility benchmark:
   - start a minimal plugin process through the real runtime and Unix socket
   - send `PersistAfter` payloads through the PDK-compatible path
   - keep it behind the repository's `e2e` build tag, because process startup,
     local ports, and Unix socket IO are slower and noisier than unit benchmarks

Benchmark results must be included in the implementation summary with the exact
command and the important `ns/op`, `B/op`, and `allocs/op` lines. The
benchmarks must not build new tooling on temporary `internal/bench/devsim`
code.

Verification commands:

```sh
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/runtime/channelappend ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/app -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/runtime/channelappend -run '^$' -bench 'Benchmark(PersistAfter|PluginHook|ChannelAppend.*Plugin)' -benchmem -count=5
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=e2e ./test/e2ev2/plugin -run TestPersistAfterPluginReceivesDurableCommittedMessage -count=1
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test -tags=e2e ./test/e2ev2/plugin -run '^$' -bench 'BenchmarkPersistAfterRealWKP' -benchmem -count=3
GOWORK=off GOCACHE=/private/tmp/wukongim-go-build-cache go test ./internalv2/... -count=1
git diff --check
```

## Rollout Order

1. Add v2 plugin config and defaults with tests.
2. Add v2 plugin event contract and mapper tests.
3. Add v2 plugin usecase for `PersistAfter` with mapper and candidate
   benchmarks.
4. Add pluginhook bounded worker with enqueue and worker benchmarks.
5. Add channelappend `PersistAfterEnqueuer` port, tests, and post-commit
   overhead benchmarks.
6. Wire runtime, usecase, worker, and enqueuer in `internalv2/app`.
7. Add real `.wkp` compatibility coverage and a focused opt-in benchmark.
8. Run focused tests, focused benchmarks, then the full `internalv2` unit
   suite.

## Open Follow-Up Phases

Later phases should migrate:

- plugin-origin `/message/send`
- `Send` hook chaining before v2 append
- `Receive` for offline recipients
- plugin HTTP `Route`
- manager plugin inventory and operations
- cluster-authoritative plugin user bindings
- host RPCs for channel reads, cluster config, channel owner lookup, and
  conversation channels

Each phase should keep a narrow acceptance test with a real `.wkp` plugin where
the public PDK contract is involved.
