# Manager System Users Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement manager-scoped system UID list/add/remove APIs and the real `web/` `/system-users` page.

**Architecture:** Reuse the existing `internal/usecase/user` persisted system UID methods and the cluster-aware cache broadcast wrapper. Add a narrow `SystemUserOperator` port to `internal/usecase/management`, expose thin JWT/permission-gated manager HTTP handlers, wire the cluster-aware user wrapper in `internal/app`, then replace the web placeholder with a small list/action page. Do not expose cache-only legacy endpoints and do not call legacy `/user/systemuids*` from `web/`.

**Tech Stack:** Go, Gin, existing user usecase, React 19, Vite, Vitest, Testing Library.

---

## Pre-Execution Notes

- Execute in the isolated worktree: `.worktrees/manager-system-users` on branch `feat/manager-system-users`.
- The main worktree may have unrelated work. Do not touch, revert, stage, or commit unrelated changes.
- `ui/` is the prototype directory and is out of scope.
- Read `AGENTS.md` before implementation.
- If a touched package has `FLOW.md`, read it first and update it if the behavior description becomes stale.
- Use TDD. Each task starts with a failing test, then minimal code, targeted verification, then a small commit.
- Do not add manager wrappers for `/user/systemuids_add_to_cache` or `/user/systemuids_remove_from_cache`.
- Do not add remove-all or pagination in this slice.

## File Structure

### Backend management usecase

- Modify: `internal/usecase/management/app.go`
  - Add `SystemUserOperator` port, `Options.SystemUsers`, and `App.systemUsers` field.
- Create: `internal/usecase/management/system_users.go`
  - Manager DTOs, UID normalization, list/add/remove orchestration.
- Create: `internal/usecase/management/system_users_test.go`
  - Fake system UID operator tests.

### Manager HTTP

- Modify: `internal/access/manager/server.go`
  - Add `ListSystemUsers`, `AddSystemUsers`, `RemoveSystemUsers` to the `Management` interface.
- Modify: `internal/access/manager/routes.go`
  - Register `/manager/system-users` routes with `cluster.user` read/write permissions.
- Create: `internal/access/manager/system_users.go`
  - Request/response DTOs, handlers, error mapping.
- Create: `internal/access/manager/system_users_test.go`
  - HTTP response, permission, bad request, unavailable tests.
- Modify: `internal/access/manager/server_test.go`
  - Extend `managementStub` with system user fields and methods.

### App wiring

- Modify: `internal/app/build.go`
  - Create one cluster-aware `clusterUserUsecase` wrapper for system UID persisted mutations and cache broadcast.
  - Wire it into `managementusecase.Options.SystemUsers`.
  - Reuse the same wrapper for legacy API `Users` when API is enabled.
- Modify: `internal/app/build_test.go`
  - Assert `managementApp.systemUsers` is non-nil when manager is enabled.

### Web client and page

- Modify: `web/src/lib/manager-api.types.ts`
  - Add system user list/mutation request and response types.
- Modify: `web/src/lib/manager-api.ts`
  - Add `getSystemUsers`, `addSystemUsers`, `removeSystemUsers`.
- Modify: `web/src/lib/manager-api.test.ts`
  - Add API client tests.
- Modify: `web/src/pages/system-users/page.tsx`
  - Replace placeholder with real list/add/remove UI.
- Create: `web/src/pages/system-users/page.test.tsx`
  - Page behavior tests.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English system user strings.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese system user strings.

### Docs

- Modify: `web/README.md`
  - Mark `/system-users` implemented.
- Modify: `docs/raw/web-admin-restructure.md`
  - Mark system users MVP implemented and cache-only/remove-all as follow-ups.
- Modify if needed: `internal/FLOW.md`
  - Add manager system UID note only if current flow docs are stale after code changes.

---

## Task 1: Add Management System User Usecases

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/system_users.go`
- Create: `internal/usecase/management/system_users_test.go`

- [ ] **Step 1: Write failing usecase tests**

Create `internal/usecase/management/system_users_test.go`:

```go
package management

import (
    "context"
    "errors"
    "testing"

    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
    "github.com/stretchr/testify/require"
)

