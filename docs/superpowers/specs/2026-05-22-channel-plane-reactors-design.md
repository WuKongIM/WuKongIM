# Channel Plane Multiple Reactors Design

**Date:** 2026-05-22
**Status:** Proposed

## Overview

The current send path has a clear gateway and storage foundation, but the boundary between business logic and cluster/channel ownership is blurred. A durable send currently crosses several places that each know part of channel routing:

- `internal/usecase/message/send.go` prepares sends and batches by channel.
- `internal/usecase/message/retry.go` refreshes channel metadata, chooses local versus remote append, and retries on stale routing errors.
- `internal/runtime/channelmeta` reads, caches, bootstraps, renews, and repairs `ChannelRuntimeMeta`.
- `internal/app/channelcluster.go` wraps `pkg/channel` but also forwards appends to a remote leader after local append errors.
- `internal/access/node/channel_append_rpc.go` accepts remote appends, refreshes metadata again, and redirects again.
- `pkg/slot/proxy` and `pkg/cluster` own authoritative slot metadata and slot leader routing.
- `pkg/channel` owns channel log append, idempotency, group commit, and replication.

This makes the hot path hard to reason about. The same send can trigger route refresh, forwarding, redirection, and retry in multiple layers. It also limits high-concurrency behavior because routing, backpressure, and remote batching are not owned by one data-plane component.

This design introduces an event-driven **Channel Plane** implemented with **Multiple Reactors**. A channel is the scheduling and state carrier. Each reactor owns many `ChannelCell` state machines, and each `ChannelCell` owns one channel's route, pending appends, inflight effects, retry state, lease/fence state, and leader hints. External APIs remain synchronous so client `SENDACK` semantics do not change; internally, appends become commands processed by reactors and completed through futures.

## Goals

- Make `internal/usecase/message` independent from slot leader, channel leader, remote append, and route refresh details.
- Move all durable channel append routing into one explicit `internal/runtime/channelplane` subsystem.
- Use multiple reactors keyed by channel to preserve same-channel ordering while allowing cross-channel concurrency.
- Keep reactor event loops non-blocking; all I/O, RPC, and local append waits run as bounded effects that report completion back to reactors.
- Support high concurrency with bounded queues, per-channel fairness, peer-lane batching, typed backpressure, and route singleflight.
- Preserve current durable send semantics: `SENDACK success` still means append completed according to `CommitMode`.
- Preserve cluster-only semantics. A single-node deployment remains a single-node cluster.
- Keep `pkg/channel` as the channel log execution layer and `pkg/slot` / `pkg/cluster` as authoritative metadata and slot Raft layers.
- Remove duplicate forwarding and metadata refresh logic from `message`, `appChannelCluster`, and node append RPC adapters.
- Keep `Fetch` and `Status` on the existing path for the first implementation slice unless the plane is explicitly extended to cover them.

## Non-Goals

- No client protocol change for this design.
- No change to the channel log storage format or idempotency semantics.
- No change to the meaning of quorum/local `CommitMode`.
- No global, generic event bus for all business modules.
- No full asynchronous send acknowledgement. Queue admission alone is not success.
- No bypass path for single-node deployments.
- No immediate rewrite of channel replication internals in `pkg/channel`.
- No mixed-version RPC compatibility. The project is not yet online, so the new node RPC can replace the old channel append RPC.

## Current Problems

### 1. Routing responsibility is scattered

`message/retry.go` refreshes metadata and decides local versus remote append. `appChannelCluster.AppendBatch` repeats a remote-forward fallback when local append fails. The node append RPC handler can refresh metadata again and redirect again. This makes the true owner of channel write routing unclear.

### 2. The hot path can stampede authoritative metadata

The current resolver has cache and singleflight primitives, but write routing is still invoked through synchronous call chains. Under cold-channel or stale-route load, many goroutines can pile into route refresh and remote append paths. The intended backpressure is spread across gateway async dispatch, context timeout, RPC timeout, and append timeout rather than being centralized.

### 3. Remote writes cannot naturally batch across channels

Gateway and message usecase already batch adjacent sends for the same channel. Remote channel append RPC is a single-channel batch. There is no component that owns a per-peer queue and can combine many channels' append batches into one `AppendBatches` RPC.

### 4. Route state is not owned by the channel

