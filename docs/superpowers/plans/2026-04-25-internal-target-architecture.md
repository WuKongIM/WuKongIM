# Internal Target Architecture Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking. This refactor is important: implement sequentially by task, use high-intelligence read-only reviewers after each phase, and never parallelize workers that edit the same `internal/app` files.

**Goal:** Evolve `internal/` toward a cluster-first modular monolith where `app` owns composition/lifecycle, runtime modules own node-local state machines, and committed channel log facts drive delivery/conversation side effects.

**Architecture:** Preserve behavior and single-node-cluster semantics while landing a safer startup/rollback model, a resource stack for build cleanup, cleaner package boundaries, `runtime/channelmeta`, and committed-event contracts. This plan intentionally does not introduce microservices or a generic service container, and it defers the optional `internal/infra` package split until lifecycle/resource ownership and runtime boundaries are stable.

**Tech Stack:** Go 1.23, `context`, `errors.Join`, `sync`, `sync/atomic`, existing `pkg/cluster`, `pkg/channel`, `pkg/slot`, Gin adapters, `testify/require`, `go vet`, `staticcheck`, Go race detector.

---

## Execution Rules

- Start from a clean git state and commit after every task whose verification passes.
- Before every commit, run `git diff --name-only` and stage only files owned by that task; never use broad commands/globs such as `git add internal`, `git add internal/app`, or `git add -p internal/foo/*_test.go`. For new files, run `git add -N <file>` first and then `git add -p <file>`.
- Before editing a package, check for a local `FLOW.md`; for `internal/app` use `internal/FLOW.md` because there is no `internal/app/FLOW.md` currently.
- Keep deployment language as “single-node cluster” or “multi-node cluster”; do not add standalone/single-server semantics or code branches.
- Do not parallelize tasks that touch `internal/app/lifecycle.go`, `internal/app/build.go`, or `internal/app/channelmeta*.go`.
- Use worker subagents only with disjoint write ownership. Every worker must be told it is not alone in the codebase and must not revert other edits.
- Use read-only reviewer subagents after each phase: lifecycle, resource stack, online boundary, channelmeta, committed events, import guards/docs.
- Expensive real cluster harness tests belong behind the `integration` build tag unless a fast component test covers the same regression in normal unit tests.
- Add English comments to new exported structs, interfaces, and important fields.
- If config fields change, update `wukongim.conf.example` in the same task.

## Implementation Phases

| Phase | Tasks | Purpose | Primary Risk Reduced |
|-------|-------|---------|----------------------|
| 0 | 0 | Preflight and branch hygiene | Avoid overwriting user work |
| 1 | 1-4 | Existing bug/static/test baseline | Start from a known, faster, clean baseline |
| 2 | 5-7 | Lifecycle manager and resource stack | Make startup rollback and build cleanup reliable |
| 3 | 8 | Runtime online boundary cleanup | Remove runtime -> gateway coupling early |
| 4 | 9-14 | Channelmeta extraction | Move runtime meta logic out of `app` without access imports |
| 5 | 15-17 | Committed event contracts | Decouple message/delivery side effects |
| 6 | 18-20 | Boundary guards, config/docs, verification | Prevent architectural regression |

---

### Task 0: Preflight And Safety Check

**Files:**
- Read: `AGENTS.md`
- Read: `internal/FLOW.md`
- Read: `docs/superpowers/specs/2026-04-25-internal-target-architecture-design.md`
- Read: `docs/superpowers/plans/2026-04-25-internal-target-architecture.md`

- [ ] **Step 1: Record git state**

Run: `git status --short`

Expected: only files intentionally created by this planning work or a clean tree. If unrelated files are modified, do not touch them.

- [ ] **Step 2: Locate flow docs**

Run: `find internal -maxdepth 4 -name FLOW.md -print`

Expected: at least `internal/FLOW.md`. Read the relevant flow doc before editing each package.

- [ ] **Step 3: Establish normal test baseline**

Run: `go test ./internal/...`

Expected: PASS before refactor work begins. If it fails, stop and diagnose before editing architecture.

- [ ] **Step 4: Capture static baseline without fixing yet**

Run: `gofmt -l $(find internal -name '*.go' -type f)`

Expected before Task 2: may list known formatting drift.

Run: `go vet ./internal/...`

Expected before Task 2: may fail on the copied `atomic.Bool` in `internal/app/lifecycle_test.go`.

Run: `staticcheck ./internal/...`

Expected before Task 2/3: may list existing SA5011/U1000/S1016 issues.

- [ ] **Step 5: Commit nothing**

This task is read-only.

---

### Task 1: Fix Current App Start Rollback Bug

**Files:**
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/FLOW.md`

This is first because the current code has a real cleanup bug: if API or manager startup fails, `conversationProjector` is not stopped even though it was already started.

- [ ] **Step 1: Write failing rollback tests**

Add hook-based tests in `internal/app/lifecycle_test.go`:

```go
func TestStartRollsBackConversationWhenAPIStartFails(t *testing.T) {}
func TestStartRollsBackConversationWhenManagerStartFails(t *testing.T) {}
```

Assertions:

- API failure stops every successfully started component in reverse order.
- Manager failure stops `api`, `gateway`, `conversation_projector`, `presence`, `channelmeta`, and `cluster` when those components were started.
- `stopConversationProjectorFn` is called exactly once.
- `app.started.Load()` is false after rollback.

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./internal/app -run 'TestStartRollsBackConversationWhenAPIStartFails|TestStartRollsBackConversationWhenManagerStartFails' -count=1
```

Expected: FAIL because current rollback misses `stopConversationProjector` in API/manager failure branches.

- [ ] **Step 3: Patch rollback order only**

In `internal/app/lifecycle.go`:

- on API start failure, call `stopConversationProjector()` after `stopGateway()` and before `stopPresence()`;
- on manager start failure, call `stopConversationProjector()` after `stopGateway()` and before `stopPresence()`;
- keep the rest of the startup/stop behavior unchanged.

Do not introduce the lifecycle manager in this task.

- [ ] **Step 4: Update flow docs**

In `internal/FLOW.md`, update the lifecycle rollback section so it says every started component is stopped in reverse order. Use “single-node cluster” wording if deployment shape is mentioned.

- [ ] **Step 5: Run GREEN**

Run:

```bash
go test ./internal/app -run 'TestStartRollsBack|TestStartStopIncludesConversationProjector|TestStartStartsManagerAfterAPIWhenEnabled|TestStop' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git diff --name-only
git add -p internal/app/lifecycle.go internal/app/lifecycle_test.go internal/FLOW.md
git commit -m "fix: rollback conversation projector on app start failure"
```

---

### Task 2: Clean Formatting, Vet, And Production Staticcheck Issues

**Files:**
- Modify: `internal/access/manager/slot_operator_test.go`
- Modify: `internal/usecase/user/token.go`
- Modify: `internal/usecase/user/token_test.go`
- Modify: `internal/gateway/types/session_values.go`
- Modify: `internal/gateway/auth.go`
- Modify: `internal/gateway/wkprotoenc/crypto.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/channelmeta_statechange.go`
- Modify: production files reported by `staticcheck ./internal/...` only when the warning is confirmed still present

Do not use this task as a broad refactor. It only creates a clean static baseline.

- [ ] **Step 1: Apply gofmt to known drift**

Run:

```bash
gofmt -w \
  internal/access/manager/slot_operator_test.go \
  internal/usecase/user/token.go \
  internal/usecase/user/token_test.go \
  internal/gateway/types/session_values.go \
  internal/gateway/auth.go \
  internal/gateway/wkprotoenc/crypto.go
```

Expected: no output.

- [ ] **Step 2: Fix copied atomic helper**

