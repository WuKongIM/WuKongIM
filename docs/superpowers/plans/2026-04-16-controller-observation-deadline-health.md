# Controller Observation and Deadline-Driven Health Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move controller observation off the Raft hot path, replace fixed-period timeout proposals with deadline-driven node health transitions, and keep repair / rebalance decisions correct across leader changes.

**Architecture:** Add a leader-local `observationCache` and `nodeHealthScheduler` under `pkg/cluster`, keep durable node status and control decisions in controller Raft, and change planner / API reads to combine durable controller state with leader-local observation snapshots. The implementation is phased so the cluster first stops proposing raw observation, then gains edge-driven health updates, then removes `EvaluateTimeouts` from steady-state control flow.

**Tech Stack:** Go 1.23, etcd/raft v3, Pebble, existing `pkg/cluster` controller host / handler / planner wiring, existing `pkg/controller/raft` service, existing `pkg/controller/plane` state machine.

---

## File Structure

- Create: `pkg/cluster/observation_cache.go` — leader-local node and slot observation cache plus immutable snapshot helpers.
- Create: `pkg/cluster/observation_cache_test.go` — unit tests for freshness, de-duplication, and snapshot behavior.
- Create: `pkg/cluster/node_health_scheduler.go` — deadline-driven health scheduler that emits only edge proposals.
- Create: `pkg/cluster/node_health_scheduler_test.go` — unit tests for deadline refresh, generation invalidation, and edge-only proposals.
- Modify: `pkg/cluster/controller_host.go` — own the observation cache, scheduler, and leader warmup lifecycle.
- Modify: `pkg/cluster/controller_handler.go` — route heartbeat reports into leader-local observation instead of controller Raft proposals.
- Modify: `pkg/cluster/cluster.go` — consume observation snapshots in planner state, remove steady-state `EvaluateTimeouts` proposal, wire warmup-aware planning.
- Modify: `pkg/cluster/readiness.go` — keep `SlotRuntimeView` shaping consistent for leader-local observation snapshots.
- Modify: `pkg/cluster/api.go` — document / preserve `ListObservedRuntimeViews` semantics as leader-observed, not store-backed.
- Modify: `pkg/cluster/controller_host_test.go` — cover host-owned observation lifecycle and leader transitions.
- Modify: `pkg/cluster/controller_handler_test.go` — cover redirect vs leader-local cache update behavior.
- Modify: `pkg/cluster/cluster_test.go` — cover planner snapshot composition, warmup gating, and query-path changes.
- Modify: `pkg/cluster/agent_internal_integration_test.go` — preserve assignment / runtime-view integration semantics after cache switch.
- Modify: `pkg/controller/raft/config.go` — add lightweight commit / leader-change observer hooks required by the scheduler.
- Modify: `pkg/controller/raft/service.go` — publish leader-change and committed-command callbacks without changing Raft semantics.
- Modify: `pkg/controller/raft/service_test.go` — verify hooks fire only after commit / leader transitions.
- Modify: `pkg/controller/plane/commands.go` — add explicit node health edge command payloads.
- Modify: `pkg/controller/plane/statemachine.go` — apply `NodeStatusUpdate` deltas and retire steady-state timeout scanning.
- Modify: `pkg/controller/plane/controller_test.go` — cover node status delta application and planner behavior with runtime supplied externally.
- Modify: `pkg/controller/FLOW.md` — update controller flow to describe leader-local observation and edge-driven status replication.
- Modify: `pkg/cluster/FLOW.md` — update observation loop, controller heartbeat handling, planner inputs, and health scheduling flow.

### Task 1: Observation cache foundation

**Files:**
- Create: `pkg/cluster/observation_cache.go`
- Create: `pkg/cluster/observation_cache_test.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/controller_host_test.go`

- [ ] **Step 1: Write the failing observation cache tests**

Add tests in `pkg/cluster/observation_cache_test.go` for:
- `TestObservationCacheUpsertNodeReportKeepsNewestObservation`
- `TestObservationCacheUpsertRuntimeViewDeduplicatesUnchangedView`
- `TestObservationCacheSnapshotReturnsStableCopies`

The tests should assert that older timestamps do not overwrite newer values and snapshots do not alias internal slices.

- [ ] **Step 2: Run the focused tests to confirm failure**

Run: `go test ./pkg/cluster -run 'TestObservationCacheUpsertNodeReportKeepsNewestObservation|TestObservationCacheUpsertRuntimeViewDeduplicatesUnchangedView|TestObservationCacheSnapshotReturnsStableCopies' -count=1`

