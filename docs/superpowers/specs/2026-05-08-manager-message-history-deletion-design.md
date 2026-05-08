# Manager Message History Deletion Design

## Context

The manager web UI can query channel-scoped messages through `GET /manager/messages`. The channel runtime already supports retention through a cluster-authoritative `RetentionThroughSeq` boundary. That boundary makes all messages with `message_seq <= RetentionThroughSeq` unavailable for reads, then lets each node apply and physically trim local rows asynchronously.

The requested manager feature is to let operators delete historical messages from the backend management UI. In this project, single-node deployment is still a single-node cluster, so the deletion path must preserve channel leader, slot metadata, ISR, replay, and follower convergence semantics.

## Goals

- Let a manager operator delete a channel's historical message prefix through a selected `message_seq`.
- Reuse the existing channel retention boundary instead of deleting storage rows directly.
- Keep deletion cluster-authoritative and monotonic across leader changes.
- Make the feature available from the `web` messages page with a destructive confirmation flow.
- Return enough status information for the UI to explain whether the request advanced, no-oped, or was blocked.

## Non-Goals

- Do not delete arbitrary messages in the middle of a channel log.
- Do not support per-message tombstones or sequence holes.
- Do not bypass channel leader or slot metadata by directly deleting Pebble rows from manager handlers.
- Do not add a separate single-node code path.
- Do not add archive/restore for deleted history in this feature.
- Do not support timestamp-based deletion in the first version; it can be added later by converting a timestamp to a contiguous prefix boundary on the leader.

## Recommended Approach

Add a manager operation that advances the channel retention boundary to a requested `through_seq`. The operation means: messages with `message_seq <= through_seq` become unavailable and may be physically trimmed.

```http
POST /manager/messages/retention
```

Request:

```json
{
  "channel_id": "room-1",
  "channel_type": 2,
  "through_seq": 1024,
  "dry_run": false
}
```

Response:

```json
{
  "channel_id": "room-1",
  "channel_type": 2,
  "requested_through_seq": 1024,
  "advanced_through_seq": 1000,
  "min_available_seq": 1001,
  "status": "advanced",
  "blocked_reason": ""
}
```

The implementation may advance to a lower safe boundary than requested when safety gates lag. If no safe advance is possible, it returns `status: "blocked"` with a `blocked_reason`.

## API Design

### Access Layer

Add request and response DTOs in `internal/access/manager/messages.go`:

- `channel_id` is required and non-empty.
- `channel_type` is required and positive.
- `through_seq` is required and positive.
- `dry_run` is optional. When true, the backend calculates the outcome without committing metadata or applying runtime effects. Dry-run uses read-only replay cursor state and is advisory; the real request still durably confirms the cursor before advancing metadata.

Add a write route in `internal/access/manager/routes.go` under `cluster.channel:w` permission:

```go
messageWrites := s.engine.Group("/manager")
messageWrites.Use(s.requirePermission("cluster.channel", "w"))
messageWrites.POST("/messages/retention", s.handleAdvanceMessageRetention)
```

The existing read route remains under `cluster.channel:r`.

### Error Mapping

- `400 bad_request`: invalid channel selector, invalid `through_seq`, or invalid request body.
- `403 forbidden`: manager auth lacks `cluster.channel:w`.
- `404 not_found`: channel runtime metadata does not exist.
- `200 OK` with `status: "blocked"`: request is valid but cannot currently advance because safety gates block it. This keeps blocked safety status available to the web client without treating it as a transport failure.
- `503 service_unavailable`: no channel leader, stale leader metadata, or remote leader unavailable.
- `500 internal_error`: unexpected storage, metadata, or runtime errors.

A safe no-op, such as requesting a `through_seq` below the current retention boundary, should return `200` with `status: "noop"`.

## Usecase Design

Extend `internal/usecase/management/messages.go` with focused request/response types and a new method on the manager app:

```go
type AdvanceMessageRetentionRequest struct {
    ChannelID string
    ChannelType int64
    ThroughSeq uint64
    DryRun bool
}

type AdvanceMessageRetentionResponse struct {
    ChannelID string
    ChannelType int64
    RequestedThroughSeq uint64
    AdvancedThroughSeq uint64
    MinAvailableSeq uint64
    Status MessageRetentionStatus
    BlockedReason MessageRetentionBlockedReason
}
```

The usecase validates request shape and delegates the cluster-aware operation to an injected port. It does not know HTTP, Gin, or Pebble details.

