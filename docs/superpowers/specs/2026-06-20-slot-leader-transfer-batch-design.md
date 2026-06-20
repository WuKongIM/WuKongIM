# Slot Leader Transfer Batch Design

## Goal

Build the next layer on top of the existing ControllerV2 task mechanism and
single-Slot leader-transfer task: a safe operator-facing batch plan and manual
execution workflow.

The first implementation should let an operator answer:

- which Slots would move away from a selected source node
- which Slots are skipped and why
- which target node would be preferred for each candidate Slot
- whether the plan is still valid for the current Controller state revision
- which existing `leader_transfer` tasks were reused instead of duplicated

This is a manual operations workflow. It prepares for future node-failure or
node-drain automation, but it does not add automatic triggers in this phase.

## Non-Goals

- Do not create a second task mechanism.
- Do not bypass the existing single-Slot `leader_transfer` task path.
- Do not call Slot Raft `TransferLeadership` from manager HTTP handlers.
- Do not make `PreferredLeader` a hard Raft leadership constraint.
- Do not store completed task history in `cluster-state.json`.
- Do not add a durable batch-task or batch-history model in this phase.
- Do not add automatic node-failure-triggered transfers in this phase.
- Do not change Slot replica membership, hash-slot ownership, or ChannelV2
  metadata.

## Existing State

The repo already has the core pieces this design must reuse:

- ControllerV2 active tasks live in `cluster-state.json`.
- Completed tasks are removed from active state by `complete_task`.
- Failed tasks remain active with bounded `LastError`.
- Task result/progress writes go through ControllerV2 Raft.
- `leader_transfer` task creation exists for a single physical Slot.
- `internalv2` exposes `POST /manager/slots/:slot_id/leader-transfer`.
- `internalv2` exposes read-only Controller task inspection through
  `/manager/controller/tasks`.
- The web manager already has a distributed task page under `/cluster/tasks`.
- The `pkg/clusterv2/tasks.LeaderTransferExecutor` completes when the observed
  Slot Raft leader is legal and no longer the task source; the requested target
  remains a preference.

Therefore the batch design should be thin: it selects candidates, fences the
batch against a state revision, then submits each selected candidate through the
same single-Slot intent path.

## Core Semantics

### Preferred Leader Is Soft Intent

`TargetNode` and `PreferredLeader` are operator intent and planner preference.
They are not the Raft source of truth.

Actual Slot leadership is determined only by Slot Raft observation. If the
preferred target is behind, unavailable, or not elected, Raft may keep or elect
another legal non-source leader. A `leader_transfer` task may complete when the
observed leader is:

- non-zero
- not the task `SourceNode`
- in the Slot assignment's desired peers
- in the observed current voters
- backed by enough voters for quorum

The batch workflow must not wait forever for the preferred target to become the
actual leader when Raft has already produced a safe alternative.

### Batch Is A Producer, Not A Scheduler

The batch workflow creates or reuses ordinary `leader_transfer` tasks. It does
not own execution. Existing node-local task executors continue to run active
tasks from control snapshots.

The first batch implementation limits risk by bounding how many tasks it
creates in one request. It does not need a durable batch scheduler. Operators
can run a second batch after the first wave converges.

### Planning Is Read-Only

Planning must never mutate Controller state. It reads:

- latest local control snapshot
- Slot assignments
- active Controller tasks
- node liveness and roles
- direct Slot Raft runtime status for candidate Slots

The response includes a `state_revision` and deterministic `plan_id`. Execution
must reject stale plans instead of applying a plan produced from an older
control snapshot.

## Manager API

### Plan Route

Add a read-only plan endpoint:

```text
POST /manager/slots/leader-transfer-plan
Permission: cluster.slot:r
```

Request:

```json
{
  "source_node_id": 1,
  "target_node_id": 0,
  "slot_ids": [1, 2, 3],
  "max_tasks": 32,
  "target_policy": "least_leaders"
}
```

Fields:

- `source_node_id`: node whose Slot leadership should move away. Required.
- `target_node_id`: optional preferred target for every candidate. Zero means
  choose a per-Slot target by policy.
- `slot_ids`: optional physical Slot allow-list. Empty means scan all Slots.
- `max_tasks`: maximum candidates that execution may create in one batch.
- `target_policy`: target choice when `target_node_id` is zero. V1 supports
  `least_leaders`.

Response:

```json
{
  "generated_at": "2026-06-20T10:00:00Z",
  "state_revision": 42,
  "plan_id": "slot-leader-transfer:42:...",
  "source_node_id": 1,
  "target_policy": "least_leaders",
  "max_tasks": 32,
  "summary": {
    "scanned": 128,
    "candidates": 12,
    "skipped": 116,
    "existing_tasks": 2,
    "would_create": 10
  },
  "candidates": [
    {
      "slot_id": 7,
      "source_node_id": 1,
      "target_node_id": 2,
      "preferred_leader": 1,
      "actual_leader": 1,
      "desired_peers": [1, 2, 3],
      "current_voters": [1, 2, 3],
      "config_epoch": 9,
      "existing_task_id": "",
      "action": "create"
    }
  ],
  "skipped": [
    {
      "slot_id": 8,
      "reason": "target_not_current_voter",
      "message": "target node is not in observed Slot voters"
    }
  ]
}
```

