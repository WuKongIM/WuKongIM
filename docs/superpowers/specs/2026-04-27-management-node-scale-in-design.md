# Management-Driven Node Scale-in Design

> Status: Draft for review
> Date: 2026-04-27
> Scope: Manager API, management usecase, cluster read/operator integration, gateway drain/readiness, optional node runtime summary
> Non-scope: Kubernetes Operator, HPA/KEDA, automatic StatefulSet scaling, Controller voter shrink, physical Slot removal

## 1. Background

WuKongIM already has controller-managed data membership, Draining node status, Slot repair/rebalance tasks, Slot leader transfer, dynamic node join, node onboarding, hash-slot migration, manager APIs, `/readyz`, and `/metrics`.

Kubernetes automatic scale-in still needs a safe bridge between Pod lifecycle and WuKongIM cluster semantics. If Kubernetes removes a Pod before WuKongIM migrates Slot replicas, transfers leaders, and drains gateway connections, the cluster can lose quorum, trigger repair storms, or drop long-lived IM connections unexpectedly.

The first stage deliberately supports only backend-management-driven scale-in preparation:

1. An operator chooses one node in the manager backend.
2. The backend validates that the node can be drained safely.
3. WuKongIM marks the node as Draining.
4. Existing controller repair/rebalance logic migrates Slot replicas away from the node.
5. The backend transfers remaining Slot leaders away from the node.
6. Gateway readiness/drain logic stops new traffic and waits for active connections to clear.
7. The backend reports `ready_to_remove`.
8. The operator manually scales Kubernetes down.

This stage does not change Kubernetes objects automatically.

## 2. Definitions

- **Node scale-in**: Safely drain one WuKongIM node until its Pod can be removed.
- **Node decommission**: Same semantic as node scale-in; this document uses `scale-in` for API names and `decommission` for durable future job concepts.
- **Target node**: The node selected for scale-in.
- **Tail node**: The node Kubernetes will remove when scaling a StatefulSet down. Production-safe V1 requires an explicit NodeID-to-StatefulSet-ordinal source or an operator-supplied tail-node confirmation; max NodeID is only a best-effort display hint.
- **Physical Slot removal**: Reducing the number of physical Slots through `RemoveSlot`. This is not node scale-in and is out of scope.
- **Controller voter removal**: Changing the Controller Raft voter set. This is out of scope for V1.

## 3. Goals

- Provide manager APIs for scale-in preflight, start, status, advance, and cancel.
- Support one target node at a time.
- Support only non-controller data/gateway nodes.
- Reuse existing `MarkNodeDraining`, `ResumeNode`, `TransferSlotLeader`, Strict cluster reads, tasks, and migration status.
- Report a deterministic `safe_to_remove` boolean only when all safety inputs are strict/leader-backed or target-node runtime summaries are available; unknown inputs fail closed.
- Keep `GET status` side-effect free.
- Make Draining nodes fail readiness so Kubernetes Services stop routing new traffic.
- Provide a path to count remote gateway connections before declaring `ready_to_remove`.
- Keep API/DTO shape compatible with future durable jobs.

## 4. Non-goals

- Do not scale StatefulSet replicas from WuKongIM.
- Do not implement a Kubernetes Operator.
- Do not support HPA/KEDA-driven scale-in.
- Do not shrink Controller voters.
- Do not remove physical Slots.
- Do not clean or delete PVCs.
- Do not support arbitrary StatefulSet ordinal deletion.
- Do not support concurrent scale-in jobs.
- Do not persist a durable scale-in job in V1.

## 5. Recommended Delivery Stages

### 5.1 V1a: Core Scale-in Report and Draining

V1a adds manager scale-in APIs and computes status from live cluster state. It does not require durable Controller metadata.

Required capabilities:

- Preflight report from existing cluster Strict reads.
- `start` calls `MarkNodeDraining` after preflight passes.
- `cancel` calls `ResumeNode`.
- `advance` transfers remaining Slot leaders away from the target node.
- `status` reports Slot replica, leader, task, and hash-slot migration progress.
- If remote connection information is not implemented yet, `connection_safety_verified=false` and `safe_to_remove=false` in production-safe mode.

