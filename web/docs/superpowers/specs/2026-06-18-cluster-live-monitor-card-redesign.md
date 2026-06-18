# Cluster Live Monitor Card Redesign

## Summary

Add a new cluster operations live monitor page at `/cluster/monitor`. The page is a UI-first monitoring card wall for cluster runtime health. It follows the visual style of the existing business live monitor page at `/business/monitor`, but the metric model is focused on cluster operations: controller progress, Slot and Channel replication, node-to-node transport, runtime queue pressure, storage write latency, and incident closure.

This phase intentionally does not connect to a backend API. It uses deterministic preview data so the operator-facing layout and metric choices can be evaluated before the API contract is designed.

## Goals

- Add a clear cluster operations live monitor entry under the cluster navigation.
- Let operators answer one question quickly: is the cluster runtime healthy right now?
- Present metrics as independent monitor cards with current value, status, trend chart, and supporting stats.
- Arrange cards in the order an operator would troubleshoot the cluster path: control plane, replication, network, queues, storage, incidents.
- Keep `/business/monitor` unchanged and use it only as the visual reference.
- Avoid backend, collector, or manager API changes in this phase.

## Non-Goals

- No real manager API integration.
- No Prometheus or external metrics dependency.
- No alert rule editor, fullscreen wallboard, export, or custom dashboard builder.
- No generic host monitoring such as CPU, memory, and disk capacity unless it directly explains cluster runtime behavior.
- No node drill-down drawer in the first UI pass.

## Recommended Approach

Use a cluster-runtime path card wall.

The card order should help an operator locate where degradation starts:

1. Controller and control-plane progress.
2. Slot replication and leader stability.
3. Channel ISR and append health.
4. Internal traffic and RPC quality.
5. Runtime queue and storage pressure.
6. Active incident rate.

This is better than grouping strictly by resource type because cluster failures often cross layers. A path-ordered page lets operators compare adjacent layers and reason about where pressure first appears.

## Page Structure

### Header

The header shows:

- Title: `实时监控` / `Live Monitor`.
- Description: cluster control plane, replication, internal network, queue, and storage watermarks.
- Eyebrow badge: `UI Preview`, so operators do not mistake static preview data for production telemetry.

### Toolbar

The toolbar mirrors the business monitor style:

- Scope pill: global cluster aggregate.
- Last updated timestamp.
- Time range segmented control: `5m`, `15m`, `30m`, `1h`.
- Pause/resume button for the preview stream.

Node filtering is deferred until the API exists. The initial UI should not force an API shape before the card model is validated.

### Snapshot Strip

The compact scan strip shows the most important current values:

- Nodes Alive.
- Slots Ready.
- Controller Apply Gap.
- Channel ISR Anomalies.
- RPC Error Rate.
- Queue Pressure.
- Storage Write P99.

The snapshot strip is for quick scanning. The card wall remains the primary diagnostic surface.

### Card Wall

The card wall renders 12 cards in a responsive grid:

- Desktop: 3 or 4 columns depending on available width.
- Tablet: 2 columns.
- Mobile: 1 column.

Each card has a stable height and contains:

- Metric title.
- Stage label.
- Status pill: normal, watch, critical, or preview.
- Primary value and unit.
- Area trend chart.
- Three supporting stats.

## Card List

### Control Plane

1. **Controller Propose Rate**
   - Primary: control commands per second.
   - Supporting stats: Avg, Peak, Rejected.
   - Purpose: show whether the control plane is receiving and accepting work.

2. **Controller Apply Gap**
   - Primary: committed-to-applied gap.
   - Supporting stats: P95 Gap, Max Gap, Slow Nodes.
   - Purpose: detect control-plane Raft apply lag.

### Slot Replication

3. **Slot Leader Stability**
   - Primary: healthy leader percentage.
   - Supporting stats: Leader Missing, Quorum Lost, Transfers.
   - Purpose: show Slot leadership churn or availability risk.

4. **Slot Replica Lag P99**
   - Primary: P99 replica lag in milliseconds.
   - Supporting stats: P50, P95, Lagging Slots.
   - Purpose: detect slow Slot replication or follower catch-up pressure.

### Channel Replication

5. **Channel ISR Health**
   - Primary: healthy Channel ISR percentage.
   - Supporting stats: ISR Insufficient, No Leader, Affected Channels.
   - Purpose: surface Channel runtime metadata and replica-set health.

6. **Channel Append Latency P99**
   - Primary: Channel append P99 latency in milliseconds.
   - Supporting stats: P50, P95, Slow Appends.
   - Purpose: show whether Channel writes are slowing before delivery symptoms appear.

