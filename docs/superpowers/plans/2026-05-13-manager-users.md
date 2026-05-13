# Manager Users Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement manager-scoped user list/detail APIs, kick and token-reset operations, and the real `web/` `/users` page.

**Architecture:** Keep manager HTTP thin. Add authoritative user page reads in `pkg/slot/meta` and `pkg/slot/proxy`, aggregate manager DTOs in `internal/usecase/management`, wire through `internal/app`, and render the page in the existing React `web/` app. Reuse `internal/usecase/user` for token reset and device quit; do not add a new broad service layer.

**Tech Stack:** Go, Gin, slot meta/proxy, existing presence/online runtime, React 19, Vite, Vitest, Testing Library.

---

## Pre-Execution Notes

- Read `AGENTS.md` before implementation.
- Use a fresh worktree for execution, for example: `git worktree add .worktrees/manager-users -b feat/manager-users main`.
- The current main worktree has user-owned uncommitted `ui/` deletions and an untracked plan file. Do not touch, revert, stage, or commit those unrelated changes.
- `ui/` is the prototype directory and must remain untouched by this feature.
- If a touched package contains `FLOW.md`, read it first and update it if behavior changes. At plan-writing time no `FLOW.md` was found under the planned touched paths.
- Use TDD. Each implementation task below starts with tests, then minimal code, then targeted verification, then a small commit.

## File Structure

### Go storage and proxy

- Modify: `pkg/slot/meta/user.go`
  - Add `UserCursor`, primary prefix helper, user record decoding helper, and `ListUsersPage`.
- Modify: `pkg/slot/meta/user_test.go`
  - Add user page scan tests.
- Modify: `pkg/slot/proxy/store.go`
  - Add public `ScanUsersSlotPage` entry point if the implementation stays small; otherwise keep only the method signature here and delegate to a new file.
- Modify: `pkg/slot/proxy/identity_rpc.go`
  - Add identity RPC operation `scan_users_page` and local/remote authoritative handlers.
- Modify: `pkg/slot/proxy/identity_subscriber_codec.go`
  - Extend binary request/response encoding for scan cursor, limit, user page, cursor, and done flag.
- Modify: `pkg/slot/proxy/integration_test.go`
  - Add proxy integration tests for local and remote authoritative user page scans.

### Go management and manager HTTP

- Modify: `internal/usecase/management/app.go`
  - Add narrow user reader/operator/presence/action ports to `Options` and `App`.
- Create: `internal/usecase/management/users.go`
  - Add DTOs, list/detail aggregation, kick, token reset, device label mapping, token generation, and cursor validation helpers.
- Create: `internal/usecase/management/users_test.go`
  - Add usecase tests with fake user reader/operator/presence/action ports.
- Modify: `internal/access/manager/server.go`
  - Add new methods to `Management` interface.
- Modify: `internal/access/manager/routes.go`
  - Register `/manager/users...` routes with `cluster.user` read/write permissions.
- Modify: `internal/access/manager/cursor_codec.go`
  - Add user list cursor encode/decode with its own magic value.
- Create: `internal/access/manager/users.go`
  - Add HTTP handlers, DTOs, request parsing, and error mapping.
- Modify: `internal/access/manager/server_test.go`
  - Add route/permission tests or keep generic route tests if already centralized.
- Create: `internal/access/manager/users_test.go`
  - Add HTTP behavior tests for list/detail/kick/reset.
- Modify: `internal/app/build.go`
  - Wire user reader/operator/presence/action dependencies into `managementusecase.New`.
- Create or modify: `internal/app/management_users.go`
  - Add small adapters if direct wiring to `app.store`, `app.userApp`, and presence action dispatch needs type conversion.
- Modify: `internal/app/build_test.go`
  - Assert management user dependencies are wired when manager is enabled.
- Modify: `internal/app/user_management_integration_test.go`
  - Add a targeted integration test for manager user APIs if it stays reasonably fast; mark integration if it starts real multi-node apps.

### Web

- Modify: `web/src/lib/manager-api.types.ts`
  - Add user list/detail/action types and params.
- Modify: `web/src/lib/manager-api.ts`
  - Add `getUsers`, `getUser`, `kickUser`, and `resetUserToken`.