Expected: FAIL because `observationCache` does not exist yet.

- [ ] **Step 3: Implement `observationCache` and host ownership**

In `pkg/cluster/observation_cache.go`, add:
- an `observationCache` type with internal maps for node observations and slot runtime views
- `applyNodeReport(...)`, `applyRuntimeView(...)`, and `snapshot()` methods
- immutable snapshot structs that copy slices before returning them

In `pkg/cluster/controller_host.go`, add fields for the cache and construct them in `newControllerHost(...)` so future tasks can depend on a single host-owned cache instance.

- [ ] **Step 4: Run the focused tests to confirm they pass**

Run: `go test ./pkg/cluster -run 'TestObservationCacheUpsertNodeReportKeepsNewestObservation|TestObservationCacheUpsertRuntimeViewDeduplicatesUnchangedView|TestObservationCacheSnapshotReturnsStableCopies' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the cache foundation**

Run:
```bash
git add pkg/cluster/observation_cache.go pkg/cluster/observation_cache_test.go pkg/cluster/controller_host.go pkg/cluster/controller_host_test.go
git commit -m "feat: add controller observation cache"
```

### Task 2: Move heartbeat and runtime view handling off controller Raft

**Files:**
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_handler_test.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/readiness.go`

- [ ] **Step 1: Write failing tests for leader-local observation handling**

Add tests covering:
- `TestControllerHandlerHeartbeatRedirectsFollower`
- `TestControllerHandlerHeartbeatUpdatesLeaderObservationWithoutProposal`
- `TestListObservedRuntimeViewsReadsLeaderObservationSnapshot`

The leader test must assert that handling `controllerRPCHeartbeat` updates the cache and does not require a successful `controller.Propose(...)` call.

- [ ] **Step 2: Run the focused tests to confirm failure**

Run: `go test ./pkg/cluster -run 'TestControllerHandlerHeartbeatRedirectsFollower|TestControllerHandlerHeartbeatUpdatesLeaderObservationWithoutProposal|TestListObservedRuntimeViewsReadsLeaderObservationSnapshot' -count=1`

Expected: FAIL because the handler still proposes `CommandKindNodeHeartbeat` and `ListObservedRuntimeViews` still reads `controllerMeta`.

- [ ] **Step 3: Implement leader-local heartbeat ingestion and query reads**

Change `pkg/cluster/controller_handler.go` so `controllerRPCHeartbeat`:
- still redirects on followers
- updates the leader host's `observationCache`
- refreshes the outgoing hash slot table response
- no longer calls `c.controller.Propose(...)`

Change `pkg/cluster/cluster.go` / `pkg/cluster/api.go` so `ListObservedRuntimeViews(...)` reads from the leader-local observation snapshot and no longer depends on `controllerMeta.ListRuntimeViews(...)` for the leader path.

Keep `pkg/cluster/readiness.go` as the single place that shapes `SlotRuntimeView` values so cache entries and tests use the same struct semantics.

- [ ] **Step 4: Run the focused tests to confirm they pass**

Run: `go test ./pkg/cluster -run 'TestControllerHandlerHeartbeatRedirectsFollower|TestControllerHandlerHeartbeatUpdatesLeaderObservationWithoutProposal|TestListObservedRuntimeViewsReadsLeaderObservationSnapshot' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the heartbeat-path change**

Run:
```bash
git add pkg/cluster/controller_handler.go pkg/cluster/controller_handler_test.go pkg/cluster/cluster.go pkg/cluster/api.go pkg/cluster/cluster_test.go pkg/cluster/readiness.go
git commit -m "feat: move controller observation off raft path"
```

### Task 3: Feed planner state from durable metadata plus leader-local observation

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/agent_internal_integration_test.go`

- [ ] **Step 1: Write failing tests for planner snapshot composition and warmup gating**

Add tests covering:
- `TestSnapshotPlannerStateUsesObservationSnapshotForRuntime`
- `TestControllerTickOnceSkipsPlanningDuringWarmup`
- `TestControllerTickOnceUsesObservationSnapshotAfterWarmup`

The warmup tests should verify that a newly elected leader does not create repair / rebalance work until fresh observation has arrived.

- [ ] **Step 2: Run the focused tests to confirm failure**

