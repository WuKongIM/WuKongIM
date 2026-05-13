# Manager Channels Business Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement manager-scoped channel business list/detail/member APIs and the real `web/` `/channels-biz` page.

**Architecture:** Keep manager HTTP thin. Add authoritative channel page reads in `pkg/slot/meta` and `pkg/slot/proxy`, add paginated member-list reads to `internal/usecase/channel`, aggregate manager DTOs in `internal/usecase/management`, wire through `internal/app`, and render the page in the existing React `web/` app. Reuse existing channel mutation usecases; do not introduce a broad service layer.

**Tech Stack:** Go, Gin, slot meta/proxy, existing channel usecase, React 19, Vite, Vitest, Testing Library.

---

## Pre-Execution Notes

- Read `AGENTS.md` before implementation.
- Execute in the current isolated worktree: `.worktrees/manager-channels-biz` on branch `feat/manager-channels-biz`.
- The main worktree has unrelated uncommitted changes. Do not touch, revert, stage, or commit those changes.
- `ui/` is the prototype directory and is out of scope.
- If a touched package contains `FLOW.md`, read it first and update it if behavior changes. For this plan that means at least `internal/FLOW.md` and `pkg/slot/FLOW.md` may need small updates if public package capabilities are added.
- Use TDD. Each task starts with failing tests, then minimal code, then targeted verification, then a small commit.

## File Structure

### Go storage and proxy

- Modify: `pkg/slot/meta/channel.go`
  - Add `ChannelCursor`, channel primary prefix/record decode helpers, and `ListChannelsPage`.
- Modify: `pkg/slot/meta/channel_test.go`
  - Add channel page scan tests.
- Modify: `pkg/slot/proxy/channel_rpc.go`
  - Add channel RPC operation `scan_channels_page` and local/remote authoritative handlers.
- Modify: `pkg/slot/proxy/store.go`
  - Add public `ScanChannelsSlotPage` entry point.
- Modify: `pkg/slot/proxy/integration_test.go` or create `pkg/slot/proxy/channel_page_integration_test.go`
  - Add local and remote authoritative channel page tests.
- Modify: `pkg/slot/proxy/binary_rpc_test.go`
  - Add channel scan binary codec round trip.
- Modify if needed: `pkg/slot/FLOW.md`
  - Document `ScanChannelsSlotPage` and channel RPC if public flow text becomes stale.

### Go usecases and manager HTTP

- Modify: `internal/usecase/channel/types.go`
  - Add `MemberListPageRequest` and `MemberListPageResult`.
- Modify: `internal/usecase/channel/app.go`
  - Add `ListSubscribersPage`, `ListAllowlistPage`, and `ListDenylistPage`.
- Modify: `internal/usecase/channel/app_test.go`
  - Add paginated member-list tests.
- Modify: `internal/usecase/management/app.go`
  - Add narrow channel business reader/operator ports and app fields.
- Create: `internal/usecase/management/channels_biz.go`
  - Add DTOs, list/detail aggregation, member page/action orchestration, cursor validation helpers, and internal-channel filtering.
- Create: `internal/usecase/management/channels_biz_test.go`
  - Add usecase tests with fake channel reader/operator ports.
- Modify: `internal/access/manager/server.go`
  - Add new methods to `Management` interface.
- Modify: `internal/access/manager/routes.go`
  - Register `/manager/channels...` routes with `cluster.channel` read/write permissions.
- Modify: `internal/access/manager/cursor_codec.go`
  - Add channel list/member cursor encode/decode with new magic values.
- Create: `internal/access/manager/channels_biz.go`
  - Add HTTP handlers, DTOs, request parsing, and error mapping.
- Create: `internal/access/manager/channels_biz_test.go`
  - Add HTTP behavior tests.
- Modify: `internal/access/manager/server_test.go`
  - Add route/permission tests if route tests are centralized there.
- Modify: `internal/app/build.go`
  - Wire `ChannelBusinessReader: app.store` and `ChannelBusinessOperator: app.channelApp` into `managementusecase.New`.
- Modify: `internal/app/build_test.go`
  - Assert channel business dependencies are wired when manager is enabled.
- Modify if needed: `internal/FLOW.md`
  - Document manager channel business APIs if existing flow docs become stale.

