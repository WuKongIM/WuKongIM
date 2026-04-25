# Manager Channel Runtime Meta List Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a JWT-protected `GET /manager/channel-runtime-meta` endpoint that returns a truly paginated, authority-sourced channel runtime meta list ordered by `slot_id`, `channel_id`, and `channel_type`.

**Architecture:** Build pagination from the bottom up: first add per-hash-slot page scanning in `pkg/slot/meta`, then add per-physical-slot authoritative page scanning plus k-way merge in `pkg/slot/proxy`, and finally add manager usecase / HTTP layers that paginate across physical slots without ever fetching the full dataset. Keep control-plane manager reads on controller-leader strict APIs, while channel runtime meta reads continue to use slot-leader authority via `app.store`.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `pkg/slot/meta`, `pkg/slot/proxy`, `pkg/cluster`, `internal/usecase/management`, `internal/access/manager`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- `pkg/slot/FLOW.md` and `pkg/cluster/FLOW.md` already exist and must be kept aligned.
- Add English comments on new exported methods, DTOs, cursors, and non-obvious helpers to satisfy `AGENTS.md`.

## File Structure

- Create: `pkg/slot/meta/channel_runtime_meta_page.go` — per-hash-slot cursor, validation, and paginated scan.
- Create: `pkg/slot/meta/channel_runtime_meta_page_test.go` — paging tests for one hash slot.
- Modify: `pkg/slot/meta/channel_runtime_meta.go` — reuse existing key helpers when needed, keep exports cohesive.
- Modify: `pkg/slot/FLOW.md` — document channel runtime meta paging behavior.
- Modify: `pkg/cluster/api.go` — expose `HashSlotsOf(slotID)` on the cluster API boundary.
- Modify: `pkg/cluster/cluster.go` — implement `HashSlotsOf(slotID)` by delegating to the router.
- Modify: `pkg/cluster/FLOW.md` — document the new read helper boundary.
- Modify: `pkg/slot/proxy/runtime_meta_rpc.go` — add authoritative `scan_page` RPC and slot-level k-way merge.
- Modify: `pkg/slot/proxy/store.go` — expose slot-page scan entrypoint used by manager usecases.
- Modify: `pkg/slot/proxy/integration_test.go` — authoritative paging tests across local/remote slot leaders.
- Modify: `internal/usecase/management/app.go` — inject a dedicated channel-runtime-meta reader alongside the existing cluster reader.
- Create: `internal/usecase/management/channel_runtime_meta.go` — manager request/response DTOs, cursor, status mapping, cross-slot pagination.
- Create: `internal/usecase/management/channel_runtime_meta_test.go` — usecase tests for cross-slot paging and status mapping.
- Modify: `internal/access/manager/server.go` — expand the management dependency interface with channel runtime meta listing.
- Modify: `internal/access/manager/routes.go` — add `/manager/channel-runtime-meta` under `cluster.channel:r`.
- Create: `internal/access/manager/channel_runtime_meta.go` — query parsing, cursor codec, DTOs, and handler.
- Modify: `internal/access/manager/server_test.go` — HTTP contract tests for auth, cursor, paging, and `503` behavior.
- Modify: `internal/app/build.go` — pass `app.store` into the management usecase as the channel runtime meta reader.
- Modify: `internal/app/lifecycle_test.go` — extend build coverage if wiring needs explicit assertion.
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` — only if implementation details drift.

### Task 1: Add per-hash-slot paging in `pkg/slot/meta`

**Files:**
- Create: `pkg/slot/meta/channel_runtime_meta_page.go`
- Create: `pkg/slot/meta/channel_runtime_meta_page_test.go`
- Modify: `pkg/slot/meta/channel_runtime_meta.go`
- Modify: `pkg/slot/FLOW.md`

- [ ] **Step 1: Write the failing shard paging tests**

Add focused tests for a single hash slot scan, for example:

```go
func TestShardListChannelRuntimeMetaPageReturnsOrderedPage(t *testing.T) { /* channel_id/channel_type ascending */ }
func TestShardListChannelRuntimeMetaPageContinuesAfterCursor(t *testing.T) { /* after cursor resumes after last emitted row */ }
func TestShardListChannelRuntimeMetaPageRejectsInvalidLimit(t *testing.T) { /* limit <= 0 should fail */ }
```

Use real `ShardStore` data so the tests verify Pebble key ordering instead of mocks.

- [ ] **Step 2: Run the focused shard paging tests to verify they fail**

Run:

```bash
go test ./pkg/slot/meta -run 'TestShardListChannelRuntimeMetaPage' -count=1
```

Expected: FAIL because the cursor type and paging method do not exist yet.

- [ ] **Step 3: Implement the minimal shard paging primitive**

Add a cursor like:

```go
type ChannelRuntimeMetaCursor struct {
    ChannelID   string
    ChannelType int64
}
```

Implement:

```go
func (s *ShardStore) ListChannelRuntimeMetaPage(ctx context.Context, after ChannelRuntimeMetaCursor, limit int) ([]ChannelRuntimeMeta, ChannelRuntimeMetaCursor, bool, error)
```

Rules:
- Validate cursor contents and `limit`.
- Iterate only the current shard's primary-key range.
- Order by `channel_id ASC`, then `channel_type ASC`.
- Read `limit+1` records to determine `done` without scanning the full shard.
- Return the last emitted cursor when records are present; otherwise return the input cursor.
- Add English comments for the exported cursor and method.

- [ ] **Step 4: Re-run the focused shard paging tests to verify they pass**

Run:

```bash
go test ./pkg/slot/meta -run 'TestShardListChannelRuntimeMetaPage' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update `pkg/slot/FLOW.md` and commit the shard paging slice**

