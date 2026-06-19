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

- `state.ReconcileTask` with `TaskID`, `SlotID`, `Kind`, `Step`,
  `SourceNode`, `TargetNode`, `TargetPeers`, `ConfigEpoch`, `Attempt`,
  `Status`, and `LastError`.
- Controller Raft commands for `upsert_slot_assignment_and_task`,
  `complete_task`, and `fail_task`.
- FSM validation that allows only one active task per Slot.
- A bootstrap planner that creates bootstrap assignments and tasks.
- `pkg/clusterv2/control` exposes active tasks through control snapshots, but
  the current DTO only carries a subset of task fields.
- `pkg/clusterv2/control.ReportSlots` is currently a best-effort no-op, so
  runtime Slot observations cannot be assumed to exist in Controller state.
- The current task record does not yet have participant progress for tasks that
  require multiple nodes to finish local work before global completion.

The missing piece is the execution and observation loop:

```text
Planner -> durable task -> node executor -> task result command -> observed convergence
```

The first implementation must therefore add the lifecycle surface, not just an
executor goroutine.

## Architecture

The task mechanism is split into eight responsibilities.

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

For the first bootstrap lifecycle, `pkg/clusterv2` should own a narrow runtime
observation provider used by executors and convergence checks. The provider can
read local Slot status and, when needed, routed peer status, but it must return
explicit "missing" or error states instead of fabricating runtime truth.

Bootstrap completion must be based on concrete Slot runtime observation:

- observed current voters match the durable assignment peer set
- a non-zero Slot Raft leader is known
- the observed Slot has quorum
- the observation is fresh enough for the local read path, or was read
  synchronously from the runtime
- status read errors or missing observations do not complete the task

Manager display fallbacks are not valid completion evidence. In particular,
the desired-peer fallback used by the migrated Slot list must never be reused
to prove bootstrap convergence.

### 2. Planner

Only ControllerV2 planning and validation logic emits durable task commands.
Manager APIs may later submit an operator intent to the Controller leader, but
they must not construct `state.ReconcileTask` records directly.

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
- `CompletionPolicy` chooses how local participant progress becomes global
  completion.
- `ParticipantProgress` records per-node progress for barrier-style tasks.
- `ConfigEpoch` fences tasks tied to a specific assignment epoch.
- `Attempt`, `Status`, and `LastError` describe the latest execution state.

The state model should continue enforcing only one active task per physical
Slot.

`TaskID` must be globally unique for each task creation and should never be
reused after completion. Result commands still need explicit guard fields
because uniqueness alone does not protect retries from stale, delayed results.
The deterministic bootstrap ID shape is acceptable only while physical Slot
bootstrap is a one-shot task. Any task kind that can be recreated after
completion must include a creation sequence, revision, or equivalent generation
in its `TaskID`.

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

- `complete_task` removes the active task from `cluster-state.json` after
  convergence is proven.
- `report_task_progress` records one participant's local progress for
  barrier-style tasks.
- `fail_task` records `LastError`, increments `Attempt`, and keeps the task
  visible.

The result writer must be a public task-result facade, not ad hoc construction
of `pkg/controllerv2/command` payloads from manager or `internalv2` packages.
The boundary should look like:

```text
task executor
  -> pkg/clusterv2/control task-result facade
  -> pkg/controllerv2 root runtime facade
  -> Controller Raft proposal path
  -> FSM task-result apply
```

This keeps production integration aligned with the existing package boundary:
`pkg/clusterv2` depends on the root `pkg/controllerv2` facade, while
`pkg/controllerv2` remains independent of `pkg/clusterv2`.

The result writer must route results to the current Controller leader. A data
node or Controller mirror should be able to submit a task result through the
same facade; if the local node is not the Controller leader, the integration
layer routes or forwards the proposal instead of silently dropping it.

The facade contract must define whether a successful call means "applied by
Controller Raft" or only "accepted for forwarding." The first implementation
should prefer waiting for apply when possible. If a routed path can only confirm
forwarding, executors must treat the outcome as uncertain and rely on
idempotent retry plus fenced task results.

Before wiring executors, task result and progress commands must be extended
with fencing fields:

- `TaskID`
- `SlotID`
- `TaskKind`
- `ConfigEpoch`
- `Attempt`
- `FinishedAt`
- optional bounded failure text

Participant progress reports also need:

- participant `NodeID`
- participant attempt
- participant status, such as `pending`, `done`, or `failed`

Apply semantics should be:

