# Manager Channels Business Design

## Goal

Implement the first real `/channels-biz` business-management slice in the `web/` admin app.

This feature gives operators a manager-scoped way to list business channels, inspect one channel, create or update channel flags, and manage subscribers, allowlists, and denylists without using the legacy client-facing `/channel/*` API surface.

## Context

`docs/raw/web-admin-restructure.md` marks channel business management as a P1 operations feature. The current UI route `web/src/pages/channels-biz/page.tsx` is still a placeholder.

The repository already has most mutation primitives:

- `internal/usecase/channel` owns reusable channel metadata and member-list operations.
- `pkg/slot/proxy.Store` writes channel metadata and subscriber mutations through the authoritative Slot path.
- `pkg/slot/proxy.Store.ListChannelSubscribers` already performs authoritative subscriber pagination.
- `internal/contracts/channelmembers` derives the internal allowlist, denylist, and temp-list channel IDs.
- Existing manager channel pages focus on runtime/ISR operations under `/channel-cluster`; business CRUD should stay separate under `/channels-biz`.

The missing pieces are manager-scoped channel inventory reads, manager DTO aggregation, HTTP routes, and the real React page.

## Scope

In scope:

- `GET /manager/channels?type=&keyword=&limit=&cursor=`
- `GET /manager/channels/:type/:id`
- `POST /manager/channels`
- `GET /manager/channels/:type/:id/subscribers?limit=&cursor=`
- `POST /manager/channels/:type/:id/subscribers/add`
- `POST /manager/channels/:type/:id/subscribers/remove`
- `GET /manager/channels/:type/:id/allowlist?limit=&cursor=`
- `POST /manager/channels/:type/:id/allowlist/add`
- `POST /manager/channels/:type/:id/allowlist/remove`
- `GET /manager/channels/:type/:id/denylist?limit=&cursor=`
- `POST /manager/channels/:type/:id/denylist/add`
- `POST /manager/channels/:type/:id/denylist/remove`
- `web/src/pages/channels-biz/page.tsx` real implementation
- `web/src/lib/manager-api.ts` and `.types.ts` channel business client bindings
- i18n strings and focused tests

Out of scope for this slice:

- remove-all or set-all destructive member operations in the manager UI
- large CSV/bulk import workflows
- channel message retention controls
- channel ISR/leader operations; those stay under `/channel-cluster`
- channel audit history
- browser E2E and `test/e2e` coverage
- any changes under `ui/`

## API Design

### Channel Types

Manager requests use numeric WuKong channel types. The web page may label common types:

- `1`: person
- `2`: group
- `3`: customer service
- `4`: community
- `5`: community topic
- `6`: info
- `7`: data
- `8`: temp
- `9`: live
- `10`: visitors
- `11`: agent
- `12`: agent group

The backend should accept any positive `uint8`-compatible channel type, but the web form should present the common list above and allow a numeric custom type only if the existing UI patterns make that simple. Person-channel subscriber mutations remain rejected to preserve existing legacy semantics.

### `GET /manager/channels`

Lists business channel metadata in stable manager pagination order.

Query parameters:

| Name | Required | Default | Notes |
| --- | --- | --- | --- |
| `type` | no | empty | Positive channel type. Empty means all types. |
| `keyword` | no | empty | Trimmed channel ID substring filter. Empty means full list. |
| `limit` | no | `50` | Maximum `200`. |
| `cursor` | no | empty | Opaque cursor returned by the previous page. |

Response:

```json
{
  "items": [
    {
      "channel_id": "g1",
      "channel_type": 2,
      "slot_id": 1,
      "hash_slot": 37,
      "ban": false,
      "disband": false,
      "send_ban": false,
      "subscriber_mutation_version": 12
    }
  ],
  "has_more": true,
  "next_cursor": "V0tDTAEAAAAB..."
}
```

Rules:

- Return only business-visible channel metadata rows.
- Exclude internal member-list channel IDs with prefix `__wk_internal_memberlist__/`.
- Exclude internal derived command channel IDs ending in `____cmd` if they appear as metadata rows.
- The response intentionally does not include `total` because exact counts require scanning every managed Slot.
- The list is ordered by physical Slot, then channel primary-key order inside that Slot.

