# Business Realtime Monitor Prometheus Design

## Goal

Build the manager realtime monitor API and wire the existing business monitor UI to real Prometheus-backed time series. The monitor page must not use `topCollector` and must not extend `pkg/metrics.DashboardCollector`; Prometheus is the single time-series source for this feature.

## Scope

In scope:

- Add a manager-facing realtime monitor endpoint for the business monitor card wall.
- Query Prometheus with PromQL for chart series, current values, and summary stats.
- Return an explicit disabled or unavailable state when Prometheus is not enabled or cannot be queried.
- Replace the old frontend monitor metrics helper and preview-only data path with the new API shape.
- Keep UI presentation concerns in the web layer: titles, i18n ids, colors, chart styles, and display labels.

Out of scope:

- Do not add compatibility for the old `/manager/monitor/metrics` response shape.
- Do not use `topCollector` for realtime monitor cards.
- Do not add or expand in-process dashboard ring buffers.
- Do not build long-term alerting rules in this pass.

## Architecture

The manager API gets a dedicated Prometheus-backed monitor provider. `internalv2/app` wires the provider only when the app has Prometheus configuration available. `internalv2/access/manager` owns HTTP query parsing, auth, error mapping, and response DTOs. The provider owns Prometheus HTTP calls, PromQL definitions, response normalization, and card/snapshot metric assembly.

Data flow:

1. Browser calls `GET /manager/monitor/realtime?window=15m&step=20s`.
2. Manager route validates `window` and `step`.
3. The monitor provider checks whether Prometheus is enabled and has an endpoint.
4. The provider issues PromQL `query_range` calls for chart series and `query` calls for instant values where needed.
5. The route returns a card-wall payload or a disabled/unavailable payload.
6. The frontend maps stable metric keys to localized labels, colors, and chart layout.

## Endpoint

`GET /manager/monitor/realtime`

Query parameters:

- `window`: one of `5m`, `15m`, `30m`, `1h`; default `15m`.
- `step`: optional duration, minimum `5s`, maximum `5m`; default is derived from `window` so the UI receives roughly 30 to 90 points.

Auth:

- When manager auth is enabled, the route requires `cluster.node:r`.

Response states:

- `ready`: Prometheus is enabled and all required queries succeeded.
- `partial`: Prometheus is enabled but one or more optional card queries failed or returned no data.
- `prometheus_disabled`: Prometheus is not configured for manager monitor queries.
- `prometheus_unavailable`: Prometheus is configured but the HTTP API is unreachable or returns an error.

## Response Shape

```json
{
  "status": "ready",
  "generated_at": "2026-06-18T10:00:00Z",
  "window_seconds": 900,
  "step_seconds": 20,
  "scope": {
    "view": "prometheus",
    "node_id": 1,
    "node_name": "node-1"
  },
  "sources": {
    "prometheus": {
      "enabled": true,
      "base_url": "http://127.0.0.1:9090",
      "query_ms": 18,
      "error": ""
    }
  },
  "snapshot": [
    {"key": "send", "metric_key": "sendRate", "value": 1240, "unit": "msg/s", "tone": "normal"},
    {"key": "delivery", "metric_key": "deliveryRate", "value": 1180, "unit": "msg/s", "tone": "normal"}
  ],
  "cards": [
    {
      "key": "sendRate",
      "stage": "sendEntry",
      "tone": "normal",
      "unit": "msg/s",
      "value": 1240,
      "series": [{"timestamp": 1781767200000, "value": 1188}],
      "stats": [
        {"key": "avg", "value": 1202},
        {"key": "peak", "value": 1320},
        {"key": "total", "value": 1081800}
      ],
      "available": true,
      "error": ""
    }
  ]
}
```

Disabled response:

