# Channel Cluster Leader Transfer P0.6 Design

## Goal

Add explicit, safe, single-channel leader transfer for the manager Channel Cluster view. Operators can choose a target replica, but the backend must only persist the leader change after the target proves it can safely lead.

## Scope

In scope:

- `POST /manager/channel-cluster/:type/:id/leader/transfer`
  - Request body: `{ "target_node_id": 2 }`.
  - Returns updated authoritative channel runtime metadata and `changed`.
  - Requires `cluster.channel` write permission.
- Runtime support for target-aware channel leader transfer.
  - Target must be a configured replica.
  - Target must be in the authoritative ISR set.
  - Channel must be active.
  - Target must pass the existing durable promotion evaluation path.
  - Only `Leader`, `LeaderEpoch`, and `LeaseUntilMS` may change.
- Frontend support in the existing `/channel-cluster/unhealthy` replica detail panel.
  - Show `Transfer leader` only for non-leader ISR replicas in an active channel.
  - Refresh replica detail and unhealthy list after transfer.
  - Preserve conflict visibility when no safe candidate is available.
- Documentation updates for API coverage and P0.6 status.

Out of scope:

- Batch leader drain.
- Automatic target recommendation.
- Durable operation history or audit log.
- Changing replica set, ISR, MinISR, channel status, message retention, or channel epoch.
- New config fields.
- Guessing follower lag or using unproven follower progress in the UI.

## Architecture

The manager remains a thin HTTP adapter in `internal/access/manager`. It validates request syntax, applies permission middleware, maps errors to HTTP responses, and delegates all behavior to `internal/usecase/management`.

`internal/usecase/management` adds a narrow `ChannelLeaderTransferOperator` port and a `TransferChannelClusterLeader` usecase. The usecase performs manager-facing validation and returns `ChannelRuntimeMetaDetail` using the same detail projection as existing channel cluster operations.

`internal/app` remains the only composition root. It wires the management transfer port to the existing `internal/runtime/channelmeta.LeaderRepairer`, extended with explicit target-transfer methods.

`internal/runtime/channelmeta` owns the safety-critical behavior. It rereads authoritative metadata on the Slot leader, validates the target against the latest metadata, evaluates only the requested target as a promotion candidate, persists the updated metadata only from the authoritative Slot leader, rereads the result, and applies it locally through the existing authoritative-apply callback.

This keeps the dependency direction clear:

```text
access/manager -> usecase/management -> app adapter -> runtime/channelmeta -> pkg/channel + pkg/slot + pkg/cluster
```

## Backend Details

### Management usecase

Add DTOs:

```go
type TransferChannelClusterLeaderRequest struct {
    // ChannelID identifies the channel whose leader should move.
    ChannelID string
    // ChannelType identifies the channel type.
    ChannelType int64
    // TargetNodeID is the replica node that should become leader.
    TargetNodeID uint64
}

type TransferChannelClusterLeaderResult struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool
    // Meta is authoritative metadata after transfer or validation.
    Meta metadb.ChannelRuntimeMeta
}

type TransferChannelClusterLeaderResponse struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool
    // Channel is authoritative manager metadata after transfer or validation.
    Channel ChannelRuntimeMetaDetail
}
```

Add a port:

```go
type ChannelLeaderTransferOperator interface {
    // TransferChannelLeader safely transfers authoritative channel leadership.
    TransferChannelLeader(ctx context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResult, error)
}
```

`TransferChannelClusterLeader` should:

1. Validate `ChannelID != ""`, `ChannelType > 0`, and `TargetNodeID > 0`.
2. Read current authoritative metadata with `GetChannelRuntimeMeta`.
3. Fail fast if the channel is not active, target is not a replica, or target is not in ISR.
4. Return `changed=false` immediately when target is already the authoritative leader.
5. Delegate to `ChannelLeaderTransferOperator`.
6. Convert the returned metadata with `channelRuntimeMetaDetailFromMeta`.

The runtime operator must repeat all state-dependent validations on the latest authoritative metadata before writing, so usecase validation is only an early user-facing guard.

### Runtime target transfer

Add target-transfer types in `internal/runtime/channelmeta`:

```go
type LeaderTransferRequest struct {
    // ChannelID identifies the channel whose leader should move.
    ChannelID channel.ChannelID
    // ObservedChannelEpoch carries the caller's last observed channel epoch.
    ObservedChannelEpoch uint64
    // ObservedLeaderEpoch carries the caller's last observed leader epoch.
    ObservedLeaderEpoch uint64
    // TargetNodeID is the requested new leader.
    TargetNodeID uint64
}

type LeaderTransferResult struct {
    // Meta is authoritative metadata after transfer or validation.
    Meta metadb.ChannelRuntimeMeta
    // Changed reports whether transfer persisted changed metadata.
    Changed bool
}
```

Extend the repairer with two methods:

```go
func (r *LeaderRepairer) TransferIfSafe(ctx context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error)

func (r *LeaderRepairer) TransferChannelLeaderAuthoritative(ctx context.Context, req LeaderTransferRequest) (LeaderTransferResult, error)
```

`TransferIfSafe` routes to the current authoritative Slot leader using cluster routing, just like `RepairIfNeeded`.

`TransferChannelLeaderAuthoritative` performs the safety-critical sequence:

1. Reread latest `ChannelRuntimeMeta` from the authoritative store.
2. If observed channel or leader epoch is stale, return the latest metadata with `changed=false`.
3. Validate latest metadata:
   - `Status == channel.StatusActive`.
   - `TargetNodeID != 0`.
   - target is in `Replicas`.
   - target is in `ISR`.
4. If target is already leader, return latest metadata with `changed=false`.
5. Evaluate only `TargetNodeID` with the existing leader promotion evaluator.
6. If the target report is not `CanLead`, return `channel.ErrNoSafeChannelLeader`.
7. Persist a copy of latest metadata with:
   - `Leader = TargetNodeID`.
   - `LeaderEpoch++`.
   - `LeaseUntilMS = now + BootstrapLease`.
8. Reread authoritative metadata.
9. Apply authoritative metadata locally with the existing callback.
10. Return `changed=true` and the reread metadata.

The implementation must not mutate `Replicas`, `ISR`, `MinISR`, `Status`, `Features`, `RetentionThroughSeq`, or `ChannelEpoch`.

### Node RPC

Manager requests may hit a node that is not the authoritative Slot leader. The target transfer path therefore needs node RPC support analogous to channel leader repair.

Add a dedicated transfer RPC instead of overloading the repair request:

- New access/node DTOs: `ChannelLeaderTransferRequest`, `ChannelLeaderTransferResult`.
- New codec magic values for transfer request/response.
- New RPC service ID for channel leader transfer.
- New `Client.TransferChannelLeader(ctx, req channelmeta.LeaderTransferRequest)` method.
- New adapter handler that calls `TransferChannelLeaderAuthoritative` only on the authoritative Slot leader.

The RPC response should reuse existing redirect statuses where possible:

- `ok` returns a transfer result.
- `not_leader` includes current Slot leader ID.
- `no_leader` maps to `raftcluster.ErrNoLeader`.
- `no_slot` maps to `raftcluster.ErrSlotNotFound`.
- `no_safe_candidate` maps to `channel.ErrNoSafeChannelLeader`.

### Error mapping

Add management-level errors for stable HTTP mapping:

```go
var (
    ErrChannelLeaderTransferTargetNotReplica = errors.New("management: channel leader transfer target is not a replica")
    ErrChannelLeaderTransferTargetNotISR     = errors.New("management: channel leader transfer target is not in isr")
    ErrChannelLeaderTransferInactiveChannel  = errors.New("management: channel leader transfer requires active channel")
)
```

HTTP mapping:

- invalid `channel_type`, missing/zero `target_node_id`, or target not replica -> `400 bad_request`.
- inactive channel, target not in ISR, or no safe candidate -> `409 conflict`.
- missing channel runtime meta -> `404 not_found`.
- Slot/controller authoritative read unavailable -> `503 service_unavailable`.
- unexpected runtime/config errors -> `500 internal_error`.

## HTTP API

### `POST /manager/channel-cluster/:type/:id/leader/transfer`

Request:

```json
{
  "target_node_id": 2
}
```