In `internal/app/lifecycle_test.go`, replace any helper that returns `atomic.Bool` by value with direct field mutation after app construction:

```go
app := &App{
    logger:        logger,
    closeRaftDBFn: func() error { return nil },
    closeWKDBFn:   func() error { return nil },
}
app.started.Store(true)
```

Then delete the helper that returns `atomic.Bool` by value.

- [ ] **Step 3: Fix channelmeta nil dereference warning**

In `internal/app/channelmeta_statechange.go`, ensure `watchLocalReplicaStateChanges` checks `s == nil` before deferring on `s.refreshWG`:

```go
func (s *channelMetaSync) watchLocalReplicaStateChanges(ctx context.Context) {
    if s == nil {
        return
    }
    defer s.refreshWG.Done()
    // existing body
}
```

- [ ] **Step 4: Re-run staticcheck and remove only confirmed production dead code**

Run: `staticcheck ./internal/...`

For each production U1000/S1016 warning, inspect call sites with `rg` before deleting or simplifying. Do not delete helpers that are only unused because they belong to integration-tagged tests; handle those in Task 3.

Known candidates from the current baseline may include:

- `internal/app/build.go`: an unused `channelLogConversationFacts.loadLatestMessage` method.
- `internal/app/messagerouting.go`: unused message routing helpers.
- `internal/log/zap.go`: an unused `logPath` helper.
- `internal/usecase/presence/app.go` or `deps.go`: unused authority helper types.
- `internal/usecase/management/overview.go`: S1016 struct literal simplification.

- [ ] **Step 5: Run focused checks**

Run: `gofmt -l $(find internal -name '*.go' -type f)`

Expected: no output.

Run: `go vet ./internal/...`

Expected: PASS.

Run: `staticcheck ./internal/...`

Expected: PASS or only integration-helper U1000 warnings intentionally deferred to Task 3.

- [ ] **Step 6: Run focused tests**

Run:

```bash
go test ./internal/app ./internal/log ./internal/usecase/presence ./internal/usecase/management ./internal/usecase/message ./internal/gateway/... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -p internal/access/manager/slot_operator_test.go internal/usecase/user/token.go internal/usecase/user/token_test.go internal/gateway/types/session_values.go internal/gateway/auth.go internal/gateway/wkprotoenc/crypto.go internal/app/lifecycle_test.go internal/app/channelmeta_statechange.go internal/app/build.go internal/app/messagerouting.go internal/log/zap.go internal/usecase/presence/app.go internal/usecase/presence/deps.go internal/usecase/message/deps.go internal/usecase/management/overview.go
git commit -m "chore: clean internal static quality gate"
```

---

### Task 3: Move Slow Real-Cluster App Tests Behind Integration Tag

**Files:**
- Modify: `internal/app/pending_checkpoint_test.go`
- Create: `internal/app/pending_checkpoint_integration_test.go` if needed
- Modify: `internal/app/comm_test.go`
- Create: `internal/app/comm_integration_test.go` if needed
- Modify: `internal/app/send_stress_test.go`
- Create: `internal/app/send_stress_integration_test.go` if needed
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Identify real cluster tests and helpers**

Run:

```bash
rg -n 'func Test.*ThreeNode|newThreeNodeAppHarness\(|WK_SEND_STRESS|SendStress.*ThreeNode' internal/app --glob '*_test.go'
```

Expected: list pending checkpoint tests, selected comm harness tests, and env-gated send stress tests.

- [ ] **Step 2: Move pending checkpoint harness tests**

Move real three-node tests from `internal/app/pending_checkpoint_test.go` to `internal/app/pending_checkpoint_integration_test.go` with:

```go
//go:build integration
// +build integration

package app
```

Move file-local helpers with them if normal unit tests no longer use those helpers.

- [ ] **Step 3: Move comm harness real-cluster tests**

Move these tests from `internal/app/comm_test.go` to `internal/app/comm_integration_test.go` if still present:

- `TestThreeNodeAppHarnessUsesSendPathTuning`
- `TestThreeNodeAppHarnessRestartNodeRestartsClusterTransportOnConfiguredAddr`
- `TestThreeNodeAppHarnessRestartedFormerLeaderAcceptsClusterDialsAfterLeaderChange`

Keep fast unit helpers untagged only when untagged tests still use them.

- [ ] **Step 4: Split send stress carefully**

Keep fast config/parser/statistics tests in `internal/app/send_stress_test.go`. Refactor config validation so invalid-env cases are tested through a pure helper that returns an error instead of shelling out to `go test ./internal/app`. Add or rename the fast regression as:

```go
func TestLoadSendStressConfigRejectsInvalidEnvWithoutSubprocess(t *testing.T) {}
```

If staticcheck reports helpers used only by expensive env-gated real-cluster tests, move only those helpers and these tests to `internal/app/send_stress_integration_test.go` with the `integration` build tag:

- `TestSendStressThreeNode`
- `TestSendStressSingleHotChannelThreeNode`
- `TestSendStressHotColdSkewThreeNode`

- [ ] **Step 5: Update flow docs**

In `internal/FLOW.md`, document:

```bash
go test ./internal/app
go test -tags=integration ./internal/app
```

Use “single-node cluster” and “multi-node cluster” wording.

- [ ] **Step 6: Run tests and staticcheck**

Run: `go test -count=1 ./internal/app`

Expected: PASS and materially faster than the previous ~70s normal app package run.

Run:

```bash
go test -tags=integration -count=1 ./internal/app -run 'TestThreeNodeAppSendAckReturnsBeforeLeaderCheckpointDurability|TestAppSingleNodeClusterStartsAndServesGateway'
```

Expected: PASS. If exact names differ, choose one pending checkpoint three-node test and one single-node cluster startup smoke.

Run: `staticcheck ./internal/...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -N internal/app/pending_checkpoint_integration_test.go internal/app/comm_integration_test.go internal/app/send_stress_integration_test.go
git add -p internal/app/pending_checkpoint_test.go internal/app/pending_checkpoint_integration_test.go internal/app/comm_test.go internal/app/comm_integration_test.go internal/app/send_stress_test.go internal/app/send_stress_integration_test.go internal/FLOW.md
git commit -m "test: move slow app harness tests behind integration tag"
```

---

### Task 4: Move Gateway Real-Network Tests Behind Integration Tag

**Files:**
- Modify: `internal/access/gateway/integration_test.go`
- Modify/Create: `internal/access/gateway/integration_fast_test.go` only if fast adapter coverage is missing
- Modify: `internal/gateway/transport/gnet/tcp_listener_test.go`
- Modify: `internal/gateway/transport/gnet/ws_listener_test.go`
- Create: `internal/gateway/transport/gnet/tcp_listener_integration_test.go` if splitting is needed
- Create: `internal/gateway/transport/gnet/ws_listener_integration_test.go` if splitting is needed
- Modify: `internal/FLOW.md`

This task is independent from app lifecycle work. It should only move real TCP/WebSocket/gnet engine tests out of the quick unit lane while preserving handler/core unit coverage.

- [ ] **Step 1: Identify real network tests**

Run:

```bash
rg -n 'func Test.*(WKProto|Ping|Pong|Timeout|Stop|TCP|WebSocket|WS|Listener|Engine|Group)' internal/access/gateway internal/gateway/transport/gnet --glob '*_test.go'
```

Expected: lists access gateway real TCP protocol tests and gnet listener/engine lifecycle tests.

- [ ] **Step 2: Protect fast adapter coverage**

Before moving any access gateway test, confirm `internal/access/gateway/handler_test.go` still covers frame-to-usecase mapping, sendack mapping, recvack mapping, encryption handling, context propagation at the handler level, and timeout/cancel behavior without a real listener.

If a real-network test is the only coverage for a mapping rule, add a fast handler/core test first.