V1a can expose a partial report quickly, but it must not silently claim full safety when target-node connection counts are unknown.

### 5.2 V1b: Gateway Drain and Remote Connection Summary

V1b completes production-safe manager-driven scale-in for IM long connections.

Required capabilities:

- Draining-aware readiness.
- Gateway rejects new sessions or stops accepting new sessions while the node is Draining.
- Manager can query active, closing, and unauthenticated gateway session counts for the target node from any manager endpoint.
- `ready_to_remove` requires all target-node connection/session counters to be zero. `force_close_connections` is only an action that tries to close sessions; it never overrides non-zero or unknown counters.

### 5.3 V2: Durable NodeDecommissionJob

V2 persists a durable job in Controller metadata and lets a controller-side coordinator advance it. It should mirror the existing NodeOnboardingJob patterns but should not be introduced until V1 semantics are proven.

### 5.4 Current Codebase Constraints

The approved V1 design intentionally builds on existing behavior, with these known constraints:

- `NodeStatusDraining` already drives controller planner repair: desired peers on Draining nodes are treated as needing replacement, and Draining nodes are excluded from future data scheduling.
- Existing manager routes already expose raw `draining` and `resume` operations, but those endpoints do not provide preflight, progress, or `ready_to_remove` semantics.
- `RemoveSlot` must not be used for node scale-in because it reduces physical Slot capacity and triggers hash-slot data migration.
- Existing `OperatorMarkNodeDraining` is not guarded by role, expected status, or capacity checks at the controller command level; V1 manager preflight plus immediate post-start refresh reduces risk, and V2 should add atomic guarded commands.
- Runtime views are leader-local observations, not steady-state durable metadata. Strict reads should fail closed if the Controller leader is unavailable or warming up.
- Gateway/client drain is separate from Slot replica drain. Existing gateway startup/shutdown can stop the whole gateway, but there is no admission gate or graceful connection-drain API yet.

## 6. Manager API

Add routes under `/manager`:

```text
POST /manager/nodes/:node_id/scale-in/plan
POST /manager/nodes/:node_id/scale-in/start
GET  /manager/nodes/:node_id/scale-in/status
POST /manager/nodes/:node_id/scale-in/advance
POST /manager/nodes/:node_id/scale-in/cancel
```

Permissions:

- `plan` and `status`: `cluster.node:r`, `cluster.slot:r`
- `start`, `advance`, `cancel`: `cluster.node:w`, `cluster.slot:w`

`plan` is a side-effect-free POST rather than GET so it can accept future request-body context such as explicit StatefulSet ordinal mapping, operator tail-node confirmation, dry-run mode, or policy flags without changing the route shape.

### 6.1 Plan

`plan` performs preflight and returns a report. It has no side effects.

### 6.2 Start

`start` performs preflight. If blocking reasons exist, return `409 scale_in_blocked` with the full report. If preflight passes, call `MarkNodeDraining` and return the refreshed report.

V1 start may accept an optional body for deployment-safety context:

```json
{
  "confirm_statefulset_tail": false,
  "expected_tail_node_id": 3
}
```

If no explicit NodeID-to-ordinal mapping exists, production-safe start should require `confirm_statefulset_tail=true` and `expected_tail_node_id` equal to the target. Max NodeID alone is not backend-verifiable safety.

`start` is idempotent when the target node is already Draining.

### 6.3 Status

`status` computes the current report. It has no side effects.

### 6.4 Advance

`advance` performs bounded imperative work that should not be hidden behind a GET request. V1 supports Slot leader transfer. V1b may also support forced connection close.

Request:

```json
{
  "max_leader_transfers": 1,
  "force_close_connections": false
}
```

Defaults:

- `max_leader_transfers`: 1
- `force_close_connections`: false

