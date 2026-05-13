# Manager Permissions Readonly Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only `/settings/permissions` page backed by `GET /manager/permissions` so operators can inspect manager auth status, static users, and resource/action grants safely.

**Architecture:** Keep the backend slice in `internal/access/manager` because it exposes sanitized manager auth state owned by that package. Add a small read-only HTTP handler and auth snapshot helpers; no management usecase or app wiring is needed. Web uses the existing manager API client, ResourceState, and page shell patterns.

**Tech Stack:** Go, Gin, existing manager JWT/permission middleware, React 19, Vite, Vitest, Testing Library.

---

## Pre-Execution Notes

- Execute in the isolated worktree: `.worktrees/manager-permissions-readonly` on branch `feat/manager-permissions-readonly`.
- The main worktree may contain unrelated user edits. Do not touch, revert, stage, or commit unrelated changes.
- `ui/` is the prototype directory and is out of scope.
- Read `AGENTS.md` before implementation.
- If a touched package has `FLOW.md`, read it first and update it only if the behavior description becomes stale.
- Use TDD. Each implementation task starts with a failing test, then minimal code, targeted verification, then a small commit.
- Do not add write APIs, config persistence, role management, password reset, JWT secret display, or config file writes.

## File Structure

### Backend manager permissions endpoint

- Create: `internal/access/manager/permissions_test.go`
  - HTTP tests for sanitized snapshot, permission enforcement, wildcard access, and auth-disabled behavior.
- Create: `internal/access/manager/permissions.go`
  - Response DTOs, built-in resource catalog, sanitized auth snapshot helpers, and `handlePermissions`.
- Modify: `internal/access/manager/routes.go`
  - Register `GET /manager/permissions` with `cluster.permission:r` when auth is enabled.

### Web API client

- Modify: `web/src/lib/manager-api.types.ts`
  - Add permissions response/user/grant/resource types.
- Modify: `web/src/lib/manager-api.ts`
  - Add `getPermissions()`.
- Modify: `web/src/lib/manager-api.test.ts`
  - Add API client test for `/manager/permissions`.

### Web page

- Create: `web/src/pages/settings/permissions/page.test.tsx`
  - Page behavior tests for successful snapshot, auth-disabled empty users, and error mapping.
- Modify: `web/src/pages/settings/permissions/page.tsx`
  - Replace placeholder with real read-only UI.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English permissions page strings.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese permissions page strings.

### Docs

- Modify: `web/README.md`
  - Mark `/settings/permissions` implemented with `GET /manager/permissions`.
- Modify: `docs/raw/web-admin-restructure.md`
  - Mark read-only permissions MVP complete; keep edit/persistence as follow-ups.
- Modify if needed: `internal/FLOW.md`
  - Only if manager access/API capability notes become stale.

---

## Task 1: Add Manager Permissions HTTP Snapshot

**Files:**
- Create: `internal/access/manager/permissions_test.go`
- Create: `internal/access/manager/permissions.go`
- Modify: `internal/access/manager/routes.go`

- [ ] **Step 1: Write failing HTTP tests**

Create `internal/access/manager/permissions_test.go`:

```go
package manager

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestManagerPermissionsReturnsSanitizedSnapshot(t *testing.T) {
    srv := New(Options{
        Auth: AuthConfig{
            On:        true,
            JWTSecret: "jwt-secret-that-must-not-leak",
            JWTIssuer: "test-manager",
            Users: []UserConfig{
                {
                    Username: "viewer",
                    Password: "viewer-password-that-must-not-leak",
                    Permissions: []PermissionConfig{
                        {Resource: "cluster.node", Actions: []string{"r"}},
                    },
                },
                {
                    Username: "admin",
                    Password: "admin-password-that-must-not-leak",
                    Permissions: []PermissionConfig{
                        {Resource: "cluster.permission", Actions: []string{"r"}},
                        {Resource: "cluster.node", Actions: []string{"w", "r"}},
                    },
                },
            },
        },
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.NotContains(t, rec.Body.String(), "admin-password-that-must-not-leak")
    require.NotContains(t, rec.Body.String(), "viewer-password-that-must-not-leak")
    require.NotContains(t, rec.Body.String(), "jwt-secret-that-must-not-leak")
    require.JSONEq(t, `{
        "auth_enabled": true,
        "current_user": "admin",
        "users": [
            {
                "username": "admin",
                "permissions": [
                    {"resource":"cluster.node","actions":["r","w"]},
                    {"resource":"cluster.permission","actions":["r"]}
                ]
            },
            {
                "username": "viewer",
                "permissions": [
                    {"resource":"cluster.node","actions":["r"]}
                ]
            }
        ],
        "resources": [
            {"resource":"*","actions":["*"],"description":"Wildcard access to all manager resources and actions."},
            {"resource":"cluster.overview","actions":["r"],"description":"Read dashboard and overview summaries."},
            {"resource":"cluster.node","actions":["r","w"],"description":"Read node inventory and perform node lifecycle actions."},
            {"resource":"cluster.slot","actions":["r","w"],"description":"Read Slot state and perform Slot operations."},
            {"resource":"cluster.controller","actions":["r","w"],"description":"Read or compact Controller Raft logs."},
            {"resource":"cluster.task","actions":["r"],"description":"Read reconcile tasks."},
            {"resource":"cluster.connection","actions":["r"],"description":"Read connection inventory and details."},
            {"resource":"cluster.network","actions":["r"],"description":"Read network diagnostics summaries."},
            {"resource":"cluster.diagnostics","actions":["r"],"description":"Read diagnostics and message trace data."},
            {"resource":"cluster.user","actions":["r","w"],"description":"Read users and mutate user or system UID state."},
            {"resource":"cluster.channel","actions":["r","w"],"description":"Read and mutate channel, message, and channel-cluster operations."},
            {"resource":"cluster.permission","actions":["r"],"description":"Read manager authentication and permission configuration snapshots."}
        ]
    }`, rec.Body.String())
}

func TestManagerPermissionsRequiresPermission(t *testing.T) {
    srv := New(Options{Auth: testAuthConfig([]UserConfig{{
        Username: "viewer",
        Password: "secret",
        Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}},
    }})})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerPermissionsAllowsWildcardPermission(t *testing.T) {
    srv := New(Options{Auth: testAuthConfig([]UserConfig{{
        Username: "root",
        Password: "secret",
        Permissions: []PermissionConfig{{Resource: "*", Actions: []string{"*"}}},
    }})})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "root"))

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
}

func TestManagerPermissionsReturnsDisabledSnapshotWithoutAuth(t *testing.T) {
    srv := New(Options{})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    var body PermissionSnapshotResponse
    require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
    require.False(t, body.AuthEnabled)
    require.Empty(t, body.CurrentUser)
    require.Empty(t, body.Users)
    require.NotEmpty(t, body.Resources)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerPermissions' -count=1
```

Expected: FAIL because `/manager/permissions`, DTOs, and `handlePermissions` do not exist.

- [ ] **Step 3: Implement DTOs, sanitized snapshot helpers, and handler**

Create `internal/access/manager/permissions.go`:

```go
package manager

import (
    "net/http"
    "sort"

    "github.com/gin-gonic/gin"
)

// PermissionSnapshotResponse is the sanitized manager permissions response body.
type PermissionSnapshotResponse struct {
    // AuthEnabled reports whether manager JWT auth and permission checks are enabled.
    AuthEnabled bool `json:"auth_enabled"`
    // CurrentUser is the authenticated manager username when auth is enabled.
    CurrentUser string `json:"current_user"`
    // Users contains static manager users without secrets.
    Users []PermissionUserDTO `json:"users"`
    // Resources contains the built-in manager permission catalog.
    Resources []PermissionResourceDTO `json:"resources"`
}

// PermissionUserDTO describes one static manager user without secrets.
type PermissionUserDTO struct {
    // Username is the static manager login identity.
    Username string `json:"username"`
    // Permissions contains sanitized resource/action grants.
    Permissions []PermissionGrantDTO `json:"permissions"`
}

// PermissionGrantDTO describes one manager resource grant.
type PermissionGrantDTO struct {
    // Resource is the protected manager resource name.
    Resource string `json:"resource"`
    // Actions contains sorted action codes granted on the resource.
    Actions []string `json:"actions"`
}

// PermissionResourceDTO describes one built-in manager permission resource.
type PermissionResourceDTO struct {
    // Resource is the protected manager resource name.
    Resource string `json:"resource"`
    // Actions contains supported action codes for this resource.
    Actions []string `json:"actions"`
    // Description explains the resource scope for operators.
    Description string `json:"description"`
}

func (s *Server) handlePermissions(c *gin.Context) {
    currentUser := ""
    if s.auth.enabled() {
        if value, ok := c.Get(managerUsernameContextKey); ok {
            currentUser, _ = value.(string)
        }
    }
    c.JSON(http.StatusOK, PermissionSnapshotResponse{
        AuthEnabled: s.auth.enabled(),
        CurrentUser: currentUser,
        Users:       s.auth.permissionUsers(),
        Resources:   managerPermissionCatalog(),
    })
}

func (a authState) permissionUsers() []PermissionUserDTO {
    if !a.enabled() {
        return nil
    }
    users := make([]PermissionUserDTO, 0, len(a.users))
    for username, principal := range a.users {
        users = append(users, PermissionUserDTO{
            Username:    username,
            Permissions: permissionGrantDTOs(principal.grants),
        })
    }
    sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
    return users
}

func permissionGrantDTOs(grants []PermissionConfig) []PermissionGrantDTO {
    out := make([]PermissionGrantDTO, 0, len(grants))
    for _, grant := range grants {
        actions := append([]string(nil), grant.Actions...)
        sort.Strings(actions)
        out = append(out, PermissionGrantDTO{Resource: grant.Resource, Actions: actions})
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
    return out
}

func managerPermissionCatalog() []PermissionResourceDTO {
    return []PermissionResourceDTO{
        {Resource: "*", Actions: []string{"*"}, Description: "Wildcard access to all manager resources and actions."},
        {Resource: "cluster.overview", Actions: []string{"r"}, Description: "Read dashboard and overview summaries."},
        {Resource: "cluster.node", Actions: []string{"r", "w"}, Description: "Read node inventory and perform node lifecycle actions."},
        {Resource: "cluster.slot", Actions: []string{"r", "w"}, Description: "Read Slot state and perform Slot operations."},
        {Resource: "cluster.controller", Actions: []string{"r", "w"}, Description: "Read or compact Controller Raft logs."},
        {Resource: "cluster.task", Actions: []string{"r"}, Description: "Read reconcile tasks."},
        {Resource: "cluster.connection", Actions: []string{"r"}, Description: "Read connection inventory and details."},
        {Resource: "cluster.network", Actions: []string{"r"}, Description: "Read network diagnostics summaries."},
        {Resource: "cluster.diagnostics", Actions: []string{"r"}, Description: "Read diagnostics and message trace data."},
        {Resource: "cluster.user", Actions: []string{"r", "w"}, Description: "Read users and mutate user or system UID state."},
        {Resource: "cluster.channel", Actions: []string{"r", "w"}, Description: "Read and mutate channel, message, and channel-cluster operations."},
        {Resource: "cluster.permission", Actions: []string{"r"}, Description: "Read manager authentication and permission configuration snapshots."},
    }
}
```

- [ ] **Step 4: Register the route**

In `internal/access/manager/routes.go`, add near other settings/diagnostic read routes:

```go
permissions := s.engine.Group("/manager")
if s.auth.enabled() {
    permissions.Use(s.requirePermission("cluster.permission", "r"))
}
permissions.GET("/permissions", s.handlePermissions)
```

- [ ] **Step 5: Run targeted HTTP tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerPermissions' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all manager HTTP tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit backend endpoint**

```bash
git add internal/access/manager/permissions.go internal/access/manager/permissions_test.go internal/access/manager/routes.go
git commit -m "feat: expose manager permissions snapshot"
```

---

## Task 2: Add Web API Client Bindings

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client test**

In `web/src/lib/manager-api.test.ts`, add `getPermissions` to the import list from `@/lib/manager-api`.

Add this test near the existing manager overview/auth tests:

```ts
it("fetches manager permissions", async () => {
  const payload = {
    auth_enabled: true,
    current_user: "admin",
    users: [{ username: "admin", permissions: [{ resource: "*", actions: ["*"] }] }],
    resources: [{ resource: "cluster.permission", actions: ["r"], description: "Read manager permissions." }],
  }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

  await expect(getPermissions()).resolves.toEqual(payload)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/permissions",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

- [ ] **Step 2: Run API client tests and verify failure**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: FAIL because `getPermissions` does not exist.

- [ ] **Step 3: Add permissions response types**

In `web/src/lib/manager-api.types.ts`, add near `ManagerPermission`:

```ts
export type ManagerPermissionGrant = {
  resource: string
  actions: string[]
}

