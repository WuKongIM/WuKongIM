# Internalv2 Plugin Send Hook Design

- Date: 2026-06-22
- Scope: migrate the PDK-compatible `Send` before-hook into `internalv2`.
- Decision: invoke local `Send` plugins in the `internalv2/usecase/message`
  send path after send permission checks and before channel append admission.

## Context

The legacy `internal` stack supports a synchronous PDK `Send` hook at
`/plugin/send`. It selects running local plugins that advertise `Send`, orders
them by priority descending, calls each plugin sequentially, lets plugins mutate
only the message payload, and may reject the send with a protocol reason.

`internalv2` already has:

- node-local plugin process/socket runtime wiring
- PDK-compatible `PersistAfter` invocation
- plugin hook metrics for bounded post-commit workers
- manager read-only plugin inventory APIs
- a message usecase that checks send permission before delegating to
  `channelappend`

This phase adds the missing synchronous before-hook without changing the
durable append authority, NoPersist realtime dispatch, or post-commit
PersistAfter behavior.

## Goals

1. Preserve legacy PDK wire compatibility for `/plugin/send`.
2. Preserve legacy failure semantics:
   - default `fail-closed`
   - `WK_PLUGIN_FAIL_OPEN=true` only fail-opens candidate lookup and hook
     invocation infrastructure errors
   - plugin-returned non-success reasons always reject
3. Keep the hot path cheap when plugins are disabled or no send hook is wired.
4. Keep the hook in the entry-agnostic message usecase instead of gateway or
   channelappend.
5. Add benchmark coverage for plugin selection, invocation mapping, and
   message send hot-path overhead.

## Non-Goals

This phase does not migrate:

- `Receive`
- plugin HTTP `Route`
- `ConfigUpdate`
- plugin config mutation APIs
- plugin bindings
- stream APIs
- owner-routed cross-node hook execution

Plugin-origin `/message/send` recursion controls are prepared in the shared
send command, but full plugin host RPC parity can be completed as a follow-up
if any missing host endpoint behavior is found during implementation.

## Options Considered

### Option A: Gateway-level hook

Invoke plugins directly from `internalv2/access/gateway` before calling the
message usecase.

Rejected. Gateway is an entry adapter and already owns async SEND queueing and
micro-batching. Placing hook logic there would duplicate behavior for HTTP,
node, or future entry points and would violate the `access -> usecase` split.

### Option B: Channelappend-level hook

Invoke plugins inside `internalv2/runtime/channelappend` before durable append.

Rejected. `channelappend` owns authority routing, NoPersist realtime dispatch,
durable append, and post-commit side effects. A PDK before-hook is business
admission/mutation logic and should not be mixed into the authority runtime.

### Option C: Message usecase hook

Invoke plugins in `internalv2/usecase/message` after permission checks and
before the configured submitter.

Selected. This matches the legacy sequencing, keeps entry adapters thin, and
preserves the clean boundary where only accepted and possibly mutated commands
reach `channelappend`.

## Architecture

The send flow becomes:

```text
gateway/API/node entry
  -> internalv2/usecase/message.Send or SendBatch
  -> permission checks
  -> optional SendHook.BeforeSend
  -> channelappend router/authority
  -> NoPersist realtime or durable append
  -> PersistAfter post-commit hook for durable messages
```

The hook port stays narrow:

```text
SendHook.BeforeSend(ctx, SendCommand) (SendCommand, Reason, error)
```

The message usecase should do one nil check when no hook is configured. It must
not query the plugin registry itself.

## Send Command Controls

Add entry-agnostic controls to `internalv2/contracts/channelappend.SendCommand`:

- `Origin`
- `HookDepth`
- `SkipPluginHooks`

`Origin` defaults to client-origin behavior when empty. Plugin-origin sends
increment `HookDepth` before hook invocation and fail with
`ErrSendHookDepthExceeded` after `DefaultPluginSendMaxHookDepth`. Trusted
internal paths can set `SkipPluginHooks` to bypass the hook.

These fields live on the shared command because `internalv2/usecase/message`
aliases the channelappend command type and forwards the same value to the
submitter.

## Plugin Usecase

Extend `internalv2/usecase/plugin` with:

- `PathSend = "/plugin/send"`
- `InvokeSend(ctx, pluginNo, *pluginproto.SendPacket)`
- `SendPluginCandidates(ctx)`
- `BeforeSend(ctx, message.SendCommand)`

`BeforeSend` mirrors legacy behavior:

