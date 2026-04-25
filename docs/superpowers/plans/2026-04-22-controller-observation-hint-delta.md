# Controller Observation Hint + Delta Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace hot steady-state controller observation polling with leader-driven hints, batched observation deltas, and low-frequency safety sync so idle CPU drops without materially slowing cluster convergence.

**Architecture:** Keep the controller leader as the source of truth, but stop making every follower repeatedly ask for unchanged state. The implementation adds revisioned observation snapshots on the leader, best-effort hint push to followers, one batched `FetchObservationDelta` pull for wake-driven reconcile, and separate low-frequency safety loops for fallback validation and planner safety ticks.

**Tech Stack:** Go, `testing`, `pkg/cluster`, existing controller RPC codec/handler/client paths, existing `pkg/transport` server/client pools, controller metadata snapshots, Markdown flow docs.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-controller-observation-hint-delta-design.md`
- Existing observation flow: `pkg/cluster/FLOW.md`
- Current observation loop: `pkg/cluster/cluster.go`
- Current controller read protocol: `pkg/cluster/codec_control.go`, `pkg/cluster/controller_handler.go`, `pkg/cluster/controller_client.go`
- Current leader-local observation state: `pkg/cluster/controller_host.go`, `pkg/cluster/observation_cache.go`
- Current runtime delta reporter: `pkg/cluster/runtime_observation_reporter.go`
- Follow `@superpowers:test-driven-development` for each behavior change.
- Run `@superpowers:verification-before-completion` before claiming the feature is complete.
- In Codex CLI, only use subagents if the user explicitly asks for delegation; otherwise execute this plan locally with `@superpowers:executing-plans`.

## File Structure

- Create: `pkg/cluster/observation_sync.go` — shared revision structs, leader-side delta builder, follower-side delta apply helpers, and reconcile scoping helpers.
- Create: `pkg/cluster/observation_sync_test.go` — unit tests for revision bumping, delta fallback, targeted slot scoping, and cache apply behavior.
- Create: `pkg/cluster/observation_hint.go` — hint payload, encode/decode helpers, follower wake-state coalescing, and best-effort receive handling.
- Create: `pkg/cluster/observation_hint_test.go` — unit tests for stale-leader rejection, duplicate-hint coalescing, and wake-state behavior.
- Modify: `pkg/cluster/codec.go` — register the one-way observation-hint message type.
- Modify: `pkg/cluster/codec_control.go` — add `fetch_observation_delta` request/response kinds and payload encoding.
- Modify: `pkg/cluster/codec_control_test.go` — verify new control payload round-trips.
- Modify: `pkg/cluster/controller_host.go` — own leader-local observation revisions, delta snapshots, planner dirty flags, and hint emission hooks.
- Modify: `pkg/cluster/controller_handler.go` — serve `FetchObservationDelta` from leader-local snapshots and keep redirect behavior.
- Modify: `pkg/cluster/controller_handler_test.go` — cover incremental delta, full-sync fallback, and leader-generation guards.
- Modify: `pkg/cluster/controller_client.go` — add `FetchObservationDelta` and preserve local fast-path behavior.
- Modify: `pkg/cluster/controller_client_internal_test.go` — cover retry and local fast-path behavior for delta reads.
- Modify: `pkg/cluster/agent.go` — replace hot steady-state `list_*` polling with wake-driven delta sync and slow-sync fallback.
- Modify: `pkg/cluster/reconciler.go` — reconcile from scoped deltas when safe and keep existing correctness fallbacks.
- Modify: `pkg/cluster/cluster.go` — split observation loops, register hint handler, run wake/slow-sync/planner loops, and keep migration observation isolated.
- Modify: `pkg/cluster/config.go` — add explicit timing knobs for slow sync and planner safety.
- Modify: `pkg/cluster/readiness.go` — expose interval accessors for the new loops.
- Modify: `pkg/cluster/config_test.go` — assert new timeout defaults and overrides.
- Modify: `pkg/cluster/observer.go` only if loop helpers need a non-periodic wake helper; keep this file focused if changed.
- Modify: `pkg/cluster/cluster_test.go` — cover loop split behavior, planner dirty wakeups, and follower fallback semantics.
- Modify: `pkg/cluster/agent_internal_integration_test.go` — cover wake-driven reconcile and dropped-hint recovery.
- Modify: `pkg/cluster/cluster_integration_test.go` — cover cluster-level convergence timing and leader-switch recovery.
- Modify: `pkg/cluster/observer_hooks_test.go` — keep observer hook expectations aligned with the new loop boundaries.
- Modify: `pkg/cluster/FLOW.md` — document hint + delta, split loops, planner dirty wakeups, and slow-sync fallback.

### Task 1: Add revisioned observation sync state on the leader

**Files:**
- Create: `pkg/cluster/observation_sync.go`
- Create: `pkg/cluster/observation_sync_test.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/observation_cache.go`

- [ ] **Step 1: Write the failing sync-state unit tests**

Add focused tests in `pkg/cluster/observation_sync_test.go` for:
- `TestObservationSyncStateBumpsRuntimeRevisionOnlyOnMeaningfulViewChange`
- `TestObservationSyncStateBuildDeltaFallsBackToFullSyncOnRevisionGap`
- `TestObservationSyncStateBuildDeltaScopesReturnedSlots`
- `TestObservationSyncStateApplyDeltaUpsertsAndDeletesCaches`

- [ ] **Step 2: Run the focused sync-state tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestObservationSyncStateBumpsRuntimeRevisionOnlyOnMeaningfulViewChange|TestObservationSyncStateBuildDeltaFallsBackToFullSyncOnRevisionGap|TestObservationSyncStateBuildDeltaScopesReturnedSlots|TestObservationSyncStateApplyDeltaUpsertsAndDeletesCaches' -count=1`

