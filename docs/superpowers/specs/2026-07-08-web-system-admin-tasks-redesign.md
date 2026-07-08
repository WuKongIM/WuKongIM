# WuKongIM Web System Admin And Tasks Redesign

## Goal

Continue the `web/DESIGN.md` editorial console redesign on the next low-risk operations batch:

- `/system/permissions`
- `/system/webhooks`
- `/cluster/tasks`

These pages should match the completed business and cluster operations batches: compact status strips, named table surfaces, thin bordered shells, restrained empty states, and low-noise control areas.

## Non-Goals

Out of scope:

- Backend API changes.
- Route changes.
- Permission, auth, or action-gating behavior changes.
- Data loading, refresh, filtering, query construction, timeline loading, error mapping, or detail-sheet behavior changes.
- Adding real webhook configuration, webhook API calls, callback logs, mutation forms, or event-type controls.
- New table, form, charting, or UI dependencies.
- Redesign of `cluster-dashboard` or `business-dashboard`.
- Redesign of `/cluster/nodes`, `/cluster/slots`, `/cluster/channels`, `/cluster/topology`, `/cluster/diagnostics`, `/cluster/monitor`, or business pages; those remain later batches.

## Shared Design Rules

- Preserve every existing field, filter, refresh action, detail sheet, table row action, loading state, empty state, and error state.
- Keep controls as compact toolbars with thin borders or bottom rules.
- Keep operational lists as the primary scanning surface after data loads.
- Name tables with accessible labels so tests and assistive technology can target the surface.
- Flatten page-local table shells to `rounded-md` or restrained `rounded-lg` bordered surfaces.
- Use metadata rows and compact status strips instead of dashboard-style card grids for counts and configuration state.
- Do not add hard-coded user-facing text. Reuse existing i18n keys or update existing localized keys only when a visible copy change is necessary.
- Preserve accessible names used by existing tests for fields, buttons, dialogs, and state messages unless this spec explicitly updates the expected surface name.

## Permissions Page

### Scope

The `permissions` page remains a read-only snapshot of manager authentication status, current user, static manager users, and permission catalog resources.

### Layout

- Keep the `PageHeader` title and description.
- Convert the four authentication summary cards into a restrained summary strip:
  - authentication status, current user, static users count, and catalog resources count stay visible.
  - the strip uses thin dividers and compact typography instead of independent rounded cards.
- Keep the read-only notice, but present it as a quiet section metadata row or low-noise notice below the summary strip.
- Convert the static manager users table into a named table surface:
  - table wrapper uses a restrained bordered `rounded-md` shell.
  - table uses `text-sm` density and an accessible label derived from the existing users section title.
  - permission grants remain chips and keep current label formatting.
- Convert the permission catalog table into the same named table-surface pattern:
  - table wrapper uses a restrained bordered `rounded-md` shell.
  - table uses `text-sm` density and an accessible label derived from the existing catalog section title.

### Behavior

- Do not change `getPermissions` request timing or retry behavior.
- Do not change auth-enabled/current-user/static-users/catalog count semantics.
- Do not change permission grant formatting.
- Do not change forbidden or unavailable error mapping.
- Do not change empty static-users behavior.

## Webhooks Page

### Scope

The `webhooks` page remains a placeholder system page. It communicates that webhook configuration is not implemented in the current manager UI.

### Layout

- Keep the `PageHeader` title and description.
- Replace the generic coming-soon card shape with a restrained empty-state surface:
  - one bordered `rounded-md` or restrained `rounded-lg` shell.
  - existing coming-soon title/description remain the visible copy.
  - the `ResourceState` empty state remains the central content.
- Add stable testable surface markers for the placeholder shell.
- Keep the page visually consistent with system support pages, without implying that webhook configuration is available.

### Behavior

- Do not add API calls.
- Do not add form controls, toggles, event chips, or callback-log tables.
- Do not change navigation, permissions, or route metadata.
- Do not introduce new webhook configuration semantics.

## Tasks Page

### Scope

The `tasks` page remains the read-only Controller task center for active tasks, retained audit history, filters, refresh, and task audit timelines.

### Layout

- Keep the `PageHeader` and read-only badge.
- Keep the existing summary strip and filter toolbar; they already match the target pattern.
- Refine the active tasks table into a named operational table surface:
  - table wrapper uses a restrained bordered `rounded-md` shell.
  - table uses `text-sm` density and an accessible label derived from the existing active tasks section title.
  - existing wide-table minimum width remains.
- Refine the task audit history table into the same named operational table surface:
  - table wrapper uses a restrained bordered `rounded-md` shell.
  - table uses `text-sm` density and an accessible label derived from the existing audit section title.
  - the view-timeline row action remains unchanged.
- Keep the audit-truncated warning as a quiet bordered notice.
- Lightly align timeline event surfaces inside the detail sheet:
  - preserve event ordering and all visible fields.
  - keep event cards restrained and compact.
  - do not change the sheet title, description, open/close behavior, or loading/error behavior.

### Behavior

- Do not change initial loading or refresh behavior.
- Do not change `buildQueries` semantics.
- Do not change active task suppression when status is `completed`.
- Do not change kind/status/slot/node/keyword filter state or request parameters.
- Do not change active task row formatting.
- Do not change audit row formatting or timeline request behavior.
- Do not change forbidden or unavailable error mapping.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/settings/permissions/page.test.tsx`
  - asserts the permissions page uses a compact authentication summary strip.
  - asserts static manager users and permission catalog render as named table surfaces.
  - keeps existing coverage for auth enabled/disabled, empty users, and forbidden/unavailable errors.
- `web/src/pages/settings/webhooks/page.test.tsx`
  - creates focused coverage for the placeholder page.
  - asserts the coming-soon empty state uses the expected system placeholder surface.
  - asserts no form controls or webhook configuration affordances are presented.
- `web/src/pages/tasks/page.test.tsx`
  - keeps existing summary strip and filter toolbar assertions.
  - asserts active tasks and audit history render as named operational table surfaces.
  - keeps existing coverage for filters, refresh request parameters, timeline loading, error mapping, and empty state.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/permissions/page.test.tsx src/pages/settings/webhooks/page.test.tsx src/pages/tasks/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- `/system/permissions`, `/system/webhooks`, and `/cluster/tasks` match the editorial console visual language from `web/DESIGN.md`.
- Existing permissions page behavior, task-center loading/filtering/refresh/timeline behavior, permission checks, and error handling remain unchanged.
- The webhooks page remains a placeholder and does not imply working webhook configuration.
- No backend API, route, auth, or i18n semantic behavior changes are introduced.
- Focused tests, TypeScript verification, production build, and whitespace check pass.