type fakeSystemUserOperator struct {
    listed []string
    listErr error
    addSink []string
    addErr error
    removeSink []string
    removeErr error
}

func (f *fakeSystemUserOperator) ListSystemUIDs(context.Context) ([]string, error) {
    return append([]string(nil), f.listed...), f.listErr
}

func (f *fakeSystemUserOperator) AddSystemUIDs(_ context.Context, uids []string) error {
    f.addSink = append([]string(nil), uids...)
    return f.addErr
}

func (f *fakeSystemUserOperator) RemoveSystemUIDs(_ context.Context, uids []string) error {
    f.removeSink = append([]string(nil), uids...)
    return f.removeErr
}

func TestSystemUsersListNormalizesAndSortsUIDs(t *testing.T) {
    op := &fakeSystemUserOperator{listed: []string{" sys-b ", "sys-a", "sys-a", ""}}
    app := New(Options{SystemUsers: op})

    got, err := app.ListSystemUsers(context.Background())

    require.NoError(t, err)
    require.Equal(t, ListSystemUsersResponse{
        Items: []SystemUser{{UID: "sys-a"}, {UID: "sys-b"}},
        Total: 2,
    }, got)
}

func TestSystemUsersAddNormalizesUIDs(t *testing.T) {
    op := &fakeSystemUserOperator{}
    app := New(Options{SystemUsers: op})

    got, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" sys-a ", "sys-b", "sys-a", ""}})

    require.NoError(t, err)
    require.Equal(t, []string{"sys-a", "sys-b"}, op.addSink)
    require.Equal(t, MutateSystemUsersResponse{UIDs: []string{"sys-a", "sys-b"}, Changed: true}, got)
}

func TestSystemUsersRemoveNormalizesUIDs(t *testing.T) {
    op := &fakeSystemUserOperator{}
    app := New(Options{SystemUsers: op})

    got, err := app.RemoveSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" sys-a ", "sys-a"}})

    require.NoError(t, err)
    require.Equal(t, []string{"sys-a"}, op.removeSink)
    require.Equal(t, MutateSystemUsersResponse{UIDs: []string{"sys-a"}, Changed: true}, got)
}

func TestSystemUsersRejectEmptyMutation(t *testing.T) {
    app := New(Options{SystemUsers: &fakeSystemUserOperator{}})

    _, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" ", ""}})

    require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestSystemUsersRequiresOperator(t *testing.T) {
    app := New(Options{})

    _, err := app.ListSystemUsers(context.Background())

    require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestSystemUsersPropagatesOperatorError(t *testing.T) {
    boom := errors.New("boom")
    app := New(Options{SystemUsers: &fakeSystemUserOperator{addErr: boom}})

    _, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{"sys-a"}})

    require.ErrorIs(t, err, boom)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestSystemUsers' -count=1
```

Expected: FAIL because system user DTOs and methods do not exist.

- [ ] **Step 3: Add the management port and app field**

In `internal/usecase/management/app.go`, add near `UserOperator`:

```go
// SystemUserOperator exposes persisted system UID mutations reused by manager actions.
type SystemUserOperator interface {
    // ListSystemUIDs returns the persisted system account UID list.
    ListSystemUIDs(ctx context.Context) ([]string, error)
    // AddSystemUIDs persists system account UIDs and refreshes caches.
    AddSystemUIDs(ctx context.Context, uids []string) error
    // RemoveSystemUIDs removes persisted system account UIDs and refreshes caches.
    RemoveSystemUIDs(ctx context.Context, uids []string) error
}
```

Add to `Options`:

```go
// SystemUsers applies persisted system UID mutations.
SystemUsers SystemUserOperator
```

Add to `App`:

```go
systemUsers SystemUserOperator
```

And set it in `New`:

```go
systemUsers: opts.SystemUsers,
```

- [ ] **Step 4: Implement minimal system user usecases**

Create `internal/usecase/management/system_users.go`:

```go
package management

