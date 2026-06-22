# internalv2 Plugin Receive Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add v2 plugin `Receive` hook support for eligible offline recipients.

**Architecture:** `channelappend` emits plugin-agnostic offline recipient
candidates after presence resolution. `app` adapts those candidates into the
bounded plugin hook worker. `usecase/plugin` owns eligibility, UID binding
selection, dedupe, and PDK `RecvPacket` invocation.

**Tech Stack:** Go, internalv2 usecases/runtime, existing PDK `pluginproto`,
focused `go test` and `go test -bench` verification.

---

### Task 1: Receive Usecase Contract and Tests

**Files:**
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/invocation.go`
- Modify: `internalv2/usecase/plugin/mapping.go`
- Create: `internalv2/usecase/plugin/receive.go`
- Create: `internalv2/usecase/plugin/receive_test.go`
- Modify: `internalv2/contracts/pluginevents/types.go`
- Modify: `internalv2/contracts/pluginevents/types_test.go`

- [ ] **Step 1: Write failing Receive eligibility and mapping tests**

Add tests that call `App.ReceiveOffline` with durable, SyncOnce,
request-scoped, sender-self, empty-sender, and system-sender events. Assert only
the durable offline recipient case invokes one plugin and that the payload is
cloned into `pluginproto.RecvPacket`.

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/contracts/pluginevents -run 'Receive|Clone' -count=1
```

Expected: FAIL because `ReceiveOffline`, `MethodReceive`, and
`pluginevents.ReceiveOffline` do not exist.

- [ ] **Step 2: Implement minimal Receive usecase API**

Add `MethodReceive`, `PathReceive`, `MsgTypeReceive`, `ReceiveOffline`,
`InvokeReceive`, `ReceiveBindingReader`, `SystemUIDChecker`, receive dedupe, and
`messageFromReceiveOffline` mapping.

- [ ] **Step 3: Verify Receive usecase tests pass**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/contracts/pluginevents -run 'Receive|Clone' -count=1
```

Expected: PASS.

### Task 2: Offline Recipient Observer in channelappend

**Files:**
- Modify: `internalv2/runtime/channelappend/delivery.go`
- Modify: `internalv2/runtime/channelappend/contracts.go`
- Modify: `internalv2/runtime/channelappend/delivery_test.go`
- Modify: `internalv2/runtime/channelappend/FLOW.md`

- [ ] **Step 1: Write failing observer classification tests**

Add tests proving `RecipientProcessor` reports only UIDs with no presence
routes, skips `SyncOnce` and `MessageSeq == 0`, and still pushes online routes.

Run:

```bash
go test ./internalv2/runtime/channelappend -run 'RecipientProcessor.*Offline|OfflineRecipient' -count=1
```

Expected: FAIL because no offline observer port exists.

- [ ] **Step 2: Implement observer port and runtime filter**

Add `OfflineRecipientObserver` and `OfflineRecipientEvent`. In
`processRecipientBatch`, build a small online UID set from resolved routes,
notify the observer for recipient UIDs absent from that set, and leave owner
push behavior unchanged.

- [ ] **Step 3: Update channelappend FLOW**

Document that offline observer notifications are emitted after presence
resolution, before owner push, and are skipped for non-durable realtime
envelopes.

- [ ] **Step 4: Verify channelappend tests pass**

Run:

```bash
go test ./internalv2/runtime/channelappend -run 'RecipientProcessor.*Offline|OfflineRecipient|ProcessRecipientBatch' -count=1
```

Expected: PASS.

### Task 3: pluginhook Worker and App Wiring

**Files:**
- Modify: `internalv2/runtime/pluginhook/worker.go`
- Modify: `internalv2/runtime/pluginhook/worker_test.go`
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/plugin_observer.go`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write failing worker and wiring tests**

Add tests proving the worker accepts Receive events, calls
`ReceiveOffline`, observes enqueue/invoke metrics with `method="receive"`, and
app exposes a non-nil channelappend offline observer when plugins are enabled.

Run:

```bash
go test ./internalv2/runtime/pluginhook ./internalv2/app -run 'Receive|Plugin.*Observer|PluginHook' -count=1
```

Expected: FAIL because worker/app wiring handles PersistAfter only.

- [ ] **Step 2: Extend worker and app adapters**

Add a Receive queue path to `pluginhook.Worker`, add
`pluginReceiveObserver`/adapter in app, pass the adapter into channelappend
options, map runtime `Receive` method through usecase/runtime adapters, and
add low-cardinality metrics observer methods.

- [ ] **Step 3: Update app FLOW**

Document plugin Receive worker start/stop ordering and channelappend offline
observer wiring.

- [ ] **Step 4: Verify worker and app tests pass**

Run:

```bash
go test ./internalv2/runtime/pluginhook ./internalv2/app -run 'Receive|Plugin.*Observer|PluginHook' -count=1
```

Expected: PASS.

### Task 4: Benchmarks and Baseline

**Files:**
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Modify: `internalv2/runtime/pluginhook/benchmark_test.go`
- Modify: `internalv2/runtime/channelappend/benchmark_test.go`
- Modify: `docs/development/PLUGIN_BENCHMARK_BASELINE.md`

- [ ] **Step 1: Add Receive benchmark cases**

Add benchmark cases for receive mapping, bound-plugin selection, pluginhook
Receive enqueue/queue-full, and recipient processor offline classification.

- [ ] **Step 2: Run focused benchmarks**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/runtime/channelappend -bench 'Receive|Offline|PluginHook' -benchmem -run '^$'
```

Expected: benchmarks complete without failures and produce allocation baselines.

- [ ] **Step 3: Update benchmark baseline document**

Append the new Receive benchmark command and observed results to
`docs/development/PLUGIN_BENCHMARK_BASELINE.md`.

### Task 5: Final Verification

**Files:**
- Review all modified files.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/runtime/channelappend ./internalv2/app -run 'Receive|Plugin|RecipientProcessor|PersistAfter' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run package tests without run filter**

Run:

```bash
go test ./internalv2/usecase/plugin ./internalv2/runtime/pluginhook ./internalv2/runtime/channelappend ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Inspect git diff**

Run:

```bash
git diff --stat
git diff -- docs/superpowers/specs/2026-06-22-internalv2-plugin-receive-hook-design.md docs/superpowers/plans/2026-06-22-internalv2-plugin-receive-hook.md
```

Expected: only Receive hook migration files and docs changed; unrelated web
working tree changes remain untouched.
