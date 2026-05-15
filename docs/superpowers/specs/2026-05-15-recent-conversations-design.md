# Manager Recent Conversations Design

## Goal

Add a manager-protected, read-only recent-conversations slice for the real `web/` admin app.

Operators should be able to open Business Management, enter a UID, and inspect that user's current recent conversation working set without using the client-facing `/conversation/sync` API directly.

## Context

The repository already has the core conversation behavior in `internal/usecase/conversation`:

- `App.Sync` builds a UID-scoped recent conversation working set.
- `SyncConversation` reports channel ID, channel type, unread count, latest sequence, read cursor, timestamp, version, and optional recent messages.
- The composition root wires `conversation.App` with authoritative Slot conversation state and channel-log facts.
- The legacy access API exposes `/conversation/sync`, but the manager API has no protected equivalent.

The existing manager pattern is:

```text
web -> internal/access/manager -> internal/usecase/management -> reusable usecases/runtime/pkg
```

This feature should follow that pattern. HTTP handlers stay thin, `internal/usecase/management` owns manager-facing aggregation, and the existing conversation usecase remains the source of conversation selection rules.

## Scope

In scope:

- `GET /manager/conversations?uid=&limit=&msg_count=&only_unread=`
- Manager permission protection for the endpoint.
- `internal/usecase/management` request/response DTOs and a `ListRecentConversations` method.
- Wiring from `internal/app` so management can call the conversation usecase.
- `web/src/lib/manager-api.ts` and `web/src/lib/manager-api.types.ts` client bindings.
- New `/business/conversations` route, navigation entry, page, and i18n strings.
- Focused Go and web tests for parsing, DTO mapping, API client paths, navigation, and page behavior.

Out of scope:

- Clearing unread, setting unread, deleting conversations, or other mutations.
- Global cross-user recent conversation scanning.
- Cursor pagination. This endpoint returns a bounded Top-N working set.
- New storage indexes or schema changes.
- Browser E2E coverage.
- Any changes under the legacy `ui/` directory.

## Recommended Approach

Use the manager aggregation path instead of exposing the client API directly:

```text
internal/access/manager
  parses HTTP query and maps errors

internal/usecase/management
  validates manager request
  calls conversation.App.Sync
  maps conversation DTOs to manager DTOs

internal/usecase/conversation
  remains the source of recent-conversation selection behavior
```

Alternatives considered:

- Directly inject `conversation.App` into `internal/access/manager`. This is smaller, but it pushes usecase orchestration into the HTTP adapter and diverges from existing manager pages.
- Create a new dedicated recent-conversation usecase. This would be clean but too much for a read-only manager adapter around an existing usecase.
- Build global recent-conversation scan APIs. This requires new storage/index design and is not needed for the requested UID-scoped management page.

## API Design

### `GET /manager/conversations`

Query parameters:

| Name | Required | Default | Notes |
| --- | --- | --- | --- |
| `uid` | yes | none | Trimmed UID whose conversations should be listed. |
| `limit` | no | `50` | Maximum `200`. |
| `msg_count` | no | `1` | Maximum `10`. `0` is allowed to omit previews. |
| `only_unread` | no | `false` | Accepts `true/false` and `1/0`. |

Response:

```json
{
  "uid": "u1",
  "limit": 50,
  "msg_count": 1,
  "only_unread": false,
  "truncated": false,
  "items": [
    {
      "uid": "u1",
      "channel_id": "g1",
      "channel_type": 2,
      "unread": 4,
      "timestamp": 1778852000,
      "last_msg_seq": 128,
      "last_client_msg_no": "client-128",
      "read_to_msg_seq": 124,
      "version": 1778852000000000000,
      "recent_messages": [
        {
          "message_id": 9128,
          "message_seq": 128,
          "client_msg_no": "client-128",
          "channel_id": "g1",
          "channel_type": 2,
          "from_uid": "u2",
          "timestamp": 1778852000,
          "payload": "aGVsbG8="
        }
      ]
    }
  ]
}
```

Field rules:

