# Node List Backend Design

## Context

The web admin `Nodes` page should describe a WuKongIM node as a layered cluster participant, not as a single-role server. A node can be a data member, a Controller voter, the Controller leader or follower, a Slot leader for some Slots, a Slot follower for others, and a Channel leader or follower for individual channels.

The existing manager API already exposes `GET /manager/nodes`, `GET /manager/nodes/:node_id`, drain/resume actions, and scale-in endpoints. Recent cleanup removed distributed-log health from the default node payload. This design keeps that separation and defines the next backend shape for node inventory.

## Goals

- Make node roles explicit without conflating membership, Controller, Slot, and runtime roles.
- Keep the list endpoint lightweight enough for frequent refreshes.
- Give the web UI enough structured data to render badges, counters, and safe action states without duplicating controller scheduling rules.
- Preserve existing API compatibility by adding fields rather than removing current fields.
- Keep heavy distributed-log diagnostics out of the list and basic detail endpoints.

## Non-Goals

- Do not expose per-Channel replica roles on the node list.
- Do not run per-Slot log-watermark RPCs during `GET /manager/nodes`.
- Do not let dynamic join automatically convert data nodes into Controller voters.
- Do not implement node scale-in or onboarding workflows in this design; only expose inventory fields that those workflows can consume.

## API Shape

### `GET /manager/nodes`

Return an inventory snapshot ordered by `node_id`.

```json
{
  "generated_at": "2026-04-28T10:00:00Z",
  "controller_leader_id": 1,
  "total": 3,
  "items": [
    {
      "node_id": 1,
      "name": "node-1",
      "addr": "10.0.0.1:7000",
      "is_local": true,
      "capacity_weight": 1,
      "membership": {
        "role": "data",
        "join_state": "active",
        "schedulable": true
      },
      "health": {
        "status": "alive",
        "last_heartbeat_at": "2026-04-28T09:59:58Z"
      },
      "controller": {
        "role": "leader",
        "voter": true,
        "leader_id": 1
      },
      "slots": {
        "replica_count": 3,
        "leader_count": 2,
        "follower_count": 1,
        "quorum_lost_count": 0,
        "unreported_count": 0
      },
      "runtime": {
        "accepting_new_sessions": true,
        "draining": false,
        "active_online": 128,
        "gateway_sessions": 96,
        "unknown": false
      },
      "actions": {
        "can_drain": true,
        "can_resume": false,
        "can_scale_in": false,
        "can_onboard": false
      }
    }
  ]
}
```

Compatibility fields should remain during transition:

- `status`
- `last_heartbeat_at`
- `controller.role`
- `slot_stats.count`
- `slot_stats.leader_count`

The web can migrate incrementally from compatibility fields to the structured groups.

### `GET /manager/nodes/:node_id`

Return the same node inventory item plus lightweight placement detail.

```json
{
  "node_id": 1,
  "name": "node-1",
  "addr": "10.0.0.1:7000",
  "membership": { "role": "data", "join_state": "active", "schedulable": true },
  "health": { "status": "alive", "last_heartbeat_at": "2026-04-28T09:59:58Z" },
  "controller": { "role": "leader", "voter": true, "leader_id": 1 },
  "slots": {
    "hosted_ids": [1, 2, 3],
    "leader_ids": [1, 2],
    "replica_count": 3,
    "leader_count": 2,
    "follower_count": 1,
    "quorum_lost_count": 0,
    "unreported_count": 0
  },
  "runtime": { "accepting_new_sessions": true, "draining": false, "active_online": 128, "gateway_sessions": 96, "unknown": false },
  "actions": { "can_drain": true, "can_resume": false, "can_scale_in": false, "can_onboard": false }
}
```

### Future Diagnostics Endpoint

Use a separate endpoint for expensive log health:

- `GET /manager/nodes/:node_id/diagnostics`
- or `GET /manager/nodes/:node_id/log-health`

This endpoint may call `SlotLogStatusOnNode` and return sampled Slot Raft lag, apply gaps, unavailable counts, and controller log context. It should not block the default inventory list.

## Field Semantics

### Membership

`membership.role` maps durable controller metadata roles:

- `data`: `controllermeta.NodeRoleData`
- `controller_voter`: `controllermeta.NodeRoleControllerVoter`
- `unknown`: unset or unrecognized role

`membership.join_state` maps durable join lifecycle:

- `joining`
- `active`
- `rejected`
- `unknown`

`membership.schedulable` must match the planner rule: the node is schedulable only when it is `Active + Alive + Data`.

### Health

`health.status` is health or operator state only:

- `alive`
- `suspect`
- `dead`
- `draining`
- `unknown`

Do not use health status as a role. A draining node may still host replicas until reconciliation completes.

### Controller

`controller.role` is the runtime Controller Raft summary:

