# wkcli top Design

Date: 2026-06-14

## Purpose

`wkcli top` should give operators a real-time, low-friction view of WuKongIM
server health and runtime pressure. The first screen should answer three
questions:

- Can this node or single-node cluster still accept writes?
- Which runtime layer is under pressure?
- Which node, queue, worker pool, or append stage is causing the visible tail?

The design targets the current `internalv2` architecture. It must not depend on
the legacy `internal` path, and it must not introduce branches that bypass
cluster semantics. A single-node deployment is still reported as a single-node
cluster.

## Design Choice

Create a dedicated top API and collector:

```text
runtime packages
  -> package-local observers and lightweight snapshots
  -> internalv2/app observer fanout
       -> metrics observer   when WK_METRICS_ENABLE=true
       -> top collector      when WK_TOP_API_ENABLE=true
  -> /metrics                Prometheus exposition only
  -> /top/v1/snapshot        wkcli top view model only
```

`wkcli top` must not depend on `WK_METRICS_ENABLE`. Prometheus metrics and top
snapshots share the same runtime observation sources, but they are independent
consumers. This keeps `/metrics` optional while preserving a small built-in
operations surface for local or remote CLI troubleshooting.

## Scope

In scope:

- A node-local, read-only `GET /top/v1/snapshot` API under `internalv2/access/api`.
- A lightweight `internalv2/app` top collector with a bounded in-memory sample
  ring.
- `wkcli top` display model and view definitions.
- Low-cardinality pressure scoring for gateway, ChannelV2, storage, delivery,
  transport, Slot, and controller runtimes.
- Clear source availability notes so the CLI can show partial snapshots without
  guessing.

Out of scope:

- Cross-node server fanout. `wkcli top` queries each configured server and
  aggregates client-side.
- A generic Prometheus query API or raw metrics proxy.
- Historical charts beyond the short rolling window needed for top.
- Mutation APIs, benchmark setup APIs, pprof, diagnostics event queries, or
  per-UID/per-channel drill-down.
- Production authentication design. The API is read-only and can be protected
  later by the existing API gateway/auth surface when that is available for
  internalv2 operations endpoints.

## Configuration

Add top-specific configuration. These settings are separate from
`WK_METRICS_ENABLE`.

```text
WK_TOP_API_ENABLE=true
WK_TOP_COLLECT_INTERVAL=1s
WK_TOP_HISTORY_WINDOW=5m
```

Semantics:

- `WK_TOP_API_ENABLE` registers `/top/v1/snapshot` and wires the top collector.
- `WK_TOP_COLLECT_INTERVAL` controls sample cadence. The default is one second.
- `WK_TOP_HISTORY_WINDOW` bounds retained samples. The default is five minutes.

Validation:

- `WK_TOP_COLLECT_INTERVAL` must be positive when top API is enabled.
- `WK_TOP_HISTORY_WINDOW` must be at least twice the collect interval.
- Snapshot query windows must be no larger than the configured history window.

## API

### Route

```text
GET /top/v1/snapshot
```

The route returns a local node snapshot. Multi-node views are built by `wkcli`
from multiple node-local snapshots.

### Query Parameters

```text
window=10s
view=overview
limit=20
```

- `window` is the aggregation window for rates, error rates, and histogram
  percentiles. Default: `10s`. Minimum: `2s`. Maximum:
  `WK_TOP_HISTORY_WINDOW`.
- `view` trims optional response sections. Allowed values:
  `overview`, `runtime`, `traffic`, `channel`, `storage`, `delivery`, `all`.
  Default: `overview`.
- `limit` caps `pressure.top`. Default: `20`. Maximum: `100`.

The API does not accept sort, color, or terminal layout parameters. Those are
CLI rendering concerns.

### Status Codes

- `200`: Snapshot returned. Optional sections may be omitted according to
  `view`; partial source issues are reported in `sources.notes`.
- `400`: Invalid query parameter.
- `404`: Top API is disabled.
- `503`: Collector is warming up or cannot produce at least two samples for the
  requested window.

The API does not return `503` only because Prometheus metrics are disabled.

## Response Model

Top-level response:

```json
{
  "version": "top/v1",
  "scope": "local_node",
  "generated_at": "2026-06-14T06:32:08Z",
  "window_seconds": 10,
  "node": {},
  "verdict": {},
  "traffic": {},
  "clients": {},
  "pressure": {},
  "channelv2": {},
  "storage": {},
  "delivery": {},
  "sources": {}
}
```

### Node

```json
{
  "node": {
    "id": 2,
    "name": "node-2",
    "ready": true,
    "ready_parts": {
      "routes": true,
      "slots": true,
      "channels": true
    },
    "state_revision": 9821,
    "controller_leader": 1,
    "slot_count": 3,
    "hash_slot_count": 128
  }
}
```

`ready` is true only when route, Slot, ChannelV2, and hash-slot readiness are
all satisfied. Field names use cluster terms even for single-node clusters.

### Verdict

```json
{
  "verdict": {
    "level": "degraded",
    "summary": "channelv2 store_append pressure is high",
    "reasons": [
      "channelv2/store_append queue ratio is 86%",
      "append p99 is 96ms over 10s"
    ]
  }
}
```

