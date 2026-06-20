# Controller Task Observability V1 Design

## Goal

Expose the active ControllerV2 task set through the `wukongimv2` manager API so
operators can answer:

- which tasks are currently active
- which Slot, kind, status, and nodes each task involves
- whether a task is failed and why
- whether a task is still pending/running or already gone from active state

This is the observability layer for the Controller task mechanism. It prepares
the system for later batch Slot leader transfer and automatic recovery work by
making active task state easy to inspect before adding more task producers.

## Non-Goals

- Do not add task history in this phase.
- Do not persist completed task records.
- Do not change `cluster-state.json` schema.
- Do not read or decode Controller Raft logs for task listing.
- Do not add cancellation, retry, pause, resume, or mutation APIs.
- Do not implement batch Slot leader transfer in this phase.
- Do not expose node-failure-triggered automation in this phase.
- Do not import legacy `internal` packages.

## Current State

ControllerV2 already persists active tasks in the control snapshot:

- active tasks live in `control.Snapshot.Tasks`
- completed tasks are removed by `complete_task`
- failed tasks remain active with bounded `LastError` and incremented
  `Attempt`
- task progress/result commands are fenced by task ID, Slot, kind, config
  epoch, and attempt
- `internalv2/usecase/management.ListSlots` already attaches one active Slot
  task summary to each Slot row
- `internalv2/access/manager` exposes Slot inventory and Slot leader-transfer
  creation, but there is no cluster-wide task list or task detail route

Slot list task summaries are useful beside Slot rows, but they do not provide a
focused operator view for task filtering, task detail lookup, or future batch
task diagnosis.

## Scope

V1 adds read-only manager task inspection:

```text
GET /manager/controller/tasks
GET /manager/controller/tasks/:task_id
```

Both routes require `cluster.controller:r` when manager auth is enabled.

The routes are backed only by the local control snapshot. A non-controller node
may serve the route from its mirrored ControllerV2 snapshot, the same way other
manager read models use `LocalControlSnapshot`.

## Data Flow

```text
manager HTTP
  -> internalv2/usecase/management.App.ListControllerTasks
  -> ControlSnapshotReader.LocalControlSnapshot
  -> clusterv2/control.Snapshot.Tasks
  -> sorted manager ControllerTask rows

manager HTTP
  -> internalv2/usecase/management.App.ControllerTask
  -> ListControllerTasks with exact task_id match
  -> one manager ControllerTask row or not_found
```

HTTP parsing, permissions, and JSON envelopes stay in
`internalv2/access/manager`. Task projection, filtering, sorting, and not-found
semantics stay in `internalv2/usecase/management`.

No new clusterv2 or controllerv2 write surface is needed.

## Usecase Model

Define task-specific management DTOs instead of reusing `SlotTask` directly.
The fields intentionally mirror current active task state.

```go
type ControllerTask struct {
    TaskID           string
    SlotID           uint32
    Kind             string
    Step             string
    Status           string
    SourceNode       uint64
    TargetNode       uint64
    TargetPeers      []uint64
    CompletionPolicy string
    ConfigEpoch      uint64
    Attempt          uint32
    LastError        string
    Participants     []ControllerTaskParticipant
}

type ControllerTaskParticipant struct {
    NodeID    uint64
    Attempt   uint32
    Status    string
    LastError string
}
```

The usecase must clone slices when mapping from `control.ReconcileTask` so
callers cannot mutate snapshot-backed data.

## List Query

`GET /manager/controller/tasks` supports:

```text
kind=bootstrap|leader_transfer
status=pending|running|failed
slot_id=1
node_id=2
limit=100
```

Filtering rules:

- `kind` matches task kind exactly.
- `status` matches task status exactly.
- `slot_id` matches task `SlotID`.
- `node_id` matches any task-related node:
  - `SourceNode`
  - `TargetNode`
  - any `TargetPeers` entry
  - any participant `NodeID`

Limit rules:

- empty or zero `limit` uses default `100`
- maximum `limit` is `500`
- negative, unparsable, or over-limit values return `400 bad_request`

Sorting is stable and deterministic:

```text
slot_id asc, kind asc, task_id asc
```

V1 does not add cursor pagination. Active task cardinality should remain small
until batch workflows are introduced. Cursor pagination can be added when batch
tasks make the active set large enough to require it.

## Detail Query

`GET /manager/controller/tasks/:task_id` returns the active task with exactly
that `task_id`.

Responses:

- found: `200 OK` with one task DTO
- missing: `404 not_found`

Only active tasks can be returned. Completed tasks are intentionally absent
because ControllerV2 removes completed active tasks from `cluster-state.json`.

## HTTP Response Shape

List response:

```json
{
  "total": 1,
  "items": [
    {
      "task_id": "slot-1-leader-transfer-7-r9",
      "slot_id": 1,
      "kind": "leader_transfer",
      "step": "transfer_leader",
      "status": "pending",
      "source_node": 1,
      "target_node": 2,
      "target_peers": [1, 2, 3],
      "completion_policy": "single_observer",
      "config_epoch": 7,
      "attempt": 0,
      "last_error": "",
      "participants": []
    }
  ]
}
```

Detail response returns the same task DTO without a list envelope:

```json
{
  "task_id": "slot-1-leader-transfer-7-r9",
  "slot_id": 1,
  "kind": "leader_transfer",
  "step": "transfer_leader",
  "status": "pending",
  "source_node": 1,
  "target_node": 2,
  "target_peers": [1, 2, 3],
  "completion_policy": "single_observer",
  "config_epoch": 7,
  "attempt": 0,
  "last_error": "",
  "participants": []
}
```

## Error Handling

HTTP layer:

- invalid `kind`, `status`, `slot_id`, `node_id`, or `limit`:
  `400 bad_request`
- control snapshot unavailable:
  `503 service_unavailable`
- detail task missing:
  `404 not_found`

Usecase layer:

- context cancellation propagates
- unavailable control snapshot errors propagate for HTTP mapping
- missing detail task returns a typed management error such as
  `ErrControllerTaskNotFound`

The list route should return an empty list when no task matches valid filters.

## Testing

Usecase tests in `internalv2/usecase/management`:

- list active tasks from a control snapshot
- filter by kind
- filter by status
- filter by slot ID
- filter by related node ID across source, target, target peers, and
  participants
- enforce default and maximum limit
- detail found and not found
- clone target peers and participant slices

HTTP tests in `internalv2/access/manager`:

- list JSON shape
- detail JSON shape
- invalid query parameters return `400`
- missing detail returns `404`
- control snapshot unavailable returns `503`
- route requires `cluster.controller:r` when auth is enabled

No e2e test is required for v1 unless implementation touches composition
wiring unexpectedly. The feature is a read-only projection over an existing
manager control snapshot port.

## Future Work

Task history should be designed separately before batch workflows depend on
it. A future history design should answer:

- whether history is stored in Controller durable state, a separate file, or an
  external diagnostics/event sink
- retention limits
- whether failed active tasks and completed historical records share one DTO
- how batch parent/child tasks are represented
- how history survives node restarts and mirror lag

Batch Slot leader transfer and node-failure-triggered automatic transfer
should wait until this active-task observability surface exists, so operators
can see in-flight work and diagnose failures before automation creates many
tasks.