- missing task: obsolete no-op
- missing required identifiers: reject as invalid input
- guard mismatch on slot, task kind, config epoch, or attempt: obsolete no-op
- unexpected participant node: reject as invalid input
- stale participant attempt: obsolete no-op
- matching `report_task_progress`: update that participant only
- matching `complete_task`: remove the active task
- matching `fail_task`: mark failed, increment `Attempt`, and store bounded
  `LastError`
- matching `fail_task` on a barrier task: also reset participant progress for
  the next global task attempt

The guard mismatch rule is important for retries. A delayed success result from
attempt `0` must not complete a task that already failed and moved to attempt
`1`.

### 6. Participant Progress

Some task kinds require multiple nodes to finish local work before the task can
be globally completed. These should be modeled as one durable task with a
participant progress table, not as several unrelated active tasks.

Recommended fields:

- `CompletionPolicy`: for example `single_observer`, `all_target_peers`, or
  future `quorum_participants`.
- `ParticipantProgress`: entries keyed by node ID and scoped to the current
  global task attempt.
- participant `Status`: `pending`, `done`, or `failed`.
- participant `Attempt`: local retry fence for that node's work.
- participant `LastError`: bounded local failure reason.

The `all_target_peers` policy works like a barrier:

```text
task.TargetPeers = [1, 2, 3]
task.CompletionPolicy = "all_target_peers"
task.ParticipantProgress = {
  1: pending,
  2: pending,
  3: pending,
}
```

Each target peer executes its local step and submits `report_task_progress`.
That report only updates the sender's participant entry. It never completes the
whole task by itself.

The global task completes only when both conditions are true:

- every required participant is `done` for the current task attempt
- the task-specific convergence predicate is true from runtime observation

For bootstrap, the convergence predicate remains concrete Slot observation:
desired voters are present, a non-zero leader is known, and quorum is healthy.
This second check prevents a task from completing just because every node
reported success before Raft actually converged.

Participant failure is reported with `report_task_progress(status=failed)`, not
with global `fail_task`. It keeps the parent task active, marks the task
visible as failed for operators, and advances only that participant's local
attempt fence. The failed participant can be retried within the same global task
attempt without forcing already done participants to redo work. A delayed
success from an old participant attempt must be an obsolete no-op.

Global `fail_task` is reserved for unrecoverable task-level failures, such as
invalid durable task shape or a convergence predicate that cannot be satisfied
without a new plan. If a barrier task is globally failed and then retried, the
new global task attempt resets participant progress to `pending`; local steps
must be idempotent so already-converged nodes can report `done` again quickly.

This model keeps the active-task invariant simple: there is still one active
task per physical Slot, but that task can have multiple participant rows.
Parent/child task trees remain a future extension for workflows that need
independent subtask scheduling across many Slots or phases.

### 7. Task Read Model

Manager and executors should consume task state through
`pkg/clusterv2/control.Snapshot`, not by importing ControllerV2 subpackages.

The first implementation must extend `pkg/clusterv2/control.ReconcileTask` and
`SnapshotFromControllerV2` so the control snapshot carries the full active-task
read model needed by manager and executors:

- `Step`
- `SourceNode`
- `TargetNode`
- `TargetPeers`
- `ConfigEpoch`
- `Attempt`
- `Status`
- `LastError`
- `CompletionPolicy`
- `ParticipantProgress`

`internalv2/usecase/management` should continue reading through
`ControlSnapshotReader.LocalControlSnapshot`. It should not read
`pkg/controllerv2/state.ClusterState` directly just to recover missing task
fields.

### 8. Task Retention

`cluster-state.json` should store active convergence tasks only. It is the
current control-plane state, not an audit log.

Retention semantics are:

- `pending`, `running`, and `failed` tasks stay in `Tasks` because they still
  represent work needed to converge the cluster.
- `complete_task` removes the task from `Tasks` after convergence is proven.
- `fail_task` keeps the task in `Tasks`, marks it `failed`, records bounded
  `LastError`, and increments `Attempt`.

Completed-task history should be a separate bounded history or event stream if
manager UI later needs "recent operations." That history must not change the
active-task invariant and should not be required for planner correctness.

## Lifecycle

The generic lifecycle is:

```text
1. Controller leader observes state.
2. Planner creates or updates one durable task.
3. Controller Raft commits the task.
4. Nodes receive the task through control snapshots.
5. Eligible executor performs a bounded attempt.
6. For participant tasks, each executor reports local progress.
7. Executor or Controller-side lifecycle checks task-specific convergence.
8. Executor proposes complete_task or fail_task when the whole task is done or
   globally failed.
9. Controller applies the result.
10. Next planner tick uses the updated state.
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

Bootstrap failures must not become a permanent planner dead-end. In the first
phase, a failed bootstrap task remains active and retryable. The bootstrap
executor should retry failed tasks conservatively with a bounded per-attempt
timeout and a coarse retry interval. The failed state remains visible between
attempts so operators can inspect `LastError`, and cluster readiness should not
claim success until the task completes.

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
  CompletionPolicy: "all_target_peers",
  ParticipantProgress: {
    1: pending,
    2: pending,
    3: pending,
  },
  ConfigEpoch: 1,
  Status: "pending",
}
```

