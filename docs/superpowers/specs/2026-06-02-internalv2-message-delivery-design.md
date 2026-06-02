# internalv2 Message Delivery Design

## Goal

Design an independent high-performance message delivery module for
`internalv2`. The module should deliver committed messages to online sessions,
support local-only benchmarking, and avoid reintroducing a broad service layer.

The chosen architecture is:

```text
UID authority partitioned fanout
+ recipient-owner local recvack
+ MessageSeq-based client gap recovery
```

The channel log still owns durable ordering and `MessageSeq` allocation. Online
delivery does not guarantee arrival order. Clients and sync flows use
`MessageSeq` to sort, detect gaps, and recover missing messages.

Single-node deployment remains a single-node cluster. The design must not add a
business branch that bypasses cluster semantics.

## Scope

P1 includes:

- durable committed-message events to online delivery.
- subscriber partition fanout for person and group channels.
- batch presence lookup through UID authority leaders.
- local and remote owner-node push handoff.
- recipient-owner local pending recvack tracking.
- session close cleanup for owner-local pending recvacks.
- delivery-only unit benchmarks and no-cluster module benchmarks.

P1 does not include:

- offline sync storage writes.
- CMD conversation and offline CMD sync.
- plugin, webhook, or AI hooks.
- full `NoPersist` realtime compatibility.
- guaranteed online arrival ordering.
- durable delivery replay cursor.

Accepted P1 reliability gap:

- if a process crashes after durable append but before delivery work is fully
  handed off, online realtime delivery for that event may rely on client gap
  sync rather than delivery replay.

P2 should add durable delivery replay, richer offline/conversation observers,
and persistent reusable delivery tags when P1 performance data shows the right
partition thresholds.

## Package Boundaries

`internalv2/usecase/delivery`
: Entry-agnostic delivery usecase. It accepts committed-message events and
  exposes delivery feedback commands. It defines ports for subscriber planning,
  presence route resolution, task routing, route pushing, and observation.

`internalv2/runtime/delivery`
: Pure delivery runtime. It owns planners, fanout workers, handoff retry state,
  recipient ack trackers, backpressure, clocks, metrics hooks, and benchmarks.
  It must not import gateway, access, app, `pkg/clusterv2`, or `pkg/channelv2`.

`internalv2/access/gateway`
: Maps gateway frames to delivery feedback only. It handles client `Recvack`
  frames by calling an entry-agnostic delivery usecase. It does not decide
  retry, routing, or business policy.

`internalv2/access/node`
: Exposes node RPCs for fanout tasks, remote owner push batches, optional ack
  summary batches, and delivery capability probes.

`internalv2/infra/cluster`
: Routes fanout tasks and push batches over `pkg/clusterv2` RPC. It adapts UID
  authority route snapshots to delivery partition targets.

`internalv2/app`
: The only composition root. It wires message committed sink, delivery usecase,
  delivery runtime, presence authority client, online registry, gateway handler,
  and node RPC adapters.

## Committed Flow

`message.App` remains responsible for durable append and sendack result
mapping. Delivery is a post-append side effect.

```text
gateway SEND
  -> message.SendBatch
  -> clusterv2/channelv2 append
  -> SendackPacket
  -> MessageCommitted
  -> delivery.SubmitCommitted
```

`SubmitCommitted` must be cheap. It clones only immutable event fields needed by
delivery, enqueues or routes fanout work, and returns without waiting for online
pushes or recvacks. A delivery failure never turns a successful durable append
into a failed sendack.

The `internalv2/contracts/messageevents.MessageCommitted` contract should be
extended for delivery before wiring the module:

```text
SenderSessionID
MessageScopedUIDs
RedDot or delivery flags needed by RecvPacket construction
```

The delivery event must remain a DTO. It should not import gateway, channelv2,
or cluster types.

## Fanout Ownership

Fanout work is partitioned by subscriber UID hash authority instead of channel
owner. This keeps the hottest route lookup close to the in-memory presence
directory.

```text
MessageCommitted(channel=g1, seq=1001)
  -> DeliveryPlanner
  -> subscriber partitions grouped by UID hash-slot authority leader
  -> FanoutTask routed to each authority leader
```

For a three-node cluster:

```text
g1 subscribers: u1..u100000

Task A -> node N1: UID hash slots currently led by N1
Task B -> node N2: UID hash slots currently led by N2
Task C -> node N3: UID hash slots currently led by N3
```

Each task owner handles only the subscribers belonging to its target UID
hash-slot range. It resolves online routes from the local authority directory
when the target leader is local, then groups pushes by recipient owner node.

