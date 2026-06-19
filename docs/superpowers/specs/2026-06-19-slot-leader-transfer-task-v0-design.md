# Slot Leader Transfer Task V0 Design

## Goal

Add the first operator-facing Slot leader transfer workflow to `wukongimv2`
after the ControllerV2 task mechanism has been established.

The v0 workflow is a manual, single-Slot operation. A manager API request
records an operator's preferred leader target in ControllerV2 durable state,
creates a `leader_transfer` task, lets the current Slot Raft leader submit the
Raft leadership-transfer request, and completes the task only after a safe Slot
Raft leader is observed.

## Non-Goals

- Do not add automatic leader balancing in this phase.
- Do not add batch transfer APIs in this phase.
- Do not add node-failure-triggered automatic transfer in this phase.
- Do not make `PreferredLeader` a hard leadership constraint.
- Do not change Slot replica membership, hash-slot ownership, or ChannelV2
  metadata.
- Do not call `pkg/slot/multiraft.Runtime.TransferLeadership` directly from
  manager HTTP handlers.
- Do not migrate legacy `pkg/controller` repair, rebalance, drain, or scale-in
  workflows.
- Do not depend on legacy `internal` paths for the new `wukongimv2` entrypoint.

## Scope

V0 supports only this operator action:

```text
POST /manager/slots/:slot_id/leader-transfer
Body: { "target_node": 2 }
Permission: cluster.slot:w
```

The request is cluster intent scoped to a physical Slot. It is not a
node-local operation. Node-local operations, such as Slot Raft log compaction,
remain under `/manager/nodes/:node_id/...`.

Successful task creation returns `202 Accepted` with a task summary. Idempotent
no-op cases return `200 OK`. Rejected unsafe states return explicit manager
errors instead of creating a task that is likely to hang.

## Current State

The existing code already has most of the substrate needed for this workflow:

- `pkg/controllerv2` persists active tasks in `cluster-state.json`.
- `complete_task` removes completed active tasks.
- `fail_task` retains failed active tasks with bounded error text and advances
  attempts.
- result commands are fenced by task identity, kind, slot, config epoch, and
  attempt.
- `pkg/clusterv2/tasks` already executes bootstrap tasks from control
  snapshots.
- `pkg/slot/multiraft.Runtime.TransferLeadership` already submits a Raft
  leadership-transfer request to the local Slot runtime.
- `internalv2/access/manager` and `internalv2/usecase/management` already
  expose manager Slot inventory and active task summaries.

The missing pieces are:

- ControllerV2 task schema support for `leader_transfer`.
- A Controller-facing intent method to atomically set `PreferredLeader` and
  create the task.
- A manager usecase and HTTP route for manual single-Slot transfer.
- A task executor that respects Slot Raft's real leadership outcome.

## Task Model

Add one task kind and step:

```text
Kind = leader_transfer
Step = transfer_leader
CompletionPolicy = single_observer
```

The task fields have these meanings:

- `SlotID`: physical Slot being transferred.
- `SourceNode`: observed Slot leader when the task is created.
- `TargetNode`: operator-requested preferred leader.
- `TargetPeers`: desired peer set from the current Slot assignment.
- `ConfigEpoch`: current Slot assignment epoch used for fencing.
- `Attempt`: global task attempt, advanced by `fail_task`.
- `Status`: active task state.

The task is tied to the current assignment, but it does not change membership.
Therefore task creation must not increment `ConfigEpoch`.

## Preferred Leader Semantics

`PreferredLeader` is durable operator intent and routing/planner preference. It
is not proof of actual Raft leadership and must not be treated as a mandatory
leader.

Task creation should update the Slot assignment's `PreferredLeader` to
`target_node`. This gives manager UI and future planners a durable target to
converge toward. However, actual leadership always comes from Slot Raft
observation.

If the target node's Slot Raft log is behind, unavailable, or otherwise unable
to safely become leader, Raft must be allowed to keep or elect another legal
leader. The task mechanism must not force leadership in Controller state.

## Safety Checks Before Task Creation

The manager usecase must validate the request against the latest visible
control snapshot and direct Slot runtime observation.

Required checks:

- `slot_id` exists in the control snapshot.
- the Slot has no conflicting active task.
- `target_node` is non-zero.
- `target_node` is in the Slot assignment's `DesiredPeers`.
- the Slot has more than one desired peer; a single-node cluster or single-peer
  Slot has no useful transfer target.