The backend should cap `max_leader_transfers` to a conservative upper bound, such as 3. `force_close_connections` means "attempt controlled session close"; it must not make `safe_to_remove=true` while sessions are still non-zero or unknown.

### 6.5 Cancel

`cancel` calls `ResumeNode` and returns the refreshed report.

If the node is already `ready_to_remove`, cancel is allowed but the response should warn that the node no longer owns replicas and may need a later rebalance/onboarding to carry load again.

## 7. Report DTO

Use one response shape for all scale-in endpoints.

```json
{
  "node_id": 3,
  "node_name": "wk-data-2",
  "status": "migrating_replicas",
  "safe_to_remove": false,
  "can_start": false,
  "can_advance": true,
  "can_cancel": true,
  "connection_safety_verified": true,
  "blocked_reasons": [
    {
      "code": "assigned_replicas_exist",
      "message": "target node still owns slot replicas",
      "count": 4,
      "slot_id": 0,
      "node_id": 3
    }
  ],
  "checks": {
    "target_exists": true,
    "target_is_data_node": true,
    "target_is_active_or_draining": true,
    "target_is_not_controller_voter": true,
    "tail_node_mapping_verified": true,
    "remaining_data_nodes_enough": true,
    "controller_leader_available": true,
    "slot_replica_count_known": true,
    "no_other_draining_node": true,
    "no_active_hashslot_migrations": true,
    "no_running_onboarding": true,
    "no_active_reconcile_tasks_involving_target": true,
    "no_failed_reconcile_tasks": true,
    "runtime_views_complete_and_fresh": true,
    "all_slots_have_quorum": true,
    "target_not_unique_healthy_replica": true
  },
  "progress": {
    "assigned_slot_replicas": 4,
    "slot_leaders": 1,
    "active_tasks_involving_node": 2,
    "active_migrations_involving_node": 0,
    "active_connections": 128,
    "closing_connections": 0,
    "gateway_sessions": 128,
    "active_connections_unknown": false
  },
  "leaders": [
    {
      "slot_id": 7,
      "current_leader_id": 3,
      "transfer_candidates": [1, 2]
    }
  ],
  "next_action": "wait_reconcile_tasks"
}
```

DTO notes:

- `blocked_reasons` is a stable machine-readable list. Frontend should use `code` for display mapping.
- `checks` contains only preflight/safety checks, not progress counters.
- `progress` contains live counters.
- `connection_safety_verified=false` means active connection data was unavailable. In production-safe mode this must prevent `safe_to_remove=true`.

## 8. Status Values

```text
not_started
blocked
draining
migrating_replicas
transferring_leaders
waiting_connections
ready_to_remove
failed
```

V1 computes status from live data. Recommended precedence:

1. `blocked`: target not found or preflight base checks fail before start.
2. `not_started`: target is not Draining and has no failed condition.
3. `failed`: after Draining, failed task, quorum loss, or Controller read failure blocks progress.
4. `migrating_replicas`: target is still present in any `SlotAssignment.DesiredPeers`.
5. `transferring_leaders`: target is still leader for any Slot.
6. `waiting_connections`: target has active gateway connections, gateway sessions, closing connections, or connection count is unknown in production-safe mode.
7. `ready_to_remove`: no replicas, leaders, tasks, migrations, active/closing connections, or gateway sessions remain.

`safe_to_remove` is true only when `status == ready_to_remove`. `cancelled` is not a V1 backend status because V1 has no durable job history. The UI may show a local cancellation result after `cancel`; durable V2 can persist `cancelled` explicitly.

## 9. Preflight Checks

Preflight runs for `plan` and `start`. It should also run in `status` to keep report diagnostics complete.

### 9.1 Required Checks

