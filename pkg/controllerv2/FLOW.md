# pkg/controllerv2 Flow

## Responsibility

`pkg/controllerv2` is a parallel Controller implementation. Controller voter nodes apply committed Controller Raft commands to a canonical `cluster-state.json` file. Non-controller nodes do not join Controller Raft; they mirror the leader state file through full-file sync.

`pkg/clusterv2/control` hosts the production-shaped integration wrapper; `pkg/controllerv2` remains the reusable Controller engine and does not import `pkg/clusterv2`.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `state` | Durable JSON model, normalization, validation, checksum, initial hash-slot table. |
| `statefile` | Atomic load/save for `cluster-state.json`. |
| `command` | Versioned Raft command envelope. |
| `fsm` | Applies committed command batches, persists the final state once per batch, publishes snapshots after durable save. |
| `planner` | Pure planning. V1 only creates bootstrap assignment/task commands. |
| `sync` | Full-file leader sync for non-controller nodes. |
| `raft` | Controller Raft wrapper, WAL-backed log storage, scheduled apply, snapshots, and compaction. |
| `server` | Thin composition facade for tests and future integration. |

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

Startup first loads `cluster-state.json`, restores it from the latest Raft snapshot when the state file is empty, then replays any committed WAL suffix after the materialized `AppliedRaftIndex`. The Raft run loop stays responsive because durable WAL append happens before `Advance`, while state-machine IO runs in the scheduler goroutine.

## Server Facade Flow

```text
Planner tick: LocalState -> planner.Next -> Raft Propose -> WAL append -> scheduled FSM apply -> statefile save.
Non-controller sync: SyncOnce -> leader GetState -> statefile save -> LocalState update.
```

When a Controller voter wires the facade with an FSM-backed state source, `LocalState` reads that authoritative snapshot and planner ticks refresh it after successful proposals. Command-producing planner ticks require a state source; `InitialState`-only facades may serve local snapshots, sync updates, or non-command planner decisions only.

## Non-Goals

- Do not replace `pkg/controller` in this package.
- Do not own production cluster startup directly; `pkg/clusterv2/control` provides the package-level integration wrapper.
- Do not store high-frequency runtime observations in `cluster-state.json`.
