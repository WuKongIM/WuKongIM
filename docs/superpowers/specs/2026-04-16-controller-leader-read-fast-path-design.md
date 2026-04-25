# Controller Leader Read Fast Path Design

## Summary

The previous controller optimization removed steady-state observation writes from
controller Raft and replaced periodic timeout proposals with a leader-local
health deadline scheduler. That change fixed the original idle CPU problem: the
controller no longer continuously commits observation traffic through Raft.

A follow-up profile on the same three-node docker-compose cluster shows a new,
smaller steady-state hotspot on whichever node is serving as controller leader.
The dominant cost is no longer Raft proposal / commit churn. Instead, it is the
controller leader repeatedly serving high-frequency observation RPCs and reading
Pebble on every request.

The remaining hot path has two main sources:

- `controllerRPCHeartbeat` always calls `ensureControllerHashSlotTable()`,
  which does a `LoadHashSlotTable()` Pebble read even when the table is already
  stable.
- `nodeHealthScheduler.observe()` always tries to compute an `Alive` edge by
  calling `GetNode()` from controller meta, even when the durable node status is
  already known locally and unchanged.

This design keeps all durable controller state in `controllerMeta`, but adds two
leader-local read fast paths so the controller leader can serve steady-state
observation traffic without hitting Pebble on every request.

## Goals

- Remove steady-state `LoadHashSlotTable()` reads from controller heartbeat and
  assignment query handling on the controller leader.
- Remove steady-state `GetNode()` reads from `nodeHealthScheduler.observe()` on
  the controller leader.
- Preserve current controller RPC semantics, leader redirect behavior, warmup
  gating, and durable controller truth in `controllerMeta`.
- Keep failover safe: a new controller leader must reconstruct fast-path caches
  from committed durable state before using them.
- Limit the optimization to local in-memory mirrors; do not change client
  protocol or controller consensus behavior.

## Non-Goals

- No change to the controller observation interval.
- No batching or protocol merge for controller RPCs in this iteration.
- No change to slot planner / reconciler policy.
- No general-purpose cache layer inside `controller/meta.Store`.
- No attempt to replicate fast-path caches to follower controllers.

## Verified Problem

### 1. The remaining CPU gap is on the controller leader read path

After the previous optimization, idle CPU in the three-node docker-compose
cluster dropped significantly, but node2 still remained consistently higher than
node1 and node3.

Observed evidence:

- node2 receives far more inbound `rpc_request` bytes than node1 and node3.
- slot runtime leadership is on node3, so node2's extra CPU is not caused by
  slot data-plane leadership.
- node2 CPU profiles show the dominant stack under:
  - `transport.(*Server).handleRPCRequest`
  - `transport.(*RPCMux).HandleRPC`
  - `cluster.(*controllerHandler).Handle`
- the main application-level hotspots are:
  - `controllerHost.applyObservation`
  - `nodeHealthScheduler.observe`
  - `Cluster.ensureControllerHashSlotTable`
  - `controller/meta.(*Store).GetNode`
  - `controller/meta.(*Store).LoadHashSlotTable`

### 2. Heartbeat handling still does a Pebble read on every request

`controllerHandler.Handle()` currently executes this path for every heartbeat on
the controller leader:

1. apply observation into leader-local caches
2. call `ensureControllerHashSlotTable()`
3. `ensureControllerHashSlotTable()` calls `store.LoadHashSlotTable()`
4. `LoadHashSlotTable()` performs `db.Get()` on Pebble

This is correct, but wasteful in steady state because the table is usually
stable and already known locally.

### 3. Health scheduling still does a Pebble read on every observation

`nodeHealthScheduler.observe()` refreshes deadlines and then immediately calls
`proposeStatusTransition(observation, Alive, observedAt)`.

`proposeStatusTransition()` currently does this before deciding whether a Raft
proposal is needed:

1. `loadNode(GetNode)` from controller meta
2. compare durable status with desired target
3. only then decide whether an edge exists

This means repeated `Alive` observations still pay a durable store read even
when the node is already known to be `Alive`.

## Decision

Add two controller-leader-local read fast paths:

1. a hash slot table snapshot cache owned by `controllerHost`
2. a durable node state mirror owned by `nodeHealthScheduler`

These are committed-state mirrors, not new sources of truth.

### Durable truth remains unchanged

The only authoritative durable controller state remains `controllerMeta`.

- `HashSlotTable` remains stored in controller meta.
- node status remains stored in controller meta.
- controller fast paths are strictly local mirrors of already committed state.

If a mirror is empty, stale, or fails to rebuild, the controller falls back to
controller meta and repopulates the mirror.

## Architecture

### A. Hash slot table fast path on `controllerHost`

Add a leader-local in-memory snapshot of `HashSlotTable` to `controllerHost`.

### Responsibilities

- hold the latest committed hash slot table known to the local controller host
- serve heartbeat and assignment RPC responses without a Pebble read in steady
  state
- rebuild from controller meta on startup, on local leader acquisition, or on
  cache miss
- clear on local leader loss

### Read path

The following controller RPCs should read from the host cache first:

- `heartbeat`
- `list_assignments`

If the cache is empty, they may fall back to `controllerMeta.LoadHashSlotTable()`
once, then backfill the cache.

### Refresh path

Refresh the host cache only when a committed controller command can change the
hash slot table:

- `StartMigration`
- `AdvanceMigration`
- `FinalizeMigration`
- `AbortMigration`
- `AddSlot`
- `RemoveSlot`

The refresh happens after the command is committed, not when the command is
proposed.

### Leader change behavior

- on `leader change -> local node`: reload the committed hash slot table from
  controller meta
- on `leader change -> remote node`: clear the local snapshot

This guarantees the fast path only reflects committed durable state belonging to
this leader term.

