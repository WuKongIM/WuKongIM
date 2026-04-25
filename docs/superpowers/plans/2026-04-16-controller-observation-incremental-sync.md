# Controller Observation Incremental Sync Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace steady-state per-slot observation spam with `NodeHeartbeat` low-frequency keepalive plus `RuntimeView` incremental sync so idle controller CPU falls with slot count while leader warmup and repair semantics stay correct.

**Architecture:** Keep durable controller metadata unchanged, but split controller observation into two RPC channels: `heartbeat` for node liveness and hash slot version exchange, and `runtime_report` for batched incremental runtime view updates. Add a node-local `runtimeObservationReporter` that tracks dirty slot views and tombstones, extend the leader-local `observationCache` to own per-node runtime snapshots, and tighten controller warmup so planning waits for fresh runtime full sync after leader change.

**Tech Stack:** Go 1.23, existing `pkg/cluster` controller client/handler/host wiring, existing `pkg/controller/meta` runtime view structs, existing `multiraft.Runtime.Status` cached snapshots, repo config loader in `cmd/wukongim`.

---

## File Structure Map

- Modify: `pkg/cluster/config.go` — add cadence defaults for heartbeat, runtime scan, runtime flush debounce, and runtime full sync recovery interval.
- Modify: `pkg/cluster/config_test.go` — cover new defaults and explicit timeout preservation.
- Modify: `cmd/wukongim/config.go` — parse new `WK_CLUSTER_*` cadence keys into `raftcluster.Timeouts`.
- Modify: `cmd/wukongim/config_test.go` — assert config parsing populates the new cadence fields.
- Modify: `wukongim.conf.example` — document the new cadence keys so runtime config and sample config stay aligned.
- Modify: `pkg/cluster/codec_control.go` — add `controllerRPCRuntimeReport` request kind plus encode/decode support for batched runtime observation payloads.
- Modify: `pkg/cluster/codec_control_test.go` — round-trip runtime report payloads and response kinds.
- Modify: `pkg/cluster/controller_client.go` — add runtime report RPC support and leader-change callback hooks that can request a runtime full sync after redirect.
- Modify: `pkg/cluster/controller_client_internal_test.go` — cover runtime report redirect/fallthrough behavior.
- Modify: `pkg/cluster/cluster_test.go` — extend `fakeControllerClient` and cluster-level tests for new observation flows.
- Create: `pkg/cluster/runtime_observation_reporter.go` — node-local runtime mirror, dirty tracking, tombstones, full-sync flagging, cadence checks, and flush logic.
- Create: `pkg/cluster/runtime_observation_reporter_test.go` — unit tests for diffing, failure retention, tombstones, and leader-change full sync behavior.
- Modify: `pkg/cluster/agent.go` — make heartbeat node-only and delegate runtime observation to the new reporter.
- Modify: `pkg/cluster/cluster.go` — start/stop separate heartbeat/runtime observation loops, wire reporter lifecycle, and update planner warmup usage.
- Modify: `pkg/cluster/readiness.go` — keep runtime-view shaping helpers centralized for reporter diffing and tests.
- Modify: `pkg/cluster/agent_internal_integration_test.go` — preserve reconcile behavior while observation transport changes.
- Modify: `pkg/cluster/observation_cache.go` — store runtime views per reporting node, support incremental upsert/delete, full-sync replacement, and coarse TTL eviction.
- Modify: `pkg/cluster/observation_cache_test.go` — cover per-node replacement, tombstone delete, and stale eviction.
- Modify: `pkg/cluster/controller_host.go` — own runtime warmup bookkeeping, runtime full-sync coverage, and cache eviction hooks across leader changes.
- Modify: `pkg/cluster/controller_host_test.go` — verify warmup reset/rebuild semantics and cache lifecycle on leader changes.
- Modify: `pkg/cluster/controller_handler.go` — handle `runtime_report` on the leader without Raft proposal; keep `heartbeat` limited to node liveness/hash slot versioning.
- Modify: `pkg/cluster/controller_handler_test.go` — cover follower redirect, leader-local cache updates, and node-only heartbeat behavior.
- Modify: `pkg/cluster/cluster_integration_test.go` — verify runtime observations still reach the controller and leader warmup completes only after runtime full sync.
- Modify: `pkg/cluster/FLOW.md` — document the split heartbeat/runtime observation flow and warmup gating semantics.

