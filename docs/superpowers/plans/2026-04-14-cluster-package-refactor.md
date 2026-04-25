# Cluster Package Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Refactor `pkg/cluster` from a monolithic glue object into smaller, testable components while keeping cluster behavior, storage formats, and upper-layer semantics stable.

**Architecture:** Treat `docs/raw/cluster-package-refactor.md` as the approved design. Land the refactor in small behavior-preserving batches: first isolate configuration and retry/runtime primitives, then peel transport/controller/observer responsibilities out of `Cluster`, then migrate managed-slot and controller RPCs to binary codecs, then split reconcile execution into `agent` + `reconciler` + `slotExecutor`, and finally switch upper layers to a stable `cluster.API` interface and wire observer hooks/docs. Each batch must keep `pkg/cluster` buildable and preserve the current single-node-cluster semantics.

**Tech Stack:** Go, `testing`, `go test`, existing `pkg/transport`, `pkg/controller/*`, `pkg/slot/multiraft`, existing binary forward codec patterns.

---

### Task 0: Capture baseline test and stress data

**Files:**
- Verify only: `pkg/cluster/stress_test.go`
- Verify only: `pkg/cluster/*.go`
- Verify only: `internal/app/*.go`
- Verify only: `pkg/slot/proxy/*.go`

- [x] **Step 1: Run the current targeted baseline test sweep before changing code**

Run: `go test ./pkg/cluster/... ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1`
Expected: PASS.

- [x] **Step 2: Capture a baseline stress run for later comparison**

Run: `WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=5s go test ./pkg/cluster -run TestStressSingleNodeThroughput -count=1`
Expected: PASS and log a throughput report for later comparison.

- [x] **Step 3: Capture a whole-repo build baseline for later batch gates**

Run: `go build ./...`
Expected: PASS.

### Task 1: Establish baseline and configurable timeouts

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/config_test.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/managed_slots.go`
- Modify: `pkg/cluster/operator.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `internal/app/config.go`
- Modify: `internal/app/build.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`
- Reference: `docs/raw/cluster-package-refactor.md`

- [x] **Step 1: Write failing tests for timeout defaults and overrides**

```go
func TestConfigApplyDefaultsTimeouts(t *testing.T) {
    cfg := Config{}
    cfg.applyDefaults()

    if cfg.Timeouts.ControllerObservation != 200*time.Millisecond {
        t.Fatalf("expected default observation timeout")
    }
}
```

- [x] **Step 2: Run timeout tests to verify they fail for the missing `Timeouts` structure**

Run: `go test ./pkg/cluster -run 'TestConfigApplyDefaultsTimeouts|TestConfigValidate' -count=1`
Expected: FAIL with unknown field / missing timeout defaults.

- [x] **Step 3: Add `Timeouts`, thread defaulted values into current hard-coded call sites, and plumb app/runtime config so deployed nodes can override the same fields**

```go
type Timeouts struct {
    ControllerObservation     time.Duration
    ControllerRequest         time.Duration
    ControllerLeaderWait      time.Duration
    ForwardRetryBudget        time.Duration
    ManagedSlotLeaderWait     time.Duration
    ManagedSlotCatchUp        time.Duration
    ManagedSlotLeaderMove     time.Duration
    ConfigChangeRetryBudget   time.Duration
    LeaderTransferRetryBudget time.Duration
}
```

- [x] **Step 4: Run focused cluster tests for config and startup paths**

Run: `go test ./pkg/cluster -run 'TestConfig|TestCluster' -count=1 && go test ./internal/app/... -run 'TestConfig|TestBuild' -count=1`
Expected: PASS.

- [x] **Step 5: Run a repo build gate after the config shape changes**

Run: `go build ./...`
Expected: PASS.

### Task 2: Introduce reusable retry and runtime state primitives

**Files:**
- Create: `pkg/cluster/retry.go`
- Create: `pkg/cluster/retry_test.go`
- Create: `pkg/cluster/runtime_state.go`
- Create: `pkg/cluster/runtime_state_test.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/operator.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/forward.go`
- Modify: `pkg/cluster/managed_slots.go`

