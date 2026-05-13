# Manager System Users Design

## Goal

Implement the first real `/system-users` management slice in the `web/` admin app.

This feature gives operators a manager-scoped way to list persisted system UIDs, add system UIDs, and remove system UIDs without calling the legacy `/user/systemuids*` API surface from the browser.

## Context

`docs/raw/web-admin-restructure.md` lists `/system-users` under Business management. The current route `web/src/pages/system-users/page.tsx` is still a placeholder.

Existing backend behavior already provides the core primitives:

- `internal/usecase/user.App` persists system UIDs via the internal subscriber-list channel `__wk_internal_system_uids__` with channel type `frame.SYSTEM`.
- `AddSystemUIDs` and `RemoveSystemUIDs` mutate persisted state and update the node-local system UID cache.
- `internal/app.clusterUserUsecase` wraps the local user usecase and broadcasts persisted add/remove cache updates to peer nodes.
- Legacy HTTP routes exist under `/user/systemuids`, `/user/systemuids_add`, `/user/systemuids_remove`, plus cache-only endpoints.
- The manager UI already uses JWT/permission-gated `/manager/*` APIs and must not call legacy user endpoints directly.

## Scope

In scope:

- `GET /manager/system-users`
- `POST /manager/system-users/add`
- `POST /manager/system-users/remove`
- Manager usecase DTOs and orchestration over the existing user system UID methods.
- Manager HTTP handlers, route registration, permission checks, and tests.
- `web/src/lib/manager-api.ts` and `.types.ts` client bindings.
- `web/src/pages/system-users/page.tsx` real implementation.
- i18n strings, focused unit tests, and docs status updates.

Out of scope:

- Manager wrappers for cache-only legacy APIs (`/user/systemuids_add_to_cache`, `/user/systemuids_remove_from_cache`).
- remove-all operations.
- pagination/cursors for the first slice.
- per-UID purpose/description metadata.
- audit history.
- browser E2E and `test/e2e` coverage.
- any changes under `ui/`.

## API Design

### `GET /manager/system-users`

Returns all persisted system UIDs.

Response:

```json
{
  "items": [
    { "uid": "sys.notify" },
    { "uid": "sys.robot" }
  ],
  "total": 2
}
```

Rules:

- The backend reads persisted system UIDs through `user.App.ListSystemUIDs` via a management-layer port.
- Returned UIDs are trimmed, deduplicated, and sorted lexicographically for stable manager rendering.
- The built-in default system UID `____system` is not injected unless it is explicitly persisted by the existing user usecase. The page should explain that built-in behavior separately if needed.
- Exact counts are cheap because this MVP returns all persisted rows.

### `POST /manager/system-users/add`

Adds one or more persisted system UIDs.

Request:

```json
{ "uids": ["sys.notify", "sys.robot"] }
```

Response:

```json
{
  "uids": ["sys.notify", "sys.robot"],
  "changed": true
}
```

Rules:

- UIDs are trimmed, deduplicated in request order, and empty values are ignored.
- Empty post-normalization UID lists return `400 bad_request`.
- The operation calls the existing persisted add path. It must not call cache-only mutation APIs.
- Existing add semantics remain idempotent.
- Persisted add/remove paths are responsible for node-local cache update and peer cache broadcast through `internal/app.clusterUserUsecase`.

### `POST /manager/system-users/remove`

Removes one or more persisted system UIDs.

Request and response use the same shape as add:

```json
{ "uids": ["sys.robot"] }
```

```json
{
  "uids": ["sys.robot"],
  "changed": true
}
```

Rules:

- Normalization and empty-list validation match add.
- Removing a missing UID is accepted as idempotent if the underlying user usecase accepts it.
- No remove-all route is exposed in this slice.

## Permissions

Use existing manager user permissions:

- `GET /manager/system-users`: `cluster.user:r`
- `POST /manager/system-users/add`: `cluster.user:w`
- `POST /manager/system-users/remove`: `cluster.user:w`

System UIDs are identity-level bypass accounts, so the user permission resource is a better fit than channel permissions.

## Backend Architecture

Add a narrow management port instead of having manager HTTP call legacy access handlers:

```go
type SystemUserOperator interface {
    // ListSystemUIDs returns the persisted system account UID list.
    ListSystemUIDs(ctx context.Context) ([]string, error)
    // AddSystemUIDs persists system account UIDs and refreshes caches.
    AddSystemUIDs(ctx context.Context, uids []string) error
    // RemoveSystemUIDs removes persisted system account UIDs and refreshes caches.
    RemoveSystemUIDs(ctx context.Context, uids []string) error
}
```

`internal/usecase/management` owns manager-facing DTOs and request validation. `internal/access/manager` stays thin: bind JSON, call management usecase, map errors, and serialize DTOs. `internal/app` wires the existing cluster-aware user usecase into `management.New`.

No new broad service package is introduced.

## Web Design

Replace `web/src/pages/system-users/page.tsx` placeholder with a focused manager page:

- Header and section card follow existing `UsersPage` and `ChannelsBizPage` patterns.
- A table lists each UID and a remove button.
- Header action opens an add dialog.
- Add dialog accepts textarea input and normalizes comma, whitespace, and newline-separated UIDs.
- Remove uses `ConfirmDialog` for one UID at a time.
- `ResourceState` covers loading, empty, forbidden, unavailable, and generic error states.
- UI copy explains system UIDs are privileged sender/receiver accounts and that changes are persisted, not cache-only.

## Error Handling

- Missing management dependency: `503 service_unavailable`.
- Invalid JSON: `400 bad_request`.
- Empty UID list after normalization: `400 bad_request`.
- User usecase validation errors: `400 bad_request` unless the error is clearly service unavailable.
- Authorization failures use existing manager auth middleware responses.
- Web maps `ManagerApiError` 403 and 503 to existing `ResourceState` variants.

## Testing Strategy

Backend:

- `internal/usecase/management`: fake `SystemUserOperator`; verify stable list normalization, add/remove request normalization, empty input rejection, and operator errors.
- `internal/access/manager`: verify route auth, response shapes, bad request handling, and method calls.
- `internal/app`: verify management wiring includes system UID operator when manager is enabled.

Frontend:

- `web/src/lib/manager-api.test.ts`: verify paths and JSON bodies for list/add/remove.
- `web/src/pages/system-users/page.test.tsx`: verify initial load, add normalized textarea values, remove confirmation, empty state, 403, and 503.

Verification commands:

```bash
GOWORK=off go test ./internal/usecase/user ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/system-users/page.test.tsx
cd web && bun run build
```

## Follow-ups

- Add audit events for system UID mutations.
- Add optional purpose/owner metadata if product requirements need it.
- Add black-box manager e2e coverage after the web page and API stabilize.
