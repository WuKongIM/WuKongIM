# ChannelV2 Deep Sendtrace Design

## Context

The internalv2 SEND trace migration records the first end-to-end stages:

- `gateway.messages_send`
- `gateway.write_sendack`
- `message.send_durable`
- `channel.append.local`

That is enough to query one SEND through the gateway, usecase, and
`internalv2/infra/cluster` adapter boundary. It does not yet explain where time
is spent inside `pkg/channelv2` after the adapter calls `AppendBatch`.

ChannelV2 already has low-cardinality append stage metrics in the reactor:

- `store_append_wait`
- `post_store_commit_wait`
- quorum sub-stages such as follower pull, ack offset, and HW advance

The next slice should correlate the most useful leader-side reactor/store
stages with SEND trace ids while keeping the high-concurrency hot path safe.

## Goals

- Record leader-side ChannelV2 sendtrace stages for traced appends:
  - `replica.leader.queue_wait`
  - `replica.leader.local_durable`
  - `replica.leader.quorum_wait`
- Keep trace metadata transient. It may live in append requests, reactor queue
  requests, append batches, and worker-side sidecars, but not in durable channel
  records or DB/idempotency semantics.
- Keep diagnostics-disabled overhead close to one cheap gate check when trace
  metadata is present, with no `time.Now`, event construction, or slice
  allocation.
- Keep diagnostics-enabled overhead bounded by a deep-trace detail policy and a
  per-batch maximum number of traced items.
- Preserve low-cardinality metrics. Trace ids, channel keys, client message
  numbers, and UIDs stay in sendtrace/diagnostics events only.

## Non-Goals

- No follower-side `replica.follower.apply_durable` correlation in this slice.
  That requires trace sidecars in pull responses and follower apply tasks and
  should be designed separately after the leader-side path proves useful.
- No manager or HTTP diagnostics query API.
- No trace labels in Prometheus metrics.
- No broad rewrite of reactor scheduling, worker pools, or storage adapters.
- No attempt to record every successful message at every reactor sub-stage by
  default.

## Recommended Approach

Use a two-level gate:

1. The request must carry trace metadata.
2. A detail policy must decide that deep trace should be collected for selected
   items, or a completed stage must be an error or slow enough to justify a
   bounded lazy scan.

The key point is that ordinary successful appends should not allocate per-message
trace sidecars just because app-level diagnostics are enabled. The diagnostics
store sampler runs after `sendtrace.Record`; deep reactor tracing needs an
earlier gate so event construction itself does not become the bottleneck.

## Detail Policy

Extend `pkg/observability/sendtrace` with an optional detail-sampling interface
implemented by the active sink:

```go
type DetailKey struct {
	TraceID     string
	ChannelKey  string
	ClientMsgNo string
	FromUID     string
}

type DetailDecision struct {
	Keep   bool
	Reason string
}

type DetailLimits struct {
	SlowThreshold    time.Duration
	MaxItemsPerBatch int
}

type DetailSampler interface {
	KeepSendTraceDetail(DetailKey) DetailDecision
	SendTraceDetailLimits() DetailLimits
}
```

`sendtrace` should expose small helper functions that consult the active sink
when it implements `DetailSampler`. If there is no active sink or the sink does
not implement the interface, helpers return disabled decisions.

`internal/observability/diagnostics.SendTraceSink` should implement the detail
sampler using the existing sampler concepts plus deep-trace-specific knobs:

- Debug/tracking rules keep matching detail traces.
- `DeepSampleRate` keeps a low-rate subset of ordinary successful deep traces.
- `DeepSlowThreshold` defaults to the normal diagnostics slow threshold.
- `DeepMaxItemsPerBatch` defaults to a small bounded value, such as 16.

Recommended defaults:

- Deep detail collection is disabled for ordinary successful events unless a
  debug/tracking rule or explicit `DeepSampleRate` enables it.
- Errors and slow stages may trigger a bounded lazy scan up to
  `DeepMaxItemsPerBatch`.

## Reactor Sidecar

Add an unexported reactor sidecar that keeps only selected trace metadata:

```go
type appendTraceItem struct {
	traceID     string
	channelKey  string
	clientMsgNo string
	fromUID     string
	attempt     int
	requestIdx  int
	recordIdx   int
}

type appendTraceBatch struct {
	items []appendTraceItem
}
```

The sidecar belongs to reactor memory only, likely on `appendBatch` and
optionally on per-op timing state. It must never be copied into `ch.Record`,
storage requests as durable data, DB messages, or idempotency keys.

Build sidecars only when needed:

- At append admission or flush, build a preselected sidecar only if
  `sendtrace` detail sampling is active and at least one request/message has
  trace metadata.
