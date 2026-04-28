# Channel Page Layout Simplification Design

## Goal

Simplify the manager Channels page layout so the page focuses on the channel runtime table and the two primary actions: inspect a channel and open its messages. Preserve the existing channel data and API calls while removing repeated page chrome and low-value summary cards.

## Current Problems

- The page repeats context across `PageHeader`, summary cards, `SectionCard`, and `TableToolbar`.
- The top header includes description and scope chips that are not useful for repeated operator workflows.
- Four summary cards (`Loaded channels`, `Active`, `Slots`, `Leaders`) consume the first screen but do not directly help the main workflow of finding a channel.
- The table toolbar duplicates title, description, and refresh behavior already available at the page level.
- The useful flow is simple: scan channel rows, inspect detail, jump to messages, and load the next page.

## Design

Use a compact channel list layout matching the simplified Nodes page:

- Keep one top line with page title, loaded count, and refresh button.
- Remove the scope chip, page description, four summary cards, `SectionCard` title/description, and `TableToolbar` title/description.
- Render the channel table in a single flat card-like surface directly below the compact header.
- Keep the existing table columns and row actions.
- Move `Load more` to the bottom-right of the table surface so it behaves like pagination instead of toolbar chrome.
- Keep channel detail in the existing right-side `DetailSheet`.

## Data Flow

No backend or API contract changes are required.

- `getChannelRuntimeMeta()` still loads the first page.
- `getChannelRuntimeMeta({ cursor })` still loads the next page.
- `getChannelRuntimeMetaDetail(channelType, channelID)` still loads the detail sheet.
- The Messages action still navigates to `/messages?channel_id=<id>&channel_type=<type>`.

## Error Handling

- Initial loading, forbidden, unavailable, and empty states stay in the main content area.
- Detail loading and errors stay inside the detail sheet.
- Load-more failures should keep the existing behavior of showing the page error state; this design does not change that behavior.

## Testing

Update `web/src/pages/channels/page.test.tsx` to verify:

- The compact page chrome renders loaded count and refresh.
- The old scope chip, summary card labels/descriptions, runtime card description, and toolbar description do not render.
- Channel rows, Inspect, Messages, and detail sheet behavior still work.
- `Load more` still appends the next page and calls the API with the cursor.

## Non-Goals

- Do not change channel runtime API shape.
- Do not remove table columns in this pass.
- Do not redesign the channel detail sheet fields.
- Do not add filters or search.