import (
    "context"
    "sort"
    "strings"

    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// SystemUser is one persisted manager-facing system UID row.
type SystemUser struct {
    // UID is the system account user identifier.
    UID string
}

// ListSystemUsersResponse is the manager system UID list result.
type ListSystemUsersResponse struct {
    // Items contains sorted system UID rows.
    Items []SystemUser
    // Total is the number of returned rows.
    Total int
}

// MutateSystemUsersRequest configures add/remove system UID mutations.
type MutateSystemUsersRequest struct {
    // UIDs contains raw user identifiers; values are trimmed and deduplicated.
    UIDs []string
}

// MutateSystemUsersResponse reports accepted system UID mutations.
type MutateSystemUsersResponse struct {
    // UIDs contains normalized UID values passed to the mutation operator.
    UIDs []string
    // Changed reports whether the mutation was accepted.
    Changed bool
}

// ListSystemUsers returns all persisted system account UIDs in stable order.
func (a *App) ListSystemUsers(ctx context.Context) (ListSystemUsersResponse, error) {
    if a == nil || a.systemUsers == nil {
        return ListSystemUsersResponse{}, metadb.ErrInvalidArgument
    }
    uids, err := a.systemUsers.ListSystemUIDs(ctx)
    if err != nil {
        return ListSystemUsersResponse{}, err
    }
    normalized := normalizeSystemUIDs(uids)
    sort.Strings(normalized)
    items := make([]SystemUser, 0, len(normalized))
    for _, uid := range normalized {
        items = append(items, SystemUser{UID: uid})
    }
    return ListSystemUsersResponse{Items: items, Total: len(items)}, nil
}

// AddSystemUsers persists system account UIDs and refreshes caches.
func (a *App) AddSystemUsers(ctx context.Context, req MutateSystemUsersRequest) (MutateSystemUsersResponse, error) {
    if a == nil || a.systemUsers == nil {
        return MutateSystemUsersResponse{}, metadb.ErrInvalidArgument
    }
    uids, err := normalizeSystemUIDMutation(req.UIDs)
    if err != nil {
        return MutateSystemUsersResponse{}, err
    }
    if err := a.systemUsers.AddSystemUIDs(ctx, uids); err != nil {
        return MutateSystemUsersResponse{}, err
    }
    return MutateSystemUsersResponse{UIDs: uids, Changed: true}, nil
}

// RemoveSystemUsers removes persisted system account UIDs and refreshes caches.
func (a *App) RemoveSystemUsers(ctx context.Context, req MutateSystemUsersRequest) (MutateSystemUsersResponse, error) {
    if a == nil || a.systemUsers == nil {
        return MutateSystemUsersResponse{}, metadb.ErrInvalidArgument
    }
    uids, err := normalizeSystemUIDMutation(req.UIDs)
    if err != nil {
        return MutateSystemUsersResponse{}, err
    }
    if err := a.systemUsers.RemoveSystemUIDs(ctx, uids); err != nil {
        return MutateSystemUsersResponse{}, err
    }
    return MutateSystemUsersResponse{UIDs: uids, Changed: true}, nil
}

func normalizeSystemUIDMutation(raw []string) ([]string, error) {
    uids := normalizeSystemUIDs(raw)
    if len(uids) == 0 {
        return nil, metadb.ErrInvalidArgument
    }
    return uids, nil
}

func normalizeSystemUIDs(raw []string) []string {
    seen := make(map[string]struct{}, len(raw))
    out := make([]string, 0, len(raw))
    for _, value := range raw {
        uid := strings.TrimSpace(value)
        if uid == "" {
            continue
        }
        if _, ok := seen[uid]; ok {
            continue
        }
        seen[uid] = struct{}{}
        out = append(out, uid)
    }
    return out
}
```

- [ ] **Step 5: Run targeted usecase tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestSystemUsers' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit usecase work**

```bash
git add internal/usecase/management/app.go internal/usecase/management/system_users.go internal/usecase/management/system_users_test.go
git commit -m "feat: add manager system user usecases"
```

---

## Task 2: Expose Manager System User HTTP APIs

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/system_users.go`
- Create: `internal/access/manager/system_users_test.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Create `internal/access/manager/system_users_test.go`:

```go
package manager

import (
    "bytes"
    "net/http"
    "net/http/httptest"
    "testing"

    managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
    "github.com/stretchr/testify/require"
)

func TestManagerSystemUsersListReturnsRows(t *testing.T) {
    srv := New(Options{Management: managementStub{systemUsers: managementusecase.ListSystemUsersResponse{
        Items: []managementusecase.SystemUser{{UID: "sys-a"}, {UID: "sys-b"}},
        Total: 2,
    }}})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.JSONEq(t, `{"items":[{"uid":"sys-a"},{"uid":"sys-b"}],"total":2}`, rec.Body.String())
}

func TestManagerSystemUsersAddPostsUIDs(t *testing.T) {
    var captured managementusecase.MutateSystemUsersRequest
    srv := New(Options{Management: managementStub{
        systemUsersAddReqSink: &captured,
        systemUsersMutation: managementusecase.MutateSystemUsersResponse{UIDs: []string{"sys-a", "sys-b"}, Changed: true},
    }})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":["sys-a","sys-b"]}`))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, []string{"sys-a", "sys-b"}, captured.UIDs)
    require.JSONEq(t, `{"uids":["sys-a","sys-b"],"changed":true}`, rec.Body.String())
}

func TestManagerSystemUsersRemovePostsUIDs(t *testing.T) {
    var captured managementusecase.MutateSystemUsersRequest
    srv := New(Options{Management: managementStub{
        systemUsersRemoveReqSink: &captured,
        systemUsersMutation: managementusecase.MutateSystemUsersResponse{UIDs: []string{"sys-a"}, Changed: true},
    }})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/system-users/remove", bytes.NewBufferString(`{"uids":["sys-a"]}`))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, []string{"sys-a"}, captured.UIDs)
    require.JSONEq(t, `{"uids":["sys-a"],"changed":true}`, rec.Body.String())
}

func TestManagerSystemUsersRejectsInvalidJSON(t *testing.T) {
    srv := New(Options{Management: managementStub{}})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{`))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusBadRequest, rec.Code)
    require.JSONEq(t, `{"error":"bad_request","message":"invalid system user request"}`, rec.Body.String())
}

func TestManagerSystemUsersMapsInvalidArgument(t *testing.T) {
    srv := New(Options{Management: managementStub{systemUsersMutationErr: metadb.ErrInvalidArgument}})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":[]}`))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusBadRequest, rec.Code)
    require.JSONEq(t, `{"error":"bad_request","message":"invalid system user request"}`, rec.Body.String())
}