Allowed levels:

- `ok`
- `busy`
- `degraded`
- `critical`

Initial scoring rules:

```text
critical:
  ready=false
  or any critical queue ratio >= 0.95
  or full/timeout admissions grow over the window
  or sendack_error_rate >= 1%

degraded:
  any critical queue ratio >= 0.80
  or append_p99 >= 100ms
  or storage request_p99 >= 100ms
  or sendack_error_rate >= 0.1%

busy:
  any critical queue ratio >= 0.60
  or append_p99 >= 50ms

ok:
  no higher-level condition matched
```

These thresholds are intentionally conservative and can become configurable
after real operations feedback.

### Traffic

```json
{
  "traffic": {
    "send_per_sec": 45000,
    "sendack_per_sec": 44970,
    "sendack_error_per_sec": 31,
    "sendack_error_rate": 0.0007,
    "append_per_sec": 44950,
    "append_p50_ms": 8,
    "append_p99_ms": 96,
    "deliver_per_sec": 132000,
    "fanout_rate": 3.1
  }
}
```

`send_per_sec` comes from gateway inbound SEND observations. `append_per_sec`
comes from durable append observations. `fanout_rate` is resolved delivery
routes divided by inbound sends over the selected window.

### Clients

```json
{
  "clients": {
    "connections": 20100,
    "connections_by_protocol": {
      "wkproto": 20100
    },
    "auth_fail_per_sec": 0,
    "close_per_sec": 12
  }
}
```

### Pressure

`pressure` is the primary section for `wkcli top` default and runtime views.

```json
{
  "pressure": {
    "overall_level": "degraded",
    "component_scores": {
      "gateway": 0.18,
      "channelv2": 0.86,
      "storage": 0.71,
      "delivery": 0.24,
      "transportv2": 0.09,
      "slot": 0.14,
      "controller": 0.03
    },
    "top": [
      {
        "component": "channelv2",
        "pool": "store_append",
        "queue": "write",
        "priority": "none",
        "level": "degraded",
        "score": 0.86,
        "depth": 860,
        "capacity": 1000,
        "inflight": 500,
        "workers": 500,
        "wait_p99_ms": 82,
        "task_p99_ms": 64,
        "admission_error_per_sec": 12,
        "hint": "store_append workers are saturated"
      }
    ]
  }
}
```

Pressure score inputs:

- Queue ratio: `depth / capacity`.
- Byte queue ratio: `bytes / bytes_capacity`.
- Worker ratio: `inflight / workers`.
- Latency score: p99 divided by the subsystem threshold.
- Admission score: growth rate for `full`, `busy`, `timeout`, `closed`, or
  `dropped` outcomes.

The final item score is the maximum of available inputs. Missing capacity does
not invent a ratio; it leaves the ratio unavailable and relies on latency or
admission signals.

### ChannelV2

```json
{
  "channelv2": {
    "active_total": 34000,
    "active_leader": 11000,
    "active_follower": 23000,
    "follower_parked": 18000,
    "reactor_mailbox_depth_max": 240,
    "worker_queue_depth_by_pool": {
      "store_append": 860,
      "store_apply": 42,
      "rpc": 21
    },
    "worker_inflight_by_pool": {
      "store_append": 500,
      "store_apply": 83,
      "rpc": 12
    },
    "append_p99_ms": 96,
    "hot_stage": "store_append_wait",
    "stage_p99_ms": {
      "meta_resolve": 2,
      "runtime_append_submit": 4,
      "store_append_wait": 82,
      "quorum_final_complete_wait": 12
    }
  }
}
```

The `hot_stage` is the stage with the highest p99 among known append and wait
stages in the selected window.

### Storage

```json
{
  "storage": {
    "commit_queues": [
      {
        "store": "message",
        "depth": 710,
        "request_p99_ms_by_lane": {
          "append": 64,
          "apply": 18
        },
        "batch_records_p50": 2,
        "batch_commit_p99_ms": 9
      }
    ]
  }
}
```

The storage view separates caller-visible request wait from physical commit
duration so operators can distinguish queue/admission wait from disk commit
tails.

### Delivery

```json
{
  "delivery": {
    "push_per_sec": 132000,
    "routes_per_sec": 390000,
    "push_p99_ms": 12,
    "retry_queue_depth": 0,
    "ack_bindings": 81000,
    "recipient_queue_depth": 110,
    "recipient_queue_capacity": 1024,
    "error_rate": 0.0001
  }
}
```

### Sources

```json
{
  "sources": {
    "collector": {
      "available": true,
      "sample_count": 11,
      "warming_up": false
    },
    "cluster_snapshot": {
      "available": true
    },
    "metrics": {
      "enabled": false,
      "required": false
    },
    "notes": []
  }
}
```

`sources.metrics.required` must remain false for `top/v1`.

## Collector Design

`internalv2/app` owns the top collector because it is the composition root and
already wires runtime observers.