### B. Durable node state mirror inside `nodeHealthScheduler`

Add a leader-local durable node mirror to `nodeHealthScheduler`.

### Responsibilities

- hold the latest committed durable node records needed for liveness edge
  decisions
- let `proposeStatusTransition()` compare desired state against local committed
  mirrors before deciding whether a Raft proposal is necessary
- rebuild from controller meta on leader acquisition
- refresh entries after committed commands that may change node durable state
- fall back to store reads only on cold miss or rebuild failure

### Read path

`proposeStatusTransition()` should:

1. look up the durable node record from the local mirror
2. if found, compute whether a real edge exists
3. if missing, fall back to `GetNode()` and populate the mirror

This preserves correctness while removing the repeated steady-state Pebble read.

### Refresh path

The durable node mirror must be refreshed after committed controller commands
that may change node durable state:

- `NodeStatusUpdate`
- `OperatorRequest`

For compatibility with any residual legacy write path, the implementation may
choose a conservative refresh strategy for:

- `NodeHeartbeat`
- `EvaluateTimeouts`

The safe rule is:

- do not mutate the mirror directly from command payload unless the command is
  guaranteed to reflect the final committed state
- if the state machine may ignore or partially apply a transition, refresh from
  store after commit instead of trusting payload fields

This is especially important for `NodeStatusUpdate`, where `ExpectedStatus`
mismatches can cause an intentional no-op.

### Leader change behavior

- on `leader change -> local node`: rebuild the entire mirror from
  `controllerMeta.ListNodes()`
- on `leader change -> remote node`: clear the mirror and reset scheduler state

The existing timer reset and warmup behavior remains unchanged.

### C. Keep controller semantics unchanged

This optimization does not alter the current controller behavior:

- heartbeat RPCs still update leader-local observation state
- hash slot table versioning and payload return behavior stay unchanged
- leader redirect behavior stays unchanged
- planner warmup gating stays unchanged
- durable node status still changes only through committed controller commands

The optimization only changes where the controller leader reads stable state
from during steady-state request handling.

## Failure Handling

### Cache rebuild failure

If leader-local cache rebuild fails during startup or leader acquisition:

- do not block controller leadership solely because the fast path is empty
- log the rebuild failure
- leave the cache empty
- allow the next request to fall back to store reads and repopulate the cache

This keeps correctness first and treats the fast path as an optimization.

### Cache miss

If a cache entry is missing:

- fall back to controller meta
- if the read succeeds, backfill the cache
- if the read fails, return the original error semantics

### Staleness protection

Do not update local mirrors from speculative or pre-commit state.

- proposals do not change mirrors
- committed command hooks or leader-rebuild paths are the only sources of cache
  refresh

## File-Level Changes

### `pkg/cluster/controller_host.go`

Add:

- leader-local hash slot table snapshot state
- helper methods to get, reload, store, and clear the snapshot
- leader change handling for hash slot snapshot lifecycle
- committed command handling that refreshes the snapshot when hash slot table
  mutations commit

### `pkg/cluster/controller_handler.go`

Change heartbeat and assignment handling so they:

- read `HashSlotTable` from the controller host snapshot first
- fall back to controller meta only on cold miss

### `pkg/cluster/node_health_scheduler.go`

Add:

- durable node mirror storage
- mirror reset / reload helpers
- mirror-aware `proposeStatusTransition()`
- committed command refresh logic for node durable state

### `pkg/cluster/cluster.go`

Keep `ensureControllerHashSlotTable()` for startup and fallback use, but no
longer treat it as the normal steady-state heartbeat dependency.

### `pkg/cluster/FLOW.md`

Update the flow documentation so it reflects:

- heartbeat steady-state serving from leader-local hash slot snapshot
- node health steady-state decisions using leader-local durable mirrors
- store fallback only on cold miss / rebuild

## Testing Strategy

All coverage should remain in unit tests unless a small integration test is
strictly required.

### 1. Controller handler fast path tests

In `pkg/cluster/controller_handler_test.go`:

- verify heartbeat returns the correct hash slot table version when the host
  snapshot is already populated
- verify `list_assignments` uses the same fast path
- verify cold miss still falls back to controller meta and backfills the cache

### 2. Controller host lifecycle tests

In `pkg/cluster/controller_host_test.go`:

- verify `leader change -> self` rebuilds the hash slot snapshot
- verify `leader change -> other` clears the snapshot
- verify committed hash-slot-mutating commands refresh the cached snapshot

### 3. Node health scheduler mirror tests

In `pkg/cluster/node_health_scheduler_test.go`:

- verify repeated `Alive` observations can decide against a new proposal from
  mirror state without requiring a store read each time
- verify mirror miss falls back to `GetNode()` and backfills the mirror
- verify committed `NodeStatusUpdate` or operator commands refresh mirror state
- verify scheduler reset clears pending timers and mirror state together

### 4. Regression guard for semantics

Where existing tests already verify controller redirect, warmup gating, or task
planning semantics, keep them unchanged. The new tests should prove that only
read-path cost changed, not controller behavior.

## Expected Outcome

After this change:

- controller leader steady-state heartbeat handling should no longer require
  `LoadHashSlotTable()` on every request
- `nodeHealthScheduler.observe()` should no longer require `GetNode()` on every
  observation when durable status is already mirrored locally
- residual idle CPU on the controller leader should move away from Pebble read
  hot paths and closer to pure RPC / in-memory observation handling

## Rollout Notes

This is a local optimization with no wire-format change, so rollout is simple:

- deploy mixed-version cluster only if existing RPC behavior remains identical
- verify controller leader failover still rebuilds fast paths correctly
- re-run idle CPU profile after rollout to confirm Pebble read hotspots have
  materially dropped on the controller leader