Success response:

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
    "isr": [1, 2, 3],
    "min_isr": 2,
    "max_message_seq": 42,
    "status": "active",
    "features": 0,
    "lease_until_ms": 1780000000000
  }
}
```

Idempotent response when target already leads:

```json
{
  "changed": false,
  "channel": {
    "channel_id": "room-1",
    "channel_type": 2,
    "leader": 2
  }
}
```

The real response includes the full `ChannelRuntimeMetaDetail` shape.

## Frontend Details

Update `web/src/lib/manager-api.types.ts`:

```ts
export type TransferChannelClusterLeaderInput = {
  target_node_id: number
}

export type ManagerChannelClusterLeaderTransferResponse = {
  changed: boolean
  channel: ManagerChannelRuntimeMetaDetailResponse
}
```

Update `web/src/lib/manager-api.ts` with:

```ts
export function transferChannelClusterLeader(
  channelType: number,
  channelId: string,
  input: TransferChannelClusterLeaderInput,
) {
  return jsonManagerFetch<ManagerChannelClusterLeaderTransferResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/leader/transfer`,
    { method: "POST", body: JSON.stringify(input) },
  )
}
```

Update `/channel-cluster/unhealthy` replica detail:

- Show `Transfer leader` for a row when:
  - `detail.channel.status === "active"`.
  - `replica.in_isr === true`.
  - `replica.is_leader === false`.
  - no transfer request is already pending for that row.
- Do not show transfer for non-ISR replicas.
- Do not require `reported=true`; the backend proves safety.
- On success, show changed/unchanged copy and reload both replica detail and unhealthy list from the first page.
- On `409`, show a conflict message such as `No safe leader candidate` and keep the row visible.

## Testing

Backend tests:

- `internal/runtime/channelmeta`
  - target already leader returns unchanged.
  - target outside replicas returns the target-not-replica error.
  - target outside ISR returns the target-not-ISR error.
  - inactive channel returns inactive error.
  - only the requested target is evaluated.
  - successful transfer increments `LeaderEpoch`, renews `LeaseUntilMS`, preserves replicas/ISR/MinISR/status/features/retention/channel epoch, rereads authoritative metadata, and applies it locally.
  - unsafe target returns `channel.ErrNoSafeChannelLeader`.
- `internal/access/node`
  - transfer codec roundtrip.
  - client follows Slot leader redirect.
  - no-safe-candidate status maps to `channel.ErrNoSafeChannelLeader`.
  - handler returns authoritative transfer result.
- `internal/usecase/management`
  - validates target.
  - rejects target outside replicas/ISR and inactive channels.
  - returns unchanged when target already leads.
  - delegates safe transfer and maps returned metadata to detail.
- `internal/access/manager`
  - route requires `cluster.channel` write permission.
  - JSON success shape and request body mapping.
  - invalid channel type/body/target, not found, conflict, and unavailable mappings.
- `internal/app`
  - adapter passes target to runtime transferer.
  - manager build wires transfer operator when manager is enabled.

Frontend tests:

- API client URL/body/error behavior.
- Replica detail shows transfer button only for active non-leader ISR rows.
- Transfer success refreshes detail and unhealthy list.
- 409 conflict renders non-destructive feedback.
- Non-ISR or current leader rows do not show transfer.

Verification commands:

```bash
GOWORK=off go test ./internal/runtime/channelmeta ./internal/access/node ./internal/usecase/management ./internal/access/manager ./internal/app -run 'Test(ChannelLeaderTransfer|TransferChannelCluster|ManagerChannelClusterLeaderTransfer|NewBuildsOptionalManagerServerWhenConfigured)' -count=1
GOWORK=off go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
GOWORK=off go test ./internal/runtime/channelmeta ./internal/access/node -count=1
cd web && bun run test src/lib/manager-api.test.ts src/pages/channel-cluster/unhealthy/page.test.tsx
cd web && bun run build
```

## Rollout Notes

- P0.6 is safe to ship without batch leader drain because the API is single-channel and target explicit.
- Existing repair behavior remains unchanged.
- Future batch leader drain should call this safe transfer primitive per channel and add operation history/limits instead of bypassing safety checks.
