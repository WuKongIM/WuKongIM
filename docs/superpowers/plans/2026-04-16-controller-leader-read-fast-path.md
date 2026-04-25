# Controller Leader Read Fast Path Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove steady-state Pebble reads from controller-leader heartbeat handling and node health edge evaluation while preserving existing controller RPC and durable-state semantics.

**Architecture:** Add two leader-local committed-state mirrors under `pkg/cluster`: a hash slot table snapshot owned by `controllerHost`, and a durable node mirror owned by `nodeHealthScheduler`. Refresh both mirrors only from committed controller state or leader-rebuild paths, keep `controllerMeta` as the single source of truth, and fall back to store reads only on cold miss.

**Tech Stack:** Go 1.23, existing `pkg/cluster` controller host / handler / scheduler wiring, existing `pkg/controller/meta` Pebble store, existing `pkg/controller/plane` command kinds, existing `pkg/controller/raft` leader-change and committed-command hooks.

---

## File Structure

- Modify: `pkg/cluster/controller_host.go` — add the leader-local hash slot table snapshot, hash-slot refresh helpers, and scheduler mirror lifecycle wiring.
- Modify: `pkg/cluster/controller_handler.go` — serve `heartbeat` and `list_assignments` from the host snapshot first, then fall back to the store only on cold miss.
- Modify: `pkg/cluster/cluster.go` — seed the host snapshot during controller startup so the first leader term begins with a hot snapshot when possible.
- Modify: `pkg/cluster/node_health_scheduler.go` — add the durable node mirror, mirror reload / clear helpers, mirror-aware `proposeStatusTransition`, and committed-command refresh logic.
- Modify: `pkg/cluster/controller_host_test.go` — cover leader-change snapshot rebuild / clear behavior and committed-command-triggered hash-slot refresh.
- Modify: `pkg/cluster/controller_handler_test.go` — cover hash-slot fast path usage and cold-miss backfill behavior.
- Modify: `pkg/cluster/node_health_scheduler_test.go` — cover mirror hits, mirror misses with backfill, committed refresh, and reset behavior.
- Modify: `pkg/cluster/FLOW.md` — update the steady-state controller observation flow to describe the new read fast path.

### Task 1: Add controller-host hash slot snapshot lifecycle

**Files:**
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/controller_host_test.go`

- [ ] **Step 1: Write the failing host lifecycle tests**

Add tests in `pkg/cluster/controller_host_test.go` for:
- `TestControllerHostHashSlotSnapshotReloadsOnLocalLeaderChange`
- `TestControllerHostHashSlotSnapshotClearsOnLeaderLoss`
- `TestControllerHostHandleCommittedCommandReloadsHashSlotSnapshotOnHashSlotMutation`

Test setup should pre-populate controller meta with a known `HashSlotTable`, then assert the host snapshot is rebuilt or cleared strictly from leader-change / committed-command callbacks.

- [ ] **Step 2: Run the targeted tests to verify RED**

Run: `go test ./pkg/cluster -run 'TestControllerHostHashSlotSnapshotReloadsOnLocalLeaderChange|TestControllerHostHashSlotSnapshotClearsOnLeaderLoss|TestControllerHostHandleCommittedCommandReloadsHashSlotSnapshotOnHashSlotMutation' -count=1`

Expected: FAIL because `controllerHost` does not yet own any hash slot snapshot lifecycle.

- [ ] **Step 3: Implement the minimal host snapshot support**

In `pkg/cluster/controller_host.go`, add host-owned snapshot state and helpers like:

```go
func (h *controllerHost) hashSlotTableSnapshot() (*HashSlotTable, bool)
func (h *controllerHost) reloadHashSlotTableSnapshot(ctx context.Context) error
func (h *controllerHost) clearHashSlotTableSnapshot()
func shouldRefreshHashSlotSnapshot(cmd slotcontroller.Command) bool
```

Update `handleLeaderChange(...)` so:
- `to == h.localNode` reloads the committed hash slot table from `h.meta`
- `to != h.localNode` clears the local snapshot

Update `handleCommittedCommand(...)` so hash-slot-mutating commands refresh the snapshot after commit.

In `pkg/cluster/cluster.go`, seed the snapshot during `startControllerRaftIfLocalPeer()` after the existing `ensureControllerHashSlotTable(...)` startup load succeeds.

- [ ] **Step 4: Run the targeted tests to verify GREEN**

Run: `go test ./pkg/cluster -run 'TestControllerHostHashSlotSnapshotReloadsOnLocalLeaderChange|TestControllerHostHashSlotSnapshotClearsOnLeaderLoss|TestControllerHostHandleCommittedCommandReloadsHashSlotSnapshotOnHashSlotMutation' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the host snapshot lifecycle work**

Run:
```bash
git add pkg/cluster/controller_host.go pkg/cluster/cluster.go pkg/cluster/controller_host_test.go
git commit -m "feat: add controller hash slot snapshot cache"
```

### Task 2: Switch controller RPC reads to the host snapshot fast path

