# Workqueue Monitor Design

## Summary

Add a local-node Workqueue monitor under the Web cluster operations area. The
first version shows runtime queue and worker pressure for the currently queried
`wukongimv2` node only. It does not fan out across cluster nodes and does not
introduce deployment branches outside the existing single-node cluster or
multi-node cluster semantics.

The UI will live under `web/` at `/cluster/workqueues`. It will read a new
manager-facing, read-only runtime endpoint that reuses the existing
`internalv2` top collector rather than scraping Prometheus or adding a separate
collector.

## Goals

- Surface current local runtime pressure for workqueue-backed and queue-like
  runtimes in the cluster operations navigation.
- Use `wukongimv2` APIs only. The legacy manager API surface is not a target.
- Keep data collection node-local for v1.
- Reuse the existing `topCollector` and `TopPressure` model so the page remains
  available when Prometheus metrics are disabled.
- Explain whether pressure is caused by queue depth, worker saturation, wait
  latency, task latency, or admission failures.
- Keep labels low-cardinality. No UID, channel ID, session ID, or per-target
  identifiers are exposed as metric labels.

## Non-Goals

- No cluster-wide workqueue aggregation in v1.
- No remote-node fan-out from the manager endpoint.
- No new Prometheus dependency for the Web page.
- No queue mutation, draining, resizing, or administrative control action.
- No attempt to make `pkg/workqueue` aware of Web, manager APIs, or concrete
  metric names.

## Current Context

`pkg/workqueue` already exposes low-cardinality observer events for bounded
pools, bounded batch pools, and sharded mailboxes:

- `BoundedPoolObservation`
- `ShardedMailboxObservation`

`BoundedBatchPool` reuses `BoundedPoolObservation`. `BoundedWorkerQueue` does
not currently expose an observer contract, so it is not part of this v1 adapter
unless that package contract is explicitly extended during implementation.

`internalv2/app` already owns runtime observability wiring. The existing
`topCollector` samples local process resources and runtime pressure into
`/top/v1/snapshot`, independent of Prometheus. The top snapshot already has:

- `TopPressure`
- `TopPressureItem`
- pressure score and level calculation
- sticky pressure alerts

`internalv2/access/manager` is the Web-facing administration API in
`wukongimv2`. The Web app already calls `/manager/*` paths through the manager
API client. Because `/top/v1/snapshot` is exposed by `internalv2/access/api`,
the Workqueue page should not call it directly from Web unless deployments also
route the API listener through the same base URL. Instead, manager should expose
a read-only local runtime view backed by the same top provider.

## Recommended Approach

Add `GET /manager/runtime/workqueues`.

This endpoint will:

- require the existing `cluster.node:r` permission when manager auth is on;
- accept `window` and `limit` query parameters;
- force `view=runtime` internally;
- call the same `TopSnapshotProvider` used by `/top/v1/snapshot`;
- return a manager-specific response focused on workqueue/runtime pressure;
- map top collector warming-up to `503 service_unavailable`;
- return local-node scope only.

The endpoint name uses `workqueues` because that is the operator-facing page,
but the response keeps the broader runtime pressure semantics. This is
intentional: many important queues are not direct `pkg/workqueue` instances, but
operators care about them in the same pressure view.

## Backend Design

### Manager Wiring

Extend `internalv2/access/manager.Options` with a top snapshot provider:

```go
// Top provides local runtime pressure snapshots for manager read-only views.
Top accessapi.TopSnapshotProvider
```

`internalv2/app.wireManager` will pass `a.topProvider` to manager when
configured. `Top.APIEnabled` still controls whether the collector exists. If no
top provider is wired, `/manager/runtime/workqueues` returns
`service_unavailable` instead of returning a false empty result.

### Manager Route

Add the route in `internalv2/access/manager`:

```text
GET /manager/runtime/workqueues
```

Query parameters:

- `window`: duration string, default `10s`, minimum `2s`.
- `limit`: positive integer, default `100`, maximum `200`.