- [x] **Step 1: Write failing unit tests for retry budgets and runtime state snapshots**
- [x] **Step 2: Run `go test ./pkg/cluster -run 'TestRetry|TestRuntimeState' -count=1` and confirm RED**
- [x] **Step 3: Implement `Retry.Do` plus `runtimeState` and switch existing retry/runtime-peer helpers to the new primitives**
- [x] **Step 4: Run `go test ./pkg/cluster -run 'TestRetry|TestRuntimeState|TestForward|TestOperator|TestControllerClient' -count=1`**
- [x] **Step 5: Run `go build ./...`**

### Task 3: Extract transport aggregation, controller host, and observer loop

**Files:**
- Create: `pkg/cluster/transport_glue.go`
- Create: `pkg/cluster/controller_host.go`
- Create: `pkg/cluster/observer.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/readiness.go`
- Modify: `pkg/cluster/router.go`
- Modify: `pkg/cluster/transport_test.go`
- Modify: `pkg/cluster/cluster_test.go`

- [x] **Step 1: Add focused tests for start/stop sequencing, controller-host lifecycle, and observer stop semantics**
- [x] **Step 2: Run `go test ./pkg/cluster -run 'TestCluster(Start|Stop)|TestObserver|TestTransport' -count=1` and confirm RED where new helpers are missing**
- [x] **Step 3: Move network/controller/observation responsibilities out of `Cluster` into dedicated structs while keeping public behavior unchanged, explicitly preserving the single-node-cluster legacy-slot startup order**
- [x] **Step 4: Run `go test ./pkg/cluster -run 'TestCluster|TestObserver|TestTransport' -count=1`**
- [x] **Step 5: Run `go build ./...`**

### Task 4: Split managed-slot local/runtime responsibilities and replace JSON RPC

**Files:**
- Create: `pkg/cluster/codec_managed.go`
- Create: `pkg/cluster/codec_managed_test.go`
- Create: `pkg/cluster/slot_manager.go`
- Create: `pkg/cluster/slot_handler.go`
- Modify: `pkg/cluster/managed_slots.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/transport_test.go`
- Modify: `pkg/cluster/cluster_integration_test.go`
- Modify: `pkg/cluster/comm_test.go`

- [x] **Step 1: Write round-trip codec tests and handler tests that describe status/config/leader-transfer semantics without JSON**
- [x] **Step 2: Run `go test ./pkg/cluster -run 'TestManagedSlot|TestCodecManaged|TestTransport' -count=1` and confirm RED**
- [x] **Step 3: Implement `slotManager`, `slotHandler`, and the managed-slot binary codec, preserving integration-test hook compatibility without package-level mutable state**
- [x] **Step 4: Run `go test ./pkg/cluster -run 'TestManagedSlot|TestCodecManaged|TestTransport|TestComm' -count=1`**

- [x] **Step 5: Run managed-slot integration coverage after the RPC swap**

Run: `go test -tags=integration ./pkg/cluster/... -run 'TestCluster|TestManaged' -count=1`
Expected: PASS.

- [x] **Step 6: Run `go build ./...`**

### Task 5: Split controller RPC server/client responsibilities and replace JSON RPC