Leader epoch, channel epoch, lease, fence, and retry hints are channel-specific state, but they are currently passed through call frames and caches. A channel-centric reactor can make route state explicit and easier to invalidate, refresh, and observe.

### 5. Side effects are coupled to send orchestration

After append success, `message.App` submits committed messages to dispatchers. That can remain initially, but the architecture should allow committed side effects to become event subscribers later without blocking the channel write core.

### 6. Transitional compatibility must be explicit

The migration must not create a phase where follower-gateway sends or remote channel leaders have no append path. The design therefore needs an explicit compatibility remote-owner adapter until the new peer-reactor RPC path lands.

## Approaches Considered

### Approach A: Keep the current synchronous path and clean up retry code

Move more logic into `message/retry.go`, remove some duplicate fallback logic, and improve error typing.

Pros:

- Lowest migration cost.
- Fewest new moving parts.

Cons:

- Business remains aware of channel leader routing.
- Remote batching and centralized backpressure remain awkward.
- The next leader-transfer or migration feature is likely to add routing logic back into multiple places.

This approach is rejected.

### Approach B: Synchronous Channel Plane facade

Introduce `internal/runtime/channelplane` with `AppendBatch(ctx, req)`, but implement it as a direct synchronous router: resolve route, append local/remote, retry.

Pros:

- Clearer boundary than the current code.
- Easier to implement and debug than reactors.
- Good intermediate step if the reactor rollout is too risky.

Cons:

- Backpressure still relies on caller goroutines and downstream queues.
- Remote batching across channels requires additional worker queues anyway.
- Hot-channel fairness and per-channel state are less explicit.

This approach is a viable fallback, but not the recommended final design.

### Approach C: Channel Plane with Multiple Reactors (Recommended)

Use a synchronous external API backed by channel-keyed reactors. Each reactor owns many `ChannelCell` state machines. Effects perform blocking work and report typed completion events back to the owning reactor. Peer reactors own per-remote-node batching.

Pros:

- Makes channel the natural state and scheduling carrier.
- Preserves same-channel ordering and enables cross-channel concurrency.
- Centralizes route, retry, remote append, and backpressure in one subsystem.
- Enables peer-level remote batching without leaking transport details into business.
- Provides clear observability and high-concurrency control points.

Cons:

- Higher implementation and testing cost.
- Requires strict queue, future, cancellation, shutdown, and tracing discipline.
- Tail latency can worsen if mailbox scheduling or batch windows are too large.

This is the recommended direction.

## Design Principles

### 1. Synchronous shell, event-driven core

`ChannelPlane.AppendBatch(ctx, req)` waits for a future and returns a result. Internally it submits `AppendRequested` to the owning reactor. The client-visible sendack semantic remains synchronous and durable.

### 2. Channel is the carrier

A channel's route, pending commands, inflight append effect, retry state, and route invalidation state live in one `ChannelCell`. Request handlers do not carry this state manually through a deep call chain.

### 3. Reactors do not block on I/O

A reactor event loop only mutates state, schedules work, and completes futures. Slot reads, leader repair, local append waits, and remote RPCs run in bounded effect executors or peer reactors.

### 4. Backpressure is explicit and typed

Every queue is bounded by count and bytes. Full queues return typed errors such as `ErrPlaneOverloaded`, `ErrChannelBackpressured`, or `ErrPeerBackpressured`, which gateway can map to clear sendack reasons or connection policy.

### 5. Slot metadata remains authoritative

`pkg/slot/meta` remains the source of truth for `ChannelRuntimeMeta`. Channel Plane caches routes but must refresh or fail closed on stale epochs, expired leases, write fences, dead/draining leaders, and migration fences.

### 6. App wiring stays in the composition root

`internal/app` wires stores, cluster APIs, local channel log, node RPC clients, and metrics into Channel Plane. It does not implement routing policy.

## Proposed Architecture

```text
gateway
  -> internal/access/gateway
  -> internal/usecase/message
  -> internal/runtime/channelplane
       ChannelReactors + ChannelCells
       PeerReactors
       RouteResolver
       PlacementPolicy
  -> pkg/channel
       handler + runtime + replica + store
  -> pkg/slot/proxy + pkg/cluster
       authoritative metadata + slot Raft + transport
  -> db
```

### Package layout