Document that channel runtime meta now supports page scans per hash slot.

```bash
git add pkg/slot/meta/channel_runtime_meta.go pkg/slot/meta/channel_runtime_meta_page.go pkg/slot/meta/channel_runtime_meta_page_test.go pkg/slot/FLOW.md
git commit -m "feat: add channel runtime meta shard paging"
```

### Task 2: Add authoritative physical-slot paging in `pkg/cluster` and `pkg/slot/proxy`

**Files:**
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `pkg/slot/proxy/runtime_meta_rpc.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/integration_test.go`
- Modify: `pkg/slot/FLOW.md`

- [ ] **Step 1: Write the failing cluster/proxy paging tests**

Add tests that lock the new slot-level paging contract. Cover at least:

```go
func TestClusterHashSlotsOfReturnsAssignedHashSlots(t *testing.T) { /* public API forwards router assignment */ }
func TestStoreScanChannelRuntimeMetaSlotPageReadsAuthoritativeSlot(t *testing.T) { /* remote slot leader returns first page */ }
func TestStoreScanChannelRuntimeMetaSlotPageMergesHashSlotsInChannelOrder(t *testing.T) { /* multiple hash slots merge by channel_id/channel_type */ }
```

Use the existing multi-node integration helpers in `pkg/slot/proxy/integration_test.go` so the scan verifies slot-leader authority instead of local fallback.

- [ ] **Step 2: Run the focused cluster/proxy tests to verify they fail**

Run:

```bash
go test ./pkg/cluster ./pkg/slot/proxy -run 'Test(ClusterHashSlotsOf|StoreScanChannelRuntimeMetaSlotPage)' -count=1
```

Expected: FAIL because the public API method and slot-page scan path do not exist yet.

- [ ] **Step 3: Implement the minimal authoritative slot-page scan**

Expose the new cluster helper:

```go
HashSlotsOf(slotID multiraft.SlotID) []uint16
```

Add a proxy entrypoint similar to:

```go
func (s *Store) ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
```

Implementation rules:
- Keep `ListChannelRuntimeMeta` unchanged for existing callers; add a new page scan instead of changing old behavior first.
- Add a new runtime-meta RPC op such as `scan_page`.
- On the authoritative slot leader, gather `hashSlots := s.cluster.HashSlotsOf(slotID)` and page each shard incrementally.
- Build the slot page with a k-way merge: seed one candidate per hash slot, pop the smallest `(channel_id, channel_type)`, then refill only that hash slot.
- Never call `ListChannelRuntimeMeta` to build a page.
- If the slot is not bootstrapped yet and the authoritative read returns `ErrSlotNotFound`, keep the existing fail-closed semantics used by manager reads (do not silently return a fake page unless the existing authoritative contract already does so for that exact case).
- Add English comments to new exported methods and cursor-bearing RPC types.

- [ ] **Step 4: Re-run the focused cluster/proxy tests to verify they pass**

