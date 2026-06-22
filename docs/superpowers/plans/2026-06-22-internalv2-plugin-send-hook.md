# Internalv2 Plugin Send Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PDK-compatible synchronous `Send` before-hook support to `internalv2`.

**Architecture:** The hook is injected in `internalv2/usecase/message` after permission checks and before `channelappend` submission. `internalv2/usecase/plugin` owns PDK packet mapping, candidate selection, fail-open/fail-closed semantics, and low-cardinality hook invocation observations. `channelappend.SendCommand` carries only entry-agnostic hook controls and gateway device metadata needed by the PDK packet.

**Tech Stack:** Go, WuKongIM internalv2 usecases, existing PDK `pluginproto`, Prometheus plugin metrics, `go test` benchmarks.

---

## File Structure

- Modify `internalv2/contracts/channelappend/types.go`: add `DeviceFlag`, `SendOrigin`, `Origin`, `HookDepth`, and `SkipPluginHooks` fields.
- Modify `internalv2/access/gateway/mapper.go`: copy gateway session device flag into `SendCommand`.
- Modify `internalv2/access/node/channel_append_codec.go`: preserve new command fields in node RPC round trips.
- Modify `internalv2/usecase/message/types.go`: re-export `SendOrigin` constants.
- Modify `internalv2/usecase/message/errors.go`: add `ErrSendHookDepthExceeded`.
- Modify `internalv2/usecase/message/ports.go`: add `SendHook` port.
- Modify `internalv2/usecase/message/app.go`: accept `Options.SendHook`.
- Modify `internalv2/usecase/message/send.go`: run hook after permission checks and before submitter delegation.
- Modify `internalv2/usecase/message/send_test.go`: add red tests for hook sequencing, rejection, recursion, skip, and batch alignment.
- Modify `internalv2/usecase/plugin/types.go`: add `FailOpen` and `Observer` options.
- Modify `internalv2/usecase/plugin/app.go`: store the new options.
- Modify `internalv2/usecase/plugin/invocation.go`: add `/plugin/send` request invocation.
- Create `internalv2/usecase/plugin/send_hook.go`: implement `BeforeSend`.
- Create `internalv2/usecase/plugin/send_hook_test.go`: add red tests for legacy-compatible hook behavior.
- Create or modify `internalv2/usecase/plugin/send_hook_benchmark_test.go`: benchmark candidates and hook invocation.
- Modify `internalv2/app/plugin.go`: pass `FailOpen` and observer to plugin usecase.
- Modify `internalv2/app/plugin_observer.go`: add synchronous `Send` hook observer methods.
- Modify `internalv2/app/wiring.go`: wire plugin usecase into message options as `SendHook`.
- Modify `internalv2/app/plugin_test.go` and `internalv2/app/observability_test.go`: prove wiring and metrics.
- Modify `internalv2/usecase/message/FLOW.md`, `internalv2/access/gateway/FLOW.md`, and `internalv2/app/FLOW.md`: document hook placement and device flag propagation.

## Task 1: Plugin Usecase Send Hook

**Files:**
- Create: `internalv2/usecase/plugin/send_hook_test.go`
- Create: `internalv2/usecase/plugin/send_hook.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Modify: `internalv2/usecase/plugin/invocation.go`

- [ ] **Step 1: Write failing tests**

Add tests covering:

```go
func TestBeforeSendRunsCandidatesInPriorityOrderAndMutatesPayload(t *testing.T)
func TestBeforeSendRejectsOnPluginReasonAndStopsChain(t *testing.T)
func TestBeforeSendFailClosedAndFailOpen(t *testing.T)
func TestBeforeSendInvalidReasonReturnsSystemError(t *testing.T)
func TestBeforeSendClonesPayloadAcrossPluginBoundary(t *testing.T)
```

Use a fake runtime with running `MethodSend` plugins and a fake invoker that records `RequestPlugin` calls and returns marshaled `pluginproto.SendPacket` responses.

- [ ] **Step 2: Run red tests**

Run:

```bash
go test ./internalv2/usecase/plugin -run 'TestBeforeSend|TestInvokeSend' -count=1
```

Expected: FAIL because `BeforeSend`, `InvokeSend`, `FailOpen`, and observer plumbing do not exist yet.

- [ ] **Step 3: Implement minimal hook**

Implement:

```go
const PathSend = "/plugin/send"