Expected: FAIL because revisioned sync state and delta helpers do not exist yet.

- [ ] **Step 3: Implement the minimal revisioned sync state**

In `pkg/cluster/observation_sync.go`:
- add shared revision structs for assignments, tasks, nodes, and runtime views
- add leader-side delta builder helpers that can answer incremental vs full-sync responses
- add follower-side apply helpers for assignments / tasks / nodes / runtime views
- add slot-scope helpers so reconcile can later narrow to affected slots safely

In `pkg/cluster/controller_host.go`:
- add fields that track the latest leader-local observation revisions
- bump revisions only when the corresponding read model actually changes
- keep the current metadata snapshot and observation cache as the source for building deltas

In `pkg/cluster/observation_cache.go`:
- expose any small helper needed to detect runtime-view changes without widening responsibilities

- [ ] **Step 4: Re-run the focused sync-state tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestObservationSyncStateBumpsRuntimeRevisionOnlyOnMeaningfulViewChange|TestObservationSyncStateBuildDeltaFallsBackToFullSyncOnRevisionGap|TestObservationSyncStateBuildDeltaScopesReturnedSlots|TestObservationSyncStateApplyDeltaUpsertsAndDeletesCaches' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the sync-state foundation**

Run:
```bash
git add pkg/cluster/observation_sync.go pkg/cluster/observation_sync_test.go pkg/cluster/controller_host.go pkg/cluster/observation_cache.go
git commit -m "feat: add revisioned observation sync state"
```

### Task 2: Wire `fetch_observation_delta` through the controller codec, handler, and client

**Files:**
- Modify: `pkg/cluster/codec_control.go`
- Modify: `pkg/cluster/codec_control_test.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_handler_test.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`

- [ ] **Step 1: Write the failing delta-protocol tests**

Add focused tests for:
- `TestControllerCodecObservationDeltaRoundTrip`
- `TestControllerHandlerFetchObservationDeltaReturnsIncrementalState`
- `TestControllerHandlerFetchObservationDeltaForcesFullSyncOnLeaderGenerationMismatch`
- `TestControllerClientFetchObservationDeltaUsesLocalFastPathWithoutSelfRPC`

- [ ] **Step 2: Run the focused delta-protocol tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestControllerCodecObservationDeltaRoundTrip|TestControllerHandlerFetchObservationDeltaReturnsIncrementalState|TestControllerHandlerFetchObservationDeltaForcesFullSyncOnLeaderGenerationMismatch|TestControllerClientFetchObservationDeltaUsesLocalFastPathWithoutSelfRPC' -count=1`

Expected: FAIL because `fetch_observation_delta` is not a controller RPC kind yet.

- [ ] **Step 3: Implement the controller delta read path**

In `pkg/cluster/codec_control.go`:
- add `controllerRPCFetchObservationDelta`
- add request/response payload encoding for revisioned delta reads

