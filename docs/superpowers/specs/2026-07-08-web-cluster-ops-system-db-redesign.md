# WuKongIM Web Cluster Ops And System DB Redesign

## Goal

Continue the `web/DESIGN.md` editorial console redesign on the next active operations batch:

- `/cluster/plugins`
- `/cluster/workqueues`
- `/system/db`

These pages should align with the completed business operations/query pages: compact toolbars, named operational tables, thin bordered surfaces, low-noise metadata rows or strips, and restrained page-local shells.

## Non-Goals

Out of scope:

- Backend API changes.
- Route changes.
- Permission, auth, or action-gating behavior changes.
- Data loading, polling, refresh, pagination, filtering, query, mutation, or dialog behavior changes.
- New table, form, charting, or UI dependencies.
- Redesign of `cluster-dashboard` or `business-dashboard`; those routes remain page-only and are not part of this active workflow batch.
- Redesign of `/cluster/nodes`, `/cluster/slots`, `/cluster/channels`, `/cluster/topology`, `/cluster/diagnostics`, `/system/permissions`, or `/system/webhooks`; those remain candidates for later batches.

## Shared Design Rules

- Preserve every existing field, filter, refresh action, pagination action, detail sheet, confirmation dialog, mutation flow, and row action.
- Keep operation controls as compact toolbars with thin borders or bottom rules.
- Keep result tables as the primary scanning surface after data loads.
- Name result tables with accessible labels so tests and assistive technology can target the operational surface.
- Flatten page-local table shells to `rounded-md` or restrained `rounded-lg` bordered surfaces.
- Use metadata rows and small status strips instead of dashboard-style card grids for counts, scope, source, and health summaries.
- Do not add hard-coded user-facing text. Reuse existing i18n keys or update existing localized keys when a visible copy change is necessary.
- Preserve accessible names used by existing tests for fields, buttons, links, dialogs, and state messages unless the spec explicitly updates the expected copy.

## Plugins Page

### Scope

The `plugins` page remains the cluster plugin inventory and binding maintenance surface. It keeps node selection, refresh, plugin status filtering, details, configure, restart, uninstall, binding search, add binding, delete binding, load more, and detail/config dialogs.

### Layout

- Keep the current `PageHeader`, node selector, and refresh action.
- Keep the existing summary strip, but keep it low-noise and rule-separated rather than card-like.
- Convert the plugin inventory section into a compact inventory workbench:
  - filters stay directly above the table as an operation toolbar.
  - the plugin table gets a stable accessible name and `text-sm` density.
  - the table wrapper becomes a restrained bordered `rounded-md` surface.
- Convert the binding section into the same pattern:
  - selector, query, search, and add controls form a compact toolbar.
  - the bindings table gets a stable accessible name and `text-sm` density.
  - load more remains a quiet row-level or section-level action.
- Keep detail/config/restart/uninstall dialogs behavior unchanged.

### Behavior

- Do not change node loading or selected node behavior.
- Do not change `getPlugins`, plugin detail, restart, uninstall, configure, binding search, add binding, delete binding, or load-more request shapes.
- Do not change plugin filtering semantics.
- Do not change permission and manager-unavailable error mapping.
- Do not change confirmation text or destructive action flow.

## Workqueues Page

### Scope

The `workqueues` page remains the runtime workqueue inspection surface for node/window/component filtering, auto refresh, abnormal-only filtering, refresh, summary status, and workqueue row scanning.

### Layout

- Keep the open `PageHeader` and refresh action.
- Convert the current control shell into a compact query toolbar:
  - node selector, window, component, auto refresh, and abnormal-only controls stay together.
  - active node and sample count move into a low-noise metadata row inside or directly below the toolbar.
- Replace the five dashboard-like summary cards with a restrained status strip:
  - overall level, total, abnormal count, hottest queue, and window stay visible.
  - presentation should use thin dividers and compact typography, not large card tiles.
- Name the workqueue result table with the page title or a specific workqueue inventory label.
- Flatten the table shell to a bordered `rounded-md` surface and keep the existing wide table constraints.

### Behavior

- Do not change initial load, refresh, auto-refresh interval behavior, or selected node behavior.
- Do not change filter state semantics for window, component, auto refresh, or abnormal-only.
- Do not change `getRuntimeWorkqueues` request construction.
- Do not change row formatting for depth, inflight, latency, rate, level, or hint.
- Preserve unavailable/forbidden error mapping.

## DB Inspect Page

### Scope

The `db-inspect` page remains the system support surface for read-only DB table discovery, table describe, SQL-like query templates, query execution, result stats, and next-page cursor execution.

### Layout

- Keep the `PageHeader` and refresh action.
- Convert the left table list into a compact inventory rail:
  - keep domain grouping and table inspect buttons.
  - reduce visual weight and keep the list scan-friendly.
- Convert the query section into a tool panel:
  - node selector, query textarea, template buttons, and run action stay in one compact workbench.
  - preserve monospace query editing.
- Convert describe and results sections into named result surfaces:
  - describe table and query result table get stable accessible names.
  - table wrappers flatten to restrained bordered `rounded-md` surfaces.
  - stats remain a compact strip above the results table.
- Keep next-page action visually quiet and section-aligned.

### Behavior

- Do not change initial node/table loading.
- Do not change table grouping, describe behavior, selected table handling, or table button labels.
- Do not change query template content.
- Do not change query execution, cursor append behavior, stats interpretation, or next-page behavior.
- Preserve permission and manager-unavailable error mapping.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/plugins/page.test.tsx`
  - asserts plugin inventory uses a named editorial table surface.
  - asserts plugin bindings use a named editorial table surface.
  - asserts node selector, refresh, filters, details/configure/restart/uninstall, binding search/add/delete, and load-more behavior remain reachable through existing tests.
- `web/src/pages/workqueues/page.test.tsx`
  - asserts the page uses an editorial workqueue query toolbar, status strip, metadata row, and named workqueue table.
  - asserts node/window/component filters, auto refresh, abnormal-only filtering, refresh, and empty/error states remain unchanged.
- `web/src/pages/db-inspect/page.test.tsx`
  - asserts the page uses an editorial table rail, query workbench, named describe table, and named query result table.
  - asserts describe, query execution, templates, stats, and next-page behavior remain unchanged.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/plugins/page.test.tsx src/pages/workqueues/page.test.tsx src/pages/db-inspect/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- `/cluster/plugins`, `/cluster/workqueues`, and `/system/db` match the editorial console visual language from `web/DESIGN.md`.
- Existing page behavior, loading, filtering, refresh, query, mutation, dialogs, pagination/cursor behavior, permission checks, and error handling remain unchanged.
- `cluster-dashboard` and `business-dashboard` are not redesigned in this batch.
- Cluster core pages and remaining system support pages are left for later redesign batches.
- Focused tests, TypeScript verification, production build, and whitespace check pass.