export type ManagerPermissionUser = {
  username: string
  permissions: ManagerPermissionGrant[]
}

export type ManagerPermissionResource = {
  resource: string
  actions: string[]
  description: string
}

export type ManagerPermissionsResponse = {
  auth_enabled: boolean
  current_user: string
  users: ManagerPermissionUser[]
  resources: ManagerPermissionResource[]
}
```

- [ ] **Step 4: Add API function**

In `web/src/lib/manager-api.ts`, import `ManagerPermissionsResponse` and add:

```ts
export function getPermissions() {
  return jsonManagerFetch<ManagerPermissionsResponse>("/manager/permissions")
}
```

- [ ] **Step 5: Run API client tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit web API client work**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web permissions api client"
```

---

## Task 3: Implement `/settings/permissions` Page

**Files:**
- Create: `web/src/pages/settings/permissions/page.test.tsx`
- Modify: `web/src/pages/settings/permissions/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/settings/permissions/page.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { PermissionsPage } from "@/pages/settings/permissions/page"

const getPermissionsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getPermissions: (...args: unknown[]) => getPermissionsMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getPermissionsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.permission", actions: ["r"] }],
  })
})

function renderPermissionsPage() {
  return render(
    <I18nProvider>
      <PermissionsPage />
    </I18nProvider>,
  )
}

test("renders auth summary, users, and permission catalog", async () => {
  getPermissionsMock.mockResolvedValueOnce({
    auth_enabled: true,
    current_user: "admin",
    users: [
      { username: "admin", permissions: [{ resource: "*", actions: ["*"] }] },
      { username: "viewer", permissions: [{ resource: "cluster.node", actions: ["r"] }] },
    ],
    resources: [
      { resource: "cluster.permission", actions: ["r"], description: "Read manager authentication and permission configuration snapshots." },
      { resource: "cluster.node", actions: ["r", "w"], description: "Read node inventory and perform node lifecycle actions." },
    ],
  })

  renderPermissionsPage()

  expect(await screen.findByText("Auth enabled")).toBeInTheDocument()
  expect(screen.getByText("Current user: admin")).toBeInTheDocument()
  expect(screen.getByText("admin")).toBeInTheDocument()
  expect(screen.getByText("viewer")).toBeInTheDocument()
  expect(screen.getByText("*:*" )).toBeInTheDocument()
  expect(screen.getByText("cluster.node:r" )).toBeInTheDocument()
  expect(screen.getByText("cluster.permission")).toBeInTheDocument()
  expect(screen.getByText("r / w")).toBeInTheDocument()
})

test("renders auth disabled with empty static users", async () => {
  getPermissionsMock.mockResolvedValueOnce({
    auth_enabled: false,
    current_user: "",
    users: [],
    resources: [{ resource: "*", actions: ["*"], description: "Wildcard access to all manager resources and actions." }],
  })

  renderPermissionsPage()

  expect(await screen.findByText("Auth disabled")).toBeInTheDocument()
  expect(screen.getByText("No static manager users are visible for this configuration.")).toBeInTheDocument()
  expect(screen.getByText("*" )).toBeInTheDocument()
})

test("maps forbidden and unavailable errors", async () => {
  getPermissionsMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderPermissionsPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getPermissionsMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderPermissionsPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run page tests and verify failure**

Run:

```bash
cd web && bun run test -- src/pages/settings/permissions/page.test.tsx
```

Expected: FAIL because the page is still a placeholder and `getPermissions` is not used.

- [ ] **Step 3: Replace placeholder with real page**

Implement `web/src/pages/settings/permissions/page.tsx` using existing components:

- `PageContainer`, `PageHeader`, `SectionCard`
- `ResourceState`
- `StatusBadge` or simple chips using existing Tailwind classes

Required helpers:

```ts
function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function grantLabel(resource: string, actions: string[]) {
  return `${resource}:${actions.join("/")}`
}