### Web

- Modify: `web/src/lib/manager-api.types.ts`
  - Add channel business list/detail/member/action types and params.
- Modify: `web/src/lib/manager-api.ts`
  - Add `getBusinessChannels`, `getBusinessChannel`, `upsertBusinessChannel`, `getBusinessChannelMembers`, `addBusinessChannelMembers`, and `removeBusinessChannelMembers`.
- Modify: `web/src/lib/manager-api.test.ts`
  - Add API client tests.
- Modify: `web/src/pages/channels-biz/page.tsx`
  - Replace placeholder with real list/detail/member UI.
- Create: `web/src/pages/channels-biz/page.test.tsx`
  - Add page tests for load, search/type filter, load more, detail, upsert, member add/remove, person subscriber disabled, and errors.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese channel business strings.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English channel business strings.
- Modify: `web/README.md`
  - Change `/channels-biz` matrix status from placeholder to implemented.
- Modify: `docs/raw/web-admin-restructure.md`
  - Mark channel business management MVP as implemented and note remove-all/import remain follow-ups.

---

## Task 1: Add single-hash-slot channel page scans

**Files:**
- Modify: `pkg/slot/meta/channel.go`
- Modify: `pkg/slot/meta/channel_test.go`

- [ ] **Step 1: Write failing tests for channel page scanning**

Add tests to `pkg/slot/meta/channel_test.go`:

```go
func TestShardListChannelsPageReturnsStableCursor(t *testing.T) {
    db := newTestDB(t)
    ctx := context.Background()
    shard := db.ForSlot(7)

    require.NoError(t, shard.CreateChannel(ctx, Channel{ChannelID: "a", ChannelType: 2, Ban: 1}))
    require.NoError(t, shard.CreateChannel(ctx, Channel{ChannelID: "b", ChannelType: 1, Disband: 1}))
    require.NoError(t, shard.CreateChannel(ctx, Channel{ChannelID: "b", ChannelType: 2, SendBan: 1}))

    page1, cursor, done, err := shard.ListChannelsPage(ctx, ChannelCursor{}, 2)
    require.NoError(t, err)
    require.False(t, done)
    require.Equal(t, []string{"a:2", "b:1"}, channelKeysForTest(page1))
    require.Equal(t, ChannelCursor{ChannelID: "b", ChannelType: 1}, cursor)

    page2, cursor, done, err := shard.ListChannelsPage(ctx, cursor, 2)
    require.NoError(t, err)
    require.True(t, done)
    require.Equal(t, []string{"b:2"}, channelKeysForTest(page2))
    require.Equal(t, ChannelCursor{ChannelID: "b", ChannelType: 2}, cursor)
}

func TestShardListChannelsPageRejectsInvalidLimit(t *testing.T) {
    db := newTestDB(t)
    _, _, _, err := db.ForSlot(7).ListChannelsPage(context.Background(), ChannelCursor{}, 0)
    require.ErrorIs(t, err, ErrInvalidArgument)
}
```

Add `channelKeysForTest` helper if none exists.

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./pkg/slot/meta -run 'TestShardListChannelsPage' -count=1`

Expected: FAIL because `ListChannelsPage` and `ChannelCursor` do not exist.

- [ ] **Step 3: Implement minimal meta page scan**

In `pkg/slot/meta/channel.go`, add:

```go
// ChannelCursor identifies the last emitted channel in a shard page scan.
type ChannelCursor struct {
    // ChannelID is the last emitted channel ID in primary-key order.
    ChannelID string
    // ChannelType is the last emitted channel type in primary-key order.
    ChannelType int64
}

// ListChannelsPage scans one hash slot page in primary-key order.
func (s *ShardStore) ListChannelsPage(ctx context.Context, after ChannelCursor, limit int) ([]Channel, ChannelCursor, bool, error)
```

Implementation notes:

- Validate store, context, `limit > 0`, and cursor `ChannelID` length.
- Use `encodeChannelPrimaryPrefix(hashSlot)` based on `encodeStatePrefix(hashSlot, ChannelTable.ID)`.
- If `after.ChannelID != ""`, lower bound is `nextPrefix(encodeChannelPrimaryKey(s.slot, after.ChannelID, after.ChannelType, channelPrimaryFamilyID))`.
- Decode key suffix as `channelID`, `channelType`, `familyID`.
- Decode value through `decodeChannelFamilyValue`.
- Collect `limit+1` rows to compute `done` and cursor.

- [ ] **Step 4: Run targeted tests**

Run: `GOWORK=off go test ./pkg/slot/meta -run 'TestShardListChannelsPage|Test.*Channel' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit storage page scan**