- [ ] **Step 3: Move access gateway real listener tests**

Add the `integration` build tag to `internal/access/gateway/integration_test.go`, or split it if the file contains fast tests that should remain untagged. Candidate tests include:

- `TestGatewayWKProtoHandlerAcknowledgesDurablePersonSend`
- `TestGatewayWKProtoHandlerRepliesPongToPing`
- `TestGatewayVersion5ClientGetsUpgradeRequiredOnSend`
- `TestGatewayWKProtoHandlerPropagatesRequestContextToUsecase`
- `TestGatewayWKProtoHandlerCancelsInFlightSendOnTimeout`
- `TestGatewayWKProtoHandlerCancelsInFlightSendOnGatewayStop`

- [ ] **Step 4: Move gnet listener/engine lifecycle tests**

Move real engine, socket, and WebSocket handshake tests from `internal/gateway/transport/gnet` into integration-tagged files. Keep pure factory/options/registry tests in the normal unit lane. Candidate tests include:

- `TestWSHandlesPingPongAndCloseFrames`
- `TestWSListenersShareOneGroupAndRouteByAddress`
- `TestTCPStopOneLogicalListenerKeepsOtherConnectionsAlive`
- `TestTCPListenersShareOneEngineAndRemainIndependentlyAddressable`
- `TestTCPAndWebSocketListenersShareOneEngineGroup`
- `TestTCPLastStopShutsSharedGroupDown`

- [ ] **Step 5: Update flow docs**

In `internal/FLOW.md`, document that real listener/gnet engine tests run with:

```bash
go test -tags=integration ./internal/access/gateway ./internal/gateway/transport/gnet
```

- [ ] **Step 6: Run quick and integration lanes**

Run:

```bash
go test -count=1 ./internal/access/gateway ./internal/gateway/transport/gnet
```

Expected: PASS and faster than the previous real-network package baseline.

Run:

```bash
go test -tags=integration -count=1 ./internal/access/gateway ./internal/gateway/transport/gnet
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -N internal/access/gateway/integration_fast_test.go internal/gateway/transport/gnet/tcp_listener_integration_test.go internal/gateway/transport/gnet/ws_listener_integration_test.go
git add -p internal/access/gateway/integration_test.go internal/access/gateway/integration_fast_test.go internal/gateway/transport/gnet/tcp_listener_test.go internal/gateway/transport/gnet/ws_listener_test.go internal/gateway/transport/gnet/tcp_listener_integration_test.go internal/gateway/transport/gnet/ws_listener_integration_test.go internal/FLOW.md
git commit -m "test: move gateway network tests behind integration tag"
```

### Task 5: Add Lifecycle Manager Primitive

**Files:**
- Create: `internal/app/lifecycle/component.go`
- Create: `internal/app/lifecycle/manager.go`
- Create: `internal/app/lifecycle/manager_test.go`
- Modify: `AGENTS.md`

Do not wire `App.Start` or `App.Stop` in this task.

- [ ] **Step 1: Write manager tests**

Create `internal/app/lifecycle/manager_test.go` with tests:

```go
func TestManagerStartsInOrderAndStopsReverse(t *testing.T) {}
func TestManagerRollsBackOnlyStartedComponentsOnStartFailure(t *testing.T) {}
func TestManagerRollbackAggregatesStopErrors(t *testing.T) {}
func TestManagerStopAggregatesErrorsInReverseOrder(t *testing.T) {}
func TestManagerStopIsIdempotent(t *testing.T) {}
func TestManagerStopAfterFailedStartDoesNotDoubleStop(t *testing.T) {}
func TestManagerStopPassesContextToComponents(t *testing.T) {}
```

Use a fake component with `name`, `startErr`, `stopErr`, and a shared `calls []string` recorder.

- [ ] **Step 2: Run RED**

Run: `go test ./internal/app/lifecycle -run TestManager -count=1`

Expected: FAIL because the package does not exist or tests do not compile.

- [ ] **Step 3: Implement component interface**

Create `internal/app/lifecycle/component.go`:

```go
package lifecycle

import "context"

// Component is a startable/stoppable app runtime unit managed by Manager.
type Component interface {
    Name() string
    Start(context.Context) error
    Stop(context.Context) error
}
```

- [ ] **Step 4: Implement sequential manager**

Create `internal/app/lifecycle/manager.go`.

Required behavior:

- `Start` starts registered components in order.
- If component N fails, rollback stops only components `N-1..0` in reverse order.
- Start failure returns `errors.Join(startErr, rollbackErrs...)`.
- `Stop` stops currently started components in reverse order.
- `Stop` passes the caller context to every component stop.
- `Stop` is idempotent and safe after failed `Start`.
- Protect state with a mutex; do not invent a full DI container.

- [ ] **Step 5: Update directory documentation**

Update `AGENTS.md` directory structure to include `internal/app/lifecycle` and its lifecycle manager/resource stack responsibility.

- [ ] **Step 6: Run GREEN**

Run: `go test ./internal/app/lifecycle -run TestManager -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -N internal/app/lifecycle/component.go internal/app/lifecycle/manager.go internal/app/lifecycle/manager_test.go
git add -p internal/app/lifecycle/component.go internal/app/lifecycle/manager.go internal/app/lifecycle/manager_test.go AGENTS.md
git commit -m "feat: add app lifecycle manager"
```

---

### Task 6: Wire App Start And Stop Through Lifecycle Manager

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/lifecycle.go`
- Create/Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Write compatibility tests**

Add tests in `internal/app/lifecycle_test.go`:

```go
func TestAppLifecycleUsesDeclaredComponentOrder(t *testing.T) {}
func TestAppLifecycleWaitManagedSlotsReadyRollsBackCluster(t *testing.T) {}
func TestAppLifecycleStopAggregatesComponentErrors(t *testing.T) {}
func TestAppLifecycleStopAfterPartialStartDoesNotDoubleStop(t *testing.T) {}
func TestAppLifecycleStopUsesBoundedContextForContextAwareComponents(t *testing.T) {}
```

Use existing test hooks such as `startClusterFn`, `stopClusterFn`, `startConversationProjectorFn`, and `stopConversationProjectorFn` so tests do not start real servers.

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./internal/app -run 'TestAppLifecycleUsesDeclaredComponentOrder|TestAppLifecycleWaitManagedSlotsReadyRollsBackCluster|TestAppLifecycleStopAggregatesComponentErrors|TestAppLifecycleStopAfterPartialStartDoesNotDoubleStop' -count=1
```

Expected: FAIL until manager wiring exists.

- [ ] **Step 3: Add app component wrappers**

Add wrappers in `internal/app/lifecycle_components.go` or `internal/app/lifecycle.go` that adapt existing methods to `lifecycle.Component`.

Declared startup order must be:

```text
cluster -> managed_slots_ready -> channelmeta -> presence -> conversation_projector -> gateway -> api -> manager
```

`managed_slots_ready` is a start-only gate that calls `waitForManagedSlotsReady` after cluster start and before channelmeta start.

- [ ] **Step 4: Convert `App.Start`**

Modify `internal/app/lifecycle.go` so `App.Start`:

- keeps existing lifecycle mutex and `started`/`stopped` semantics;
- validates the app is built;
- builds the component list dynamically based on enabled dependencies/hooks;
- starts through the lifecycle manager;
- rolls back through the manager on failure;
- preserves existing atomics (`clusterOn`, `gatewayOn`, `apiOn`, etc.) by setting them in wrappers only after successful start.

- [ ] **Step 5: Convert `App.Stop`**

Modify `App.Stop` so it creates a bounded stop context for context-aware components and component stop order is the reverse of the declared startup order:

```text
manager -> api -> gateway -> conversation_projector -> presence -> channelmeta -> managed_slots_ready -> cluster
```