- Modify: `web/src/lib/manager-api.test.ts`
  - Add API client tests.
- Modify: `web/src/pages/users/page.tsx`
  - Replace placeholder with real list/detail/action UI.
- Create: `web/src/pages/users/page.test.tsx`
  - Add page tests for load, search, more, detail, kick, token reset, and error states.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese user page strings.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English user page strings.
- Modify: `web/README.md`
  - Change `/users` matrix status from placeholder to implemented.

---

### Task 1: Add single-hash-slot user page scans

**Files:**
- Modify: `pkg/slot/meta/user.go`
- Modify: `pkg/slot/meta/user_test.go`

- [ ] **Step 1: Write failing tests for user page scanning**

Add tests to `pkg/slot/meta/user_test.go`:

```go
func TestShardListUsersPageReturnsStableCursor(t *testing.T) {
    db := newTestDB(t)
    ctx := context.Background()
    shard := db.ForSlot(7)

    require.NoError(t, shard.CreateUser(ctx, User{UID: "u1", Token: "t1"}))
    require.NoError(t, shard.CreateUser(ctx, User{UID: "u2", Token: "t2"}))
    require.NoError(t, shard.CreateUser(ctx, User{UID: "u3", Token: "t3"}))

    page1, cursor, done, err := shard.ListUsersPage(ctx, UserCursor{}, 2)
    require.NoError(t, err)
    require.False(t, done)
    require.Equal(t, []string{"u1", "u2"}, userUIDs(page1))
    require.Equal(t, UserCursor{UID: "u2"}, cursor)

    page2, cursor, done, err := shard.ListUsersPage(ctx, cursor, 2)
    require.NoError(t, err)
    require.True(t, done)
    require.Equal(t, []string{"u3"}, userUIDs(page2))
    require.Equal(t, UserCursor{UID: "u3"}, cursor)
}

func TestShardListUsersPageRejectsInvalidLimit(t *testing.T) {
    db := newTestDB(t)
    _, _, _, err := db.ForSlot(7).ListUsersPage(context.Background(), UserCursor{}, 0)
    require.ErrorIs(t, err, ErrInvalidArgument)
}
```

Also add a small `userUIDs` helper if one does not already exist.

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./pkg/slot/meta -run 'TestShardListUsersPage' -count=1`

Expected: FAIL because `ListUsersPage` and `UserCursor` do not exist.

- [ ] **Step 3: Implement minimal meta page scan**

In `pkg/slot/meta/user.go`, add:

```go
// UserCursor identifies the last emitted user in a shard page scan.
type UserCursor struct {
    // UID is the last emitted user ID in primary-key order.
    UID string
}

func (s *ShardStore) ListUsersPage(ctx context.Context, after UserCursor, limit int) ([]User, UserCursor, bool, error) {
    if err := s.validate(); err != nil { return nil, UserCursor{}, false, err }
    if err := s.db.checkContext(ctx); err != nil { return nil, UserCursor{}, false, err }
    if limit <= 0 { return nil, UserCursor{}, false, ErrInvalidArgument }
    if after.UID != "" && len(after.UID) > maxKeyStringLen { return nil, UserCursor{}, false, ErrInvalidArgument }

    s.db.mu.RLock()
    defer s.db.mu.RUnlock()

    prefix := encodeUserPrimaryPrefix(s.slot)
    lowerBound := prefix
    if after.UID != "" {
        lowerBound = nextPrefix(encodeUserPrimaryKey(s.slot, after.UID, userPrimaryFamilyID))
    }
    // Iterate LowerBound..UpperBound, decode primary family records, collect limit+1.
}

func encodeUserPrimaryPrefix(hashSlot uint16) []byte {
    return encodeStatePrefix(hashSlot, UserTable.ID)
}
```

Implement a private `decodeUserRecord(key, value, prefix []byte) (User, uint16, error)` helper using existing key/value decoding patterns. Keep comments in English for exported types/methods.

- [ ] **Step 4: Run targeted tests**

Run: `GOWORK=off go test ./pkg/slot/meta -run 'TestShardListUsersPage|Test.*User' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit storage page scan**