In `pkg/cluster/controller_handler.go`:
- redirect followers as usual
- serve delta reads from leader-local metadata snapshot + observation snapshot + revision state
- return full-sync fallback when the request is stale, leader generation mismatches, or the leader cannot serve an incremental window safely

In `pkg/cluster/controller_client.go`:
- add `FetchObservationDelta(ctx, req)` to `controllerAPI`
- keep using the existing `call()` path so local fast-path behavior stays centralized

- [ ] **Step 4: Re-run the focused delta-protocol tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestControllerCodecObservationDeltaRoundTrip|TestControllerHandlerFetchObservationDeltaReturnsIncrementalState|TestControllerHandlerFetchObservationDeltaForcesFullSyncOnLeaderGenerationMismatch|TestControllerClientFetchObservationDeltaUsesLocalFastPathWithoutSelfRPC' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the delta protocol wiring**

Run:
```bash
git add pkg/cluster/codec_control.go pkg/cluster/codec_control_test.go pkg/cluster/controller_handler.go pkg/cluster/controller_handler_test.go pkg/cluster/controller_client.go pkg/cluster/controller_client_internal_test.go
git commit -m "feat: add controller observation delta rpc"
```

### Task 3: Add best-effort observation hints and follower wake-state coalescing

**Files:**
- Create: `pkg/cluster/observation_hint.go`
- Create: `pkg/cluster/observation_hint_test.go`
- Modify: `pkg/cluster/codec.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/cluster.go`

- [ ] **Step 1: Write the failing hint/wake-state tests**

Add focused tests for:
- `TestObservationHintCodecRoundTrip`
- `TestObservationWakeStateRejectsStaleLeaderGeneration`
- `TestObservationWakeStateCoalescesDuplicateHints`
- `TestClusterHandleObservationHintMarksWakePending`

- [ ] **Step 2: Run the focused hint tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestObservationHintCodecRoundTrip|TestObservationWakeStateRejectsStaleLeaderGeneration|TestObservationWakeStateCoalescesDuplicateHints|TestClusterHandleObservationHintMarksWakePending' -count=1`

Expected: FAIL because the hint payload, message type, and wake-state handling do not exist yet.

- [ ] **Step 3: Implement hint transport and wake-state**

In `pkg/cluster/observation_hint.go`:
- define the best-effort hint payload with `LeaderID`, `LeaderGeneration`, revisions, `AffectedSlots`, and `NeedFullSync`
- add encode/decode helpers
- add a follower-local wake-state that tracks the latest hint, rejects stale leaders, and coalesces duplicate wake requests

In `pkg/cluster/codec.go`:
- register a new one-way cluster message type for observation hints

In `pkg/cluster/cluster.go`:
- register a handler for inbound observation hints
- route valid hints into the follower wake-state instead of directly reconciling inline

In `pkg/cluster/controller_host.go`:
- emit hints best-effort when leader-local revisions change
- reuse existing transport clients instead of introducing a second transport stack

- [ ] **Step 4: Re-run the focused hint tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestObservationHintCodecRoundTrip|TestObservationWakeStateRejectsStaleLeaderGeneration|TestObservationWakeStateCoalescesDuplicateHints|TestClusterHandleObservationHintMarksWakePending' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the hint foundation**

Run:
```bash
git add pkg/cluster/observation_hint.go pkg/cluster/observation_hint_test.go pkg/cluster/codec.go pkg/cluster/controller_host.go pkg/cluster/cluster.go
git commit -m "feat: add controller observation hints"
```

### Task 4: Replace hot follower `list_*` reads with wake-driven delta sync and slow-sync fallback

**Files:**
- Modify: `pkg/cluster/agent.go`
- Modify: `pkg/cluster/reconciler.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/agent_internal_integration_test.go`
- Modify: `pkg/cluster/cluster_integration_test.go`

- [ ] **Step 1: Write the failing follower-sync tests**

Add focused tests for:
- `TestObserveOnceUsesObservationDeltaBeforeSeparateListReads`
- `TestWakeReconcileLoopSingleflightsConcurrentHints`
- `TestSlowSyncLoopRecoversDroppedHint`
- `TestReconcilerTickScopesToAffectedLocalSlotsWhenDeltaIsScoped`