| Code | Rule | Blocking |
|------|------|----------|
| `target_not_found` | Target node exists in `ListNodesStrict` | yes |
| `target_not_data_node` | Target role is data/gateway-capable, not controller-only | yes |
| `target_not_active_or_draining` | Join state is Active and status is Alive or Draining. Suspect/Dead targets require manual recovery before V1 scale-in | yes |
| `target_is_controller_voter` | Target is not in configured Controller peer IDs and not `NodeRoleControllerVoter` | yes |
| `tail_node_mapping_unverified` | Explicit ordinal mapping or operator confirmation proves Kubernetes scale-down will remove the target | yes |
| `remaining_data_nodes_insufficient` | Active + Alive non-target Data node count is at least `SlotReplicaN` | yes |
| `controller_leader_unavailable` | Strict reads from Controller leader succeed | yes |
| `slot_replica_count_unknown` | Configured `SlotReplicaN` is available to the management usecase | yes |
| `other_draining_node_exists` | No other Active Data node is already Draining | yes |
| `active_hashslot_migrations_exist` | No active hash-slot migrations are visible. V1 may use `GetMigrationStatus()`; a strict leader-backed migration read is preferred | yes |
| `running_onboarding_exists` | No running NodeOnboardingJob from existing onboarding APIs | yes |
| `active_reconcile_tasks_involving_target` | No active reconcile task references target, unless target is already Draining and the task is part of this drain | yes |
| `failed_reconcile_tasks_exist` | No `TaskStatusFailed` task | yes |
| `runtime_views_incomplete_or_stale` | Every assigned Slot has a fresh runtime view from a warmed Controller leader | yes |
| `slot_quorum_lost` | Every runtime view has `HasQuorum=true` | yes |
| `target_unique_healthy_replica` | No Slot would lose its only healthy voter if target exits | yes |

### 9.2 Tail Node Rule

StatefulSet scale-down removes the highest ordinal Pod. V1 should only support that Pod.

Backend validation cannot safely infer the StatefulSet tail from NodeID unless the deployment guarantees `NodeID == ordinal + offset` and the manager has that rule as configuration. Therefore:

- Production-safe start requires explicit NodeID-to-ordinal mapping or an operator-supplied `confirm_statefulset_tail` request.
- `max(activeDataNodeIDs)` may be shown as a best-effort hint only. It must not be described as backend-verified safety unless NodeID/ordinal mapping is configured.
- Future Operator work should replace this manual confirmation with direct StatefulSet ordinal knowledge.

### 9.3 Unique Healthy Replica Rule

A simple conservative V1 check:

- For each Slot runtime view containing target in `CurrentPeers`, if `HealthyVoters <= 1`, block.
- If runtime views are stale or missing, block instead of assuming safety.

A later implementation can expose per-peer health to make this more precise.

### 9.4 API Gaps To Close

Most V1 checks can be computed from existing Strict reads, but the following gaps should be tracked explicitly:

- `GetMigrationStatus()` is currently local cluster state, not a named Strict controller-leader read. V1 can use it for display only; production-safe `start` and `ready_to_remove` should use a strict active-migration read or strict HashSlotTable snapshot read, or fail closed.
- `ListObservedRuntimeViewsStrict` must be paired with a runtime-view completeness/freshness check. Missing views for assigned Slots, Controller leader warmup, or stale observation should block `start` and `ready_to_remove`.
- `SlotReplicaN` is required for capacity checks but is not currently part of the management usecase options; Batch 0 must inject it from app config or expose it through a cluster config read helper.
- `MarkNodeDraining` is not an atomic preflight-plus-transition operation. Between manager preflight and the operator command, cluster state can change. V1 accepts this and keeps `status` fail-closed; V2 should add guarded command semantics.
- Existing planner emits one repair decision per tick and does not create a full decommission plan. V1 status must therefore diagnose stalls rather than promise a fixed move list.
- Management currently does not expose `ForceReconcile`; if drain stalls on a retryable task, a later batch may add a guarded operator action rather than overloading scale-in.

## 10. Progress Calculation

Inputs:

```go
nodes := ListNodesStrict(ctx)
assignments := ListSlotAssignmentsStrict(ctx)
views := ListObservedRuntimeViewsStrict(ctx)
tasks := ListTasksStrict(ctx)
migrations := ListActiveMigrationsStrict(ctx) // preferred; local GetMigrationStatus is display-only if strict read is unavailable
```