Preserve the current app stop semantic for channelmeta: call `channelMetaSync.StopWithoutCleanup`, not cleanup-oriented `Stop`, because cluster/channel runtime shutdown owns local runtime cleanup.

API and manager wrappers should use the manager stop context instead of creating unrelated background contexts. Non-context component APIs may keep their existing stop calls; do not add goroutine-based forced cancellation in this task.

Storage/logger resource closing remains in existing `close*Fn` hooks in this task.

- [ ] **Step 6: Update flow docs**

Update `internal/FLOW.md` lifecycle section with the manager order, rollback behavior, and `StopWithoutCleanup` rationale.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/app -run 'TestStart|TestStop|TestAppLifecycle' -count=1`

Expected: PASS.

Run: `go test ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git diff --name-only
git add -N internal/app/lifecycle_components.go
git add -p internal/app/app.go internal/app/lifecycle.go internal/app/lifecycle_components.go internal/app/lifecycle_test.go internal/FLOW.md
git commit -m "refactor: wire app lifecycle through component manager"
```

---

### Task 7: Add Resource Stack And Build Failure Cleanup

**Files:**
- Create: `internal/app/lifecycle/resource_stack.go`
- Create: `internal/app/lifecycle/resource_stack_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/lifecycle.go` only if `App.Stop` needs stack ownership

- [ ] **Step 1: Write resource stack tests**

Create tests in `internal/app/lifecycle/resource_stack_test.go`:

```go
func TestResourceStackClosesInReverseOrder(t *testing.T) {}
func TestResourceStackAggregatesCloseErrorsWithNames(t *testing.T) {}
func TestResourceStackReleaseTransfersOwnership(t *testing.T) {}
func TestResourceStackCloseIsIdempotent(t *testing.T) {}
```

- [ ] **Step 2: Run RED**

Run: `go test ./internal/app/lifecycle -run TestResourceStack -count=1`

Expected: FAIL until the stack exists.

- [ ] **Step 3: Implement resource stack**

Create `internal/app/lifecycle/resource_stack.go` with:

```go
// ResourceStack closes resources in reverse construction order unless ownership is released.
type ResourceStack struct { /* mutex, closers, closed, released */ }

func (s *ResourceStack) Push(name string, closeFn func() error)
func (s *ResourceStack) Close() error
func (s *ResourceStack) Release()
```

`Close` must use reverse order and aggregate errors with `errors.Join`. `Release` prevents deferred build-failure close after the constructed `App` owns resources.

- [ ] **Step 4: Write build cleanup regression test**

In `internal/app/build_test.go`, add a regression that forces a late build failure after data-plane/channel runtime resources are created.

Preferred pattern:

```go
func TestNewClosesChannelRuntimeResourcesWhenGatewayBuildFails(t *testing.T) {}
```

Use an existing duplicate listener or test-only injection point. Verify cleanup by reopening stores, rebinding transports, or observing fake close calls. Avoid sleeping or real long-running harnesses.

- [ ] **Step 5: Wire stack into build**

In `internal/app/build.go`:

- create a stack near the top of `build`;
- immediately push a closer after each resource is successfully created;
- include logger sync, metadb, raft log DB, channel log DB, data-plane client, data-plane pool, channel transport, ISR runtime, and `appChannelCluster` when applicable;
- on build failure, join the build error with `stack.Close()`;
- on build success, call `stack.Release()` only after `App` owns all resources.

Avoid double-close by transferring ownership explicitly or by removing lower-level closers once a higher-level close owns them. Do not rely on double-close unless the close method is proven idempotent in tests.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/app/lifecycle -run TestResourceStack -count=1`

Expected: PASS.

Run: `go test ./internal/app -run 'TestNewCloses|TestBuild|TestAccessorsExposeBuiltRuntime' -count=1`

Expected: PASS.

Run: `go test ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -N internal/app/lifecycle/resource_stack.go internal/app/lifecycle/resource_stack_test.go
git add -p internal/app/lifecycle/resource_stack.go internal/app/lifecycle/resource_stack_test.go internal/app/build.go internal/app/build_test.go internal/app/app.go internal/app/lifecycle.go
git commit -m "fix: clean app build resources with resource stack"
```

---

### Task 8: Decouple Runtime Online From Gateway Session Package

**Files:**
- Modify: `internal/runtime/online/types.go`
- Modify: `internal/runtime/online/registry.go`
- Modify: `internal/runtime/online/delivery.go`
- Modify: `internal/runtime/online/registry_test.go`
- Modify: `internal/runtime/online/delivery_test.go`
- Create: `internal/access/gateway/online_session_adapter.go`
- Modify: `internal/access/gateway/handler.go` or gateway registration file that builds `online.OnlineConn`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/app/deliveryrouting.go` if compile failures require call-site changes

- [ ] **Step 1: Confirm current coupling**

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/online | rg 'internal/gateway'
```

Expected before change: finds `internal/gateway/session`.

- [ ] **Step 2: Define runtime-local writer interface**

In `internal/runtime/online/types.go`, remove `internal/gateway/session` import and define the minimal interface actually used by online runtime. Do not assume `internal/gateway/session.Session` satisfies this interface: its `WriteFrame` method has variadic write options, so it needs an adapter.

```go
// SessionWriter is the minimal connection writer required by the online runtime.
type SessionWriter interface {
    WriteFrame(frame.Frame) error
    Close() error
    ID() uint64
    Listener() string
    RemoteAddr() string
    LocalAddr() string
    SetValue(key string, value any)
    Value(key string) any
}

type Session = SessionWriter
```

- [ ] **Step 3: Update nil/session helpers**

Update helpers such as `isNilSession` to accept `any` or `SessionWriter` without importing gateway packages.

- [ ] **Step 4: Add gateway boundary adapter and update call sites**

Create an adapter in `internal/access/gateway` that wraps `gatewaysession.Session` and implements `online.SessionWriter`:

```go
type onlineSessionAdapter struct {
    session gatewaysession.Session
}

func (a onlineSessionAdapter) WriteFrame(f frame.Frame) error {
    return a.session.WriteFrame(f)
}
```

Forward the metadata/value methods to the wrapped session. Use this adapter only at the gateway/access boundary when building `online.OnlineConn`; do not import gateway packages from `internal/runtime/online`.

Update online runtime fake sessions to implement `SessionWriter`. Add a fast test that a wrapped gateway session satisfies the runtime writer contract.

- [ ] **Step 5: Run checks**

Run:

```bash
go test ./internal/runtime/online ./internal/access/gateway ./internal/usecase/presence ./internal/usecase/user ./internal/app -run 'Test.*Online|Test.*Presence|Test.*Gateway|Test.*DeliveryRouting' -count=1
```

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/online | rg 'internal/gateway' || true
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git diff --name-only
git add -N internal/access/gateway/online_session_adapter.go
git add -p internal/runtime/online/types.go internal/runtime/online/registry.go internal/runtime/online/delivery.go internal/runtime/online/registry_test.go internal/runtime/online/delivery_test.go internal/access/gateway/online_session_adapter.go internal/access/gateway/handler.go internal/access/gateway/handler_test.go internal/app/deliveryrouting.go
git commit -m "refactor: decouple online runtime from gateway session"
```

---

### Task 9: Create Runtime Channelmeta Shell And Neutral DTOs

**Files:**
- Create: `internal/runtime/channelmeta/FLOW.md`
- Create: `internal/runtime/channelmeta/types.go`
- Create: `internal/runtime/channelmeta/interfaces.go`
- Create: `internal/runtime/channelmeta/repair_types.go`
- Create: `internal/runtime/channelmeta/repair_types_test.go`
- Modify: `internal/app/channelmeta_repair.go` only for conversion functions if needed
- Modify: `internal/access/node/channel_leader_repair_rpc.go` only for conversion functions if needed
- Modify: `internal/access/node/channel_leader_evaluate_rpc.go` only for conversion functions if needed
- Modify: `AGENTS.md`

Do not move channelmeta logic yet. This task exists to prevent `runtime/channelmeta` from importing `internal/access/node` later.

- [ ] **Step 1: Write package flow doc**

Create `internal/runtime/channelmeta/FLOW.md` documenting:

- package responsibility: node-local channel runtime meta resolver/bootstrap/liveness/repair;
- cluster-first semantics, including single-node cluster;
- forbidden dependencies: `internal/access/*`, `internal/gateway/*`, `internal/usecase/*`, `internal/app`;
- temporary migration state: logic still in `internal/app/channelmeta*.go` until Tasks 10-14 complete.

- [ ] **Step 2: Define narrow interfaces**

In `interfaces.go`, define narrow ports needed by moved logic without importing access packages. Example shape:

```go
// MetaSource reads authoritative channel runtime metadata.
type MetaSource interface { /* use existing app method shapes */ }

