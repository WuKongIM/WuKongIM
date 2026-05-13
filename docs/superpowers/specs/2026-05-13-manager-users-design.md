# Manager Users Design

## Goal

Add the first real manager user-management slice: manager-scoped user list/detail APIs, kick and token-reset operations, and the `/users` page in the real `web/` React admin project.

This turns the current roadmap placeholder into an operational page for inspecting registered users, their online state, device token state, and UID routing placement.

## Context

`docs/raw/web-admin-restructure.md` lists user management as a P1 business operation area:

- user list with pagination and search
- user detail with UID, online status, devices, and token information
- actions for ban/unban, force offline, and token reset

Current code already has reusable user usecases in `internal/usecase/user`:

- `UpdateToken`
- `DeviceQuit`
- `OnlineStatus`
- system UID operations

The manager API does not yet expose `/manager/users...`. The real frontend project is `web/`; `ui/` is an old prototype directory and is out of scope for this work.

## Scope

In scope:

- `GET /manager/users?keyword=&limit=&cursor=`
- `GET /manager/users/:uid`
- `POST /manager/users/:uid/kick`
- `POST /manager/users/:uid/token/reset`
- `web/src/pages/users/page.tsx` real implementation
- `web/src/lib/manager-api.ts` and `web/src/lib/manager-api.types.ts` user-management client bindings
- i18n copy under `web/src/i18n/messages/*`
- Unit tests for storage scan, proxy scan, management aggregation, manager HTTP, web API client, and web page behavior

Out of scope for this slice:

- `POST /manager/users/:uid/ban`
- `POST /manager/users/:uid/unban`
- gateway login rejection based on persisted user ban state
- user audit log/history
- indexed full-text or prefix search
- browser E2E and `test/e2e` black-box coverage
- any changes under `ui/`

Ban/unban should be designed as a follow-up because it needs a persistent user status field, slot FSM compatibility work, and gateway `IsBanned` integration. This slice may reserve copy/API notes for future ban/unban, but it should not expose a fake successful ban action.

## Recommended Approach

Use a manager aggregation path instead of pushing user-management rules into HTTP handlers:

```text
internal/access/manager -> internal/usecase/management -> pkg/slot/proxy + internal/usecase/user + presence/online
```

The HTTP layer parses parameters, applies permissions, maps errors, and renders DTOs. `internal/usecase/management` owns manager-facing aggregation. Existing `internal/usecase/user` remains the source of behavior for token update and device quit.

## API Design

### `GET /manager/users`

Lists users in stable manager pagination order.

Query parameters:

| Name | Required | Default | Notes |
| --- | --- | --- | --- |
| `keyword` | no | empty | Trimmed UID substring filter. Empty means full list. |
| `limit` | no | `50` | Maximum `200`. |
| `cursor` | no | empty | Opaque cursor returned by the previous page. |

Response:

```json
{
  "items": [
    {
      "uid": "u_10294",
      "slot_id": 1,
      "hash_slot": 37,
      "online": true,
      "online_device_count": 2,
      "online_device_flags": ["app", "web"],
      "device_count": 3,
      "token_set_count": 2
    }
  ],
  "has_more": true,
  "next_cursor": "V0tVUwEAAAAB..."
}
```

Field semantics:

- `uid`: user identity.
- `slot_id`: physical Slot selected by the current hash-slot table for the UID.
- `hash_slot`: logical hash slot selected by the current hash-slot table for the UID.
- `online`: true when authoritative presence has at least one live route.
- `online_device_count`: count of live presence routes for the UID.
- `online_device_flags`: deduplicated stable labels: `app`, `web`, `pc`, `system`, or `unknown`.
- `device_count`: count of persisted device rows found for the fixed device flag set `app/web/pc/system`.
- `token_set_count`: count of found device rows whose token is non-empty.

The response intentionally does not include `total`; an exact total would require full scanning all managed Slots and is too expensive for the default page path.

### User list cursor

Use an opaque base64 cursor similar to the existing manager channel-runtime cursor.

Internal shape:

```go
type UserListCursor struct {
    // SlotID is the current physical Slot scan position.
    SlotID uint32
    // UID is the last emitted UID inside SlotID.
    UID string
    // KeywordHash binds the cursor to the keyword used to create it.
    KeywordHash uint32
}
```

Rules:

- Cursor records the last scan/emit position.
- Cursor is bound to `keyword`. Reusing a cursor with a different keyword returns `400 invalid cursor`.
- `limit` may change between requests.
- `has_more=false` omits `next_cursor`.
- Cursor versioning should use a new magic value, for example `WKUL`, and reuse the existing manager cursor helper style.

### `GET /manager/users/:uid`

Returns one user detail.

Response:

```json
{
  "uid": "u_10294",
  "slot_id": 1,
  "hash_slot": 37,
  "online": true,
  "devices": [
    {
      "device_flag": "app",
      "device_level": "master",
      "token_set": true,
      "online": true,
      "online_session_count": 1
    }
  ],
  "connections": [
    {
      "node_id": 2,
      "session_id": 1001,
      "device_id": "iphone-15",
      "device_flag": "app",
      "device_level": "master",
      "listener": "tcp-wkproto",
      "connected_at": "2026-05-13T08:00:00Z",
      "remote_addr": "10.0.0.10:50000",
      "local_addr": "127.0.0.1:5100"
    }
  ]
}
```

Rules:

- Detail reads the user record from the authoritative Slot owner.
- Device rows are read for fixed flags `app/web/pc/system`; missing rows are omitted unless the device is currently online.
- Tokens are never returned as cleartext. Only `token_set` is exposed.
- Online routes come from authoritative presence lookup.
- Connection details should reuse the existing connection DTO shape where possible.

### `POST /manager/users/:uid/kick`

Forces one user or one user device class offline.

Request:

```json
{
  "device_flag": "all"
}
```

`device_flag` accepts: `all`, `app`, `web`, `pc`. `all` maps to app/web/pc to match the existing `DeviceQuit` semantics; `system` is visible in read models but is not kicked in this slice.

Response:

```json
{
  "uid": "u_10294",
  "device_flag": "all",
  "changed": true
}
```

Behavior:

- Reuse `internal/usecase/user.DeviceQuit`.
- For persisted devices, clear stored tokens so clients must authenticate again.
- Kick matching online sessions through the existing usecase behavior.
- The manager endpoint does not bypass cluster semantics. Writes must route through the authoritative UID/Slot path just like existing user token operations.

### `POST /manager/users/:uid/token/reset`

Resets a token for one UID/device flag.

Request:

```json
{
  "device_flag": "app",
  "device_level": "master",
  "token": "optional-manual-token"
}
```

Response:

```json
{
  "uid": "u_10294",
  "device_flag": "app",
  "device_level": "master",
  "token": "new-token-visible-once"
}
```

Behavior:

- If request `token` is empty, the backend generates a cryptographically random token.
- Reuse `internal/usecase/user.UpdateToken`.
- Return the new token only in this operation response.
- The list and detail APIs only expose `token_set`.

## Backend Design

### `pkg/slot/meta`

Add single-hash-slot user page scanning:

```go
type UserCursor struct {
    // UID is the last emitted user ID in a hash-slot page scan.
    UID string
}

func (s *ShardStore) ListUsersPage(ctx context.Context, after UserCursor, limit int) ([]User, UserCursor, bool, error)
```

Implementation notes:

- Scan `UserTable` primary keys under one hash slot.
- Use `encodeUserPrimaryKey` and a primary-prefix helper similar to channel runtime metadata paging.
- Return users ordered by UID primary-key order.
- `done=false` means the caller should continue with the returned cursor.
- Validate `limit > 0` and cursor UID length.

### `pkg/slot/proxy`

Add physical-Slot authoritative scanning:

```go
func (s *Store) ScanUsersSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error)
```

Implementation notes:

- For a local authoritative Slot, merge the user pages of all hash slots owned by the physical Slot.
- Use a small heap, following the existing `scanChannelRuntimeMetaSlotPageLocal` pattern.
- For a remote authoritative Slot leader, add an identity RPC operation such as `scan_users_page`.
- Return slot-leader authoritative errors as service-unavailable at the manager HTTP layer.

### `internal/usecase/management`

Add `users.go` with manager DTOs and usecases:

```go
type ListUsersRequest struct {
    Limit int
    Cursor UserListCursor
    Keyword string
}

type ListUsersResponse struct {
    Items []UserListItem
    HasMore bool
    NextCursor UserListCursor
}

type UserReader interface {
    ScanUsersSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error)
    GetUser(ctx context.Context, uid string) (metadb.User, error)
    GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

type UserOperator interface {
    UpdateToken(ctx context.Context, cmd user.UpdateTokenCommand) error
    DeviceQuit(ctx context.Context, cmd user.DeviceQuitCommand) error
}
```

Aggregation rules:

- Scan Slots in sorted `cluster.SlotIDs()` order.
- Apply `keyword` as trimmed UID substring filtering.
- Build online summaries through a presence directory (`EndpointsByUIDs`).
- Count persisted devices for `app/web/pc/system`.
- Do not return token values in list/detail.
- Generate reset token in the usecase when no token is supplied by the request.

### `internal/access/manager`

Add `users.go` routes and DTO conversion.

Routes:

```text
GET  /manager/users
GET  /manager/users/:uid
POST /manager/users/:uid/kick
POST /manager/users/:uid/token/reset
```

Permission middleware:

- Read routes require `cluster.user:r`.
- Write routes require `cluster.user:w`.

Error mapping:

- `400 bad_request`: invalid limit, cursor, device flag, device level, or empty UID.
- `404 not_found`: user does not exist on detail/read paths.
- `409 conflict`: reserved for future semantic conflicts.
- `503 service_unavailable`: manager not configured or authoritative Slot leader read/write unavailable.
- `500 internal_error`: unexpected internal failure.