Run:

```bash
go test ./pkg/cluster ./pkg/slot/proxy -run 'Test(ClusterHashSlotsOf|StoreScanChannelRuntimeMetaSlotPage)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update `pkg/cluster/FLOW.md`, `pkg/slot/FLOW.md`, and commit the authoritative paging slice**

Document the new API helper and slot-leader paging path.

```bash
git add pkg/cluster/api.go pkg/cluster/cluster.go pkg/cluster/FLOW.md pkg/slot/proxy/runtime_meta_rpc.go pkg/slot/proxy/store.go pkg/slot/proxy/integration_test.go pkg/slot/FLOW.md
git commit -m "feat: add channel runtime meta authoritative paging"
```

### Task 3: Add manager usecase pagination for channel runtime meta

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/channel_runtime_meta.go`
- Create: `internal/usecase/management/channel_runtime_meta_test.go`

- [ ] **Step 1: Write the failing management usecase tests**

Add focused tests for cross-slot pagination, for example:

```go
func TestListChannelRuntimeMetaReturnsFirstPageInGlobalOrder(t *testing.T) { /* slot_id, channel_id, channel_type */ }
func TestListChannelRuntimeMetaContinuesWithNextCursorAcrossSlots(t *testing.T) { /* slot boundary transition */ }
func TestListChannelRuntimeMetaMapsStableStatusStrings(t *testing.T) { /* creating/active/deleting/deleted/unknown */ }
func TestListChannelRuntimeMetaPropagatesAuthoritativeErrors(t *testing.T) { /* slot reader failure -> return error */ }
```

Use a fake slot reader that returns per-slot pages; do not hide ordering behavior behind opaque mocks.

- [ ] **Step 2: Run the focused management usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestListChannelRuntimeMeta' -count=1
```

Expected: FAIL because the request/response DTOs and usecase method do not exist yet.

- [ ] **Step 3: Implement the minimal cross-slot pagination usecase**

Add a dedicated reader boundary and manager DTOs, e.g.:

```go
type ChannelRuntimeMetaReader interface {
    ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
}

type ListChannelRuntimeMetaRequest struct {
    Limit  int
    Cursor ChannelRuntimeMetaListCursor
}
```

Rules:
- Keep the existing `ClusterReader` focused on controller-side strict reads.
- Add the channel-runtime reader as a separate dependency in `Options` / `App`.
- The usecase must paginate across `cluster.SlotIDs()` in ascending slot order.
- For the current slot, pass the decoded per-slot cursor; for later slots, start with an empty shard cursor.
- Stop as soon as `limit` items are collected; compute `HasMore` / `NextCursor` from the last emitted item and the slot scan result.
- Map `metadb.ChannelRuntimeMeta.Status` to stable strings using `pkg/channel` status constants, with `unknown` as the fallback.
- Add English comments on exported request/response/cursor types.

- [ ] **Step 4: Re-run the focused management usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestListChannelRuntimeMeta' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the management usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/channel_runtime_meta.go internal/usecase/management/channel_runtime_meta_test.go
git commit -m "feat: add manager channel runtime meta usecase"
```

### Task 4: Add `GET /manager/channel-runtime-meta`

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/channel_runtime_meta.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP contract tests**

Extend `internal/access/manager/server_test.go` with at least:

```go
func TestManagerChannelRuntimeMetaRejectsMissingToken(t *testing.T) { /* expect 401 */ }
func TestManagerChannelRuntimeMetaRejectsInvalidLimit(t *testing.T) { /* expect 400 */ }
func TestManagerChannelRuntimeMetaRejectsInvalidCursor(t *testing.T) { /* expect 400 */ }
func TestManagerChannelRuntimeMetaRejectsInsufficientPermission(t *testing.T) { /* expect 403 for cluster.channel:r */ }
func TestManagerChannelRuntimeMetaReturnsFirstPage(t *testing.T) { /* items + next_cursor */ }
func TestManagerChannelRuntimeMetaReturnsSecondPageFromCursor(t *testing.T) { /* cursor resume */ }
func TestManagerChannelRuntimeMetaReturnsServiceUnavailableWhenAuthorityUnavailable(t *testing.T) { /* expect 503 */ }
```

