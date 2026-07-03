# pkg/controller Flow

## Responsibility

`pkg/controller` is the canonical Controller implementation. Controller voter nodes apply committed Controller Raft commands to a canonical `cluster-state.json` file. Non-controller nodes do not join Controller Raft; they mirror Controller voter state files through full-file sync.

The root `pkg/controller` package is the external facade. Callers should depend on its `Runtime`, strongly typed `ClusterState` / `StateEvent`, Raft message stepping, and state sync request/response contracts. Subpackages remain Controller engine implementation details and should not be imported by `pkg/cluster` production code.

`pkg/cluster/control` hosts the production-shaped integration wrapper; `pkg/controller` remains the reusable Controller engine and does not import `pkg/cluster`.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| root `controller` | Public runtime facade, strongly typed state change events, and transport-agnostic Controller network contracts. |
| `state` | Durable JSON model, normalization, validation, checksum, initial hash-slot table. |
| `statefile` | Atomic load/save for `cluster-state.json`. |
| `command` | Versioned Raft command envelope. |
| `fsm` | Applies committed command batches, persists the final state once per batch, publishes snapshots after durable save. |
| `planner` | Pure planning. V1 only creates bootstrap assignment/task commands. |
| `sync` | Full-file Controller voter state sync for non-controller nodes. |
| `raft` | Controller Raft wrapper, WAL-backed log storage, scheduled apply, snapshots, and compaction. |
| `server` | Thin composition facade for tests and future integration. |

Runtime composition may provide a `RaftObserver` to record queue and enqueue
latency telemetry from the Controller Raft ingress path. The observer is
read-only and must not affect Raft stepping, WAL persistence, or apply order.
Runtime composition may also provide a `TaskTransitionObserver`; the apply
scheduler emits task edges only after FSM state changes have been saved,
in-memory snapshots have been published, and the applied Raft boundary has been
persisted. Observer failures are intentionally outside Raft apply semantics, so
composition roots must keep their observer work bounded and nonblocking.
`Runtime.Watch` is a latest-state notification stream: when a watcher buffer
is full, publication replaces one stale buffered event with the newest locally
visible state instead of permanently dropping that state. Consumers should
still treat events as wakeups and call `LocalState` when they need the current
snapshot; same-revision checksum drift, such as health-only refreshes, may be
published again.

`Runtime.LogEntries` is a read-only diagnostics facade over the Controller Raft
WAL. It returns newest-first, cursor-paginated entry summaries for manager UI
inspection, decoding normal command payloads through the Controller command
codec and reporting empty normal entries as non-mutating noops. It does not
change Raft apply, compaction, or state-file persistence semantics.

`Runtime.ControllerRaftStatus` and `Runtime.CompactControllerRaftLog` are
node-local Controller Raft management facades. Status reports volatile
leader/term/apply watermarks plus durable log and snapshot watermarks.
`CompactControllerRaftLog` forces the same materialized-state snapshot path
used by automatic compaction, then trims only entries already covered by the
snapshot catch-up window. It is not a replicated Raft command; callers that want
cluster-wide compaction must explicitly fan out the local operation to
Controller voters.

## Reading Guide

Start at the root facade when reading production behavior:

The split-file names below describe the intended layout after this readability pass is completed; before the split lands, the same logic still lives in the original larger files.

1. `runtime.go` exposes the public API and holds runtime state.
2. `runtime_start.go` wires voter or mirror mode.
3. `runtime_bootstrap.go` creates the initial ControllerV2 state through the same Raft path used by multi-node voters.
4. `raft/service.go` owns public Raft lifecycle; `raft/service_run.go` owns Ready persistence, message send order, and scheduled apply.
5. `fsm/mutations.go` dispatches commands; `fsm/mutation_handlers.go` contains the actual state changes.
6. `sync/server.go` and `sync/client.go` implement full-file sync for mirror nodes.

`Revision` is the logical cluster-state version. `AppliedRaftIndex` is the last committed Raft entry materialized into `cluster-state.json`. Probe entries may advance applied metadata without advancing `Revision`.

## Raft And Apply Order

```text
RawNode Ready
  -> persist HardState, new entries, and incoming snapshots to ControllerV2 Raft WAL
  -> send Raft messages in etcd-style leader/follower order
  -> enqueue committed entries to the FIFO apply scheduler
  -> batch normal command entries by count/bytes/delay
  -> FSM semantic apply for the batch
  -> save cluster-state.json once for the final batch state
  -> publish in-memory state snapshot
  -> persist raftstore applied metadata once for the batch
  -> trigger snapshot and WAL compaction when thresholds are reached
```

`Revision` is the logical cluster-state version. `AppliedRaftIndex` records the last Raft entry materialized into `cluster-state.json`.
`cluster-state.json` is the materialized ControllerV2 state snapshot; the ControllerV2 Raft WAL is the authoritative committed log and applied-boundary metadata source.

