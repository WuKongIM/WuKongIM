# WuKongIM Web Business Query Pages Redesign

## Goal

Complete the active business operations redesign by applying the approved `web/DESIGN.md` editorial console language to the remaining business pages that were intentionally left out of the previous batch:

- `/business/messages`
- `/business/conversations`
- `/business/system-users`

These pages should align with the redesigned shell and the completed users/channels/connections pages: compact operation toolbars, named inventory tables, thin bordered surfaces, low-noise metadata rows, and no dashboard-style summary-card treatment.

## Non-Goals

Out of scope:

- Backend API changes.
- Route changes.
- Permission, auth, or action-gating behavior changes.
- Data loading, cursor, default filter, URL auto-query, or query semantics changes.
- Message retention semantics changes.
- New table, form, charting, or UI dependencies.
- Redesign of `cluster-dashboard` or `business-dashboard`, because those routes are not currently part of the active workflow.
- Redesign of `/cluster/*` or `/system/*` support pages; those remain candidates for later batches.

## Shared Design Rules

- Preserve every existing query field, validation rule, refresh action, pagination action, detail sheet, confirmation dialog, and row action.
- Keep query forms as compact operation toolbars with bottom rules or thin bordered shells.
- Keep result tables as the dominant scanning surface after data loads.
- Name result tables with accessible labels so tests and assistive technology can target the operational surface.
- Replace remaining `rounded-xl` page-local inventory shells with restrained `rounded-lg` or `rounded-md` bordered surfaces.
- Use metadata rows and small status strips instead of card grids for counts, active scope, truncation, and cache notes.
- Keep destructive message-retention actions quiet in row context and rely on the existing confirmation dialog.
- Preserve accessible names used by existing tests for fields, buttons, links, dialogs, and state messages.

## Messages Page

### Scope

The `messages` page remains the business query surface for channel message history, URL-driven auto-query, channel suggestions, pagination, message detail inspection, payload text/base64 switching, payload copy, and retention advancement through a selected message sequence.

### Layout

- Keep the current compact page chrome and scope/loaded metadata.
- Convert the query form shell into an editorial query toolbar with a thin border and stable field layout.
- Convert the message result shell into a named inventory surface.
- Name the message table with the existing messages title.
- Flatten the table scroll shell to `rounded-md`.
- Keep retention success feedback visible as a restrained notice inside the result surface.
- Keep the payload detail section as a thin bordered panel with compact action buttons.

### Behavior

- Do not change URL auto-query from `channel_id` and `channel_type`.
- Do not change request construction for `getMessages`.
- Do not change channel suggestion debounce, request limit, or selection behavior.
- Do not change pagination cursor behavior.
- Do not change payload decoding, format switching, copy behavior, or message detail fields.
- Do not change retention advancement request shape, blocked handling, success handling, or confirmation flow.
- Preserve permission and manager-unavailable error mapping.

## Conversations Page

### Scope

The `conversations` page remains the business query surface for recent conversations by UID, optional unread-only filtering, limit/message-preview validation, URL-driven auto-query, truncation notice, and navigation into the messages page.

### Layout

- Keep the open `PageHeader`.
- Convert the old `SectionCard` body into a compact query-and-results workbench.
- Place UID, limit, message preview count, unread-only, search, and refresh controls in a compact toolbar.
- Present active scope, loaded count, and truncation as a low-noise metadata row.
- Name the conversations result table with the page title.
- Flatten the result table shell to `rounded-md`.
- Keep the "View messages" links visually quiet and table-aligned.

### Behavior

- Do not change URL auto-query from `uid` and `only_unread`.
- Do not change validation rules: limit must stay `1..200`, message previews must stay `0..10`.
- Do not change request construction for `getRecentConversations`.
- Do not change refresh behavior or unavailable/forbidden error mapping.
- Do not change the messages link target format.

## System Users Page

### Scope

The `system-users` page remains the business maintenance surface for persisted system UIDs, add-with-normalization, removal confirmation, refresh, and error handling.

### Layout

- Keep the open `PageHeader` and existing add/refresh actions.
- Convert the list body into a compact inventory surface consistent with Users and Business Channels.
- Present total persisted UID count and cache-only exclusion note as a quiet metadata row.
- Name the system-users table with the existing list title.
- Flatten the table shell to `rounded-md`.
- Keep add/remove dialogs unchanged except for any needed visual alignment with existing compact form styling.

### Behavior

- Do not change `getSystemUsers`, `addSystemUsers`, or `removeSystemUsers` request shapes.
- Do not change UID normalization or de-duplication rules.
- Do not change empty-input validation.
- Do not change remove confirmation or refresh-after-mutation behavior.
- Preserve permission and manager-unavailable error mapping.

## Testing

Focused frontend coverage should prove the visual contract without over-coupling to every Tailwind class:

- `web/src/pages/messages/page.test.tsx`
  - asserts the messages page uses an editorial query toolbar and named inventory table.
  - asserts URL auto-query, manual query, channel suggestions, pagination, detail, payload switching/copy, and retention delete behavior remain reachable.
  - asserts long payload text remains constrained to the payload column.
- `web/src/pages/conversations/page.test.tsx`
  - asserts the conversations page uses an editorial query toolbar and named result table.
  - asserts manual query, URL auto-query, unread-only filtering, validation, truncation notice, and messages links remain unchanged.
  - asserts permission and manager-unavailable errors still render correctly.
- `web/src/pages/system-users/page.test.tsx`
  - asserts the system users page uses an editorial inventory metadata row and named table.
  - asserts add normalization, empty validation, remove confirmation, refresh-after-mutation, and error mapping remain unchanged.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/messages/page.test.tsx src/pages/conversations/page.test.tsx src/pages/system-users/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that generated hash churn before committing source changes.

## Acceptance

- The remaining three active business pages match the editorial console visual language from `web/DESIGN.md`.
- Existing page behavior, URL behavior, data loading, permission checks, confirmations, default filters, cursor behavior, and message retention handling remain unchanged.
- The unused dashboard routes are not redesigned in this batch.
- Cluster and system support pages are left for later redesign batches.
- Focused tests and TypeScript/build verification pass.