### Task 1: Add cadence knobs and runtime report protocol surface

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/config_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: `pkg/cluster/codec_control.go`
- Modify: `pkg/cluster/codec_control_test.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`
- Modify: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Write the failing config and codec tests**

Add focused tests for:
- `TestConfigApplyDefaultsIncludesObservationCadence`
- `TestConfigApplyDefaultsPreservesExplicitObservationCadence`
- `TestLoadConfigParsesObservationCadence`
- `TestControllerCodecRuntimeObservationReportRoundTrip`
- `TestControllerClientRuntimeReportFallsThroughRedirectToCurrentLeader`

The codec test should round-trip a batched payload with `FullSync=true`, multiple `Views`, and `ClosedSlots`.

- [ ] **Step 2: Run the focused tests to verify RED**

Run:
```bash
go test ./pkg/cluster -run 'TestConfigApplyDefaultsIncludesObservationCadence|TestConfigApplyDefaultsPreservesExplicitObservationCadence|TestControllerCodecRuntimeObservationReportRoundTrip|TestControllerClientRuntimeReportFallsThroughRedirectToCurrentLeader' -count=1 && go test ./cmd/wukongim -run 'TestLoadConfigParsesObservationCadence' -count=1
```

Expected: FAIL because the new timeout fields, env parsing, and `runtime_report` codec/client surface do not exist yet.

- [ ] **Step 3: Implement the minimal config + protocol scaffolding**

In `pkg/cluster/config.go`, extend `Timeouts` with:

```go
ObservationHeartbeatInterval      time.Duration
ObservationRuntimeScanInterval    time.Duration
ObservationRuntimeFlushDebounce   time.Duration
ObservationRuntimeFullSyncInterval time.Duration
```

Add default values matching the approved design (`2s`, `1s`, `100-200ms`, `60s`).

In `cmd/wukongim/config.go`, parse:
- `WK_CLUSTER_OBSERVATION_HEARTBEAT_INTERVAL`
- `WK_CLUSTER_OBSERVATION_RUNTIME_SCAN_INTERVAL`
- `WK_CLUSTER_OBSERVATION_RUNTIME_FLUSH_DEBOUNCE`
- `WK_CLUSTER_OBSERVATION_RUNTIME_FULL_SYNC_INTERVAL`

Wire those values into `raftcluster.Timeouts`, update `cmd/wukongim/config_test.go`, and document the keys in `wukongim.conf.example`.

In `pkg/cluster/codec_control.go`, add:

```go
const controllerRPCRuntimeReport = "runtime_report"

type runtimeObservationReport struct {
    NodeID      uint64
    ObservedAt  time.Time
    FullSync    bool
    Views       []controllermeta.SlotRuntimeView
    ClosedSlots []uint32
}
```

Add request encoding/decoding support and a `controllerAPI` / `controllerClient` method dedicated to runtime reports. Update the fake controller client in `pkg/cluster/cluster_test.go` and add the redirect/fallthrough coverage in `pkg/cluster/controller_client_internal_test.go`.

- [ ] **Step 4: Run the focused tests to verify GREEN**