The handler constructs:

```go
accessapi.TopSnapshotQuery{
    Window: parsedWindow,
    View:   accessapi.TopViewRuntime,
    Limit:  parsedLimit,
}
```

The handler then maps `accessapi.TopSnapshot` to a manager DTO. The DTO should
not leak internal error strings.

### Response Model

```json
{
  "generated_at": "2026-06-17T10:00:00Z",
  "window_seconds": 10,
  "scope": {
    "view": "local_node",
    "node_id": 1,
    "node_name": "node-1",
    "ready": true
  },
  "summary": {
    "overall_level": "busy",
    "total": 8,
    "ok": 5,
    "busy": 2,
    "degraded": 1,
    "critical": 0,
    "hottest": {
      "component": "gateway",
      "pool": "async_send",
      "queue": "send",
      "priority": "none",
      "level": "degraded",
      "score": 0.82
    }
  },
  "items": [
    {
      "component": "gateway",
      "pool": "async_send",
      "queue": "send",
      "priority": "none",
      "level": "degraded",
      "score": 0.82,
      "depth": 820,
      "capacity": 1000,
      "inflight": 0,
      "workers": 0,
      "wait_p99_ms": 12.4,
      "task_p99_ms": 0,
      "admission_error_per_sec": 0.1,
      "hint": "queue depth is approaching capacity"
    }
  ],
  "sources": {
    "collector": {
      "available": true,
      "sample_count": 10
    },
    "metrics": {
      "enabled": false,
      "required": false
    },
    "notes": []
  }
}
```

### Top Collector Enhancements

`TopPressureItem` already has fields for queue wait, task duration, and
admission error rate. The collector should populate them for runtime pressure:

- `wait_p99_ms`: p99 of
  `pressure.<component>.<pool>.<queue>.<priority>.wait`.
- `task_p99_ms`: p99 of task duration histograms associated with the same
  component and pool.
- `admission_error_per_sec`: rate of non-`ok` admission outcomes for the same
  component, pool, queue, and priority.

The score still uses the maximum of:

- `depth / capacity`
- `inflight / workers`

Wait latency, task latency, and admission error rate enrich diagnosis but do
not change the v1 score. This keeps the existing pressure level behavior stable.

### Workqueue Adapter

Add an internal app-level adapter that can translate direct `pkg/workqueue`
observations into the runtime pressure model:

- `BoundedPoolObservation` from `BoundedPool` or `BoundedBatchPool` updates
  queue gauges, worker gauges, wait histograms, task histograms, and admission
  counters.
- `Kind == capacity/depth/worker` updates queue and worker gauges.
- `Kind == admission` increments admission outcome counters.
- `Kind == wait` records queue wait duration.
- `Kind == task` records task duration.
- `ShardedMailboxObservation` maps shard-local depth using the mailbox name as
  pool and `"shard"` as the queue label.

The adapter belongs in `internalv2/app`, not in `pkg/workqueue`. Runtime
packages may still use their typed observers when they already provide richer
domain names. The adapter is for current and future direct workqueue users that
do not already have a domain-specific observer. It should not add new labels
from submitted item contents.

## Frontend Design

### Navigation

Add a Cluster Ops item:

- route: `/cluster/workqueues`
- legacy redirect: `/workqueues -> /cluster/workqueues`
- title: `Workqueue`
- description: local runtime queue and worker pressure
- icon: a runtime/pressure-oriented Lucide icon such as `Gauge` or `Activity`

### Page Structure

The page should be a dense operational view, not a landing page.

Top controls:

- time window: `10s`, `30s`, `1m`
- refresh button
- auto refresh toggle, default on, 5 second interval
- abnormal-only toggle, default off
- component filter derived from returned items

Summary strip:

- overall level
- total pressure item count
- busy/degraded/critical counts
- hottest queue label
- generated time and window

Main table:

- Level
- Component
- Pool
- Queue
- Priority
- Depth / Capacity
- Inflight / Workers
- Score
- Wait P99
- Task P99
- Admission errors/s
- Hint

Rows sort by score descending, then severity, then component/pool/queue. The
table must stay readable on narrow screens through horizontal scrolling rather
than text overlap.

Details:

Clicking a row opens a compact detail panel or expanded row that shows the raw
item and a short interpretation of:

- queue utilization
- worker utilization
- wait latency
- task latency
- admission failures

### Frontend API Client

Add a manager API client function:

```ts
getRuntimeWorkqueues(params?: { window?: string; limit?: number })
```

It calls `/manager/runtime/workqueues`, not `/top/v1/snapshot`. This keeps Web
traffic on the manager listener and avoids depending on the separate API
listener being reachable from the browser.

### Empty and Error States

- `503 top collector warming up`: show a warm-up state and keep refresh
  available.
- top provider not configured: show a service unavailable state with a hint to
  enable `WK_TOP_API_ENABLE`.
- empty item list: show "current node has no runtime queue pressure samples".
- `403`: use the existing forbidden resource state.
- other errors: use the existing retryable error state.

## Files Expected To Change

Backend:

- `internalv2/access/manager/server.go`
- `internalv2/access/manager/runtime_workqueues.go`
- `internalv2/access/manager/FLOW.md`
- `internalv2/app/wiring.go`
- `internalv2/app/top_collector.go`
- `internalv2/app/top_collector_test.go`
- `internalv2/access/manager/server_test.go` or a dedicated manager route test

Frontend:

- `web/src/lib/manager-api.ts`
- `web/src/lib/manager-api.types.ts`
- `web/src/lib/manager-api.test.ts`
- `web/src/lib/navigation.ts`
- `web/src/lib/navigation.test.ts`
- `web/src/app/router.tsx`
- `web/src/app/router.test.tsx`
- `web/src/pages/workqueues/page.tsx`
- `web/src/pages/workqueues/page.test.tsx`
- `web/src/i18n/messages/zh-CN.ts`
- `web/src/i18n/messages/en.ts`

Documentation:

- `internalv2/access/manager/FLOW.md`
- `internalv2/app/FLOW.md` if top collector or manager wiring descriptions
  change materially
- `pkg/workqueue/FLOW.md` only if the package-level observer contract changes

## Testing Plan

Backend unit tests:

- pressure score uses queue depth/capacity.
- pressure score also uses inflight/workers.
- pressure items include wait p99 when wait histograms exist.
- pressure items include task p99 when task histograms exist.
- pressure items include non-`ok` admission error rate.
- manager route maps warming-up to `503`.
- manager route returns `service_unavailable` when no top provider is
  configured.
- manager route requires `cluster.node:r` when auth is enabled.

Frontend tests:

- API client builds `/manager/runtime/workqueues?window=10s&limit=100`.
- navigation exposes `/cluster/workqueues`.
- route renders the Workqueue page.
- page renders summary and table from a real response fixture.
- abnormal-only toggle hides `ok` rows.
- component filter narrows visible rows.
- refresh calls the API.
- warming-up and empty states render correctly.

Focused verification commands:

```bash
go test ./internalv2/app ./internalv2/access/manager
cd web && npm test -- --run src/lib/manager-api.test.ts src/lib/navigation.test.ts src/app/router.test.tsx src/pages/workqueues/page.test.tsx
```

If implementation touches shared Web utilities or wider manager API behavior,
expand to the relevant package tests.

## Rollout Notes

Operators should enable `WK_TOP_API_ENABLE=true` for this page. Prometheus
metrics may stay disabled. In a single-node deployment, the page describes the
local node in the single-node cluster. In a multi-node cluster, v1 still
describes only the node serving the manager request.

Future work can add cluster fan-out by querying peer manager runtimes through
node RPC or by adding a cluster-aware manager aggregation endpoint. That is
intentionally out of scope for this version.