**Files:**
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_handler_test.go`
- Modify: `pkg/cluster/controller_host.go`

- [ ] **Step 1: Write the failing handler tests**

Add tests in `pkg/cluster/controller_handler_test.go` for:
- `TestControllerHandlerHeartbeatUsesHashSlotSnapshotWhenWarm`
- `TestControllerHandlerListAssignmentsUsesHashSlotSnapshotWhenWarm`
- `TestControllerHandlerHeartbeatBackfillsHashSlotSnapshotOnColdMiss`

Use a test host whose snapshot is either preloaded or intentionally empty. For the warm-path tests, arrange the store so any unexpected store read would fail the test.

- [ ] **Step 2: Run the targeted tests to verify RED**

Run: `go test ./pkg/cluster -run 'TestControllerHandlerHeartbeatUsesHashSlotSnapshotWhenWarm|TestControllerHandlerListAssignmentsUsesHashSlotSnapshotWhenWarm|TestControllerHandlerHeartbeatBackfillsHashSlotSnapshotOnColdMiss' -count=1`

Expected: FAIL because `controllerHandler` still calls `ensureControllerHashSlotTable(...)` on every heartbeat and assignment listing.

- [ ] **Step 3: Implement the minimal fast-path reads**

In `pkg/cluster/controller_handler.go`, replace the steady-state path with logic like:

```go
table, ok := c.controllerHost.hashSlotTableSnapshot()
if !ok {
    table, err = c.ensureControllerHashSlotTable(ctx, c.controllerMeta)
    if err != nil { ... }
    c.controllerHost.storeHashSlotTableSnapshot(table)
}
```

Use the same helper for:
- `controllerRPCHeartbeat`
- `controllerRPCListAssignments`

Keep response semantics unchanged:
- still return `HashSlotTableVersion`
- still return the encoded table only when the requester version is stale
- still redirect followers exactly as before

- [ ] **Step 4: Run the targeted tests to verify GREEN**

Run: `go test ./pkg/cluster -run 'TestControllerHandlerHeartbeatUsesHashSlotSnapshotWhenWarm|TestControllerHandlerListAssignmentsUsesHashSlotSnapshotWhenWarm|TestControllerHandlerHeartbeatBackfillsHashSlotSnapshotOnColdMiss' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the controller handler fast path**

Run:
```bash
git add pkg/cluster/controller_handler.go pkg/cluster/controller_handler_test.go pkg/cluster/controller_host.go
git commit -m "feat: add controller hash slot read fast path"
```

### Task 3: Add durable node mirror reads to `nodeHealthScheduler`

**Files:**
- Modify: `pkg/cluster/node_health_scheduler.go`
- Modify: `pkg/cluster/node_health_scheduler_test.go`

- [ ] **Step 1: Write the failing scheduler tests**

Add tests in `pkg/cluster/node_health_scheduler_test.go` for:
- `TestNodeHealthSchedulerUsesMirrorForRepeatedAliveObservation`
- `TestNodeHealthSchedulerMirrorMissLoadsNodeAndBackfills`
- `TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirror`
- `TestNodeHealthSchedulerResetClearsMirror`

For the repeated-Alive test, count `loadNode` invocations and assert that only the first read hits the store path while subsequent steady-state Alive observations use the mirror.

- [ ] **Step 2: Run the targeted tests to verify RED**

Run: `go test ./pkg/cluster -run 'TestNodeHealthSchedulerUsesMirrorForRepeatedAliveObservation|TestNodeHealthSchedulerMirrorMissLoadsNodeAndBackfills|TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirror|TestNodeHealthSchedulerResetClearsMirror' -count=1`

Expected: FAIL because the scheduler currently reads durable node state from `loadNode(...)` for every transition check and has no mirror lifecycle.

- [ ] **Step 3: Implement the minimal durable mirror support**

In `pkg/cluster/node_health_scheduler.go`, extend scheduler state with a durable mirror, for example:

```go
type nodeHealthScheduler struct {
    cfg        nodeHealthSchedulerConfig
    mu         sync.Mutex
    nodes      map[uint64]*nodeHealthState
    nodeMirror map[uint64]controllermeta.ClusterNode
}
```

Add helpers like:

```go
func (s *nodeHealthScheduler) mirrorNode(node controllermeta.ClusterNode)
func (s *nodeHealthScheduler) mirroredNode(nodeID uint64) (controllermeta.ClusterNode, bool)
func (s *nodeHealthScheduler) loadNodeForTransition(nodeID uint64) (controllermeta.ClusterNode, error)
```

Then update `proposeStatusTransition(...)` so it:
- uses a mirrored durable node when present
- falls back to `s.cfg.loadNode(...)` only on mirror miss
- backfills the mirror after a successful load

Update `reset()` to clear both timers and `nodeMirror`.

- [ ] **Step 4: Run the targeted tests to verify GREEN**