Run:
```bash
go test ./pkg/cluster -run 'TestConfigApplyDefaultsIncludesObservationCadence|TestConfigApplyDefaultsPreservesExplicitObservationCadence|TestControllerCodecRuntimeObservationReportRoundTrip|TestControllerClientRuntimeReportFallsThroughRedirectToCurrentLeader' -count=1 && go test ./cmd/wukongim -run 'TestLoadConfigParsesObservationCadence' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the cadence/protocol scaffolding**

Run:
```bash
git add pkg/cluster/config.go pkg/cluster/config_test.go cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example pkg/cluster/codec_control.go pkg/cluster/codec_control_test.go pkg/cluster/controller_client.go pkg/cluster/controller_client_internal_test.go pkg/cluster/cluster_test.go
git commit -m "feat: add controller runtime observation protocol"
```

### Task 2: Build the node-local runtime observation reporter

**Files:**
- Create: `pkg/cluster/runtime_observation_reporter.go`
- Create: `pkg/cluster/runtime_observation_reporter_test.go`
- Modify: `pkg/cluster/readiness.go`
- Modify: `pkg/cluster/agent.go`

- [ ] **Step 1: Write the failing reporter unit tests**

Add tests for:
- `TestRuntimeObservationReporterFlushesDirtyViewsOnly`
- `TestRuntimeObservationReporterQueuesClosedSlots`
- `TestRuntimeObservationReporterRetainsDirtyAfterSendFailure`
- `TestRuntimeObservationReporterRequestsFullSyncAfterLeaderChange`
- `TestRuntimeObservationReporterIdleTickSkipsRuntimeRPC`

The tests should use fake runtime snapshots and a fake send function so they can assert exactly which `Views`, `ClosedSlots`, and `FullSync` flags are emitted.

- [ ] **Step 2: Run the focused tests to verify RED**

Run:
```bash
go test ./pkg/cluster -run 'TestRuntimeObservationReporterFlushesDirtyViewsOnly|TestRuntimeObservationReporterQueuesClosedSlots|TestRuntimeObservationReporterRetainsDirtyAfterSendFailure|TestRuntimeObservationReporterRequestsFullSyncAfterLeaderChange|TestRuntimeObservationReporterIdleTickSkipsRuntimeRPC' -count=1
```

Expected: FAIL because `runtimeObservationReporter` does not exist.

- [ ] **Step 3: Implement the minimal reporter**

Create `pkg/cluster/runtime_observation_reporter.go` with a focused type such as:

```go
type runtimeObservationReporter struct {
    mirror       map[uint32]controllermeta.SlotRuntimeView
    dirtyViews   map[uint32]controllermeta.SlotRuntimeView
    closedSlots  map[uint32]struct{}
    needFullSync bool
}
```

Give it helpers to:
- snapshot current local runtime views from `runtime.Slots()` + `runtime.Status(slotID)`
- diff against the last acknowledged mirror
- mark explicit slot closures
- debounce flushes
- retain dirty state on send failure
- request a `FullSync` after controller leader change or redirect

Keep `buildRuntimeView(...)` in `pkg/cluster/readiness.go` as the one place that shapes comparable runtime views. Update `pkg/cluster/agent.go` so node heartbeat and runtime-report generation are no longer bundled into the same method.

- [ ] **Step 4: Run the focused tests to verify GREEN**

Run:
```bash
go test ./pkg/cluster -run 'TestRuntimeObservationReporterFlushesDirtyViewsOnly|TestRuntimeObservationReporterQueuesClosedSlots|TestRuntimeObservationReporterRetainsDirtyAfterSendFailure|TestRuntimeObservationReporterRequestsFullSyncAfterLeaderChange|TestRuntimeObservationReporterIdleTickSkipsRuntimeRPC' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the reporter foundation**

Run:
```bash
git add pkg/cluster/runtime_observation_reporter.go pkg/cluster/runtime_observation_reporter_test.go pkg/cluster/readiness.go pkg/cluster/agent.go
git commit -m "feat: add runtime observation reporter"
```

### Task 3: Split heartbeat and runtime observation on the node side

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/agent_internal_integration_test.go`

- [ ] **Step 1: Write the failing node-side integration tests**

Add tests for:
- `TestSlotAgentHeartbeatOnceSendsNodeHeartbeatOnly`
- `TestClusterRuntimeObservationLoopSkipsIdleFlush`
- `TestClusterRuntimeObservationLoopSendsCloseTombstone`
- `TestControllerClientRuntimeReportRedirectMarksReporterForFullSync`

The idle test should assert that repeated runtime observation ticks do not produce controller RPC calls when no slot state changed.

- [ ] **Step 2: Run the focused tests to verify RED**

Run:
```bash
go test ./pkg/cluster -run 'TestSlotAgentHeartbeatOnceSendsNodeHeartbeatOnly|TestClusterRuntimeObservationLoopSkipsIdleFlush|TestClusterRuntimeObservationLoopSendsCloseTombstone|TestControllerClientRuntimeReportRedirectMarksReporterForFullSync' -count=1
```

Expected: FAIL because `Cluster` still has a single observation loop and runtime views are still sent from the heartbeat path.

- [ ] **Step 3: Wire the split loops and leader-change lifecycle**

In `pkg/cluster/cluster.go`:
- keep a low-frequency heartbeat loop that only sends node heartbeat
- add a separate runtime observation loop that calls the reporter
- stop both loops in `Cluster.Stop()`

Use the new cadence helpers instead of `ControllerObservation` for these loops.

In `pkg/cluster/controller_client.go`, add a narrow callback so redirects / leader-cache updates can tell the reporter to request `FullSync=true` on the next runtime flush.

In `pkg/cluster/agent_internal_integration_test.go`, keep the reconcile path intact while exercising the new observation transport behavior.

- [ ] **Step 4: Run the focused tests to verify GREEN**

Run:
```bash
go test ./pkg/cluster -run 'TestSlotAgentHeartbeatOnceSendsNodeHeartbeatOnly|TestClusterRuntimeObservationLoopSkipsIdleFlush|TestClusterRuntimeObservationLoopSendsCloseTombstone|TestControllerClientRuntimeReportRedirectMarksReporterForFullSync' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the node-side observation split**