Counters:

- `assigned_slot_replicas`: count assignments whose `DesiredPeers` contains target node.
- `slot_leaders`: count runtime views whose `LeaderID` equals target node.
- `active_tasks_involving_node`: count non-failed, non-terminal tasks whose `SourceNode` or `TargetNode` equals target node.
- `active_migrations_involving_node`: count active hash-slot migrations whose source or target physical Slot assignment contains target node.
- `active_connections`: target node active authenticated online connections.
- `closing_connections`: target node online connections already marked closing.
- `gateway_sessions`: target node gateway sessions, including unauthenticated sessions that may not be visible in `online.Registry`.
- `active_connections_unknown`: true when the target node runtime summary cannot be read.

Terminal tasks are not explicit in current metadata. In V1, a task exists until completion, so any non-failed task involving the node is active. Existing online connection APIs only expose local active authenticated connections; V1b must add runtime summary coverage for closing and unauthenticated gateway sessions before using connection counters for `ready_to_remove`.

## 11. Leader Transfer Advance

`advance` transfers a bounded number of Slot leaders away from the target node.

Algorithm:

1. Build report.
2. If status is not `transferring_leaders`, return report unchanged unless force options are requested.
3. For each leader item, choose one transfer candidate.
4. Candidate must be:
   - not the target node;
   - in `DesiredPeers` or `CurrentPeers`;
   - Active + Alive + Data;
   - preferably observed in current peers and under quorum.
5. Call `TransferSlotLeader(ctx, slotID, candidateID)`.
6. Stop after `max_leader_transfers` attempts.
7. Return a refreshed report.

If no candidate exists for a leader, return `409 invalid_scale_in_state` with a blocked reason such as `leader_transfer_candidate_missing`.

## 12. Gateway Drain and Readiness

### 12.1 Readiness

When the local node is Draining, `/readyz` must return 503.

Current readiness already checks gateway listener readiness, managed Slots readiness, and HashSlotTable readiness. Add a local node drain check, but avoid making every Kubernetes probe perform a live Controller strict read. Recommended approach:

- Maintain a cached local node status updated from committed controller node-status observations or a bounded background refresh.
- If the cache says local node is Draining, readiness returns 503.
- If the cache is unknown or stale beyond a configured short tolerance, readiness should fail closed only for the drain check while keeping liveness unchanged.

```text
ready = gatewayReady && clusterReady && hashSlotReady && localNodeDrainState == not_draining
```

Liveness should remain independent; a Draining node is intentionally not ready but should not be killed before replica and connection drain completes. The response should include:

```json
{
  "checks": {
    "node_not_draining": false
  }
}
```

### 12.2 Rejecting New Sessions

V1b should prevent new gateway sessions while the node is Draining. Current gateway open flow registers sessions before any drain/policy gate, so the check must happen at the earliest core admission point, before session state registration and observer open events. Minimal design:

- `internal/app` maintains or queries local drain status.
- Gateway session accept/open path checks a `DrainGate` interface.
- If draining, reject the session before registering it in gateway state or `online.Registry`.

Suggested interface:

```go
type DrainGate interface {
    AcceptingNewSessions() bool
}
```

The exact package placement should follow existing gateway option patterns.

### 12.3 Closing Existing Sessions

V1b can wait for natural disconnects. Forced close can be optional behind `advance.force_close_connections`.

If forced close is implemented, add a small online/session control interface that can:

- list active sessions for local node;
- mark them closing;
- close their underlying gateway session through the core gateway session lifecycle.

Force close should be rate-limited. Do not rely on `online.Conn.Session.Close()` alone for immediate safe disconnect, because that adapter-level close is not equivalent to the gateway core `sessionState.close` path that performs unregister, observer close, and session-close side effects.

## 13. Remote Connection Summary

Manager may be called on node A while target node is node B. Existing manager connection APIs are local. V1b needs remote target summary.

