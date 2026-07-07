# WuKongIM Web Business Operations Redesign

## Goal

Extend the approved `web/DESIGN.md` editorial console redesign to the active business operations pages that are still visually behind the redesigned shell and operations workbench:

- `/business/users`
- `/business/channels`
- `/business/connections`

The pages should keep their current manager API behavior while matching the rest of the redesigned console: white canvas, thin borders, compact controls, table-first scanning, restrained status color, and no card-heavy dashboard treatment.

## Non-Goals

This batch intentionally does not redesign `cluster-dashboard` or `business-dashboard`, because those routes are not currently part of the active workflow.

Out of scope:

- Backend API changes.
- Route changes.
- Permission, auth, or action-gating behavior changes.
- Data loading, cursor, default filter, or query semantics changes.
- New charting, table, form, or UI dependencies.
- Broad refactors of page data management.
- Redesign of `/business/messages`, `/business/conversations`, or `/business/system-users`; those remain candidates for a later batch.

## Shared Design Rules

- Preserve every existing search, filter, refresh, pagination, detail, dialog, tab, and row action.
- Keep the main inventory table as the dominant surface.
- Use compact operation toolbars instead of large card headers.
- Prefer rule-separated key-value strips and small metadata rows over summary-card grids.
- Use white or muted backgrounds with thin borders; avoid glow, heavy shadow, broad gradient, and decorative surfaces.
- Keep technical identifiers, UIDs, channel IDs, device IDs, and node IDs readable with mono styling where the current UI already presents them as operational data.
- Keep destructive or mutating actions visually quiet in row context and rely on existing confirmation/error handling where present.
- Preserve accessible names used by existing tests for search fields, filters, refresh buttons, detail triggers, row actions, dialogs, and empty/error states.

## Users Page

### Scope

The `users` page remains the operator surface for user inventory, UID search, cursor pagination, user detail loading, token update, device quit, and user delete actions.

### Layout

- Keep the open `PageHeader` pattern from the redesigned shell.
- Replace the card-like content body with a restrained inventory section.
- Place UID search, submit, reset, and pagination actions in a compact toolbar.
- Present the user list as a dense table/list with stable identity, device, token, and action columns.
- Keep user detail and action feedback readable without introducing a dashboard-style summary row.

### Behavior

- Do not change UID search request construction.
- Do not change cursor pagination behavior.
- Do not change user detail loading, token update, device quit, delete, or refresh semantics.
- Preserve permission and manager-unavailable error mapping.

## Business Channels Page

### Scope

The `channels` business page remains the operator surface for channel inventory, channel ID search, channel type filtering, cursor pagination, channel metadata create/update, channel detail, subscriber/denylist/allowlist tabs, and member add/remove actions.

### Layout

- Keep the open `PageHeader` and put channel search/type filters in a compact operation toolbar.
- Keep the channel list as the primary scanning surface.
- Reduce nested-card visuals around metadata forms, member tabs, and detail areas by using thin bordered panels and compact section labels.
- Keep member-management forms visually grouped by purpose without expanding the feature surface.
- Preserve the distinction between ordinary subscriber edits and person-channel restrictions.

### Behavior

- Do not change channel list query parameters, channel type options, or cursor behavior.
- Do not change channel metadata create/update request shape.
- Do not change member normalization, member tab switching, add/remove semantics, or person-channel disabled behavior.
- Preserve permission and manager-unavailable error mapping.

## Connections Page

### Scope

The `connections` page remains the operator surface for node-scoped session inventory, local-node default filtering, refresh, empty/unavailable states, and session detail loading.

### Layout

- Keep the compact connection page chrome that already avoids summary cards.
- Align the filter row, refresh action, and table container with the editorial console visual language.
- Keep session identity, node, device, protocol, and online metadata easy to scan in a dense table.
- Present session detail in the existing flow with flatter borders and lower visual noise.

### Behavior

- Do not change the default node filter behavior.
- Do not change the existing manager connection list limit behavior.
- Do not add real-time subscriptions or polling changes.
- Do not change detail loading, refresh, unavailable state, or empty-state behavior.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/users/page.test.tsx`
  - asserts the users page uses the editorial inventory structure.
  - asserts UID search, reset, load-more, detail, and mutating actions remain reachable.
  - asserts permission and manager-unavailable errors still render correctly.
- `web/src/pages/channels-biz/page.test.tsx`
  - asserts the business channels page uses the editorial inventory structure.
  - asserts channel search, type filter, pagination, detail tabs, metadata save, member add/remove, and person-channel restrictions remain reachable.
  - asserts permission and manager-unavailable errors still render correctly.
- `web/src/pages/connections/page.test.tsx`
  - asserts the connections page keeps compact page chrome without summary cards.
  - asserts local-node default filtering, node filter reload, refresh, detail, unavailable state, and empty state remain reachable.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/users/page.test.tsx src/pages/channels-biz/page.test.tsx src/pages/connections/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- The three active business operations pages match the editorial console visual language from `web/DESIGN.md`.
- Existing page behavior, data loading, permission checks, confirmations, default filters, and cursor behavior remain unchanged.
- The unused dashboard routes are not redesigned in this batch.
- Messages, conversations, and system users are left for a later business-page batch.
- Focused tests and TypeScript/build verification pass.