```bash
git add pkg/slot/meta/user.go pkg/slot/meta/user_test.go
git commit -m "feat: add user page scans"
```

---

### Task 2: Add authoritative user page scans in slot proxy

**Files:**
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/identity_rpc.go`
- Modify: `pkg/slot/proxy/identity_subscriber_codec.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing proxy integration tests**

Add tests to `pkg/slot/proxy/integration_test.go` near existing identity tests:

```go
func TestStoreScanUsersSlotPageReadsAuthoritativeLocalSlot(t *testing.T) {
    nodes := startTwoNodeHashSlotStores(t, 8)
    ctx := context.Background()
    slotID := multiraft.SlotID(1)
    uidA := findUIDForSlot(t, nodes[0].cluster, uint64(slotID), "u-local-a")
    hashSlotA := mustHashSlotForKey(t, nodes[0].cluster, uidA)
    uidB := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, uint64(slotID), hashSlotA, "u-local-b")
    hashSlotB := mustHashSlotForKey(t, nodes[0].cluster, uidB)
    require.NoError(t, nodes[0].db.ForHashSlot(hashSlotA).CreateUser(ctx, metadb.User{UID: uidA}))
    require.NoError(t, nodes[0].db.ForHashSlot(hashSlotB).CreateUser(ctx, metadb.User{UID: uidB}))

    page, cursor, done, err := nodes[0].store.ScanUsersSlotPage(ctx, slotID, metadb.UserCursor{}, 1)
    require.NoError(t, err)
    require.Len(t, page, 1)
    require.False(t, done)

    page2, _, _, err := nodes[0].store.ScanUsersSlotPage(ctx, slotID, cursor, 10)
    require.NoError(t, err)
    require.NotEmpty(t, page2)
}

func TestStoreScanUsersSlotPageReadsAuthoritativeRemoteSlot(t *testing.T) {
    nodes := startTwoNodeHashSlotStores(t, 8)
    ctx := context.Background()
    slotID := multiraft.SlotID(2)
    uid := findUIDForSlot(t, nodes[1].cluster, uint64(slotID), "u-remote-scan")
    hashSlot := mustHashSlotForKey(t, nodes[1].cluster, uid)
    require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateUser(ctx, metadb.User{UID: uid}))

    page, _, done, err := nodes[0].store.ScanUsersSlotPage(ctx, slotID, metadb.UserCursor{}, 10)
    require.NoError(t, err)
    require.True(t, done)
    require.Contains(t, userUIDsFromMeta(page), uid)
}
```

Use the existing `startTwoNodeHashSlotStores`, `findUIDForSlot`, `findUIDForSlotWithDifferentHashSlot`, and `mustHashSlotForKey` helpers from `pkg/slot/proxy/testutil_test.go`.

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./pkg/slot/proxy -run 'TestStoreScanUsersSlotPage' -count=1`

Expected: FAIL because `ScanUsersSlotPage` and RPC op do not exist.

- [ ] **Step 3: Add proxy method and identity RPC op**

In `identity_rpc.go` add:

```go
const identityRPCScanUsersPage = "scan_users_page"
```

Extend request/response structs:

```go
type identityRPCRequest struct {
    Op string `json:"op"`
    SlotID uint64 `json:"slot_id"`
    UID string `json:"uid,omitempty"`
    DeviceFlag int64 `json:"device_flag,omitempty"`
    After metadb.UserCursor `json:"after,omitempty"`
    Limit int `json:"limit,omitempty"`
}

