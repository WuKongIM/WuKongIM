# Dashboard Metrics API Design

- Status: Draft
- Owner: backend (manager)
- Created: 2026-05-15
- Scope: `pkg/metrics/dashboard_collector.go`, `internal/usecase/management/dashboard_metrics.go`, `internal/access/manager/dashboard_metrics.go`, route registration

## 1. Goal

Provide a single HTTP endpoint that returns time-series data for the 10 dashboard pulse metrics. The endpoint is designed to be consumed directly by the frontend `useDashboardPulse` hook, replacing the current deterministic mock.

All 10 metrics are derived from existing Prometheus instrumentation — no new application-level counters or histograms are needed.

## 2. Non-Goals

- No dependency on an external Prometheus server or PromQL.
- No per-node filtering (cluster-wide view only; node-level drill-down is a future extension).
- No persistent storage of metrics history (in-memory ring buffer only; restarts lose history).
- No WebSocket or push-based streaming; the frontend polls on manual refresh.

## 3. Endpoint

```
GET /manager/dashboard/metrics?window=30m&step=30s
```

### Parameters

| Param | Type | Default | Constraints |
|---|---|---|---|
| `window` | duration string | `30m` | min `1m`, max `1h` |
| `step` | duration string | `30s` | min `5s`, max `60s`; must divide window evenly |

### Permission

`cluster.overview` read — same as the existing `/manager/overview` endpoint.

### Response (200 OK)

```json
{
  "generated_at": "2026-05-15T12:00:00Z",
  "window_seconds": 1800,
  "step_seconds": 30,
  "points": 60,
  "metrics": {
    "send_per_sec": { "latest": 1234, "peak": 1580, "avg": 1150, "series": [1100, 1120, ...] },
    "deliver_per_sec": { "latest": 1100, "peak": 1400, "avg": 1050, "series": [...] },
    "connections": { "latest": 850, "peak": 920, "avg": 840, "series": [...] },
    "send_latency_p99_ms": { "latest": 45, "peak": 82, "avg": 48, "series": [...] },
    "delivery_latency_p99_ms": { "latest": 120, "peak": 210, "avg": 130, "series": [...] },
    "send_fail_rate_percent": { "latest": 0.1, "peak": 1.2, "avg": 0.3, "series": [...] },
    "delivery_fail_rate_percent": { "latest": 0.2, "peak": 2.0, "avg": 0.4, "series": [...] },
    "active_channels": { "latest": 320, "peak": 400, "avg": 310, "series": [...] },
    "retry_queue_depth": { "latest": 5, "peak": 18, "avg": 6, "series": [...] },
    "fan_out_rate": { "latest": 12.3, "peak": 18.0, "avg": 11.5, "series": [...] }
  }
}
```

Each metric object has the same shape: `{ latest: number, peak: number, avg: number, series: number[] }`.

- `series` length equals `window / step` (e.g. 60 points for 30m/30s).
- `latest` = last element of series.
- `peak` = max of series.
- `avg` = arithmetic mean of series (rounded to 1 decimal for rates/percentages, integer for counts).

### Error Responses

| Status | Condition |
|---|---|
| 400 | Invalid window/step params |
| 403 | Missing `cluster.overview` read permission |
| 503 | Collector not yet initialized (< step seconds since boot) |

## 4. Metric Derivation

### Source Mapping

| Dashboard Metric | Prometheus Source | Derivation |
|---|---|---|
| send_per_sec | `wukongim_gateway_messages_received_total` | rate (counter delta / step) |
| deliver_per_sec | `wukongim_gateway_messages_delivered_total` | rate |
| connections | `wukongim_gateway_connections_active` | gauge (last value in bucket) |
| send_latency_p99_ms | `wukongim_message_append_duration_seconds` | histogram p99 × 1000 |
| delivery_latency_p99_ms | `wukongim_delivery_push_rpc_duration_seconds` | histogram p99 × 1000 |
| send_fail_rate_percent | `wukongim_message_append_total{result=error}` / `..._total` | ratio × 100 |
| delivery_fail_rate_percent | `wukongim_delivery_push_rpc_total{result=error}` / `..._total` | ratio × 100 |
| active_channels | `wukongim_channel_active_channels` | gauge |
| retry_queue_depth | `wukongim_message_committed_dispatch_queue_depth` | gauge (sum all shards) |
| fan_out_rate | `wukongim_delivery_resolve_routes_total` / `messages_received_total` | ratio (routes per message) |

### Derivation Rules

