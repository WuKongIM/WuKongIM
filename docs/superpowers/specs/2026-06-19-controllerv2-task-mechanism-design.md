# ControllerV2 Task Mechanism Design

## Goal

Build a durable task mechanism for `pkg/controllerv2` before adding Slot
leader transfer operations to `wukongimv2`.

The first implementation should prove the generic task lifecycle with the
existing bootstrap task kind. Slot leader transfer, repair, rebalance, node
drain, and automatic batch transfer are follow-up task kinds that must reuse
the same mechanism instead of adding one-off manager HTTP actions.

## Non-Goals

- Do not implement `POST /manager/slots/:slot_id/leader/transfer` in this
  phase.
- Do not call `pkg/slot/multiraft.Runtime.TransferLeadership` directly from
  manager HTTP handlers.
- Do not migrate all legacy `pkg/controller` repair, rebalance, onboarding, or
  scale-in behavior at once.
- Do not persist high-frequency runtime observations in `cluster-state.json`.
- Do not introduce a broad service layer above `controllerv2`.

## Current State

`pkg/controllerv2` already has the durable pieces needed for a task mechanism:

- `state.ReconcileTask` with `TaskID`, `SlotID`, `Kind`, `Step`, `TargetNode`,
  `TargetPeers`, `ConfigEpoch`, `Attempt`, `Status`, and `LastError`.
- Controller Raft commands for `upsert_slot_assignment_and_task`,
  `complete_task`, and `fail_task`.
- FSM validation that allows only one active task per Slot.
- A bootstrap planner that creates bootstrap assignments and tasks.
- `pkg/clusterv2/control` exposes active tasks through control snapshots.

The missing piece is the execution and observation loop:

```text
Planner -> durable task -> node executor -> task result command -> observed convergence
```

## Architecture

The task mechanism is split into five responsibilities.

### 1. Observation

Observation collects data used to decide whether a task is needed and whether
an active task has converged.

Durable state comes from `controllerv2.ClusterState`:

- nodes
- slot assignments
- hash-slot table
- active tasks
- cluster config

Runtime observation is read outside the durable state file:

- Slot Raft leader
- local node Slot role
- current voters
- quorum state
- commit/apply watermarks
- node liveness

Runtime observation is an input to planning and task completion checks. It must
not become a high-frequency replicated data model.

### 2. Planner

Only the Controller leader creates task commands.

A planner compares durable state plus runtime observation and returns at most
one command per tick. The first version should keep the existing bootstrap
planner and add the missing execution lifecycle around it.

Future planners should run in priority order:

1. bootstrap missing Slots
2. safety repair
3. replica rebalance
4. leader transfer / leader skew correction

The planner must fail closed when required runtime observation is missing. For
example, a future leader-transfer planner must not infer real leadership from
`PreferredLeader`.

### 3. Durable Task

A task is a durable intent, not proof that work has happened.

`ReconcileTask` remains the shared task record. It should keep these semantics:

- `Kind` chooses the workflow, such as `bootstrap` or future
  `leader_transfer`.
- `Step` chooses the current phase inside that workflow.
- `TargetNode` is the main target of the workflow, not always the executor.
- `SourceNode` is optional and used by move-like tasks.
- `TargetPeers` records the target peer set for assignment-changing tasks.
- `ConfigEpoch` fences tasks tied to a specific assignment epoch.
- `Attempt`, `Status`, and `LastError` describe the latest execution state.

The state model should continue enforcing only one active task per physical
Slot.

### 4. Executor

Each `wukongimv2` node watches the local control snapshot and independently
decides whether it owns a task attempt.

Executor ownership is task-specific:

- Bootstrap task: nodes in `TargetPeers` reconcile local Slot runtime; the task
  completes when the assigned Slot is open and healthy enough.
- Future leader transfer task: the current Slot Raft leader executes the
  transfer, while `TargetNode` is the desired new leader.
- Future repair task: the node that can safely perform the repair executes the
  recovery step.

The executor must be idempotent. If it sees the same task again after restart,
it should either continue, prove it is already done, or report a bounded
failure. It must not rely on in-memory ownership for correctness.

### 5. Result Writer

Task results are written through Controller Raft commands:

- `complete_task` removes the active task after convergence is proven.
- `fail_task` records `LastError`, increments `Attempt`, and keeps the task
  visible.

Obsolete results should be no-ops. If another node already completed the task,
a late `complete_task` or `fail_task` must not recreate or corrupt state.

## Lifecycle

The generic lifecycle is:

```text
1. Controller leader observes state.
2. Planner creates or updates one durable task.
3. Controller Raft commits the task.
4. Nodes receive the task through control snapshots.
5. Eligible executor performs a bounded attempt.
6. Executor waits for or checks convergence.
7. Executor proposes complete_task or fail_task.
8. Controller applies the result.
9. Next planner tick uses the updated state.
```

Task status means:

- `pending`: task is waiting for an eligible executor or convergence check.
- `running`: optional visible state for a claimed attempt. The first
  implementation may skip explicit running claims if attempts are idempotent.