type identityRPCResponse struct {
    Status string `json:"status"`
    LeaderID uint64 `json:"leader_id,omitempty"`
    User *metadb.User `json:"user,omitempty"`
    Device *metadb.Device `json:"device,omitempty"`
    Users []metadb.User `json:"users,omitempty"`
    Cursor metadb.UserCursor `json:"cursor,omitempty"`
    Done bool `json:"done,omitempty"`
}
```

Add public method:

```go
// ScanUsersSlotPage reads one authoritative user page for a physical Slot.
func (s *Store) ScanUsersSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error) {
    if s.shouldServeSlotLocally(slotID) {
        return s.scanUsersSlotPageLocal(ctx, slotID, after, limit)
    }
    resp, err := s.callIdentityRPC(ctx, slotID, identityRPCRequest{Op: identityRPCScanUsersPage, SlotID: uint64(slotID), After: after, Limit: limit})
    if err != nil { return nil, metadb.UserCursor{}, false, err }
    return append([]metadb.User(nil), resp.Users...), resp.Cursor, resp.Done, nil
}
```

Implement `scanUsersSlotPageLocal` using the same heap pattern as `scanChannelRuntimeMetaSlotPageLocal`, but compare by `UID`.

- [ ] **Step 4: Extend binary codec**

In `identity_subscriber_codec.go`:

- Add an op ID for `scan_users_page`.
- Append/read `After.UID` and `Limit` in requests.
- Append/read `Users`, `Cursor.UID`, and `Done` in responses.
- Keep backward compatibility inside current binary version by adding fields in a deterministic order; tests should cover get user/get device still round trip.

- [ ] **Step 5: Run proxy tests**

Run: `GOWORK=off go test ./pkg/slot/proxy -run 'TestStoreScanUsersSlotPage|TestStoreGetUser|TestStoreCreateUser|Test.*Identity' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit proxy scan**

```bash
git add pkg/slot/proxy/store.go pkg/slot/proxy/identity_rpc.go pkg/slot/proxy/identity_subscriber_codec.go pkg/slot/proxy/integration_test.go
git commit -m "feat: scan users through slot proxy"
```

---

### Task 3: Add management user aggregation usecase

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/users.go`
- Create: `internal/usecase/management/users_test.go`

- [ ] **Step 1: Write failing management tests**

Create `internal/usecase/management/users_test.go` with focused fakes. Cover list aggregation first:

```go
func TestListUsersAggregatesDeviceAndPresenceSummary(t *testing.T) {
    reader := newFakeManagementUserReader()
    reader.slotPages[1] = []metadb.User{{UID: "u1"}, {UID: "u2"}}
    reader.devices[deviceKey{"u1", int64(frame.APP)}] = metadb.Device{UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1"}
    presence := &fakeManagementPresence{routes: map[string][]presence.Route{
        "u1": {{UID: "u1", NodeID: 2, SessionID: 10, DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster)}},
    }}
    app := New(Options{Cluster: fakeClusterWithSlots(1), Users: reader, UserPresence: presence})

    got, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 50})
    require.NoError(t, err)
    require.Len(t, got.Items, 2)
    require.Equal(t, "u1", got.Items[0].UID)
    require.True(t, got.Items[0].Online)
    require.Equal(t, 1, got.Items[0].DeviceCount)
    require.Equal(t, 1, got.Items[0].TokenSetCount)
}
```

Add separate tests for keyword filtering, cursor keyword mismatch, detail, kick, and reset token generation.

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./internal/usecase/management -run 'Test.*User' -count=1`

Expected: FAIL because user usecase types/methods do not exist.

- [ ] **Step 3: Add management options and ports**

In `app.go`, add narrow ports and fields:

```go
type UserReader interface {
    // ScanUsersSlotPage returns one authoritative user page for a physical Slot.
    ScanUsersSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error)
    // GetUser returns one authoritative user record.
    GetUser(ctx context.Context, uid string) (metadb.User, error)
    // GetDevice returns one authoritative device record.
    GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

type UserOperator interface {
    // UpdateToken creates or replaces a user's device token.
    UpdateToken(ctx context.Context, cmd user.UpdateTokenCommand) error
    // DeviceQuit clears stored device tokens and kicks matching local sessions.
    DeviceQuit(ctx context.Context, cmd user.DeviceQuitCommand) error
}

type UserPresenceDirectory interface {
    // EndpointsByUIDs returns authoritative online routes keyed by UID.
    EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error)
}

type UserRouteActionDispatcher interface {
    // ApplyRouteAction applies a close/kick action on the route owner node.
    ApplyRouteAction(ctx context.Context, action presence.RouteAction) error
}
```

Add `Users`, `UserOperator`, `UserPresence`, and `UserActions` to `Options` and `App`.

- [ ] **Step 4: Implement DTOs and list/detail logic**

