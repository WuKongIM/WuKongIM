# Dashboard Split Design

## Summary

Remove the top-level Overview section from the web manager shell and split its dashboard and monitor content into Cluster Ops and Business Management. Cluster Ops gets a default dashboard focused on cluster health, internal transport, RPC quality, replication health, and topology watermarks. Business Management gets a default dashboard focused on client-facing message quality, online usage, retry pressure, and management entry points. Missing data can use clearly labeled mock or derived values until manager APIs expose the exact fields.

## Goals

- Remove the Overview top tab and avoid a generic cross-domain dashboard.
- Make `/cluster/dashboard` the default Cluster Ops landing page.
- Make `/business/dashboard` the default Business Management landing page.
- Preserve detailed monitoring by splitting it into cluster network diagnostics and business live monitoring.
- Reuse existing manager APIs first; only mock data that is currently unavailable.

## Non-Goals

- Do not add new backend manager endpoints for this first UI split.
- Do not remove existing detailed pages such as network diagnostics, slots, channel cluster, or message search.
- Do not make mock data look authoritative; it must be labeled as sample or derived in the UI.

## Navigation

Top-level sections become:

1. Cluster Ops
2. Business
3. System

Route behavior:

- `/` redirects to `/cluster/dashboard`.
- `/dashboard` redirects to `/cluster/dashboard` for bookmark compatibility.
- `/monitor` redirects to `/business/monitor` for bookmark compatibility.
- `/cluster/dashboard` renders the Cluster Ops dashboard.
- `/business/dashboard` renders the Business dashboard.
- `/business/monitor` renders the existing business live monitor charts.
- `/cluster/diagnostics?tab=network` remains the detailed cluster network monitoring page.

Cluster Ops sidebar order:

1. Dashboard
2. Nodes
3. Slots
4. Channel Cluster
5. Tasks
6. Topology
7. Diagnostics

Business sidebar order:

1. Dashboard
2. Live Monitor
3. Users
4. Channels
5. Messages
6. System Users

## Cluster Ops Dashboard

The Cluster Ops dashboard answers: is the cluster healthy, and where should an operator inspect next?

### Layout

1. Health hero
   - Cluster verdict: healthy, degraded, or critical.
   - Controller leader id.
   - Generated timestamp and refresh action.
   - Summary text for nodes alive, slots ready, and active incidents.

2. Core metric strip
   - Nodes alive / total.
   - Slots ready / total.
   - Failed or retrying tasks.
   - Channel ISR or no-leader anomalies.
   - Internal message rate.
   - RPC error rate.
   - RPC p95 or p99 latency when space allows.

3. Internal link trends
   - Main chart for internal transport tx/rx and internal RPC/message rate.
   - Secondary values for RPC calls/s, RPC errors, inflight, queue full, and timeouts.
   - Primary attention should go to RPC error rate and internal message/s.

4. Replication health
   - Slot readiness donut.
   - Slot quorum lost, leader missing, and unreported counts.
   - Channel cluster healthy, ISR insufficient, and no-leader counts.
   - Links to `/cluster/slots` and `/cluster/channels`.

5. Active incidents
   - Slot quorum lost.
   - Slot leader missing.
   - Slot sync mismatch.
   - Distributed task failures and retries.
   - Controller raft degraded states.
   - RPC timeout or queue pressure events from network summary.

6. Topology and watermarks
   - Node status, local marker, controller role, slot count, leader count.
   - Controller raft health.
   - First, applied, and snapshot indexes.
   - Per-node RPC error or latency summary when available.

### Data Sources

Use real data in this order:

- `GET /manager/overview` for cluster summary and slot/task anomalies.
- `GET /manager/nodes` for node, controller role, slot stats, and raft watermarks.
- `GET /manager/distributed-tasks/summary` or existing task list data when needed.
- `GET /manager/channel-cluster/summary` for channel health.
- `GET /manager/network/summary` for internal transport, RPC, errors, events, and history.

Derived values:

- RPC error rate = abnormal RPC failures / completed RPC calls. Expected long-poll expiries should not count as abnormal failures.
- RPC calls/s = recent RPC calls divided by the network history step or window.
- Internal transport trend = `history.traffic` tx/rx values.
- Internal message/s can be derived from RPC/message call history when a direct metric is unavailable.

Mock values:

- Only use mock values for missing dashboard-only summaries.
- Label them as `sample` when fully mocked.
- Label them as `derived` when calculated from available history rather than directly reported by the API.

## Business Dashboard

The Business dashboard answers: is client-facing messaging healthy, and which business management area needs attention?

### Layout

1. Business health hero
   - Business traffic verdict: normal, degraded, or critical.
   - Generated timestamp and refresh action.
   - Summary text for send/deliver rate, failure rate, online connections, and retry queue.