## App and Runtime Design

Implement the manager operation in `internal/app/manager_messages.go` or a nearby focused file.

Data flow:

1. Load `ChannelRuntimeMeta` for `(channel_id, channel_type)`.
2. Require a known leader. If the leader is remote, forward the operation to the leader through node RPC.
3. On the leader, read `RetentionView` from channel runtime.
4. Reject stale or non-leader views.
5. Calculate the safe boundary as the minimum of:
   - requested `through_seq`;
   - current `HW`;
   - current `CheckpointHW`;
   - current `MinISRMatchOffset`;
   - durable committed replay cursor confirmed by the leader-local channel store.
6. If the leader cannot durably confirm the committed replay cursor, fail the request without advancing metadata.
7. If safe boundary is not greater than current `RetentionThroughSeq`, return `noop` or `blocked` depending on the reason.
8. For non-dry-run requests, advance slot metadata with `AdvanceChannelRetentionThroughSeq` using channel epoch, leader epoch, leader, and lease fences from the latest view.
9. Apply the committed boundary locally with `ApplyRetentionBoundary`.
10. Return the resulting `advanced_through_seq` and `min_available_seq`.

The operation must not call local store prefix delete APIs directly. It may only use the store to confirm the committed replay cursor before metadata advance. Physical deletion remains the responsibility of channel runtime/store retention effects after the authoritative boundary is committed.

## Node RPC Design

If a manager request lands on a non-leader node, the app layer should forward the command to the current channel leader. Add a focused node RPC alongside `ChannelMessagesQuery`:

- `AdvanceChannelRetention(ctx, nodeID, req)` on the node client.
- A binary codec matching the existing node RPC style.
- The server-side adapter refreshes channel metadata, verifies local leadership, executes the same leader-local operation, and returns status.
- `not_leader` responses may include the newer leader ID so the client can retry once, following the `QueryChannelMessages` pattern.

This keeps the manager endpoint usable from any node without adding a local-only fallback.

## Web Design

Update the manager web app in `/web`:

- `web/src/lib/manager-api.types.ts`: add `AdvanceMessageRetentionInput`, response type, status and blocked-reason unions.
- `web/src/lib/manager-api.ts`: add `advanceMessageRetention(input)` that posts to `/manager/messages/retention`.
- `web/src/pages/messages/page.tsx`: add a destructive action named "Delete through this Seq" for each message row, plus optional toolbar input for an explicit sequence.
- Confirmation must show channel ID, channel type, and `through_seq`, and explain that messages with `message_seq <= through_seq` will become unavailable.
- After success, refresh the current query and show the returned `min_available_seq` or blocked reason.
- `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`: add button, dialog, success, blocked, and error copy.

The first version should avoid overloading the UI with timestamp deletion. Operators already query messages by channel and can choose the row sequence boundary directly.

## Testing

Backend tests:

- `internal/access/manager/messages_test.go`
  - rejects invalid request bodies and invalid `through_seq`;
  - requires `cluster.channel:w`;
  - returns success JSON;
  - maps blocked status responses, not found, leader unavailable, and stale metadata errors.
- `internal/usecase/management/messages_test.go`
  - validates request shape;
  - passes the request to the retention port;
  - maps response fields without mutating them.
- `internal/app/manager_messages_test.go`
  - local leader advances metadata and applies runtime boundary;
  - dry-run does not mutate metadata or runtime;
  - remote leader forwards through node RPC;
  - stale metadata and no-leader cases return service-unavailable errors;
  - safety gates clamp or block the requested boundary.
- `internal/access/node` tests
  - codec round trip;
  - leader-only execution;
  - not-leader retry response.

Frontend tests:

- `web/src/lib/manager-api.test.ts` verifies path, method, and request body.
- `web/src/pages/messages/page.test.tsx` verifies destructive confirmation, success refresh, blocked status rendering, and error rendering.

Focused verification commands:

```sh
go test ./internal/access/manager ./internal/usecase/management ./internal/app ./internal/access/node -count=1
cd web && yarn test --run src/lib/manager-api.test.ts src/pages/messages/page.test.tsx
```

## Rollout Notes

- Ship `through_seq` deletion first.
- Add timestamp deletion later only by leader-side conversion to a contiguous `through_seq` prefix.
- Keep manager wording precise: this deletes history through a sequence boundary, not individual messages.
- If the dry-run path proves unnecessary during implementation, it can be deferred, but the response should still report blocked/no-op reasons.