- **Rate** (counter → per-second): `(value[t] - value[t-step]) / step_seconds`. Negative deltas (counter reset) are clamped to 0.
- **Gauge**: last sampled value within the step bucket.
- **Histogram p99**: computed from bucket boundaries using linear interpolation on the cumulative histogram snapshot.
- **Ratio**: `numerator_delta / denominator_delta × 100`. When denominator delta is 0, result is 0.
- **Fan-out**: `resolve_routes_delta / send_count_delta`. When send_count_delta is 0, result is 0.

## 5. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  HTTP Layer                                                   │
│  internal/access/manager/dashboard_metrics.go                 │
│  - Parse window/step params                                   │
│  - Call management.GetDashboardMetrics(ctx, window, step)     │
│  - Serialize JSON response                                    │
├──────────────────────────────────────────────────────────────┤
│  Usecase Layer                                                │
│  internal/usecase/management/dashboard_metrics.go             │
│  - Call collector.Query(window, step)                          │
│  - Map raw samples to response DTO                            │
├──────────────────────────────────────────────────────────────┤
│  Collector                                                    │
│  pkg/metrics/dashboard_collector.go                            │
│  - Ring buffer (3600 slots = 1h at 1 sample/sec)              │
│  - Background goroutine: tick every 1s, snapshot metrics      │
│  - Query(window, step) → []BucketedMetric                     │
├──────────────────────────────────────────────────────────────┤
│  Prometheus Registry (existing, unchanged)                    │
│  pkg/metrics/registry.go                                      │
│  - Gateway, Channel, Message, Delivery, Transport metrics     │
│  - Snapshot() methods on gauge-bearing structs                │
└──────────────────────────────────────────────────────────────┘
```

## 6. DashboardMetricsCollector

### Data Structures

```go
// pkg/metrics/dashboard_collector.go

// RawSample holds one second of snapshotted metric values.
type RawSample struct {
    At                 time.Time
    // Counters (cumulative)
    SendCount          int64
    DeliverCount       int64
    SendTotalCount     int64
    SendFailCount      int64
    DeliverTotalCount  int64
    DeliverFailCount   int64
    ResolveRoutesCount int64
    // Gauges (instantaneous)
    Connections        int64
    ActiveChannels     int64
    RetryQueueDepth    int64
    // Histogram snapshots (pre-computed p99 in seconds)
    SendLatencyP99     float64
    DeliveryLatencyP99 float64
}

// DashboardCollector samples metrics every second into a ring buffer.
type DashboardCollector struct {
    mu       sync.RWMutex
    ring     []RawSample
    head     int
    count    int
    capacity int // 3600
    registry *Registry
    stop     chan struct{}
}
```

### Lifecycle

```go
func NewDashboardCollector(registry *Registry) *DashboardCollector
func (c *DashboardCollector) Start()  // launches background goroutine
func (c *DashboardCollector) Stop()   // signals goroutine to exit
func (c *DashboardCollector) Query(window, step time.Duration) (QueryResult, error)
```

### Sampling (every 1 second)

```go
func (c *DashboardCollector) sample() RawSample {
    gw := c.registry.Gateway.Snapshot()
    ch := c.registry.Channel.Snapshot()
    msg := c.registry.Message.Snapshot()

    return RawSample{
        At:                 time.Now().UTC(),
        SendCount:          readCounter(c.registry.Gateway.MessagesReceivedTotal),
        DeliverCount:       readCounter(c.registry.Gateway.MessagesDeliveredTotal),
        Connections:        gw.ActiveConnections,
        SendTotalCount:     readCounterVec(c.registry.Message.AppendTotal, nil),
        SendFailCount:      readCounterVec(c.registry.Message.AppendTotal, labels{"result": "error"}),
        DeliverTotalCount:  readCounterVec(c.registry.Delivery.PushRPCTotal, nil),
        DeliverFailCount:   readCounterVec(c.registry.Delivery.PushRPCTotal, labels{"result": "error"}),
        ResolveRoutesCount: readCounter(c.registry.Delivery.ResolveRoutesTotal),
        ActiveChannels:     ch.ActiveChannels,
        RetryQueueDepth:    sumGaugeVec(msg.CommittedDispatchQueueDepthByShard),
        SendLatencyP99:     histogramQuantile(c.registry.Message.AppendDuration, 0.99),
        DeliveryLatencyP99: histogramQuantile(c.registry.Delivery.PushRPCDuration, 0.99),
    }
}
```

### Query Logic

```go
func (c *DashboardCollector) Query(window, step time.Duration) (QueryResult, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    cutoff := time.Now().Add(-window)
    bucketCount := int(window / step)

    // 1. Collect samples within window from ring buffer
    // 2. Assign each sample to a bucket index: (sample.At - cutoff) / step
    // 3. For each bucket, compute:
    //    - Rate metrics: (last.Counter - first.Counter) / step_seconds
    //    - Gauge metrics: last sample value in bucket
    //    - Latency: max p99 value in bucket
    //    - Ratio: delta(fail) / delta(total) * 100
    //    - Fan-out: delta(routes) / delta(send)
    // 4. Return series arrays + latest/peak/avg summaries
}
```

### Memory Budget

- Ring capacity: 3600 samples (1 hour at 1/s)
- Sample size: ~120 bytes
- Total: ~430 KB fixed allocation
- No heap growth over time

## 7. Histogram P99 Computation

Prometheus histograms expose cumulative bucket counts. To compute p99 without an external Prometheus server:

```go
func histogramQuantile(h prometheus.Observer, q float64) float64 {
    // Cast to prometheus.Histogram, call Write() to get dto.Metric
    // Read bucket boundaries and cumulative counts
    // Apply standard linear interpolation:
    //   find the bucket where cumulative_count / total >= q
    //   interpolate within that bucket's [lower, upper] range
}
```

This is the same algorithm Prometheus uses internally. The `prometheus/client_golang` library exposes `dto.Metric` via the `Write()` method on metric instances.

## 8. File Layout

```
pkg/metrics/
  dashboard_collector.go          — collector struct, ring buffer, sampling, query
  dashboard_collector_test.go     — unit tests with fake registry

