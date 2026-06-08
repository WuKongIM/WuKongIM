# Async Conversation Projection Design

## Context

The current conversation authority committed sink can run conversation
projection and authority admission in the foreground after a durable message
append. In the three-node real-QPS benchmark, this makes SENDACK latency depend
on group member reads, UID authority routing, local authority cache locks, and
remote Conversation Authority RPCs.

Conversation active rows are a best-effort working-set hint. Message durability
is already provided by the channel log. Therefore the SENDACK path should not
wait for conversation active projection.

## Goals

- Keep SENDACK latency independent from conversation projection and authority
  admission.
- Preserve conversation list eventual consistency through background active-row
  projection.
- Reuse the existing `internalv2/usecase/conversation.Projector` policy.
- Reuse the existing routed `ConversationAuthorityClient` and local
  `conversationAuthority` cache.
- Keep the runtime node-local, bounded, observable, and simple.
- Delete unused or obsolete foreground-admission code after the async path is
  introduced.

## Non-Goals

- Do not introduce a durable projection log, offset tracking, replay queue, or a
  second reliable message system.
- Do not make conversation active hints part of the message durability contract.
- Do not add a compatibility fallback that keeps the old foreground admission
  path alive.

## Recommended Approach

Introduce a node-local `conversationAsyncProjector` in `internalv2/app`.

The committed sink becomes thin:

```text
MessageCommitted
  -> thin committed sink
  -> conversationAsyncProjector.Submit(event)
       - validate minimal fields
       - strip payload and request-scoped UID copies
       - shard by channelID/channelType/fromUID
       - keep the newest event for the shard key
       - return immediately
```

The background runtime performs projection and admission:

```text
flush ticker or dirty threshold
  -> drain coalesced committed events from all shards
  -> classify group members with a per-flush member cache
  -> build ActivePatch values through conversation.Projector
  -> coalesce patches by uid/channelID/channelType
  -> group patches by current UID authority target
  -> admit local and remote target groups with bounded concurrency and batch size
  -> requeue retryable failures into bounded retry state
```

## Components

### conversationAsyncProjector

Owns foreground submission, sharded coalescing, dirty limits, background flush,
retry state, lifecycle start/stop, and observations.

Foreground `Submit` must not call member storage, authority routing, authority
RPC, or durable conversation storage.

### thin committed sink

Implements `message.CommittedSink` and `message.CommittedPayloadPolicy`.

It passes metadata-only committed events to `conversationAsyncProjector.Submit`
and reports `RequiresCommittedPayload=false`.

### conversationProjectorMemberCache

Lives for one flush cycle. It wraps the existing `MemberSource` and memoizes
`ClassifyMembers(channelID, channelType, limit)` results so repeated events for
the same channel in one cycle do not repeatedly scan subscribers.

### conversationAuthorityAdmitter

Encapsulates background patch admission:

- coalesce patches by UID-owned conversation key
- resolve UID route targets
- group patches by exact `RouteTarget`
- admit local and remote target groups with configured batch size and
  concurrency
- return normalized retry/drop results to the async projector

This keeps authority routing out of the foreground committed sink.

### authority route lifecycle

Route-authority watching and handoff remain app-level lifecycle behavior, but
they are separated from committed-event projection. This component updates the
local `conversationAuthority` target state and drains old local authority
targets during handoff.

It must not receive committed message events.

## Configuration

Replace foreground-admission naming with background projection naming.

New configuration:

- `WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL`
- `WK_CONVERSATION_PROJECTION_SHARD_COUNT`
- `WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS`
- `WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES`
- `WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE`
- `WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS`
- `WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY`
- `WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT`

Existing cache and list-read configuration remains:

- `WK_CONVERSATION_SMALL_GROUP_FANOUT_LIMIT`
- `WK_CONVERSATION_MAX_LAST_MESSAGE_CONCURRENCY`
- `WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID`
- `WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS`
- `WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX`
- `WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT`

Deprecated foreground names should be removed from config parsing, examples,
tests, and docs:

- `WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT`
- `WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT`
- `WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS`
- `WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY`

## Backpressure And Drop Semantics

Foreground submission is best-effort and bounded.

Submit behavior:

1. If the event is invalid or has no channel identity, ignore and count it.
2. If the same shard key already exists, replace it when the new event is newer.
3. If the event is a new key and dirty capacity is available, reserve capacity
   and store it.
4. If the event is a new key and dirty capacity is full, drop the hint and count
   it.

There is no foreground sparse fallback. Sparse fallback still belongs to the
projector policy when the background member classifier determines a channel is
not small.

Retry behavior:

- Retry `route_not_ready`, `stale_route`, `not_leader`, timeout, and temporary
  transport failures.
- Drop invalid events, projector configuration errors, and malformed patches.
- Bound retry state by row count and age.
- When retry state is full, drop the oldest retry hint and count it.

Local authority cache pressure continues to be handled inside
`conversationAuthority`, including durable fallback when needed.

## Observability

Metrics should describe the new responsibilities directly:

- foreground submit result: accepted, coalesced, dropped, ignored
- dirty key count and configured dirty capacity
- flush duration and drained event count
- projected patch count, dense event count, sparse event count
- member classify result and cache hit/miss
- authority admit result, target group count, local batch count, remote batch
  count
- retry patch count, retry age, retry drop count

Avoid high-cardinality labels such as UID, channel ID, or route target.

The old mixed foreground projector/authority observations should be removed or
renamed so metrics do not imply that authority admission still runs in the
SENDACK path.

## Code Removal

After the async path is introduced, remove the obsolete foreground-admission
implementation instead of keeping a parallel fallback.

Remove or replace:

- synchronous projection and admission from `conversationAuthorityCommittedSink`
- foreground pending structures and helpers such as
  `conversationAuthorityPendingKey`, `pendingSnapshotWith`, `rememberPending`,
  `clearPending`, and `conversationActivePatchDominates`
- committed-sink-owned route watcher and handoff drain logic
- old foreground admission config parsing, example config entries, and tests
- old projector/authority metric names that no longer map to a live path
- stale fakes, test helpers, interfaces, and FLOW.md text that describe
  foreground authority admission

Keep:

- `internalv2/usecase/conversation.Projector`
- `ConversationAuthorityClient`
- local `conversationAuthority`
- `combineCommittedSinks` and metadata-only payload policy if still used by
  conversation or delivery committed sinks
- route authority handoff semantics, moved out of the committed sink

## Testing

Unit tests in `internalv2/app`:

- `Submit` does not call member storage, authority client, authority RPC, or
  durable conversation storage.
- repeated events for the same shard key keep the newest event.
- dirty capacity drops new keys without blocking.
- `Flush` drains events, projects patches, and admits them in the background
  path.
- retryable admission failures enter bounded retry state and are cleared after
  a later successful flush.
- `Stop` attempts a bounded final flush.
- `RequiresCommittedPayload=false`.

Usecase tests:

- keep projector policy coverage in `internalv2/usecase/conversation`
- add boundary coverage only if dense and sparse projection limits are not
  already covered

App wiring and lifecycle tests:

- app construction wires the thin committed sink and async projector
- lifecycle `Start` starts the projector worker
- lifecycle `Stop` stops and flushes it
- delivery-disabled deployments still wire conversation projection when the
  cluster exposes conversation authority and projection surfaces

Cleanup tests:

- compile-time checks should fail if removed config fields are still referenced
- targeted `rg` checks can be used in development to ensure old foreground
  admission names are gone

Performance verification:

```bash
go test ./internalv2/app ./internalv2/usecase/conversation ./internalv2/infra/cluster
./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000
```

Optional contrast run:

```bash
WK_CONVERSATION_SMALL_GROUP_FANOUT_LIMIT=9 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000
```

Expected benchmark result:

- SENDACK p99 is no longer dominated by conversation authority admission.
- The async projector metrics show background flush and admission work.
- Conversation list remains eventually consistent.

## Rollout

Implement as one focused replacement, not a dual-path rollout:

1. Add the async projector runtime and tests.
2. Wire the thin committed sink.
3. Move route authority lifecycle out of the committed sink.
4. Remove old foreground admission code, config, metrics, tests, and docs.
5. Run unit tests and the 10k real-QPS benchmark.

Because conversation active projection is best-effort, failure of the async
projector must not fail SEND or alter message append semantics.