## Subscriber Planning

The planner supports three sources:

- person channel: derive the two UIDs from the canonical person channel ID.
- store-backed channel: page subscriber UIDs and filter them by task partition.
- message-scoped delivery: use the committed event's one-shot UID list when it
  exists.

P1 should avoid one full subscriber scan per partition. Until persistent
delivery tags exist, use one of these two P1-safe sources:

- a benchmark or test source that can directly page UIDs by partition.
- an ephemeral plan built by scanning the subscriber source once, sharding UIDs
  into bounded partition pages, and then dispatching those partition pages.

P2 should replace hot-channel ephemeral scans with persistent reusable delivery
tags or a partition-aware subscriber index. The port must make room for that:

```go
type SubscriberPlanner interface {
    Begin(ctx context.Context, env Envelope) (PlanToken, error)
    NextPartitionPage(ctx context.Context, token PlanToken, partition Partition, cursor string, limit int) (UIDPage, error)
}
```

The benchmark harness must be able to replace this port with synthetic
subscriber distributions so delivery throughput can be measured without a real
metadata store.

Ephemeral plans are an accepted P1 compromise for correctness and modular
benchmarking. They are not the final high-volume hot-channel path.

## Presence Resolution

Presence resolution is batch-oriented. Group delivery must not perform one RPC
per UID.

```go
type PresenceResolver interface {
    EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]Route, error)
}
```

When a fanout task runs on the UID authority leader, `EndpointsByUIDs` should
read the local `runtime/presence.Directory`. If a task observes stale route
authority, it is rerouted with a bounded retry using the latest cluster route
snapshot.

## Push Handoff

The fanout worker treats remote owner push as a handoff to the node that owns
the real session.

```text
FanoutWorker
  -> group routes by OwnerNodeID
  -> local owner: write local sessions directly
  -> remote owner: PushBatch RPC
  -> accepted routes: handoff complete
  -> retryable routes: schedule handoff retry
  -> dropped routes: stop retrying this exact route
```

Remote `PushBatch` runs on the recipient owner. It validates route fencing
against `runtime/online.Registry`, writes `RecvPacket` to the local gateway
session, and registers pending recvack state locally.

The fanout worker tracks handoff success, not client recvack. This avoids
cross-node ack storms for large online fanout.

## Recvack Ownership

The recipient owner owns client recvack state because it owns the real session.

```text
FanoutWorker on N1
  -> PushBatch to recipient owner N3

Recipient owner N3
  -> writes RecvPacket to session770
  -> records PendingRecvAck(session770, messageID90001)
  -> returns accepted to N1

client u77
  -> Recvack(messageID90001) to N3

N3
  -> clears PendingRecvAck locally
  -> records ack latency
  -> optionally emits async AckSummary
```

`Recvack` does not synchronously call back to the fanout task owner. Optional
ack summaries are batched and used for metrics or future delivery receipts, not
for the realtime push critical path.

Session close is also local:

```text
OnSessionClose(session770)
  -> recipient ack tracker removes pending acks for session770
  -> optional session-close summary is batched for observers
```

This accepts that the fanout worker knows a message was handed to the recipient
owner, not that the client definitely acked it. End-to-end reliability comes
from durable channel log, `MessageSeq`, and client gap sync.

## Data Types

Delivery envelope:

```go
type Envelope struct {
    MessageID       uint64
    MessageSeq      uint64
    ChannelID       string
    ChannelType     uint8
    FromUID         string
    SenderSessionID uint64
    ClientMsgNo     string
    Payload         []byte
    MessageScopedUIDs []string
}
```

Fanout task:

```go
type FanoutTask struct {
    Envelope      Envelope
    Partition     Partition
    SourceCursor  string
    Attempt       int
}
```

Route:

```go
type Route struct {
    UID          string
    OwnerNodeID  uint64
    OwnerBootID  uint64
    OwnerSeq     uint64
    SessionID    uint64
    DeviceID     string
    DeviceFlag   uint8
    DeviceLevel  uint8
}
```

Push result:

```go
type PushResult struct {
    Accepted  []Route
    Retryable []Route
    Dropped   []Route
}
```

Recipient pending ack:

```go
type PendingRecvAck struct {
    UID         string
    SessionID   uint64
    MessageID   uint64
    MessageSeq  uint64
    ChannelID   string
    ChannelType uint8
    DeliveredAt int64
}
```

## Ordering Semantics

The system guarantees:

- channel log stores messages in `MessageSeq` order.
- sendack returns the durable `MessageSeq`.
- every RecvPacket carries `MessageSeq`.
- clients can detect gaps and trigger sync.