Minimal node RPC:

```text
service: node runtime summary
request: { node_id }
response: {
  node_id,
  active_online,
  closing_online,
  total_online,
  gateway_sessions,
  sessions_by_listener,
  accepting_new_sessions,
  draining
}
```

Rules:

- If `node_id` is local, read local `online.Registry`, gateway session registry, and gateway drain state.
- If remote, route through cluster node RPC.
- Count active online connections, closing online connections, and unauthenticated gateway sessions.
- If RPC fails, report `active_connections_unknown=true` and block `ready_to_remove` in production-safe mode.

Alternative later: include connection summary in Controller observation. That gives better manager reads but increases steady-state control-plane data. For V1b, direct node RPC is smaller and more explicit.

## 14. Usecase Design

Add manager usecase methods:

```go
func (a *App) PlanNodeScaleIn(ctx context.Context, nodeID uint64) (NodeScaleInReport, error)
func (a *App) StartNodeScaleIn(ctx context.Context, nodeID uint64) (NodeScaleInReport, error)
func (a *App) GetNodeScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInReport, error)
func (a *App) AdvanceNodeScaleIn(ctx context.Context, nodeID uint64, req AdvanceNodeScaleInRequest) (NodeScaleInReport, error)
func (a *App) CancelNodeScaleIn(ctx context.Context, nodeID uint64) (NodeScaleInReport, error)
```

Suggested types:

```go
type NodeScaleInStatus string

type NodeScaleInReport struct {
    NodeID                    uint64
    NodeName                  string
    Status                    NodeScaleInStatus
    SafeToRemove              bool
    CanStart                  bool
    CanAdvance                bool
    CanCancel                 bool
    ConnectionSafetyVerified  bool
    BlockedReasons            []NodeScaleInBlockedReason
    Checks                    NodeScaleInChecks
    Progress                  NodeScaleInProgress
    Leaders                   []NodeScaleInLeader
    NextAction                string
}
```

Add management options rather than importing access adapters into the usecase:

```go
type RuntimeSummaryReader interface {
    NodeRuntimeSummary(ctx context.Context, nodeID uint64) (NodeRuntimeSummary, error)
}

type Options struct {
    SlotReplicaN int
    RuntimeSummary RuntimeSummaryReader
}
```

`internal/app` should implement this interface by combining local runtime/gateway state with an `internal/access/node` RPC client for remote targets.

Error behavior:

- `PlanNodeScaleIn`: returns report even when blocked; only infrastructural read failures return errors.
- `StartNodeScaleIn`: returns `ErrNodeScaleInBlocked` with report mapped to HTTP 409. Because the lower controller operator command is not guarded in V1, `StartNodeScaleIn` should refresh the report immediately after `MarkNodeDraining`.
- `AdvanceNodeScaleIn`: returns `ErrInvalidScaleInState` if no safe transfer candidate exists.
- `CancelNodeScaleIn`: idempotent if node is already not Draining.

## 15. HTTP Error Mapping

| Error | HTTP | Code |
|-------|------|------|
| invalid node id/body | 400 | `invalid_request` |
| node not found | 404 | `node_not_found` |
| preflight blocked | 409 | `scale_in_blocked` |
| invalid state for advance/cancel | 409 | `invalid_scale_in_state` |
| Controller leader unavailable | 503 | `controller_unavailable` |
| runtime summary unavailable | 503 or 409 with blocked report | `runtime_summary_unavailable` |
| unexpected error | 500 | `internal_error` |

For 409 scale-in errors, include the full report in the response body.

## 16. Kubernetes Manual Runbook for V1

Manual operator flow:

```text
1. Confirm target is the StatefulSet tail node.
2. Call plan or use manager UI preflight.
3. If no blocking reasons, call start.
4. Watch status.
5. Call advance until no target Slot leaders remain.
6. Wait for active connections to reach zero, or use controlled force close if enabled.
7. Wait for ready_to_remove.
8. Run kubectl scale statefulset <name> --replicas=<N-1>.
9. Verify cluster health after Pod removal.
```