`plan_id` is not durable state. It is a deterministic digest of:

- request criteria
- `state_revision`
- ordered candidate Slot IDs
- chosen target nodes
- assignment config epochs

The execute route can recompute the plan and compare the digest. This avoids a
new persistent plan table while still protecting operators from stale plans.

### Execute Route

Add a manual batch execution endpoint:

```text
POST /manager/slots/leader-transfer-batch
Permission: cluster.slot:w
```

Request:

```json
{
  "source_node_id": 1,
  "target_node_id": 0,
  "slot_ids": [1, 2, 3],
  "max_tasks": 32,
  "target_policy": "least_leaders",
  "state_revision": 42,
  "plan_id": "slot-leader-transfer:42:..."
}
```

Execution re-runs the planner against the current snapshot. It proceeds only
when:

- current `state_revision` equals the request `state_revision`
- recomputed `plan_id` equals the request `plan_id`
- request fields are valid

The route submits each candidate by calling the same single-Slot usecase path
used by `POST /manager/slots/:slot_id/leader-transfer`.

Response:

```json
{
  "generated_at": "2026-06-20T10:00:02Z",
  "state_revision": 42,
  "plan_id": "slot-leader-transfer:42:...",
  "summary": {
    "requested": 12,
    "created": 10,
    "existing": 2,
    "already_leader": 0,
    "skipped": 0,
    "failed": 0
  },
  "results": [
    {
      "slot_id": 7,
      "target_node_id": 2,
      "status": "created",
      "task_id": "slot-7-leader-transfer-9-r42",
      "message": "leader transfer task created"
    }
  ]
}
```

HTTP status mapping:

- `200 OK`: plan executed but no new task was created, for example all entries
  were existing tasks or already leaders.
- `202 Accepted`: at least one task was created.
- `400 Bad Request`: malformed request, invalid node, invalid limit, or invalid
  target policy.
- `409 Conflict`: stale `state_revision`, mismatched `plan_id`, active task
  conflict, or unsafe runtime state.
- `503 Service Unavailable`: control snapshot, Slot runtime status, or
  Controller write path is unavailable.

Batch execution may return per-slot failures only after the global revision and
plan checks pass. If the global plan is stale, it must fail before creating any
task.

## Candidate Selection

The planner scans Slots in stable `slot_id` order.

A Slot is eligible when:

- it is in the optional `slot_ids` allow-list, if provided
- it has an assignment with at least two desired peers
- `source_node_id` is in desired peers
- direct Slot runtime status is available
- actual leader is non-zero
- actual leader equals `source_node_id`, or durable `PreferredLeader` equals
  `source_node_id` after Raft has already moved leadership elsewhere
- current voters include enough peers for quorum
- there is no conflicting active task for this Slot
- a legal target can be selected

The `PreferredLeader == source_node_id` case is important for node-failure
follow-up. If Raft has already elected a different legal leader after a source
node failed, the batch can still correct durable preferred placement by creating
a normal `leader_transfer` task. The task may complete immediately because the
observed actual leader is already legal and no longer the source.

### Target Selection

When `target_node_id` is non-zero, every candidate uses that target. The target
must be:

- in assignment desired peers
- in observed current voters
- alive
- data-capable
- not equal to `source_node_id`

When `target_node_id` is zero, `target_policy = least_leaders` chooses a target
per Slot:

1. Start from alive data nodes that are both desired peers and current voters.
2. Remove `source_node_id`.
3. Prefer the node with the lowest projected Slot leader count.
4. Break ties by lower node ID.
5. After selecting a target for one candidate, update the projected counts so
   the rest of the plan trends toward balance.

Projected counts use the current actual leader distribution plus already
planned transfers. This keeps the first batch simple while avoiding obvious
leader concentration.

For a candidate whose actual leader is still `source_node_id`, selecting a
target decrements the projected count for `source_node_id` and increments the
target. For a candidate whose actual leader has already moved away from
`source_node_id` and only `PreferredLeader` still points at the source, the
planner must not decrement the source count again. It should treat the actual
leader distribution as already moved and only use the selected target as the
new preferred placement for future candidates.

### Skip Reasons

Skip reasons must be stable strings so tests and future UI labels can rely on
them:

- `slot_not_allowed`
- `assignment_missing`
- `single_peer_slot`
- `source_not_desired_peer`
- `runtime_unavailable`
- `leader_unknown`
- `source_not_leader_or_preferred`
- `quorum_unavailable`
- `active_task_conflict`
- `matching_task_exists`
- `target_invalid`
- `target_not_alive_data_node`
- `target_not_desired_peer`
- `target_not_current_voter`
- `already_on_target`
- `max_tasks_reached`