```bash
git add pkg/slot/meta/channel.go pkg/slot/meta/channel_test.go
git commit -m "feat: add channel page scans"
```

---

## Task 2: Add authoritative channel page scans in slot proxy

**Files:**
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/channel_rpc.go`
- Modify: `pkg/slot/proxy/binary_rpc_test.go`
- Create or modify: `pkg/slot/proxy/channel_page_integration_test.go`

- [ ] **Step 1: Write failing proxy integration tests**

Add tests:

```go
func TestStoreScanChannelsSlotPageReadsAuthoritativeLocalSlot(t *testing.T) {
    nodes := startTwoNodeHashSlotStores(t, 8)
    ctx := context.Background()
    slotID := multiraft.SlotID(1)
    channelA := findChannelIDForSlot(t, nodes[0].cluster, uint64(slotID), "local-a")
    channelB := findChannelIDForSlot(t, nodes[0].cluster, uint64(slotID), "local-b")
    hashSlotA := mustHashSlotForKey(t, nodes[0].cluster, channelA)
    hashSlotB := mustHashSlotForKey(t, nodes[0].cluster, channelB)
    require.NoError(t, nodes[0].db.ForHashSlot(hashSlotA).CreateChannel(ctx, metadb.Channel{ChannelID: channelA, ChannelType: 2}))
    require.NoError(t, nodes[0].db.ForHashSlot(hashSlotB).CreateChannel(ctx, metadb.Channel{ChannelID: channelB, ChannelType: 2}))

    page, cursor, done, err := nodes[0].store.ScanChannelsSlotPage(ctx, slotID, metadb.ChannelCursor{}, 1)
    require.NoError(t, err)
    require.Len(t, page, 1)
    require.False(t, done)

    page2, _, _, err := nodes[0].store.ScanChannelsSlotPage(ctx, slotID, cursor, 10)
    require.NoError(t, err)
    require.NotEmpty(t, page2)
}

func TestStoreScanChannelsSlotPageReadsAuthoritativeRemoteSlot(t *testing.T) {
    nodes := startTwoNodeHashSlotStores(t, 8)
    ctx := context.Background()
    slotID := multiraft.SlotID(2)
    channelID := findChannelIDForSlot(t, nodes[1].cluster, uint64(slotID), "remote-scan")
    hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
    require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannel(ctx, metadb.Channel{ChannelID: channelID, ChannelType: 2}))

    page, _, done, err := nodes[0].store.ScanChannelsSlotPage(ctx, slotID, metadb.ChannelCursor{}, 10)
    require.NoError(t, err)
    require.True(t, done)
    require.Contains(t, channelIDsFromMeta(page), channelID)
}
```

If no channel-slot helper exists, add a small local helper that tries suffixes until `cluster.SlotForKey(candidate) == slotID`.

- [ ] **Step 2: Add failing channel RPC codec test**

In `pkg/slot/proxy/binary_rpc_test.go`, add a round-trip covering:

```go
channelRPCRequest{Op: channelRPCScanChannelsPage, SlotID: 1, After: metadb.ChannelCursor{ChannelID: "a", ChannelType: 2}, Limit: 50}
channelRPCResponse{Status: rpcStatusOK, Channels: []metadb.Channel{{ChannelID: "a", ChannelType: 2, Ban: 1}}, Cursor: metadb.ChannelCursor{ChannelID: "a", ChannelType: 2}, Done: true}
```

- [ ] **Step 3: Run tests and verify failure**

Run: `GOWORK=off go test ./pkg/slot/proxy -run 'TestStoreScanChannelsSlotPage|Test.*Channel.*RPC' -count=1`

Expected: FAIL because `ScanChannelsSlotPage` and channel scan RPC fields do not exist.

- [ ] **Step 4: Add proxy method and channel RPC op**

In `channel_rpc.go`:

- Add `channelRPCGetForPermission = "get_for_permission"` and `channelRPCScanChannelsPage = "scan_channels_page"` if an op field is needed.
- Extend `channelRPCRequest` with `Op string`, `After metadb.ChannelCursor`, and `Limit int`.
- Extend `channelRPCResponse` with `Channels []metadb.Channel`, `Cursor metadb.ChannelCursor`, and `Done bool`.
- Preserve compatibility for existing get-channel RPC by treating empty `Op` as get-for-permission.

In `store.go` or `channel_rpc.go`, add:

```go
// ScanChannelsSlotPage reads one authoritative channel page for a physical Slot.
func (s *Store) ScanChannelsSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
```

Local scan should merge hash-slot pages with a heap ordered by `(ChannelID, ChannelType)`.

- [ ] **Step 5: Extend binary codec**

Update channel RPC binary encoding to include op, after cursor, limit, channels page, cursor, and done. Keep decoding strict about trailing bytes.

- [ ] **Step 6: Run targeted tests**

Run: `GOWORK=off go test ./pkg/slot/proxy -run 'TestStoreScanChannelsSlotPage|Test.*Channel.*RPC|TestStoreGetChannelForPermission' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit proxy channel scan**