2. Business quality metric strip
   - Send msg/s.
   - Deliver msg/s.
   - Send p99 latency.
   - Delivery p99 latency.
   - Send failure rate.
   - Delivery failure rate.
   - Online connections.
   - Active channels.
   - Retry queue depth.
   - Fan-out rate.

3. Message trend section
   - Larger trend chart for send and delivery rates.
   - Latency p99 trend.
   - Failure rate trend.
   - The dashboard should show the key summary; `/business/monitor` remains the detailed chart page.

4. Business risk section
   - Retry queue pressure.
   - Elevated send or delivery failure rate.
   - Delivery latency regression.
   - Low delivery rate compared to send rate.
   - Links to `/business/messages` and `/business/monitor`.

5. Management entry cards
   - Users.
   - Channels.
   - Messages.
   - System Users.
   - Cards may show real summaries if available; otherwise they are navigation cards with sample labels for unavailable counts.

### Data Sources

Use real data in this order:

- `GET /manager/dashboard/metrics` for compact dashboard series.
- `GET /manager/monitor/metrics` for richer time-series charts and node filtering.
- Existing business list APIs only when a summary is already cheap and available.

Mock values:

- User count, channel count, or system user count can be sample values in v1 if the API does not expose summary counts.
- Sample labels must be visible enough that operators do not treat those counts as authoritative.

## Component Design

Suggested file layout:

```text
web/src/pages/cluster-dashboard/
  page.tsx
  view-model.ts
  view-model.test.ts
  components/
    cluster-health-hero.tsx
    cluster-link-trends.tsx
    cluster-metric-strip.tsx
    cluster-replication-health.tsx
    cluster-incident-list.tsx
    cluster-topology-watermarks.tsx

web/src/pages/business-dashboard/
  page.tsx
  view-model.ts
  view-model.test.ts
  components/
    business-health-hero.tsx
    business-metric-strip.tsx
    business-message-trends.tsx
    business-risk-list.tsx
    business-entry-cards.tsx
```

Reusable pieces can move into `web/src/components/dashboard/` if both dashboards need the same card chrome, mini sparkline, metric source badge, or refresh header. Avoid keeping a generic `pages/dashboard` domain after the split.

The existing monitor page can remain in `web/src/pages/monitor` initially, but route and navigation should present it as `/business/monitor`. A later cleanup can rename the folder to `business-monitor` if that improves clarity.

## Error And Loading States

- Each dashboard should use `ResourceState` for full-page initial loading and fatal errors.
- If a secondary API fails, show the rest of the dashboard and render a local warning card for the missing section.
- Mock fallback should only apply to missing optional summaries, not to failed authoritative health APIs.
- Refresh buttons should keep stale successful data visible while a refresh is in flight.

## Internationalization

Add separate message namespaces:

- `clusterDashboard.*`
- `businessDashboard.*`

Keep old `dashboard.*` keys only while components still reference them. Remove or rename old keys when the generic dashboard page is deleted.

## Testing

### Navigation Tests

- Topbar does not render the Overview section.
- `/` redirects to `/cluster/dashboard`.
- `/dashboard` redirects to `/cluster/dashboard`.
- `/monitor` redirects to `/business/monitor`.
- Cluster sidebar includes Dashboard first.
- Business sidebar includes Dashboard and Live Monitor first.

### Cluster Dashboard Tests

- Successful API responses render nodes, slots, channel health, RPC error rate, and internal message/s.
- Network summary derives RPC error rate without counting expected long-poll expiries as abnormal failures.
- Traffic and RPC history derive stable trend series.
- Failed overview or nodes requests render an error `ResourceState`.
- Failed network summary keeps health content visible and shows a network section warning.

### Business Dashboard Tests

- Metrics response renders send, deliver, latency, failure, connection, active channel, retry queue, and fan-out metrics.
- Missing optional business summaries render sample badges.
- Metrics request failure renders an error `ResourceState`.
- Entry cards link to Users, Channels, Messages, and System Users.

### View Model Tests

- Cluster verdict is healthy, degraded, or critical based on existing anomaly rules.
- RPC error rate and calls/s calculations handle zero calls.
- Business verdict escalates on high failure rate, latency, or retry queue depth.
- Mock/sample fallback values are deterministic.

## Rollout Plan

1. Add the two new dashboard pages and view models.
2. Update navigation sections and legacy redirects.
3. Wire `/business/monitor` to the existing monitor page.
4. Move or adapt old dashboard components into cluster/business-specific components.
5. Add i18n keys and tests.
6. Update `web/README.md` page/API matrix and legacy redirect notes.