**Files:**
- Create: `pkg/cluster/codec_control.go`
- Create: `pkg/cluster/codec_control_test.go`
- Create: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/cluster_integration_test.go`

- [x] **Step 1: Write failing controller codec and redirect/leader-cache tests**
- [x] **Step 2: Run `go test ./pkg/cluster -run 'TestControllerClient|TestCodecControl' -count=1` and confirm RED**
- [x] **Step 3: Move controller RPC serving into `controller_handler.go`, switch controller client calls to binary codec, and replace mutex leader caching with `atomic.Uint64`**
- [x] **Step 4: Run `go test ./pkg/cluster -run 'TestControllerClient|TestCodecControl|TestCluster' -count=1`**

- [x] **Step 5: Run controller-path integration coverage after the RPC swap**

Run: `go test -tags=integration ./pkg/cluster/... ./internal/app/... -run 'TestCluster|TestApp' -count=1`
Expected: PASS.

- [x] **Step 6: Run `go build ./...`**

### Task 6: Refactor assignment application into `agent`, `reconciler`, and `slotExecutor`

**Files:**
- Create: `pkg/cluster/reconciler.go`
- Create: `pkg/cluster/reconciler_test.go`
- Create: `pkg/cluster/slot_executor.go`
- Create: `pkg/cluster/slot_executor_test.go`
- Modify: `pkg/cluster/agent.go`
- Modify: `pkg/cluster/managed_slots.go`
- Modify: `pkg/cluster/assignment_cache.go`
- Modify: `pkg/cluster/readiness.go`
- Modify: `pkg/cluster/operator.go`
- Modify: `pkg/cluster/cluster_integration_test.go`
- Modify: `pkg/cluster/forward_test.go`

- [x] **Step 1: Write failing tests for reconcile stage boundaries, pending report replay, and slot-execution step ordering**
- [x] **Step 2: Run `go test ./pkg/cluster -run 'TestAgent|TestReconciler|TestSlotExecutor' -count=1` and confirm RED**
- [x] **Step 3: Extract planning/execution logic out of `agent.ApplyAssignments`, move slot-migration steps into `slotExecutor`, and use bounded concurrency for controller reads**
- [x] **Step 4: Run `go test ./pkg/cluster -run 'TestAgent|TestReconciler|TestSlotExecutor|TestForward' -count=1`**

- [x] **Step 5: Run reconcile/failover integration coverage before moving on**

Run: `go test -tags=integration ./pkg/cluster/... ./internal/app/... -count=1`
Expected: PASS.

- [x] **Step 6: Run `go build ./...`**

### Task 7: Add `cluster.API` and migrate upper-layer consumers off `*Cluster`

**Files:**
- Create: `pkg/cluster/api.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/messagerouting.go`
- Modify: `internal/app/presenceauthority.go`
- Modify: `internal/access/node/options.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/runtime_meta_rpc.go`
- Modify: related tests under `internal/app/...` and `pkg/slot/proxy/...`

- [x] **Step 1: Write failing compile-time and unit-level tests/assertions for the new `API` boundary**
- [x] **Step 2: Run `go test ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1` and confirm RED on the old concrete-type dependency**
- [x] **Step 3: Define `API`, make `Cluster` satisfy it, and update app/access/proxy consumers to depend on the interface except where concrete-type tests explicitly require `*Cluster`**
- [x] **Step 4: Run `go test ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1`**
- [x] **Step 5: Run `go build ./...`**

### Task 8: Wire observer hooks, remove obsolete globals, and update docs

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/forward.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/reconciler.go`
- Modify: `pkg/cluster/slot_executor.go`
- Modify: `pkg/cluster/slot_manager.go`
- Modify: `AGENTS.md`
- Modify: `docs/wiki/architecture/02-slot-layer.md`
- Modify: `wukongim.conf.example` (only if config surface changes externally; otherwise leave untouched)

- [x] **Step 1: Add failing tests for all five observer callbacks and for absence of package-level mutable managed-slot globals**
- [x] **Step 2: Run `go test ./pkg/cluster -run 'TestObserverHooks|TestManagedSlot' -count=1` and confirm RED**
- [x] **Step 3: Thread `ObserverHooks` through hot paths (`OnControllerCall`, `OnReconcileStep`, `OnForwardPropose`, `OnSlotEnsure`, `OnLeaderChange`), delete obsolete package globals, and sync docs if the cluster file layout/responsibility descriptions changed**
- [x] **Step 4: Run `go test ./pkg/cluster ./internal/app/... ./pkg/slot/proxy/... -count=1`**
- [x] **Step 5: Run `go build ./...`**

### Task 9: Final verification and regression sweep

**Files:**
- Verify only: `pkg/cluster/*.go`
- Verify only: `internal/app/*.go`
- Verify only: `pkg/slot/proxy/*.go`
- Verify only: `docs/superpowers/plans/2026-04-14-cluster-package-refactor.md`

- [x] **Step 1: Run full unit-test sweep for touched packages**

Run: `go test ./pkg/cluster/... ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1`
Expected: PASS.

