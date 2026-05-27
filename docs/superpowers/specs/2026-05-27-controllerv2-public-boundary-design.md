# ControllerV2 Public Boundary Design

## Goal

Shrink `pkg/controllerv2` so external callers only depend on a narrow runtime surface: Controller network handlers and strongly typed `ClusterState` change events. `pkg/clusterv2` should consume those events and decide which local runtime areas need reconciliation.

## Architecture

`pkg/controllerv2` owns the durable Controller engine. Raft, FSM, planner, state-file IO, and full-file sync stay implementation details behind one root-package runtime facade.

The public surface should expose:

- `ClusterState`, the strongly typed durable state payload carried by change events.
- `StateEvent`, emitted after a valid `cluster-state.json` state becomes locally visible.
- `Runtime`, with `Start`, `Stop`, `Watch`, `LeaderID`, `ProbePropose`, Raft message handling, and state sync handling.
- Small network request/response types required to carry Controller Raft and state sync RPCs.

`pkg/clusterv2/control` adapts this runtime into the existing `control.Controller` interface. It should stop importing `pkg/controllerv2/command`, `fsm`, `raft`, `server`, `statefile`, and `sync` directly. It may import only the root `pkg/controllerv2` package.

## Data Flow

```text
ControllerV2 Runtime
  -> applies or syncs a valid cluster-state.json
  -> publishes StateEvent{State: ClusterState}
  -> clusterv2/control maps ClusterState to control.Snapshot
  -> clusterv2.Node compares/applies snapshot domains
  -> nodes update discovery/node runtime
  -> slots update slot runtime
  -> tasks update task execution path
  -> hash-slot table updates router
```

`clusterv2` owns domain-level interpretation. `controllerv2` only states that the canonical cluster state changed.

## Change Detection In clusterv2

`clusterv2.Node` should eventually apply snapshots by domain instead of treating every revision as a full opaque update:

- Node changes update discovery and future node lifecycle handling.
- Slot changes reconcile local Slot runtime assignments.
- Task changes feed task-specific convergence handlers.
- Hash-slot table changes rebuild foreground routing.

The first implementation can keep the existing full `applySnapshot` behavior while adding comparison helpers and tests that make the intended split explicit.

## Error Handling

ControllerV2 should publish events only after validation and local visibility are complete. Invalid states stay internal errors and must not be emitted.

`clusterv2/control` should reject invalid mapped snapshots before publishing them to `Node`. A slow consumer must not block Controller progress; the existing bounded watch channel behavior is acceptable for v1.

## Testing

Add focused unit tests before implementation:

- ControllerV2 root runtime emits a strongly typed state event after voter apply or mirror sync.
- ControllerV2 root network handlers hide internal sync/Raft package details from `clusterv2`.
- `clusterv2/control` imports only the root `pkg/controllerv2` facade for production code.
- `clusterv2.Node` applies node, slot, task, and hash-slot changes through explicit domain comparison helpers.

Run targeted package tests first:

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/...
```

Then run the broader unit suite if the change touches shared contracts:

```bash
go test ./...
```
