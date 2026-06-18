# Cluster Realtime Monitor API Design

## Goal

Design the manager API that will feed the new `/cluster/monitor` card wall. The UI has already established the operator-facing model: 7 snapshot values and 12 troubleshooting cards ordered by control plane, Slot replication, Channel replication, internal network, runtime pressure, storage, and incidents. This API should preserve that model and expose raw monitor data without embedding presentation details.

## Scope

In scope:

- Add a cluster operations realtime monitor endpoint for the cluster monitor card wall.
- Use Prometheus as the primary cross-node time-series source.
- Allow a small control snapshot source for current aggregate health values that are not naturally represented as time-series yet.
- Return per-card availability so missing metrics do not block the whole page.
- Keep frontend ownership of i18n labels, colors, icons, chart layout, value formatting, and card order.

Out of scope:

- Do not reuse or overload the business monitor endpoint.
- Do not use `topCollector` for cluster-wide time series.
- Do not add alert rule editing, wallboard layouts, or custom dashboard composition.
- Do not expose high-cardinality per-channel or per-replica diagnostic data in this endpoint.
- Do not add node drill-down parameters in the first API pass.

## Recommended Approach

Add a dedicated endpoint:

```text
GET /manager/cluster-monitor/realtime
```

This keeps the cluster operations monitor independent from `GET /manager/monitor/realtime`, which is shaped around business message flow stages. The response should reuse the same high-level card-wall pattern, but define cluster-specific DTOs so the API can carry mixed numeric/text stats and explicit source metadata.

Data flow:

1. Browser calls `GET /manager/cluster-monitor/realtime?window=15m&step=20s`.
2. Manager route validates the query and enforces `cluster.node:r`.
3. Cluster monitor provider queries Prometheus for card series and current values.
4. Provider reads a bounded control snapshot for values that are current-state summaries.
5. Provider assembles snapshot entries and cards, marking each card `available=false` when its source is missing.
6. Frontend maps stable keys to localized labels and visual treatment.

## Endpoint Contract

Query parameters:

- `window`: one of `5m`, `15m`, `30m`, `1h`; default `15m`.
- `step`: optional duration; minimum `5s`, maximum `5m`; default is derived from `window` to produce roughly 30 to 90 points.

Auth:

- When manager auth is enabled, require `cluster.node:r`.

Response states:

- `ready`: all required sources are available and all required cards produced data.
- `partial`: the endpoint returned useful data, but at least one card or optional source is unavailable.
- `prometheus_disabled`: Prometheus is not configured for manager monitor queries.
- `prometheus_unavailable`: Prometheus is configured but the HTTP API cannot be queried.

Invalid `window` or `step` returns HTTP 400 with `invalid_request`. Disabled or unavailable data sources return HTTP 200 with an explicit `status` so the UI can render an operator-guided empty or partial state.

## Response Shape

```json
{
  "status": "ready",
  "generated_at": "2026-06-18T10:00:00Z",
  "window_seconds": 900,
  "step_seconds": 20,
  "scope": {
    "view": "cluster"
  },
  "sources": {
    "prometheus": {
      "enabled": true,
      "base_url": "http://127.0.0.1:9090",
      "query_ms": 42,
      "error": ""
    },
    "control_snapshot": {
      "enabled": true,
      "query_ms": 3,
      "error": ""
    }
  },
  "snapshot": [
    {
      "key": "nodesAlive",
      "metric_key": "nodesAlive",
      "value": 3,
      "unit": "",
      "tone": "normal",
      "source": "control_snapshot"
    }
  ],
  "cards": [
    {
      "key": "rpcSuccessRate",
      "stage": "internalNetwork",
      "tone": "normal",
      "unit": "%",
      "value": 99.96,
      "source": "prometheus",
      "available": true,
      "error": "",
      "series": [
        {
          "timestamp": 1781767200000,
          "value": 99.92
        }
      ],
      "stats": [
        {
          "key": "callsPerSecond",
          "value": 1280,
          "unit": "calls/s"
        },
        {
          "key": "errorsPerSecond",
          "value": 0.4,
          "unit": "errors/s"
        },
        {
          "key": "topReason",
          "text": "timeout"
        }
      ]
    }
  ]
}
```

