# internalv2 Adaptive Partitioned Delivery Runtime Design

## Goal

Upgrade `internalv2` online delivery from a synchronous fanout facade into an
adaptive partitioned runtime:

```text
small/person/scoped delivery -> memory fast path
large/hot channel delivery   -> partitioned fanout job path
blocking effects             -> typed bounded executors
owner-node delivery          -> per-owner lanes and owner-local write shards
```

The design keeps `SENDACK` tied only to durable ChannelV2 commit. Delivery is an
asynchronous side effect and must not add a business branch that bypasses
cluster semantics. A single-node deployment remains a single-node cluster.

## Why Not Plain Multiple Reactors Plus Goroutine Pool

Multiple reactors plus a goroutine pool is a good execution base, but it is not
enough for high-performance IM delivery by itself. A plain implementation can
still scan all subscribers for every group message, flood presence lookups, let
one slow owner node block unrelated pushes, and keep large channel fanout as one
huge in-memory task.

The runtime should instead reduce fanout work before executing it, then schedule
the remaining work fairly:

- Prefer online route indexes or delivery tags over full subscriber scans.
- Split fanout by UID authority partition.
- Split push handoff by owner node lane.
- Keep all slow I/O out of reactor goroutines.
- Preserve fanout cursor shape so large-channel work can become durable later.

## Current Contracts To Preserve

- `message.App.SendBatch` appends through `clusterv2` / `channelv2` and emits
  `MessageCommitted` only after durable success.
- `SENDACK` latency must not wait for subscriber scan, presence resolution,
  owner push, session write, or recvack.
- Person-channel committed events are scoped to the two channel participants.
- Request-scoped delivery uses `MessageScopedUIDs` and bypasses subscriber scan.
- Non-person channel delivery can page subscribers through
  `runtime/delivery.ChannelSubscriberSource`.
- Presence and owner push stay behind small ports so `runtime/delivery` remains
  independent from gateway, app, and concrete cluster packages.
- Recipient owner nodes own pending recvack state because they own the real
  gateway sessions.

## High-Level Flow

Example: `g1` has 100,000 members, 12,000 online users, and a message with
`MessageSeq=90001`.

```text
gateway SEND on N1
  -> message.SendBatch
  -> clusterv2 routes append to g1 ChannelV2 leader
  -> quorum commit assigns MessageSeq=90001
  -> SENDACK is written to sender
  -> MessageCommitted enters delivery

Delivery dispatcher
  -> classifies g1 as large/hot
  -> CoordinatorReactor creates or resumes fanout plan
  -> FanoutTask per UID authority partition

FanoutReactor for each partition
  -> reads one online-index or delivery-tag page
  -> resolves UIDs through PresenceExecutor
  -> groups online routes by OwnerNodeID
  -> enqueues owner batches to owner lanes

Owner lane
  -> local owner: OwnerReactor + SessionWriteExecutor
  -> remote owner: PushRPCExecutor to access/node Delivery Push RPC

Recipient owner node
  -> validates route fences
  -> binds pending recvack
  -> writes RecvPacket
  -> clears pending state on client Recvack or session close
```

## Package Boundaries

`internalv2/usecase/delivery`
: Keeps the entry-agnostic usecase boundary. It accepts committed events and
  feedback commands. It should not expose reactor internals.

`internalv2/runtime/delivery`
: Owns adaptive delivery runtime primitives: coordinator, reactors, schedulers,
  fanout cells, retry state, owner lanes, typed executor interfaces, ack tracker,
  and no-cluster benchmarks. It must not import gateway, app, `pkg/clusterv2`,
  or `pkg/channelv2`.

`internalv2/infra/cluster`
: Adapts UID hash-slot route snapshots, remote fanout, and owner push to
  `pkg/clusterv2` / access-node RPC.

`internalv2/access/node`
: Keeps deterministic binary RPC codecs for delivery fanout and owner push.
  It should not decide fanout policy, retry policy, or session mutation rules.