This deterministic `TaskID` is valid only because initial physical Slot
bootstrap is one-shot. Future task kinds that can be recreated after completion
must include a generation in the task identity.

Each peer reconciles local Slot runtime from the assignment. Node 1, 2, and 3
each report `report_task_progress(..., status=done)` after their local Slot
runtime step succeeds. These reports update only their own participant rows.

After every target peer is `done`, an eligible executor checks runtime
observation. Once the Slot has the desired peers, a leader, and quorum, it
proposes `complete_task`.

If only one peer fails its local step, that peer reports failed participant
progress. The parent task stays active and visible. Already-done peers do not
need to redo local work; the failed peer can retry with its next participant
attempt.

If all peers report `done` but quorum never forms before the bounded convergence
attempt expires, an eligible executor can propose global `fail_task` with a
concise error. The failed task stays visible so the manager UI can show the
problem, and a later retry can re-check the convergence predicate using the next
fenced task attempt.

## Example 2: Future Manual Slot Leader Transfer

This phase does not implement leader transfer, but the task mechanism must
support it later.

Operator intent:

```text
Move Slot 9 leader from node 1 to node 2.
```

Future flow:

```text
manager API submits a leader-transfer intent
  -> Controller leader validates Slot 9 and target node 2
  -> Controller planning/validation writes leader_transfer task
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
- completion policy
- participant progress
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
- Stale task results become no-ops through the task-result guard fields.
- Participant execution errors write failed participant progress with bounded
  `LastError`.
- Unrecoverable task-level errors write `fail_task` with bounded `LastError`.
- Runtime observation gaps defer work or fail closed.
- Retrying is controlled by task kind, attempt count, and executor rate limits.

The first implementation can keep retry policy simple:

- failed bootstrap tasks stay visible
- failed bootstrap tasks remain retryable by the executor
- retry is conservative enough to avoid hot Raft write loops
- operator retry, pause, or cancellation controls can be added later as
  Controller commands or manager actions

## Testing Strategy

Unit tests should cover:

- state validation for supported task kinds
- planner output for bootstrap tasks
- FSM apply behavior for complete and failed tasks
- FSM apply behavior for participant progress updates
- task-result fencing for stale attempts, stale epochs, and missing tasks
- participant-progress fencing for stale participant attempts and unexpected
  participant nodes
- result writer routing from Controller leader, Controller follower, and mirror
  or data nodes
- `pkg/clusterv2/control` snapshot mapping for the full task read model
- executor ownership selection for bootstrap tasks
- barrier completion for `all_target_peers`
- bootstrap observation missing or stale fails closed
- failed bootstrap task retry semantics
- restart idempotence when an executor sees the same task after process restart
- no direct manager Slot leader transfer route in this phase

Integration or e2e tests should be added only when the task executor reaches a
real process boundary. The first black-box target should prove a bootstrap task
is created, observed, and completed in a `wukongimv2` single-node cluster.

## Open Extension Points

The design intentionally leaves these as follow-up work:

- `leader_transfer` task kind
- `repair` task kind
- `rebalance` task kind
- operator retry, pause, or cancellation commands
- task history or terminal retention
- batch task planning for node drain
- parent/child task trees for multi-Slot or multi-phase workflows

These features should reuse the same durable task lifecycle rather than adding
manager-only action paths.

## Acceptance Criteria

- `controllerv2` has a documented generic task lifecycle.
- Bootstrap tasks can complete or fail through Controller Raft result commands.
- Task result commands are proposed through a public facade and fenced by task
  identity, Slot, kind, config epoch, and attempt.
- Stale task results cannot complete or fail a newer attempt.
- Barrier-style tasks can track per-node participant progress and complete only
  after every required participant is done plus the runtime convergence
  predicate is true.
- Stale participant progress cannot overwrite a newer participant attempt.
- Failed bootstrap tasks are visible and retryable without blocking the planner
  forever.
- Bootstrap completion uses real runtime observation, not manager display
  fallbacks.
- `pkg/clusterv2/control` exposes the full active-task read model needed by
  manager and executors.
- Manager read models can display active task state.
- The design does not expose Slot leader transfer as a direct manager action.
- Future Slot leader transfer can be implemented as a new task kind without
  changing the lifecycle.