```bash
git add pkg/slot/proxy/store.go pkg/slot/proxy/channel_rpc.go pkg/slot/proxy/binary_rpc_test.go pkg/slot/proxy/channel_page_integration_test.go
git commit -m "feat: scan channels through slot proxy"
```

---

## Task 3: Add paginated channel member usecase methods

**Files:**
- Modify: `internal/usecase/channel/types.go`
- Modify: `internal/usecase/channel/app.go`
- Modify: `internal/usecase/channel/app_test.go`

- [ ] **Step 1: Write failing member page tests**

Add tests for:

- `ListSubscribersPage` delegates to ordinary `ListChannelSubscribers`.
- `ListAllowlistPage` delegates to namespaced allowlist channel ID.
- `ListDenylistPage` delegates to namespaced denylist channel ID.
- invalid or missing store returns existing errors.

Example:

```go
func TestListSubscribersPageReturnsOnePage(t *testing.T) {
    store := &recordingStore{listPages: []listPage{{uids: []string{"u1", "u2"}, cursor: "u2", done: false}}}
    app := New(Options{Store: store})

    got, err := app.ListSubscribersPage(context.Background(), MemberListPageRequest{
        ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
        Limit: 2,
    })

    require.NoError(t, err)
    require.Equal(t, MemberListPageResult{Members: []Member{{UID: "u1"}, {UID: "u2"}}, NextCursor: "u2", HasMore: true}, got)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./internal/usecase/channel -run 'TestList.*Page' -count=1`

Expected: FAIL because page methods/types do not exist.

- [ ] **Step 3: Implement member page methods**

Add types:

```go
// MemberListPageRequest configures one paginated channel member-list read.
type MemberListPageRequest struct {
    ChannelKey ChannelKey
    AfterUID string
    Limit int
}

// MemberListPageResult contains one member-list page.
type MemberListPageResult struct {
    Members []Member
    NextCursor string
    HasMore bool
}
```

Add methods that call `store.ListChannelSubscribers` with ordinary or namespaced channel ID. Validate store and `Limit > 0`.

- [ ] **Step 4: Run targeted tests**

Run: `GOWORK=off go test ./internal/usecase/channel -count=1`

Expected: PASS.

- [ ] **Step 5: Commit member page methods**

```bash
git add internal/usecase/channel/types.go internal/usecase/channel/app.go internal/usecase/channel/app_test.go
git commit -m "feat: page channel member lists"
```

---

## Task 4: Add manager channel business usecases

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/channels_biz.go`
- Create: `internal/usecase/management/channels_biz_test.go`

- [ ] **Step 1: Write failing management usecase tests**

Cover:

- list aggregates slot/hash-slot and filters by keyword/type.
- list filters internal member-list and `____cmd` channel IDs.
- cursor rejects keyword/type mismatch.
- detail returns flags and `has_subscribers` / `has_allowlist` / `has_denylist`.
- upsert delegates to channel operator and returns fresh detail.
- member pages delegate list kind and encode `has_more`.
- member add/remove trim/deduplicate UIDs.
- person-channel ordinary subscriber mutations are rejected.

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./internal/usecase/management -run 'Test.*ChannelBiz|Test.*BusinessChannel' -count=1`