func (a *App) InvokeSend(ctx context.Context, pluginNo string, packet *pluginproto.SendPacket) (*pluginproto.SendPacket, error)
func (a *App) SendPluginCandidates(ctx context.Context) ([]ObservedPlugin, error)
func (a *App) BeforeSend(ctx context.Context, cmd message.SendCommand) (message.SendCommand, message.Reason, error)
```

Add payload-only mutation, fail-open handling, invalid reason validation, cloned request and response payloads, and low-cardinality observer calls.

- [ ] **Step 4: Run green tests**

Run:

```bash
go test ./internalv2/usecase/plugin -run 'TestBeforeSend|TestInvokeSend' -count=1
```

Expected: PASS.

## Task 2: Message Usecase Hook Integration

**Files:**
- Modify: `internalv2/contracts/channelappend/types.go`
- Modify: `internalv2/usecase/message/types.go`
- Modify: `internalv2/usecase/message/errors.go`
- Modify: `internalv2/usecase/message/ports.go`
- Modify: `internalv2/usecase/message/app.go`
- Modify: `internalv2/usecase/message/send.go`
- Modify: `internalv2/usecase/message/send_test.go`

- [ ] **Step 1: Write failing tests**

Add tests covering:

```go
func TestSendHookRunsAfterPermissionAndBeforeSubmitter(t *testing.T)
func TestSendHookRejectsBeforeSubmitter(t *testing.T)
func TestSendBatchHookResultsRemainItemAligned(t *testing.T)
func TestSendHookPluginOriginDepthGuard(t *testing.T)
func TestSendHookSkipPluginHooksBypassesHook(t *testing.T)
```

Use a fake `SendHook` and existing `recordingSubmitter`/`fakePermissionStore` helpers.

- [ ] **Step 2: Run red tests**

Run:

```bash
go test ./internalv2/usecase/message -run 'TestSendHook|TestSendBatchHook' -count=1
```

Expected: FAIL because `Options.SendHook`, origin controls, and depth guard are missing.

- [ ] **Step 3: Implement minimal integration**

Add:

```go
type SendHook interface {
    BeforeSend(context.Context, SendCommand) (SendCommand, Reason, error)
}
```

In `Send` and `SendBatch`, call a helper after `checkSendPermission`. The helper defaults empty origin to `SendOriginClient`, increments plugin-origin `HookDepth`, rejects after `DefaultPluginSendMaxHookDepth`, bypasses on `SkipPluginHooks`, and returns item-aligned rejection results without calling submitter.

- [ ] **Step 4: Run green tests**

Run:

```bash
go test ./internalv2/usecase/message -run 'TestSendHook|TestSendBatchHook|TestSendBatchFiltersPermissionRejectedItemsAndDelegatesAllowedItems|TestSendDelegatesToSubmitter' -count=1
```

Expected: PASS.

## Task 3: Gateway And Node Command Propagation

**Files:**
- Modify: `internalv2/access/gateway/mapper.go`
- Modify: `internalv2/access/node/channel_append_codec.go`
- Modify: `internalv2/access/node/channel_append_codec_test.go`
- Modify: `internalv2/contracts/channelappend/types_test.go`

- [ ] **Step 1: Write failing tests**

Add or extend tests to prove:

```go
func TestMapSendCommandCarriesDeviceFlag(t *testing.T)
func TestChannelAppendSendCommandCodecPreservesHookControls(t *testing.T)
func TestSendCommandClonePreservesHookControlsAndClonesSlices(t *testing.T)
```

- [ ] **Step 2: Run red tests**

Run:

```bash
go test ./internalv2/access/gateway ./internalv2/access/node ./internalv2/contracts/channelappend -run 'TestMapSendCommand|TestChannelAppendSendCommandCodec|TestSendCommandClone' -count=1
```

Expected: FAIL because `DeviceFlag`, `Origin`, `HookDepth`, and `SkipPluginHooks` are not encoded yet.

- [ ] **Step 3: Implement propagation**

Set `SendCommand.DeviceFlag` from gateway session value. Encode and decode `DeviceFlag`, `Origin`, `HookDepth`, and `SkipPluginHooks` in node channel append codec. Ensure `Clone` preserves scalar fields and still clones payload/scoped UID slices.

- [ ] **Step 4: Run green tests**

Run:

```bash
go test ./internalv2/access/gateway ./internalv2/access/node ./internalv2/contracts/channelappend -run 'TestMapSendCommand|TestChannelAppendSendCommandCodec|TestSendCommandClone' -count=1
```

Expected: PASS.

## Task 4: App Wiring And Metrics

**Files:**
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_observer.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write failing tests**

Add tests covering:

```go
func TestNewWiresPluginUsecaseAsMessageSendHook(t *testing.T)
func TestNewPassesPluginFailOpenToPluginUsecase(t *testing.T)
func TestPluginMetricsObserverTracksSendInvoke(t *testing.T)
```

- [ ] **Step 2: Run red tests**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresPluginUsecaseAsMessageSendHook|TestNewPassesPluginFailOpenToPluginUsecase|TestPluginMetricsObserverTracksSendInvoke' -count=1
```