1. Read running local `Send` candidates ordered by priority descending and
   plugin number ascending as a tie-breaker.
2. Convert the current command into `pluginproto.SendPacket`.
3. Call each plugin synchronously with `RequestPlugin`.
4. Reject on non-success plugin reason.
5. Apply only response payload mutation.
6. Preserve sender, channel, device, session, message id, durability flags, and
   all routing identity fields.

All byte slices crossing the plugin boundary must be cloned.

## Failure Semantics

Candidate lookup or plugin invocation errors:

- `FailOpen=false`: return `ReasonSystemError` and the error.
- `FailOpen=true`: return the original command with `ReasonSuccess`.

Plugin response validation errors:

- out-of-range reason returns `ReasonSystemError`
- malformed response follows invocation error handling

Plugin-declared rejection:

- any non-zero, non-success reason rejects regardless of `FailOpen`
- no later plugin runs after rejection

Empty or nil response payload preserves the current command payload.

## Metrics

Reuse the plugin hook invoke metric family:

```text
wukongim_plugin_hook_invoke_total{method="send",result}
wukongim_plugin_hook_invoke_duration_seconds{method="send",result}
```

Use low-cardinality result labels such as `ok`, `reject`, `error`,
`fail_open`, and `invalid_reason`. Do not label by plugin number.

No enqueue metric is emitted for `Send` because it is synchronous and not
worker-backed.

## App Wiring

`wirePluginSubsystem` already creates `a.plugins` when `Config.Plugin.Enable`
is true. `wireMessages` should set `message.Options.SendHook = a.plugins` when
plugins are enabled and the usecase exists.

`pluginusecase.Options` should accept:

- `FailOpen`
- optional observer for synchronous Send hook invocation metrics

The existing `WK_PLUGIN_FAIL_OPEN` parser and config comments are already
present, so this phase should not add a new config key.

## Testing

Use TDD for each slice.

`internalv2/usecase/plugin`:

- candidates are ordered and filtered by method, enabled flag, and running
  status
- sequential send hooks mutate payload in order
- empty response preserves payload
- non-success reason rejects and stops later plugins
- invocation failure fail-closes by default
- invocation failure fail-opens when configured
- out-of-range reason maps to system error
- request/response payload slices are cloned

`internalv2/usecase/message`:

- nil hook preserves current behavior
- hook runs after permission success and before submitter
- rejected hook result does not call submitter
- batch results remain item-aligned for mixed permission rejection, hook
  rejection, and accepted sends
- plugin-origin recursion depth is enforced
- `SkipPluginHooks` bypasses hook

`internalv2/app`:

- plugin subsystem passes `FailOpen` into the plugin usecase
- message usecase wires the plugin app as SendHook when plugins are enabled
- plugin metrics observer emits `method="send"` invoke samples when metrics are
  enabled

## Benchmarks

Add focused benchmarks:

- `BenchmarkSendPluginCandidates` for 1, 16, 256, and 1024 observed plugins
- `BenchmarkBeforeSend` for no candidates, one plugin, and a small plugin chain
- `BenchmarkMessageSendBatchNoHook` to guard disabled-plugin hot-path overhead
- `BenchmarkMessageSendBatchWithHook` for batch hook cost with a cheap fake hook
- observer-enabled Send hook metric overhead

Benchmarks should avoid real process/socket startup. Use fake runtimes and fake
invokers so results isolate selection, mapping, marshal/unmarshal, and usecase
dispatch cost.

## Performance Notes

The default disabled-plugin path must remain a nil interface check in the
message usecase. Registry scans happen only when the hook is configured and a
send has already passed permission checks. Since large groups and high online
fanout can drive heavy SEND throughput, the implementation should avoid extra
allocations in the no-hook path and clone payloads only when crossing the plugin
boundary.

Sequential plugin invocation preserves legacy semantics but means hook latency
is directly on the SENDACK path. Operators should keep `Send` plugin chains
short and use the existing plugin manager/metrics surfaces to observe hook cost.

## Self-Review

- Layering: selected message usecase hook keeps access and channelappend clean.
- Compatibility: PDK path, payload-only mutation, priority order, and
  fail-open semantics match legacy behavior.
- Performance: no-hook path is intentionally one nil check; plugin registry
  scans and protobuf work are limited to enabled hooks.
- Operability: metrics use existing low-cardinality hook invoke families.
- Risk: adding control fields to the shared SendCommand touches several
  packages; tests should pin clone behavior and import boundaries.