```text
topCollector
  Start:
    create bounded ring based on WK_TOP_HISTORY_WINDOW / WK_TOP_COLLECT_INTERVAL
    register top observers in app fanout
    sample point-in-time gauges and cluster snapshot every interval

  Runtime observer methods:
    update counters, current gauges, and bounded histogram reservoirs

  SnapshotTop(query):
    read ring buffer
    find samples covering query window
    compute rates, error rates, p50/p99, pressure scores, and verdict
```

The collector keeps only low-cardinality data:

- counters by stable component/result labels
- gauges by stable component/pool/queue labels
- histogram buckets or bounded quantile sketches by stable stage/kind labels
- cluster readiness snapshot

The collector does not store UID, channel ID, client message number, trace ID,
request ID, raw error text, or message ID.

## Observer Fanout

Runtime packages continue to define package-local observer interfaces. They
must not import top or Prometheus packages.

`internalv2/app` wires observer fanout:

```text
Gateway observer fanout:
  metrics observer when metrics are enabled
  top observer when top is enabled

ChannelV2 observer fanout:
  metrics observer when metrics are enabled
  top observer when top is enabled

Storage, delivery, transport, Slot, controller observers:
  same pattern
```

Where a runtime has only Prometheus-specific observation today, add a narrow
package-local event or snapshot interface and map both metrics and top from
that shared source.

## wkcli top Display

Default overview:

```text
WuKongIM top   ctx=prod   nodes=3/3 ready   refresh=1s   view=overview

VERDICT   DEGRADED: channelv2 store_append p99 high on node-2

CLUSTER   controller=1   revision=9821   routes=ok   slots=ok   channels=ok
TRAFFIC   send=128k/s   sendack=127.8k/s   recv=390k/s   fanout=3.1x   append_p99=42ms
CLIENTS   conn=58.2k    auth_err=0.00%     close/s=12       sendack_err=0.03%
PRESSURE  gateway=18%   channelv2=86%      storage=71%     delivery=24%     transport=9%

NODE  READY  CONN    SEND/S  ACK/S   APPEND_P99  ERR%   TOP PRESSURE
1     yes    19.2k   42k     42k     18ms        0.01   storage commit_queue 43%
2     yes    20.1k   45k     44k     96ms        0.07   channelv2 store_append 86%
3     yes    18.9k   41k     41k     21ms        0.01   gateway async_send 31%

HOT PRESSURE
node-2  channelv2/store_append    queue=860/1000  inflight=500/500  wait_p99=82ms
node-2  storage/message append     queue=710       request_p99=64ms  batch_p50=2
```

Views:

- `overview`: health, traffic, clients, component pressure, node table, hot
  pressure.
- `runtime`: all pressure items by component/pool/queue.
- `traffic`: gateway send, sendack, append, delivery throughput, and error
  rates.
- `channel`: ChannelV2 active runtime distribution, append stages, worker
  queues, and parked followers.
- `storage`: grouped commit queues, logical request p99 by lane, physical
  commit p99, and batch efficiency.
- `delivery`: push RPC, route resolution, retry queue, recipient worker, and
  ack binding pressure.

CLI keys:

- `q`: quit
- `?`: help
- `1..6`: switch views
- `s`: rotate sort mode
- `p`: pause refresh
- `r`: raw/human units
- `e`: show only unhealthy or pressured rows

## Error Handling

Observation must not change runtime behavior:

- Nil observers are valid.
- Top observer calls must be non-blocking and best effort.
- Bounded collector buffers must drop oldest samples rather than blocking hot
  paths.
- Snapshot API failures must report source status instead of panicking or
  falling back to Prometheus.
- Negative counter deltas are treated as process restart or reset and clamped
  to zero for the current window.

## Testing

Unit tests:

- `internalv2/app`: top collector starts and stops with lifecycle, does not
  require metrics, computes rates from ring samples, clamps counter resets, and
  produces warming-up errors with insufficient samples.
- `internalv2/app`: observer fanout wires top with metrics disabled and wires
  both when both features are enabled.
- `internalv2/access/api`: `/top/v1/snapshot` validates `window`, `view`, and
  `limit`, maps provider warming-up to `503`, and omits sections according to
  view.
- `cmd/wukongimv2`: config parsing covers `WK_TOP_API_ENABLE`,
  `WK_TOP_COLLECT_INTERVAL`, and `WK_TOP_HISTORY_WINDOW`.
- `cmd/wkcli/internal/top`: rendering handles partial source notes, multi-node
  aggregation, `--once --json`, and pressure sorting.

Focused verification after implementation:

```bash
go test ./internalv2/app ./internalv2/access/api ./cmd/wukongimv2 ./cmd/wkcli/...
```

Full verification remains:

```bash
go test ./...
```

## Rollout

1. Add config fields and defaults.
2. Add top model and collector in `internalv2/app`.
3. Wire observer fanout so top works with metrics disabled.
4. Expose `GET /top/v1/snapshot`.
5. Replace `wkcli top` placeholder with a non-interactive `--once` renderer.
6. Add interactive refresh after the snapshot path is stable.

This sequence lets the API contract stabilize before the TUI grows richer.