Create `users.go` with:

```go
const defaultUserListInternalScanLimit = 200

type UserListCursor struct {
    SlotID uint32
    UID string
    KeywordHash uint32
}

type ListUsersRequest struct { Limit int; Cursor UserListCursor; Keyword string }
type ListUsersResponse struct { Items []UserListItem; HasMore bool; NextCursor UserListCursor }
type UserListItem struct { UID string; SlotID uint32; HashSlot uint16; Online bool; OnlineDeviceCount int; OnlineDeviceFlags []string; DeviceCount int; TokenSetCount int }
type UserDetail struct { UID string; SlotID uint32; HashSlot uint16; Online bool; Devices []UserDevice; Connections []Connection }
```

Implementation details:

- Sort `cluster.SlotIDs()` before scanning.
- Validate `Limit > 0` and let manager HTTP enforce max 200.
- Bind cursor to `keyword` using `crc32.ChecksumIEEE([]byte(keyword))`; empty keyword hash should be deterministic.
- For each returned UID, call `a.userDeviceSummary(ctx, uid)` and batch presence for the page.
- Use fixed device flags `frame.APP`, `frame.WEB`, `frame.PC`, `frame.SYSTEM` for read summaries.
- Device labels should match existing manager labels: `app`, `web`, `pc`, `system`, `unknown`.

- [ ] **Step 5: Implement kick and reset operations**

Add request/response types:

```go
type KickUserRequest struct { UID string; DeviceFlag string }
type KickUserResponse struct { UID string; DeviceFlag string; Changed bool }
type ResetUserTokenRequest struct { UID string; DeviceFlag string; DeviceLevel string; Token string }
type ResetUserTokenResponse struct { UID string; DeviceFlag string; DeviceLevel string; Token string }
```

Behavior:

- `KickUser` calls `UserOperator.DeviceQuit` with `DeviceFlag=-1` for `all`, or the numeric flag for `app/web/pc`.
- To make force-offline cluster-wide, also read authoritative presence routes for the UID and call `UserActions.ApplyRouteAction` for matching routes:

```go
presence.RouteAction{
    UID: route.UID,
    NodeID: route.NodeID,
    BootID: route.BootID,
    SessionID: route.SessionID,
    Kind: "kick_then_close",
    Reason: "manager force offline",
}
```

- Do not support kicking `system` in this slice; return `metadb.ErrInvalidArgument` for `system` write input.
- `ResetUserToken` generates a token with `crypto/rand` when request token is blank, then calls `UpdateToken`.

- [ ] **Step 6: Run management tests**

Run: `GOWORK=off go test ./internal/usecase/management -run 'Test.*User' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit management usecase**

```bash
git add internal/usecase/management/app.go internal/usecase/management/users.go internal/usecase/management/users_test.go
git commit -m "feat: add manager user usecases"
```

---

### Task 4: Add manager HTTP routes and cursor codec

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/cursor_codec.go`
- Create: `internal/access/manager/users.go`
- Create: `internal/access/manager/users_test.go`
- Modify: `internal/access/manager/server_test.go` if permission tests are centralized there

- [ ] **Step 1: Write failing HTTP tests**

Create `users_test.go` with tests like:

```go
func TestManagerListUsersReturnsPage(t *testing.T) {
    srv := New(Options{ListenAddr: "127.0.0.1:0", Management: &fakeManagerUsers{
        listUsers: managementusecase.ListUsersResponse{Items: []managementusecase.UserListItem{{UID: "u1", SlotID: 1, HashSlot: 7}}, HasMore: true, NextCursor: managementusecase.UserListCursor{SlotID: 1, UID: "u1"}},
    }})
    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/users?limit=1", nil)
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.JSONEq(t, `{"items":[{"uid":"u1","slot_id":1,"hash_slot":7,"online":false,"online_device_count":0,"online_device_flags":null,"device_count":0,"token_set_count":0}],"has_more":true,"next_cursor":"ignored-in-test"}`, normalizeCursorJSON(t, rec.Body.Bytes()))
}
```

Use a helper to avoid asserting exact opaque cursor when practical, or decode it and assert round trip.

Add tests for:

- invalid limit => 400
- cursor/keyword mismatch => 400
- detail not found => 404
- kick/reset call management with parsed labels
- auth read/write permission enforcement (`cluster.user:r`, `cluster.user:w`)

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./internal/access/manager -run 'TestManager.*User|Test.*Users' -count=1`

Expected: FAIL because routes do not exist.

- [ ] **Step 3: Extend Management interface**

In `server.go` add:

```go
// ListUsers returns one manager-facing user page.
ListUsers(ctx context.Context, req managementusecase.ListUsersRequest) (managementusecase.ListUsersResponse, error)
// GetUser returns one manager-facing user detail.
GetUser(ctx context.Context, uid string) (managementusecase.UserDetail, error)
// KickUser forces one user's sessions offline.
KickUser(ctx context.Context, req managementusecase.KickUserRequest) (managementusecase.KickUserResponse, error)
// ResetUserToken resets one user device token.
ResetUserToken(ctx context.Context, req managementusecase.ResetUserTokenRequest) (managementusecase.ResetUserTokenResponse, error)
```

- [ ] **Step 4: Add routes**

In `routes.go`:

```go
userReads := s.engine.Group("/manager")
if s.auth.enabled() { userReads.Use(s.requirePermission("cluster.user", "r")) }
userReads.GET("/users", s.handleUsers)
userReads.GET("/users/:uid", s.handleUser)

userWrites := s.engine.Group("/manager")
if s.auth.enabled() { userWrites.Use(s.requirePermission("cluster.user", "w")) }
userWrites.POST("/users/:uid/kick", s.handleUserKick)
userWrites.POST("/users/:uid/token/reset", s.handleUserTokenReset)
```

- [ ] **Step 5: Add user cursor codec**

In `cursor_codec.go`, add a magic value and helpers:

```go
var userListCursorMagic = [...]byte{'W', 'K', 'U', 'L'}

func encodeUserListCursor(cursor managementusecase.UserListCursor) (string, error) { ... }
func decodeUserListCursor(raw string) (managementusecase.UserListCursor, error) { ... }
```

Binary payload: magic, version, `SlotID` uint32, `KeywordHash` uint32, varlen UID.

- [ ] **Step 6: Implement HTTP handlers**

Create `users.go` with DTOs and parsing helpers. Keep comments in English on response structs. Map errors:

- `metadb.ErrInvalidArgument` => 400
- `metadb.ErrNotFound` => 404
- slot leader authoritative unavailable helpers => 503
- default => 500

- [ ] **Step 7: Run manager tests**

Run: `GOWORK=off go test ./internal/access/manager -run 'TestManager.*User|Test.*Users|TestManagerAuth' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit manager HTTP**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/cursor_codec.go internal/access/manager/users.go internal/access/manager/users_test.go internal/access/manager/server_test.go
git commit -m "feat: expose manager user APIs"
```

---

### Task 5: Wire manager users through `internal/app`

**Files:**
- Modify: `internal/app/build.go`
- Create or modify: `internal/app/management_users.go`
- Modify: `internal/app/build_test.go`
- Modify: `internal/app/user_management_integration_test.go` if adding integration coverage here

- [ ] **Step 1: Write failing wiring test**

In `build_test.go`, extend the manager wiring test to assert the `management.App` has user dependencies. If direct field access is not available, add a focused integration-ish test that constructs an app with manager enabled and verifies `app.managementApp.ListUsers` does not fail due to missing user reader when store is configured.

- [ ] **Step 2: Run wiring test and verify failure**

Run: `GOWORK=off go test ./internal/app -run 'Test.*Build.*Manager|Test.*Management.*User' -count=1`

Expected: FAIL because management user dependencies are not wired.

- [ ] **Step 3: Wire dependencies in build**

In `build.go` inside `managementusecase.New` options add:

```go
Users: app.store,
UserOperator: userApp,
UserPresence: authorityClient,
UserActions: authorityClient,
```

If type names differ from Task 3, use the final names from `internal/usecase/management`.

- [ ] **Step 4: Add adapters only if needed**

If `presenceAuthorityClient` already satisfies `EndpointsByUIDs` and `ApplyRouteAction`, no adapter is needed. If conversion is needed, add `management_users.go` with small focused adapters only.

- [ ] **Step 5: Add optional integration test**

If fast enough, add to `internal/app/user_management_integration_test.go`:

- start one app with manager enabled
- create/update token through existing user API or store
- call manager `/manager/users` and `/manager/users/:uid`
- verify the user appears and token is not returned

If it starts multi-node apps or is slow, keep it behind `integration` tag.

- [ ] **Step 6: Run app tests**

Run: `GOWORK=off go test ./internal/app -run 'Test.*UserManagement|Test.*Build.*Manager' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit app wiring**

