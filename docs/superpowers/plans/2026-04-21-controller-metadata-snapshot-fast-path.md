# Controller Metadata Snapshot Fast Path Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a leader-local controller metadata snapshot fast path for nodes, assignments, and tasks so the steady-state control-plane hot path avoids repeated Pebble reads without changing external `pkg/cluster.API` semantics.

**Architecture:** Keep Pebble-backed controller metadata as the source of truth and add a `controllerHost`-owned in-memory snapshot that is only trusted while the local node is the controller leader and the snapshot is `Ready && !Dirty`. Reload the snapshot on local leader acquisition, invalidate it on leader loss, mark it dirty on any committed command that may affect nodes / assignments / tasks, and let internal readers fall back to the current store-backed path whenever the snapshot is unavailable.

**Tech Stack:** Go, Pebble-backed controller metadata store, controller raft callbacks, existing `pkg/cluster` unit/integration tests, `go test`

---

### Task 1: Add the controller metadata snapshot state and lifecycle

**Files:**
- Create: `pkg/cluster/controller_metadata_snapshot.go`
- Modify: `pkg/cluster/controller_host.go`
- Test: `pkg/cluster/controller_host_test.go`

- [ ] **Step 1: Write the failing lifecycle tests**

Add tests in `pkg/cluster/controller_host_test.go` that cover:

```go
func TestControllerHostMetadataSnapshotReloadsOnLocalLeaderChange(t *testing.T)
func TestControllerHostMetadataSnapshotClearsOnLeaderLoss(t *testing.T)
func TestControllerHostHandleCommittedCommandMarksMetadataSnapshotDirty(t *testing.T)
func TestControllerHostMetadataSnapshotReloadRestoresReadyState(t *testing.T)
```

Each test should seed `host.meta` with at least one node, one assignment, and one task, then assert the snapshot transitions through `ready`, `dirty`, and leader-generation invalidation exactly as designed.

- [ ] **Step 2: Run the new lifecycle tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestControllerHostMetadataSnapshot'`
Expected: FAIL because the metadata snapshot type and lifecycle hooks do not exist yet.

- [ ] **Step 3: Write the minimal snapshot model**

Create `pkg/cluster/controller_metadata_snapshot.go` with a focused `controllerMetadataSnapshot` / `controllerMetadataSnapshotState` implementation that provides:

```go
type controllerMetadataSnapshot struct {
    Nodes             []controllermeta.ClusterNode
    NodesByID         map[uint64]controllermeta.ClusterNode
    Assignments       []controllermeta.SlotAssignment
    AssignmentsBySlot map[uint32]controllermeta.SlotAssignment
    Tasks             []controllermeta.ReconcileTask
    TasksBySlot       map[uint32]controllermeta.ReconcileTask
    LeaderID          multiraft.NodeID
    Generation        uint64
    Ready             bool
    Dirty             bool
}
```

Also add helper methods for:
- loading all three metadata sets from `controllerMeta`
- atomically swapping the snapshot on successful reload
- invalidating the snapshot on leader loss
- returning a clone/read-only copy only when `Ready && !Dirty`
- marking the snapshot dirty and coalescing reload requests without mutating Pebble-backed state

- [ ] **Step 4: Wire the snapshot into `controllerHost`**

Update `pkg/cluster/controller_host.go` to:
- add the snapshot state to `controllerHost`
- initialize it in `newControllerHost`
- reload it in `handleLeaderChange(..., to == localNode)` after local-leader setup
- invalidate it in `handleLeaderChange(..., to != localNode)`
- mark it dirty from `handleCommittedCommand` when the command can affect nodes / assignments / tasks
- expose a small internal accessor such as `metadataSnapshot()` / `plannerMetadataSnapshot()` for other cluster fast paths

Keep the first version conservative: any relevant committed command may dirty the whole snapshot, and all uncertain reads must fall back.

- [ ] **Step 5: Run the lifecycle tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestControllerHostMetadataSnapshot'`
Expected: PASS.

- [ ] **Step 6: Commit the lifecycle slice**