Run:
```bash
git add pkg/cluster/cluster.go pkg/cluster/controller_client.go pkg/cluster/controller_client_internal_test.go pkg/cluster/cluster_test.go pkg/cluster/agent_internal_integration_test.go
git commit -m "feat: split heartbeat and runtime observation loops"
```

### Task 4: Ingest runtime reports on the controller leader and restructure observation cache

**Files:**
- Modify: `pkg/cluster/observation_cache.go`
- Modify: `pkg/cluster/observation_cache_test.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/controller_host_test.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_handler_test.go`
- Modify: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Write the failing leader-side cache/handler tests**

Add tests for:
- `TestObservationCacheApplyRuntimeReportFullSyncReplacesNodeViews`
- `TestObservationCacheApplyRuntimeReportIncrementalUpsertsAndDeletes`
- `TestObservationCacheEvictsStaleRuntimeViews`
- `TestControllerHandlerRuntimeReportRedirectsFollower`
- `TestControllerHandlerRuntimeReportUpdatesLeaderObservationWithoutProposal`
- `TestControllerHandlerHeartbeatUpdatesNodeObservationOnly`

- [ ] **Step 2: Run the focused tests to verify RED**

Run:
```bash
go test ./pkg/cluster -run 'TestObservationCacheApplyRuntimeReportFullSyncReplacesNodeViews|TestObservationCacheApplyRuntimeReportIncrementalUpsertsAndDeletes|TestObservationCacheEvictsStaleRuntimeViews|TestControllerHandlerRuntimeReportRedirectsFollower|TestControllerHandlerRuntimeReportUpdatesLeaderObservationWithoutProposal|TestControllerHandlerHeartbeatUpdatesNodeObservationOnly' -count=1
```

Expected: FAIL because the cache is still keyed as one flat runtime-view map and the handler has no `runtime_report` branch.

- [ ] **Step 3: Implement per-node runtime snapshots and runtime-report handling**

In `pkg/cluster/observation_cache.go`, replace the flat runtime-view storage with per-node ownership, for example:

```go
type observationCache struct {
    nodes              map[uint64]nodeObservation
    runtimeViewsByNode map[uint64]map[uint32]controllermeta.SlotRuntimeView
}
```

Add methods to:
- apply node heartbeats without touching runtime views
- apply runtime incremental upserts / deletes
- replace one node's runtime snapshot on `FullSync=true`
- evict stale runtime views using the configured full-sync recovery interval

In `pkg/cluster/controller_handler.go`, add `controllerRPCRuntimeReport` handling on the leader and keep `controllerRPCHeartbeat` strictly node-focused. Update `pkg/cluster/controller_host.go` to expose the right cache hooks.

- [ ] **Step 4: Run the focused tests to verify GREEN**

Run:
```bash
go test ./pkg/cluster -run 'TestObservationCacheApplyRuntimeReportFullSyncReplacesNodeViews|TestObservationCacheApplyRuntimeReportIncrementalUpsertsAndDeletes|TestObservationCacheEvictsStaleRuntimeViews|TestControllerHandlerRuntimeReportRedirectsFollower|TestControllerHandlerRuntimeReportUpdatesLeaderObservationWithoutProposal|TestControllerHandlerHeartbeatUpdatesNodeObservationOnly' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the leader-side ingestion changes**

Run:
```bash
git add pkg/cluster/observation_cache.go pkg/cluster/observation_cache_test.go pkg/cluster/controller_host.go pkg/cluster/controller_host_test.go pkg/cluster/controller_handler.go pkg/cluster/controller_handler_test.go pkg/cluster/cluster_test.go
git commit -m "feat: ingest incremental runtime observation reports"
```

### Task 5: Tighten warmup gating and preserve planning semantics

**Files:**
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/controller_host_test.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/cluster_integration_test.go`