```json
{
  "status": "prometheus_disabled",
  "generated_at": "2026-06-18T10:00:00Z",
  "window_seconds": 900,
  "step_seconds": 20,
  "scope": {"view": "prometheus"},
  "sources": {
    "prometheus": {
      "enabled": false,
      "base_url": "",
      "query_ms": 0,
      "error": "prometheus is disabled; set WK_METRICS_ENABLE=true and WK_PROMETHEUS_ENABLE=true"
    }
  },
  "snapshot": [],
  "cards": []
}
```

## Metric Cards

The first implementation should include these card keys:

- `sendRate`: `sum(rate(wukongim_gateway_messages_received_total[<rate_window>]))`
- `sendSuccessRate`: success SENDACK divided by total SENDACK using `wukongim_gateway_sendacks_total`
- `entryLatencyP99`: `histogram_quantile(0.99, sum(rate(wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket[<rate_window>])) by (le))`
- `commitRate`: `sum(rate(wukongim_message_append_total{result="ok"}[<rate_window>]))`
- `commitLatencyP99`: `histogram_quantile(0.99, sum(rate(wukongim_message_append_duration_seconds_bucket{result="ok"}[<rate_window>])) by (le))`
- `pendingCommitBacklog`: `sum(wukongim_message_committed_dispatch_queue_depth)`
- `deliveryRate`: `sum(rate(wukongim_delivery_push_rpc_routes_total{result="ok"}[<rate_window>]))`
- `deliveryLatencyP99`: `histogram_quantile(0.99, sum(rate(wukongim_delivery_push_rpc_duration_seconds_bucket{result="ok"}[<rate_window>])) by (le))`
- `fanOutRatio`: `sum(rate(wukongim_delivery_resolve_routes_total[<rate_window>])) / clamp_min(sum(rate(wukongim_gateway_messages_received_total[<rate_window>])), 1)`
- `retryQueueDepth`: `sum(wukongim_delivery_retry_queue_depth)`
- `pathErrorRate`: normalized error rate from SENDACK error reasons and delivery push errors.
- `activeConnections`: `sum(wukongim_gateway_connections_active)`

`rate_window` is the larger of `step * 3` and `30s`, capped by the selected `window`.

## Frontend Behavior

The monitor page should call the realtime endpoint. When `status` is `ready` or `partial`, it renders cards from API data. When status is `prometheus_disabled` or `prometheus_unavailable`, it renders a prominent empty state with the exact operator action:

- Enable metrics: `WK_METRICS_ENABLE=true`
- Enable Prometheus: `WK_PROMETHEUS_ENABLE=true`
- Confirm `WK_PROMETHEUS_LISTEN_ADDR` is reachable from the manager process

The previous preview model can remain only as test fixture or story data, not as production fallback.

## Error Handling

- Invalid `window` or `step` returns HTTP 400 with `invalid_request`.
- Disabled Prometheus returns HTTP 200 with `status="prometheus_disabled"` so the page can render a guided empty state.
- Prometheus connection errors return HTTP 200 with `status="prometheus_unavailable"` and `sources.prometheus.error`.
- Malformed Prometheus responses return `partial` when isolated to optional metrics, or `prometheus_unavailable` when the whole query path fails.

## Tests

Backend:

- Route rejects invalid `window` and `step`.
- Route requires `cluster.node:r` when auth is enabled.
- Disabled Prometheus returns the disabled payload.
- Fake Prometheus server responses map to card series and stats.
- Prometheus HTTP errors map to unavailable payload.
- Partial metric failures preserve successful cards and report `partial`.

Frontend:

- Monitor page requests `/manager/monitor/realtime` with selected range.
- Ready response renders metric cards and chart data.
- Disabled response renders Prometheus setup guidance.
- Partial response renders available cards and a non-blocking source warning.
- Old preview-only path is removed from production rendering.

## Migration Notes

This is a breaking cleanup. The old frontend helper and old monitor response types should be replaced, not kept. Existing top/workqueue APIs continue using `topCollector`, but the business realtime monitor does not depend on them.