// LocalRuntime applies channel metadata to node-local channel runtime.
type LocalRuntime interface { /* use existing app method shapes */ }
```

Prefer existing method names to reduce call-site churn.

- [ ] **Step 3: Define neutral repair DTOs**

In `repair_types.go`, define runtime-owned DTOs with English comments:

```go
// LeaderRepairRequest describes a channel leader repair request independent of RPC transport DTOs.
type LeaderRepairRequest struct { /* minimal fields copied from access/node DTOs */ }

type LeaderRepairResult struct { /* repaired bool, meta/report fields */ }
type LeaderEvaluateRequest struct { /* candidate and replica data */ }
type LeaderPromotionReport struct { /* safe promotion report data */ }
```

- [ ] **Step 4: Add conversion tests**

Add tests verifying conversions preserve fields between `internal/access/node` RPC DTOs and `internal/runtime/channelmeta` DTOs. Keep conversion functions at the adapter edge (`access/node` or `app`), not in runtime if that would import access.

- [ ] **Step 5: Update directory documentation**

Update `AGENTS.md` directory structure to include `internal/runtime/channelmeta` and its runtime meta resolver/bootstrap/repair/liveness responsibility.

- [ ] **Step 6: Run tests and import check**

Run: `go test ./internal/runtime/channelmeta ./internal/access/node -run 'Test.*Repair.*Convert|Test.*Evaluate.*Convert|Test.*DTO' -count=1`

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/channelmeta | rg 'internal/(access|gateway|usecase|app)' || true
```