Expected: FAIL because types/methods do not exist.

- [ ] **Step 3: Add ports and DTOs**

In `app.go`, add `ChannelBusinessReader`, `ChannelBusinessOperator`, options, and app fields.

In `channels_biz.go`, add DTOs:

```go
type ListBusinessChannelsRequest struct { Limit int; Cursor ChannelListCursor; TypeFilter int64; Keyword string }
type ListBusinessChannelsResponse struct { Items []BusinessChannelListItem; HasMore bool; NextCursor ChannelListCursor }
type BusinessChannelListItem struct { ChannelID string; ChannelType int64; SlotID uint32; HashSlot uint16; Ban bool; Disband bool; SendBan bool; SubscriberMutationVersion uint64 }
type BusinessChannelDetail struct { BusinessChannelListItem; HasSubscribers bool; HasAllowlist bool; HasDenylist bool }
type UpsertBusinessChannelRequest struct { ChannelID string; ChannelType int64; Ban bool; Disband bool; SendBan bool }
type ChannelMemberCursor struct { ChannelIDHash uint32; ChannelType int64; ListKind uint8; UID string }
type ListBusinessChannelMembersRequest struct { ChannelID string; ChannelType int64; ListKind string; Limit int; Cursor ChannelMemberCursor }
type ListBusinessChannelMembersResponse struct { Items []BusinessChannelMember; HasMore bool; NextCursor ChannelMemberCursor }
type MutateBusinessChannelMembersRequest struct { ChannelID string; ChannelType int64; ListKind string; UIDs []string; Add bool }
```

- [ ] **Step 4: Implement list/detail/upsert/member logic**

Rules:

- Scan sorted physical Slots.
- Filter internal channel IDs with helpers:
  - `strings.HasPrefix(channelID, "__wk_internal_memberlist__/")`
  - `strings.HasSuffix(channelID, "____cmd")`
- Optional type filter is zero for all, otherwise exact match.
- Keyword is trimmed substring match.
- Detail reads through `GetChannelForPermission`.
- Upsert uses channel operator `UpdateInfo`, then returns detail.
- Member list maps `subscribers`, `allowlist`, and `denylist` to operator page methods.
- Member mutations map list kind to operator add/remove method.
- Normalize UIDs by trim + dedupe in first-seen order; reject empty result.

- [ ] **Step 5: Run targeted tests**

Run: `GOWORK=off go test ./internal/usecase/management -run 'Test.*ChannelBiz|Test.*BusinessChannel' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit management usecases**

```bash
git add internal/usecase/management/app.go internal/usecase/management/channels_biz.go internal/usecase/management/channels_biz_test.go
git commit -m "feat: add manager channel business usecases"
```

---

## Task 5: Expose manager channel business HTTP APIs

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/cursor_codec.go`
- Create: `internal/access/manager/channels_biz.go`
- Create: `internal/access/manager/channels_biz_test.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Cover:

- `GET /manager/channels` parses `type`, `keyword`, `limit`, and `cursor`.
- invalid limit/cursor/type returns 400.
- `GET /manager/channels/:type/:id` maps detail and 404.
- `POST /manager/channels` maps JSON body to upsert request.
- member list routes parse list cursor and return page body.
- member add/remove routes pass UIDs and list kind.
- route permissions require `cluster.channel:r` or `cluster.channel:w`.

- [ ] **Step 2: Run tests and verify failure**

Run: `GOWORK=off go test ./internal/access/manager -run 'Test.*ChannelBiz|Test.*BusinessChannel|TestManagerRoutesRequirePermissions' -count=1`

Expected: FAIL because routes/handlers do not exist.

- [ ] **Step 3: Add interface and routes**

Extend `Management` interface with channel business methods. Register routes:

```go
channelBizReads := s.engine.Group("/manager")
channelBizReads.Use(s.requirePermission("cluster.channel", "r"))
channelBizReads.GET("/channels", s.handleBusinessChannels)
channelBizReads.GET("/channels/:channel_type/:channel_id", s.handleBusinessChannel)
channelBizReads.GET("/channels/:channel_type/:channel_id/subscribers", s.handleBusinessChannelSubscribers)
channelBizReads.GET("/channels/:channel_type/:channel_id/allowlist", s.handleBusinessChannelAllowlist)
channelBizReads.GET("/channels/:channel_type/:channel_id/denylist", s.handleBusinessChannelDenylist)