Run: `go test ./pkg/cluster -run 'TestSnapshotPlannerStateUsesObservationSnapshotForRuntime|TestControllerTickOnceSkipsPlanningDuringWarmup|TestControllerTickOnceUsesObservationSnapshotAfterWarmup' -count=1`

Expected: FAIL because planner runtime input still comes from `controllerMeta.ListRuntimeViews(...)` and there is no warmup concept.

- [ ] **Step 3: Implement combined planner state and warmup gating**

In `pkg/cluster/controller_host.go`, add leader-local warmup bookkeeping that can answer:
- whether the local controller is leader
- whether warmup is complete
- current observation snapshot

In `pkg/cluster/cluster.go`, change `snapshotPlannerState(...)` to:
- keep nodes / assignments / tasks from `controllerMeta`
- source runtime views from the leader-local observation snapshot
- leave runtime empty when the leader is not warmed up yet

Update `controllerTickOnce(...)` to skip repair / rebalance decisions during warmup while still allowing assignment / task decisions once fresh observation exists.

- [ ] **Step 4: Run the focused tests to confirm they pass**

Run: `go test ./pkg/cluster -run 'TestSnapshotPlannerStateUsesObservationSnapshotForRuntime|TestControllerTickOnceSkipsPlanningDuringWarmup|TestControllerTickOnceUsesObservationSnapshotAfterWarmup' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit planner-state changes**

Run:
```bash
git add pkg/cluster/cluster.go pkg/cluster/controller_host.go pkg/cluster/cluster_test.go pkg/cluster/agent_internal_integration_test.go
git commit -m "feat: drive controller planning from observed runtime state"
```

### Task 4: Add explicit node health edge commands and Raft callbacks

**Files:**
- Modify: `pkg/controller/plane/commands.go`
- Modify: `pkg/controller/plane/statemachine.go`
- Modify: `pkg/controller/plane/controller_test.go`
- Modify: `pkg/controller/raft/config.go`
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`

- [ ] **Step 1: Write failing tests for edge-command application and service callbacks**

Add tests covering:
- `TestStateMachineApplyNodeStatusUpdateAppliesExpectedTransition`
- `TestStateMachineApplyNodeStatusUpdateIgnoresMismatchedPriorStatus`
- `TestControllerRaftServicePublishesCommittedCommandHook`
- `TestControllerRaftServicePublishesLeaderChangeHook`

The command tests should use durable node records and assert only the targeted nodes are updated.

- [ ] **Step 2: Run the focused tests to confirm failure**

Run: `go test ./pkg/controller/plane -run 'TestStateMachineApplyNodeStatusUpdateAppliesExpectedTransition|TestStateMachineApplyNodeStatusUpdateIgnoresMismatchedPriorStatus' -count=1 && go test ./pkg/controller/raft -run 'TestControllerRaftServicePublishesCommittedCommandHook|TestControllerRaftServicePublishesLeaderChangeHook' -count=1`

Expected: FAIL because `NodeStatusUpdate` and service observer hooks do not exist yet.

- [ ] **Step 3: Implement node-status delta commands and Raft hooks**

In `pkg/controller/plane/commands.go`, add explicit node-status-update payload types, for example a batch of `{nodeID, newStatus, expectedStatus, evaluatedAt}`.

In `pkg/controller/plane/statemachine.go`, add apply logic that:
- loads only the referenced nodes
- validates optional expected prior status
- updates durable node status without scanning the entire node table

In `pkg/controller/raft/config.go` and `pkg/controller/raft/service.go`, add lightweight callbacks for:
- leader changes
- committed commands after successful apply

These hooks must be observational only; they must not change Raft correctness or message ordering.

- [ ] **Step 4: Run the focused tests to confirm they pass**

Run: `go test ./pkg/controller/plane -run 'TestStateMachineApplyNodeStatusUpdateAppliesExpectedTransition|TestStateMachineApplyNodeStatusUpdateIgnoresMismatchedPriorStatus' -count=1 && go test ./pkg/controller/raft -run 'TestControllerRaftServicePublishesCommittedCommandHook|TestControllerRaftServicePublishesLeaderChangeHook' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the node-status command layer**

Run:
```bash
git add pkg/controller/plane/commands.go pkg/controller/plane/statemachine.go pkg/controller/plane/controller_test.go pkg/controller/raft/config.go pkg/controller/raft/service.go pkg/controller/raft/service_test.go
git commit -m "feat: add controller node status update commands"
```

### Task 5: Add deadline-driven node health scheduling and remove steady-state `EvaluateTimeouts`

**Files:**
- Create: `pkg/cluster/node_health_scheduler.go`
- Create: `pkg/cluster/node_health_scheduler_test.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/controller/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Write failing tests for deadline refresh, edge-only proposals, and no idle timeout proposals**