The system does not guarantee:

- online RecvPacket arrival order across fanout partitions.
- fanout completion order across recipients.
- global ordering between client recvack summaries.

Example:

```text
client sees seq=1002 before seq=1001
  -> buffer 1002
  -> wait briefly or trigger gap sync for 1001
  -> display in seq order
```

## Failure Handling

Append success and delivery failure are separated. Delivery failure is observed
and retried where useful, but it does not affect sendack.

Planner failure:
: Retry fanout task planning with backoff. If retries are exhausted, emit a
  delivery failure observation and rely on future replay in P2.

Route authority stale:
: Resolve a fresh UID authority target and reroute the fanout task. Use bounded
  retries to avoid spinning during cluster movement.

Subscriber page failure:
: Retry the fanout task page. Do not block unrelated partitions.

Remote push RPC failure:
: Mark the affected owner-node batch retryable. Retry with bounded backoff and
  jitter.

Recipient owner session missing:
: Return dropped for that exact route. Do not retry unless a later presence
  lookup returns a newer owner route.

Session write failure:
: If the session is closed or route fencing mismatches, dropped. If the owner
  classifies the write as transient pressure, retryable.

Recvack missing:
: Recipient owner expires local pending ack entries after a configured TTL.
  Missing recvack does not cause per-message cross-node retries.

## Backpressure

Backpressure is per stage:

- planner queue depth limits committed events waiting for partition planning.
- fanout task queues are bounded per authority target.
- subscriber page size bounds memory and store pressure.
- push batch size bounds RPC payloads and owner write bursts.
- recipient pending ack entries are bounded per session and per node.

When pressure is high, delivery should prefer degrading realtime fanout over
blocking append/sendack. Observers record drops, retries, queue depth, and
latency so capacity can be tuned with benchmarks.

## Benchmarks

The delivery module must support benchmarks without starting the full gateway
or cluster.

Runtime benchmarks:

```text
go test ./internalv2/runtime/delivery -run '^$' -bench . -benchmem
```

Required benchmark scenarios:

- `BenchmarkPlannerPartition100K`
- `BenchmarkFanoutWorkerLocalPresence`
- `BenchmarkFanoutWorkerRemotePushBatch`
- `BenchmarkRecipientAckTrackerRecvack`
- `BenchmarkDeliveryEndToEndNoCluster`

Benchmark knobs:

```text
subscribers:       1k, 100k, 1m
online_rate:       10%, 50%, 90%
routes_per_uid:    1, 2, 4
partition_count:   64, 256
remote_owner_rate: 0%, 50%, 90%
push_batch_size:   128, 512, 1024
recvack_rate:      0%, 50%, 100%
payload_bytes:     64, 512, 4096
```

Reported metrics:

- committed submit QPS.
- fanout tasks/s.
- resolved UIDs/s.
- resolved routes/s.
- push accepted/retryable/dropped counts.
- pending recvack count.
- queue depth per stage.
- p50/p95/p99 handoff latency.
- p50/p95/p99 local recvack latency.
- allocations per route and per pushed message.

The no-cluster benchmark uses fake ports for subscriber planning, presence,
task routing, and owner push so the runtime can be optimized independently.

## Testing

Unit tests should cover:

- person-channel subscriber derivation.
- partition filtering for store-backed subscriber pages.
- fanout task routing to current UID authority leaders.
- local presence batch lookup.
- stale authority reroute.
- local owner push writes a RecvPacket and records pending ack.
- remote owner push returns accepted, retryable, and dropped routes.
- recvack clears local pending ack state.
- session close clears all pending ack entries for that session.
- missing recvack expires by TTL.
- delivery failure does not change sendack result.

App wiring tests should prove:

- message committed sink is connected to delivery.
- delivery node RPC handlers are registered when the cluster runtime supports
  them.
- single-node cluster delivery uses the same routing surfaces as multi-node
  clusters.
- delivery-only benchmarks can instantiate the runtime without app, gateway, or
  cluster dependencies.

## Migration Notes

Implementation should be staged:

1. Add `internalv2/usecase/delivery` and `internalv2/runtime/delivery` with fake
   ports and benchmarks.
2. Add recipient ack tracker and gateway recvack mapping.
3. Add local owner push through `runtime/online.Registry`.
4. Add node RPC for remote owner `PushBatch`.
5. Add fanout task routing by UID authority leader.
6. Wire message committed sink in `internalv2/app`.
7. Add delivery metrics and no-cluster benchmark reports.

Do not migrate CMD, plugin, offline sync, or full `NoPersist` behavior during
this P1 delivery module work.