func TestManagerSystemUsersRequiresReadPermission(t *testing.T) {
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}}}}),
        Management: managementStub{},
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerSystemUsersRequiresWritePermission(t *testing.T) {
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.user", Actions: []string{"r"}}}}}),
        Management: managementStub{},
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":["sys-a"]}`))
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerSystemUsersReturnsUnavailableWithoutManagement(t *testing.T) {
    srv := New(Options{})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
```

- [ ] **Step 2: Extend the test stub minimally**

In `internal/access/manager/server_test.go`, add fields to `managementStub`:

```go
systemUsers              managementusecase.ListSystemUsersResponse
systemUsersErr           error
systemUsersAddReqSink    *managementusecase.MutateSystemUsersRequest
systemUsersRemoveReqSink *managementusecase.MutateSystemUsersRequest
systemUsersMutation      managementusecase.MutateSystemUsersResponse
systemUsersMutationErr   error
```

Add methods near user methods:

```go
func (s managementStub) ListSystemUsers(context.Context) (managementusecase.ListSystemUsersResponse, error) {
    return s.systemUsers, s.systemUsersErr
}

func (s managementStub) AddSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
    if s.systemUsersAddReqSink != nil {
        *s.systemUsersAddReqSink = req
    }
    return s.systemUsersMutation, s.systemUsersMutationErr
}

func (s managementStub) RemoveSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
    if s.systemUsersRemoveReqSink != nil {
        *s.systemUsersRemoveReqSink = req
    }
    return s.systemUsersMutation, s.systemUsersMutationErr
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerSystemUsers' -count=1
```

Expected: FAIL because routes/handlers/interface methods do not exist yet.

- [ ] **Step 4: Extend manager Management interface**

In `internal/access/manager/server.go`, add:

```go
// ListSystemUsers returns persisted manager-facing system UIDs.
ListSystemUsers(ctx context.Context) (managementusecase.ListSystemUsersResponse, error)
// AddSystemUsers persists system UIDs.
AddSystemUsers(ctx context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error)
// RemoveSystemUsers removes persisted system UIDs.
RemoveSystemUsers(ctx context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error)
```

- [ ] **Step 5: Register routes**

In `internal/access/manager/routes.go`, after user routes, add:

```go
systemUserReads := s.engine.Group("/manager")
if s.auth.enabled() {
    systemUserReads.Use(s.requirePermission("cluster.user", "r"))
}
systemUserReads.GET("/system-users", s.handleSystemUsers)

systemUserWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    systemUserWrites.Use(s.requirePermission("cluster.user", "w"))
}
systemUserWrites.POST("/system-users/add", s.handleSystemUsersAdd)
systemUserWrites.POST("/system-users/remove", s.handleSystemUsersRemove)
```

- [ ] **Step 6: Implement HTTP handlers**

Create `internal/access/manager/system_users.go`:

```go
package manager