```text
internal/runtime/channelplane/
  plane.go              public facade and lifecycle
  options.go            dependency and tuning options
  command.go            append/fetch/status commands
  event.go              reactor event types
  future.go             request futures and cancellation helpers
  reactor.go            channel reactor event loop
  channel_cell.go       per-channel state machine
  scheduler.go          per-reactor fair active-channel scheduling
  route.go              route model, resolve policy, invalidation rules
  resolver.go           authoritative route read/bootstrap/repair effects
  placement.go          initial channel leader selection policy
  owner.go              local owner append effect interface
  peer_reactor.go       remote peer lanes and batch RPC aggregation
  peer_rpc.go           neutral RPC DTOs used by access/node adapter
  errors.go             typed plane errors
  metrics.go            observer interfaces
  tracing.go            sendtrace/diagnostics stage helpers
```

`internal/access/node` keeps only transport adapters:

```text
internal/access/node/channel_plane_rpc.go
internal/access/node/channel_plane_codec.go
```

The old `internal/access/node/channel_append_rpc.go` can be removed after the new RPC is wired.

### Public facade

```go
type Plane interface {
    Start() error
    Stop(context.Context) error
    AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error)
    Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
    Status(ctx context.Context, id channel.ChannelID) (ChannelStatus, error)
}
```

The first rollout only requires `AppendBatch`. `Fetch` and `Status` can either remain on existing services or be thin wrappers until the read path is migrated.

### Append request model

The plane request should include message usecase metadata needed for result mapping and committed side effects, but it must not know business permission rules.

```go
type AppendBatchRequest struct {
    ChannelID             channel.ChannelID
    Messages              []channel.Message
    SupportsMessageSeqU64 bool
    CommitMode            channel.CommitMode
    TraceID               string
    SenderSessionID       uint64
    RequestSubscribers    []string
}

type AppendBatchResult struct {
    Items []channel.AppendBatchItemResult
}
```

`message.App` remains responsible for:

- authenticated sender validation
- user send limit
- personal/agent channel normalization
- send permission checks
- plugin before-send hook
- durable message construction
- committed side-effect submission in the first rollout

`message.App` no longer depends on:

- `MetaRefresher`
- `RemoteAppender`
- `ChannelCluster` as a routing primitive

It depends on `channelplane.Plane` or a narrow `ChannelAppender` interface.

### Channel route model

```go
type ChannelRoute struct {
    ChannelID    channel.ChannelID
    Key          channel.ChannelKey
    SlotID       multiraft.SlotID
    HashSlot     uint16
    Leader       uint64
    ChannelEpoch uint64
    LeaderEpoch  uint64
    LeaseUntil   time.Time
    Replicas     []uint64
    ISR          []uint64
    MinISR       int
    Status       channel.Status
    Features     channel.Features
    RetentionThroughSeq uint64
    WriteFence   channel.WriteFence
}
```

A route is reusable for writes only when all of these are true:

- status is active
- leader is non-zero
- channel epoch and leader epoch are non-zero
- lease is valid beyond a configurable write lead time
- write fence is empty
- leader is not known dead or draining
- route generation matches the current cache entry

### ChannelCell

A `ChannelCell` owns one channel's write state inside a reactor.

```go
type ChannelCell struct {
    Key       channel.ChannelKey
    ID        channel.ChannelID
    Route     ChannelRoute
    RouteOK   bool

    Pending   AppendQueue
    Inflight  int
    Resolving bool
    Closed    bool

    Retry     RetryState
    LastError error
}
```

Responsibilities:

- accept append commands for its channel
- coalesce adjacent compatible append commands into one local/remote append batch
- trigger route resolve when route is missing, stale, expired, or fenced
- issue local append effect when local node is the channel leader
- issue remote append through peer reactor when another node is the channel leader
- handle typed append failures and decide whether to retry, refresh route, or complete futures with error
- complete futures in request order
- enforce per-channel pending count, pending bytes, and inflight limits

### ChannelReactors

A fixed number of reactors is created at startup. Each channel key maps to exactly one reactor:

```go
reactorID := hash(channelKey) % reactorCount
```

A reactor has:

- an MPSC inbox for incoming events
- a map of `ChannelCell` instances
- an active ring for fair scheduling across hot channels
- a bounded effect submission interface
- shutdown state that completes all pending futures

The event loop performs:

1. drain a bounded number of inbox events
2. enqueue append commands into `ChannelCell` pending queues
3. run active-channel round-robin with per-cell operation and byte budgets
4. submit effects without waiting for them
5. process completion events and update cells

This prevents a single hot channel from monopolizing the whole reactor.

### Effects

Effects are blocking or potentially slow work initiated by reactors.

```text
ResolveRouteEffect
  Reads authoritative route, bootstraps missing metadata, repairs unhealthy leader,
  renews local leader lease when appropriate, and applies route metadata locally.

LocalAppendEffect
  Calls local owner append over pkg/channel with expected channel and leader epochs.

RemoteAppendEffect
  Submitted to PeerReactor; sends append to remote channel leader and returns typed result.

RepairLeaderEffect
  Runs slot-authoritative channel leader repair when route validation requires it.
```

Effects always report completion back to the owning channel reactor. They must never mutate `ChannelCell` state directly.

### PeerReactors

Peer reactors own outbound writes to remote channel leaders. They are keyed by target node and lane.

```text
PeerReactor(node-2, lane-0)
  accepts RemoteAppendEffect from many ChannelReactors
  aggregates by short max wait / max records / max bytes
  sends AppendBatches RPC
  splits results back to original ChannelReactors
```

Peer reactor queues are bounded by request count and payload bytes. When full, they return `ErrPeerBackpressured` to the originating ChannelCell.

### Node RPC protocol

Replace single-channel append RPC with a typed batch protocol:

```go
type AppendBatchesRequest struct {
    Batches []AppendBatchEnvelope
}

type AppendBatchEnvelope struct {
    RouteEpoch RouteEpoch
    Request    channel.AppendBatchRequest
}

type AppendBatchesResponse struct {
    Results []AppendBatchRemoteResult
}

type AppendBatchRemoteResult struct {
    Status string
    LeaderHint uint64
    ChannelEpoch uint64
    LeaderEpoch uint64
    Result channel.AppendBatchResult
}
```

Statuses:

```text
ok
not_leader
stale_route
lease_expired
write_fenced
not_ready
backpressure
invalid
```

The node adapter does not perform multi-step route refresh. It validates local owner state and either appends locally or returns a typed hint. The originating ChannelCell owns refresh and retry decisions.

During the first implementation slices, Channel Plane may use an explicit compatibility remote-owner adapter that reuses the existing single-channel append RPC. That adapter is a temporary bridge so Phase 2 can cover remote leaders before the new peer-reactor RPC is introduced in Phase 3.

### Local owner append

`appChannelCluster` becomes a local owner facade only:

- keep `ApplyRoutingMeta`
- keep `EnsureLocalRuntime`
- keep `RemoveLocalRuntime`
- keep local `AppendBatch`
- remove remote forwarding from `AppendBatch`
- remove `remoteAppender` from this type

Local append validates expected channel and leader epochs through `pkg/channel/handler.AppendBatch` as today. Stale or not-leader results become typed completion events handled by the ChannelCell.

### Route resolver and placement

The resolver uses `pkg/slot/proxy` and `pkg/cluster` but hides them from business code.

Resolve behavior:

1. calculate channel key, hash slot, and physical slot
2. read authoritative `ChannelRuntimeMeta` from the slot leader
3. if not found on a business write, bootstrap a new record through slot authority
4. validate leader liveness, lease, ISR membership, status, and write fence
5. if invalid and repairable, ask slot authority to repair leader metadata
6. project the authoritative record into `ChannelRoute`
7. apply routing metadata locally; materialize local runtime only when this node is in replicas and hot-path policy requires it

Initial placement must not blindly select slot leader for every channel. The first rollout can use deterministic rendezvous hashing over healthy slot replicas, but only while the compatibility remote-owner adapter exists:

```text
leader = highestScore(channelKey, candidateReplica, nodeHealth, optionalLoadHint)
```

The slot leader remains the authority that persists metadata, but the chosen channel leader may be any healthy replica in the slot assignment.

## Detailed Append Flow

### Gateway and message usecase

```text
1. gateway core decodes SEND frames and forms microbatches by channel key
2. access/gateway maps frames to message.SendCommand
3. message.App validates sender, rate limit, permission, hook, and channel normalization
4. message.App builds channel.Message values
5. message.App calls channelplane.AppendBatch(ctx, req)
```