internal/usecase/management/
  dashboard_metrics.go            — DTO types, GetDashboardMetrics method
  dashboard_metrics_test.go       — unit tests with mock collector

internal/access/manager/
  dashboard_metrics.go            — HTTP handler, param parsing, JSON response
  dashboard_metrics_test.go       — integration test with httptest
  routes.go                       — add route registration (1 line)
```

## 9. Bootstrap Integration

In the application bootstrap (`internal/app/` or equivalent):

```go
// Create collector with the existing metrics registry
dashCollector := metrics.NewDashboardCollector(metricsRegistry)
dashCollector.Start()
defer dashCollector.Stop()

// Pass to management usecase
mgmt := management.NewApp(..., dashCollector)
```

The collector starts sampling immediately. The first `step` seconds of data will be partial — the endpoint returns 503 if fewer than 2 samples exist.

## 10. Frontend Integration

Replace the mock in `web/src/pages/dashboard/use-dashboard-pulse.ts`:

```typescript
import { useCallback, useEffect, useState } from "react"
import { getDashboardMetrics } from "@/lib/manager-api"
import type { PulseData } from "./use-dashboard-pulse"

export function useDashboardPulse(generatedAt: string | null): PulseData | null {
  const [data, setData] = useState<PulseData | null>(null)

  const load = useCallback(async () => {
    const resp = await getDashboardMetrics({ window: "30m", step: "30s" })
    setData({
      sendPerSec: resp.metrics.send_per_sec,
      deliverPerSec: resp.metrics.deliver_per_sec,
      connections: resp.metrics.connections,
      sendLatencyP99: resp.metrics.send_latency_p99_ms,
      deliveryLatencyP99: resp.metrics.delivery_latency_p99_ms,
      sendFailRate: resp.metrics.send_fail_rate_percent,
      deliveryFailRate: resp.metrics.delivery_fail_rate_percent,
      activeChannels: resp.metrics.active_channels,
      retryQueueDepth: resp.metrics.retry_queue_depth,
      fanOutRate: resp.metrics.fan_out_rate,
    })
  }, [])

  useEffect(() => {
    if (generatedAt) void load()
  }, [generatedAt, load])

  return data
}
```

The `mocked` tag on pulse tiles is removed once this integration is live.

## 11. Testing Strategy

### Unit Tests

1. **`dashboard_collector_test.go`**:
   - Feed known counter/gauge values into a fake registry
   - Advance time, verify ring buffer stores correct samples
   - Query with various window/step combinations, verify series output
   - Verify counter reset handling (negative delta → 0)
   - Verify divide-by-zero protection (fan-out, fail rate)

2. **`dashboard_metrics_test.go`** (usecase):
   - Mock collector returning fixed QueryResult
   - Verify DTO mapping (field names, rounding)

3. **`dashboard_metrics_test.go`** (handler):
   - httptest with valid/invalid params
   - Verify 400 on bad params, 503 on cold start, 200 with correct JSON shape

### Integration

- Existing e2e test infrastructure can hit the endpoint after cluster boot
- Verify series length matches `window/step`
- Verify `latest == series[len-1]`

## 12. Rollout

1. Implement collector + tests (Go side)
2. Register route, deploy
3. Update frontend hook to call real endpoint (remove mock, remove `mocked` tag)
4. Keep `generatePulseData` as fallback when endpoint returns 503 (cold start grace period)

## 13. Open Questions

None — all 10 metrics map to existing Prometheus instrumentation. The only new code is the ring-buffer collector and the HTTP handler.
