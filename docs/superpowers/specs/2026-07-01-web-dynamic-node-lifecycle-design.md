# Web Dynamic Node Lifecycle Design

Date: 2026-07-01
Status: Proposed for review
Scope: `web`, internalv2 manager API bindings, cluster node lifecycle UI

## Purpose

The web manager should expose node expansion and node scale-in using the current
`internalv2` manager APIs. The UI must guide operators through the real
cluster-authoritative lifecycle instead of preserving older job-shaped
frontend-only assumptions.

This design covers the web product surface and API binding shape only. The
backend source of truth remains the existing `internalv2/access/manager` and
`internalv2/usecase/management` node lifecycle, onboarding, scale-in, and
diagnostics flows.

Single-node deployment is still a single-node cluster. The UI must not present
any action as bypassing cluster state or ControllerV2 safety gates.

## Current State

The backend now exposes per-node lifecycle routes:

- `POST /manager/nodes/join`
- `POST /manager/nodes/:node_id/activate`
- `POST /manager/nodes/:node_id/onboarding/plan`
- `POST /manager/nodes/:node_id/onboarding/start`
- `GET /manager/nodes/:node_id/onboarding/status`
- `POST /manager/nodes/:node_id/onboarding/advance`
- `POST /manager/nodes/:node_id/scale-in/plan`
- `POST /manager/nodes/:node_id/scale-in/start`
- `POST /manager/nodes/:node_id/scale-in/drain`
- `GET /manager/nodes/:node_id/scale-in/status`
- `POST /manager/nodes/:node_id/scale-in/advance`
- `POST /manager/nodes/:node_id/scale-in/remove`
- `GET /manager/nodes/:node_id/diagnostics`

The current web code still contains stale contracts:

- `web/src/pages/onboarding` calls `/manager/node-onboarding/*`, but the
  backend no longer exposes that standalone candidate/job surface.
- `web/src/lib/manager-api.ts` sends old scale-in fields such as
  `confirm_statefulset_tail`, `expected_tail_node_id`, and
  `force_close_connections`.
- The current scale-in UI expects one aggregated report with `checks`,
  `progress`, and `cancel`, while the backend now returns separate stage
  responses for plan, start, drain, status, advance, and remove.
- The backend intentionally has no scale-in cancel route. The web must not
  advertise cancellation until a fenced backend writer exists.

## Goals

1. Make `/cluster/nodes` the single web control surface for data-node lifecycle.
2. Support full expansion: join, activate, and bounded Slot onboarding.
3. Support full scale-in: mark leaving, enable gateway drain mode, move Slot
   replicas away, inspect safety, and remove only when safe.
4. Make dynamic-node diagnostics the primary "why is this stuck" entrypoint.
5. Keep task history in `/cluster/tasks`; lifecycle panels should link to task
   filters instead of cloning task-history behavior.
6. Keep all operator writes explicit and bounded.

## Non-Goals

- Do not add backend compatibility routes for `/manager/node-onboarding/*`.
- Do not implement scale-in cancel in the web.
- Do not force-close existing gateway sessions from the web.
- Do not mutate Slot assignments, Channel metadata, or node lifecycle from web
  code except through the documented manager APIs.
- Do not add automatic polling over full-cardinality cluster inventories.

## Information Architecture

`/cluster/nodes` becomes the node lifecycle console:

- The page header exposes `Add node`.
- Each node row exposes lifecycle-aware actions:
  - `joining`: `Activate`, `Diagnostics`
  - `active` and schedulable data node: `Onboard slots`, `Scale in`,
    `Diagnostics`
  - `leaving`: `Scale-in status`, `Drain mode`, `Advance`, `Diagnostics`
  - `removed`: read-only details
- Existing legacy redirects may continue to send `/onboarding` to
  `/cluster/nodes?panel=onboarding`, but that panel must use the per-node
  onboarding routes.
- `/cluster/tasks` remains the task history and audit surface. Lifecycle panels
  link to `slot_replica_move` task filters with the relevant `node_id`.

## API Binding Contract

Replace stale lifecycle API bindings with the current backend contract:

- `joinNode(input)` -> `POST /manager/nodes/join`
- `activateNode(nodeId)` -> `POST /manager/nodes/:node_id/activate`
- `planNodeOnboarding(nodeId, input)` ->
  `POST /manager/nodes/:node_id/onboarding/plan`
- `startNodeOnboarding(nodeId, input)` ->
  `POST /manager/nodes/:node_id/onboarding/start`
- `getNodeOnboardingStatus(nodeId)` ->
  `GET /manager/nodes/:node_id/onboarding/status`