function actionLabel(actions: string[]) {
  return actions.join(" / ")
}
```

Required behavior:

- On mount, call `getPermissions()`.
- Show loading while request is pending.
- Show a summary section:
  - auth enabled/disabled
  - current user or `-`
  - static user count
  - catalog resource count
- Show static users table when `users.length > 0`.
- Show `ResourceState kind="empty"` or an inline empty card for no users with copy `permissions.users.empty`.
- Show resource catalog table for every `resources` row.
- Show read-only note with copy `permissions.readonlyNotice`.
- Map 403/503 through `ResourceState`.

Use stable accessible text matching the tests:

- `Auth enabled`
- `Auth disabled`
- `Current user: admin`
- `No static manager users are visible for this configuration.`

- [ ] **Step 4: Add i18n strings**

In `web/src/i18n/messages/en.ts`, add:

```ts
"permissions.summary.title": "Authentication Summary",
"permissions.summary.description": "Read-only manager authentication and permission state from the server.",
"permissions.auth.enabled": "Auth enabled",
"permissions.auth.disabled": "Auth disabled",
"permissions.currentUser": "Current user: {user}",
"permissions.currentUser.empty": "Current user: -",
"permissions.staticUsers": "Static users: {count}",
"permissions.catalogResources": "Catalog resources: {count}",
"permissions.users.title": "Static Manager Users",
"permissions.users.description": "Configured manager users are shown without passwords or secrets.",
"permissions.users.empty": "No static manager users are visible for this configuration.",
"permissions.table.username": "Username",
"permissions.table.permissions": "Permissions",
"permissions.catalog.title": "Permission Catalog",
"permissions.catalog.description": "Built-in manager resources and supported action codes.",
"permissions.table.resource": "Resource",
"permissions.table.actions": "Actions",
"permissions.table.description": "Description",
"permissions.readonlyNotice": "This page is read-only. Update manager users and permissions in the server static configuration.",
```

In `web/src/i18n/messages/zh-CN.ts`, add Chinese equivalents.

- [ ] **Step 5: Run page tests**

Run:

```bash
cd web && bun run test -- src/pages/settings/permissions/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Run focused web tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/settings/permissions/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit web page work**

```bash
git add web/src/pages/settings/permissions/page.tsx web/src/pages/settings/permissions/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: implement permissions readonly page"
```

---

## Task 4: Update Docs and Flow Notes

**Files:**
- Modify: `web/README.md`
- Modify: `docs/raw/web-admin-restructure.md`
- Modify if needed: `internal/FLOW.md`

- [ ] **Step 1: Update `web/README.md`**

In the Page And API Matrix, split `/settings/permissions` from the combined placeholder row if needed:

```md
| `/settings/permissions` | `GET /manager/permissions` | Implemented |
| `/monitor`, `/settings/webhooks`, `/topology` | Requires follow-up read/write API design | Placeholder |
```

- [ ] **Step 2: Update `docs/raw/web-admin-restructure.md`**

Under `权限管理 /settings/permissions`, mark read-only MVP complete:

```md
- 状态：只读 MVP 已完成（认证状态、静态管理员、权限资源矩阵）；新增/编辑/删除管理员、持久化权限配置和审计日志仍待后续设计
```

Update API list to include:

```md
GET /manager/permissions
```

In the status checklist, mark permissions management as complete for the read-only MVP and keep write management as a follow-up.

- [ ] **Step 3: Update `internal/FLOW.md` only if stale**

If the manager access capabilities list does not mention auth/permission snapshots clearly enough after the code change, add a concise note that `access/manager` owns the read-only sanitized permission snapshot. Do not over-expand the document.

- [ ] **Step 4: Commit docs**

```bash
git add web/README.md docs/raw/web-admin-restructure.md internal/FLOW.md
git commit -m "docs: update permissions management status"
```

If `internal/FLOW.md` did not require changes, omit it from `git add`.

---

## Task 5: Final Verification

**Files:**
- All touched files

- [ ] **Step 1: Run focused backend verification**

Run:

```bash
GOWORK=off go test ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused web verification**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/settings/permissions/page.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run web build**

Run:

```bash
cd web && bun run build
```

Expected: PASS. If `web/dist/index.html` changes only due to generated asset hashes, restore that generated hash-only change with:

```bash
git show HEAD:web/dist/index.html > web/dist/index.html
```

- [ ] **Step 4: Check status and recent commits**

Run:

```bash
git status --short
git log --oneline -10
```

Expected: worktree clean, with commits for design, plan, backend endpoint, web API, web page, and docs.

- [ ] **Step 5: Completion handoff**

Use `superpowers:verification-before-completion`, then `superpowers:finishing-a-development-branch` to present merge/PR/keep/discard options after verification passes.