Run: `go test ./pkg/cluster -run 'TestNodeHealthSchedulerUsesMirrorForRepeatedAliveObservation|TestNodeHealthSchedulerMirrorMissLoadsNodeAndBackfills|TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirror|TestNodeHealthSchedulerResetClearsMirror' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the scheduler mirror read path**

Run:
```bash
git add pkg/cluster/node_health_scheduler.go pkg/cluster/node_health_scheduler_test.go
git commit -m "feat: add controller node health mirror"
```

### Task 4: Refresh the node mirror only from committed controller state

**Files:**
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/node_health_scheduler.go`
- Modify: `pkg/cluster/controller_host_test.go`
- Modify: `pkg/cluster/node_health_scheduler_test.go`

- [ ] **Step 1: Write the failing lifecycle tests**

Add tests for:
- `TestControllerHostLeaderChangeReloadsNodeMirrorOnLocalLeadership`
- `TestControllerHostLeaderChangeClearsNodeMirrorOnLeaderLoss`
- `TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForNodeStatusUpdate`
- `TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForOperatorRequest`

The node-status-update test should simulate a committed command whose payload could be a no-op and assert that the mirror is refreshed from store truth, not copied from payload guesses.

- [ ] **Step 2: Run the targeted tests to verify RED**

Run: `go test ./pkg/cluster -run 'TestControllerHostLeaderChangeReloadsNodeMirrorOnLocalLeadership|TestControllerHostLeaderChangeClearsNodeMirrorOnLeaderLoss|TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForNodeStatusUpdate|TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForOperatorRequest' -count=1`

Expected: FAIL because leader changes do not rebuild any durable node mirror and committed commands do not refresh mirrored node state.

- [ ] **Step 3: Implement committed-state-only mirror refresh**

In `pkg/cluster/node_health_scheduler.go`, add refresh helpers such as:

```go
func (s *nodeHealthScheduler) reloadAllNodes(ctx context.Context) error
func (s *nodeHealthScheduler) refreshNodeFromStore(ctx context.Context, nodeID uint64)
func (s *nodeHealthScheduler) handleCommittedCommand(cmd slotcontroller.Command)
```

Rules:
- on `leader change -> local`, rebuild the entire mirror from `ListNodes(...)`
- on `leader change -> remote`, clear the mirror
- on committed `NodeStatusUpdate`, refresh the referenced node IDs from store
- on committed `OperatorRequest`, refresh `op.NodeID` from store
- for any residual legacy command that mutates nodes, prefer conservative store refresh over payload-derived mutation

Wire these lifecycle calls through `pkg/cluster/controller_host.go` so leader-change and committed-command callbacks keep the scheduler mirror in sync.

- [ ] **Step 4: Run the targeted tests to verify GREEN**

Run: `go test ./pkg/cluster -run 'TestControllerHostLeaderChangeReloadsNodeMirrorOnLocalLeadership|TestControllerHostLeaderChangeClearsNodeMirrorOnLeaderLoss|TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForNodeStatusUpdate|TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForOperatorRequest' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the committed-refresh lifecycle work**

Run:
```bash
git add pkg/cluster/controller_host.go pkg/cluster/node_health_scheduler.go pkg/cluster/controller_host_test.go pkg/cluster/node_health_scheduler_test.go
git commit -m "feat: refresh controller mirrors from committed state"
```

### Task 5: Update flow docs and run full verification

**Files:**
- Modify: `pkg/cluster/FLOW.md`
- Modify: any test files touched above if wording / comments need final cleanup

- [ ] **Step 1: Write the doc changes after code behavior is green**

Update `pkg/cluster/FLOW.md` to describe:
- steady-state `heartbeat` reading hash slot data from a leader-local snapshot
- steady-state node health edge checks reading durable node status from a leader-local mirror
- store fallback only on cold miss or rebuild

- [ ] **Step 2: Run the focused package tests before broader verification**

Run: `go test ./pkg/cluster -count=1`

Expected: PASS.

- [ ] **Step 3: Run the related controller verification suite**

Run: `go test ./pkg/cluster ./pkg/controller/... -count=1`

Expected: PASS.

- [ ] **Step 4: Review the final diff for unintended changes**

Run:
```bash
git status --short
git diff -- pkg/cluster/controller_host.go pkg/cluster/controller_handler.go pkg/cluster/node_health_scheduler.go pkg/cluster/FLOW.md
```

Expected: only the planned fast-path, tests, and documentation updates are present.

- [ ] **Step 5: Commit the docs and final verification state**

Run:
```bash
git add pkg/cluster/FLOW.md
git commit -m "docs: update controller fast path flow"
```

## Notes for the Implementer

- Do not change RPC payload formats, redirect semantics, or warmup behavior while adding the fast path.
- Do not mutate mirrors from proposal-time state. Refresh mirrors only from committed state or full leader rebuilds.
- Keep `controllerMeta` as the only durable authority; mirrors are optimization-only.
- Preserve the existing `ensureControllerHashSlotTable(...)` fallback path for startup and cold misses.
- If a leader-side cache rebuild fails, prefer logging plus fallback behavior over making the controller unavailable.