- `advanceNodeOnboarding(nodeId, input)` ->
  `POST /manager/nodes/:node_id/onboarding/advance`
- `planNodeScaleIn(nodeId, input)` ->
  `POST /manager/nodes/:node_id/scale-in/plan`
- `startNodeScaleIn(nodeId)` ->
  `POST /manager/nodes/:node_id/scale-in/start`
- `setNodeScaleInDrain(nodeId, input)` ->
  `POST /manager/nodes/:node_id/scale-in/drain`
- `getNodeScaleInStatus(nodeId)` ->
  `GET /manager/nodes/:node_id/scale-in/status`
- `advanceNodeScaleIn(nodeId, input)` ->
  `POST /manager/nodes/:node_id/scale-in/advance`
- `removeNodeAfterScaleIn(nodeId)` ->
  `POST /manager/nodes/:node_id/scale-in/remove`
- `getDynamicNodeDiagnostics(nodeId, limits)` ->
  `GET /manager/nodes/:node_id/diagnostics`

Request bodies should use only backend-supported fields:

```json
{ "max_slot_moves": 1 }
```

for onboarding and Slot-drain planning or advancement, and:

```json
{ "draining": true }
```

for gateway drain mode.

Frontend types should model each backend response separately. Avoid one
catch-all `ManagerNodeScaleInReport` because the backend stages intentionally
return different evidence:

- `ManagerJoinNodeResponse`
- `ManagerActivateNodeResponse`
- `ManagerNodeOnboardingPlanResponse`
- `ManagerNodeOnboardingStartResponse`
- `ManagerNodeOnboardingStatusResponse`
- `ManagerNodeScaleInPlanResponse`
- `ManagerNodeScaleInStartResponse`
- `ManagerNodeScaleInDrainResponse`
- `ManagerNodeScaleInStatusResponse`
- `ManagerNodeScaleInAdvanceResponse`
- `ManagerNodeScaleInRemoveResponse`
- `ManagerDynamicNodeDiagnosticsResponse`

## Expansion Flow

Expansion is a guided side panel opened from `/cluster/nodes`.

### 1. Join

The form accepts:

- `node_id`
- `addr`
- optional `name`
- optional `capacity_weight`

Submitting calls `joinNode`. `202 Accepted` means the Controller state changed;
`200 OK` means an idempotent no-op. After either result, refresh the node list.

The UI should describe the result as `joining`, not as available capacity. A
joining node is visible for discovery and readiness work but is not schedulable.

### 2. Activate

For `joining` nodes, the row and side panel show `Activate`. The action calls
`activateNode`.

On success, refresh the node list and show that the node is now eligible for
Slot onboarding if it becomes schedulable.

On `409 conflict`, surface the backend message without replacing it with a
generic text. Activation errors are useful evidence because they can name
transport reachability, control mirror catch-up, runtime readiness, cluster ID,
or revision fencing.

### 3. Onboard Slots

For active schedulable data nodes, `Onboard slots` calls
`planNodeOnboarding` first. The preview displays:

- `state_revision`
- `max_slot_moves`
- candidate rows: Slot ID, source node, target node, target peers, config epoch
- skipped rows: Slot ID, reason, message

After operator confirmation, `startNodeOnboarding` creates bounded
`slot_replica_move` tasks. The panel then polls `getNodeOnboardingStatus` only
while the panel is open and active tasks exist.

When active tasks reach zero, the operator can request a new plan or call
`advanceNodeOnboarding` to create the next bounded batch. This keeps large
clusters from receiving unbounded task bursts.

## Scale-In Flow

Scale-in is also a guided side panel opened from `/cluster/nodes`.

### 1. Mark Leaving

The `Scale in` action first calls `planNodeScaleIn` to preview candidate Slot
replica moves away from the target node. If the operator confirms, call
`startNodeScaleIn`. This only marks the node `leaving`; it does not close
sessions or move Slots by itself.

After the node is leaving, refresh the node list and open the scale-in status
view.

### 2. Gateway Drain Mode

The panel exposes an explicit `Drain new sessions` toggle backed by
`setNodeScaleInDrain`.

The UI copy must state that drain mode disables new gateway admission and does
not close existing sessions. Runtime counters returned by the drain response
should be shown immediately: gateway sessions, active online, closing online,
total online, pending activations, and whether runtime is unknown.

### 3. Slot Drain

`planNodeScaleIn` previews Slot replica moves from the leaving node to active
replacement data nodes. `advanceNodeScaleIn` creates at most the requested
bounded batch. Default the control to a small value, with the backend cap still
authoritative.