channelBizWrites := s.engine.Group("/manager")
channelBizWrites.Use(s.requirePermission("cluster.channel", "w"))
channelBizWrites.POST("/channels", s.handleBusinessChannelUpsert)
channelBizWrites.POST("/channels/:channel_type/:channel_id/subscribers/add", s.handleBusinessChannelSubscribersAdd)
channelBizWrites.POST("/channels/:channel_type/:channel_id/subscribers/remove", s.handleBusinessChannelSubscribersRemove)
...
```

- [ ] **Step 4: Add cursor codecs**

In `cursor_codec.go`, add:

- `channelListCursorMagic = [...]byte{'W','K','C','L'}`
- `channelMemberCursorMagic = [...]byte{'W','K','C','M'}`
- encode/decode helpers for `management.ChannelListCursor` and `management.ChannelMemberCursor`.

- [ ] **Step 5: Add handlers and DTO conversions**

Implement `channels_biz.go` with:

- default list limit 50/max 200.
- default member limit 100/max 500.
- path channel type parse with positive uint8-compatible range.
- JSON body structs for upsert and member UIDs.
- `writeBusinessChannelError` mapping `metadb.ErrInvalidArgument`, `metadb.ErrNotFound`, and authoritative leader unavailable.

- [ ] **Step 6: Run targeted tests**

Run: `GOWORK=off go test ./internal/access/manager -count=1`

Expected: PASS.

- [ ] **Step 7: Commit manager HTTP APIs**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/cursor_codec.go internal/access/manager/channels_biz.go internal/access/manager/channels_biz_test.go internal/access/manager/server_test.go
git commit -m "feat: expose manager channel business APIs"
```

---

## Task 6: Wire channel business dependencies in app

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`

- [ ] **Step 1: Write failing wiring test**

Add a test similar to the manager user wiring test that builds with manager enabled and asserts management channel business dependencies are configured. If direct field access is unavailable, use a small fake route or constructor-level test pattern already present in `build_test.go`.

- [ ] **Step 2: Run test and verify failure**

Run: `GOWORK=off go test ./internal/app -run 'Test.*ChannelBusiness|TestBuildWires.*Channel' -count=1`

Expected: FAIL because dependencies are not wired.

- [ ] **Step 3: Wire dependencies**

In `internal/app/build.go`, add to `managementusecase.New` options:

```go
ChannelBusinessReader: app.store,
ChannelBusinessOperator: app.channelApp,
```

- [ ] **Step 4: Run targeted test**

Run: `GOWORK=off go test ./internal/app -run 'Test.*ChannelBusiness|TestBuildWires.*Channel' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit app wiring**

```bash
git add internal/app/build.go internal/app/build_test.go
git commit -m "feat: wire manager channel business operations"
```

---

## Task 7: Add web API client bindings

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

Add tests for:

- list path with `type`, `keyword`, `limit`, and `cursor`.
- detail path encodes channel ID.
- upsert body maps snake_case fields.
- member list path selects list kind and cursor.
- add/remove body maps `uids`.

- [ ] **Step 2: Run tests and verify failure**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts -t 'channel business|business channel|channels biz'`

Expected: FAIL because API functions do not exist.

- [ ] **Step 3: Add types and functions**

Add TypeScript types for:

- `BusinessChannelListParams`
- `ManagerBusinessChannelListItem`
- `ManagerBusinessChannelsResponse`
- `ManagerBusinessChannelDetailResponse`
- `UpsertBusinessChannelInput`
- `BusinessChannelMemberListKind`
- `BusinessChannelMembersResponse`
- `MutateBusinessChannelMembersInput`
- `MutateBusinessChannelMembersResponse`

Add functions:

- `getBusinessChannels`
- `getBusinessChannel`
- `upsertBusinessChannel`
- `getBusinessChannelMembers`
- `addBusinessChannelMembers`
- `removeBusinessChannelMembers`

- [ ] **Step 4: Run API tests**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit web API client**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web channel business api client"
```