import (
    "errors"
    "net/http"

    managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
    "github.com/gin-gonic/gin"
)

// SystemUsersResponse is the manager system UID list response body.
type SystemUsersResponse struct {
    // Items contains persisted system UID rows.
    Items []SystemUserDTO `json:"items"`
    // Total is the number of returned rows.
    Total int `json:"total"`
}

// SystemUserDTO is one manager-facing system UID row.
type SystemUserDTO struct {
    // UID is the system account user identifier.
    UID string `json:"uid"`
}

// MutateSystemUsersResponseDTO is the manager system UID mutation response body.
type MutateSystemUsersResponseDTO struct {
    // UIDs contains normalized UID values accepted by the mutation.
    UIDs []string `json:"uids"`
    // Changed reports whether the mutation was accepted.
    Changed bool `json:"changed"`
}

type mutateSystemUsersBody struct {
    UIDs []string `json:"uids"`
}

func (s *Server) handleSystemUsers(c *gin.Context) {
    if s.management == nil {
        jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
        return
    }
    resp, err := s.management.ListSystemUsers(c.Request.Context())
    if err != nil {
        writeSystemUserError(c, err)
        return
    }
    c.JSON(http.StatusOK, systemUsersResponseDTO(resp))
}

func (s *Server) handleSystemUsersAdd(c *gin.Context) {
    s.handleSystemUsersMutation(c, true)
}

func (s *Server) handleSystemUsersRemove(c *gin.Context) {
    s.handleSystemUsersMutation(c, false)
}

func (s *Server) handleSystemUsersMutation(c *gin.Context, add bool) {
    if s.management == nil {
        jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
        return
    }
    var body mutateSystemUsersBody
    if err := c.ShouldBindJSON(&body); err != nil {
        jsonError(c, http.StatusBadRequest, "bad_request", "invalid system user request")
        return
    }
    req := managementusecase.MutateSystemUsersRequest{UIDs: body.UIDs}
    var resp managementusecase.MutateSystemUsersResponse
    var err error
    if add {
        resp, err = s.management.AddSystemUsers(c.Request.Context(), req)
    } else {
        resp, err = s.management.RemoveSystemUsers(c.Request.Context(), req)
    }
    if err != nil {
        writeSystemUserError(c, err)
        return
    }
    c.JSON(http.StatusOK, mutateSystemUsersResponseDTO(resp))
}

func systemUsersResponseDTO(resp managementusecase.ListSystemUsersResponse) SystemUsersResponse {
    items := make([]SystemUserDTO, 0, len(resp.Items))
    for _, item := range resp.Items {
        items = append(items, SystemUserDTO{UID: item.UID})
    }
    return SystemUsersResponse{Items: items, Total: resp.Total}
}

func mutateSystemUsersResponseDTO(resp managementusecase.MutateSystemUsersResponse) MutateSystemUsersResponseDTO {
    return MutateSystemUsersResponseDTO{UIDs: resp.UIDs, Changed: resp.Changed}
}

func writeSystemUserError(c *gin.Context, err error) {
    switch {
    case errors.Is(err, metadb.ErrInvalidArgument):
        jsonError(c, http.StatusBadRequest, "bad_request", "invalid system user request")
    default:
        jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
    }
}
```

- [ ] **Step 7: Run targeted HTTP tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerSystemUsers' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run all manager HTTP tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit HTTP work**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/system_users.go internal/access/manager/system_users_test.go internal/access/manager/server_test.go
git commit -m "feat: expose manager system user APIs"
```

