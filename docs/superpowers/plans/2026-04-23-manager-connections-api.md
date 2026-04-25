# Manager Connections API Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add manager-side Connections read APIs and connect the `web/` Connections page to real backend data without changing existing manager API contracts for other pages.

**Architecture:** Build a manager-facing local-connections view on top of `internal/runtime/online.Registry`, map it through `internal/usecase/management`, and expose it through new read-only routes in `internal/access/manager`. Reuse the existing frontend manager API client, `ResourceState`, `DetailSheet`, and table patterns so the Connections page behaves like the already-integrated `Nodes`, `Slots`, and `Channels` pages.

**Tech Stack:** Go, Gin, React 19, React Router 7, TypeScript, Vite, Vitest, Testing Library, existing manager page components, existing online runtime/session abstractions.

---

## References

- Spec: `docs/superpowers/specs/2026-04-23-manager-connections-api-design.md`
- Existing manager API client: `web/src/lib/manager-api.ts`
- Existing manager page patterns:
  - `web/src/pages/nodes/page.tsx`
  - `web/src/pages/channels/page.tsx`
  - `web/src/pages/slots/page.tsx`
- Existing management usecases:
  - `internal/usecase/management/nodes.go`
  - `internal/usecase/management/channel_runtime_meta.go`
- Existing runtime data source:
  - `internal/runtime/online/types.go`
  - `internal/runtime/online/registry.go`
- Existing manager handler coverage:
  - `internal/access/manager/server_test.go`

## File Structure

- Create: `internal/usecase/management/connections.go` — manager-facing connection DTOs and list/detail queries.
- Create: `internal/usecase/management/connections_test.go` — TDD coverage for local online registry mapping and ordering.
- Modify: `internal/usecase/management/app.go` — add `Online online.Registry` option and store it in the app.
- Create: `internal/access/manager/connections.go` — HTTP handlers and DTOs for list/detail endpoints.
- Modify: `internal/access/manager/routes.go` — register `/manager/connections` routes behind `cluster.connection:r`.
- Modify: `internal/access/manager/server.go` — extend `Management` interface with list/detail connection reads.
- Modify: `internal/access/manager/server_test.go` — extend `managementStub` and add handler tests.
- Modify: `internal/app/build.go` — inject the existing `onlineRegistry` into `managementusecase.New(...)`.
- Modify: `web/src/lib/manager-api.types.ts` — add connection DTO response types.
- Modify: `web/src/lib/manager-api.ts` — add `getConnections()` and `getConnection(sessionId)`.
- Modify: `web/src/lib/manager-api.test.ts` — add client wrapper request/response tests.
- Create: `web/src/pages/connections/page.test.tsx` — page behavior tests.
- Modify: `web/src/pages/connections/page.tsx` — replace “not exposed” copy with a real list/detail page.
- Modify: `web/src/pages/page-shells.test.tsx` — move Connections from unsupported-page expectations to real-shell expectations.

---

### Task 1: Add management connection queries on top of the online registry

**Files:**
- Create: `internal/usecase/management/connections.go`
- Create: `internal/usecase/management/connections_test.go`
- Modify: `internal/usecase/management/app.go`

- [ ] **Step 1: Write the failing usecase tests**

Create `internal/usecase/management/connections_test.go` with focused tests for:

```go
func TestListConnectionsReturnsMappedItemsOrderedByConnectedAtDesc(t *testing.T) {}
func TestGetConnectionReturnsMappedDetail(t *testing.T) {}
func TestGetConnectionReturnsNotFoundWhenMissing(t *testing.T) {}
```

Test fixtures should use `online.NewRegistry()` plus `session.New(...)`, and assert the manager-facing DTO contains:

- `SessionID`
- `UID`
- `DeviceID`
- `DeviceFlag`
- `DeviceLevel`
- `SlotID`
- `State`
- `Listener`
- `ConnectedAt`
- `RemoteAddr`
- `LocalAddr`

Expected ordering:

- newer `ConnectedAt` first
- same timestamp falls back to lower `SessionID` first

- [ ] **Step 2: Run the usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestListConnections|TestGetConnection'
```

Expected:

- FAIL because `ListConnections` / `GetConnection` and the new DTOs do not exist yet

- [ ] **Step 3: Add the minimal management app wiring for online registry**

In `internal/usecase/management/app.go`:

- import `github.com/WuKongIM/WuKongIM/internal/runtime/online`
- add `Online online.Registry` to `Options`
- add `online online.Registry` to `App`
- store `opts.Online` in `New(...)`

Keep this change minimal; do not refactor unrelated usecase fields.

- [ ] **Step 4: Implement the minimal connection DTOs and queries**

In `internal/usecase/management/connections.go` add:

```go
type Connection struct {
    SessionID   uint64
    UID         string
    DeviceID    string
    DeviceFlag  string
    DeviceLevel string
    SlotID      uint64
    State       string
    Listener    string
    ConnectedAt time.Time
    RemoteAddr  string
    LocalAddr   string
}