### Internal Network

7. **Internal Traffic**
   - Primary: total internal transfer rate.
   - Supporting stats: TX, RX, Peak.
   - Purpose: show node-to-node transport load.

8. **RPC Success Rate**
   - Primary: internal RPC success percentage.
   - Supporting stats: Calls/s, Errors/s, Timeouts.
   - Purpose: detect RPC reliability problems.

9. **RPC Latency P95**
   - Primary: internal RPC P95 latency in milliseconds.
   - Supporting stats: P50, P95, Inflight.
   - Purpose: reveal network or remote service pressure before outright failures.

### Runtime Pressure

10. **Workqueue Pressure**
    - Primary: normalized queue pressure percentage.
    - Supporting stats: Busy, Critical, Queue Full.
    - Purpose: summarize runtime queue saturation across nodes.

11. **Storage Write P99**
    - Primary: storage write P99 latency in milliseconds.
    - Supporting stats: P50, P95, Flush Wait.
    - Purpose: expose write-path storage pressure that can affect Raft and Channel append behavior.

### Incident Closure

12. **Incident Rate**
    - Primary: cluster incidents per minute.
    - Supporting stats: Critical, Warning, Top Reason.
    - Purpose: show whether runtime errors are spreading or settling.

## Visual Rules

- Keep the restrained management-console style already used by the web app.
- Use small-radius cards with borders, quiet backgrounds, and dense but readable spacing.
- Make the trend chart visually prominent; the card should feel like a monitor panel, not a KPI tile.
- Use color sparingly and by metric type:
  - Blue for control plane.
  - Teal and green for Slot and Channel replication.
  - Indigo and cyan for internal network.
  - Amber for queue pressure.
  - Rose for storage latency.
  - Red for incidents and critical states.
- Avoid a one-color page. The card wall should be scannable by category without becoming decorative.
- Keep card dimensions stable so value changes, labels, and translated text do not shift the layout.

## Component Direction

Create a separate page folder so cluster monitor types do not mix with business monitor metrics:

```text
web/src/pages/cluster-monitor/
  page.tsx
  types.ts
  preview-data.ts
  components/
    cluster-monitor-toolbar.tsx
    cluster-monitor-snapshot-strip.tsx
    cluster-monitor-card-grid.tsx
    cluster-monitor-metric-card.tsx
```

The implementation can borrow structure from `web/src/pages/monitor`, but the cluster page should have its own metric keys, stage labels, stats, and preview model.

## Preview Data Model

The preview model should provide:

- `generatedAt`
- `scopeLabelId`
- `timeRange`
- `isPaused`
- `snapshot`
- `cards`

Each card should include:

- Stable metric key.
- Localized title id and stage label id.
- Status id and tone.
- Current value, unit, chart color.
- Time series points.
- Three supporting stats.

The generated series should be deterministic, realistic, and shaped by time range:

- Control-plane rates should be steady with small bursts.
- Apply gap and replica lag should show short spikes.
- ISR and success percentages should stay high with small dips.
- Queue and storage pressure can slowly build and recover.
- Incident rate should remain low but visibly pulse.

## Navigation And Routing

- Add `/cluster/monitor` to the cluster operations navigation.
- Place it near the cluster dashboard so it is discoverable as a real-time counterpart to the dashboard.
- Use a new message id for cluster monitor navigation instead of reusing the business monitor nav metadata.
- Keep the legacy `/monitor` redirect pointing to `/business/monitor`.

## Tests

Add focused web tests for the UI-first implementation:

- Router renders `/cluster/monitor`.
- Cluster navigation includes the cluster live monitor item.
- Page renders the preview badge, snapshot strip, and 12 metric cards.
- Time range switching updates the preview model.
- Pause/resume toggles the toolbar state.

Run focused verification before completion:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx src/lib/navigation.test.ts src/app/router.test.tsx
cd web && bunx tsc -b
```

Run `cd web && bunx vite build` if the change touches shared route, shell, or build configuration behavior. Restore generated `web/dist/index.html` hash churn if the build rewrites it.

## Future API Direction

After the UI is approved, design one cluster monitor API around the page model:

- Summary snapshot for top strip values.
- Card definitions with latest value, status, stats, and series.
- Optional `node_id`, `window`, and `step` parameters.
- Explicit source metadata so operators can distinguish real, derived, and sampled metrics.

This keeps the API aligned with what the operator needs to see rather than exposing raw collector internals first.
