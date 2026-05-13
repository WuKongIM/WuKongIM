# Manager Permissions Readonly Design

## Goal

Implement the first real `/settings/permissions` management slice in the `web/` admin app.

This feature gives operators a safe, read-only view of manager authentication status, static manager users, and the permission resource/action catalog. It improves security visibility without introducing runtime permission editing, config writes, role models, or password handling.

## Context

`docs/raw/web-admin-restructure.md` lists `/settings/permissions` under System Settings. The current route `web/src/pages/settings/permissions/page.tsx` is still a placeholder.

Existing manager authentication is already static and server-local:

- `internal/access/manager.AuthConfig` contains `On`, JWT settings, and `Users`.
- `internal/access/manager.UserConfig` contains `Username`, `Password`, and `Permissions`.
- Login responses already return the current user's permissions.
- Permission checks are enforced by `requirePermission(resource, action)` and support resource/action wildcard grants.
- Config wiring maps `internal/app.ManagerUserConfig` into manager access config.

The MVP should expose a sanitized snapshot of this existing auth state through a manager-scoped endpoint. It must never return manager passwords, JWT secrets, password hashes, raw config files, or environment-derived secrets.

## Scope

In scope:

- `GET /manager/permissions` manager HTTP endpoint.
- Sanitized response DTOs for auth state, static users, grants, and resource catalog.
- Permission gate for the endpoint.
- Web API client bindings and tests.
- Replace `/settings/permissions` placeholder with a read-only page.
- i18n strings, focused tests, and docs status updates.

Out of scope:

- Creating, editing, or deleting manager users.
- Updating permissions from the browser.
- Writing `wukongim.conf` or environment configuration.
- Hot reloading auth config.
- Role/group abstractions.
- Password reset or password display.
- Audit history.
- `ui/` prototype changes.

## API Design

### `GET /manager/permissions`

Returns a sanitized manager permissions snapshot.

Response example when auth is enabled:

```json
{
  "auth_enabled": true,
  "current_user": "admin",
  "users": [
    {
      "username": "admin",
      "permissions": [
        { "resource": "*", "actions": ["*"] }
      ]
    },
    {
      "username": "viewer",
      "permissions": [
        { "resource": "cluster.node", "actions": ["r"] },
        { "resource": "cluster.slot", "actions": ["r"] }
      ]
    }
  ],
  "resources": [
    {
      "resource": "cluster.permission",
      "actions": ["r"],
      "description": "Read manager authentication and permission configuration snapshots."
    }
  ]
}
```

Response example when auth is disabled:

```json
{
  "auth_enabled": false,
  "current_user": "",
  "users": [],
  "resources": [
    { "resource": "*", "actions": ["*"], "description": "Wildcard access to all manager resources and actions." }
  ]
}
```

Rules:

- `users` are static manager users known to `authState`.
- `users[].permissions` are copied from sanitized grants, preserving configured resource/action names.
- The backend sorts users by username for stable rendering.
- Each user's grants are sorted by resource and actions for stable rendering.
- `resources` is a built-in catalog maintained beside manager route registration.
- `current_user` comes from the authenticated request context when auth is enabled.
- No password, JWT secret, token, config path, environment value, or secret-like field is returned.

## Permission Model

Use a new read permission:

- `GET /manager/permissions`: `cluster.permission:r`

Existing wildcard semantics continue to work:

- `resource="*", actions=["*"]` can read the endpoint.
- `resource="cluster.permission", actions=["*"]` can read the endpoint.

When auth is disabled, no JWT or permission middleware runs. The endpoint returns `auth_enabled=false` and an empty user list, because there are no authenticated static principals to display.

The implementation should document that operators must grant `cluster.permission:r` or wildcard access to any account that should view the permissions page.

## Resource Catalog

The first catalog should cover resources already enforced in `internal/access/manager/routes.go`:

| Resource | Actions | Description |
| --- | --- | --- |
| `cluster.overview` | `r` | Read dashboard and overview summaries. |
| `cluster.node` | `r`, `w` | Read node inventory and perform node lifecycle actions. |
| `cluster.slot` | `r`, `w` | Read Slot state and perform Slot operations. |
| `cluster.controller` | `r`, `w` | Read or compact Controller Raft logs. |
| `cluster.task` | `r` | Read reconcile tasks. |
| `cluster.connection` | `r` | Read connection inventory and details. |
| `cluster.network` | `r` | Read network diagnostics summaries. |
| `cluster.diagnostics` | `r` | Read diagnostics and message trace data. |
| `cluster.user` | `r`, `w` | Read users and mutate user/system UID state. |
| `cluster.channel` | `r`, `w` | Read and mutate channel, message, and channel-cluster operations. |
| `cluster.permission` | `r` | Read manager auth and permission snapshots. |
| `*` | `*` | Wildcard access to all manager resources and actions. |