type ConnectionDetail = Connection
```

Add:

```go
func (a *App) ListConnections(ctx context.Context) ([]Connection, error)
func (a *App) GetConnection(ctx context.Context, sessionID uint64) (ConnectionDetail, error)
```

Implementation rules:

- If `a == nil` or `a.online == nil`, return zero values and no panic
- Build list from `online.ActiveSlots()` + `online.ActiveConnectionsBySlot(...)` so you can enumerate the registry without adding new runtime APIs
- Map session addresses with `conn.Session.RemoteAddr()` / `conn.Session.LocalAddr()`
- Convert runtime enums to stable manager-facing strings:
  - state: `active`, `closing`, `unknown`
  - device level: `master`, `slave`, `unknown`
  - device flag: lower-case stable labels if available, otherwise `unknown`
- `GetConnection` should search the active connections and return `controllermeta.ErrNotFound` when not present

- [ ] **Step 5: Re-run the usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestListConnections|TestGetConnection'
```

Expected:

- PASS

- [ ] **Step 6: Commit the usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/connections.go internal/usecase/management/connections_test.go
git commit -m "feat: add manager connection queries"
```

---

### Task 2: Expose Connections through manager HTTP routes

**Files:**
- Create: `internal/access/manager/connections.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing handler tests**

Add tests to `internal/access/manager/server_test.go` for:

```go
func TestManagerConnectionsReturnsList(t *testing.T) {}
func TestManagerConnectionDetailReturnsItem(t *testing.T) {}
func TestManagerConnectionDetailRejectsInvalidSessionID(t *testing.T) {}
func TestManagerConnectionDetailReturnsNotFound(t *testing.T) {}
func TestManagerConnectionsRejectsInsufficientPermission(t *testing.T) {}
func TestManagerConnectionsReturnsServiceUnavailableWhenManagementNotConfigured(t *testing.T) {}
```

List response shape should mirror existing manager list APIs:

```json
{
  "total": 1,
  "items": [{
    "session_id": 101,
    "uid": "u1",
    "device_id": "device-a",
    "device_flag": "app",
    "device_level": "master",
    "slot_id": 9,
    "state": "active",
    "listener": "tcp",
    "connected_at": "2026-04-23T08:00:00Z",
    "remote_addr": "10.0.0.1:5000",
    "local_addr": "127.0.0.1:7000"
  }]
}
```

- [ ] **Step 2: Run the handler tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerConnections|TestManagerConnectionDetail'
```

Expected:

- FAIL because the routes, interface methods, and handlers do not exist yet

- [ ] **Step 3: Extend the manager interface and test stub**

In `internal/access/manager/server.go`, add:

```go
ListConnections(ctx context.Context) ([]managementusecase.Connection, error)
GetConnection(ctx context.Context, sessionID uint64) (managementusecase.ConnectionDetail, error)
```

In `internal/access/manager/server_test.go`, extend `managementStub` with:

- `connections []managementusecase.Connection`
- `connectionsErr error`
- `connectionDetail managementusecase.ConnectionDetail`
- `connectionDetailErr error`

and implement:

```go
func (s managementStub) ListConnections(context.Context) ([]managementusecase.Connection, error)
func (s managementStub) GetConnection(context.Context, uint64) (managementusecase.ConnectionDetail, error)
```

- [ ] **Step 4: Register the new routes**

In `internal/access/manager/routes.go`, add a new read group:

```go
connections := s.engine.Group("/manager")
if s.auth.enabled() {
    connections.Use(s.requirePermission("cluster.connection", "r"))
}
connections.GET("/connections", s.handleConnections)
connections.GET("/connections/:session_id", s.handleConnection)
```

Do not reuse `cluster.channel` or `cluster.node`; keep a dedicated `cluster.connection` resource.

- [ ] **Step 5: Implement the minimal handlers and DTO mappers**

In `internal/access/manager/connections.go`:

- define response DTOs using snake_case JSON names
- implement:

```go
func (s *Server) handleConnections(c *gin.Context)
func (s *Server) handleConnection(c *gin.Context)
```

Behavior:

- if `s.management == nil`, return `503` with `management not configured`
- parse `session_id` as `uint64`; invalid -> `400 bad_request` / `invalid session_id`
- `controllermeta.ErrNotFound` -> `404 not_found` / `connection not found`
- success -> JSON DTO

- [ ] **Step 6: Re-run the handler tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerConnections|TestManagerConnectionDetail'
```

Expected:

- PASS