`internalv2/app`
: The only composition root. It wires config, observers, cluster adapters,
  delivery runtime, async committed sink, gateway feedback, and lifecycle.

## Runtime Components

### DeliveryManager

`DeliveryManager` remains the runtime facade consumed by the usecase adapter.
`SubmitCommitted` clones the event, performs bounded admission, and returns.
It no longer runs all fanout tasks synchronously.

```text
SubmitCommitted
  -> classify envelope
  -> enqueue CoordinatorCommand
  -> return ok / queue_full / closed
```

`Recvack`, `SessionClosed`, `BindPendingAck`, and `ExpirePendingAcks` continue
to work through owner-local ack state.

### CoordinatorReactors

Coordinator reactors own message-level fanout planning. They are keyed by
channel key so hot channels keep ordered planning without blocking other
channels.

Responsibilities:

- classify delivery as fast path or job path;
- resolve current delivery partitions;
- create `FanoutJob` / `FanoutTask` records in memory;
- route local tasks to fanout reactors;
- route remote authority tasks through fanout RPC lanes;
- observe planning failures and retryable route-not-ready errors.

They do not scan subscribers, resolve presence, call node RPC, or write
gateway sessions.

### FanoutReactors

Fanout reactors own partition-local progress. A fanout task is routed by the
stable delivery task key `(ChannelID, ChannelType, Partition.ID)`, hashed across
the local fanout reactor group.

Each reactor keeps a fair scheduler of `fanoutCell` values. A cell represents
one message partition:

```text
FanoutCell
  Envelope
  Partition
  Cursor
  Attempt
  State
  InflightEffect
  RetryDueAt
  Stats
```

The reactor advances a bounded amount per turn, such as one subscriber page or
one owner-batch dispatch. This prevents one large group from monopolizing the
reactor.

### Owner Lanes

Owner lanes isolate push pressure by recipient owner node.

```text
OwnerNodeID -> bounded lane queue -> bounded inflight RPC/write work
```

A slow remote owner consumes only its own lane budget. Other owner nodes and
local owner writes continue.

Local owner work enters owner reactors or write shards. Remote owner work enters
`PushRPCExecutor` and uses access-node Delivery Push RPC.

### Owner Reactors Or Write Shards

Owner-local delivery validates route fences and owns pending recvack mutation.
The hot operation is session write, so actual writes should run through
`SessionWriteExecutor` instead of blocking the reactor.

```text
OwnerPushCommand
  -> validate route against online.Registry
  -> bind PendingRecvAck if budget allows
  -> submit SessionWriteTask
  -> write result returns accepted / retryable / dropped
```

If route validation fails, the route is dropped. If session write fails with a
transient pressure class, the route is retryable.

## Typed Bounded Executors

Use typed bounded executors instead of one generic goroutine pool:

```text
PlannerExecutor      route snapshots and delivery plan cache refresh
SubscriberExecutor   subscriber or delivery-tag page reads
PresenceExecutor     EndpointsByUIDs batch resolution
FanoutRPCExecutor    remote authority fanout RPC
PushRPCExecutor      remote owner push RPC
SessionWriteExecutor owner-local gateway session writes
```

Each executor has independent worker and queue budgets. This prevents, for
example, slow session writes from starving presence resolution.

Executor results return to the owning reactor with a fence:

```text
JobID / TaskID
PartitionID
Generation
Cursor
Attempt
EffectKind
```

The reactor applies a completion only when the fence still matches the current
cell.

## Adaptive Path Selection

The runtime should support three paths.

### Fast Scoped Path

Used for:

- person channels after app scopes the two UIDs;
- messages with `MessageScopedUIDs`;
- small explicit UID batches.

Flow:

```text
Envelope.MessageScopedUIDs
  -> one FanoutTask
  -> PresenceExecutor
  -> owner lanes
```

No subscriber scan and no delivery tag lookup.

### Normal Paged Path

Used for medium channels or when no online index/tag exists.

Flow:

```text
SubscriberExecutor.NextPartitionPage
  -> PresenceExecutor
  -> owner lanes
  -> cursor advances in memory
```