The panel should show created and skipped counts plus the submitted candidates.
Detailed task history remains a link to `/cluster/tasks`.

### 4. Safety Status

`getNodeScaleInStatus` is the source of truth for removal readiness. Display
both the high-level result and individual blockers:

- durable join state and `state_revision`
- `safe_to_proceed`
- `safe_to_remove`
- health freshness, health status, report age, report TTL
- observed and required control revisions
- Slot replica count and Slot leader count
- active and failed task counts
- Channel leader, replica, and ISR counts
- gateway drain mode, accepting new sessions, gateway sessions, active online,
  closing online, total online, and pending activations
- machine-readable `blocked_reasons`

Only enable `Remove node` when `safe_to_remove=true`. The remove action calls
`removeNodeAfterScaleIn`, then refreshes the node list. Removed nodes are shown
as durable tombstones and have no mutating actions.

## Diagnostics Flow

Every expansion and scale-in panel includes `Diagnostics`.

`getDynamicNodeDiagnostics` should display:

- summary and recommended next action
- blocked reasons
- active task count and failed task count
- Slot replica count and Slot leader count
- control revision gap
- oldest task age
- source availability for control snapshot, task audit, and Slot runtime
- bounded active task rows
- bounded retained task-audit rows
- bounded related Slot rows
- warnings

The diagnostics panel is read-only. It must not retry tasks, change drain mode,
advance Slot movement, or remove nodes.

## Component Plan

Suggested frontend shape:

- `web/src/lib/manager-api.types.ts`: replace stale lifecycle types with current
  per-stage response types.
- `web/src/lib/manager-api.ts`: remove stale `/manager/node-onboarding/*`,
  `/draining`, `/resume`, and `/scale-in/cancel` lifecycle calls where the
  current internalv2 manager no longer supports them.
- `web/src/pages/nodes/page.tsx`: own node table and lifecycle side panels.
- `web/src/pages/onboarding/page.tsx`: either remove it or reduce it to a thin
  wrapper that renders the nodes lifecycle panel from the redirected route.
- `web/src/pages/tasks/page.tsx`: keep read-only, with filters used by lifecycle
  links.

Use dense operational UI patterns already present in the manager shell:

- tables for candidate Slot moves and related tasks
- status badges for lifecycle states and blocker states
- explicit confirm dialogs for `start`, `advance`, `drain`, and `remove`
- a detail sheet for lifecycle and diagnostics instead of a new landing page

## Error Handling

- `400 bad_request`: show validation text near the form or action.
- `403 forbidden`: show permission guidance and disable mutating controls.
- `404 not_found`: refresh the node list and tell the operator the node is no
  longer present in current control state.
- `409 conflict`: preserve the backend message; for activation and scale-in this
  is actionable evidence.
- `503 service_unavailable`: keep the current panel open and show that manager
  lifecycle dependencies are unavailable.

If an action fails, do not clear the latest known plan, status, or diagnostics
evidence. Operators need the previous evidence to compare against the failure.

## Performance And Safety

- Default `max_slot_moves` should be small and visible. Large clusters should
  advance in bounded batches, not in an unbounded browser request.
- Poll only while the lifecycle panel is open and there are active relevant
  tasks. Use manual refresh for diagnostics and final scale-in status when no
  active task is running.
- Do not fetch full Channel inventories from the web; rely on the backend's
  bounded scale-in status and diagnostics responses.
- Keep default node list lightweight. Diagnostics and task detail are explicit
  drill-downs.

## Testing

Frontend tests should cover:

- API binding paths and request bodies for join, activate, onboarding, scale-in,
  drain, remove, and diagnostics.
- Node row actions for `joining`, `active`, `leaving`, and `removed` states.
- Expansion flow from join result to activate to onboarding plan/start/status.
- Scale-in flow from plan to start, drain mode, advance, status, and remove.
- Conflict handling that preserves backend messages.
- The absence of stale calls: `/manager/node-onboarding/*`,
  `/manager/nodes/:id/draining`, `/manager/nodes/:id/resume`, and
  `/manager/nodes/:id/scale-in/cancel`.
- TypeScript compilation for the new response model.

Focused verification:

```bash
cd web && ./node_modules/.bin/vitest run src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
cd web && ./node_modules/.bin/tsc -b
git diff --check
```

If the implementation changes manager route DTOs, also run the focused Go tests:

```bash
GOWORK=off /usr/local/go/bin/go test -count=1 ./internalv2/access/manager ./internalv2/usecase/management
```
