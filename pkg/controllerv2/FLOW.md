# pkg/controllerv2 Flow

## Responsibility

`pkg/controllerv2` is a parallel Controller implementation. Controller voter nodes apply committed Controller Raft commands to a canonical `cluster-state.json` file. Non-controller nodes do not join Controller Raft; they mirror the leader state file through full-file sync.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `state` | Durable JSON model, normalization, validation, checksum, initial hash-slot table. |
| `statefile` | Atomic load/save for `cluster-state.json`. |
| `command` | Versioned Raft command envelope. |
| `fsm` | Applies committed commands, persists final state, publishes snapshots after durable save. |
| `planner` | Pure planning. V1 only creates bootstrap assignment/task commands. |
| `sync` | Full-file leader sync for non-controller nodes. |
| `raft` | Controller Raft wrapper and apply loop. |
| `server` | Thin composition facade for tests and future integration. |

## Apply Order

```text
Raft commit -> semantic apply -> save cluster-state.json -> publish in-memory state -> MarkApplied
```

`Revision` is the logical cluster-state version. `AppliedRaftIndex` records which Raft entry produced the file. `pkg/raftlog` remains the authoritative local applied boundary.

## Server Facade Flow

```text
Planner tick: LocalState -> planner.Next -> Raft Propose -> FSM Apply -> statefile save.
Non-controller sync: SyncOnce -> leader GetState -> statefile save -> LocalState update.
```

## Non-Goals

- Do not replace `pkg/controller` in this package.
- Do not wire production cluster startup to v2 yet.
- Do not store high-frequency runtime observations in `cluster-state.json`.