```bash
git add internal/app/build.go internal/app/management_users.go internal/app/build_test.go internal/app/user_management_integration_test.go
git commit -m "feat: wire manager user operations"
```

Only include `internal/app/management_users.go` if it was created.

---

### Task 6: Add web manager API client bindings

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

In `web/src/lib/manager-api.test.ts`, add tests:

```ts
it("fetches manager users with search params", async () => {
  fetchMock.mockResolvedValue(new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 }))

  await expect(getUsers({ keyword: "u1", limit: 25, cursor: "abc" })).resolves.toEqual({ items: [], has_more: false })

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/users?keyword=u1&limit=25&cursor=abc",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})

it("resets a manager user token", async () => {
  fetchMock.mockResolvedValue(new Response(JSON.stringify({ uid: "u1", device_flag: "app", device_level: "master", token: "next" }), { status: 200 }))

  await resetUserToken("u1", { deviceFlag: "app", deviceLevel: "master" })

  expect(JSON.parse((fetchMock.mock.calls[0]?.[1] as RequestInit).body as string)).toEqual({
    device_flag: "app",
    device_level: "master",
  })
})
```

- [ ] **Step 2: Run web API tests and verify failure**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`

Expected: FAIL because functions/types are missing.

- [ ] **Step 3: Add TypeScript types**

In `manager-api.types.ts` add:

```ts
export type UserListParams = { keyword?: string; limit?: number; cursor?: string }
export type ManagerUserListItem = { uid: string; slot_id: number; hash_slot: number; online: boolean; online_device_count: number; online_device_flags: string[]; device_count: number; token_set_count: number }
export type ManagerUsersResponse = { items: ManagerUserListItem[]; has_more: boolean; next_cursor?: string }
export type ManagerUserDevice = { device_flag: string; device_level: string; token_set: boolean; online: boolean; online_session_count: number }
export type ManagerUserConnection = { node_id: number; session_id: number; device_id: string; device_flag: string; device_level: string; listener: string; connected_at: string; remote_addr: string; local_addr: string }
export type ManagerUserDetailResponse = { uid: string; slot_id: number; hash_slot: number; online: boolean; devices: ManagerUserDevice[]; connections: ManagerUserConnection[] }
export type KickUserInput = { deviceFlag: "all" | "app" | "web" | "pc" }
export type KickUserResponse = { uid: string; device_flag: string; changed: boolean }
export type ResetUserTokenInput = { deviceFlag: "app" | "web" | "pc" | "system"; deviceLevel: "master" | "slave"; token?: string }
export type ResetUserTokenResponse = { uid: string; device_flag: string; device_level: string; token: string }
```

- [ ] **Step 4: Add API functions**

In `manager-api.ts` add `buildUsersPath`, `getUsers`, `getUser`, `kickUser`, and `resetUserToken`. Encode UID path segments with `encodeURIComponent`.

- [ ] **Step 5: Run web API tests**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`

Expected: PASS.

