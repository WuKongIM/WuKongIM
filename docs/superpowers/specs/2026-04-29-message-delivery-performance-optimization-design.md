# Message Delivery Performance Optimization Design

## Context

The current message path is already cluster-first: a send request is durably appended to the channel log, then committed side effects fan out into realtime delivery and conversation projection. A successful sendack means the channel log append reached the configured commit mode; it does not wait for realtime delivery or receiver ack.

The main hot path today is:

```text
Gateway/API send
  -> internal/usecase/message.Send
  -> channelmeta.RefreshChannelMeta
  -> pkg/channel.Append / remote channel_append
  -> messageevents.MessageCommitted
  -> asyncCommittedDispatcher goroutine
  -> delivery.Manager per-channel actor
  -> subscriber + presence route resolve
  -> local or remote RecvPacket push
  -> Recvack / SessionClosed route cleanup
```

This design focuses on reducing per-message fixed cost and reducing fanout amplification under high QPS, cross-node delivery, and large group channels. It keeps the project rule that a single-node deployment is still a single-node cluster; no optimization may add a separate non-cluster business branch.

## Goals

1. Reduce send hot-path latency and CPU by avoiding unnecessary authoritative channel metadata refreshes.
2. Replace per-committed-message goroutine fanout with bounded, observable worker queues.
3. Reduce cross-node realtime delivery RPC and encoding amplification.
4. Prevent realtime delivery inflight state from accumulating indefinitely when routes never ack.
5. Add enough metrics/logging to prove the optimizations help and to detect overload before it becomes message loss.

## Non-Goals

- Do not change durable send semantics or make `CommitModeLocal` the default.
- Do not implement a persistent offline message queue inside `internal/runtime/delivery`; Channel Log remains the durable truth and replay/sync remain the catch-up path.
- Do not introduce a new service layer or bypass `internal/app` as the composition root.
- Do not implement a node-to-node delivery stream or online-subscriber intersection index in this phase. Those are future options after measurements from this phase.
- Do not optimize by ignoring `NodeID`, `BootID`, session state, channel epoch, or leader epoch fencing.

## Proposed Scope

Implement one focused performance-hardening phase with four changes:

1. **Observability baseline** for send, meta refresh, committed dispatch, resolve, push, ack, retry, and replay lag.
2. **Channel metadata fast path** for healthy business sends, with forced refresh on stale/leader/lease errors.
3. **Bounded committed dispatch queues** instead of unbounded goroutine-per-message routing.
4. **Realtime delivery batching and lifecycle cleanup** for remote push, retry ticks, idle actors, and max-attempt expiry.

The actor IO-decoupling refactor is intentionally deferred. It is higher risk because it changes actor execution semantics. The first phase should make the current design cheaper and measurable before changing actor internals.

## Design

### 1. Observability Baseline

Add lightweight metrics and structured diagnostics around the existing flow before changing behavior.

Minimum counters/gauges/timers:

- `message_send_meta_refresh_total`, `message_send_meta_refresh_duration`, labeled by outcome: cache_hit, authoritative_read, bootstrap, repair, error.
- `message_send_append_duration`, labeled by local/remote and outcome.
- `committed_dispatch_queue_depth`, `committed_dispatch_enqueue_total`, `committed_dispatch_overflow_total`.
- `delivery_resolve_duration`, `delivery_resolve_pages_total`, `delivery_resolve_routes_total`, labeled by channel type.
- `delivery_push_rpc_total`, `delivery_push_rpc_routes_total`, `delivery_push_rpc_duration`, labeled by target node and outcome.
- `delivery_actor_inflight_routes`, `delivery_ack_binding_total`, `delivery_route_expired_total`.
- `committed_replay_lag_messages` and replay pass duration.

Use existing logging and metrics patterns where available. Metrics must not allocate heavily on the hot path. If the current metrics surface is insufficient, add narrow runtime counters first and expose them later through management APIs.

### 2. Channel Metadata Fast Path

Current `sendWithEnsuredMeta` always calls `RefreshChannelMeta` for business sends. The optimization adds a safe cache path in `internal/runtime/channelmeta` and keeps authoritative refresh as fallback.