- [ ] **Step 7: Commit the handler slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/connections.go internal/access/manager/server_test.go
git commit -m "feat: add manager connections routes"
```

---

### Task 3: Wire the management app to the shared online registry

**Files:**
- Modify: `internal/app/build.go`

- [ ] **Step 1: Write the smallest wiring guard first**

Add or extend a narrow build-level assertion in an existing `internal/app` test file if one already covers `managementusecase.Options`; if no fitting test exists, skip new test creation and rely on compile + downstream manager/usecase tests for this tiny wiring change.

- [ ] **Step 2: Implement the injection**

In `internal/app/build.go`, when constructing `managementusecase.New(...)`, pass:

```go
Online: onlineRegistry,
```

Do not allocate a second registry for management.

- [ ] **Step 3: Run targeted backend verification**

Run:

```bash
go test ./internal/access/manager ./internal/usecase/management
```

Expected:

- PASS

- [ ] **Step 4: Commit the wiring change**

```bash
git add internal/app/build.go
git commit -m "feat: wire manager connections to online registry"
```

---

### Task 4: Extend the frontend manager API client for Connections

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write the failing client tests**

Add tests for:

```ts
it("fetches connections list and detail data", async () => {})
```

Assert:

- `getConnections()` calls `GET /manager/connections`
- `getConnection(101)` calls `GET /manager/connections/101`
- raw backend response fields stay snake_case in fixtures

Expected response types:

```ts
{
  total: 1,
  items: [{
    session_id: 101,
    uid: "u1",
    device_id: "device-a",
    device_flag: "app",
    device_level: "master",
    slot_id: 9,
    state: "active",
    listener: "tcp",
    connected_at: "2026-04-23T08:00:00Z",
    remote_addr: "10.0.0.1:5000",
    local_addr: "127.0.0.1:7000",
  }]
}
```

- [ ] **Step 2: Run the client tests to verify they fail**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected:

- FAIL because the new wrappers/types are missing

- [ ] **Step 3: Add the minimal types and wrappers**

In `web/src/lib/manager-api.types.ts` add:

- `ManagerConnection`
- `ManagerConnectionsResponse`
- `ManagerConnectionDetailResponse`

In `web/src/lib/manager-api.ts` add:

```ts
export function getConnections() {
  return jsonManagerFetch<ManagerConnectionsResponse>("/manager/connections")
}

export function getConnection(sessionId: number) {
  return jsonManagerFetch<ManagerConnectionDetailResponse>(`/manager/connections/${sessionId}`)
}
```

- [ ] **Step 4: Re-run the client tests to verify they pass**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected:

- PASS

- [ ] **Step 5: Commit the client slice**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add manager connections client"
```

---

### Task 5: Replace the Connections placeholder page with a real list/detail view

**Files:**
- Create: `web/src/pages/connections/page.test.tsx`
- Modify: `web/src/pages/connections/page.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Write the failing page tests**

Create `web/src/pages/connections/page.test.tsx` covering:

```ts
test("renders connection list and opens detail", async () => {})
test("refreshes the connection list", async () => {})
test("shows unavailable state when the list request fails", async () => {})
test("shows empty state when there are no active connections", async () => {})
```

Minimum expectations:

- list row shows `u1`
- button `Inspect connection 101`
- detail sheet shows `Remote address` and `Local address`
- refresh calls `getConnections()` again
- `503` maps to unavailable `ResourceState`

Update `web/src/pages/page-shells.test.tsx` so `/connections` expects the real section title, not “Manager API Coverage”.

- [ ] **Step 2: Run the page tests to verify they fail**

Run:

```bash
cd web && bun run test -- src/pages/connections/page.test.tsx src/pages/page-shells.test.tsx
```

Expected:

- FAIL because the page is still a placeholder

- [ ] **Step 3: Implement the minimal real page**

Rewrite `web/src/pages/connections/page.tsx` to follow the existing manager page pattern:

- page-local async state
- refresh button
- summary cards derived from the list:
  - total connections
  - active listeners
  - distinct UIDs
  - distinct slots
- table columns:
  - Session
  - UID
  - Device ID
  - Device
  - Listener
  - State
  - Connected
  - Actions
- detail sheet driven by `getConnection(sessionId)`
- reuse:
  - `ResourceState`
  - `StatusBadge`
  - `DetailSheet`
  - `KeyValueList`
  - `TableToolbar`

Formatting helpers:

- `connected_at` with `toLocaleString()`
- device display as `${device_flag} / ${device_level}`

- [ ] **Step 4: Re-run the page tests to verify they pass**

Run:

```bash
cd web && bun run test -- src/pages/connections/page.test.tsx src/pages/page-shells.test.tsx
```

Expected:

- PASS

- [ ] **Step 5: Commit the UI slice**

```bash
git add web/src/pages/connections/page.tsx web/src/pages/connections/page.test.tsx web/src/pages/page-shells.test.tsx
git commit -m "feat: connect manager connections page"
```

---

### Task 6: Final verification

**Files:**
- No new files; verify the complete integrated result

- [ ] **Step 1: Run the backend verification**

```bash
go test ./internal/access/manager ./internal/usecase/management
```

Expected:

- PASS

- [ ] **Step 2: Run the focused frontend verification**

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/connections/page.test.tsx src/pages/page-shells.test.tsx
```

Expected:

- PASS

- [ ] **Step 3: Run the full web test suite**

```bash
cd web && bun run test
```

Expected:

- PASS

- [ ] **Step 4: Run the production build**

```bash
cd web && bun run build
```

Expected:

- PASS

- [ ] **Step 5: Review git status before reporting completion**

```bash
git status --short
```

Expected:

- only intended changes remain

