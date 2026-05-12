# Channel Cluster Operations P0.5 Design

## Goal

Add the first safe manager operations for Channel Cluster pages after the P0 read path: a truthful replica/status detail endpoint and a policy-driven channel leader repair action. This slice must not expose fake leader-transfer controls or infer follower lag from metadata that cannot prove it.

## Scope

In scope:

- `GET /manager/channel-cluster/:type/:id/replicas`
  - Returns the authoritative channel runtime metadata already used by the list/detail pages.
  - Returns the best available runtime status for the channel from the local/leader runtime path.
  - Returns per-replica rows with replica IDs and explicit `reported` flags.
  - Uses `null` for unknown replica `commit_seq`, `leo`, `checkpoint_hw`, and `lag` values.
- `POST /manager/channel-cluster/:type/:id/repair`
  - Runs the existing safe leader repair path through `internal/usecase/management`.
  - Returns the repaired/validated channel metadata and whether authoritative metadata changed.
  - Uses `cluster.channel` write permission.
- Frontend updates on `/channel-cluster/unhealthy`
  - Add inspectable replica/status detail for an unhealthy row.
  - Add a `Repair leader` action only for rows whose reasons include `no_leader` or leader-related unhealthy state.
  - Refresh the unhealthy page after a successful repair.

Out of scope:

- Explicit `target_node_id` leader transfer.
- Batch leader drain.
- Claimed follower lag when follower proof is unavailable.
- Durable operation history or audit log.
- New config fields.

## Architecture

The manager HTTP layer remains an adapter under `internal/access/manager`. It calls only `internal/usecase/management`; it does not call runtime or node RPCs directly.

`internal/usecase/management` gets narrow operation ports:

- `ChannelReplicaStatusReader` reads best-effort channel runtime status.
- `ChannelLeaderRepairOperator` invokes the existing channel leader repairer.

`internal/app` is the only composition root. It wires these ports from existing local objects:

- status reader from `app.channelLog.Status` for the current node's runtime path.
- repair operator from the existing `runtime/channelmeta.LeaderRepairer` built in `build.go`.

This keeps the dependency direction consistent: `access -> usecase -> runtime/pkg via ports`, with concrete wiring in `app`.

## Backend Details

### Replica/status detail

Add usecase DTOs such as:

```go
type ChannelClusterReplicaStatus struct {
    NodeID uint64
    Role string
    IsLeader bool
    InISR bool
    Reported bool
    CommitSeq *uint64
    LEO *uint64
    CheckpointHW *uint64
    Lag *uint64
}

type ChannelClusterReplicaDetail struct {
    Channel ChannelRuntimeMetaDetail
    RuntimeReported bool
    CommitSeq *uint64
    MinAvailableSeq *uint64
    RetentionThroughSeq *uint64
    Replicas []ChannelClusterReplicaStatus
}
```

Initial implementation is intentionally conservative:

- The method first reads authoritative metadata with existing `GetChannelRuntimeMeta`.
- It builds a row for every `Replicas` entry.
- It calls the status reader once for the channel.
- If status succeeds, it marks the leader/local row as `reported=true` and fills `commit_seq` from `CommittedSeq`/`HW`.
- For other replicas, fields remain unknown unless a future runtime port proves them.
- `lag` is calculated only when both leader commit and replica commit are known; otherwise `null`.
- `ErrNotReady`, `ErrStaleMeta`, or missing local runtime should not make the whole endpoint fail if authoritative metadata exists; they only set `runtime_reported=false`.

This gives operators a truthful detail panel without pretending to know follower progress.

### Repair action

Add usecase DTOs such as:

```go
type RepairChannelClusterLeaderRequest struct {
    ChannelID string
    ChannelType int64
    Reason string
}

type RepairChannelClusterLeaderResponse struct {
    Changed bool
    Channel ChannelRuntimeMetaDetail
}
```

The usecase flow:

1. Validate channel ID and type.
2. Load authoritative `ChannelRuntimeMeta`.
3. Choose the reason:
   - Use request reason if non-empty and allowed.
   - Otherwise derive from metadata: `no_leader` maps to a leader repair reason that the existing repairer can evaluate.
4. Call `ChannelLeaderRepairOperator`.
5. Return authoritative metadata after repair/validation as a `ChannelRuntimeMetaDetail`.