- `target_node` is an active, alive, data-capable node.
- the current actual Slot leader is observable and non-zero.
- the current actual Slot leader is in `DesiredPeers`.
- the current observed voters include `target_node`.
- the observed current voter set is large enough to form quorum.

V0 should treat quorum as a narrow observation abstraction. If the runtime
provider exposes an explicit `HasQuorum` signal, it must be true. If the direct
status source only exposes a non-zero leader and `CurrentVoters`, the v0 check
should require a non-zero leader plus enough observed current voters to form a
quorum. Raft's actual leadership outcome remains the correctness boundary.

Idempotent cases:

- If the actual leader is already `target_node`, return success with
  `Created=false` and do not create a task.
- If an existing active `leader_transfer` task already targets the same Slot
  and node, return the existing task with `Created=false`.

Conflict cases:

- If another active task exists for the Slot, return `409 Conflict`.
- If observation is missing, stale, or unsafe, return `409 Conflict`.

The usecase must not use manager Slot-list fallback values as safety evidence.
The current Slot list can derive runtime-looking values from desired
assignments for display. That fallback is not valid proof for task creation or
task completion.

## Controller Intent API

Manager code should not construct `pkg/controllerv2/command.Command`
directly. Add a narrow control-plane intent facade, shaped like:

```go
type SlotLeaderTransferRequest struct {
    SlotID     uint32
    TargetNode uint64
}

type SlotLeaderTransferResult struct {
    SlotID          uint32
    TargetNode      uint64
    PreferredLeader uint64
    ActualLeader    uint64
    Created         bool
    Task            *ReconcileTask
    Message         string
}

RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error)
```

The facade owns ControllerV2 command construction:

```text
ExpectedRevision = current snapshot revision
Assignment.PreferredLeader = target_node
Task.Kind = leader_transfer
Task.Step = transfer_leader
Task.SourceNode = observed actual leader
Task.TargetNode = target_node
Task.TargetPeers = assignment.DesiredPeers
Task.ConfigEpoch = assignment.ConfigEpoch
Task.CompletionPolicy = single_observer
```

The command should atomically upsert the updated assignment and task through
ControllerV2 Raft. It should be routed to the current Controller leader, using
the same forwarding principles as task progress/result writes.

## Manager HTTP API

Add the route in `internalv2/access/manager`:

```text
POST /manager/slots/:slot_id/leader-transfer
```

Request:

```json
{
  "target_node": 2
}
```

Response shape should mirror the usecase result:

```json
{
  "slot_id": 1,
  "target_node": 2,
  "preferred_leader": 2,
  "actual_leader": 1,
  "created": true,
  "task": {
    "task_id": "slot-1-leader-transfer-...",
    "kind": "leader_transfer",
    "step": "transfer_leader",
    "status": "pending",
    "source_node": 1,
    "target_node": 2,
    "target_peers": [1, 2, 3],
    "completion_policy": "single_observer",
    "config_epoch": 1,
    "attempt": 0
  },
  "message": "leader transfer task created"
}
```

HTTP status mapping:

- `202 Accepted`: task created.
- `200 OK`: already leader or matching task already exists.
- `400 Bad Request`: invalid `slot_id` or `target_node`.
- `404 Not Found`: Slot does not exist.
- `409 Conflict`: active task conflict or unsafe Slot runtime state.
- `503 Service Unavailable`: Controller write path is unavailable.

## Executor

Add a `LeaderTransferExecutor` under `pkg/clusterv2/tasks`.

The executor watches control snapshots like the bootstrap executor. For each
active `leader_transfer` task:

```text
read local multiraft.Status(slot)
if actual legal leader is no longer SourceNode:
  CompleteTask
else if local node is the current Slot leader:
  call TransferLeadership(slot, TargetNode)
  observe the Slot for a bounded interval
  CompleteTask when a legal non-source leader is observed
  FailTask on transfer error or timeout
else:
  no-op
```

Only the current actual Slot leader should call `TransferLeadership`. Other
nodes may observe, but they must not try to force leadership.

The executor must be idempotent. Re-seeing the same task after restart should
either prove it is already done, retry the same bounded action, or report a
bounded failure.

## Completion Semantics

The task completes when Slot Raft has reached a safe observed leader state. It
does not require `TargetNode` to become leader.

Completion conditions:

- `LeaderID == TargetNode`, or
- `LeaderID` is non-zero, is in `CurrentVoters`, is in `DesiredPeers`, the
  observed current voter set can form quorum, and `LeaderID != SourceNode`.