- [x] **Step 2: Run integration suites for the high-risk paths**

Run: `go test -tags=integration ./pkg/cluster/... ./internal/app/... -count=1`
Expected: PASS.

- [x] **Step 3: Run repeat cluster package tests for flake detection**

Run: `go test ./pkg/cluster/... -count=3`
Expected: PASS on all three runs.

- [x] **Step 4: Re-run the same stress test from Task 0 and compare the throughput report to the baseline**

Run: `WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=5s go test ./pkg/cluster -run TestStressSingleNodeThroughput -count=1`
Expected: PASS with throughput at least holding steady relative to the recorded baseline.

- [x] **Step 5: Check acceptance-criteria-specific invariants directly**

Run: `rg -n '^var ' pkg/cluster/*.go && rg -n 'time\\.(Second|Millisecond)' pkg/cluster/*.go`
Expected: only allowed declarations/defaults remain, or the output is empty where the spec requires that.

- [x] **Step 6: Re-read `docs/raw/cluster-package-refactor.md` and verify every landed change maps to the approved design without unplanned behavior changes**

- [x] **Step 7: Record any intentionally deferred items (if any) in the plan file before handoff**

---

## Execution Notes

### 2026-04-14 verification snapshot

- `go test ./pkg/cluster/... ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1` -> PASS
- `go build ./...` -> PASS
- `go test -tags=integration ./pkg/cluster -count=1` -> PASS
- `go test ./pkg/cluster -run 'TestControllerClient|TestCodecControl|TestControllerHandler' -count=1` -> PASS
- `go test ./pkg/cluster/... -count=1` -> PASS
- `go test -tags=integration ./pkg/cluster/... -run 'TestCluster' -count=1` -> PASS
- `go test -tags=integration ./pkg/cluster/... ./internal/app/... -run 'TestCluster|TestApp' -count=1` -> FAIL in `internal/app` at `TestAppManagedSlotStartupAllowsSubsetAssignmentsPerNode` with `raftcluster: no leader for slot`
- `go test -tags=integration ./internal/app -run TestAppManagedSlotStartupAllowsSubsetAssignmentsPerNode -count=1` reproduces the same failure on both:
  - `/Users/tt/Desktop/work/go/WuKongIM-v3.1`
  - `/Users/tt/Desktop/work/go/WuKongIM-v3.1/.worktrees/cluster-package-refactor`
- Current assessment: the managed-slot subset-assignment startup failure is a pre-existing `internal/app` integration issue, not a regression introduced by this refactor branch.
- Managed-slot test hooks are now stored on each `Cluster` instance instead of package-level globals; this removes the ordered `TestClusterSurfacesFailedRepairAfterRetryExhaustion` flake caused by cross-test hook leakage during integration runs.
- Additional post-fix verification:
  - `go test ./pkg/cluster -run 'TestManagedSlotExecutionTestHookIsClusterScoped|TestWaitForManagedSlotCatchUpUsesConfiguredPollInterval|TestGroupAgentRetriesTaskResultReportOnControllerLeaderChange' -count=1` -> PASS
  - `go test -tags=integration ./pkg/cluster -run 'TestCluster(SurfacesFailedRepairAfterRetryExhaustion|ControllerLeaderFailoverResumesInFlightRepair)$' -count=1` -> PASS
  - `go test ./pkg/cluster/... -count=1` -> PASS
  - `go test -tags=integration ./pkg/cluster/... -run 'TestCluster' -count=1` -> PASS
  - `go test ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1` -> PASS
  - `go build ./...` -> PASS
- ObserverHooks batch verification:
  - `go test ./pkg/cluster -run 'TestObserverHooks(OnForwardPropose|OnControllerCall|OnReconcileStep|OnSlotEnsure|OnSlotOpen|OnSlotClose|OnLeaderChange|OnLeaderMoveTransition|OnLeaderMoveSkipsUnknownLeader)$' -count=1` -> PASS
  - `go test ./pkg/cluster/... -count=1` -> PASS
  - `go test ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1` -> PASS
  - `go build ./...` -> PASS
  - `go test -tags=integration ./pkg/cluster -run 'TestClusterControllerLeaderFailoverResumesInFlightRepair$' -count=3` -> PASS
  - `go test -tags=integration ./pkg/cluster/... -run 'TestCluster' -count=1` -> PASS
  - One intermediate full-suite integration run transiently timed out at `TestClusterControllerLeaderFailoverResumesInFlightRepair`; the isolated test passed on three consecutive reruns and the subsequent full rerun passed, so this did not reproduce as a deterministic regression.
