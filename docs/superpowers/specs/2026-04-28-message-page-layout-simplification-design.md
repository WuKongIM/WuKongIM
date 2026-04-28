# Message Page Layout Simplification Design

## Goal

Simplify the manager Messages page so operators can query a channel and inspect matched messages without moving through repeated page chrome, summary cards, and toolbar descriptions. Preserve the existing message query behavior, pagination, URL auto-query, and message detail sheet.

## Current Problems

- The page repeats context across `PageHeader`, scope chips, the filter `SectionCard`, result `SectionCard`, and `TableToolbar`.
- The filter form is necessary, but its title and description consume vertical space without improving the query workflow.
- The `Loaded messages` and `Senders` summary cards duplicate information that is visible in the table and do not help the main workflow.
- The result card title, description, and toolbar title/description add another layer before the operator reaches the message rows.
- `Load more` currently appears as toolbar chrome instead of table pagination.

## Design

Use a compact message query layout matching the simplified Nodes and Channels pages:

- Keep one top line with page title, current query scope, loaded count, and a refresh button when a query has been submitted.
- Replace the filter `SectionCard` with a lightweight inline filter surface containing:
  - Channel ID
  - Channel type
  - Message ID
  - Client message no
  - Search action
  - Validation error text
- Remove the page description, scope chip card treatment, filter card title/description, loaded/senders summary cards, result card title/description, and `TableToolbar` title/description.
- Render loading, error, empty, and table states in one flat result surface.
- Move `Load more` to the bottom-right under the result table.
- Keep the existing message detail `DetailSheet`, payload text/base64 toggle, and copy action unchanged.

## Data Flow

No backend or API contract changes are required.

- URL parameters `channel_id` and `channel_type` still auto-run the initial query.
- Manual search still validates channel ID, channel type, and optional message ID before calling `getMessages()`.
- `getMessages()` still receives the same channel, message, client message number, limit, and cursor parameters.
- `Load more` still appends the next page using `next_cursor`.
- Refresh still re-runs the last submitted query without a cursor.

## Error Handling

- Query validation errors remain next to the search action.
- Loading, forbidden, unavailable, general error, not-yet-queried, and empty states stay in the result surface.
- Retry remains available for submitted queries through the result error state and compact refresh button.
- Detail payload rendering and clipboard failures keep the existing best-effort behavior.

## Testing

Update `web/src/pages/messages/page.test.tsx` to verify:

- The compact page chrome renders scope, loaded count, and refresh after a query.
- The compact filter surface keeps the four existing inputs and Search action.
- The old page description, filter card title/description, summary card labels/descriptions, result card descriptions, and toolbar description do not render.
- URL auto-query, manual query, pagination, detail sheet, payload toggle, and copy behavior still work.

## Non-Goals

- Do not change the manager message API shape.
- Do not remove any existing query fields.
- Do not change table columns in this pass.
- Do not redesign the message detail sheet.
- Do not add new filters, search presets, or server-side behavior.