- [ ] **Step 6: Commit web API client**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web user api client"
```

---

### Task 7: Implement the real web `/users` page

**Files:**
- Modify: `web/src/pages/users/page.tsx`
- Create: `web/src/pages/users/page.test.tsx`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/i18n/messages/en.ts`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/users/page.test.tsx`. Mock manager API functions and render through project providers. Cover:

```ts
it("renders the first user page", async () => {
  getUsersMock.mockResolvedValue({
    items: [{ uid: "u1", slot_id: 1, hash_slot: 7, online: true, online_device_count: 1, online_device_flags: ["app"], device_count: 1, token_set_count: 1 }],
    has_more: false,
  })

  renderUsersPage()

  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.getByText(/app/i)).toBeInTheDocument()
})
```

Add tests for:

- searching by UID calls `getUsers({ keyword })`
- load more appends rows with `next_cursor`
- opening detail calls `getUser(uid)`
- kick dialog calls `kickUser`
- token reset dialog calls `resetUserToken` and displays returned token
- 403 maps to forbidden state
- 503 maps to unavailable state
- empty page shows empty state

- [ ] **Step 2: Run page tests and verify failure**

Run: `cd web && bun run test -- src/pages/users/page.test.tsx`

Expected: FAIL because page is a placeholder.

- [ ] **Step 3: Add i18n messages**

Add keys under `users.*`, for example:

```ts
"users.search.placeholder": "搜索 UID",
"users.table.uid": "UID",
"users.table.online": "在线",
"users.table.devices": "设备",
"users.table.tokens": "Token",
"users.table.routing": "路由",
"users.action.kick": "强制下线",
"users.action.resetToken": "重置 Token",
"users.token.visibleOnce": "新 Token 仅显示一次，请立即保存。",
```

Mirror English strings in `en.ts`.

- [ ] **Step 4: Implement page state and list rendering**

In `page.tsx` replace the placeholder. Use:

- `useEffect` for initial `getUsers({ limit: 50 })`
- controlled search input
- `runQuery({ keyword, cursor })` helper
- `ResourceState` for loading/error/empty
- `SectionCard` containing a responsive table
- `Button` for search, refresh, load more, detail, kick, reset

- [ ] **Step 5: Implement detail and actions**

Use existing components:

- `DetailSheet` for details
- `ConfirmDialog` for kick
- `ActionFormDialog` for token reset

Keep token reset result in component state and render it in the dialog or a result card. Clear it when dialog closes.

- [ ] **Step 6: Run page tests**

Run: `cd web && bun run test -- src/pages/users/page.test.tsx`

Expected: PASS.

- [ ] **Step 7: Commit web page**

```bash
git add web/src/pages/users/page.tsx web/src/pages/users/page.test.tsx web/src/i18n/messages/zh-CN.ts web/src/i18n/messages/en.ts
git commit -m "feat: implement users admin page"
```

---

### Task 8: Documentation and final verification

**Files:**
- Modify: `web/README.md`
- Optional modify: `docs/raw/web-admin-restructure.md` if you want the roadmap to mark this slice implemented

- [ ] **Step 1: Update web README matrix**

In `web/README.md`, change `/users` from placeholder to implemented and list APIs:

```markdown
| `/users` | `GET /manager/users`, `GET /manager/users/:uid`, `POST /manager/users/:uid/kick`, `POST /manager/users/:uid/token/reset` | Implemented |
```

Keep `/channels-biz` and `/system-users` as placeholder unless implemented separately.

- [ ] **Step 2: Optional roadmap update**

If updating `docs/raw/web-admin-restructure.md`, mark user management backend API + frontend page as partially implemented and note ban/unban remains follow-up. Do not overstate completion of ban/unban.

- [ ] **Step 3: Run Go targeted verification**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/proxy -count=1
GOWORK=off go test ./internal/usecase/management ./internal/access/manager -count=1
GOWORK=off go test ./internal/app -run 'Test.*UserManagement|Test.*Build.*Manager' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run web verification**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/users/page.test.tsx
cd web && bun run build
```

Expected: PASS.

- [ ] **Step 5: Run broader relevant tests if time permits**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/slot/... -count=1
```

Expected: PASS. If too slow, record the targeted verification and why broader tests were skipped.

- [ ] **Step 6: Commit docs and verification notes**

```bash
git add web/README.md docs/raw/web-admin-restructure.md
git commit -m "docs: update users admin status"
```

Only add `docs/raw/web-admin-restructure.md` if it was modified.

- [ ] **Step 7: Final review before handoff**

Run:

```bash
git status --short
git log --oneline -8
```

Expected:

- only intended feature changes are present in the feature worktree
- unrelated user-owned `ui/` deletions from the original worktree are not included
- commits are small and ordered by storage -> usecase -> HTTP -> web -> docs