The UI should show the exact command only as guidance. WuKongIM should not execute it in V1.

## 17. Implementation Batches

### Batch 0: Strict Read and Safety Primitives

Files:

```text
pkg/cluster/api.go
pkg/cluster/operator.go or a new read helper file
internal/usecase/management/app.go
internal/app build wiring
```

Work:

- Add or identify a strict active-migration/HashSlotTable read for production-safe start and `ready_to_remove`; local `GetMigrationStatus()` is display-only if strict read is unavailable.
- Add runtime-view completeness/freshness validation for all assigned Slots.
- Inject configured `SlotReplicaN` into the management usecase.
- Add `RuntimeSummaryReader` interface to management options, even if the first implementation reports unknown.
- Keep `RemoveSlot` out of the node scale-in path.

### Batch 1: Usecase Report, Preflight, and Method Stubs

Files:

```text
internal/usecase/management/node_scalein.go
internal/usecase/management/node_scalein_test.go
internal/usecase/management/app.go
```

Work:

- Add report DTOs and status constants.
- Add all usecase method stubs so later manager routes compile cleanly.
- Build report from Strict reads, strict migration state, runtime-view completeness, `SlotReplicaN`, and runtime summary.
- Implement preflight checks.
- Implement status calculation.

### Batch 2: Start, Cancel, and Leader Transfer Advance

Files:

```text
internal/usecase/management/node_scalein.go
internal/usecase/management/node_scalein_test.go
```

Work:

- `StartNodeScaleIn` calls `MarkNodeDraining`.
- `CancelNodeScaleIn` calls `ResumeNode`.
- `AdvanceNodeScaleIn` transfers bounded Slot leaders.
- Keep `force_close_connections` as a no-op or unsupported until gateway core drain exists.

### Batch 3: Manager API

Files:

```text
internal/access/manager/node_scalein.go
internal/access/manager/routes.go
internal/access/manager/server_test.go
```

Work:

- Add five routes.
- Map usecase report to JSON DTO.
- Map errors to HTTP responses.

### Batch 4: Draining-aware Readiness

Files:

```text
internal/app/observability.go
pkg/cluster/api.go or existing cluster read path
```

Work:

- Add a cached local drain-state provider updated outside the hot probe path.
- Add `node_not_draining` check to `/readyz`.
- Define a bounded stale/unknown policy for the cached state.
- Keep liveness behavior unchanged.

### Batch 5a: Local Runtime and Gateway Summary

Files are subject to discovery, likely:

```text
internal/runtime/online/
internal/gateway/
internal/app/
```

Work:

- Add local online summary that includes active and closing online connections.
- Add gateway session summary that includes unauthenticated sessions and counts by listener.

### Batch 5b: Remote Runtime Summary RPC

Files are subject to discovery, likely:

```text
internal/access/node/
internal/app/
internal/usecase/management/app.go
internal/usecase/management/node_scalein.go
```

Work:

- Implement `RuntimeSummaryReader` in `internal/app` using local summary or node RPC.
- Keep `internal/usecase/management` dependent only on the interface, not on `internal/access/node`.
- Block `ready_to_remove` when connection/session count is unknown or non-zero.

### Batch 5c: Gateway Admission Drain Gate

Files are subject to discovery, likely:

```text
internal/gateway/
internal/app/
```

Work:

- Reject new sessions while Draining at gateway core admission time, before session registration.

### Batch 5d: Optional Forced Session Close

Files are subject to discovery, likely:

```text
internal/gateway/
internal/runtime/online/
internal/access/node/
```

Work:

- Force-close sessions through gateway core lifecycle with rate limiting.
- Keep this as an action only; never treat it as a safety override.

### Batch 6: Docs and Runbook

Files:

```text
docs/wiki/ or docs/development/
wukongim.conf.example only if new config keys are added
```

Work:

- Document manager-driven scale-in flow.
- Document StatefulSet tail-node restriction.
- Document operational failure modes and rollback.

## 18. Test Plan

### 18.1 Usecase Unit Tests

- Missing node returns not found/blocked.
- Controller voter target is blocked.
- Unverified tail-node mapping is blocked unless operator confirmation is supplied.
- Another Draining data node is blocked.
- Missing `SlotReplicaN` is blocked.
- Remaining data nodes below `SlotReplicaN` is blocked.
- Active hash-slot migration is blocked.
- Active reconcile task involving target is blocked before start.
- Failed reconcile task is blocked.
- Incomplete/stale runtime views are blocked.
- Quorum-lost runtime view is blocked.
- Target as unique healthy replica is blocked.
- `start` calls `MarkNodeDraining` after clean preflight.
- `start` is idempotent when already Draining.
- Assignments containing target produce `migrating_replicas`.
- Target leaders produce `transferring_leaders`.
- Active connections produce `waiting_connections`.
- Clean target produces `ready_to_remove`.
- `advance` selects a non-target Alive Data peer and calls `TransferSlotLeader`.
- `cancel` calls `ResumeNode`.

### 18.2 Manager API Tests

- Route permissions match node/slot read/write actions.
- Invalid node id returns 400.
- Blocked start returns 409 with report.
- Successful start returns report.
- Advance request clamps `max_leader_transfers`.
- Cancel returns report.

### 18.3 Integration / E2E Tests

- Three-node cluster with `SlotReplicaN=2`: scale in tail node to `ready_to_remove`.
- Three-node cluster with `SlotReplicaN=3`: scale-in blocked.
- Target node with active Slot leader: `advance` transfers leader.
- Active hash-slot migration blocks scale-in.
- Draining target readiness returns 503.
- After manual Pod removal, remaining cluster stays healthy.

## 19. Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| StatefulSet deletes a different Pod than selected | Require explicit NodeID/ordinal mapping or operator tail confirmation; future Operator verifies directly |
| Draining does not migrate replicas fast enough | Show active tasks and blocking reasons; allow cancel/resume; keep repair logic existing |
| Draining stalls because no replacement node exists | Preflight checks replacement capacity and status reports missing candidates |
| Slot lacks quorum, has active hash-slot migration, or has failed repair task | Fail closed and expose exact blocker; require operator repair/retry before continuing |
| Leader transfer causes bursty leadership changes | Bound transfers per `advance`; default 1 |
| Connection count unavailable for remote target | V1b node runtime summary; block `ready_to_remove` if unknown |
| Controller leader unavailable during scale-in | Fail closed; report 503 and keep node Draining until operator cancels or retries |
| Failed task leaves node stuck | Report `failed`; require repair/recover/force reconcile before continuing |
| Kubernetes deletes the draining source before it executes its own repair task | UI/runbook must wait for `ready_to_remove`; source executor preference makes early Pod deletion unsafe |
| Gateway sessions are closed through online adapter only | Forced close must use gateway core lifecycle so unregister/offline side effects run |
| Operator scales Kubernetes before `ready_to_remove` | UI/runbook warning; future Operator can enforce automatically |
| Resuming after replicas migrated leaves node idle | Document that Rebalance/Onboarding may be needed to carry load again |

## 20. Future V2 Durable Job

When V1 stabilizes, introduce a durable `NodeDecommissionJob` that mirrors NodeOnboardingJob:

- Metadata stored in Controller meta.
- Raft command for job update.
- Status guard on transitions.
- One running decommission/onboarding job at a time.
- Controller-side coordinator advances replica migration, leader transfer, and connection drain states.
- Manager API can keep the same response shape and add job IDs/history.

Suggested future states:

```text
planned
prechecking
draining
migrating_replicas
transferring_leaders
waiting_connections
ready_to_remove
completed
failed
cancelled
```

V2 should also introduce a `Removed` or `Tombstoned` membership lifecycle state if the cluster needs to distinguish a safely removed node from a temporarily Dead or Draining node.