A cached meta may be used only when all conditions hold:

- Channel status is active.
- Leader is non-zero.
- `LeaseUntil` is after `now + refreshLeadTime`.
- The cached leader is not known dead/draining in the node liveness cache.
- The cached meta's epoch and leader epoch are present and were successfully applied to routing/local runtime when cached.
- Repair policy does not currently require repair for that meta.

Behavior:

1. `message.Send` asks the refresher for business metadata.
2. The refresher first checks the healthy positive cache.
3. On cache hit, it returns the cached `channel.Meta` without slot metadata IO.
4. On cache miss or unsafe cache, it performs the existing authoritative refresh/repair/bootstrap path.
5. If append returns `ErrStaleMeta`, `ErrNotLeader`, `ErrLeaseExpired`, `ErrRerouted`, or equivalent remote redirect, invalidate that channel cache and retry one authoritative refresh + append.
6. Retry budget remains bounded: at most one forced refresh retry per send.

This preserves cluster semantics because cached routing is used only while the authoritative lease and liveness evidence are still valid. The authoritative store remains the source of truth.

### 3. Bounded Committed Dispatch Queues

Replace `asyncCommittedDispatcher.SubmitCommitted` spawning one goroutine per event with sharded workers owned by the app lifecycle.

Architecture:

```text
messageevents.MessageCommitted
  -> committedDispatcher.SubmitCommitted
  -> shard by ChannelID/ChannelType
  -> bounded queue per shard
  -> worker routeCommitted(event)
  -> delivery queue and conversation projector
```

Rules:

- Queue sharding must preserve per-channel order inside a shard.
- `SubmitCommitted` must not turn a durable send into a failed send after append succeeds.
- If enqueue succeeds, workers run existing route logic.
- If the queue is full, log and count overflow. Realtime delivery for that event may be skipped because committed replay can repair missed side effects. To reduce user-visible conversation lag, attempt a best-effort immediate conversation submit/fallback before returning.
- Workers stop through `internal/app` lifecycle before channel log and store shutdown.
- Queue size and worker count start as internal defaults derived from `GOMAXPROCS`; do not add public config until metrics show operators need tuning.

This keeps sendack latency bounded and avoids unbounded goroutine growth under spikes.

### 4. Remote Push Batching

Current `distributedDeliveryPush` groups remote routes by target node and UID, then sends one RPC per UID. For group channels this can cause many RPCs even when the frame is identical for every recipient on the same node.

Change remote push to batch by target node and frame identity:

- For group channels, build one `RecvPacket`/encoded frame per message and target node, with all routes on that node in one request.
- For person channels, group by recipient view because `ChannelID` differs by recipient perspective. Usually this remains small.
- Extend the delivery push RPC payload to support multiple items per node:

```text
DeliveryPushBatchRequest
  OwnerNodeID
  Items[]:
    ChannelID
    ChannelType
    MessageID
    MessageSeq
    FrameBytes
    Routes[]
```

The remote adapter processes each item with the same fencing checks it already has: target node, gateway boot id, online registry session identity, and active route state. It returns accepted/retryable/dropped routes per item or as aggregate route lists.

Compatibility can be handled by reusing the existing service ID with a backward-compatible request shape only if practical; otherwise add a new internal service ID and have the client use the new RPC because this repository controls both sides of the node adapter.

### 5. Delivery Retry, Expiry, and Idle Lifecycle

The runtime currently exposes `ProcessRetryTicks` and `SweepIdle`, but the app composition does not clearly own a lifecycle component for them. Add a `delivery_runtime` lifecycle component that starts a lightweight ticker and stops before delivery dependencies are closed.

Behavior:

- Process retry ticks at a small fixed interval, for example 100-250ms, with jitter if needed.
- Sweep idle actors at a slower interval, for example 30s.
- When a route reaches `MaxRetryAttempts` without ack, finish the route as `expired` and remove its ack binding. This frees actor route budget and relies on Channel Log catch-up for later delivery.
- Record route expiry metrics and structured logs with channel, message id, seq, session id, and attempt count.
- Session close/offline continues to remove route state immediately.

