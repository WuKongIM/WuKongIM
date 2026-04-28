# Channel List Replica and Message Drilldown Design

## Context

The web manager already has two related views:

- `Channels` reads `/manager/channel-runtime-meta` and displays paged channel runtime metadata.
- `Messages` reads `/manager/messages` and supports channel-scoped message pagination and message detail inspection.

The requested feature is to make the channel list show the current replica set, show the maximum `message_seq`, and support viewing the current channel's messages with pagination.

## Goals

- Show each channel's current replica set in the channel list.
- Show each channel's maximum committed `message_seq` in the channel list and detail view.
- Let operators open a channel's message list from the channel list.
- Reuse the existing paged message query UI instead of duplicating message table, payload decoding, and pagination logic.

## Non-Goals

- Do not add a separate single-node code path; this remains cluster-authoritative manager data.
- Do not create a second embedded message-query implementation inside the channel detail sheet.
- Do not change the existing `/manager/messages` response shape unless URL-driven querying exposes a missing need.

## Recommended Approach

Use a lightweight channel-list enhancement plus URL-driven navigation to the existing messages page.

1. Extend the manager channel runtime metadata response with `max_message_seq`.
2. Display `replicas` and `max_message_seq` columns in `web/src/pages/channels/page.tsx`.
3. Add a per-row `Messages` action that navigates to `/messages?channel_id=<id>&channel_type=<type>`.
4. Update `web/src/pages/messages/page.tsx` so it reads `channel_id` and `channel_type` from the URL on first render and automatically runs the existing paged query.

## Backend Design

### Usecase Layer

- Extend `internal/usecase/management.ChannelRuntimeMeta` with `MaxMessageSeq uint64`.
- Populate `MaxMessageSeq` while building list and detail DTOs.
- Prefer an authoritative source that reflects committed channel messages. The current message query path already loads committed HW from the channel log leader before scanning messages, so the new field should follow the same cluster-authoritative semantics where possible.
- If a channel has no committed messages, return `0`.

### Access Layer

- Extend `internal/access/manager.ChannelRuntimeMetaDTO` with `MaxMessageSeq uint64 json:"max_message_seq"`.
- Include the field in both list and detail responses.
- Preserve existing error handling for unavailable authoritative reads.

## Frontend Design

### Channels Page

- Add a `Replicas` table column using the existing `formatNodeList` helper.
- Add a `Max messageSeq` table column.
- Add a `Messages` row action next to `Inspect`.
- The action builds `/messages?channel_id=<encoded channel id>&channel_type=<channel type>`.

### Messages Page

- On first render, read `channel_id` and `channel_type` from `useSearchParams`.
- If both are valid, initialize the form and submit the existing query automatically.
- Keep manual search behavior unchanged.
- Keep cursor-based `Load more` behavior unchanged.

## Data Flow

1. Operator opens Channels.
2. Web calls `/manager/channel-runtime-meta`.
3. Manager returns each channel with runtime metadata, replicas, and `max_message_seq`.
4. Operator clicks `Messages` on a row.
5. Browser navigates to `/messages?channel_id=...&channel_type=...`.
6. Messages page auto-runs the existing `/manager/messages` query with `limit=50`.
7. Operator uses existing `Load more` pagination and message detail inspection.

## Error Handling

- Channel metadata loading keeps current 403, 503, and generic error mapping.
- Message page auto-query uses the same validation and error rendering as manual queries.
- Invalid URL query values should populate the form but not auto-query if `channel_type` is invalid or `channel_id` is empty.
- Unknown max sequence should be avoided by using an authoritative committed source. If implementation discovers an unavoidable partial read, the API should document whether `0` means no committed messages versus unknown; the preferred behavior is no unknown state.

## Testing

- Backend manager access tests verify `max_message_seq` appears in list and detail JSON.
- Management usecase tests verify `MaxMessageSeq` is populated for channel runtime list/detail.
- Frontend API types and tests include `max_message_seq`.
- Channels page tests verify replicas, max sequence, and `Messages` navigation URL.
- Messages page tests verify URL params auto-run a query and preserve load-more pagination.

## Open Implementation Note

The exact backend source for `max_message_seq` must be selected during implementation after checking the existing committed-HW/message-log interfaces. The source must preserve cluster semantics and should not introduce a local-only fallback path.