---

## Task 3: Wire Cluster-Aware System User Operations in App

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`

- [ ] **Step 1: Write failing app wiring assertion**

In `internal/app/build_test.go`, extend `TestBuildWiresManagerUserDependencies`:

```go
requireManagementAppFieldNonNil(t, app, "systemUsers")
```

- [ ] **Step 2: Run app test and verify failure**

Run:

```bash
GOWORK=off go test ./internal/app -run TestBuildWiresManagerUserDependencies -count=1
```

Expected: FAIL because `systemUsers` is nil or missing.

- [ ] **Step 3: Wire cluster-aware user wrapper once**

In `internal/app/build.go`, after `userApp` is created and before the manager/API blocks, add:

```go
clusterUsers := &clusterUserUsecase{
    local:       userApp,
    remote:      app.nodeClient,
    localNodeID: cfg.Node.ID,
    peerNodeIDs: controllerPeerIDs(cfg.Cluster.DerivedControllerNodes(), cfg.Cluster.runtimeSeeds()),
}
```

In `managementusecase.New(...)`, add:

```go
SystemUsers: clusterUsers,
```

In the API block, replace the local `userAPI := &clusterUserUsecase{...}` construction and use:

```go
Users: clusterUsers,
```

This ensures manager system UID add/remove uses persisted mutation plus peer cache broadcast even when legacy API is disabled.

- [ ] **Step 4: Run targeted app test**

Run:

```bash
GOWORK=off go test ./internal/app -run TestBuildWiresManagerUserDependencies -count=1
```

Expected: PASS.

- [ ] **Step 5: Run focused backend tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/user ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit app wiring**

```bash
git add internal/app/build.go internal/app/build_test.go
git commit -m "feat: wire manager system user operations"
```

---

## Task 4: Add Web API Client Bindings

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

In `web/src/lib/manager-api.test.ts`, add imports:

```ts
getSystemUsers,
addSystemUsers,
removeSystemUsers,
```

Add tests:

```ts
it("fetches manager system users", async () => {
  const payload = { items: [{ uid: "sys-a" }], total: 1 }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

  await expect(getSystemUsers()).resolves.toEqual(payload)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/system-users",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})

it("adds manager system users", async () => {
  const payload = { uids: ["sys-a", "sys-b"], changed: true }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

  await expect(addSystemUsers({ uids: ["sys-a", "sys-b"] })).resolves.toEqual(payload)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/system-users/add",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ uids: ["sys-a", "sys-b"] }),
      headers: expect.any(Headers),
    }),
  )
})

it("removes manager system users", async () => {
  const payload = { uids: ["sys-a"], changed: true }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

  await expect(removeSystemUsers({ uids: ["sys-a"] })).resolves.toEqual(payload)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/system-users/remove",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ uids: ["sys-a"] }),
      headers: expect.any(Headers),
    }),
  )
})
```

- [ ] **Step 2: Run API client tests and verify failure**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: FAIL because functions/types do not exist.

- [ ] **Step 3: Add web types**

In `web/src/lib/manager-api.types.ts`, add near user types:

```ts
export type ManagerSystemUser = {
  uid: string
}

export type ManagerSystemUsersResponse = {
  items: ManagerSystemUser[]
  total: number
}

export type MutateSystemUsersInput = {
  uids: string[]
}

export type MutateSystemUsersResponse = {
  uids: string[]
  changed: boolean
}
```

- [ ] **Step 4: Add web API functions**

In `web/src/lib/manager-api.ts`, import the new types and add:

```ts
export function getSystemUsers() {
  return jsonManagerFetch<ManagerSystemUsersResponse>("/manager/system-users")
}

export function addSystemUsers(input: MutateSystemUsersInput) {
  return jsonManagerFetch<MutateSystemUsersResponse>("/manager/system-users/add", {
    method: "POST",
    body: JSON.stringify({ uids: input.uids }),
  })
}