Expected: no output.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -N internal/runtime/channelmeta/FLOW.md internal/runtime/channelmeta/types.go internal/runtime/channelmeta/interfaces.go internal/runtime/channelmeta/repair_types.go internal/runtime/channelmeta/repair_types_test.go
git add -p internal/runtime/channelmeta/FLOW.md internal/runtime/channelmeta/types.go internal/runtime/channelmeta/interfaces.go internal/runtime/channelmeta/repair_types.go internal/runtime/channelmeta/repair_types_test.go internal/app/channelmeta_repair.go internal/access/node/channel_leader_repair_rpc.go internal/access/node/channel_leader_evaluate_rpc.go AGENTS.md
git commit -m "refactor: add channelmeta runtime contracts"
```

---

### Task 10: Move Channelmeta Cache And Activation

**Files:**
- Move/Modify: `internal/app/channelmeta_cache.go` -> `internal/runtime/channelmeta/cache.go`
- Move/Modify: activation-cache portions of `internal/app/channelmeta_activate.go` -> `internal/runtime/channelmeta/activate.go`
- Move/Modify: cache/activation tests from `internal/app/channelmeta_test.go` -> `internal/runtime/channelmeta/cache_test.go` and `internal/runtime/channelmeta/activate_test.go`
- Modify: `internal/app/channelmeta.go` only for compatibility references

Do not move bootstrap, liveness, watcher, resolver, or repair logic in this task.

- [ ] **Step 1: Identify cache and activation tests**

Run:

```bash
rg -n 'Cache|cache|ActivateByKey|ActivateByID|singleflight|activation' internal/app/channelmeta*.go internal/app/*_test.go
```

Expected: list cache/activation behavior and tests that can move without touching bootstrap or repair.

- [ ] **Step 2: Move cache/activation behavior**

Move only cache and activation singleflight/coalescing behavior into `internal/runtime/channelmeta`. Keep method names compatible where that avoids app churn.

- [ ] **Step 3: Add runtime tests**

Add or move tests for:

- positive cache hit;
- negative cache hit;
- activation call coalescing;
- concurrent stop/refresh if this state owns cancellation.

- [ ] **Step 4: Run focused tests and import check**

Run: `go test ./internal/runtime/channelmeta ./internal/app -run 'Test.*Cache|Test.*Activation|TestChannelMetaSyncActivate' -count=1`

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/channelmeta | rg 'internal/(access|gateway|usecase|app)' || true
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git diff --name-only
git add -N internal/runtime/channelmeta/cache.go internal/runtime/channelmeta/cache_test.go internal/runtime/channelmeta/activate.go internal/runtime/channelmeta/activate_test.go
git add -p internal/runtime/channelmeta/cache.go internal/runtime/channelmeta/cache_test.go internal/runtime/channelmeta/activate.go internal/runtime/channelmeta/activate_test.go internal/app/channelmeta_cache.go internal/app/channelmeta_activate.go internal/app/channelmeta.go internal/app/channelmeta_test.go
git commit -m "refactor: move channel meta cache to runtime"
```

---

### Task 11: Move Channelmeta Bootstrapper

**Files:**
- Move/Modify: `internal/app/channelmeta_bootstrap.go` -> `internal/runtime/channelmeta/bootstrap.go`
- Move/Modify: bootstrap tests from `internal/app/channelmeta_test.go` -> `internal/runtime/channelmeta/bootstrap_test.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/build.go` only if construction wiring changes

Do not move liveness, watcher, resolver, or repair logic in this task.

- [ ] **Step 1: Identify bootstrap behavior**

Run:

```bash
rg -n 'Bootstrap|bootstrap|ensureChannelRuntimeMeta|authoritative miss|AuthoritativeMiss' internal/app/channelmeta*.go internal/app/*_test.go
```

Expected: list bootstrap code and tests.

- [ ] **Step 2: Move bootstrapper behind neutral ports**

Move bootstrapper code to `internal/runtime/channelmeta/bootstrap.go`. Use the interfaces created in Task 9 for authoritative meta and local runtime access. Do not import `internal/app` or `internal/access/node`.

- [ ] **Step 3: Add runtime bootstrap tests**

Cover authoritative miss bootstrap, failed bootstrap not applying partial metadata, and single-node cluster bootstrap behavior with fakes.

- [ ] **Step 4: Run tests and import check**

Run: `go test ./internal/runtime/channelmeta ./internal/app -run 'Test.*Bootstrap|TestChannelMetaSyncRefreshBootstraps' -count=1`

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/channelmeta | rg 'internal/(access|gateway|usecase|app)' || true
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git diff --name-only
git add -N internal/runtime/channelmeta/bootstrap.go internal/runtime/channelmeta/bootstrap_test.go
git add -p internal/runtime/channelmeta/bootstrap.go internal/runtime/channelmeta/bootstrap_test.go internal/app/channelmeta_bootstrap.go internal/app/channelmeta.go internal/app/build.go internal/app/channelmeta_test.go
git commit -m "refactor: move channel meta bootstrapper to runtime"
```

---

### Task 12: Move Channelmeta Liveness And State Watchers

**Files:**
- Move/Modify: `internal/app/channelmeta_liveness.go` -> `internal/runtime/channelmeta/liveness.go`
- Move/Modify: `internal/app/channelmeta_statechange.go` -> `internal/runtime/channelmeta/statechange.go`
- Move/Modify: watcher/liveness tests from `internal/app/channelmeta_test.go` -> `internal/runtime/channelmeta/liveness_test.go` and `internal/runtime/channelmeta/watcher_test.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/build.go` only if construction wiring changes

Do not move resolver core or repair decisions in this task.

- [ ] **Step 1: Identify liveness and watcher behavior**

Run:

```bash
rg -n 'Liveness|liveness|ReplicaState|watch|Watcher|ActiveSlots|dirty|StopCancels|SlotLeaderRefresh' internal/app/channelmeta*.go internal/app/*_test.go
```

Expected: list liveness cache and watcher behavior.

- [ ] **Step 2: Move liveness and watcher code**

Move liveness cache and local replica state watcher code behind runtime ports. Keep app responsible only for constructing concrete cluster/runtime/store adapters.

- [ ] **Step 3: Add runtime tests**

Cover cache warm on miss, dead/draining/lease-expired leader detection, active-slot-only polling, dirty refresh reruns, and stop cancellation.

- [ ] **Step 4: Run tests and import check**

Run: `go test ./internal/runtime/channelmeta ./internal/app -run 'Test.*Liveness|Test.*Watcher|Test.*ReplicaState|TestChannelMetaSyncStopCancels' -count=1`

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/channelmeta | rg 'internal/(access|gateway|usecase|app)' || true
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git diff --name-only
git add -N internal/runtime/channelmeta/liveness.go internal/runtime/channelmeta/liveness_test.go internal/runtime/channelmeta/statechange.go internal/runtime/channelmeta/watcher_test.go
git add -p internal/runtime/channelmeta/liveness.go internal/runtime/channelmeta/liveness_test.go internal/runtime/channelmeta/statechange.go internal/runtime/channelmeta/watcher_test.go internal/app/channelmeta_liveness.go internal/app/channelmeta_statechange.go internal/app/channelmeta.go internal/app/build.go internal/app/channelmeta_test.go
git commit -m "refactor: move channel meta liveness watchers to runtime"
```

---

### Task 13: Move Channelmeta Resolver Core Without Repair

**Files:**
- Move/Modify: resolver portions of `internal/app/channelmeta.go` -> `internal/runtime/channelmeta/resolver.go`
- Move/Modify: resolver tests from `internal/app/channelmeta_test.go` -> `internal/runtime/channelmeta/resolver_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/channelmeta_repair.go` only to satisfy injected repair interface

Do not move leader repair logic in this task; inject it through the neutral `Repairer` interface from Task 9.

- [ ] **Step 1: Create runtime resolver type**

Create a runtime `Sync` or `Resolver` type with methods compatible with current app call sites:

```go
func (s *Sync) Start() error
func (s *Sync) StopWithoutCleanup() error
func (s *Sync) RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
func (s *Sync) ActivateByID(ctx context.Context, id channel.ChannelID, source channelruntime.ActivationSource) (channel.Meta, error)
```

- [ ] **Step 2: Move resolver behavior**

Move refresh/apply/activation orchestration into runtime. Keep app as composition only and keep repair injected.

- [ ] **Step 3: Move/add resolver tests**

Cover leader epoch/lease apply, remote routing meta caching without local runtime, slot leader change not rewriting healthy channel leader, expired lease renewal before apply, and no partial apply when dependencies fail.

- [ ] **Step 4: Update app wiring**

Update `internal/app/build.go`, `internal/app/app.go`, and `internal/app/lifecycle.go` so app owns construction/lifecycle only. Keep app tests for build/lifecycle wiring.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/runtime/channelmeta ./internal/app ./internal/access/node -run 'Test.*Resolver|TestChannelMeta|TestStart|TestChannelAppend|TestChannelLeader' -count=1
```

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/channelmeta | rg 'internal/(access|gateway|usecase|app)' || true
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git diff --name-only
git add -N internal/runtime/channelmeta/resolver.go internal/runtime/channelmeta/resolver_test.go
git add -p internal/runtime/channelmeta/resolver.go internal/runtime/channelmeta/resolver_test.go internal/app/channelmeta.go internal/app/channelmeta_repair.go internal/app/app.go internal/app/build.go internal/app/lifecycle.go internal/app/channelmeta_test.go internal/access/node/channel_leader_repair_rpc.go internal/access/node/channel_leader_evaluate_rpc.go
git commit -m "refactor: move channel meta resolver to runtime"
```

---

### Task 14: Move Channel Leader Repair To Runtime Channelmeta

**Files:**
- Move/Modify: `internal/app/channelmeta_repair.go` -> `internal/runtime/channelmeta/repair.go`
- Move/Modify: `internal/app/channelmeta_repair_test.go` -> `internal/runtime/channelmeta/repair_test.go` where behavior-only
- Modify: `internal/access/node/channel_leader_repair_rpc.go`
- Modify: `internal/access/node/channel_leader_evaluate_rpc.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/channelmeta.go` if compatibility wrappers remain

- [ ] **Step 1: Define repair ports**

In `internal/runtime/channelmeta`, define interfaces for concrete dependencies instead of importing app/access:

- authoritative meta store;
- cluster/slot leader lookup;
- remote repair client using neutral DTOs;
- probe client using neutral DTOs;
- local channel log DB/runtime checks.

- [ ] **Step 2: Move repairer/evaluator behavior**

Move `channelLeaderRepairer` and `channelLeaderPromotionEvaluator` logic into runtime package. Keep adapter conversion at `access/node` or app wiring edges.

- [ ] **Step 3: Keep access/node as RPC adapter**

`internal/access/node` continues decoding/encoding node RPCs and calls the runtime repair/evaluate interface through app wiring. It must not own repair decisions.

- [ ] **Step 4: Run repair tests**

Run:

```bash
go test ./internal/runtime/channelmeta ./internal/app ./internal/access/node -run 'TestChannelMeta.*Repair|TestChannelLeader.*Repair|TestChannelLeader.*Evaluate' -count=1
```

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/runtime/channelmeta | rg 'internal/(access|gateway|usecase|app)' || true
```

Expected: no output.

- [ ] **Step 5: Update docs and commit**

Update `internal/runtime/channelmeta/FLOW.md` and `internal/FLOW.md` with the final ownership.

```bash
git diff --name-only
git add -N internal/runtime/channelmeta/repair.go internal/runtime/channelmeta/repair_test.go
git add -p internal/runtime/channelmeta/repair.go internal/runtime/channelmeta/repair_test.go internal/app/channelmeta_repair.go internal/app/channelmeta_repair_test.go internal/access/node/channel_leader_repair_rpc.go internal/access/node/channel_leader_evaluate_rpc.go internal/app/build.go internal/app/channelmeta.go internal/FLOW.md
git commit -m "refactor: move channel leader repair to runtime"
```

---

### Task 15: Add Message Committed Event Contract

**Files:**
- Create: `internal/contracts/messageevents/events.go`
- Create: `internal/contracts/messageevents/events_test.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/app/build.go`
- Modify: `AGENTS.md`

- [ ] **Step 1: Create event contract**

Create `internal/contracts/messageevents/events.go`:

```go
package messageevents

import "github.com/WuKongIM/WuKongIM/pkg/channel"

// MessageCommitted describes a durable channel-log message ready for side effects.
type MessageCommitted struct {
    Message         channel.Message
    SenderSessionID uint64
}

// Clone returns an event copy safe for fanout subscribers to mutate independently.
func (e MessageCommitted) Clone() MessageCommitted {
    e.Message.Payload = append([]byte(nil), e.Message.Payload...)
    return e
}
```

- [ ] **Step 2: Test clone behavior**

Create `events_test.go` verifying payload deep copy.

Run: `go test ./internal/contracts/messageevents -count=1`

Expected: PASS.

- [ ] **Step 3: Update message dispatcher interface**

In `internal/usecase/message/deps.go`, replace any direct `internal/runtime/delivery` event type with:

```go
type CommittedMessageDispatcher interface {
    SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error
}
```

- [ ] **Step 4: Update send path and app adapter**

In `internal/usecase/message/send.go`, publish `messageevents.MessageCommitted` after durable append. Preserve the current sendack semantic: if durable append succeeds, `Send` returns success even when `SubmitCommitted` returns an error; log the dispatch error for retry/observability but do not return it to the caller.

In `internal/app/deliveryrouting.go`, convert `messageevents.MessageCommitted` to the existing delivery runtime envelope internally when calling delivery runtime.

- [ ] **Step 5: Update directory documentation**

Update `AGENTS.md` directory structure to include `internal/contracts/messageevents` under a new `internal/contracts` section.

- [ ] **Step 6: Update tests and import check**

Add or preserve these send behavior tests before running the command:

```go
func TestSendReturnsSuccessWhenCommittedSubmitFails(t *testing.T) {}
func TestSendReturnsSuccessWhenCommittedSubscriberFailsAfterDurableAppend(t *testing.T) {}
```

Run:

```bash
go test ./internal/contracts/messageevents ./internal/usecase/message ./internal/app -run 'TestSend|TestAsyncCommittedDispatcher|TestBuildRealtimeRecvPacket' -count=1
```

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/usecase/message | rg 'internal/runtime/delivery' || true
```

Expected: no output.

- [ ] **Step 7: Commit**

```bash
git diff --name-only
git add -N internal/contracts/messageevents/events.go internal/contracts/messageevents/events_test.go
git add -p internal/contracts/messageevents/events.go internal/contracts/messageevents/events_test.go internal/usecase/message/deps.go internal/usecase/message/send.go internal/usecase/message/send_test.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/app/build.go AGENTS.md
git commit -m "refactor: publish committed message events"
```

---

### Task 16: Add In-Process Committed Event Fanout

**Files:**
- Create: `internal/app/committed_events.go`
- Create: `internal/app/committed_events_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/deliveryrouting.go`

This task introduces the seam only. Do not change sendack durability or make delivery/projection newly blocking. Subscriber errors are observability/logging signals after durable append; they must not make gateway sendack fail.

- [ ] **Step 1: Write fanout tests**

Create tests:

```go
func TestCommittedFanoutCallsSubscribersInOrder(t *testing.T) {}
func TestCommittedFanoutAggregatesSubscriberErrorsForLogging(t *testing.T) {}
func TestCommittedFanoutClonesEventPerSubscriber(t *testing.T) {}
func TestSendReturnsSuccessWhenCommittedSubscriberFailsAfterDurableAppend(t *testing.T) {}
```

- [ ] **Step 2: Run RED**

Run: `go test ./internal/app -run '^TestCommittedFanout' -count=1`

Expected: FAIL until implementation exists.

- [ ] **Step 3: Implement fanout**

Create `internal/app/committed_events.go`:

```go
type committedSubscriber interface {
    SubmitCommitted(context.Context, messageevents.MessageCommitted) error
}

type committedFanout struct {
    subscribers []committedSubscriber
}

// SubmitCommitted returns subscriber errors for logging by the caller; message.Send must not convert them into send failures after durable append.
func (f committedFanout) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
    var joined error
    for _, sub := range f.subscribers {
        if sub == nil {
            continue
        }
        joined = errors.Join(joined, sub.SubmitCommitted(ctx, event.Clone()))
    }
    return joined
}
```

- [ ] **Step 4: Wire fanout conservatively**

Keep current behavior by wrapping existing committed dispatcher(s) as subscribers. If `asyncCommittedDispatcher` currently combines delivery and conversation behavior, keep it as one subscriber first; the goal is to introduce the fanout seam, not to rewrite routing in the same task.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/app ./internal/usecase/message -run 'TestCommittedFanout|TestAsyncCommittedDispatcher|TestSend|TestSendReturnsSuccessWhenCommittedSubscriberFailsAfterDurableAppend' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git diff --name-only
git add -N internal/app/committed_events.go internal/app/committed_events_test.go
git add -p internal/app/committed_events.go internal/app/committed_events_test.go internal/app/build.go internal/app/deliveryrouting.go
git commit -m "refactor: add committed message event fanout"
```

---

### Task 17: Remove Delivery Usecase Dependency On Message Commands

**Files:**
- Create: `internal/contracts/deliveryevents/events.go`
- Modify: `internal/usecase/delivery/types.go`
- Modify: `internal/usecase/delivery/ack.go`
- Modify: `internal/usecase/delivery/offline.go`
- Modify: `internal/usecase/delivery/app_test.go`
- Modify: `internal/usecase/delivery/subscriber_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/access/node/delivery_ack_rpc.go`
- Modify: `internal/access/node/delivery_offline_rpc.go`
- Modify: `internal/usecase/message/deps.go` only if compile failures require it
- Modify: `AGENTS.md`

- [ ] **Step 1: Create delivery event contracts**

Create `internal/contracts/deliveryevents/events.go`:

```go
package deliveryevents

// RouteAck records that a receiver acknowledged a delivered message route.
type RouteAck struct {
    UID        string
    SessionID  uint64
    MessageID  uint64
    MessageSeq uint64
}

// SessionClosed records that a user session closed and pending routes should be cleaned up.
type SessionClosed struct {
    UID       string
    SessionID uint64
}
```

- [ ] **Step 2: Update delivery usecase API**

Change `internal/usecase/delivery` methods to accept `deliveryevents.RouteAck` and `deliveryevents.SessionClosed` instead of command types from `internal/usecase/message`.

- [ ] **Step 3: Add boundary adapters**

Convert existing message/gateway/node command shapes into `deliveryevents` at access/app edges. Do not make `delivery` import `message`.

- [ ] **Step 4: Update directory documentation**

Update `AGENTS.md` directory structure to include `internal/contracts/deliveryevents`.

- [ ] **Step 5: Run tests and import check**

Run:

```bash
go test ./internal/contracts/deliveryevents ./internal/usecase/delivery ./internal/usecase/message ./internal/access/node ./internal/app -run 'Test.*Ack|Test.*Offline|TestSessionClosed|TestRecvAck' -count=1
```

Expected: PASS.

Run:

```bash
go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/usecase/delivery | rg 'internal/usecase/message' || true
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git diff --name-only
git add -N internal/contracts/deliveryevents/events.go
git add -p internal/contracts/deliveryevents/events.go internal/usecase/delivery/types.go internal/usecase/delivery/ack.go internal/usecase/delivery/offline.go internal/usecase/delivery/app_test.go internal/usecase/delivery/subscriber_test.go internal/app/deliveryrouting.go internal/access/node/delivery_ack_rpc.go internal/access/node/delivery_offline_rpc.go internal/usecase/message/deps.go AGENTS.md
git commit -m "refactor: decouple delivery usecase command contracts"
```

---

### Task 18: Add Import Boundary Checks

**Files:**
- Create: `internal/architecture_imports_test.go`
- Modify: `internal/FLOW.md`

Place the test at `internal/architecture_imports_test.go` so `go test ./internal -run TestInternalImportBoundaries` is the stable guard command. The test should be deterministic and should not depend on shelling out to `rg`.

- [ ] **Step 1: Write boundary test**

Create a Go test using `go list -json ./internal/...` or `go/parser` over local files. Enforce at least these rules:

```go
var forbidden = map[string][]string{
    "github.com/WuKongIM/WuKongIM/internal/runtime/": {
        "github.com/WuKongIM/WuKongIM/internal/access/",
        "github.com/WuKongIM/WuKongIM/internal/gateway/",
        "github.com/WuKongIM/WuKongIM/internal/usecase/",
        "github.com/WuKongIM/WuKongIM/internal/app",
    },
    "github.com/WuKongIM/WuKongIM/internal/usecase/": {
        "github.com/WuKongIM/WuKongIM/internal/access/",
        "github.com/WuKongIM/WuKongIM/internal/app",
    },
}
```

Add narrow documented exceptions only for existing unavoidable imports, and include a TODO/removal note for each exception.

- [ ] **Step 2: Run boundary test**

Run: `go test ./internal -run TestInternalImportBoundaries -count=1`

Expected: PASS after Tasks 8-17. If it fails, fix imports instead of weakening the rule unless the exception is explicitly documented.

- [ ] **Step 3: Update flow docs**

In `internal/FLOW.md`, document dependency rules and the boundary test command.

- [ ] **Step 4: Commit**

```bash
git diff --name-only
git add -N internal/architecture_imports_test.go
git add -p internal/architecture_imports_test.go internal/FLOW.md
git commit -m "test: enforce internal import boundaries"
```

---

### Task 19: Update Config Comments And Config Example Guard

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: config loading files if tests reveal schema drift

- [ ] **Step 1: Add missing English comments**

Add concise English comments to every exported config struct and every exported config field in `internal/app/config.go`, including at least:

- `Config`
- `ObservabilityConfig`
- `LogConfig`
- `NodeConfig`
- `StorageConfig`
- `ClusterConfig`
- `NodeConfigRef`
- `SlotConfig`
- `GatewayConfig`
- `APIConfig`
- `ManagerConfig`
- `ManagerUserConfig`
- `ManagerPermissionConfig`
- `ConversationConfig`

Example:

```go
// ClusterConfig defines controller, slot, and channel replication settings for the node's cluster runtime.
type ClusterConfig struct {
    // ListenAddr is the node-to-node cluster RPC listen address.
    ListenAddr string
}
```

- [ ] **Step 2: Add config example guard test**

In `internal/app/config_test.go`, add a guard that derives or lists every `WK_` key supported by the current config loader and verifies each key appears in `wukongim.conf.example`. This guard must cover all current keys, not only keys touched by this refactor.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/app -run 'Test.*Config|Test.*Example' -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git diff --name-only
git add -p internal/app/config.go internal/app/config_test.go wukongim.conf.example
git commit -m "docs: document app config fields"
```

---

### Task 20: Final Documentation And Verification Gate

**Files:**
- Modify: `internal/FLOW.md`
- Modify: `internal/runtime/channelmeta/FLOW.md`
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/specs/2026-04-25-internal-target-architecture-design.md` only if implementation legitimately changed the accepted architecture
- Modify: `docs/superpowers/plans/2026-04-25-internal-target-architecture.md` only for implementation notes or deviations

- [ ] **Step 1: Align flow docs**

Update `internal/FLOW.md` and `internal/runtime/channelmeta/FLOW.md` to reflect:

- lifecycle manager/resource stack;
- `runtime/channelmeta` ownership;
- committed message event fanout;
- startup order: `cluster -> managed_slots_ready -> channelmeta -> presence -> conversation_projector -> gateway -> api -> manager`;
- stop order: `manager -> api -> gateway -> conversation_projector -> presence -> channelmeta -> cluster -> channel/runtime resources -> raft db -> meta db -> logger`;
- normal unit and integration test commands;
- `AGENTS.md` directory structure for `internal/app/lifecycle`, `internal/runtime/channelmeta`, and `internal/contracts/*`.

- [ ] **Step 2: Run doc grep checks**

Run:

```bash
rg -n 'standalone|single-server|单机语义|sendWithMetaRefreshRetry|startAPI → api.Start\(\).*任一步骤' internal/FLOW.md docs/superpowers/specs/2026-04-25-internal-target-architecture-design.md || true
```

Expected: no stale standalone wording and no obsolete helper names. The term “单机语义” may appear only when explicitly saying the project does not have standalone semantics.

- [ ] **Step 3: Run final formatting and tests**

Run: `gofmt -l $(find internal -name '*.go' -type f)`

Expected: no output.

Run: `go test -count=1 ./internal/...`

Expected: PASS.

Run: `go vet ./internal/...`

Expected: PASS.

Run: `staticcheck ./internal/...`

Expected: PASS.

- [ ] **Step 4: Run race smoke**

Run:

```bash
go test -race -count=1 ./internal/runtime/delivery ./internal/runtime/online ./internal/runtime/channelmeta ./internal/gateway/session ./internal/gateway/core
```

Expected: PASS.

- [ ] **Step 5: Run integration smoke**

Run:

```bash
go test -tags=integration -count=1 ./internal/app -run 'TestThreeNodeAppSendAckReturnsBeforeLeaderCheckpointDurability|TestAppSingleNodeClusterStartsAndServesGateway'
```

Expected: PASS. If exact names changed, choose one pending checkpoint three-node test and one single-node cluster app startup test.

- [ ] **Step 6: Run import boundary check**

Run: `go test ./internal -run TestInternalImportBoundaries -count=1`

Expected: PASS.

- [ ] **Step 7: Commit docs if changed**

```bash
git diff --name-only
git add -p internal/FLOW.md internal/runtime/channelmeta/FLOW.md AGENTS.md docs/superpowers/specs/2026-04-25-internal-target-architecture-design.md docs/superpowers/plans/2026-04-25-internal-target-architecture.md
git commit -m "docs: align internal architecture flow"
```

If no docs changed, do not commit.

---

## Suggested Subagent Assignment During Execution

- **Lifecycle worker:** Tasks 1, 5, 6. Owns `internal/app/lifecycle*`, `internal/app/app.go`, `internal/app/lifecycle.go`, and lifecycle tests. Do not run alongside another worker editing those files.
- **Resource worker:** Task 7. Owns `internal/app/build.go`, `internal/app/build_test.go`, and `internal/app/lifecycle/resource_stack*`. Must coordinate with lifecycle worker before editing `App.Stop` ownership.
- **Test-quality worker:** Tasks 2-4. Owns formatting/static cleanup and slow test tagging. Should not delete integration-only helpers without moving them behind matching build tags.
- **Boundary worker:** Tasks 8, 15, 17, 18. Owns online boundary, contracts, and import guard files.
- **Channelmeta worker:** Tasks 9-14. Owns `internal/runtime/channelmeta` and `internal/app/channelmeta*.go`. Must not run in parallel with resource/lifecycle workers when `internal/app/build.go` is shared.
- **Docs/config worker:** Tasks 19-20. Owns config comments, examples, and flow docs.
- **Reviewer agents:** After each phase, run a read-only high-intelligence reviewer focused on regressions, dependency direction, and missing tests.

## Known Risks And Guardrails

- Do not move `channelmeta_repair.go` before neutral DTOs/conversions exist; otherwise `runtime/channelmeta` can inherit `internal/access/node` coupling.
- Do not introduce `internal/infra` in this execution slice. The design allows it later, but this plan first stabilizes lifecycle/resource ownership and runtime boundaries.
- Do not alter sendack durability semantics. `MessageCommitted` is published after durable append, and fanout starts as a behavior-preserving seam.
- Do not replace `channelMetaSync.StopWithoutCleanup` with cleanup-oriented stop during app shutdown.
- Do not hide slow tests by tagging them integration without preserving fast unit coverage for the same regression.
- Resource stack cleanup must not double-close transport/runtime resources unless idempotency is proven by tests.
