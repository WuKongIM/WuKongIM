# Business Live Monitor Card Redesign

## Summary

Redesign the WuKongIM web business live monitor page (`/business/monitor`) into a cloud-console-style chart card wall. The page should focus on the end-to-end business message path rather than a generic metric grid: send entry, append/commit, online delivery, offline/retry, connection-capacity context, and error closure.

This phase is UI-first. It uses local preview data to optimize the monitoring layout and metric selection before designing or connecting the manager API.

## Goals

- Make the monitor page answer one primary question: is the global business message path healthy right now?
- Present metrics as independent chart cards similar to Alibaba Cloud or Tencent Cloud monitor panels.
- Order cards by message path stage so operators can quickly locate where degradation begins.
- Keep the existing route and navigation entry: `/business/monitor`.
- Avoid backend or API changes in this phase.

## Non-Goals

- No manager API contract changes.
- No backend collector changes.
- No node-level drill-down implementation.
- No alert rule editor, export, or fullscreen large-screen mode.
- No generic infrastructure monitoring such as CPU, memory, disk, or storage I/O cards unless they directly explain the message path.

## Current State

The current page already exists under the business navigation and renders a real-time chart grid backed by `getMonitorMetrics`. It groups charts by message flow and connection status, with node and time controls.

The new design changes the information architecture. The page should become a message-path monitor with many chart cards, and the first implementation should use preview data so the UI can be evaluated before the API shape is locked.

## Recommended Approach

Use a **path-stage card wall**.

The cards and compact supporting context are arranged in business path order:

1. Send entry
2. Append and commit
3. Online delivery
4. Offline and retry
5. Connection-capacity context
6. Error closure

This is better than a flat category dashboard because it lets operators compare adjacent stages: if send rate is normal but commit rate drops, the issue is likely before or inside append/commit; if commit is healthy but delivery latency rises, the issue is downstream.

## Page Structure

### Header

The header shows:

- Title: Live Monitor / 实时监控
- Description: global business message path health trends
- Preview badge: indicates that the current card data is UI preview data
- Last refresh time
- Live/Pause control
- Time range segmented control: `5m`, `15m`, `30m`, `1h`

The default scope is global aggregate. Node filtering can be added later after the API supports it.

### Snapshot Strip

A compact strip below the header shows the path's most important current values:

- Send rate
- Delivery rate
- Entry P99 latency
- Delivery P99 latency
- Error rate
- Retry queue depth
- Online connections

This strip is for fast scanning only. The chart cards remain the main content.

### Chart Card Wall

The main content renders 12 chart cards in a responsive grid:

- Desktop: 3 or 4 columns depending on available width.
- Tablet: 2 columns.
- Mobile: 1 column.

Each card is an independent monitor panel with a stable height and consistent internal layout.

## Chart Card Anatomy

Each chart card contains:

- Metric title, for example Send Rate or Retry Queue Depth.
- Stage label, for example Send Entry / Global Aggregate.
- Status indicator: normal, warning, critical, or preview.
- Primary value with unit, for example `12,480 msg/s`, `38.6 ms`, or `0.03%`.
- Trend chart using line or area chart.
- Three supporting stats, such as Avg, Peak, P99, Errors, Oldest Wait, or Affected Nodes.

The card should look like a monitoring panel, not a KPI tile. The trend chart must remain visually prominent.

## Card List

### Send Entry

1. **Send Rate**
   - Primary: current msg/s
   - Supporting: Avg, Peak, 5m Total
   - Purpose: detect traffic drops or sudden bursts.

2. **Send Success Rate**
   - Primary: success percentage
   - Supporting: Failed, Top Error, Affected Nodes
   - Purpose: detect client send failures.

3. **Entry Latency P99**
   - Primary: P99 latency in ms
   - Supporting: P50, P95, Peak P99
   - Purpose: detect slow send-entry handling.

### Append And Commit

4. **Commit Rate**
   - Primary: append/commit msg/s
   - Supporting: Avg, Peak, Batches
   - Purpose: compare write throughput with send entry throughput.