Error mapping should preserve existing semantics:

- `metadb.ErrNotFound` -> 404.
- `channel.ErrNoSafeChannelLeader` -> 409 conflict.
- `channel.ErrInvalidConfig`, invalid body/params -> 400 or 500 depending on source.
- slot/controller leader unavailability -> 503.

### Reason mapping

P0 unhealthy reasons are manager-facing strings. Repair must use runtime channel leader repair reasons. The design should not invent a new runtime reason if an existing one can be reused. For the first slice:

- `no_leader` maps to the existing missing/dead leader repair policy path.
- `isr_insufficient` alone does not show a repair button, because leader repair does not add ISR members.
- `status_not_active` alone does not show a repair button, because non-active channel lifecycle is not a leader repair problem.

## HTTP API

### `GET /manager/channel-cluster/:type/:id/replicas`

Response shape:

```json
{
  "channel": {
    "channel_id": "room-1",
    "channel_type": 2,
    "slot_id": 9,
    "hash_slot": 123,
    "channel_epoch": 7,
    "leader_epoch": 3,
    "leader": 1,
    "replicas": [1, 2, 3],
    "isr": [1, 2],
    "min_isr": 2,
    "max_message_seq": 42,
    "status": "active",
    "features": 0,
    "lease_until_ms": 0
  },
  "runtime_reported": true,
  "commit_seq": 42,
  "min_available_seq": 1,
  "retention_through_seq": 0,
  "replicas": [
    {
      "node_id": 1,
      "role": "leader",
      "is_leader": true,
      "in_isr": true,
      "reported": true,
      "commit_seq": 42,
      "leo": null,
      "checkpoint_hw": null,
      "lag": 0
    },
    {
      "node_id": 2,
      "role": "follower",
      "is_leader": false,
      "in_isr": true,
      "reported": false,
      "commit_seq": null,
      "leo": null,
      "checkpoint_hw": null,
      "lag": null
    }
  ]
}
```

Read permission: `cluster.channel` `r`.

### `POST /manager/channel-cluster/:type/:id/repair`

Request body:

```json
{
  "reason": "no_leader"
}
```

Response shape:

```json
{
  "changed": true,
  "channel": {
    "channel_id": "room-1",
    "channel_type": 2,
    "slot_id": 9,
    "hash_slot": 123,
    "channel_epoch": 7,
    "leader_epoch": 4,
    "leader": 2,
    "replicas": [1, 2, 3],
    "isr": [2, 3],
    "min_isr": 2,
    "max_message_seq": 42,
    "status": "active",
    "features": 0,
    "lease_until_ms": 1780000000000
  }
}
```

Write permission: `cluster.channel` `w`.

## Frontend Details

Update `web/src/lib/manager-api.types.ts` and `web/src/lib/manager-api.ts` with the new DTOs and wrappers.

Update `/channel-cluster/unhealthy`:

- Add a row action `Inspect replicas` that opens a detail sheet or inline panel.
- The detail view shows:
  - channel ID/type, slot, leader, ISR, min ISR,
  - runtime commit sequence if reported,
  - per-replica rows with `Reported` / `Not reported` and unknown fields rendered as `-`.
- Add `Repair leader` only when a row has `no_leader` reason.
- On successful repair, show the returned leader/changed state and reload the unhealthy list from the first page.
- On 409 no safe candidate, show a non-destructive conflict state and keep the row visible.

## Testing

Use TDD for every slice.

Backend tests:

- Usecase tests for replica detail with reported and unreported runtime status.
- Usecase tests for repair reason mapping, changed result, not-found, no-safe-candidate conflict, and invalid request validation.
- Manager HTTP tests for JSON shape, route permissions, invalid params/body, 404, 409, and 503 mappings.
- App wiring test that the management app receives non-nil channel cluster operations when manager is enabled.

Frontend tests:

- API client URL/body/error tests.
- Unhealthy page tests for inspect detail, unknown replica values, repair success refresh, repair conflict, and permission/unavailable states.

Verification:

- `GOWORK=off go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1`
- targeted web vitest for manager API and unhealthy page
- `cd web && bun run build`

## Safety And Follow-up

This slice deliberately keeps explicit target transfer out of the UI/API. A later P0.6 can add target transfer only after the runtime repairer supports “specified candidate with dry-run safety proof” as a first-class port.
