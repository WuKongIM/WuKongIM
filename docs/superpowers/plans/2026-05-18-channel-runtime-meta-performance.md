# Channel Runtime Meta Performance Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `/manager/channel-runtime-meta` fast and semantically correct for large channel counts by honoring `node_id`, filtering by node scope, and disabling per-row `max_message_seq` enrichment by default.

**Architecture:** Keep the first pass in the manager access/usecase/app adapter layers without changing metadb layout or slot RPC formats. The list usecase scans existing authoritative pages, applies optional node filtering, and enriches max sequence only when explicitly requested. Detail reads keep exact max sequence behavior.

**Tech Stack:** Go, Gin manager HTTP handlers, WuKongIM management usecase, slot proxy runtime meta scans, channel store checkpoint reads, testify tests.

---

## File Structure

- Modify `internal/usecase/management/channel_runtime_meta.go`: add request fields, node filtering, optional max sequence enrichment, and optional `MaxMessageSeq` DTO field.
- Modify `internal/usecase/management/app.go`: extend `MessageReader` with a meta-aware max sequence method or add a narrow optional interface used by channel runtime meta list.
- Modify `internal/app/manager_messages.go`: add `MaxMessageSeqForMeta` helper to reuse already-scanned runtime meta.
- Modify `internal/access/manager/channel_runtime_meta.go`: parse `node_id`, `node_scope`, `include_max_message_seq`; make response `max_message_seq` optional.
- Modify `internal/access/manager/server_test.go`: handler parsing and JSON behavior tests.
- Modify `internal/usecase/management/channel_runtime_meta_test.go`: node filter, default no-enrichment, include enrichment tests.
- Modify `internal/app/manager_messages_test.go`: meta-aware max sequence adapter tests.

## Task 1: Access Layer Query Parsing and Optional DTO Field

**Files:**
- Modify: `internal/access/manager/channel_runtime_meta.go`
- Test: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing access tests**

Add tests that verify:

```go
func TestManagerChannelRuntimeMetaPassesNodeFiltersAndIncludeMaxSeq(t *testing.T) {
    var received managementusecase.ListChannelRuntimeMetaRequest
    srv := New(Options{Auth: testAuthConfig(...), Management: managementStub{channelRuntimeMetaReqSink: &received}})
    req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?node_id=1&node_scope=replica&include_max_message_seq=true", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
    srv.Engine().ServeHTTP(rec, req)
    require.Equal(t, uint64(1), received.NodeID)
    require.Equal(t, managementusecase.ChannelRuntimeMetaNodeScopeReplica, received.NodeScope)
    require.True(t, received.IncludeMaxMessageSeq)
}
```

Also add invalid cases for `node_id=0`, `node_id=bad`, invalid `node_scope`, and invalid `include_max_message_seq`.

Add response tests:

- default list response omits `max_message_seq` when `MaxMessageSeq == nil`.
- list response includes `max_message_seq` when pointer is set.

- [ ] **Step 2: Run failing access tests**

Run: `go test ./internal/access/manager -run 'TestManagerChannelRuntimeMeta'`

Expected: FAIL because request fields/constants/parsers do not exist or JSON still emits zero max seq.

- [ ] **Step 3: Implement access parsing and DTO changes**

In `internal/access/manager/channel_runtime_meta.go`:

- Change `ChannelRuntimeMetaDTO.MaxMessageSeq` from `uint64` to `*uint64` with `omitempty`.
- Add parsers:

```go
func parseOptionalChannelRuntimeMetaNodeID(raw string) (uint64, error)
func parseChannelRuntimeMetaNodeScope(raw string, nodeID uint64) (managementusecase.ChannelRuntimeMetaNodeScope, error)
func parseOptionalBoolQuery(raw string) (bool, error)
```

- Pass fields to `ListChannelRuntimeMetaRequest`.
- Return HTTP 400 for invalid values.

- [ ] **Step 4: Run access tests**

Run: `go test ./internal/access/manager -run 'TestManagerChannelRuntimeMeta'`

Expected: PASS.

## Task 2: Usecase Node Filtering and Default No Max Sequence

**Files:**
- Modify: `internal/usecase/management/channel_runtime_meta.go`
- Modify: `internal/usecase/management/app.go`
- Test: `internal/usecase/management/channel_runtime_meta_test.go`

- [ ] **Step 1: Write failing usecase tests**

Add constants and fields expected by access tests:

```go
type ChannelRuntimeMetaNodeScope string
const (
    ChannelRuntimeMetaNodeScopeAny ChannelRuntimeMetaNodeScope = "any"
    ChannelRuntimeMetaNodeScopeLeader ChannelRuntimeMetaNodeScope = "leader"
    ChannelRuntimeMetaNodeScopeReplica ChannelRuntimeMetaNodeScope = "replica"
    ChannelRuntimeMetaNodeScopeISR ChannelRuntimeMetaNodeScope = "isr"
)
```

Add tests:

- `TestListChannelRuntimeMetaFiltersByNodeScope` covers all scopes.
- `TestListChannelRuntimeMetaFilteredScanContinuesUntilLimit` verifies filtering continues across pages/slots.
- `TestListChannelRuntimeMetaDoesNotReadMaxSeqByDefault` uses a fake message reader that fails if called.
- `TestListChannelRuntimeMetaIncludesMaxSeqWhenRequested` verifies pointer values are set only when requested.

- [ ] **Step 2: Run failing usecase tests**