This is the current `ChannelSubscriberSource` model, but executed through
reactors and typed executors.

### Large/Hot Job Path

Used when channel subscriber count or online count crosses configured
thresholds. The first implementation uses config thresholds only so behavior is
deterministic; later versions may also use observed fanout latency, queue depth,
or message rate.

Flow:

```text
DeliveryTag or OnlineIndex page
  -> FanoutJob{Envelope, Partition, Cursor, Attempt}
  -> fair partition scheduler
  -> owner lanes
```

P1 can keep jobs in memory. The data shape must remain serializable so P2 can
persist job cursor and retry state.

## Delivery Tags And Online Indexes

The best large-channel path should avoid scanning offline members for every
message.

Preferred future source:

```text
channel + subscriber_version + topology_version
  -> delivery tag partitions
  -> UID pages by partition
```

Best realtime source when available:

```text
channel -> online UID or online route index
```

P1 does not need to build the full persistent tag/index system. It must,
however, keep the runtime source port flexible:

```go
type FanoutSource interface {
    Begin(ctx context.Context, env Envelope) (FanoutPlan, error)
    NextPage(ctx context.Context, req FanoutPageRequest) (UIDPage, error)
}
```

Existing `ChannelSubscriberSource` can adapt to this interface. Future delivery
tags and online indexes can implement it without changing reactor execution.

## Backpressure

Backpressure is stage-specific:

- committed-event admission queue;
- coordinator mailbox;
- fanout reactor mailbox;
- per-cell queued work;
- typed executor queues;
- per-owner lane queue and inflight budget;
- pending recvack per session and per node.

When pressure is high, delivery should degrade realtime fanout rather than
blocking append or SENDACK. Typed errors and observer labels should distinguish:

```text
queue_full
executor_backpressured
owner_lane_full
retry_queue_full
max_attempts
route_not_ready
stale_route
session_missing
session_write_failed
```

## Retry And Failure Handling

Route-not-ready or stale authority:
: retry planning or reroute with bounded backoff.

Subscriber page failure:
: retry the current cell cursor. Do not block unrelated cells.

Presence failure:
: retry the current page. If the failure is non-retryable, drop the page and
  observe the error.

Remote authority fanout failure:
: retry the fanout task through the owner partition's routing path.

Remote owner push failure:
: classify all routes in that owner batch as retryable.

Local route fence mismatch or missing session:
: drop the exact route.

Session write transient failure:
: mark the exact route retryable and re-enter owner lane after backoff.

Recvack missing:
: expire owner-local pending ack after TTL. Missing recvack does not trigger
  cross-node fanout retries.

## Ordering Semantics

The runtime preserves durable ordering facts but does not guarantee online
arrival order:

- ChannelV2 assigns `MessageSeq`.
- `SENDACK` returns the committed `MessageSeq`.
- every `RecvPacket` includes `MessageSeq`.
- clients use `MessageSeq` to order and detect gaps.

Fanout partitions, owner lanes, and session writes may complete out of order.
This is acceptable for realtime delivery. Gap recovery and message sync remain
the correctness mechanism.

## Configuration

Add delivery execution settings under `internalv2/app.DeliveryConfig` only when
implementation needs them. Keep defaults conservative and avoid exposing every
internal knob at once.

Useful first knobs:

- `ReactorCount`
- `CoordinatorMailboxSize`
- `FanoutMailboxSize`
- `ExecutorQueueSize`
- `SubscriberWorkers`
- `PresenceWorkers`
- `PushRPCWorkers`
- `SessionWriteWorkers`
- `OwnerLaneQueueSize`
- `OwnerLaneInflight`
- `LargeChannelSubscriberThreshold`
- `LargeChannelOnlineThreshold`
- `FanoutPageSize`
- `PushBatchSize`
- `RetryMaxAttempts`
- `RetryBackoff`

Existing `FanoutPageSize`, `PushBatchSize`, `EventQueueSize`,
`PendingAckTTL`, and `PendingAckMaxPerSession` remain valid.

## Observability

