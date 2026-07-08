# WuKongIM Web Cluster Nodes And Slots Subviews Redesign

## Goal

Continue the `web/DESIGN.md` editorial console redesign on the active cluster operations pages that still have older secondary surfaces:

- `/cluster/nodes`
- `/cluster/slots`

The main list panels already received recent density and workbench polish. This batch should keep those primary scanning surfaces stable and refine the remaining subviews: detail sheets, overview panels, unhealthy panels, and slot operation result surfaces.

## Non-Goals

Out of scope:

- Backend API changes.
- Route changes or navigation metadata changes.
- Permission, auth, or action-gating behavior changes.
- Node lifecycle semantics, controller-voter promotion semantics, slot move-in or move-out semantics, slot recovery, slot removal, slot leader transfer, rebalance, or batch transfer behavior changes.
- Data loading, refresh, filtering, query construction, detail loading, retry behavior, confirmation dialogs, or mutation request shapes.
- Redesign of `cluster-dashboard` or `business-dashboard`; those pages remain unused and deferred.
- Redesign of `/cluster/topology`, `/cluster/channels`, `/cluster/diagnostics`, `/cluster/monitor`, business pages, or system pages.
- New table, form, charting, or UI dependencies.
- New user-facing copy unless an existing localized key already provides it.

## Shared Design Rules

- Preserve every existing field, status badge, filter, refresh action, table row action, detail-sheet footer action, confirmation dialog, loading state, empty state, and error state.
- Keep the node and slot list panels as the primary operational workbenches.
- Treat overview panels as compact status summaries, not dashboard card grids.
- Treat unhealthy panels as named incident tables with restrained shells and accessible labels.
- Treat detail sheets as compact inspection surfaces with stable content markers.
- Prefer named surfaces with stable data attributes over brittle structure-only tests.
- Flatten secondary table and result shells to `rounded-md` or restrained `rounded-lg` bordered surfaces.
- Do not add hard-coded visible text. Reuse existing i18n keys.
- Preserve accessible names used by existing tests for buttons, dialogs, tabs, fields, and row actions unless this spec explicitly adds a new label to a previously unnamed table.

## Nodes Page

### Scope

`/cluster/nodes` remains the cluster node inventory and lifecycle action page. The existing list panel keeps node summary metrics, controller role state, membership state, runtime state, controller-raft links, lifecycle actions, slot move actions, detail loading, and controller-voter promotion behavior.

### Layout

- Keep the main `NodeClusterListPanel` table and direct row actions intact.
- Add a stable detail content surface inside the node `DetailSheet`:
  - preserve the existing `KeyValueList` and all current fields.
  - keep controller-raft link rendering and hosted/leader slot IDs unchanged.
  - use the marker `data-node-surface="detail"` on the content shell.
- Keep `NodeClusterOverviewPanel` as a read-only overview:
  - keep the existing summary strip and `data-testid="nodes-summary-strip"`.
  - convert the unhealthy breakdown and runtime totals from card-like nested blocks into named compact overview surfaces.
  - use `data-node-surface="overview-unhealthy"` and `data-node-surface="overview-runtime"`.
  - keep total, alive, unhealthy, draining, schedulable, controller-voter, gateway-session, active-online, and unknown-runtime values unchanged.
- Refine `NodeClusterUnhealthyPanel`:
  - wrap the loaded panel in `data-node-surface="unhealthy"`.
  - wrap the table in `data-node-surface="unhealthy-table"`.
  - add an accessible table label derived from the existing unhealthy title.
  - keep the same columns, row values, empty state, loading state, retry behavior, and error mapping.

### Behavior

- Do not change `getNodes` request timing for list, overview, or unhealthy panels.
- Do not change `getNode` detail timing, selected-node state, or detail retry behavior.
- Do not change lifecycle action availability or confirmation flow.
- Do not change controller-voter promotion availability or confirmation flow.
- Do not change slot move-in or move-out availability or confirmation flow.
- Do not change controller-raft links, node-health classification, membership formatting, slot summaries, runtime summaries, or timestamp formatting.