### Channel reactor

```text
6. plane hashes channel key to reactor
7. AppendRequested enters reactor inbox
8. reactor creates or loads ChannelCell
9. ChannelCell queues the append command and becomes active
10. scheduler picks ChannelCell within per-cycle budget
11. if route is missing or unhealthy, ChannelCell submits ResolveRouteEffect
12. after RouteResolved, ChannelCell resumes scheduling
13. if leader is local, ChannelCell submits LocalAppendEffect
14. if leader is remote, ChannelCell submits RemoteAppendEffect through PeerReactor
15. append completion returns to owning reactor
16. ChannelCell completes futures in request order
```

### Message completion

```text
17. message.App maps append results to SendResult
18. message.App submits committed dispatcher for successful items
19. access/gateway writes sendack
```

## Error and Retry Semantics

Retry is owned by `ChannelCell`.

Retryable errors:

- `not_leader`
- `stale_route`
- `lease_expired`
- route generation changed during effect
- cluster reroute / slot not leader during authoritative route read

Fail-closed errors:

- active write fence
- channel deleting/deleted
- protocol upgrade required
- idempotency conflict
- invalid channel metadata
- no safe channel leader after repair budget

Backpressure errors:

- plane inbox full
- channel pending queue full
- channel pending bytes full
- effect executor full
- peer reactor queue full

Retry policy:

- one immediate refresh retry for stale/not-leader/lease-expired append failures
- bounded total retry budget tied to caller context deadline
- no unbounded requeue loops
- retry increments diagnostics attempt but does not alter persisted message data

## Backpressure Model

Required limits:

```text
PlaneMaxPendingCommands
PlaneMaxPendingBytes
ReactorInboxSize
ReactorMaxCells
ChannelMaxPendingCommands
ChannelMaxPendingBytes
ChannelMaxInflightAppends
RouteResolveMaxInflight
EffectWorkerQueueSize
PeerQueueMaxCommands
PeerQueueMaxBytes
PeerBatchMaxRecords
PeerBatchMaxBytes
PeerBatchMaxWait
```

All limits must have metrics and typed errors. The gateway maps expected overload errors to a controlled sendack reason rather than surfacing an opaque system error.

## Ordering Model

- Same channel: append commands are completed in channel order.
- Same channel batches: coalescing must preserve request item order.
- Cross channel: no ordering guarantee.
- Remote peer batching: batching across channels must preserve ordering only within each channel envelope.
- Retried commands remain associated with their original futures and original request order.

## Cancellation and Shutdown

Cancellation states:

- Before command is accepted by reactor: return context error.
- Queued but not started: remove or skip command and complete future with context error.
- Effect already submitted: do not assume append can be canceled. Wait for effect or complete caller on context deadline while allowing effect completion to clean reactor state.
- Append committed but sendack write fails: rely on idempotency via `FromUID + ClientMsgNo` for client retry safety.

Shutdown behavior:

- stop accepting new commands
- drain or fail pending commands according to shutdown timeout
- stop peer reactors
- complete all remaining futures with `ErrPlaneClosed`
- do not leak goroutines or futures

## Observability

Metrics:

- reactor inbox depth and drops
- active cell count per reactor
- channel pending depth and bytes
- channel schedule wait duration
- route cache hit/miss/refresh/repair/bootstrap
- route resolve duration
- local append duration and result
- peer queue depth and bytes
- peer batch size, bytes, wait, RPC duration, result
- retry count by reason
- backpressure count by limit
- future wait duration

Tracing stages:

```text
gateway_messages_send
channelplane_enqueue
channelplane_route_resolve
channelplane_local_append
channelplane_remote_enqueue
channelplane_remote_rpc
channelplane_future_complete
```

Diagnostics events should include channel key, route epochs, leader, peer node, attempt, batch record count, and result code.

Append failure reporting must use one typed surface for both admission failures and post-admission effect failures so gateway mapping stays simple:

- admission failures: backpressure, invalid request, closed plane
- effect failures: not leader, stale route, lease expired, write fenced, remote RPC error

The plane can return typed errors directly, and gateway can map those errors to sendack reasons in one place.

## Migration Plan

### Phase 1: Add Channel Plane beside existing path