Runtime observers stay in `internalv2/runtime/delivery`; Prometheus remains an
app concern.

Required low-cardinality observations:

- committed admission result and queue depth;
- coordinator planning result and duration;
- fanout task result by path: local, remote, job;
- subscriber page result, page size, and duration;
- presence resolve result, UID count, route count, and duration;
- owner lane enqueue result and queue depth;
- push result by owner node label, route count, and duration;
- session write result and duration;
- retry enqueue, attempt, drop, and max-attempts;
- pending ack count.

Do not label metrics with channel IDs, UIDs, session IDs, or message IDs.

## Lifecycle

Start order:

```text
retry/job scheduler
typed executors
coordinator reactors
fanout reactors
owner lanes / owner reactors
async committed sink
```

Stop order:

```text
async committed sink stops admitting and drains
coordinator reactors stop accepting and complete queued work
fanout reactors drain or fail queued cells
owner lanes drain accepted owner batches
typed executors stop after completions are returned or cancelled
retry/job scheduler exits
```

No accepted command should be left without a terminal observation. P1 may drop
in-memory fanout work on process crash; P2 durable jobs should recover from
stored cursors.

## Implementation Slices

### Slice 1: Runtime Execution Skeleton

- Add coordinator/fanout reactor group and typed executor interfaces.
- Keep existing `Planner`, `FanoutWorker`, `FanoutTaskRouter`, and
  `RetryScheduler` behavior behind compatibility adapters.
- Convert `Manager.SubmitCommitted` from synchronous execution to bounded
  admission.
- Add no-cluster unit tests for admission, routing, and stop behavior.

### Slice 2: Owner Lanes And Write Shards

- Split owner push into per-owner lanes.
- Move local session writes through `SessionWriteExecutor`.
- Preserve pending ack semantics.
- Test slow owner isolation and recvack cleanup.

### Slice 3: Adaptive Source Port

- Introduce `FanoutSource` and adapt current `ChannelSubscriberSource`.
- Keep person/scoped fast path.
- Add large-channel path selection but still use in-memory cursor state.

### Slice 4: Large/Hot Channel Job Shape

- Add in-memory `FanoutJob` and per-partition cursor state.
- Add fair scheduling limits per reactor turn.
- Add benchmark scenarios for 100k subscribers with offline-heavy and
  online-heavy distributions.

### Slice 5: Future Durable Job Store

- Persist `FanoutJob` cursor and retry state.
- Recover dirty jobs after process restart.
- Keep this out of the first implementation unless a reliability requirement
  demands it immediately.

## Testing Strategy

Runtime unit tests:

- `SubmitCommitted` is bounded and does not execute blocking effects inline.
- same channel planning is ordered while unrelated channels progress.
- fanout reactor advances large jobs fairly.
- stale executor completions are ignored by fence checks.
- owner lane pressure does not block other owner lanes.
- local route fence mismatch drops exact routes.
- session write retry returns exact retryable routes.
- recvack and session close clear owner-local pending ack state.
- stop/submit races do not hang accepted commands.

App wiring tests:

- delivery enabled wires the adaptive runtime and existing gateway feedback.
- node RPC handlers still register for delivery push and fanout.
- single-node cluster delivery uses the same routing surfaces as multi-node
  clusters.
- delivery disabled preserves existing `SEND -> SENDACK` behavior.

Benchmarks:

```text
go test ./internalv2/runtime/delivery -run '^$' -bench . -benchmem
```

Scenarios:

- scoped 2 UID person delivery;
- 1k subscriber medium group;
- 100k subscribers, 0-1% online;
- 100k subscribers, 50% online;
- 100k subscribers, slow remote owner node;
- hot channel continuous messages;
- recvack-heavy owner-local session churn.

## Non-Goals

- Do not migrate CMD, plugin, conversation, or offline sync in this slice.
- Do not implement full `NoPersist` realtime delivery in this slice.
- Do not make online arrival ordering a runtime guarantee.
- Do not add a new global service layer.
- Do not bypass cluster routing for single-node deployments.