- `leader`: current Controller leader
- `follower`: configured Controller voter but not leader
- `none`: not a Controller voter

`controller.voter` should be derived from configured Controller peer IDs, not only from `membership.role`, because current static voter membership is configuration-derived.

### Slots

`slots.replica_count` counts observed Slot runtime peers hosted by the node.
`slots.leader_count` counts observed Slots whose leader is the node.
`slots.follower_count` is `replica_count - leader_count` clamped at zero.
`slots.quorum_lost_count` counts hosted Slots whose runtime view has `HasQuorum=false`.
`slots.unreported_count` is reserved for assignment/runtime mismatch cases where the node is expected to host a Slot but no runtime view confirms it.

### Runtime

`runtime` is optional in the first implementation phase. When present, it should reuse the manager runtime summary source used by scale-in safety checks.

- `accepting_new_sessions`: whether the node accepts new gateway sessions.
- `draining`: whether node-local admission/runtime is draining.
- `active_online`: active online connections or sessions owned by the node.
- `gateway_sessions`: gateway session count.
- `unknown`: true when runtime data is unavailable or stale.

### Actions

`actions` are backend business capability hints. The frontend still applies auth permissions before enabling buttons.

- `can_drain`: node is not already draining and can be targeted by the drain operation.
- `can_resume`: node is currently draining.
- `can_scale_in`: node can start or continue the data-node scale-in flow at a high level; detailed blockers remain in the scale-in report endpoint.
- `can_onboard`: node is an active data node that is a candidate for explicit resource allocation.

## Backend Design

### Usecase Layer

Update `internal/usecase/management` with explicit node inventory types:

- `NodeMembership`
- `NodeHealth`
- `NodeController`
- `NodeSlotSummary`
- `NodeRuntimeSummary`
- `NodeActions`

`ListNodes(ctx)` should perform only strict controller-backed inventory reads plus cheap in-process summaries:

1. `cluster.ListNodesStrict(ctx)`
2. `cluster.ListObservedRuntimeViewsStrict(ctx)`
3. `cluster.ControllerLeaderID()`
4. runtime summary reader if available and bounded
5. action hint calculation

It should not call `SlotLogStatusOnNode`.

`GetNode(ctx, nodeID)` should reuse the same snapshot and add hosted and leader Slot IDs. It should not call distributed-log diagnostics by default.

### Access Layer

Update `internal/access/manager` DTOs to mirror the usecase groups. The access layer should remain an adapter only: no scheduling decisions, no role inference beyond mapping usecase DTOs to JSON.

Existing fields remain during a compatibility window. Tests should assert both old fields and new grouped fields until the web no longer needs the old shape.

### Web Contract

Update `web/src/lib/manager-api.types.ts` to include the grouped fields while preserving existing fields. The Nodes page should render grouped badges in this order:

1. Node identity: ID, name, local badge, address.
2. Membership: data/controller voter, active/joining/rejected, schedulable.
3. Health: alive/suspect/dead/draining.
4. Controller: leader/follower/none and voter marker.
5. Slots: replicas, leaders, followers, quorum-lost/unreported indicators.
6. Runtime: sessions/admission when present.
7. Actions: drain/resume, scale-in, inspect.

## Error Handling

- If strict controller reads fail with no leader, not leader, not started, or deadline exceeded, return `503` with the existing `service_unavailable` shape.
- If a node detail ID is invalid, return `400`.
- If a node is absent from controller metadata, return `404`.
- If runtime summary is unavailable but controller reads succeeded, return inventory with `runtime.unknown=true` instead of failing the entire list.
- Diagnostics endpoint failures should not affect list/detail inventory endpoints.

## Testing Plan

### Go

- `internal/usecase/management` tests:
  - maps `NodeRoleData`, `NodeRoleControllerVoter`, and unknown roles.
  - maps `NodeJoinStateJoining`, `Active`, `Rejected`, and unknown states.
  - computes `schedulable` only for `Active + Alive + Data`.
  - computes Controller `leader`, `follower`, and `none`.
  - computes Slot replica, leader, follower, quorum-lost, and unreported counts.
  - proves `ListNodes` does not call `SlotLogStatusOnNode`.
- `internal/access/manager` tests:
  - JSON includes new grouped fields.
  - legacy compatibility fields still exist.
  - strict read failures still return 503.

### Web

- Type tests or component tests cover new grouped response shape.
- Nodes page renders separate membership, health, Controller, Slot, and runtime summaries.
- Buttons remain permission-gated and also respect backend action hints.

## Rollout

1. Add backend usecase fields and access DTOs while preserving current JSON fields.
2. Update web TypeScript types and node page rendering to prefer grouped fields.
3. Add diagnostics endpoint only after list/detail fields are stable.
4. Remove compatibility fields only after a separate deprecation decision.