## Slots Page

### Scope

`/cluster/slots` remains the physical slot inventory and slot operation page. The existing list panel keeps node filtering, slot inspection, add slot, remove slot, transfer leader, recover slot, rebalance, batch transfer planning/execution, and slot log behavior.

### Layout

- Keep the main slot inventory workbench intact:
  - preserve `data-slot-surface="inventory"` and the accessible inventory table label.
  - keep node filtering, refresh, add, rebalance, batch transfer, inspect, and hash-slot ownership rendering unchanged.
- Add a stable detail content surface inside the slot `DetailSheet`:
  - preserve the existing `KeyValueList` and all current fields.
  - keep footer actions for remove, transfer leader, and recover unchanged.
  - use the marker `data-slot-surface="detail"` on the content shell.
- Refine operation result surfaces:
  - keep `data-slot-surface="rebalance-result"` and `data-slot-surface="batch-transfer-result"`.
  - flatten result item shells to compact bordered `rounded-md` surfaces.
  - keep all result rows and operation status text unchanged.
- Convert `SlotClusterOverviewPanel` summary cards into a compact summary strip:
  - use `data-testid="slots-overview-summary-strip"`.
  - preserve total, ready, quorum-lost, leader-missing, peer-mismatch, and unreported values.
  - convert unhealthy breakdown and runtime totals into named compact overview surfaces.
  - use `data-slot-surface="overview-unhealthy"` and `data-slot-surface="overview-runtime"`.
- Refine `SlotClusterUnhealthyPanel`:
  - keep `data-slot-surface="unhealthy"` on the loaded panel.
  - add `data-slot-surface="unhealthy-table"` to the table wrapper.
  - add an accessible table label derived from the existing unhealthy title.
  - keep the same columns, anomaly reason mapping, row values, empty state, loading state, retry behavior, and error mapping.

### Behavior

- Do not change `getSlots`, `getOverview`, or `getSlot` request timing.
- Do not change selected node filter behavior, local-node default selection, or query parameter construction.
- Do not change add slot, remove slot, transfer leader, recover slot, rebalance, batch transfer plan, or batch transfer execution request shapes.
- Do not change detail footer action availability.
- Do not change slot quorum, sync, desired/current peer, leader, epoch, hash-slot, anomaly reason, slot-log, or timestamp formatting.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/nodes/page.test.tsx`
  - asserts node detail content exposes `data-node-surface="detail"` while preserving existing detail fields.
  - asserts node overview keeps the summary strip and exposes named unhealthy/runtime overview surfaces.
  - asserts node unhealthy renders a named panel and accessible unhealthy table.
  - keeps existing coverage for list density, lifecycle actions, controller-voter promotion, slot move actions, controller-raft detail, loading, empty, and error states.
- `web/src/pages/slots/page.test.tsx`
  - asserts slot detail content exposes `data-slot-surface="detail"` while preserving existing detail fields and footer actions.
  - asserts slot operation result surfaces remain named and use compact shells.
  - asserts slot overview renders the new compact summary strip and named unhealthy/runtime overview surfaces.
  - asserts slot unhealthy renders a named panel and accessible unhealthy table.
  - keeps existing coverage for inventory density, node filtering, add/remove/recover/transfer/rebalance/batch-transfer behavior, loading, empty, and error states.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx src/pages/slots/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- `/cluster/nodes` and `/cluster/slots` secondary surfaces match the editorial console visual language from `web/DESIGN.md`.
- Existing node list, slot list, node lifecycle, controller-voter, slot move, slot operation, detail, loading, refresh, retry, empty, and error behavior remain unchanged.
- Overview panels read as compact operational summaries rather than dashboard card grids.
- Unhealthy panels expose accessible, named incident tables.
- No backend API, route, auth, permission, or i18n semantic behavior changes are introduced.
- `cluster-dashboard`, `business-dashboard`, monitor, topology, channels, diagnostics, business, and system pages remain out of scope.
- Focused tests, TypeScript verification, production build, and whitespace check pass.
