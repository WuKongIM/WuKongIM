# WuKongIM Web Cluster Monitor Workbench Redesign

## Goal

Continue the `web/DESIGN.md` editorial console redesign on the active realtime monitor page:

- `/cluster/monitor`

This page is already a dedicated v2 cluster operations surface with recent workbench polish. This batch should not rework the monitor information architecture. It should only add the missing stable surface contracts and lightly align loading/source states with the rest of the editorial console redesign.

## Non-Goals

Out of scope:

- Backend API changes.
- Route changes or navigation metadata changes.
- Permission, auth, or action-gating behavior changes.
- Monitor category contract changes.
- Reintroducing the retired high-cardinality `all` category.
- Changing the default category from `common`.
- Changing `getRealtimeMonitor`, `getNodes`, query construction, request timing, refresh nonce behavior, auto-refresh behavior, node filtering, category filtering, or time-range filtering.
- Changing metric definitions, stage mapping, tone mapping, chart scaling, byte scaling, stat formatting, or preview data semantics.
- Changing visible copy, i18n message files, or metric help text.
- Replacing metric cards with a table or list.
- Adding charting, table, form, or UI dependencies.
- Redesign of `cluster-dashboard` or `business-dashboard`; those pages remain unused and deferred.
- Redesign of `/cluster/nodes`, `/cluster/slots`, `/cluster/topology`, `/cluster/channels`, `/cluster/diagnostics`, business pages, or system pages.

## Shared Design Rules

- Preserve the current monitor structure: page header, toolbar, snapshot strip, metric card grid, loading state, disabled state, and unavailable state.
- Keep `/cluster/monitor` a graph-first operations surface. Metric cards may stay larger than ordinary inventory cards because the charts need readable space.
- Add stable `data-cluster-monitor-surface` markers for major regions instead of relying only on incidental DOM shape.
- Keep toolbar controls compact and rule-separated.
- Keep the snapshot strip as a compact status strip, not individual dashboard cards.
- Keep metric cards as restrained operational chart cards with `shadow-none` and stable card tests.
- Keep loading, disabled, and unavailable source states visually quiet and named.
- Do not add hard-coded visible text. Reuse existing i18n keys.
- Preserve accessible names used by existing tests for category, node, time range, refresh, auto-refresh, help buttons, and status messages.

## Monitor Page

### Scope

`/cluster/monitor` remains the realtime cluster monitor backed by v2 manager monitor APIs. It keeps the current `common` default category, category selector, node selector, time range controls, manual refresh, auto refresh, realtime cards, snapshot strip, source error states, and disabled Prometheus guidance.

### Layout

- Keep the `PageHeader` title, eyebrow, and description.
- Keep `ClusterMonitorToolbar` as the query workbench:
  - keep `data-monitor-toolbar="true"` for existing tests.
  - add `data-cluster-monitor-surface="toolbar"` to the same section.
  - preserve category, node, time-range, manual-refresh, and auto-refresh controls.
  - preserve the generated-at and scope metadata row.
- Name the snapshot strip:
  - add `data-cluster-monitor-surface="snapshot"` to `ClusterMonitorSnapshotStrip`.
  - keep each snapshot cell's existing `data-testid="cluster-monitor-snapshot-cell"`.
  - keep the compact border-bottom cell layout and tone dot.
- Name the metric card grid:
  - add `data-cluster-monitor-surface="metrics"` to `ClusterMonitorCardGrid`.
  - keep the existing responsive grid and metric card test IDs.
  - do not reduce chart height or card minimum height in this batch.
- Lightly align metric card radius only if needed:
  - metric cards may move from `rounded-lg` to `rounded-md` if tests lock the expected editorial shell.
  - keep `shadow-none`, chart rendering, help tooltip, stat grid, tone badge, and series behavior unchanged.
- Name source states:
  - add `data-cluster-monitor-surface="loading"` to the loading state.
  - add `data-cluster-monitor-surface="source-state"` to disabled and unavailable states.
  - use a restrained `rounded-md` or low-noise `rounded-lg` bordered shell.
  - keep disabled-state code chips for `WK_METRICS_ENABLE=true` and `WK_PROMETHEUS_ENABLE=true`.

### Behavior

- Do not change initial category: the first realtime request remains `{ window: "15m", category: "common" }`.
- Do not add an `All` category option.
- Do not change category options, labels, or ordering.
- Do not change selected-node query behavior.
- Do not change time-range state, selected range aria labels, or request parameters.
- Do not change manual refresh or auto-refresh interval behavior.
- Do not change stale-query loading behavior through `lastQueryKeyRef`.
- Do not change source status handling for `prometheus_disabled` or `prometheus_unavailable`.
- Do not change filtering of unknown snapshot keys or unknown metric cards.
- Do not change chart series grouping, tooltip behavior, area colors, byte scaling, or stat formatting.

## Testing

Focused frontend coverage should prove the visual contract without re-testing every metric:

- `web/src/pages/cluster-monitor/page.test.tsx`
  - asserts the toolbar exposes both `data-monitor-toolbar="true"` and `data-cluster-monitor-surface="toolbar"`.
  - asserts the snapshot strip exposes `data-cluster-monitor-surface="snapshot"` while snapshot cells remain present.
  - asserts the metric grid exposes `data-cluster-monitor-surface="metrics"` while metric cards remain present.
  - asserts loading, disabled, and unavailable source states expose the expected surface markers.
  - keeps existing coverage that the first request uses `{ window: "15m", category: "common" }`.
  - keeps existing coverage that the category selector does not include `All`.
  - keeps existing coverage for category filtering, node filtering, time range, manual refresh, auto refresh, unavailable/disabled source states, help tooltips, and metric card rendering.
- `web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.test.ts`
  - remains focused on chart model and value formatting. No change is planned unless metric card radius or marker tests need a component-local assertion.
- `web/src/pages/cluster-monitor/preview-data.test.ts`
  - remains focused on deterministic preview model data. No change is planned.
- `web/src/pages/page-shells.test.tsx`
  - remains route smoke coverage for `/cluster/monitor`. No change is planned unless the shell test currently depends on old source-state structure.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/cluster-monitor/page.test.tsx src/pages/cluster-monitor/components/cluster-monitor-metric-card.test.ts src/pages/cluster-monitor/preview-data.test.ts src/pages/page-shells.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- `/cluster/monitor` matches the editorial console surface language from `web/DESIGN.md` without losing graph readability.
- Toolbar, snapshot strip, metric grid, loading state, and source states expose stable named monitor surfaces.
- Existing monitor category contract remains unchanged: default is `common`, `All` stays absent, and the request parameters stay stable.
- Existing node filtering, category filtering, time range, manual refresh, auto refresh, source status, metric mapping, chart rendering, and stat formatting behavior remain unchanged.
- No backend API, route, auth, i18n, dashboard, nodes, slots, topology, channels, diagnostics, business, or system-page changes are introduced.
- Focused tests, TypeScript verification, production build, and whitespace check pass.