- [ ] **Step 2: Run the focused follower-sync tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestObserveOnceUsesObservationDeltaBeforeSeparateListReads|TestWakeReconcileLoopSingleflightsConcurrentHints|TestSlowSyncLoopRecoversDroppedHint|TestReconcilerTickScopesToAffectedLocalSlotsWhenDeltaIsScoped' -count=1`

Expected: FAIL because followers still depend on hot periodic `RefreshAssignments/ListNodes/ListRuntimeViews/ListTasks` reads.

- [ ] **Step 3: Implement wake-driven delta sync on followers**

In `pkg/cluster/agent.go`:
- add a delta-sync method that requests one observation delta, applies it to local caches, and updates local revisions
- preserve correctness fallback to full-sync or store-backed paths when incremental sync cannot be served

In `pkg/cluster/cluster.go`:
- add a wake-driven reconcile loop that consumes the coalesced wake-state
- keep a low-frequency slow-sync loop that revalidates revisions even if hints were lost

In `pkg/cluster/reconciler.go`:
- accept optional affected-slot scope from the applied delta
- reconcile only affected local slots when safe
- fall back to current all-local-assignment reconcile behavior when scope is missing or unsafe

- [ ] **Step 4: Re-run the focused follower-sync tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestObserveOnceUsesObservationDeltaBeforeSeparateListReads|TestWakeReconcileLoopSingleflightsConcurrentHints|TestSlowSyncLoopRecoversDroppedHint|TestReconcilerTickScopesToAffectedLocalSlotsWhenDeltaIsScoped' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the wake-driven follower sync**

Run:
```bash
git add pkg/cluster/agent.go pkg/cluster/reconciler.go pkg/cluster/cluster.go pkg/cluster/cluster_test.go pkg/cluster/agent_internal_integration_test.go pkg/cluster/cluster_integration_test.go
git commit -m "feat: drive follower reconcile from observation delta"
```

### Task 5: Split observation cadences and make planner wake on dirty state

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/readiness.go`
- Modify: `pkg/cluster/config_test.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/observer_hooks_test.go`
- Modify: `pkg/cluster/hashslot_migration.go` only if loop extraction needs a small helper

- [ ] **Step 1: Write the failing loop/cadence tests**

Add focused tests for:
- `TestControllerPlannerRunsOnDirtyWake`
- `TestControllerPlannerSafetyTickStillEvaluatesWhenNoHintArrives`
- `TestIdleClusterSkipsMigrationObserveWithoutActiveWork`
- `TestClusterTimeoutDefaultsIncludeSlowSyncAndPlannerSafetyIntervals`

- [ ] **Step 2: Run the focused loop/cadence tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestControllerPlannerRunsOnDirtyWake|TestControllerPlannerSafetyTickStillEvaluatesWhenNoHintArrives|TestIdleClusterSkipsMigrationObserveWithoutActiveWork|TestClusterTimeoutDefaultsIncludeSlowSyncAndPlannerSafetyIntervals' -count=1`

Expected: FAIL because planner work still sits behind the old periodic controller observation loop and the new timing knobs do not exist.

- [ ] **Step 3: Implement split loops and dirty planner scheduling**

In `pkg/cluster/config.go` and `pkg/cluster/readiness.go`:
- add explicit timeouts/accessors for at least:
  - slow-sync interval
  - planner safety interval
  - planner wake debounce
- keep defaults conservative and cluster-safe

In `pkg/cluster/controller_host.go`:
- mark planner dirty when assignments, tasks, runtime reports, task results, migration status, or leader ownership change
- debounce planner wakeups

In `pkg/cluster/cluster.go`:
- split the old hot observation loop into:
  - wake-driven reconcile loop
  - slow-sync loop
  - planner loop with safety tick
  - migration progress loop that sleeps when there is no active migration

- [ ] **Step 4: Re-run the focused loop/cadence tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestControllerPlannerRunsOnDirtyWake|TestControllerPlannerSafetyTickStillEvaluatesWhenNoHintArrives|TestIdleClusterSkipsMigrationObserveWithoutActiveWork|TestClusterTimeoutDefaultsIncludeSlowSyncAndPlannerSafetyIntervals' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the loop split and planner wakeups**

Run:
```bash
git add pkg/cluster/cluster.go pkg/cluster/controller_host.go pkg/cluster/config.go pkg/cluster/readiness.go pkg/cluster/config_test.go pkg/cluster/cluster_test.go pkg/cluster/observer_hooks_test.go pkg/cluster/hashslot_migration.go
git commit -m "feat: split controller observation loops"
```