export function removeSystemUsers(input: MutateSystemUsersInput) {
  return jsonManagerFetch<MutateSystemUsersResponse>("/manager/system-users/remove", {
    method: "POST",
    body: JSON.stringify({ uids: input.uids }),
  })
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
git commit -m "feat: add web system user api client"
```

---

## Task 5: Implement `/system-users` Web Page

**Files:**
- Modify: `web/src/pages/system-users/page.tsx`
- Create: `web/src/pages/system-users/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/system-users/page.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { SystemUsersPage } from "@/pages/system-users/page"

const getSystemUsersMock = vi.fn()
const addSystemUsersMock = vi.fn()
const removeSystemUsersMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getSystemUsers: (...args: unknown[]) => getSystemUsersMock(...args),
    addSystemUsers: (...args: unknown[]) => addSystemUsersMock(...args),
    removeSystemUsers: (...args: unknown[]) => removeSystemUsersMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getSystemUsersMock.mockReset()
  addSystemUsersMock.mockReset()
  removeSystemUsersMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.user", actions: ["r", "w"] }],
  })
})

function renderSystemUsersPage() {
  return render(
    <I18nProvider>
      <SystemUsersPage />
    </I18nProvider>,
  )
}

test("renders persisted system users", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [{ uid: "sys-a" }], total: 1 })

  renderSystemUsersPage()

  expect(await screen.findByText("sys-a")).toBeInTheDocument()
  expect(screen.getByText("1 persisted UID")).toBeInTheDocument()
})

test("adds normalized system users and refreshes", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [], total: 0 })
  getSystemUsersMock.mockResolvedValueOnce({ items: [{ uid: "sys-a" }, { uid: "sys-b" }], total: 2 })
  addSystemUsersMock.mockResolvedValueOnce({ uids: ["sys-a", "sys-b"], changed: true })

  const user = userEvent.setup()
  renderSystemUsersPage()

  await screen.findByText("No manager data is available for this view yet.")
  await user.click(screen.getByRole("button", { name: "Add system UIDs" }))
  await user.type(screen.getByLabelText("UIDs"), "sys-a, sys-b\nsys-a")
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add system UIDs" }))

  expect(addSystemUsersMock).toHaveBeenCalledWith({ uids: ["sys-a", "sys-b"] })
  expect(await screen.findByText("sys-b")).toBeInTheDocument()
})

test("removes one system user after confirmation", async () => {
  getSystemUsersMock.mockResolvedValue({ items: [{ uid: "sys-a" }], total: 1 })
  removeSystemUsersMock.mockResolvedValueOnce({ uids: ["sys-a"], changed: true })

  const user = userEvent.setup()
  renderSystemUsersPage()

  await screen.findByText("sys-a")
  await user.click(screen.getByRole("button", { name: "Remove system UID sys-a" }))
  await user.click(screen.getByRole("button", { name: "Confirm remove" }))

  expect(removeSystemUsersMock).toHaveBeenCalledWith({ uids: ["sys-a"] })
})

