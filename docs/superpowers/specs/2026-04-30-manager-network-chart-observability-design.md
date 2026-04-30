# Manager Network Chart Observability Design

## Goal

Redesign the manager Observability > Network page as a chart-first local-node cluster dashboard, using shadcn chart patterns on top of Recharts. The page should make traffic, RPC, health, pool, channel replication, and recent network behavior readable at a glance while preserving the existing local-node scope and single-node cluster semantics.

## Recommended Approach

Use a hybrid frontend/backend upgrade:

1. Keep `/manager/network/summary` as the page's single request.
2. Add an additive `history` object to the response, derived from the existing app-level network observability buckets.
3. Render all primary sections with shadcn `ChartContainer` wrappers and Recharts primitives.
4. Keep compact details and empty states for precise values, but make tables secondary rather than primary.

This is the best trade-off because it gives real time-series charts without inventing data, adding a polling collector, or changing access-layer responsibilities. The backend already stores bounded one-minute buckets for traffic/RPC/errors, so exposing them is lower risk than building a new history subsystem.

## Scope And Semantics

- The view remains `local_node`: all charts are observations from the current node.
- The UI must not imply a complete cluster-wide network matrix.
- Peer traffic remains unavailable unless the collector can attribute traffic to peers; charts must label traffic as local total by message type.
- A single-node cluster remains a healthy empty outbound-peer state.
- Expected `channel_long_poll_fetch` wait expirations remain neutral samples, not abnormal failures.
- Access layer stays thin. Aggregation lives in `internal/usecase/management`; collection lives in `internal/app`.

## Backend Data Shape

Extend the existing summary response with:

```json
{
  "history": {
    "window_seconds": 60,
    "step_seconds": 5,
    "traffic": [
      { "at": "2026-04-30T09:00:00Z", "tx_bytes": 1024, "rx_bytes": 512 }
    ],
    "rpc": [
      { "at": "2026-04-30T09:00:00Z", "calls": 20, "success": 18, "errors": 2, "expected_timeouts": 1 }
    ],
    "errors": [
      { "at": "2026-04-30T09:00:00Z", "dial_errors": 1, "queue_full": 0, "timeouts": 2, "remote_errors": 0 }
    ]
  }
}
```

The app collector builds this from existing bucket maps when `NetworkSnapshot(now)` is called. It should aggregate into fixed five-second steps for the current one-minute window. The usecase copies the history into `NetworkSummary`; the manager access DTO maps it to JSON; web TypeScript types add matching fields.


## Expected Long-poll Timeout Classification

The backend must not infer expected long-poll wait expiry from elapsed time or from a generic transport `timeout` result. The safe source is the decoded `LongPollFetchResponse.TimedOut` flag from `pkg/channel/transport`.

Implementation rule:

1. Add a generic transport-client hook that lets a caller classify a successful RPC response before the observer records the final result.
2. Use that hook only for `LongPollFetch`: after decoding the response, classify `TimedOut == true` as `expected_timeout`; classify non-timeout successful responses as `ok`; keep wire/RPC deadline failures as `timeout`.
3. The app network collector treats `expected_timeout` as a completed neutral sample: increment `Calls1m` and `ExpectedTimeout1m`, do not increment `Success1m` or abnormal timeout/error counters, and do not emit a warning event.
4. History uses the same result labels. `history.rpc.expected_timeouts` comes only from `expected_timeout` buckets; `history.rpc.errors` and `history.errors.timeouts` exclude expected wait expiries.

This keeps application-level long-poll expiry separate from transport/RPC deadline failure and preserves current success-rate semantics.

## Frontend Layout

1. Header
   - Existing title, description, refresh action, and scope badges.
   - Keep generated timestamp and single-node cluster badge.

2. Chart command deck
   - Node Health donut chart: alive, suspect, dead, draining.
   - Pool Connections stacked bar: active vs idle.
   - Error Mix bar/donut: dial, queue-full, abnormal timeout.
   - Traffic Flow area/line chart from `history.traffic`.

3. Traffic section
   - Horizontal bar chart grouped by message type and direction.
   - Small cards for TX/RX totals and bps.
   - Explicit local-total disclaimer.

4. RPC section
   - Services chart with calls, errors, expected timeouts, and p95 labels/tooltips.
   - Compact service detail rows for exact last-seen/success rate values.

5. Peers section
   - Peer cards become mini chart panels: cluster/data-plane active/idle bars, RPC/error badges.
   - Empty state remains healthy for single-node cluster.

6. Channel replication section
   - Data-plane pool bar chart.
   - Long-poll configuration as limit bars/cards.
   - Long-poll expected expiries shown separately from abnormal failures.

7. Discovery and events
   - Discovery remains compact key/value config.
   - Events render as a timeline with severity badges.

## Chart Library

Add shadcn chart support by introducing `web/src/components/ui/chart.tsx` and adding `recharts` to the web dependencies. Use the existing `--chart-*` CSS variables and current shadcn/Tailwind styling so the page fits the admin shell.

## Error Handling And Empty States

- Loading, 403, 503, and retry behavior stay unchanged.
- When history is empty, chart sections should render a clear empty state or fall back to snapshot totals.
- Invalid zero timestamps still render `Insufficient samples`.
- Charts must remain accessible through textual labels and surrounding metric values, because SVG internals are not enough for tests or screen readers.

## Testing

Backend:
- App collector test for fixed-step traffic/RPC/error history from existing buckets.
- Usecase test that copies history and preserves local-node/single-node semantics.
- Access handler test that serializes the new `history` field.

Frontend:
- API test fixture includes `history`.
- Network page tests assert chart-first section names and preserve existing behavior tests.
- Page-shell/topbar tests continue to mock the summary successfully.

## Non-Goals

- No global cluster-wide network matrix.
- No per-peer traffic inference.
- No new background sampler for gauges in this iteration.
- No separate history endpoint unless future pages need it.