The second condition covers the case where the target cannot safely become
leader and Raft elects or keeps another legal leader after the transfer
attempt. The task succeeded in moving away from the original source without
violating Raft.

Non-completion conditions:

- leader is still `SourceNode`
- leader is zero or unobservable
- leader is outside current voters or desired peers
- current voters are missing or cannot form quorum
- task fencing fields no longer match the active task

If the bounded observation window expires without completion, submit
`FailTask`. The task remains visible and can be retried; `PreferredLeader` is
not cleared.

## Raft Safety

ControllerV2 never declares the target leader by fiat. It records preference
and task intent only.

`multiraft.TransferLeadership` delegates leadership transfer to the Slot Raft
implementation. Raft decides whether the target can safely lead based on its
own log and election rules. The Controller task layer does not need per-peer
match indexes for correctness in v0.

Future versions may expose leader-side voter progress, such as match index and
recent activity, to reject unlikely transfers earlier. That is an optimization
and operator feedback improvement, not a correctness requirement for v0.

## Error Handling and Retry

Creation-time errors must not write partial tasks. The usecase should return a
typed error that manager HTTP maps to the status codes above.

Executor-time errors should be written through `FailTask`:

- transfer call returned an error
- local status read failed
- bounded observation timed out
- observed voters became unsafe or could not form quorum

`FailTask` keeps the active task and increments `Attempt`, preserving the
existing retry model. Completion and failure commands remain fenced by task
kind, slot, config epoch, and attempt, so stale executors cannot overwrite a
newer task attempt.

## Future Batch and Automation Path

This v0 intentionally creates a reusable single-Slot primitive.

Future batch transfer can reuse it by selecting many Slot/target pairs and
creating one `leader_transfer` task per Slot. A future node-failure workflow
can do the same after it determines which Slots need leadership moved away
from an unhealthy node.

Future automatic planner work should add:

- observed leader-load distribution
- cooldown to avoid transfer churn
- batch throttling
- target selection based on alive data nodes and current voters
- optional voter progress checks for better preflight feedback

Those features must build on the same task kind instead of bypassing it.

## Testing Strategy

### ControllerV2 State and FSM

- `leader_transfer/transfer_leader` is a valid task kind and step.
- The task must bind to an existing Slot assignment.
- `TargetNode` must match assignment `PreferredLeader`.
- `CompletionPolicy=single_observer` must not include participant progress.
- `CompleteTask` and `FailTask` fencing works for kind, slot, epoch, and
  attempt.
- One active task per Slot remains enforced.

### Control Intent

- request updates `PreferredLeader` without incrementing `ConfigEpoch`
- request creates a `leader_transfer` task
- request uses `ExpectedRevision`
- active task conflict is rejected
- matching duplicate requests are idempotent
- already-leader requests do not create tasks

### LeaderTransferExecutor

- current Slot leader calls `TransferLeadership`
- non-leader nodes do not call `TransferLeadership`
- `LeaderID == TargetNode` completes the task
- `LeaderID` changing to another legal voter also completes the task
- unknown leader, quorum-incapable current voters, or illegal voters do not
  complete the task
- transfer errors and observation timeouts call `FailTask`
- stale attempts are no-ops through existing ControllerV2 fencing

### Manager HTTP and Usecase

- route requires `cluster.slot:w` when manager auth is enabled
- invalid body returns `400`
- missing Slot returns `404`
- already leader returns `200` with `created=false`
- task creation returns `202` with `created=true`
- conflicting active task returns `409`
- unsafe runtime state returns `409`
- Controller unavailable returns `503`

### E2E

Add one narrow `test/e2ev2` scenario after unit coverage:

```text
start a 3-node wukongimv2 cluster
wait for a Slot leader
choose a follower as target
POST /manager/slots/:slot_id/leader-transfer
wait for the task to disappear from active tasks
assert actual leader is legal and observed
assert PreferredLeader remains the requested target
```

The e2e may log whether the target became leader, but the required assertion is
that the final actual leader is legal and the task completes safely. Target
leadership is a preferred outcome, not a correctness requirement.

## Acceptance Criteria

- Manual manager API can create one Slot leader-transfer task.
- Completed transfer tasks are removed from active `cluster-state.json` state.
- Failed transfer tasks remain visible with bounded error text.
- `PreferredLeader` is durable preference, not forced actual leadership.
- Actual leader is always derived from Slot Raft observation.
- The workflow does not touch legacy `internal` implementation paths.
- No batch or automatic balancing behavior is implemented in v0.
- The design leaves a clear reusable path for future batch and failure-driven
  transfer workflows.