### Channel list cursor

Use a new opaque base64 cursor with a distinct magic value, for example `WKCL`.

Internal shape:

```go
type ChannelListCursor struct {
    // SlotID is the current physical Slot scan position.
    SlotID uint32
    // ChannelID is the last emitted channel ID inside SlotID.
    ChannelID string
    // ChannelType is the last emitted channel type inside SlotID.
    ChannelType int64
    // TypeFilter binds the cursor to the requested type filter. Zero means all types.
    TypeFilter int64
    // KeywordHash binds the cursor to the keyword used to create it.
    KeywordHash uint32
}
```

Rules:

- Cursor records the last emitted position.
- Cursor is bound to `keyword` and `type`.
- Reusing a cursor with a different `keyword` or `type` returns `400 invalid cursor`.
- `limit` may change between requests.
- `has_more=false` omits `next_cursor`.

### `GET /manager/channels/:type/:id`

Returns one business channel detail.

Response:

```json
{
  "channel_id": "g1",
  "channel_type": 2,
  "slot_id": 1,
  "hash_slot": 37,
  "ban": false,
  "disband": false,
  "send_ban": false,
  "subscriber_mutation_version": 12,
  "has_subscribers": true,
  "has_allowlist": false,
  "has_denylist": true
}
```

Rules:

- Detail reads channel metadata from the authoritative Slot owner.
- `has_subscribers`, `has_allowlist`, and `has_denylist` are cheap non-empty checks, not exact counts.
- Missing channel metadata returns `404 not_found`.
- Internal member-list and command-channel IDs should return `400 bad_request` for manager business detail paths.

### `POST /manager/channels`

Creates or updates one channel metadata row.

Request:

```json
{
  "channel_id": "g1",
  "channel_type": 2,
  "ban": false,
  "disband": false,
  "send_ban": false
}
```

Response: channel detail in the same shape as `GET /manager/channels/:type/:id`.

Behavior:

- This is an upsert operation, matching existing channel metadata semantics.
- It reuses `internal/usecase/channel.UpdateInfo`.
- It must preserve `SubscriberMutationVersion` for existing channel records.
- Empty `channel_id`, invalid `channel_type`, or internal channel IDs return `400 bad_request`.
- Writes must route through the authoritative Slot path; no local shortcut is allowed, including single-node cluster deployments.

### Member List APIs

All member lists use the same page response:

```json
{
  "items": [{ "uid": "u1" }, { "uid": "u2" }],
  "has_more": true,
  "next_cursor": "V0tDTQEAAA..."
}
```

Supported list kinds:

- `subscribers`: ordinary channel subscribers
- `allowlist`: internal allowlist members derived by `internal/contracts/channelmembers.AllowlistChannelID`
- `denylist`: internal denylist members derived by `internal/contracts/channelmembers.DenylistChannelID`

Pagination query parameters:

| Name | Required | Default | Notes |
| --- | --- | --- | --- |
| `limit` | no | `100` | Maximum `500`. |
| `cursor` | no | empty | Opaque cursor returned by the previous page. |

Mutation request:

```json
{
  "uids": ["u1", "u2"]
}
```

Mutation response:

```json
{
  "channel_id": "g1",
  "channel_type": 2,
  "list": "subscribers",
  "changed": true
}
```

Rules:

- UIDs are trimmed, deduplicated in request order, and empty values are ignored.
- Empty post-normalization UID lists return `400 bad_request`.
- Person-channel ordinary subscriber add/remove returns `400 bad_request`, matching existing legacy semantics.
- Allowlist and denylist operations are allowed for person channels because the send path can use those dimensions.
- The backend should not provide remove-all/set-all manager routes in this slice.

### Member list cursor

Use an opaque base64 cursor with a distinct magic value, for example `WKCM`.

Internal shape:

```go
type ChannelMemberCursor struct {
    // ChannelIDHash binds the cursor to the requested channel ID.
    ChannelIDHash uint32
    // ChannelType binds the cursor to the requested channel type.
    ChannelType int64
    // ListKind identifies subscribers, allowlist, or denylist.
    ListKind uint8
    // UID is the last emitted UID.
    UID string
}
```