test("validates empty add input", async () => {
  getSystemUsersMock.mockResolvedValueOnce({ items: [], total: 0 })

  const user = userEvent.setup()
  renderSystemUsersPage()

  await screen.findByText("No manager data is available for this view yet.")
  await user.click(screen.getByRole("button", { name: "Add system UIDs" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add system UIDs" }))

  expect(await screen.findByText("Enter at least one UID.")).toBeInTheDocument()
  expect(addSystemUsersMock).not.toHaveBeenCalled()
})

test("maps permission and availability errors", async () => {
  getSystemUsersMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderSystemUsersPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getSystemUsersMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderSystemUsersPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run page tests and verify failure**

Run:

```bash
cd web && bun run test -- src/pages/system-users/page.test.tsx
```

Expected: FAIL because page is still placeholder and API functions are not used.

- [ ] **Step 3: Replace placeholder with real page**

Implement `web/src/pages/system-users/page.tsx` using existing components:

- `PageContainer`, `PageHeader`, `SectionCard`
- `ResourceState`, `ActionFormDialog`, `ConfirmDialog`
- `Button`

Required behavior:

- On mount, call `getSystemUsers()`.
- Render a compact table with UID and actions.
- Show total as `{count} persisted UID(s)`.
- Add dialog textarea normalizes comma/space/newline separated UIDs.
- Remove one UID with confirm dialog.
- Refresh list after successful add/remove.
- Map 403 and 503 through `ResourceState` using the same `mapErrorKind` pattern as `UsersPage`.

Use helper:

```ts
function normalizeUIDs(value: string) {
  const seen = new Set<string>()
  const uids: string[] = []
  for (const raw of value.split(/[,\s;]+/)) {
    const uid = raw.trim()
    if (uid && !seen.has(uid)) {
      seen.add(uid)
      uids.push(uid)
    }
  }
  return uids
}
```

- [ ] **Step 4: Add i18n strings**

In `web/src/i18n/messages/en.ts`, add:

```ts
"systemUsers.list.title": "Persisted system UIDs",
"systemUsers.list.description": "System UIDs are privileged accounts used by bots, notification senders, and internal flows.",
"systemUsers.totalValue": "{count, plural, one {# persisted UID} other {# persisted UIDs}}",
"systemUsers.table.uid": "UID",
"systemUsers.table.actions": "Actions",
"systemUsers.action.add": "Add system UIDs",
"systemUsers.action.remove": "Remove",
"systemUsers.action.removeOne": "Remove system UID {uid}",
"systemUsers.action.confirmRemove": "Confirm remove",
"systemUsers.add.description": "Persist system UIDs and refresh node-local caches through the cluster-aware path.",
"systemUsers.form.uids": "UIDs",
"systemUsers.form.uidsPlaceholder": "sys.notify, sys.robot",
"systemUsers.form.emptyUIDs": "Enter at least one UID.",
"systemUsers.remove.description": "Remove {uid} from the persisted system UID list.",
"systemUsers.cacheOnlyExcluded": "Cache-only legacy operations are intentionally not exposed here.",
```

In `web/src/i18n/messages/zh-CN.ts`, add Chinese equivalents.

- [ ] **Step 5: Run page tests**

Run:

```bash
cd web && bun run test -- src/pages/system-users/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Run focused web tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/system-users/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit web page work**

```bash
git add web/src/pages/system-users/page.tsx web/src/pages/system-users/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: implement system users admin page"
```

---

## Task 6: Update Docs and Flow Notes

**Files:**
- Modify: `web/README.md`
- Modify: `docs/raw/web-admin-restructure.md`
- Modify if needed: `internal/FLOW.md`

- [ ] **Step 1: Update `web/README.md`**

Change `/system-users` matrix status from placeholder to implemented:

```md
| `/system-users` | `GET /manager/system-users`, `POST /manager/system-users/add`, `POST /manager/system-users/remove` | Implemented |
```

Keep unrelated placeholder rows unchanged.

- [ ] **Step 2: Update `docs/raw/web-admin-restructure.md`**

Under `系统用户 /system-users`, mark MVP implemented:

```md
- 状态：MVP 已完成（持久化系统 UID 列表、添加、移除）；cache-only 操作、remove-all、用途元数据和审计日志仍待后续设计
```

Update API list to use manager routes:

```md
GET  /manager/system-users
POST /manager/system-users/add
POST /manager/system-users/remove
```

- [ ] **Step 3: Update `internal/FLOW.md` only if stale**

If manager API capability notes become stale, add a concise note that manager system user APIs call the persisted user usecase path and do not expose cache-only legacy endpoints.

- [ ] **Step 4: Commit docs**

```bash
git add web/README.md docs/raw/web-admin-restructure.md internal/FLOW.md
git commit -m "docs: update system users management status"
```

If `internal/FLOW.md` did not require changes, omit it from `git add`.

---

## Task 7: Final Verification

**Files:**
- All touched files

- [ ] **Step 1: Run focused backend verification**

Run:

```bash
GOWORK=off go test ./internal/usecase/user ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused web verification**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/system-users/page.test.tsx
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

Expected: worktree clean, with commits for spec, plan, usecase, HTTP, app wiring, web API, web page, and docs.

- [ ] **Step 5: Completion handoff**

Use `superpowers:verification-before-completion`, then `superpowers:finishing-a-development-branch` to present merge/PR/keep/discard options after verification passes.