5. **Commit Latency P99**
   - Primary: P99 latency in ms
   - Supporting: P50, P95, Slow Commits
   - Purpose: detect slow storage, raft, or channel append behavior.

6. **Pending Commit Backlog**
   - Primary: pending messages
   - Supporting: Peak Queue, Oldest Wait, Affected Channels
   - Purpose: detect accumulation between entry and commit.

### Online Delivery

7. **Delivery Rate**
   - Primary: deliver msg/s
   - Supporting: Avg, Peak, 5m Total
   - Purpose: compare delivery capability with committed traffic.

8. **Delivery Latency P99**
   - Primary: P99 latency in ms
   - Supporting: P50, P95, Timeouts
   - Purpose: detect slow online delivery.

9. **Fan-out Ratio**
   - Primary: deliver/send ratio
   - Supporting: Avg, Peak, Active Channels
   - Purpose: show group/channel expansion pressure.

### Offline And Retry

10. **Offline Enqueue Rate**
    - Primary: offline enqueue msg/s
    - Supporting: Avg, Peak, Offline Users
    - Purpose: detect abnormal offline-message pressure.

11. **Retry Queue Depth**
    - Primary: retry queue depth
    - Supporting: Oldest Wait, Retry Success, Top Reason
    - Purpose: detect delivery failure accumulation.

### Error Closure

12. **Path Error Rate**
    - Primary: error percentage
    - Supporting: Protocol Errors, Rate Limited, Commit Errors
    - Purpose: show whether errors are spreading across the path.

## Connection Capacity

Connection capacity is important but should not dominate this page. It appears in the snapshot strip or as compact supporting context:

- Online connections
- Active users
- Active channels
- Disconnect rate

A deeper connection-focused page can live under the existing business connections area.

## Visual Rules

- Use a restrained management-console style with dense but readable cards.
- Keep card radius small and consistent with existing manager UI.
- Use color sparingly:
  - Green for normal status and healthy throughput.
  - Yellow for latency or backlog warnings.
  - Red for failures, retry buildup, and error rate.
  - Muted gray for preview or no-data states.
- Avoid a single-color page. Throughput, latency, backlog, and errors should have distinct but restrained chart colors.
- Make chart cards stable in height so changing labels or values does not shift the grid.
- Avoid visible instructional copy. The UI should present monitoring data directly.

## Preview Data Model

The UI phase should use local preview data:

- No `getMonitorMetrics` call in the first card-wall implementation.
- Generate deterministic sample series for all 12 metrics.
- Simulate realistic shapes:
  - Throughput has steady variation and occasional bursts.
  - Latency has brief spikes.
  - Failure rates stay low but can pulse.
  - Retry depth can slowly accumulate and recover.
  - Backlog can lag behind send bursts.
- Show a small preview badge so operators do not mistake the data for production telemetry.

## Component Direction

Expected frontend structure:

```text
src/pages/monitor/
  page.tsx
  types.ts
  preview-data.ts
  components/
    monitor-toolbar.tsx
    monitor-snapshot-strip.tsx
    monitor-metric-card.tsx
    monitor-card-grid.tsx
```

The existing chart grid components can be replaced if the new components are clearer. Keep route and navigation wiring unchanged.

## Testing

Focused frontend tests should verify:

- The page renders the preview badge.
- The page renders 12 monitor chart cards.
- Key path-stage metrics are visible: Send Rate, Commit Rate, Delivery Rate, Retry Queue Depth, Path Error Rate.
- The toolbar includes time range and Live/Pause controls.
- The implementation does not require `getMonitorMetrics` while in preview mode.

Run focused tests before wider verification:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx
cd web && bunx tsc -b
```

## Future API Considerations

After the UI is validated, design the API around the card model rather than around arbitrary metric names. A future response can return:

- Global generated time, window, and step.
- Card definitions or stable card keys.
- Series points per card.
- Latest, average, peak, and supporting stats.
- Scope capabilities such as global aggregate and node filter.
- Status severity per card.

The API should preserve the same path-stage ordering used by the UI.