- `failed`: latest attempt failed, but the task remains visible for retry or
  operator inspection.

For the first implementation, `pending` plus `failed` is enough if the executor
is idempotent and bounded. `running` can be added later when the UI needs live
attempt ownership.

## Example 1: Bootstrap Slot

Initial state:

```text
Cluster config wants Slot 1.
Slot 1 has no assignment.
No active task exists for Slot 1.
```

Flow:

```text
Bootstrap planner
  -> choose DesiredPeers [1, 2, 3]
  -> choose PreferredLeader 2
  -> propose upsert_slot_assignment_and_task
```

Durable state becomes:

```text
SlotAssignment{
  SlotID: 1,
  DesiredPeers: [1, 2, 3],
  ConfigEpoch: 1,
  PreferredLeader: 2,
}

ReconcileTask{
  TaskID: "slot-1-bootstrap-1",
  SlotID: 1,
  Kind: "bootstrap",
  Step: "create_slot",
  TargetNode: 2,
  TargetPeers: [1, 2, 3],
  ConfigEpoch: 1,
  Status: "pending",
}
```

Each peer reconciles local Slot runtime from the assignment. The executor then
checks runtime observation. Once the Slot has the desired peers, a leader, and
quorum, an eligible node proposes `complete_task`.

If the Slot cannot be opened or quorum never forms before the bounded attempt
expires, the executor proposes `fail_task` with a concise error. The failed task
stays visible so the manager UI can show the problem.

## Example 2: Future Manual Slot Leader Transfer

This phase does not implement leader transfer, but the task mechanism must
support it later.

Operator intent:

```text
Move Slot 9 leader from node 1 to node 2.
```

Future flow:

```text
manager API creates a durable task request
  -> Controller validates Slot 9 and target node 2
  -> Controller writes leader_transfer task
  -> current Slot Raft leader executes TransferLeadership
  -> executor waits for observed leader == 2
  -> complete_task removes the task
```

Important distinction:

- `TargetNode=2` means desired new leader.
- Executor should be the current Slot Raft leader, not necessarily node 2.
- `PreferredLeader=2` is a durable preference. It is not proof that node 2 is
  currently the Raft leader.

If the current leader has already become node 2, the executor can complete the
task without calling `TransferLeadership`. If the observed leader changed from
node 1 to node 3 before execution, the executor should fail or defer because
the safety premise is stale.

## Example 3: Future Node Drain Or Failure

Draining node:

```text
node 1 is alive but should stop leading Slots
```

The planner can create a bounded number of leader-transfer tasks for Slots
whose current leader is node 1. Concurrency limits belong to the planner or
task scheduler, not to manager HTTP handlers.

Dead node:

```text
node 1 is unreachable
```

The system must not try to send a transfer command to node 1. For Slots that
still have quorum, Slot Raft should elect a new leader. The controller can then
observe the new leader and optionally plan preferred-leader or balance work. For
Slots without quorum, the correct task kind is repair or recovery, not leader
transfer.

## Manager UI And API

The first phase should expose task visibility before exposing new write
operations.

Recommended manager read model:

- task id
- slot id
- kind
- step
- status
- source node
- target node
- target peers
- config epoch
- attempt
- last error

Slot list/detail can keep showing `leader_transfer_pending` later by deriving
it from active tasks. For the first task-mechanism phase, showing bootstrap
tasks is enough to prove the model.

Manual task creation APIs should come after the lifecycle is proven. Slot
leader transfer should be a task creation request, not a direct synchronous
runtime action.

## Failure Handling

Failures should be explicit and bounded:

- Invalid task input is rejected before it enters Controller Raft.
- Stale task results become no-ops.
- Execution errors write `fail_task` with bounded `LastError`.
- Runtime observation gaps defer work or fail closed.
- Retrying should be controlled by task kind, attempt count, and optional
  cooldown.

The first implementation can keep retry policy simple:

- failed tasks stay visible
- automatic retry is off or very conservative
- manual retry can be added later as a Controller command or manager action

## Testing Strategy

Unit tests should cover:

- state validation for supported task kinds
- planner output for bootstrap tasks
- FSM apply behavior for complete and failed tasks
- idempotent obsolete result handling
- executor ownership selection for bootstrap tasks
- no direct manager Slot leader transfer route in this phase

Integration or e2e tests should be added only when the task executor reaches a
real process boundary. The first black-box target should prove a bootstrap task
is created, observed, and completed in a `wukongimv2` single-node cluster.

## Open Extension Points

The design intentionally leaves these as follow-up work:

- `leader_transfer` task kind
- `repair` task kind
- `rebalance` task kind
- task retry command
- task cancellation command
- task history or terminal retention
- batch task planning for node drain

These features should reuse the same durable task lifecycle rather than adding
manager-only action paths.

## Acceptance Criteria

- `controllerv2` has a documented generic task lifecycle.
- Bootstrap tasks can complete or fail through Controller Raft result commands.
- Manager read models can display active task state.
- The design does not expose Slot leader transfer as a direct manager action.
- Future Slot leader transfer can be implemented as a new task kind without
  changing the lifecycle.
