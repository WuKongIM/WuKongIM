# WuKongIM Web Cluster Light Pages Redesign

## Goal

Continue the `web/DESIGN.md` editorial console redesign on the remaining low-risk cluster navigation pages:

- `/cluster/topology`
- `/cluster/channels`
- `/cluster/diagnostics`

These pages should match the completed operations batches: compact summary strips, named operational surfaces, thin bordered shells, restrained empty states, and low-noise page-local wrappers. This batch intentionally avoids the heavier `nodes`, `slots`, and `monitor` pages because they already received recent polish and carry larger interaction surfaces.

## Non-Goals

Out of scope:

- Backend API changes.
- Route changes or navigation metadata changes.
- Permission, auth, or action-gating behavior changes.
- New tabs, restored retired tabs, or hidden dashboard work.
- Changes to `cluster-dashboard` or `business-dashboard`; those pages remain unused and deferred.
- Changes to `getOverview`, `getNodes`, `getSlots`, `getChannelRuntimeMeta`, diagnostic trace queries, request timing, retry behavior, or query parameter semantics.
- Changes to channel filtering, load-more behavior, diagnostics tab normalization, topology node filtering, or topology error handling.
- New table, charting, form, or UI dependencies.
- New user-facing copy unless an existing localized key already provides it.

## Shared Design Rules

- Preserve every existing data request, field, filter, loading state, empty state, and error state.
- Keep page-level changes local to the three target pages and their tests.
- Reuse existing `PageHeader`, `SectionCard`, `PageTabs`, `ResourceState`, and table primitives.
- Prefer named surfaces with stable data attributes over brittle structure-only assertions.
- Name tables and major operational regions with accessible labels when the underlying element supports one.
- Convert isolated card grids into compact strips when the content is a summary, not a set of independent actions.
- Use `rounded-md` or restrained `rounded-lg` bordered shells; avoid dashboard-style card expansion.
- Do not add hard-coded visible text. Reuse existing i18n keys.

## Topology Page

### Scope

`/cluster/topology` remains a read-only topology view built from overview, nodes, and slots data. It keeps the same loading, retry, forbidden, unavailable, empty, and selected-node filtering behavior.

### Layout

- Keep the `PageHeader` title, description, and read-only metadata chips.
- Convert the four topology summary cards into a compact summary strip:
  - controller leader, node count, slot count, and anomaly count remain visible.
  - the strip uses thin dividers and compact typography instead of four independent rounded cards.
  - add `data-testid="topology-summary-strip"` and stable summary-cell markers for tests.
- Keep the node filter exactly where it is, but place node cards in a named topology surface:
  - add the stable node-grid marker `data-topology-surface="nodes"`.
  - node cards remain readable operational cards, with the existing node name, address, status, local/controller chips, slot summary, and runtime summary.
  - node cards use `rounded-md` bordered surfaces and keep all existing data.
- Convert the slot placement table into a named table surface:
  - wrapper uses a restrained bordered `rounded-md` shell and `data-topology-surface="slot-placement"`.
  - table uses `text-sm` density and an accessible label derived from the existing slot placement section title.
  - filtered-slot count stays above the table.

### Behavior

- Do not change the `Promise.all([getOverview(), getNodes(), getSlots()])` loading flow.
- Do not change selected-node state, node filter labels, or slot filtering semantics.
- Do not change controller leader, anomaly count, node runtime, slot quorum, leader, sync, or hash-slot formatting.
- Do not change forbidden or unavailable error mapping.
- Do not change empty node or empty slot rendering.

## Cluster Channels Page

### Scope

`/cluster/channels` remains the cluster-routed entry to `ChannelClusterListPanel`. It must continue to show only the list experience and must not reintroduce retired overview or unhealthy tabs.

### Layout

- Keep the `PageHeader` eyebrow, title, and description.
- Wrap `ChannelClusterListPanel` in a named operational surface:
  - use the stable marker `data-cluster-channels-surface="list"`.
  - use a neutral wrapper with spacing only; do not add a second border around the panel because `ChannelClusterListPanel` already owns its table shell.
  - keep the channel runtime list as the primary scanning surface.
- Preserve the current `/business/messages` link target passed through `messagesHref`.
- Do not add explanatory copy or new visible controls.

### Behavior

- Do not change channel runtime API calls, node selection, keyword filtering, refresh, or load-more behavior.
- Do not call business-channel list APIs from this route.
- Do not show overview, unhealthy, or list tabs.
- Do not change legacy `?tab=list` compatibility behavior.

## Cluster Diagnostics Page

### Scope

`/cluster/diagnostics` remains the cluster diagnostics entry for the trace panel. The retired diagnostics tabs stay normalized to tracing.

### Layout

- Keep the `PageHeader` eyebrow, title, and description.
- Wrap the trace tab area in a named diagnostics surface:
  - use the stable marker `data-cluster-diagnostics-surface="trace"`.
  - keep `PageTabs` compact and rule-separated.
  - keep `DiagnosticsTracePanel` as the only rendered panel.
- Do not add network, controller-log, slot-log, or app-log panels.
- Do not add new visible copy.

### Behavior

- Preserve `normalizeTab` behavior: unsupported or retired tab values resolve to `trace`.
- Preserve `setSearchParams` behavior for the single trace tab.
- Do not call network summary, controller logs, application log sources, or application log entry APIs from this route.
- Do not change `DiagnosticsTracePanel` internals in this batch unless a wrapper marker is necessary and behavior-neutral.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/topology/page.test.tsx`
  - asserts the topology page uses a compact summary strip with four cells.
  - asserts the node topology area exposes a named surface while preserving node content.
  - asserts slot placement renders as a named table surface with an accessible table label.
  - keeps existing coverage for selected-node filtering, forbidden/unavailable errors, and empty states.
- `web/src/pages/cluster/channels/page.test.tsx`
  - asserts the cluster channel route wraps the list panel in the named list surface.
  - keeps existing assertions that overview, unhealthy, and list tabs are absent.
  - keeps existing assertions that runtime metadata is requested and business-channel APIs are not called.
- `web/src/pages/cluster/diagnostics/page.test.tsx`
  - asserts the diagnostics route renders the trace surface and only the tracing tab.
  - keeps existing assertions that retired tabs normalize to tracing.
  - keeps existing assertions that retired network/app-log APIs are not called.
- `web/src/pages/page-shells.test.tsx`
  - remains route smoke coverage for `/cluster/topology`, `/cluster/channels`, and `/cluster/diagnostics`.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/topology/page.test.tsx src/pages/cluster/channels/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx src/pages/page-shells.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- `/cluster/topology`, `/cluster/channels`, and `/cluster/diagnostics` match the editorial console visual language from `web/DESIGN.md`.
- Topology keeps the same overview/nodes/slots data flow, selected-node filtering, error mapping, and empty states.
- Cluster channels remains list-only and does not reintroduce retired tabs or business-channel API calls.
- Cluster diagnostics remains trace-only and continues normalizing retired tabs to tracing.
- No backend API, route, auth, i18n semantic behavior, dashboard, nodes, slots, or monitor changes are introduced.
- Focused tests, TypeScript verification, production build, and whitespace check pass.