Card stats should support numeric and text values:

```json
{
  "key": "topReason",
  "text": "timeout"
}
```

The provider should omit `value` when a stat is text-only and omit `text` when a stat is numeric-only. This keeps `topReason` and similar operational hints out of awkward numeric encodings.

## Source Model

Prometheus is the primary source for chart series, current values, and most numeric stats. It should query `/api/v1/query_range` for series and `/api/v1/query` only when an instant query is clearer or cheaper.

Control snapshot is a bounded read model for current cluster health summaries. It can be served by the app-level provider using existing management readers or narrow new read ports. It should never scan unbounded channel data or expose high-cardinality labels.

`topCollector` is not a first-pass source for this endpoint. It is node-local and useful for a future node detail view, but mixing it into aggregate cluster cards would make the global monitor misleading.

## Metric Cards

| Card key | Stage | Primary source | Primary expression or read | Notes |
| --- | --- | --- | --- | --- |
| `controllerProposeRate` | `controlPlane` | Prometheus | `sum(rate(wukongim_controller_decisions_total[<rate_window>]))` | Stats: avg, peak, rejected or failed decisions from controller task counters. |
| `controllerApplyGap` | `controlPlane` | Control snapshot plus new metric | Proposed gauge `wukongim_controller_apply_gap` | Needed to show committed-to-applied gap. Until the gauge exists, return the card unavailable with a clear error. |
| `slotLeaderStability` | `slotReplication` | Prometheus plus control snapshot | `rate(wukongim_slot_leader_elections_total[<rate_window>])` and current slot summary | Use snapshot for leader missing and quorum lost counts. |
| `slotReplicaLagP99` | `slotReplication` | New low-cardinality metric | `quantile(0.99, wukongim_slot_replica_lag_seconds)` | Proposed gauge labels are `slot_id` and `replica_node`; cardinality stays bounded by configured slots and nodes. |
| `channelISRHealth` | `channelReplication` | Control snapshot plus new aggregate gauges | Proposed gauges `wukongim_channelv2_isr_anomaly_channels{reason}` | Do not label by channel id. Return counts by reason only. |
| `channelAppendLatencyP99` | `channelReplication` | Prometheus | `histogram_quantile(0.99, sum(rate(wukongim_channelv2_append_duration_seconds_bucket[<rate_window>])) by (le))` | Stats: p50, p95, slow append count from append stage errors when available. |
| `internalTraffic` | `internalNetwork` | Prometheus | `sum(rate(wukongim_transport_sent_bytes_total[<rate_window>])) + sum(rate(wukongim_transport_received_bytes_total[<rate_window>]))` | Stats: tx, rx, peak. |
| `rpcSuccessRate` | `internalNetwork` | Prometheus | success ratio from `wukongim_transport_rpc_total` and `wukongim_transport_rpc_client_total` | Stats: calls/s, errors/s, timeouts. |
| `rpcLatencyP95` | `internalNetwork` | Prometheus | `histogram_quantile(0.95, sum(rate(wukongim_transport_rpc_duration_seconds_bucket[<rate_window>])) by (le))` | Include client-side duration when server-side data is absent. |
| `workqueuePressure` | `runtimePressure` | Prometheus | `max(wukongim_runtime_pool_queue_depth / clamp_min(wukongim_runtime_pool_queue_capacity, 1)) * 100` | Stats: busy pools, critical pools, queue full admissions. |
| `storageWriteP99` | `runtimePressure` | Prometheus | `histogram_quantile(0.99, sum(rate(wukongim_storage_commit_request_duration_seconds_bucket[<rate_window>])) by (le))` | Stats: p50, p95, flush or commit wait. |
| `incidentRate` | `incidentClosure` | Prometheus diagnostics | `sum(rate(wukongim_diagnostics_events_recorded_total{result=~"error|timeout|partial"}[<rate_window>]))` | Top reason is the highest `stage/result` pair. Critical and warning are derived from result class unless a severity metric is added. |