- At stage completion, if there was no preselected sidecar but the stage errored
  or exceeded the slow threshold, lazily scan the batch requests and select up
  to `MaxItemsPerBatch` traced items.

All stage timing should be batch-level. Do not call `time.Now` per item.

## Stage Mapping

### `replica.leader.queue_wait`

Measures queue and batch wait from reactor append admission to store append
submission.

Use existing request timing:

```text
appendRequest.enqueuedAt -> markAppendStoreSubmitted submittedAt
```

Emit one event per selected trace item. `MessageSeq` can be filled later when
store offsets are known, or the event can use `RangeStart`/`RangeEnd` for batch
coverage. The preferred implementation is to emit after store completion so the
assigned sequence is known while still measuring queue duration from the earlier
timestamps.

### `replica.leader.local_durable`

Measures local durable append latency:

```text
storeSubmittedAt -> handleStoreAppendResult completedAt
```

Use `worker.Result.StoreAppend.BaseOffset` and per-record index to compute the
message sequence for each selected item. Record stable error classifications for
store append failures.

### `replica.leader.quorum_wait`

Measures quorum wait after local durable append:

```text
storeCompletedAt -> append future completion
```

Emit only for quorum-mode append waiters. Local commit mode should either skip
this stage or emit a skipped result only if a future diagnostics view explicitly
needs it. The first implementation should skip local mode to avoid noisy events.

The existing quorum sub-stage metrics should remain metrics-only for now. They
can be used to debug aggregate behavior, while `replica.leader.quorum_wait`
correlates the traced SEND with the overall post-store wait.

## Error Handling

Use stable error codes that match the rest of sendtrace where possible:

- `not_leader`
- `stale_route`
- `channel_not_found`
- `backpressured`
- `canceled`
- `timeout`
- `append_failed`
- `other`

For batch-level failures, record at most `MaxItemsPerBatch` traced items. For
item-level failures, record only the selected item when that item has trace
metadata or is selected by the lazy error scan.

## Performance Rules

- Do not call `sendtrace.Record` directly from broad loops without checking the
  local sidecar or a lazy error/slow condition.
- Do not call `time.Now` per message. Capture timestamps once per batch/stage.
- Do not allocate trace sidecar slices for untraced requests or ordinary
  successful requests that the detail policy drops.
- Do not use `context.Context` values to carry trace metadata through reactor
  and worker paths.
- Do not add locks to the reactor hot path for detail sampling. The active
  detail policy should be immutable or atomic after app construction.
- Enforce `MaxItemsPerBatch` before event construction.

## Configuration

Add deep-trace knobs under internalv2 diagnostics:

- `DeepSampleRate`
- `DeepSlowThreshold`
- `DeepMaxItemsPerBatch`

Expose them in `cmd/wukongimv2` only after the implementation exists. Defaults
should be conservative:

- `DeepSampleRate = 0`
- `DeepSlowThreshold = Diagnostics.SlowThreshold`
- `DeepMaxItemsPerBatch = 16`

Static `DebugMatches` and runtime tracking rules should be able to force deep
trace detail without changing the ordinary successful sample rate.

## Testing

Add focused tests before implementation:

- `sendtrace` detail helper returns disabled when no sink or the sink does not
  implement `DetailSampler`.
- diagnostics sink keeps detail for debug/tracking matches and honors
  `DeepSampleRate` and `DeepMaxItemsPerBatch`.
- reactor emits `replica.leader.queue_wait` and
  `replica.leader.local_durable` for a selected traced append.
- reactor emits `replica.leader.quorum_wait` only for quorum commit mode after
  local durable append completes.
- untraced appends do not build trace sidecars or emit events.
- diagnostics-disabled traced requests do not allocate sidecars or call
  `time.Now` for deep stages.
- trace metadata is not persisted into channel records or DB-compatible
  messages.

Run targeted tests:

```bash
go test ./pkg/observability/sendtrace ./internal/observability/diagnostics ./pkg/channelv2/reactor ./pkg/channelv2/worker ./pkg/channelv2/store -count=1
go test ./internalv2/... ./pkg/channelv2 ./pkg/clusterv2/channels -count=1
```

## Rollout

Implement this as a second migration slice after the internalv2 shallow SEND
trace branch lands. Keep the first implementation leader-side only. Use
wkbench or internalv2 SEND smoke runs to compare throughput with:

- diagnostics disabled
- diagnostics enabled with `DeepSampleRate=0`
- diagnostics enabled with debug match enabled for one channel or sender
- diagnostics enabled with a low nonzero deep sample rate

Only consider follower-side trace sidecars after these measurements show that
leader-side detail is useful and the overhead stays within budget.