- `uid` echoes the normalized query UID.
- `items` are ordered by the same priority as `conversation.App.Sync`: newest display timestamp first, then deterministic channel ordering.
- `channel_id` uses the conversation usecase's display channel ID. For person conversations, this is the opposite UID when derivation succeeds.
- `recent_messages` reuses manager message DTO semantics, including base64 JSON encoding for byte payloads.
- `truncated` reports whether another matching item was detected by internally querying `limit + 1`. It does not imply cursor pagination is available.

Error handling:

- Missing or blank `uid`: `400 bad_request`.
- Invalid `limit`, `msg_count`, or `only_unread`: `400 bad_request`.
- Conversation dependencies unavailable: `503 service_unavailable`.
- Unexpected usecase failure: `500 internal_error`.

Permissions:

- Protect the route with manager auth when enabled.
- Require `cluster.channel:r`, matching `/manager/messages` because the endpoint exposes channel conversation/message data.

## Management Usecase Design

Add these manager-facing types in `internal/usecase/management`:

```go
type RecentConversationsRequest struct {
    UID        string
    Limit      int
    MsgCount   int
    OnlyUnread bool
}

type RecentConversationsResponse struct {
    UID        string
    Limit      int
    MsgCount   int
    OnlyUnread bool
    Truncated  bool
    Items      []RecentConversation
}

type RecentConversation struct {
    UID             string
    ChannelID       string
    ChannelType     uint8
    Unread          int
    Timestamp       int64
    LastMsgSeq      uint32
    LastClientMsgNo string
    ReadToMsgSeq    uint32
    Version         int64
    RecentMessages  []Message
}
```

`management.App.ListRecentConversations` should:

1. Validate `UID`, `Limit`, and `MsgCount`.
2. Return a new `ErrRecentConversationsUnavailable` sentinel when the conversation reader is not configured, so the manager HTTP layer can map it to `503 service_unavailable`.
3. Call `conversation.Sync` with `Limit: limit + 1`, `MsgCount`, `OnlyUnread`, and no client overlay map.
4. Set `Truncated` when more than `limit` items are returned, then trim to `limit`.
5. Map recent `channel.Message` values into existing manager `Message` DTOs.

This keeps the manager endpoint read-only and avoids any new business branch that bypasses cluster-owned state. In a single-node cluster, the same authoritative store and channel-log paths are used.

## Frontend Design

Add a Business Management item:

- Route: `/business/conversations`
- Legacy alias: `/conversations` redirects to `/business/conversations`
- Navigation title: Recent Conversations / 最近会话
- Path label: `BUSINESS / CONVERSATIONS`

Page behavior:

- Initial state shows an empty prompt asking for UID.
- Query form fields: UID, limit, msg count, and only-unread toggle/select.
- If the page is opened with `?uid=u1`, auto-run the query.
- Results table columns: channel, type, unread, last sequence, last client message number, timestamp, last message preview, actions.
- Row action: open `/business/messages?channel_id=<id>&channel_type=<type>`.
- Empty, loading, forbidden, unavailable, and generic error states should reuse existing manager UI components.

The last-message preview should decode payloads the same way the messages page does: try text decoding from base64 and fall back to the raw base64 string when content is not printable.

## Testing Plan

Go tests:

- `internal/usecase/management`: validates request mapping, `limit + 1` truncation, only-unread pass-through, and recent message DTO mapping.
- `internal/access/manager`: verifies query parsing, missing UID errors, response shape, permission middleware behavior when auth is enabled, and unavailable handling when management is missing.
- `internal/app`: focused wiring test if needed to ensure management receives the conversation reader.

Web tests:

- `web/src/lib/manager-api.test.ts`: verifies `/manager/conversations` query string construction.
- `web/src/pages/conversations/page.test.tsx`: verifies initial prompt, UID search, auto-query from URL, only-unread parameter, table rendering, message-page link, and error-state mapping.
- `web/src/pages/page-shells.test.tsx` and navigation tests: verify route, nav label, and i18n in English and Chinese.

Run targeted tests first:

```sh
go test ./internal/usecase/management ./internal/access/manager ./internal/app
cd web && yarn test
```