- Add `internal/runtime/channelplane` with reactor/future skeleton.
- Implement local append effect over current `appChannelCluster` local append.
- Add route resolver using current `channelMetaSync` / store primitives.
- Add a temporary compatibility remote-owner adapter that reuses the existing single-channel remote append RPC so remote leaders remain writable before peer reactors land.
- Add tests for reactor enqueue, ordering, future completion, timeout, and local append.
- Keep existing message path unchanged until the plane is verified.

### Phase 2: Move message durable send to Channel Plane

- Replace `message.Options.Cluster`, `MetaRefresher`, and `RemoteAppender` for durable send with `ChannelAppender`.
- Move logic from `message/retry.go` into Channel Plane and remove the old retry helper.
- Keep the compatibility remote-owner adapter in place so follower-gateway sends and remote leaders continue to work before the new peer-reactor RPC exists.
- Preserve message permission, hook, and committed dispatcher behavior.
- Add unit tests for message sending through a fake plane.

### Phase 3: Replace remote append RPC

- Add `AppendBatches` node RPC and binary codec.
- Add `PeerReactor` and remote append effect.
- Remove the temporary compatibility remote-owner adapter after peer reactors are stable.
- Remove remote forwarding from `appChannelCluster`.
- Remove or deprecate old `channel_append_rpc.go`.
- Add multi-node tests for follower gateway send, stale route retry, leader hint retry, peer backpressure, and batch RPC result alignment.

### Phase 4: Move route ownership fully into Channel Plane

- Split or wrap current `channelmeta` so write route resolution is owned by Channel Plane.
- Keep runtime activation interfaces for `pkg/channel` replication ingress.
- Ensure FLOW docs describe the new boundary.
- Update `internal/FLOW.md`, `internal/runtime/channelmeta/FLOW.md`, and add `internal/runtime/channelplane/FLOW.md` when code lands.

### Phase 5: Event-driven committed side effects

- Publish committed events from Channel Plane or from a narrow committed event bridge after future success.
- Gradually move delivery, conversation, CMD, and plugin side effects to subscribers.
- Keep sendack success tied to append completion, not side-effect completion, unless a request-scoped command explicitly requires side-effect success.

### Config updates

- Any new reactor, queue, batch, or timeout knob must use the repository `WK_` config prefix convention.
- Whenever a new configuration field is introduced, update `wukongim.conf.example` in the same change.

## Testing Strategy

Unit tests:

- reactor maps same channel to same reactor
- same-channel commands complete in order
- cross-channel commands do not block each other under fair scheduling
- route singleflight coalesces concurrent cold-channel misses
- route invalidation forces refresh before retry
- local append effect returns aligned batch results
- remote peer reactor batches multiple channel envelopes
- typed remote statuses map to retry/fail/backpressure decisions
- context timeout releases futures
- shutdown completes pending futures

Integration tests:

- single-node cluster first send bootstraps channel runtime meta
- three-node follower gateway sends to remote channel leader
- stale local route retries after leader hint
- expired leader lease refreshes and succeeds
- write fence fails closed
- dead/draining channel leader repairs through slot authority
- peer queue backpressure maps to sendack reason
- idempotent client retry after sendack write failure returns same message seq

Performance tests:

- send stress throughput preset before and after migration
- cold-channel storm with bounded route resolves
- hot-channel fairness under mixed hot/cold workload
- remote peer batching efficiency and p95/p99 guardrails
- memory budget under large payload queue pressure

## Open Decisions for Implementation Planning

These are implementation details, not blockers for the architecture:

- default reactor count: likely `max(4, GOMAXPROCS)` with config override
- peer lane count per node: likely tied to transport pool size
- initial batch wait window: start conservative, around 100 microseconds to 1 millisecond
- exact sendack reason for plane backpressure: map to rate limit or system busy depending protocol support
- whether Phase 1 resolver wraps `channelMetaSync` directly or introduces `channelplane.Directory` immediately

## Expected Result

After the migration, the durable send path has one owner for channel write routing:

```text
message.App -> channelplane -> pkg/channel / node RPC -> pkg/slot/pkg/cluster
```

Business code no longer knows how to find channel leaders. `appChannelCluster` no longer forwards. Node append RPC no longer performs multi-step refresh. Channel-specific state is explicit inside `ChannelCell`, and high-concurrency behavior is controlled through reactors, peer lanes, bounded queues, and typed errors.