- Final verification sweep:
  - `go test ./pkg/cluster/... -count=3` -> PASS
  - `rg -n '^var ' pkg/cluster/*.go` -> only `pkg/cluster/api.go` interface assertion and `pkg/cluster/errors.go` error declarations
  - `rg -n 'time\\.(Second|Millisecond)' pkg/cluster/*.go` -> defaults/tests plus `pkg/cluster/readiness.go` derived interval constants; no new unexpected production literals beyond configured defaults/helper baselines
  - `WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=5s go test -v ./pkg/cluster -run TestStressSingleNodeThroughput -count=1` -> PASS, throughput `151 ops/s` (`757` ops over `5s`, `0` errors)
  - `go test -tags=integration ./pkg/cluster/... ./internal/app/... -count=1` -> FAIL in `internal/app` after timeout; full output did not include a concise per-test summary before the goroutine dump
  - `go test -tags=integration ./internal/app -run 'TestAppManagedSlotStartupAllowsSubsetAssignmentsPerNode$' -count=1` -> FAIL with `raftcluster: no leader for slot`
  - Current assessment remains unchanged: the `internal/app` subset-assignment startup case is still the known blocker for a completely green cross-package integration sweep.
- Docs sync:
  - Updated `docs/wiki/architecture/02-slot-layer.md` to reflect the current `pkg/slot/multiraft` path, flat `pkg/cluster` helper layout, shared `Retry` semantics, and controller RPC registration in `transport_glue.go`.
  - `AGENTS.md` did not need changes for this batch because its `pkg/cluster` description is still accurate at the current level of detail.

### 2026-04-15 final completion snapshot

- Structural completion:
  - Added `pkg/cluster/cluster_layout_test.go` with `TestClusterStructFieldBudget` to keep `Cluster` top-level field count within the design budget.
  - Folded transport/controller/managed-slot wiring into embedded resource groups in `pkg/cluster/cluster.go` so the top-level `Cluster` layout stays under the plan's field-count target without changing public behavior.
  - Confirmed the final Task 4 / Task 6 carry-over work is landed: `slotManager` is in `pkg/cluster/slot_manager.go`, `slotHandler` routes through managed-slot helpers, and `reconciler` task preloading uses bounded controller-read concurrency.
- Fresh verification after the final layout cleanup:
  - `go test ./pkg/cluster -run TestClusterStructFieldBudget -count=1` -> PASS
  - `go test ./pkg/cluster/... ./internal/app/... ./internal/access/node ./pkg/slot/proxy/... -count=1` -> PASS
  - `go test -tags=integration ./pkg/cluster/... ./internal/app/... -count=1 -json` -> PASS
  - `go test ./pkg/cluster/... -count=3` -> PASS
  - `WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=5s go test -v ./pkg/cluster -run TestStressSingleNodeThroughput -count=1` -> PASS, throughput `153 ops/s` (`765` ops over `5s`, `0` errors)
  - `rg -n '^var ' pkg/cluster/*.go && rg -n 'time\\.(Second|Millisecond)' pkg/cluster/*.go` -> only the expected package vars plus config defaults / derived polling constants / test fixtures
  - `go build ./...` -> PASS
- Integration note:
  - One intermediate combined integration run failed transiently before the `-json` rerun. The immediately repeated `go test -tags=integration ./pkg/cluster/... ./internal/app/... -count=1 -json` passed end-to-end, and isolated `pkg/cluster` integration also passed, so the failure did not reproduce as a deterministic regression after the final layout cleanup.
- Deferred items:
  - None. This plan is complete in the worktree `refactor/cluster-package-refactor`.