---

## Task 8: Implement `/channels-biz` page

**Files:**
- Modify: `web/src/pages/channels-biz/page.tsx`
- Create: `web/src/pages/channels-biz/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Add tests for:

- initial list load.
- keyword search and type filter.
- load more.
- detail drawer opens and loads detail.
- create/update dialog posts metadata and refreshes list/detail.
- member tab loads subscribers/allowlist/denylist.
- add members posts normalized textarea values.
- remove one member posts remove action.
- person-channel subscriber add is disabled or hidden.
- 403 and 503 errors map to `ResourceState` variants.

- [ ] **Step 2: Run tests and verify failure**

Run: `cd web && bun run test -- src/pages/channels-biz/page.test.tsx`

Expected: FAIL because page is still placeholder.

- [ ] **Step 3: Replace placeholder with real page**

Reuse existing web components:

- `PageContainer`, `PageHeader`, `SectionCard`
- `ResourceState`, `DetailSheet`, `ActionFormDialog`, `ConfirmDialog`
- `StatusBadge`, `Button`

Implement state for:

- list loading/search/filter/pagination.
- selected channel detail.
- active member list tab and member page state.
- upsert dialog.
- add members dialog.
- remove member confirm dialog.

Keep layout mobile-safe with existing table overflow patterns.

- [ ] **Step 4: Add i18n strings**

Add English and Chinese copy under `channelsBiz.*` for title, descriptions, table headings, flags, dialogs, member tabs, and errors.

- [ ] **Step 5: Run page tests**

Run: `cd web && bun run test -- src/pages/channels-biz/page.test.tsx`

Expected: PASS.

- [ ] **Step 6: Run focused web tests**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts src/pages/channels-biz/page.test.tsx`

Expected: PASS.

- [ ] **Step 7: Commit web page**

```bash
git add web/src/pages/channels-biz/page.tsx web/src/pages/channels-biz/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: implement channels business admin page"
```

---

## Task 9: Update docs and flow notes

**Files:**
- Modify: `web/README.md`
- Modify: `docs/raw/web-admin-restructure.md`
- Modify if needed: `internal/FLOW.md`
- Modify if needed: `pkg/slot/FLOW.md`

- [ ] **Step 1: Update user-facing docs**

Update `web/README.md` matrix:

- `/channels-biz` implemented with manager channel APIs.
- `/system-users` remains placeholder.

Update `docs/raw/web-admin-restructure.md`:

- Mark channel business MVP implemented.
- Leave remove-all/import/bulk operations as follow-ups.

- [ ] **Step 2: Update FLOW docs if stale**

If public package capability descriptions are now stale:

- Add `Store.ScanChannelsSlotPage` to `pkg/slot/FLOW.md` store API list and RPC service notes if needed.
- Add manager channel business API flow note to `internal/FLOW.md` if manager/API flow list is stale.

- [ ] **Step 3: Commit docs**

```bash
git add web/README.md docs/raw/web-admin-restructure.md internal/FLOW.md pkg/slot/FLOW.md
git commit -m "docs: update channels business status"
```

If FLOW files did not require changes, omit them from `git add`.

---

## Task 10: Final verification

**Files:**
- All touched files

- [ ] **Step 1: Run focused Go verification**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/proxy ./internal/usecase/channel ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused web tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/channels-biz/page.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run web build**

Run:

```bash
cd web && bun run build
```

Expected: PASS. If `web/dist/index.html` changes only because of build hashes and generated assets are not part of this feature, revert that generated hash-only change using a non-destructive file edit.

- [ ] **Step 4: Check status and summarize commits**

Run:

```bash
git status --short
git log --oneline -12
```

Expected: worktree clean except any intentionally uncommitted generated files should be explained and fixed before final handoff.

- [ ] **Step 5: Completion handoff**

Use `superpowers:verification-before-completion`, then `superpowers:finishing-a-development-branch` to present merge/PR/keep/discard options after verification passes.