Expected: FAIL because message hook wiring and send observer methods are not present.

- [ ] **Step 3: Implement wiring**

Pass `FailOpen: a.cfg.Plugin.FailOpen` and `Observer: a.pluginHookObserver()` into `pluginusecase.NewApp`. Add `ObserveSendInvoke` to the app observer and map it to `method="send"`. Set `messageOpts.SendHook = a.plugins` in `wireMessages` when plugins are enabled.

- [ ] **Step 4: Run green tests**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresPluginUsecaseAsMessageSendHook|TestNewPassesPluginFailOpenToPluginUsecase|TestPluginMetricsObserverTracksSendInvoke|TestNewWiresPluginSubsystemWhenEnabled' -count=1
```

Expected: PASS.

## Task 5: Benchmarks

**Files:**
- Create or modify: `internalv2/usecase/plugin/send_hook_benchmark_test.go`
- Create or modify: `internalv2/usecase/message/send_benchmark_test.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Add benchmark tests**

Add:

```go
func BenchmarkSendPluginCandidates(b *testing.B)
func BenchmarkBeforeSend(b *testing.B)
func BenchmarkMessageSendBatchNoHook(b *testing.B)
func BenchmarkMessageSendBatchWithHook(b *testing.B)
func BenchmarkPluginMetricsObserverSendInvoke(b *testing.B)
```

Use fake runtime/invoker/hook objects and `b.ReportAllocs()`.

- [ ] **Step 2: Run benchmarks**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/usecase/message ./internalv2/app -run '^$' -bench 'Benchmark(SendPluginCandidates|BeforeSend|MessageSendBatch|PluginMetricsObserverSendInvoke)' -benchmem
```

Expected: PASS and print benchmark rows.

## Task 6: Flow Docs And Focused Verification

**Files:**
- Modify: `internalv2/usecase/message/FLOW.md`
- Modify: `internalv2/usecase/plugin/FLOW.md` if present or add concise package flow notes if absent
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Update flow docs**

Document that Send hooks run in message usecase after permissions and before append, gateway carries device flag into the command, and app wires the plugin usecase as both Send and PersistAfter hook provider.

- [ ] **Step 2: Run focused verification**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/usecase/message ./internalv2/access/gateway ./internalv2/access/node ./internalv2/contracts/channelappend ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run benchmark verification**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/usecase/message ./internalv2/app -run '^$' -bench 'Benchmark(SendPluginCandidates|BeforeSend|MessageSendBatch|PluginMetricsObserverSendInvoke)' -benchmem
```

Expected: PASS and print benchmark rows.

## Self-Review

- Spec coverage: hook placement, PDK path, fail-open semantics, payload-only mutation, metrics, and benchmarks are each mapped to tasks.
- Placeholder scan: no task uses TBD or open-ended “add tests” without named test cases.
- Type consistency: the hook signature uses `message.SendCommand` and `message.Reason`, while shared command fields live in `channelappend` and are re-exported by `message`.