The catalog is intentionally descriptive. It does not perform authorization by itself; route middleware remains the source of enforcement.

## Backend Architecture

Keep this slice inside `internal/access/manager` because it exposes the manager access layer's own static auth configuration. It does not need a new reusable business usecase.

Add small helpers near auth/server code:

- `PermissionSnapshotResponse`
- `PermissionUserDTO`
- `PermissionGrantDTO`
- `PermissionResourceDTO`
- `authState.permissionUsers()` or equivalent sanitized snapshot helper
- `managerPermissionCatalog()` for the built-in resource matrix
- `handlePermissions` HTTP handler

Route registration:

```go
permissions := s.engine.Group("/manager")
if s.auth.enabled() {
    permissions.Use(s.requirePermission("cluster.permission", "r"))
}
permissions.GET("/permissions", s.handlePermissions)
```

This preserves the current layering:

- `internal/access/manager` owns auth, JWT, route permission middleware, and sanitized auth snapshots.
- No new broad service package is introduced.
- `internal/app` does not need new wiring for this read-only endpoint because the data already lives in `accessmanager.Options.Auth`.

## Web Design

Replace `web/src/pages/settings/permissions/page.tsx` placeholder with a read-only page:

- Header follows existing settings page shell patterns.
- Summary cards show auth status, current user, static user count, and catalog resource count.
- User table lists usernames and permission grant chips.
- Resource matrix lists each resource, supported actions, and a short explanation.
- A note states the view is read-only and sourced from server-side static manager config.
- Empty state handles auth disabled or zero configured users.
- `ResourceState` handles loading, forbidden, unavailable, and generic error states.

Suggested page sections:

1. **Authentication Summary**
   - Auth enabled/disabled.
   - Current user or `-`.
   - Static user count.

2. **Static Manager Users**
   - `username`.
   - grants as chips: `cluster.node:r`, `cluster.slot:r/w`, `*:*`.

3. **Permission Catalog**
   - `resource`.
   - supported actions.
   - description.

## Error Handling

Backend:

- Missing/invalid JWT: existing `401 unauthorized` middleware response.
- Missing `cluster.permission:r`: existing `403 forbidden` middleware response.
- Auth disabled: return `200` with `auth_enabled=false`.
- Handler should not expose internal config parsing errors because it only reads already-normalized in-memory auth state.

Frontend:

- `ManagerApiError` 403 maps to forbidden `ResourceState`.
- `ManagerApiError` 503 maps to unavailable `ResourceState`.
- Other errors map to generic error `ResourceState`.
- Auth disabled with no users renders the summary plus empty static-user state, not an error.

## Testing Strategy

Backend:

- `internal/access/manager`: test `GET /manager/permissions` returns auth status, current user, sanitized static users, and catalog rows.
- Verify passwords/JWT secrets are absent from the JSON body.
- Verify endpoint requires `cluster.permission:r` when auth is enabled.
- Verify wildcard permissions can access the endpoint.
- Verify auth-disabled server returns `200` with `auth_enabled=false`.

Frontend:

- `web/src/lib/manager-api.test.ts`: verify `getPermissions()` calls `/manager/permissions`.
- `web/src/pages/settings/permissions/page.test.tsx`: verify summary cards, user grants, resource catalog, auth-disabled empty users, 403, and 503.

Verification commands:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerPermissions' -count=1
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/settings/permissions/page.test.tsx
cd web && bun run build
```

Final focused verification should include:

```bash
GOWORK=off go test ./internal/access/manager ./internal/app -count=1
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/settings/permissions/page.test.tsx
cd web && bun run build
```

## Documentation Updates

Update:

- `web/README.md`: mark `/settings/permissions` implemented with `GET /manager/permissions`.
- `docs/raw/web-admin-restructure.md`: mark permissions read-only MVP complete and note edit/persistence as follow-up.
- `internal/FLOW.md` only if manager access/API capability notes become stale.

## Follow-ups

- Persisted permission management with create/edit/delete flows.
- Role/group abstraction if static per-user grants become too verbose.
- Audit events for permission changes once write APIs exist.
- Password rotation or manager account lifecycle APIs.
- Browser E2E after the read-only page stabilizes.
