# Controller Metadata Snapshot Fast Path Design

- Date: 2026-04-21
- Scope: reduce steady-state controller Pebble reads on the production path without changing external API semantics
- Related directories:
  - `pkg/cluster`
  - `pkg/controller/meta`
  - `internal/app`
  - `docs/superpowers/specs`

## 1. Background

The current cluster steady-state control-plane loop polls every `WK_CLUSTER_CONTROLLER_OBSERVATION_INTERVAL`. With the default `200ms`, each node repeatedly triggers controller reads such as:

- `list_assignments`
- `list_nodes`
- `list_runtime_views`
- `get_task`

The direct CPU hotspot is Pebble-backed controller metadata reads, not the long-poll replication path. The relevant code paths are:

- planner-side control-plane reads in `pkg/cluster/cluster.go`
- reconcile-side reads in `pkg/cluster/reconciler.go`
- controller read RPC handling in `pkg/cluster/controller_handler.go`
- Pebble-backed metadata store reads in `pkg/controller/meta/store.go`

The existing runtime-view fast path already uses a leader-local observation snapshot, but nodes, assignments, and tasks still fall back to `controllerMeta` store reads on the hot path.

## 2. Goals and Non-Goals

## 2.1 Goals

1. Add a production-safe leader-local metadata snapshot for controller nodes, assignments, and tasks.
2. Use the snapshot only for internal hot paths and controller read RPC handling.
3. Keep Pebble as the source of truth.
4. Preserve current external `pkg/cluster.API` semantics.
5. Ensure any snapshot inconsistency falls back to the current Pebble-backed path instead of returning stale data.

## 2.2 Non-Goals

This design does not aim to:

- change public `API.ListNodes`, `API.ListTasks`, or `API.ListSlotAssignments` semantics
- replace Pebble persistence with an in-memory source of truth
- implement command-by-command incremental cache mutation
- redesign controller observation cadence or tuning defaults
- optimize the long-poll replication path in this iteration

## 3. Approaches Considered

## 3.1 Approach A: Tune `WK_CLUSTER_CONTROLLER_OBSERVATION_INTERVAL` only

Increase the polling interval to reduce control-plane read frequency.

Pros:

- minimal code change
- immediate CPU reduction

Cons:

- only reduces frequency, does not remove redundant Pebble reads
- slows control-plane convergence, repair, migration, and task progression
- does not solve the root hot path for production

## 3.2 Approach B: Add a leader-local metadata snapshot for internal fast paths

Maintain a controller leader-local in-memory snapshot of nodes, assignments, and tasks, and use it only in internal hot paths plus controller read RPC handlers.

Pros:

- directly removes repeated steady-state Pebble reads
- keeps external API semantics unchanged
- keeps fallback to current behavior when the snapshot is unavailable or dirty
- low enough risk for production rollout

Cons:

- requires snapshot lifecycle and refresh logic
- needs careful stale-data protection

## 3.3 Approach C: Fine-grained incremental cache updates per committed command

Update in-memory metadata incrementally for every committed controller command.

Pros:

- best theoretical steady-state performance

Cons:

- highest correctness risk
- easy to miss command kinds or edge cases
- not the right first production rollout

## 3.4 Recommended approach

Use Approach B.

It provides a meaningful steady-state performance win while keeping the design conservative:

- Pebble remains authoritative
- internal readers use the snapshot only when it is known-good
- any uncertainty falls back to the current implementation

## 4. Design Principles

## 4.1 Internal fast path only

The new snapshot is strictly an internal optimization layer. It must not redefine the semantics of the exported cluster API.

## 4.2 Fallback over stale reads

If the snapshot is not definitely fresh, readers must fall back to the current Pebble-backed path. Correctness is more important than always hitting the fast path.

## 4.3 Coarse-grained refresh over incremental mutation

The first production version should refresh the snapshot by reloading authoritative metadata from the store, not by mutating cached state per command type.

## 4.4 Reuse existing controller-host leader locality

The snapshot belongs in `controllerHost`, alongside the existing leader-local runtime observation and hash-slot-table fast paths.

## 5. Snapshot Model

Add a new controller-host-owned structure, conceptually:

```go
type controllerMetadataSnapshot struct {
    Nodes           []controllermeta.ClusterNode
    NodesByID       map[uint64]controllermeta.ClusterNode
    Assignments     []controllermeta.SlotAssignment
    AssignmentsBySlot map[uint32]controllermeta.SlotAssignment
    Tasks           []controllermeta.ReconcileTask
    TasksBySlot     map[uint32]controllermeta.ReconcileTask

    LeaderID        multiraft.NodeID
    Generation      uint64
    Ready           bool
    Dirty           bool
}
```

Implementation details may differ, but the snapshot must provide:

- ordered slices for bulk reads
- indexed maps for point lookups
- explicit readiness and dirtiness state
- leader generation binding so stale snapshots are never reused across leadership changes

## 6. Refresh Lifecycle

## 6.1 Initial load on local leader transition

When `controllerHost` becomes the local leader:

1. reset snapshot state
2. trigger an immediate full metadata load from `controllerMeta`
3. mark the snapshot ready only after the reload completes successfully

When local leadership is lost:

- clear or invalidate the snapshot immediately

This mirrors the existing leader-local setup used by the runtime observation and hash-slot-table snapshots.

## 6.2 Dirty-on-change policy

When a committed controller command may affect nodes, assignments, or tasks:

1. mark the metadata snapshot as dirty
2. enqueue a coalesced refresh request

While dirty:

- internal fast-path readers must not trust the snapshot
- reads fall back to the current store-based implementation

## 6.3 Coalesced async reload worker

Add a small controller-host-owned refresh worker:

- single-flight / serialized
- coalesces multiple dirty events into one reload
- reloads:
  - `ListNodes`
  - `ListAssignments`
  - `ListTasks`
- atomically swaps the in-memory snapshot on success

This avoids running Pebble-heavy reload logic inline in `handleCommittedCommand`.

## 6.4 Failure behavior

If snapshot reload fails:

- keep `Dirty=true`
- do not expose the stale snapshot as fast-path readable
- allow all call sites to continue via the current store fallback

This guarantees that snapshot issues degrade to current behavior rather than introducing incorrect reads.

## 7. Read Path Changes

## 7.1 Planner state fast path

Update `snapshotPlannerState()` in `pkg/cluster/cluster.go`:

- prefer metadata snapshot for:
  - nodes
  - assignments
  - tasks
- continue using the existing runtime-view fast path for runtime views
- if metadata snapshot is unavailable or dirty, fall back to current `controllerMeta` reads

This is expected to remove the most expensive steady-state leader-local Pebble reads.

## 7.2 Controller read RPC fast path

Update `pkg/cluster/controller_handler.go` so these leader-side RPCs prefer the metadata snapshot:

- `list_assignments`
- `list_nodes`
- `list_tasks`
- `get_task`

If the snapshot is unavailable or dirty, keep the current `controllerMeta` behavior.

This reduces repeated remote requests that currently force the leader into Pebble reads.

## 7.3 Local leader fast path in agent/reconcile helpers

Update local leader helper paths in `pkg/cluster/agent.go` to prefer the metadata snapshot for:

- assignment sync
- node listing
- task lookup

The reconciler itself should mostly benefit through these helpers, keeping the core reconcile logic unchanged.

## 7.4 No external API semantic change

Do not change the externally visible semantics of:

- `API.ListNodes`
- `API.ListTasks`
- `API.ListSlotAssignments`

They may continue to use current routing and fallback behavior. This iteration targets only internal hot paths.

## 8. Dirty Command Coverage

The first version should be conservative: any committed command that may affect node, assignment, or task metadata marks the snapshot dirty.

At minimum, this includes command families equivalent to:

- node status updates / operator requests
- assignment task updates
- task results
- start / advance / finalize / abort migration
- add / remove slot

Being slightly over-conservative is acceptable in v1 because dirty snapshots safely fall back to the current implementation.

## 9. Correctness Semantics

The snapshot may only be used when all of the following are true:

1. local node is the controller leader
2. snapshot leader generation matches the current local leadership generation
3. snapshot is marked ready
4. snapshot is not dirty

Otherwise, readers must fall back.

This preserves the current correctness model while adding a best-effort fast path.

## 10. Testing Strategy

## 10.1 `controller_host` tests

Add coverage for:

- initial snapshot load when becoming leader
- snapshot invalidation on leader loss
- dirty state blocking fast-path reads
- successful async refresh clearing dirty state
- refresh failure preserving fallback behavior

## 10.2 planner-state tests

Add or extend tests so `snapshotPlannerState()`:

- uses metadata snapshot when fresh
- falls back to `controllerMeta` when snapshot is unavailable or dirty

## 10.3 controller handler tests

Add coverage for:

- `list_nodes`
- `list_assignments`
- `list_tasks`
- `get_task`

Verify snapshot-first behavior with store fallback.

## 10.4 agent fast-path tests

Add coverage for local leader helper methods:

- snapshot hit on fresh metadata
- fallback on dirty or unavailable snapshot

## 10.5 regression tests

Cover failure-prone transitions:

- leader change while snapshot exists
- task mutation followed by immediate read
- assignment change followed by reconcile
- refresh failure followed by later recovery

## 11. Rollout and Observability

No configuration toggle is required for the first version if the fallback behavior is kept strict.

Recommended observability additions during implementation:

- debug counters for snapshot hits / misses / dirty fallbacks
- optional refresh success / failure counters

These are not mandatory for the design to be valid, but they would make production validation easier.

## 12. Expected Outcome

After this design is implemented:

- controller leader steady-state planner ticks should stop reading Pebble on every loop for nodes, assignments, and tasks
- follower controller read RPCs should stop forcing repeated leader-side Pebble reads for those same datasets
- control-plane correctness should remain unchanged because uncertain states always fall back to the current implementation
- future optimization work can still move from coarse-grained reloads to incremental mutation if needed, without changing the public API