- [ ] **Step 2: Run the focused manager HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerChannelRuntimeMeta' -count=1
```

Expected: FAIL because the route, handler, and cursor codec do not exist yet.

- [ ] **Step 3: Implement the minimal manager HTTP endpoint**

Update the management dependency interface in `internal/access/manager/server.go`:

```go
ListChannelRuntimeMeta(ctx context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ChannelRuntimeMetaPage, error)
```

Add route wiring in `internal/access/manager/routes.go` under the new permission group:

```go
channels := s.engine.Group("/manager")
channels.Use(s.requirePermission("cluster.channel", "r"))
channels.GET("/channel-runtime-meta", s.handleChannelRuntimeMeta)
```

Implement in `internal/access/manager/channel_runtime_meta.go`:
- request parsing for `limit` and `cursor`
- base64(JSON) cursor encode/decode with version `v=1`
- manager response DTOs
- `400` mapping for invalid `limit` / invalid `cursor`
- `503` mapping for slot-leader authority failures
- no `total` field in the response

- [ ] **Step 4: Re-run the focused manager HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerChannelRuntimeMeta' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the manager HTTP slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/channel_runtime_meta.go internal/access/manager/server_test.go
git commit -m "feat: add manager channel runtime meta endpoint"
```

### Task 5: Wire the app, run focused verification, and align docs

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle_test.go` (only if an explicit wiring assertion is needed)
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` (only if implementation details drift)

- [ ] **Step 1: Write or extend the failing app wiring test if compile coverage is not enough**

Prefer extending an existing build/lifecycle test only if you need an explicit assertion that the management usecase receives the new reader dependency.

Candidate test shape:

```go
func TestNewBuildsManagerWithChannelRuntimeMetaReader(t *testing.T) { /* manager build path still succeeds with new dependency */ }
```

If the existing manager build tests already exercise the wiring sufficiently once the code compiles, document that in the implementation and skip adding a redundant new test.

- [ ] **Step 2: Run the focused build / lifecycle tests to verify the wiring state**

Run:

```bash
go test ./internal/app -run 'Test(NewBuildsOptionalManagerServerWhenConfigured|NewBuildsManagerWithChannelRuntimeMetaReader|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose)' -count=1
```

Expected: PASS if no new test was needed, or FAIL first if you added a new explicit test before wiring.

- [ ] **Step 3: Implement the app wiring and re-run the focused app tests**

Wire `app.store` into `managementusecase.New(...)` as the channel runtime meta reader without changing manager lifecycle semantics.

Re-run:

```bash
go test ./internal/app -run 'Test(NewBuildsOptionalManagerServerWhenConfigured|NewBuildsManagerWithChannelRuntimeMetaReader|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose)' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run the focused end-to-end verification suite**

Run:

```bash
go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./pkg/cluster ./pkg/slot/meta ./pkg/slot/proxy -run 'Test(LoadConfig.*Manager|Config.*Manager|Manager(Login|Nodes|Slots|SlotDetail|Tasks|TaskDetail|ChannelRuntimeMeta)|List(Nodes|Slots|Tasks|ChannelRuntimeMeta)|Get(Task|Slot)|NewBuildsOptionalManagerServerWhenConfigured|NewBuildsManagerWithChannelRuntimeMetaReader|StartStartsManagerAfterAPIWhenEnabled|StartRollsBackAPIAndClusterWhenManagerStartFails|StopStopsManagerBeforeAPIGatewayAndClusterClose|AccessorsExposeBuiltRuntime|ClusterHashSlotsOf|ShardListChannelRuntimeMetaPage|StoreScanChannelRuntimeMetaSlotPage|List(Nodes|ObservedRuntimeViews|SlotAssignments|Tasks)Strict|GetReconcileTaskStrict)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run a grep-based contract check and commit any remaining doc alignment**

Run:

```bash
rg -n '/manager/channel-runtime-meta|cluster\.channel|next_cursor|HashSlotsOf\(|ScanChannelRuntimeMetaSlotPage\(|ListChannelRuntimeMetaPage\(|invalid cursor|invalid limit' cmd internal pkg docs -S
```

Expected: the route, permission, cursor/paging helpers, and error semantics appear in the intended files.

If implementation drift required doc-only changes beyond the feature commits:

```bash
git add docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md
git commit -m "docs: align manager channel runtime meta contract"
```

If no further files changed, skip this commit.