- [ ] **Step 1: Write the failing warmup/planner tests**

Add tests for:
- `TestControllerHostWarmupRequiresRuntimeFullSync`
- `TestControllerHostLeaderChangeResetsRuntimeWarmupCoverage`
- `TestControllerTickOnceSkipsPlanningUntilRuntimeWarm`
- `TestControllerTickOnceRunsAfterRuntimeFullSync`
- `TestClusterReportsRuntimeViewsToControllerIncrementally`

The incremental integration test should prove that the controller still learns runtime views, but not through per-slot heartbeat spam.

- [ ] **Step 2: Run the focused tests to verify RED**

Run:
```bash
go test ./pkg/cluster -run 'TestControllerHostWarmupRequiresRuntimeFullSync|TestControllerHostLeaderChangeResetsRuntimeWarmupCoverage|TestControllerTickOnceSkipsPlanningUntilRuntimeWarm|TestControllerTickOnceRunsAfterRuntimeFullSync|TestClusterReportsRuntimeViewsToControllerIncrementally' -count=1
```

Expected: FAIL because warmup currently flips ready on any observation and planning does not wait for runtime full-sync coverage.

- [ ] **Step 3: Implement runtime-aware warmup coverage**

In `pkg/cluster/controller_host.go`, extend warmup bookkeeping so the leader tracks which alive nodes have delivered a fresh runtime full sync in the current term.

In `pkg/cluster/cluster.go`:
- make `controllerTickOnce(...)` skip planning until runtime warmup is satisfied
- continue to source runtime views from leader-local observation snapshots
- preserve existing fail-closed behavior when fresh runtime observation has not arrived yet

Update the integration harness so runtime reports still satisfy `ListObservedRuntimeViews(...)` and repair/rebalance logic after warmup.

- [ ] **Step 4: Run the focused tests to verify GREEN**

Run:
```bash
go test ./pkg/cluster -run 'TestControllerHostWarmupRequiresRuntimeFullSync|TestControllerHostLeaderChangeResetsRuntimeWarmupCoverage|TestControllerTickOnceSkipsPlanningUntilRuntimeWarm|TestControllerTickOnceRunsAfterRuntimeFullSync|TestClusterReportsRuntimeViewsToControllerIncrementally' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the warmup/planner gating changes**

Run:
```bash
git add pkg/cluster/controller_host.go pkg/cluster/controller_host_test.go pkg/cluster/cluster.go pkg/cluster/cluster_test.go pkg/cluster/cluster_integration_test.go
git commit -m "feat: gate controller warmup on runtime full sync"
```

### Task 6: Update flow docs and verify the real idle CPU win

**Files:**
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Update the flow documentation**

Document:
- node-only heartbeat cadence
- runtime incremental report cadence and full-sync recovery behavior
- leader-side per-node observation cache semantics
- runtime-full-sync warmup gate before planner decisions

- [ ] **Step 2: Run focused package tests**

Run:
```bash
go test ./pkg/cluster ./pkg/controller/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Rebuild the local three-node compose cluster from current main**

Run:
```bash
DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 docker compose build wk-node1 wk-node2 wk-node3 && docker compose up -d wk-node1 wk-node2 wk-node3
```

Expected: PASS with all three node containers recreated and healthy.

- [ ] **Step 4: Re-run the idle CPU and pprof verification**

Run:
```bash
for i in 1 2 3 4 5; do docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' wukongim-v31-wk-node1-1 wukongim-v31-wk-node2-1 wukongim-v31-wk-node3-1; echo '---'; sleep 2; done
go tool pprof -top -cum -nodecount=80 'http://127.0.0.1:5001/debug/pprof/profile?seconds=30'
go tool pprof -top -cum -nodecount=60 'http://127.0.0.1:5002/debug/pprof/profile?seconds=20'
```

Expected:
- idle CPU lower than the current `node1 ~6-7%`, `node2 ~4-5%`, `node3 ~4-5%` baseline
- no more steady-state `tick × slot` observation spam in the leader profile

- [ ] **Step 5: Review the diff and finalize**

Run:
```bash
git status --short
git log --oneline --decorate -n 8
```

Expected: only the planned files are modified, tests are green, docs match the implementation, and the branch is ready for `superpowers:executing-plans`.