Rules:

- Reusing a cursor for another channel or list kind returns `400 invalid cursor`.
- `limit` may change between requests.
- `has_more=false` omits `next_cursor`.

## Backend Design

### `pkg/slot/meta`

Add single-hash-slot channel page scanning:

```go
// ChannelCursor identifies the last emitted channel in a shard page scan.
type ChannelCursor struct {
    // ChannelID is the last emitted channel ID in primary-key order.
    ChannelID string
    // ChannelType is the last emitted channel type in primary-key order.
    ChannelType int64
}

func (s *ShardStore) ListChannelsPage(ctx context.Context, after ChannelCursor, limit int) ([]Channel, ChannelCursor, bool, error)
```

Implementation notes:

- Scan `ChannelTable` primary keys under one hash slot.
- Decode primary family records into `metadb.Channel`.
- Return channels ordered by `(channel_id, channel_type)` primary-key order.
- Validate `limit > 0`, cursor channel ID length, and cursor shape.
- Keep exported comments in English.

### `pkg/slot/proxy`

Add physical-Slot authoritative scanning:

```go
func (s *Store) ScanChannelsSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
```

Implementation notes:

- For a local authoritative Slot, merge the channel pages of all hash slots owned by the physical Slot with a small heap.
- For a remote authoritative Slot leader, add a channel RPC operation such as `scan_channels_page`.
- Reuse the existing binary channel RPC codec pattern; do not add JSON reflection.
- Return slot-leader authoritative errors as service-unavailable at the manager HTTP layer.

### `internal/usecase/channel`

Extend the reusable channel usecase with page reads for members:

```go
type MemberListPageRequest struct {
    ChannelKey ChannelKey
    AfterUID string
    Limit int
}

type MemberListPageResult struct {
    Members []Member
    NextCursor string
    HasMore bool
}

func (a *App) ListSubscribersPage(ctx context.Context, req MemberListPageRequest) (MemberListPageResult, error)
func (a *App) ListAllowlistPage(ctx context.Context, req MemberListPageRequest) (MemberListPageResult, error)
func (a *App) ListDenylistPage(ctx context.Context, req MemberListPageRequest) (MemberListPageResult, error)
```

Keep the existing legacy `ListAllowlist` all-read method for compatibility. The new page methods should delegate to `Store.ListChannelSubscribers` with the correct ordinary or namespaced channel ID.

### `internal/usecase/management`

Add `channels_biz.go` with manager DTOs and usecases.

New narrow ports:

```go
type ChannelBusinessReader interface {
    ScanChannelsSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
    GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
    HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error)
}

type ChannelBusinessOperator interface {
    UpdateInfo(ctx context.Context, info channelusecase.Info) error
    AddSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error
    RemoveSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error
    AddAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
    RemoveAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
    AddDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
    RemoveDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
    ListSubscribersPage(ctx context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error)
    ListAllowlistPage(ctx context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error)
    ListDenylistPage(ctx context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error)
}
```

Aggregation rules:

- Scan Slots in sorted `cluster.SlotIDs()` order.
- Apply `keyword` as trimmed channel ID substring filtering.
- Apply `type` as an optional channel type filter.
- Filter out internal member-list IDs and derived command-channel IDs before emitting manager list items.
- Build routing fields from `cluster.SlotForKey` and `cluster.HashSlotForKey`.
- Build `has_*` detail booleans through authoritative subscriber non-empty checks.

### `internal/access/manager`

Add `channels_biz.go` handlers and cursor helpers.

Routes:

```text
GET  /manager/channels
GET  /manager/channels/:channel_type/:channel_id
POST /manager/channels
GET  /manager/channels/:channel_type/:channel_id/subscribers
POST /manager/channels/:channel_type/:channel_id/subscribers/add
POST /manager/channels/:channel_type/:channel_id/subscribers/remove
GET  /manager/channels/:channel_type/:channel_id/allowlist
POST /manager/channels/:channel_type/:channel_id/allowlist/add
POST /manager/channels/:channel_type/:channel_id/allowlist/remove
GET  /manager/channels/:channel_type/:channel_id/denylist
POST /manager/channels/:channel_type/:channel_id/denylist/add
POST /manager/channels/:channel_type/:channel_id/denylist/remove
```