Add tests covering:
- `TestNodeHealthSchedulerRefreshesDeadlinesOnObservation`
- `TestNodeHealthSchedulerIgnoresStaleGenerationWakeup`
- `TestNodeHealthSchedulerProposesOnlyOnStatusEdge`
- `TestControllerTickOnceDoesNotProposeEvaluateTimeouts`

The last test must prove the steady-state controller tick no longer emits a timeout proposal when nothing changed.

- [ ] **Step 2: Run the focused tests to confirm failure**

Run: `go test ./pkg/cluster -run 'TestNodeHealthSchedulerRefreshesDeadlinesOnObservation|TestNodeHealthSchedulerIgnoresStaleGenerationWakeup|TestNodeHealthSchedulerProposesOnlyOnStatusEdge|TestControllerTickOnceDoesNotProposeEvaluateTimeouts' -count=1`

Expected: FAIL because there is no scheduler and `controllerTickOnce(...)` still proposes `EvaluateTimeouts`.

- [ ] **Step 3: Implement `nodeHealthScheduler` and integrate it with the host lifecycle**

In `pkg/cluster/node_health_scheduler.go`, implement:
- per-node deadline tracking
- generation invalidation
- a single wake-up loop keyed to the nearest deadline
- callback / proposal emission for batched `NodeStatusUpdate` commands

In `pkg/cluster/controller_host.go`, wire the scheduler so that:
- leader acquisition starts or re-arms scheduling after warmup
- leader loss invalidates old timers
- committed node observation refreshes scheduler deadlines

In `pkg/cluster/cluster.go`, remove the unconditional `Propose(EvaluateTimeouts)` call from `controllerTickOnce(...)` and rely on scheduler-emitted edge proposals instead.

Update `pkg/controller/FLOW.md` and `pkg/cluster/FLOW.md` so the docs match the new observation + scheduling model.

- [ ] **Step 4: Run the focused tests to confirm they pass**

Run: `go test ./pkg/cluster -run 'TestNodeHealthSchedulerRefreshesDeadlinesOnObservation|TestNodeHealthSchedulerIgnoresStaleGenerationWakeup|TestNodeHealthSchedulerProposesOnlyOnStatusEdge|TestControllerTickOnceDoesNotProposeEvaluateTimeouts' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the scheduler migration**

Run:
```bash
git add pkg/cluster/node_health_scheduler.go pkg/cluster/node_health_scheduler_test.go pkg/cluster/controller_host.go pkg/cluster/cluster.go pkg/cluster/cluster_test.go pkg/controller/FLOW.md pkg/cluster/FLOW.md
git commit -m "feat: make controller health transitions deadline driven"
```

### Task 6: Full verification and regression sweep

**Files:**
- Test: `pkg/cluster/...`
- Test: `pkg/controller/raft/...`
- Test: `pkg/controller/plane/...`
- Test: `docker-compose.yml` runtime environment for manual verification

- [ ] **Step 1: Run focused package tests for all touched controller and cluster paths**

Run: `go test ./pkg/controller/plane ./pkg/controller/raft ./pkg/cluster -count=1`

Expected: PASS.

- [ ] **Step 2: Run the broader AGENTS.md-aligned regression sweep**

Run: `go test ./internal/... ./pkg/...`

Expected: PASS. If unrelated existing failures appear, record them separately and do not hide them.

- [ ] **Step 3: Re-profile the three-node compose cluster after rebuilding images**

Run:
```bash
docker compose up -d --build
sleep 15
go tool pprof -top -nodecount=30 'http://127.0.0.1:5001/debug/pprof/profile?seconds=20'
docker stats --no-stream --format 'table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' wukongim-v31-wk-node1-1 wukongim-v31-wk-node2-1 wukongim-v31-wk-node3-1
```

Expected:
- controller `CommittedEntries` pressure is materially lower
- controller CPU is materially lower at idle
- stopped-node recovery still transitions through `Suspect` / `Dead` and produces repair work after timeout

- [ ] **Step 4: Commit the final verified implementation**

Run:
```bash
git add pkg/cluster pkg/controller
git commit -m "feat: decouple controller observation from raft hot path"
```