### `internal/app`

Wire the management user reader/operator in the composition root:

- user reader: `app.store` (`pkg/slot/proxy.Store`) after it exposes user page scanning.
- user operator: `app.userApp` or a small adapter around it.
- presence directory: reuse the existing authority client so online state remains authoritative for the UID's Slot.

Do not add a new global service package or a broad aggregation object.

## Web Design

The real frontend lives under `web/`. The `ui/` prototype directory is excluded.

Update these files:

- `web/src/pages/users/page.tsx`
- `web/src/lib/manager-api.ts`
- `web/src/lib/manager-api.types.ts`
- `web/src/i18n/messages/zh-CN.ts`
- `web/src/i18n/messages/en.ts`
- `web/src/pages/users/page.test.tsx`
- `web/src/lib/manager-api.test.ts`

The page should use existing project components:

- `PageContainer`
- `PageHeader`
- `SectionCard`
- `ResourceState`
- `StatusBadge`
- `TableToolbar`
- `DetailSheet`
- `ConfirmDialog`
- `ActionFormDialog`
- `Button`

Interactions:

- Initial load calls `getUsers({ limit: 50 })`.
- UID search calls `getUsers({ keyword, limit: 50 })` and resets cursor.
- `Load more` passes the previous `next_cursor`.
- Row click or detail button calls `getUser(uid)` and opens a detail sheet.
- Kick action opens confirm dialog and calls `kickUser(uid, input)`.
- Token reset opens action-form dialog and calls `resetUserToken(uid, input)`.
- A reset token success shows the returned token once.
- Successful writes refresh the detail and the currently loaded list.

UI states:

- Empty list: no users or no UID match.
- `403`: permission-denied state mentioning `cluster.user`.
- `503`: authoritative Slot read/write unavailable; prompt retry.
- Other errors: generic manager API error display.

## Testing

### Go unit tests

`pkg/slot/meta`:

- lists users in stable UID order
- continues after cursor
- returns `done=false` when another page exists
- rejects invalid limit/cursor
- returns empty done page for empty shard

`pkg/slot/proxy`:

- scans a local authoritative physical Slot across multiple hash slots
- scans a remote authoritative Slot through identity RPC
- returns service-unavailable-style errors when the Slot leader is unavailable

`internal/usecase/management`:

- list aggregates slot/hash-slot, online summary, device count, and token-set count
- keyword filtering returns only matching UIDs
- cursor and keyword mismatch is rejected
- detail merges persisted devices and online routes
- kick maps device flag labels to `DeviceQuitCommand`
- token reset generates a token when omitted and delegates to `UpdateToken`

`internal/access/manager`:

- validates list limit/cursor/keyword
- maps not found and authoritative read errors
- enforces read/write permissions
- renders list/detail/kick/reset DTOs

`internal/app`:

- management app wiring includes user reader/operator when manager is enabled

### Web tests

`web/src/lib/manager-api.test.ts`:

- list path encodes `keyword`, `limit`, and `cursor`
- detail path encodes UID
- kick body uses `device_flag`
- reset body uses `device_flag`, `device_level`, and optional `token`

`web/src/pages/users/page.test.tsx`:

- renders first page
- searches by UID
- loads more using `next_cursor`
- opens detail sheet
- kicks a user and refreshes
- resets a token and displays the new token once
- handles empty, `403`, and `503` states

### E2E

No `test/e2e` scenario is required in this slice. A follow-up E2E can start a real three-node cluster, create/update a user token through public APIs, query manager users from a non-owner node, then verify kick/reset behavior through real WKProto connections.

## Rollout Plan

1. Add storage and proxy user page reads.
2. Add management user aggregation and operations.
3. Add manager HTTP routes and permissions.
4. Wire through `internal/app`.
5. Add `web/` API client and `/users` page.
6. Update `web/README.md` page/API matrix from placeholder to implemented.
7. Run targeted Go and web tests.

## Risks And Mitigations

- User search is a bounded scan, not an indexed query. Mitigation: cap page limits and document first-version semantics.
- Pagination is not a cross-Slot snapshot. Mitigation: make the view operational and refreshable; no exact `total` is exposed.
- Token values are sensitive. Mitigation: list/detail expose only token presence; reset response shows generated token once.
- Ban/unban needs deeper auth semantics. Mitigation: split it into a follow-up design and avoid fake successful UI actions.
- Slot leadership may change during reads. Mitigation: proxy reads through authoritative Slot leader and surface unavailable states as `503`.

## Acceptance Criteria

- `GET /manager/users` returns a stable paginated list with online and device summaries.
- `GET /manager/users/:uid` returns route, device, and online detail without exposing token values.
- `POST /manager/users/:uid/kick` delegates to the existing user device-quit behavior.
- `POST /manager/users/:uid/token/reset` delegates to existing update-token behavior and returns the new token only in the operation response.
- `/users` in `web/` is no longer a placeholder and uses real manager APIs.
- `ui/` remains untouched by this feature.
- Targeted Go and web tests pass.