Permissions:

- read routes require `cluster.channel:r`
- write routes require `cluster.channel:w`

Error mapping:

- `400 bad_request`: invalid limit, cursor, channel type, channel ID, list kind, or empty UID list
- `404 not_found`: channel does not exist on detail/read paths
- `503 service_unavailable`: manager not configured or authoritative Slot leader unavailable
- `500 internal_error`: unexpected internal failure

### `internal/app`

Wire dependencies in the composition root:

- `ChannelBusinessReader`: `app.store`
- `ChannelBusinessOperator`: `app.channelApp`

Do not introduce a new broad service package or global aggregation object.

## Web Design

Update only the real React app under `web/`; do not touch `ui/`.

Files:

- `web/src/lib/manager-api.types.ts`
- `web/src/lib/manager-api.ts`
- `web/src/lib/manager-api.test.ts`
- `web/src/pages/channels-biz/page.tsx`
- `web/src/pages/channels-biz/page.test.tsx`
- `web/src/i18n/messages/en.ts`
- `web/src/i18n/messages/zh-CN.ts`
- `web/README.md`

Page behavior:

- Load `/manager/channels` on mount.
- Provide channel type filter and channel ID keyword search.
- Show list rows with channel ID, type label, status flags, Slot/hash-slot, and actions.
- Provide create/update metadata dialog for `ban`, `disband`, and `send_ban`.
- Open detail drawer for one channel.
- Detail drawer has three member tabs: subscribers, allowlist, denylist.
- Each tab supports load, load more, add UIDs, and remove one UID.
- Do not expose remove-all/set-all destructive operations.
- For person channels, disable or hide ordinary subscriber add/remove and explain why.

## Testing Strategy

Use TDD. Tests should stay focused and fast.

Backend tests:

- `pkg/slot/meta`: channel page scan order, cursor, invalid limit, cursor with channel type.
- `pkg/slot/proxy`: local and remote authoritative channel page scans.
- `internal/usecase/channel`: paginated subscribers, allowlist, denylist page methods.
- `internal/usecase/management`: list filtering, cursor validation, detail `has_*`, member page/action delegation, internal-channel filtering.
- `internal/access/manager`: route registration/permissions and HTTP behavior for list/detail/upsert/member pages/member actions.
- `internal/app`: wiring test for channel business dependencies.

Frontend tests:

- API client path and body tests.
- Page tests for initial load, search/type filter, load more, detail drawer, create/update, member add/remove, person-channel subscriber disabled state, and error states.

Verification commands:

```bash
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/proxy ./internal/usecase/channel ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/channels-biz/page.test.tsx
cd web && bun run build
```

No `test/e2e` scenario is required in this slice. A follow-up E2E can start a real cluster, create a channel, add subscribers through manager APIs, and verify send permission behavior through public message APIs.

## Risks And Mitigations

- Internal list rows may leak into channel inventory. Mitigation: filter `__wk_internal_memberlist__/` and `____cmd` rows before manager output.
- Exact member counts are expensive. Mitigation: expose paginated lists and cheap `has_*` booleans only.
- Destructive member operations are risky. Mitigation: omit remove-all/set-all from manager UI and API for this slice.
- Large UID mutation requests can exceed Raft command limits. Mitigation: reuse `internal/usecase/channel` chunking and existing command limits.
- Manager APIs could diverge from legacy semantics. Mitigation: reuse `internal/usecase/channel` for all mutations and add usecase tests around person-channel restrictions.

## Acceptance Criteria

- `/channels-biz` is no longer a placeholder.
- `GET /manager/channels` returns stable paginated business channel rows without internal member-list channels.
- `GET /manager/channels/:type/:id` returns authoritative channel flags and member-list non-empty indicators.
- `POST /manager/channels` creates or updates channel flags without resetting subscribers.
- Member list pages and add/remove actions work for subscribers, allowlist, and denylist.
- Person-channel ordinary subscriber mutations are rejected or disabled while allowlist/denylist mutations remain available.
- All targeted Go, web unit, and web build verification commands pass.