`rate_window` is the larger of `step * 3` and `30s`, capped by the selected `window`.

## Snapshot Entries

The snapshot strip should contain:

- `nodesAlive`: current alive node count from control snapshot or `wukongim_controller_nodes_alive`.
- `slotsReady`: current ready slot ratio from control snapshot.
- `controllerApplyGap`: current controller apply gap from the proposed gauge or snapshot.
- `channelISRAnomalies`: sum of ISR anomaly reasons.
- `rpcErrorRate`: recent RPC error percentage from transport counters.
- `queuePressure`: max runtime queue pressure percentage.
- `storageWriteP99`: current storage write P99 in milliseconds.

Snapshot entries should reference a card `metric_key` when they mirror a card. The UI can use this to keep scan-strip values and cards aligned.

## Missing Observability

The UI intentionally asked for the best operational indicators, and a few of them are not fully covered by current low-cardinality metrics. Implementation should add only the narrow measurements required by this API:

- `wukongim_controller_apply_gap`: gauge for ControllerV2 committed-to-applied gap.
- `wukongim_slot_replica_lag_seconds{slot_id,replica_node}`: bounded Slot replica lag gauge suitable for P99 aggregation.
- `wukongim_channelv2_isr_anomaly_channels{reason}`: aggregate Channel ISR anomaly counts by reason.

These metrics must avoid user ids, channel ids, message ids, and unbounded error strings. If a source cannot provide a low-cardinality aggregate, the card should remain unavailable rather than adding unsafe labels.

## Frontend Integration

The frontend should add cluster-specific API types and a `getClusterRealtimeMonitor` client method. The page can initially keep preview data as an explicit development fixture, but production rendering should use the API response once the backend exists.

Frontend behavior:

- `ready`: render all cards.
- `partial`: render available cards and show a non-blocking source warning.
- `prometheus_disabled`: render setup guidance for metrics and Prometheus.
- `prometheus_unavailable`: render connection guidance with `sources.prometheus.error`.
- Per-card `available=false`: keep the card visible with a clear unavailable state so the operator sees which metric is missing.

## Error Handling

- Invalid query parameters return HTTP 400 with the same manager JSON error shape used by existing routes.
- Prometheus disabled returns an empty card list and `status="prometheus_disabled"`.
- Prometheus unreachable returns `status="prometheus_unavailable"` when no useful data can be produced.
- Isolated query failures return `status="partial"` and mark affected cards unavailable.
- Control snapshot failure returns `status="partial"` when Prometheus data is still usable.

## Tests

Backend:

- Route rejects invalid `window` and `step`.
- Route requires `cluster.node:r` when auth is enabled.
- Disabled Prometheus returns `prometheus_disabled`.
- Fake Prometheus query-range responses map to card series, current values, stats, and sources.
- Single-card query failures produce `partial` and preserve successful cards.
- Missing low-cardinality metrics produce unavailable cards rather than HTTP 500.
- Control snapshot failures do not hide Prometheus-backed cards.

Frontend:

- Cluster monitor page requests `/manager/cluster-monitor/realtime` with selected range.
- Ready response renders the 12 card keys in UI order.
- Partial response renders available cards and unavailable-card states.
- Disabled and unavailable responses render operator guidance.
- Preview data is not used as a silent production fallback once API integration is enabled.

## Implementation Boundary

Place HTTP DTOs and query parsing in `internalv2/access/manager`. Place the Prometheus-backed provider in `internalv2/app`, following the existing business realtime monitor pattern. If control snapshot reads need new usecase ports, add narrow read-only methods under `internalv2/usecase/management` and wire them from `internalv2/app`.

Do not add a generic monitor framework in this pass. The business and cluster monitor endpoints can share small parsing helpers if they stay simple, but their providers and card definitions should remain separate.
