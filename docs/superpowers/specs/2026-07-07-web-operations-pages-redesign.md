# WuKongIM Web Operations Pages Redesign

## Goal

Extend the approved `web/DESIGN.md` editorial console redesign to the high-use operations pages that are still outside the first redesign batch:

- `slots`
- `tasks`
- `controller`

The pages should feel consistent with the redesigned shell, cluster monitor, nodes, plugins, and login surfaces: white canvas, thin rules, compact controls, restrained status color, and table-first operational density.

## Non-Goals

This batch intentionally does not redesign `cluster-dashboard` or `business-dashboard`, because those routes are not currently part of the active workflow.

Out of scope:

- Backend API changes.
- Route changes.
- Permission or auth behavior changes.
- Slot, task, controller, compaction, or log semantics changes.
- New charting or UI dependencies.
- Broad refactors of page data loading.

## Shared Design Rules

- Preserve every existing operation, dialog, tab, filter, and row action.
- Prefer table/list scanning over large card walls.
- Use `PageHeader`, `SectionCard`, `StatusBadge`, and `Button` consistently with the first redesign batch.
- Keep page-local summary blocks as compact key-value strips with stable cell dimensions.
- Use thin borders and white or muted backgrounds; avoid glow, heavy shadow, broad gradient, and large decorative surfaces.
- Keep technical labels in mono-styled uppercase text with positive tracking.
- Keep destructive or risky operations visually quiet until confirmation is opened.
- Preserve accessible names for filters, refresh buttons, compaction buttons, dialogs, row actions, and detail triggers.

## Slots Page

### Scope

The `slots` page remains the source for slot list, unhealthy slot visibility, slot logs, detail sheets, leader transfer, batch leader transfer, rebalance, recover, add, and remove actions.

### Layout

- Keep the `list` and `logs` tabs.
- Keep the open page header from the shared shell.
- Convert slot summary areas into compact key-value strips instead of isolated card blocks.
- Keep the main slot list as the dominant surface.
- Keep anomaly and operation panels grouped but visually restrained with thin borders and compact forms.
- Keep logs visually separate from the list view through the existing tab boundary.

### Behavior

- Do not change slot API calls, query parameters, default selected node behavior, or action confirmation flow.
- Do not rename fields such as `preferred_leader_id`, `node_log.leader_id`, or node-local Raft `role`.
- Preserve hash-slot range display and slot log height display.

## Tasks Page

### Scope

The `tasks` page remains a read-only controller task center, showing active tasks, audit history, filters, and timeline/detail events.

### Layout

- Keep the read-only state visible in the page header.
- Place filters in a compact toolbar inside the task list surface.
- Convert summary cards into a rule-separated summary strip.
- Present active tasks and audit history as dense tables/lists under one task-center section.
- Keep audit truncation warnings visible but restrained.
- Keep the timeline detail sheet behavior, but align its visual surfaces with the thin-border console style.

### Behavior

- Do not change active task query construction.
- Do not change audit query construction.
- Do not change task timeline loading, stale state, or error handling.
- Preserve the completed-status behavior where active task querying is skipped.

## Controller Page

### Scope

The `controller` page remains the operator workbench for controller node selection, Raft status, Raft logs, decoded log expansion, and Raft log compaction.

### Layout

- Keep node selector, refresh, and compaction actions in a compact top operation row.
- Convert Raft status cells into compact key-value blocks with thin borders and consistent labels.
- Keep follower progress as a table-first surface.
- Keep log rows dense and readable, with decoded payload expansion still clearly separated.
- Present compaction result rows with the same table density as other controller sections.

### Behavior

- Do not change node selection URL behavior.
- Do not change request sequencing protections for logs and status.
- Do not change compaction scope defaults or success/error handling.
- Preserve stale-response guards and expanded decoded log state behavior.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/slots/page.test.tsx`
  - asserts the slots page uses the editorial summary strip/list surfaces.
  - asserts slot logs remain available through the existing tab.
  - asserts operation controls and risky actions remain reachable.
- `web/src/pages/tasks/page.test.tsx`
  - asserts the read-only task center uses a compact summary strip.
  - asserts filters remain present and refresh still triggers the existing load path.
  - asserts active/audit/timeline behavior remains unchanged.
- `web/src/pages/controller/page.test.tsx`
  - asserts controller status uses the compact workbench structure.
  - asserts node selector, refresh, compaction, log expansion, and result/error states remain reachable.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/slots/page.test.tsx src/pages/tasks/page.test.tsx src/pages/controller/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- The three active operations pages match the editorial console visual language from `web/DESIGN.md`.
- Existing page behavior, data loading, permission checks, confirmations, and URL behavior remain unchanged.
- No unused dashboard routes are redesigned in this batch.
- Focused tests and TypeScript/build verification pass.