Empty normal Raft entries are also used by `raft.Service.ProbePropose` as non-mutating readiness probes. A probe entry advances Controller Raft applied metadata after it is committed and scheduled, but it is not decoded as a Controller command, does not call the FSM, does not increment logical `Revision`, and does not rewrite `cluster-state.json` business state.

Startup first loads `cluster-state.json`, restores it from the latest Raft snapshot when the state file is empty, then replays any committed WAL suffix after the materialized `AppliedRaftIndex`. The Raft run loop stays responsive because durable WAL append happens before `Advance`, while state-machine IO runs in the scheduler goroutine.

Manual Controller Raft compaction enters the Raft run loop through
`Service.CompactLog`, reads the applied boundary already persisted by the apply
scheduler, snapshots only the materialized Controller state, saves the snapshot
to the raftstore, and compacts WAL entries older than
`SnapshotCatchUpEntries`. A request can be safely skipped when the service is
not started, no applied index exists, no materialized state has been written, or
the latest snapshot already covers the applied boundary.

### Node Health Reports

`ReportNodeHealth` is a low-frequency Controller Raft write that stores one
bounded `NodeHealthReport` per node. It updates durable health evidence and
`AppliedRaftIndex`, but it does not advance the logical cluster-state
`Revision`; placement and lifecycle compare-and-set fences must not race with
heartbeat churn. Consumers that need health freshness compare report age against
the configured TTL instead of interpreting membership `JoinState` as liveness.

## Server Facade Flow

```text
Planner tick: LocalState -> planner.Next -> Raft Propose -> WAL append -> scheduled FSM apply -> statefile save -> StateEvent.
Non-controller sync: SyncOnce -> Controller voter GetState -> statefile save -> LocalState update -> StateEvent.
```

Task progress and task result writes enter ControllerV2 as Raft commands.
Results are fenced by `task_id`, `slot_id`, task kind, `config_epoch`, and
global attempt. Barrier-style tasks also accept participant progress fenced by
participant node and participant attempt. Completed tasks are removed from
active cluster-state tasks, including `leader_transfer` tasks completed through
`complete_task`; failed tasks remain active with bounded errors until a
subsequent successful attempt or operator action.

Node lifecycle writes are also ControllerV2-authoritative Raft commands.
`JoinNode`, `ActivateNode`, `MarkNodeLeaving`, and `MarkNodeRemoved` update the
durable node record through `KindUpsertNode`. `MarkNodeRemoved` only tombstones
an already leaving data node as `removed` and `down`; higher layers must prove
Slot, Channel, task, gateway, and runtime drain safety before calling it. When
the caller provides `ExpectedRevision`, a changed remove write is rejected
unless the current ControllerV2 state still matches the safety-check revision;
an already-removed tombstone remains idempotent and returns `changed=false`
even when a retried request carries a stale revision fence.

Controller voter promotion is split into live Raft preparation and one final
durable state command. `PrepareControllerVoter` runs only on the target node:
it stops mirror refresh, selects the newest valid mirrored `cluster-state.json`
or `cluster-state.mirror-before-controller-voter-promotion.json`, moves the
active mirror state aside, publishes the preserved state, and starts the local
runtime in voter mode with the requested Controller voter set. The Raft package
exposes learner-first membership APIs (`AddLearner`, `PromoteLearner`) for
callers that need to build live membership proof before finalizing durable
metadata. `KindPromoteControllerVoter` is the atomic ControllerV2 state command:
it requires the expected revision/voter fence plus observed Controller Raft
config index and voter set, then adds the Controller voter role to the node,
updates the durable Controller voter table, and remains idempotent when the
node is already consistently promoted.

Slot replica move intent is represented as a staged `slot_replica_move` task.
Creating the task does not change `DesiredPeers`; it only records source,
target, target peer set, assignment epoch, and phase progress fields. Dedicated
phase commands advance `open_learner -> add_learner -> promote_learner ->
remove_voter -> commit_assignment` with task, slot, epoch, attempt, and phase
index fences. Except for the local `open_learner -> add_learner` preparation
step, phase commands also carry observed Slot Raft config/voter/learner state.
The final commit command carries the committing executor's live observed voter
proof, replaces `DesiredPeers`, increments the Slot `ConfigEpoch`, and removes
the task only after both the cached phase observation and commit observation
prove the target peer set.

When a Controller voter wires the facade with an FSM-backed state source, `LocalState` reads that authoritative snapshot and planner ticks refresh it after successful proposals. Command-producing planner ticks require a state source; `InitialState`-only facades may serve local snapshots, sync updates, or non-command planner decisions only.

The root `Runtime` builds one full-file state sync endpoint during voter startup. `Runtime.GetState` delegates to that endpoint instead of constructing sync server wiring on each request. Ready Controller followers may serve their locally committed state file payload with the current leader ID attached; mirrors accept the payload as a valid floor and continue refreshing through the same sync loop.

## Non-Goals

- Do not replace `pkg/controller` in this package.
- Do not own production cluster startup directly; `pkg/cluster/control` provides the package-level integration wrapper.
- Do not store high-frequency runtime observations in `cluster-state.json`.