This prevents unacked realtime routes from pinning memory and blocking later resolve pages indefinitely.

## Data Flow After Phase 1

```text
SendPacket
  -> message.Send
  -> channelmeta healthy cache hit OR authoritative refresh
  -> local append / remote channel_append
  -> quorum commit
  -> enqueue committed event into sharded dispatch queue
  -> worker submits delivery and conversation projection
  -> delivery actor resolves subscribers and authoritative online routes
  -> local push or remote batched push
  -> accepted routes bind ack index
  -> ack/offline/expiry removes route state
  -> committed replay repairs overflow or missed async side effects
```

## Error Handling

- Metadata cache hit followed by stale append: invalidate cache and do exactly one forced refresh retry.
- Queue overflow: never fail the already-committed send. Count overflow, log it, best-effort submit conversation, rely on committed replay for missed realtime/conversation side effects.
- Remote push batch partial failure: accepted routes bind ack; dropped routes finish immediately; retryable routes keep existing retry behavior.
- Retry expiry: remove ack binding and finish the route. Do not retry forever.
- Delivery lifecycle ticker errors: log and continue unless context is canceled.

## Testing Strategy

Unit tests:

- Channel meta fast path returns cached metadata only when lease/liveness/status are safe.
- Fast path invalidates and forced-refreshes after stale/not-leader/lease append errors.
- Committed dispatch queue preserves per-channel ordering and does not fail `SubmitCommitted` on overflow.
- Overflow triggers metrics/logging and conversation fallback.
- Remote push batches group channel routes by node into one request.
- Person channel push keeps recipient-specific channel view.
- Delivery lifecycle calls retry processing and idle sweep.
- Max retry expiry removes ack binding and frees inflight route budget.

Integration tests:

- Single-node cluster send still returns sendack after quorum commit and before recvack.
- Three-node cross-node personal delivery still receives `RecvPacket` and ack clears owner + remote ack indexes.
- Group channel delivery to users on multiple nodes uses batched remote push and preserves frame contents.
- Simulated committed queue overflow is repaired by committed replay.
- Lease expiry / leader change during cached metadata send retries with authoritative refresh and succeeds or returns the existing typed error.

Performance validation:

- Microbenchmark `message.Send` for hot channel cache hit vs forced refresh.
- Benchmark remote group push fanout before/after batching.
- Stress test high QPS send with bounded dispatch queues and verify goroutine count does not grow linearly with messages.
- Track p50/p95/p99 send latency, delivery latency, RPC count per message, allocations per send, and replay lag.

## Rollout Plan

1. Add observability first with no behavior change.
2. Add delivery lifecycle ticker and retry expiry; verify no ack binding leaks in existing tests.
3. Add metadata fast path behind internal behavior with conservative safety checks.
4. Replace per-message goroutine dispatch with sharded bounded workers.
5. Add remote push batching and update node adapter tests.
6. Run targeted unit tests, then `go test ./internal/... ./pkg/...`; run integration/stress only when needed because integration tests are slow.

## Risks and Mitigations

- **Stale metadata cache can misroute appends.** Mitigate with lease/liveness checks and one forced refresh retry on stale/leader errors.
- **Queue overflow can reduce realtime delivery.** Mitigate with metrics, logs, committed replay repair, and best-effort conversation fallback.
- **Batch RPC can complicate partial success.** Keep per-route accepted/retryable/dropped results and preserve current ack binding semantics.
- **Retry expiry can drop realtime delivery too aggressively.** Keep default retry budget equivalent to current delays, count expiries, and rely on durable catch-up.
- **New worker queues can hide backpressure.** Expose queue depth and overflow counters before relying on them in production.

## Future Work

- Move actor IO out of actor mutex using mailbox result events.
- Add online subscriber intersection index for large hot groups.
- Add node-to-node delivery lanes/streams for high-volume cross-node realtime traffic.
- Promote queue sizes and retry settings to `WK_` config only after metrics show stable tuning needs.
