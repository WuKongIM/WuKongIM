# Runtime Pressure Metrics Design

Date: 2026-06-05

## Purpose

WuKongIM now has several bounded worker pools and bounded queues across gateway,
ChannelV2, ClusterV2, Slot Multi-Raft, and TransportV2. Performance triage needs
one clear view that answers:

- Which pool or queue is full?
- How many tasks or bytes are waiting?
- How many workers are currently busy?
- Which submit path is rejecting, blocking, timing out, or dropping work?
- How long does accepted work wait before execution?

This design adds Prometheus and Grafana visibility for those runtime pressure
points. It does not change scheduling behavior, backpressure behavior, queue
limits, cluster semantics, or management UI / monitor API responses.

## Scope

In scope:

- `pkg/gateway`
- `pkg/channelv2`
- `pkg/clusterv2`
- `pkg/slot`
- `pkg/transportv2`
- `pkg/metrics`
- Grafana dashboard assets under `docker/observability/grafana`

Out of scope:

- `pkg/transport`. That package is intentionally excluded because the cluster
  transport direction is `pkg/transportv2`.
- Management UI and monitor API changes.
- High-cardinality per-channel, per-user, per-connection, or per-slot labels in
  the unified runtime pressure metrics.
- OpenTelemetry or tracing changes.

## Design Choice

Use one unified runtime pressure metric family in `pkg/metrics`, while each
runtime package keeps a local observer interface and emits package-native events.

This preserves package boundaries:

```text
runtime package
  -> local observer interface or transportv2 Event
  -> internalv2/app observability adapter
  -> pkg/metrics RuntimePressureMetrics
  -> /metrics
  -> Grafana Runtime Pressure dashboard
```

Runtime packages must not import Prometheus or `pkg/metrics`. Composition roots
wire observers.

Existing domain metrics remain compatible. For example, existing
`wukongim_channelv2_*`, `wukongim_gateway_*`, and controller Raft metrics keep
their names. The new unified metrics provide a cross-runtime view for pressure
attribution.

## Metric Model

Add `RuntimePressureMetrics` to `pkg/metrics.Registry`.

Recommended metric names:

- `wukongim_runtime_pool_workers`
  - Gauge. Configured worker count.
  - Labels: `component`, `pool`.
- `wukongim_runtime_pool_inflight`
  - Gauge. Currently running work.
  - Labels: `component`, `pool`.
- `wukongim_runtime_pool_queue_depth`
  - Gauge. Queued item count.
  - Labels: `component`, `pool`, `queue`, `priority`.
- `wukongim_runtime_pool_queue_capacity`
  - Gauge. Queued item capacity.
  - Labels: `component`, `pool`, `queue`, `priority`.
- `wukongim_runtime_pool_queue_bytes`
  - Gauge. Queued payload bytes.
  - Labels: `component`, `pool`, `queue`, `priority`.
- `wukongim_runtime_pool_queue_bytes_capacity`
  - Gauge. Queued byte capacity.
  - Labels: `component`, `pool`, `queue`, `priority`.
- `wukongim_runtime_pool_admission_total`
  - Counter. Submit/enqueue/admission outcomes.
  - Labels: `component`, `pool`, `queue`, `priority`, `result`.
- `wukongim_runtime_pool_wait_duration_seconds`
  - Histogram. Queue wait or enqueue wait duration.
  - Labels: `component`, `pool`, `queue`, `priority`, `result`.
- `wukongim_runtime_pool_task_duration_seconds`
  - Histogram. Worker task execution duration.
  - Labels: `component`, `pool`, `task`, `result`.

Label rules:

- `component`: stable subsystem name such as `gateway`, `channelv2`, `slot`,
  `transportv2`, `controller`.
- `pool`: low-cardinality runtime pool name such as `async_auth`,
  `async_send`, `store_append`, `store_read`, `store_apply`, `rpc`,
  `scheduler`, `service`.
- `queue`: low-cardinality queue name such as `auth`, `send`, `mailbox`,
  `scheduler`, `write`, `service`.
- `priority`: lane name when meaningful, otherwise `none`.
- `task`: low-cardinality task kind when meaningful, otherwise pool-local task
  class.
- `result`: normalized to `ok`, `full`, `busy`, `closed`, `canceled`,
  `timeout`, `too_large`, `invalid`, `dropped`, `coalesced`, or `other`.

Do not use channel id, uid, node address, connection id, request id, message id,
or raw error strings as labels.

## Package Coverage

### Gateway

Covered pressure points:

- Async CONNECT auth worker pool.
- Async SEND dispatch worker pool.
- gnet actor shard ready queues.
- Per-connection inbound pending bytes.
- Per-connection outbound pending/buffered bytes.

Design:

- Keep `Observer` and `AsyncSendObserver` stable.
- Add narrow optional observer interfaces for auth queue and transport pressure
  instead of renaming `AsyncSendObserver` into a generic observer.
- Emit admission outcomes for auth and SEND enqueue, including full/closed.
- Emit queue depth/capacity after enqueue and dequeue.
- Emit auth queue wait separately from total auth duration.
- Emit actor ready depth/capacity by shard aggregate without connection labels.
- Emit inbound/outbound byte gauges as aggregate pressure, not per connection.

### ChannelV2

Covered pressure points:

- Reactor mailbox per priority.
- Worker pools: store append, store read, store apply, RPC.
- Worker inflight and configured workers.
- Worker queue depth/capacity.
- Worker submit result.
- Worker queue wait and execution duration.
- Per-channel append queue pressure as aggregate gauges or counters, without
  channel labels.

Design:

- Extend the existing `reactor.Observer` and `worker.QueueObserver` shape with
  optional capacity/admission/wait methods.
- Preserve existing `wukongim_channelv2_worker_queue_depth`,
  `wukongim_channelv2_worker_inflight`, and task duration metrics.
- Mirror the same observations into unified runtime pressure metrics.
- Use existing low-cardinality worker task labels.

### Slot Multi-Raft

Covered pressure points:

- Scheduler channel depth/capacity.
- Scheduler pending, queued, processing, and dirty counts.
- Worker count and currently processing worker count.
- Scheduler enqueue outcomes: accepted, coalesced, dirty, requeued.
- Slot processing duration as task duration with low-cardinality task
  `process_slot`.

Design:

- Add `Observer` to `multiraft.Options`.
- Keep observer methods small and package-local.
- Emit scheduler state after enqueue, begin, done, and requeue.
- Do not label unified metrics by `slot_id`. Existing slot-specific domain
  metrics may keep `slot_id`.
- Keep scheduler enqueue non-blocking behavior unchanged.

### ClusterV2

Covered pressure points:

- ClusterV2 itself remains a composition root and observer pass-through point.
- ControllerV2 Raft step queue remains exposed through existing controller
  metrics and is also mirrored into unified queue metrics.
- Default Slot runtime receives the Slot observer.
- Default ChannelV2 service receives the ChannelV2 observer.
- TransportV2 receives the TransportV2 observer when it is wired as the cluster
  transport.

Design:

- Do not put queue accounting logic in `pkg/clusterv2.Node`.
- Add config fields only where required to pass observers into default runtimes.
- If old `pkg/transport` remains temporarily in a compatibility path, leave it
  uninstrumented for this design.

### TransportV2

Covered pressure points:

- Outbound per-connection byte-aware scheduler.
- Scheduler queued items, bytes, capacity, byte capacity, and priority lanes.
- Scheduler admission outcomes: ok, full, stopped, canceled, too_large,
  invalid.
- Scheduler queue wait before write.
- Server service worker pools and bounded service queues.
- Service queue items/bytes/capacity.
- Service admission outcomes: ok, busy, stopped, too_large.
- Service handler inflight and execution duration.
- Pending RPC count.
- Peer pool connection stats.

Design:

- Reuse `core.Observer.ObserveTransport(event)`.
- Add low-cardinality event names such as:
  - `scheduler_queue`
  - `scheduler_admission`
  - `scheduler_wait`
  - `service_queue`
  - `service_admission`
  - `service_task`
  - `pending_rpc`
  - `peer_pool`
- Thread observer from public `ClientConfig` and `ServerConfig` into
  `peer.Manager`, `conn.Conn`, `sched.Scheduler`, and `rpc.Service`.
- Keep event emission best-effort and non-blocking.

## Error Handling

Observation must never change runtime semantics:

- Nil observers are valid and cheap.
- Observer calls must not determine admission success.
- Queue-full and busy paths must release owned payloads exactly as they do now.
- Close paths must continue to drain/release queued payloads.
- Metrics result labels must be normalized before reaching Prometheus.
- Negative durations are clamped to zero.

## Grafana

Add a Runtime Pressure section or dashboard with:

- Pool inflight vs worker count.
- Queue depth / capacity ratio.
- Queue bytes / byte capacity ratio.
- Admission failures by component/pool/result.
- Queue wait p95/p99.
- Worker task duration p95/p99.
- TransportV2 scheduler pressure by priority.
- ChannelV2 worker pressure by pool.
- Gateway auth/SEND pressure.
- Slot scheduler pressure.

Dashboard asset tests must include the new metric names.

## Testing

Unit tests:

- `pkg/metrics`: registry exposes all runtime pressure metrics, label handles
  are stable, and nil-safe methods do not panic.
- `pkg/gateway`: auth queue full, SEND queue full, worker dequeue, gnet actor
  schedule/drain, inbound pending byte overflow, outbound byte overflow.
- `pkg/channelv2/worker`: queue capacity, queue depth, inflight, admission
  result, wait duration, execution duration.
- `pkg/channelv2/reactor`: mailbox capacity/depth, backpressure, low-priority
  drop/coalesce behavior.
- `pkg/slot/multiraft`: scheduler full remains non-blocking, dirty/requeue
  observations, worker inflight, process duration.
- `pkg/transportv2/internal/sched`: item full, byte full, too-large,
  per-priority queue state, queue wait.
- `pkg/transportv2/internal/rpc`: service queue busy, byte busy, inflight,
  task duration.
- `pkg/transportv2/internal/peer` and `internal/conn`: pending RPC and peer
  connection stats.
- `internalv2/app`: observer adapters map package events to `pkg/metrics`.
- Grafana dashboard asset coverage tests pass.

Suggested focused verification:

```bash
GOWORK=off go test ./pkg/metrics ./pkg/gateway/... ./pkg/channelv2/... ./pkg/slot/... ./pkg/transportv2/... ./internalv2/app -count=1
GOWORK=off go test ./docker/observability/grafana -count=1
```

Full repository verification remains:

```bash
go test ./...
```

## Migration Notes

This design is additive:

- Existing metrics stay available.
- New metrics can be used immediately by Prometheus and Grafana.
- Management UI and monitor API do not change.
- Runtime behavior and backpressure rules do not change.

The first implementation should prioritize the pools and queues most useful for
performance triage:

1. `pkg/metrics` runtime pressure family.
2. `channelv2` worker pools and reactor mailboxes.
3. `gateway` auth/SEND queues.
4. `transportv2` scheduler and service pools.
5. `slot` scheduler.
6. Grafana Runtime Pressure panels.
