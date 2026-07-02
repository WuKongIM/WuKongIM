# internalv2 Dynamic Node Stage 12 Diagnostics Design

Date: 2026-07-01
Status: Proposed for review
Scope: internalv2 manager/usecase, ControllerV2 task evidence, Slot replica move observability, wkcli node operations, e2ev2 readiness gate, dynamic-node runbook

## 1. Purpose

Stage 11 proved the dynamic-node lifecycle through a real `wkcli node`
operations rehearsal, but the verification work also exposed a production
diagnosis gap: when Slot replica movement or control revision convergence is
slow, operators can see that `safe_to_remove=false`, but still need to combine
node inventory, scale-in status, active Controller tasks, retained task audit
events, Slot runtime state, and metrics by hand.

Stage 12 turns that evidence into a bounded diagnostic surface. It must not
change lifecycle semantics, increase placement risk, or hide slow convergence
behind larger timeouts. The goal is to make the root cause of
`blocked_by_slots`, `blocked_by_tasks`, `blocked_by_control_revision`,
`blocked_by_slot_runtime`, and related readiness blockers directly visible from
manager HTTP, `wkcli`, metrics, e2ev2 artifacts, and the runbook.

## 2. Current State

The completed dynamic-node stages already provide:

- ControllerV2-authoritative dynamic node join, activation, leaving, and
  removed tombstones.
- Health-gated placement and fail-closed scale-in status.
- Bounded Slot onboarding and scale-in through `slot_replica_move` tasks.
- Manager routes for:
  - `GET /manager/nodes`
  - `GET /manager/nodes/:node_id/scale-in/status`
  - `GET /manager/controller/tasks`
  - `GET /manager/controller/tasks/:task_id`
  - `GET /manager/controller/task-audits`
  - `GET /manager/controller/task-audits/:task_id/events`
  - `GET /manager/slots`
- `wkcli node` commands for node list, activate, onboarding, scale-in status,
  drain, advance, and remove.
- Low-cardinality node lifecycle metrics in `pkg/metrics/node_lifecycle.go`.
- Controller metrics for active/failed task counts, but the current task-kind
  whitelist does not yet include `slot_replica_move`.
- Task audit history with `StartedAt`, `CompletedAt`, summaries, last reasons,
  and bounded per-task event timelines.
- e2ev2 readiness and operations gates that write command logs and summaries.

The remaining gaps are operational:

- There is no single diagnostic read model for one dynamic node.
- `wkcli node scale-in status` shows blocker flags but does not include related
  task IDs, task steps, participant progress, audit summaries, or related Slot
  runtime rows.
- The active task DTOs used by manager and onboarding/scale-in task summaries
  do not yet expose Slot replica move proof fields such as `phase_index`,
  `observed_config_index`, `observed_voters`, and `observed_learners`.
- Controller active task metrics do not classify `slot_replica_move`, so
  dynamic-node Slot move pressure can be invisible in task gauges.
- Task age can only be derived from task audit history; active tasks do not
  carry a reliable created timestamp.
- e2ev2 failures do not automatically dump a dynamic-node diagnostic bundle
  before teardown.
- The runbook lists blockers but does not give a single command-driven
  root-cause decision tree.

## 3. Goals

1. Add a read-only dynamic-node diagnostic route:

   ```text
   GET /manager/nodes/:node_id/diagnostics
   ```

2. Add `wkcli node diagnose NODE_ID` that calls the manager diagnostic route
   and preserves every decisive field in JSON and human output.
3. Reuse existing manager read models and task audit history; avoid duplicating
   task semantics in CLI code.
4. Add low-cardinality metrics for `slot_replica_move` task visibility and task
   audit-derived age where audit data exists.
5. Add e2ev2 diagnostic dump helpers and wire them into dynamic-node operations
   failures.
6. Extend the readiness gate `ops` profile to retain diagnostic JSON, metrics
   snapshots, and command evidence.
7. Update the dynamic-node operations runbook with a concrete blocker-to-action
   decision tree.

## 4. Non-Goals

- Do not add automatic recovery, task cancellation, or task retry controls.
- Do not modify Slot replica move, ControllerV2 task, or node lifecycle safety
  semantics.
- Do not add Controller Raft voter membership changes.
- Do not add unbounded scans over channels, users, sessions, Slots, tasks, or
  audit events.
- Do not add high-cardinality metric labels such as task ID, channel ID, UID,
  session ID, address, or error text.
- Do not make `wkcli` import `internalv2`, `pkg/controllerv2`,
  `pkg/clusterv2`, or storage internals.
- Do not introduce a web UI in this stage. The manager route should be shaped
  so a later UI can consume it without changing the backend contract.

## 5. Diagnostic Contract

The manager diagnostic route returns a bounded snapshot for exactly one node:

```text
node identity and lifecycle
  -> current scale-in safety status when available
  -> onboarding status when active onboarding tasks target the node
  -> active Controller tasks related to the node
  -> retained task audit summaries related to the node
  -> related Slot rows where the node is desired peer, preferred leader,
     live leader, source node, or target node in active tasks
  -> bounded diagnostic summary and warnings
```

The route must include:

- `generated_at`
- `state_revision`
- `node_id`
- `node`
- `scale_in`
- `onboarding`
- `active_tasks`
- `task_audits`
- `slots`
- `summary`
- `warnings`
- `sources`

`summary` contains stable machine-readable counts and root-cause hints:

```text
safe_to_remove
blocked_reasons
active_task_count
failed_task_count
slot_replica_count
slot_leader_count
control_revision_gap
slot_replica_move_state
oldest_task_age_seconds
audit_available
slot_runtime_unknown
runtime_unknown
recommended_next_action
```

`slot_replica_move_state` must be one of:

```text
waiting_learner_catchup
waiting_leader_transfer
phase_observation_missing
task_failed
no_active_move
unknown
```

The route should fail with:

- `400 bad_request` for invalid node ID or limit values.
- `404 not_found` when control state has no matching node or removed tombstone.
- `503 service_unavailable` when the local control snapshot cannot be read.

Task audit unavailability must not fail the entire route. It should set
`sources.task_audit.available=false` and add a warning. That keeps active task
diagnosis available when the retained history store is not configured.

## 6. Bounds And Performance

Stage 12 diagnostic reads are explicit operator reads, not hot-path reads.
Still, bounds must be hard-coded and tested:

- Active tasks: maximum 50 rows, filtered by node.
- Task audits: maximum 20 retained histories, filtered by node.
- Task audit events: not included in the diagnostic route by default. The route
  may include the retained task audit summary and last reason only. Event
  timelines remain behind `GET /manager/controller/task-audits/:task_id/events`.
- Slots: maximum 256 physical Slots by default cluster sizing. If future
  clusters exceed that, the route must cap returned rows and set
  `slots_truncated=true`.
- Channel inventory is not rescanned by diagnostics. Scale-in status already
  owns bounded channel inventory.
- Metrics labels must stay bounded to kind, status, step, result, reason, and
  source.

## 7. Metrics Contract

Stage 12 extends existing metrics without replacing Stage 9C:

```text
wukongim_controller_tasks_active{type="slot_replica_move"}
wukongim_controller_tasks_failed{type="slot_replica_move"}
wukongim_controller_task_oldest_age_seconds{kind,status,step,source}
wukongim_slot_replica_move_phase_observed_total{step,result}
wukongim_slot_replica_move_phase_duration_seconds{step,result}
```

Rules:

- `wukongim_controller_task_oldest_age_seconds` uses task audit `StartedAt`
  only. When audit is unavailable or no matching retained history exists, the
  metric must not infer age from control-state `UpdatedAt`.
- `source` is bounded to `audit` or `unknown`.
- `kind` must include `slot_replica_move`.
- `step` must be one of the bounded ControllerV2 task steps, normalized to
  `other` for unknown future values.
- Phase duration observes executor phase work only around local
  `SlotReplicaMoveExecutor` operations. It must not include task queue age
  unless a later task-audit-based metric explicitly says so.

## 8. E2EV2 Evidence Contract

Dynamic-node e2ev2 tests should dump diagnostics before failing or during
final evidence collection:

```text
data/e2ev2/<test>/dynamic-node-diagnostics/node-<id>.json
data/e2ev2/<test>/dynamic-node-diagnostics/node-<id>-metrics.prom
data/e2ev2/<test>/dynamic-node-diagnostics/node-<id>-wkcli.txt
```

The helper should collect:

- `GET /manager/nodes/:node_id/diagnostics`
- `wkcli node diagnose NODE_ID --json`
- `/metrics` from all running nodes when metrics are enabled
- the existing cluster diagnostics dump

The readiness gate `ops` profile should copy these artifacts into its evidence
directory and link them from `summary.md` when the Stage 11 operations scenario
fails or succeeds.

## 9. Runbook Contract

The runbook must map blockers to first diagnostic commands:

- `blocked_by_control_revision`: inspect node diagnostics and compare
  required vs observed revision.
- `blocked_by_tasks`: inspect active task IDs, step, status, participant
  progress, and latest audit reason.
- `blocked_by_tasks` with `slot_replica_move`: inspect `phase_index`,
  `observed_config_index`, `observed_voters`, `observed_learners`, and the
  latest audit event before deciding whether the task is legitimately waiting
  or has failed.
- `blocked_by_slots`: inspect related Slot rows and run bounded scale-in
  advance only when no conflicting task is active.
- `blocked_by_slot_runtime`: inspect live Slot runtime leader/voter/learner
  evidence from the diagnostic slot rows.
- `unknown_runtime`: inspect node process, `/readyz`, manager node list, and
  metrics scrape status.
- `unknown_channel_inventory`: do not remove; collect channel inventory status
  from scale-in status and retry only after the inventory read succeeds.

## 10. Stage Breakdown

Stage 12 should be implemented as three separate sub-stages:

- Stage 12A: active task proof fields, manager diagnostic route, and
  `wkcli node diagnose`.
- Stage 12B: low-cardinality metrics and Slot replica move phase observation.
- Stage 12C: e2ev2 diagnostic dump, readiness gate evidence, and runbook
  update.

Each sub-stage must pass its own focused tests before the next begins. The final
Stage 12 gate is the `ops` readiness gate plus targeted package tests.