`matching_task_exists` is not an error. It is counted separately so the plan can
show idempotent reuse.

## Data Flow

### Plan

```text
manager HTTP
  -> internalv2/usecase/management.PlanSlotLeaderTransfers
  -> LocalControlSnapshot
  -> SlotRuntimeStatus(slot, desired peers)
  -> candidate/skip rows
  -> deterministic plan_id
```

### Execute

```text
manager HTTP
  -> internalv2/usecase/management.ExecuteSlotLeaderTransferBatch
  -> recompute plan and verify state_revision + plan_id
  -> for each candidate:
       RequestSlotLeaderTransfer(slot_id, target_node)
       -> existing single-Slot validation
       -> control.RequestSlotLeaderTransfer
       -> ControllerV2 Raft command
       -> active leader_transfer task
  -> task executors observe control snapshot and complete/fail tasks
```

The batch usecase may call a shared internal planner helper, but the actual
task write must stay behind the existing single-Slot writer path.

## UI Shape

The first UI can be a conservative extension of existing cluster operations
surfaces:

- a "Plan leader transfers" action from the Slot page or task page
- a plan preview table with candidate and skipped rows
- a confirm action that submits the exact `state_revision` and `plan_id`
- links from execution results to `/cluster/tasks`

The existing `/cluster/tasks` page remains the source of truth for active task
progress. A separate batch history page is out of scope until batch history is
durably modeled.

## Error Handling

The batch usecase should fail closed:

- stale revision: no task writes
- plan digest mismatch: no task writes
- missing runtime status: skip in plan; conflict in execute if it invalidates
  a previously candidate Slot
- unsafe target: skip in plan; conflict in execute if global criteria are
  impossible
- Controller write failure for one candidate: return per-slot failure after
  previously accepted task writes; do not attempt rollback

No rollback is needed because each accepted candidate is an ordinary fenced
Controller task. Re-running the same batch after a partial write should return
existing task rows for already accepted candidates and create only missing
eligible tasks.

## Future Automatic Trigger

Automatic node-failure handling should be a caller of this batch planner, not a
separate mutation path.

Before automatic execution is allowed, a future design must add:

- node health debounce window
- cooldown after a failed or partial batch
- maximum active leader-transfer tasks cluster-wide
- operator override to disable automation
- metrics and audit events for automatic batches
- clear behavior for network partitions where source-node liveness is
  ambiguous

The automatic trigger should produce the same plan shape and task rows as the
manual path. That keeps manual dry-run, UI review, and automation explainable
with one model.

## Testing

### Usecase Tests

Add tests in `internalv2/usecase/management` for:

- plan selects only Slots led by `source_node_id`
- plan can correct `PreferredLeader == source_node_id` after actual leader has
  already moved
- explicit target is validated against desired peers, current voters, and alive
  data nodes
- `least_leaders` balances projected target counts
- existing matching tasks are reported as existing instead of duplicated
- conflicting active tasks are skipped or rejected
- `max_tasks` bounds candidate count with stable `max_tasks_reached` skips
- execute rejects stale `state_revision`
- execute rejects mismatched `plan_id`
- execute reuses the single-Slot writer and returns per-slot result rows

### HTTP Tests

Add tests in `internalv2/access/manager` for:

- plan route request parsing and permission
- execute route request parsing and permission
- `202 Accepted` when at least one task is created
- `200 OK` when all selected items are existing or already leaders
- `409 Conflict` for stale plan
- `503 Service Unavailable` for unavailable planning or write dependencies

### Controller And Cluster Tests

Keep existing single-Slot task tests as the foundation. Add focused coverage
only where batch behavior touches lower layers:

- no extra ControllerV2 command kind is needed for batch
- repeated execution remains idempotent because single-Slot task creation
  deduplicates by active task
- leader-transfer executor still completes when actual leader is legal but not
  the preferred target

### E2E / Black-Box Tests

Add a narrow e2ev2 or cluster black-box scenario after usecase and HTTP tests:

- start a three-node `wukongimv2` cluster
- pick a source node with at least one observed Slot leader
- dry-run a batch away from that source
- execute a bounded batch
- poll `/manager/controller/tasks` or `/cluster/tasks` until created tasks
  disappear from active state or fail visibly
- assert final Slot leaders are legal and no completed tasks remain active

This test should be integration-tagged if it starts real processes or waits on
real Raft timing.

## Implementation Boundary

The implementation plan should be split into small commits:

1. Management usecase planner and DTO tests.
2. Manager HTTP plan/execute routes and tests.
3. Web plan preview and submit flow, if the user wants UI in the same phase.
4. Focused e2e/integration verification.

The first backend-only slice is useful without the web UI because operators and
future automation can call the manager API directly.