```bash
git add pkg/cluster/controller_metadata_snapshot.go pkg/cluster/controller_host.go pkg/cluster/controller_host_test.go
git commit -m "feat: add controller metadata snapshot lifecycle"
```

### Task 2: Use the snapshot in planner state assembly and local leader helpers

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/agent.go`
- Test: `pkg/cluster/cluster_test.go`
- Test: `pkg/cluster/agent_internal_integration_test.go`

- [ ] **Step 7: Write the failing planner/helper tests**

Add coverage for:

```go
func TestSnapshotPlannerStateUsesMetadataSnapshotWhenWarm(t *testing.T)
func TestSnapshotPlannerStateFallsBackToStoreWhenMetadataSnapshotDirty(t *testing.T)
func TestSlotAgentListControllerNodesUsesLocalMetadataSnapshot(t *testing.T)
func TestSlotAgentGetTaskUsesLocalMetadataSnapshot(t *testing.T)
```

Test intent:
- seed store data and a conflicting in-memory snapshot to prove the code now prefers the snapshot on the internal fast path
- mark the snapshot dirty to prove planner/helper code falls back to `controllerMeta`
- keep existing remote-controller behavior unchanged

- [ ] **Step 8: Run the targeted tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestSnapshotPlannerStateUsesMetadataSnapshotWhenWarm|TestSnapshotPlannerStateFallsBackToStoreWhenMetadataSnapshotDirty|TestSlotAgentListControllerNodesUsesLocalMetadataSnapshot|TestSlotAgentGetTaskUsesLocalMetadataSnapshot'`
Expected: FAIL because planner state assembly and local leader helper reads still go directly to `controllerMeta`.

- [ ] **Step 9: Implement the planner fast path**

Update `pkg/cluster/cluster.go` so `snapshotPlannerState()`:
- asks `controllerHost` for a metadata snapshot when the local node is the controller leader
- uses snapshot `Nodes`, `Assignments`, and `Tasks` when available
- continues to use the existing observation snapshot fast path for runtime views
- falls back to `controllerMeta.ListNodes/ListAssignments/ListTasks` when the metadata snapshot is unavailable or dirty

Keep the final `slotcontroller.PlannerState` assembly logic unchanged apart from the data source selection.

- [ ] **Step 10: Implement the local leader helper fast path**

Update `pkg/cluster/agent.go` so local-leader helper reads prefer the metadata snapshot before touching Pebble:
- `SyncAssignments()` local fallback path may keep using the store because it also refreshes hash-slot state
- `listControllerNodes()` should use snapshot nodes when available
- `getTask()` should use `TasksBySlot` from the snapshot when available, while preserving the current not-found/error behavior

Do not change externally exported `Cluster.ListNodes`, `Cluster.ListTasks`, or operator APIs in this task.

- [ ] **Step 11: Run the targeted tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestSnapshotPlannerStateUsesMetadataSnapshotWhenWarm|TestSnapshotPlannerStateFallsBackToStoreWhenMetadataSnapshotDirty|TestSlotAgentListControllerNodesUsesLocalMetadataSnapshot|TestSlotAgentGetTaskUsesLocalMetadataSnapshot'`
Expected: PASS.

- [ ] **Step 12: Commit the planner/helper slice**

```bash
git add pkg/cluster/cluster.go pkg/cluster/agent.go pkg/cluster/cluster_test.go pkg/cluster/agent_internal_integration_test.go
git commit -m "feat: use metadata snapshot in controller hot paths"
```

### Task 3: Use the snapshot in leader-side controller read RPCs

**Files:**
- Modify: `pkg/cluster/controller_handler.go`
- Test: `pkg/cluster/controller_handler_test.go`

- [ ] **Step 13: Write the failing RPC fast-path tests**

Add tests in `pkg/cluster/controller_handler_test.go` for:

```go
func TestControllerHandlerListAssignmentsUsesMetadataSnapshotWhenWarm(t *testing.T)
func TestControllerHandlerListNodesUsesMetadataSnapshotWhenWarm(t *testing.T)
func TestControllerHandlerListTasksUsesMetadataSnapshotWhenWarm(t *testing.T)
func TestControllerHandlerGetTaskUsesMetadataSnapshotWhenWarm(t *testing.T)
func TestControllerHandlerMetadataReadsFallBackToStoreWhenSnapshotDirty(t *testing.T)
```

Each test should intentionally diverge store contents from the in-memory snapshot so the assertion proves which data source the handler used.

- [ ] **Step 14: Run the targeted RPC tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestControllerHandlerListAssignmentsUsesMetadataSnapshotWhenWarm|TestControllerHandlerListNodesUsesMetadataSnapshotWhenWarm|TestControllerHandlerListTasksUsesMetadataSnapshotWhenWarm|TestControllerHandlerGetTaskUsesMetadataSnapshotWhenWarm|TestControllerHandlerMetadataReadsFallBackToStoreWhenSnapshotDirty'`
Expected: FAIL because the handlers still read `controllerMeta` directly.

- [ ] **Step 15: Implement the leader-side RPC fast path**

Update `pkg/cluster/controller_handler.go` so leader-side handlers prefer the metadata snapshot for:
- `controllerRPCListAssignments`
- `controllerRPCListNodes`
- `controllerRPCListTasks`
- `controllerRPCGetTask`

Rules:
- only use the snapshot when `controllerHost` says it is valid
- keep the current redirect semantics for followers unchanged
- keep `list_assignments` hash-slot-table behavior unchanged
- preserve `ErrNotFound` / `NotFound` response behavior for `get_task`
- fall back to the current `controllerMeta` reads when the snapshot is not ready or is dirty

- [ ] **Step 16: Run the targeted RPC tests to verify they pass**

Run: `go test ./pkg/cluster -run 'TestControllerHandlerListAssignmentsUsesMetadataSnapshotWhenWarm|TestControllerHandlerListNodesUsesMetadataSnapshotWhenWarm|TestControllerHandlerListTasksUsesMetadataSnapshotWhenWarm|TestControllerHandlerGetTaskUsesMetadataSnapshotWhenWarm|TestControllerHandlerMetadataReadsFallBackToStoreWhenSnapshotDirty'`
Expected: PASS.

- [ ] **Step 17: Commit the RPC slice**

```bash
git add pkg/cluster/controller_handler.go pkg/cluster/controller_handler_test.go
git commit -m "feat: use metadata snapshot in controller read rpc"
```

### Task 4: Update flow docs and run end-to-end verification

**Files:**
- Modify: `pkg/cluster/FLOW.md`
- Verify: `pkg/cluster/controller_host.go`
- Verify: `pkg/cluster/controller_handler.go`
- Verify: `pkg/cluster/cluster.go`
- Verify: `pkg/cluster/agent.go`

- [ ] **Step 18: Update the flow documentation**

Revise `pkg/cluster/FLOW.md` so the observation/planner and controller-RPC sections explain that leader-local metadata reads now prefer the controller metadata snapshot and fall back to `controllerMeta` when the snapshot is cold or dirty.

- [ ] **Step 19: Run focused package verification**

Run: `go test ./pkg/cluster ./pkg/controller/meta`
Expected: PASS.

- [ ] **Step 20: Run a broader regression sweep for cluster-facing callers**

Run: `go test ./internal/app ./pkg/cluster/...`
Expected: PASS. If this is too slow in the local environment, at minimum keep the first command plus the exact targeted test runs from Tasks 1-3 in the execution log.

- [ ] **Step 21: Inspect the diff for accidental API changes**

Run: `git log --oneline --stat -n 4`
Expected: the last four commits should only touch the planned `pkg/cluster` implementation/tests and `pkg/cluster/FLOW.md`; exported cluster APIs should not gain new user-facing semantics.

- [ ] **Step 22: Commit docs + final verification notes**

```bash
git add pkg/cluster/FLOW.md
git commit -m "docs: update cluster flow for metadata snapshot fast path"
```