### Task 6: Update flow docs and run focused package verification

**Files:**
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Update `pkg/cluster/FLOW.md`**

Document:
- observation hints
- `fetch_observation_delta`
- wake-driven reconcile
- slow-sync fallback
- planner dirty wakeup + safety tick
- migration progress isolation

- [ ] **Step 2: Run the focused end-to-end cluster suite**

Run: `go test ./pkg/cluster -run 'Test(ObservationSyncState|ControllerCodecObservationDeltaRoundTrip|ControllerHandlerFetchObservationDelta|ObservationHint|ObserveOnceUsesObservationDeltaBeforeSeparateListReads|WakeReconcileLoopSingleflightsConcurrentHints|SlowSyncLoopRecoversDroppedHint|ControllerPlannerRunsOnDirtyWake|ControllerPlannerSafetyTickStillEvaluatesWhenNoHintArrives|IdleClusterSkipsMigrationObserveWithoutActiveWork)' -count=1`

Expected: PASS.

- [ ] **Step 3: Commit the flow docs and focused verification**

Run:
```bash
git add pkg/cluster/FLOW.md
git commit -m "docs: describe observation hint and delta flow"
```

### Task 7: Run full verification and runtime acceptance checks

**Files:**
- No new source files expected in this task; only fix fallout discovered by verification.

- [ ] **Step 1: Run the full `pkg/cluster` package tests**

Run: `go test ./pkg/cluster -count=1`

Expected: PASS.

- [ ] **Step 2: Rebuild the three-node docker-compose cluster**

Run: `docker compose up --build -d`

Expected: all three `wk-node*` containers restart cleanly on the new image.

- [ ] **Step 3: Measure idle RPC deltas and CPU after rollout**

Run:
```bash
python3 - <<'PY'
import subprocess, time, re, urllib.request

containers = [
    'wukongim-v31-wk-node1-1',
    'wukongim-v31-wk-node2-1',
    'wukongim-v31-wk-node3-1',
]
print('--- STATS ---')
subprocess.run(['docker','stats','--no-stream','--format','table {{.Name}}\\t{{.CPUPerc}}\\t{{.MemUsage}}',*containers], check=True)

ports = [5001,5002,5003]
keys = ['list_assignments','list_nodes','list_runtime_views','list_tasks','get_task','heartbeat','runtime_report']
pat = re.compile(r'^wukongim_transport_rpc_duration_seconds_count\\{[^}]*service=\"([^\"]+)\"[^}]*\\}\\s+([0-9.eE+-]+)$')

def snap(port):
    out = {}
    with urllib.request.urlopen(f'http://127.0.0.1:{port}/metrics', timeout=5) as resp:
        for raw in resp.read().decode().splitlines():
            m = pat.match(raw)
            if not m:
                continue
            service, val = m.groups()
            if service in keys:
                out[service] = float(val)
    return out

before = {p: snap(p) for p in ports}
time.sleep(5)
after = {p: snap(p) for p in ports}
print('\\n--- RPC DELTA (5s) ---')
for p in ports:
    delta = {}
    for k in keys:
        v = after[p].get(k, 0.0) - before[p].get(k, 0.0)
        if abs(v) > 1e-9:
            delta[k] = int(v) if abs(v - round(v)) < 1e-9 else v
    print(f'port {p} {delta}')
PY
```

Expected:
- steady-state `list_assignments`, `list_nodes`, `list_runtime_views`, and `list_tasks` drop sharply versus the current post-optimization baseline
- `get_task` remains near zero
- idle CPU drops below the current `~4.5% ~ 7%` range

- [ ] **Step 4: Capture fresh idle CPU profiles**

Run:
```bash
go tool pprof -top -nodecount=40 'http://127.0.0.1:5002/debug/pprof/profile?seconds=20'
go tool pprof -top -nodecount=40 'http://127.0.0.1:5003/debug/pprof/profile?seconds=20'
```

Expected:
- `pkg/cluster.(*Cluster).observeOnce` is no longer a hot steady-state path
- controller handler / transport read-write idle cost drops materially relative to the prior baseline

- [ ] **Step 5: Commit any verification fallout fixes**

If verification requires code changes, commit them with a focused message such as:

```bash
git add <files>
git commit -m "fix: stabilize observation hint delta rollout"
```