Run: `go test ./internal/usecase/management -run 'TestListChannelRuntimeMeta'`

Expected: FAIL because fields/filtering/optional max seq are not implemented.

- [ ] **Step 3: Implement usecase filtering and optional enrichment**

In `internal/usecase/management/channel_runtime_meta.go`:

- Add `NodeID`, `NodeScope`, `IncludeMaxMessageSeq` to `ListChannelRuntimeMetaRequest`.
- Change `ChannelRuntimeMeta.MaxMessageSeq` to `*uint64`.
- Add helpers:

```go
func normalizeChannelRuntimeMetaNodeScope(scope ChannelRuntimeMetaNodeScope, nodeID uint64) (ChannelRuntimeMetaNodeScope, error)
func channelRuntimeMetaMatchesNode(meta metadb.ChannelRuntimeMeta, nodeID uint64, scope ChannelRuntimeMetaNodeScope) bool
func containsNodeID(values []uint64, nodeID uint64) bool
```

- Update list loop so it scans until it has `Limit` matching rows or reaches end.
- Update `managerChannelRuntimeMetaItems` to accept `includeMaxSeq bool` and skip `channelMaxMessageSeq` unless true.
- Ensure `findNextChannelRuntimeMetaSlotWithData` remains usable for unfiltered lists; filtered lists should not rely on unfiltered next-slot detection if it can produce misleading cursors.

In `internal/usecase/management/app.go`:

- Add optional meta-aware interface if needed:

```go
type MessageMetaMaxSeqReader interface {
    MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error)
}
```

- Use it when available; otherwise fall back to `MaxMessageSeq`.

- [ ] **Step 4: Run usecase tests**

Run: `go test ./internal/usecase/management -run 'TestListChannelRuntimeMeta'`

Expected: PASS.

## Task 3: App Adapter Meta-Aware Max Sequence

**Files:**
- Modify: `internal/app/manager_messages.go`
- Test: `internal/app/manager_messages_test.go`

- [ ] **Step 1: Write failing adapter tests**

Add tests for `managerMessageReader.MaxMessageSeqForMeta`:

- local leader reads `channelhandler.LoadCommittedHW` without calling `metas.GetChannelRuntimeMeta`.
- remote leader calls `QueryChannelMessages` with `MaxSeqOnly=true` without calling `metas.GetChannelRuntimeMeta`.
- zero leader returns `raftcluster.ErrNoLeader`.

Use existing fake/capture types where possible; add a fake meta getter that records calls and assert no calls happen.

- [ ] **Step 2: Run failing adapter tests**

Run: `go test ./internal/app -run 'TestManagerMessage.*MaxMessageSeq'`

Expected: FAIL because method does not exist.

- [ ] **Step 3: Implement `MaxMessageSeqForMeta`**

In `internal/app/manager_messages.go`:

```go
func (r managerMessageReader) MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error) {
    if r.channelLog == nil {
        return 0, nil
    }
    if meta.Leader == 0 {
        return 0, raftcluster.ErrNoLeader
    }
    id := channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
    if meta.Leader == r.localNodeID {
        return channelhandler.LoadCommittedHW(r.channelLog, id)
    }
    if r.remote == nil {
        return 0, channel.ErrStaleMeta
    }
    page, err := r.remote.QueryChannelMessages(ctx, meta.Leader, accessnode.ChannelMessagesQuery{ChannelID: id, MaxSeqOnly: true})
    if err != nil {
        return 0, err
    }
    return page.MaxMessageSeq, nil
}
```

Refactor `MaxMessageSeq` to read meta once and delegate to `MaxMessageSeqForMeta`.

- [ ] **Step 4: Run adapter tests**

Run: `go test ./internal/app -run 'TestManagerMessage.*MaxMessageSeq'`

Expected: PASS.

## Task 4: Integration of Access + Usecase Types

**Files:**
- Modify as needed based on compile failures.
- Test: `internal/access/manager`, `internal/usecase/management`, `internal/app`.

- [ ] **Step 1: Run package tests together**

Run: `go test ./internal/access/manager ./internal/usecase/management ./internal/app`

Expected: may FAIL with compile errors in tests or DTO comparisons after `MaxMessageSeq` becomes optional.

- [ ] **Step 2: Fix compile/test fallout**

Adjust expected structs and JSON in tests:

- Use `uint64Ptr(42)` helper where expected max seq is present.
- Remove expected max seq from default list JSON.
- Keep detail JSON expecting `max_message_seq`.

- [ ] **Step 3: Re-run package tests**

Run: `go test ./internal/access/manager ./internal/usecase/management ./internal/app`

Expected: PASS.

## Task 5: Final Targeted Verification

**Files:**
- No planned source changes.

- [ ] **Step 1: Run focused tests**

Run: `go test ./internal/access/manager ./internal/usecase/management ./internal/app ./pkg/slot/proxy ./pkg/slot/meta`

Expected: PASS.

- [ ] **Step 2: Inspect git diff**

Run: `git diff -- internal/access/manager/channel_runtime_meta.go internal/usecase/management/channel_runtime_meta.go internal/usecase/management/app.go internal/app/manager_messages.go internal/access/manager/server_test.go internal/usecase/management/channel_runtime_meta_test.go internal/app/manager_messages_test.go`

Expected: Diff only contains planned changes.

- [ ] **Step 3: Commit only related files if requested**

Do not commit unless the user explicitly asks. The worktree has unrelated pre-existing changes.
