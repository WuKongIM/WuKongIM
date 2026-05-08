# Manager Message History Deletion Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manager/web operation that deletes channel history by advancing the cluster-authoritative channel retention boundary through a selected `message_seq`.

**Architecture:** The manager HTTP route validates operator input and delegates to management usecases. The app layer performs leader-aware retention-boundary planning, confirms the committed replay cursor durably, advances slot metadata, and applies the runtime boundary; remote channel leaders are reached through node RPC. The web messages page exposes a destructive confirmation action and refreshes the current query after success.

**Tech Stack:** Go, Gin, WuKongIM channel runtime/store, slot metadata proxy, node RPC binary codecs, React, TypeScript, Vitest, React Testing Library.

---

## Preflight and Current Workspace Notes

- The approved design is `docs/superpowers/specs/2026-05-08-manager-message-history-deletion-design.md`.
- Package-local `FLOW.md` files were checked for the target packages and none were found under `internal/access/manager`, `internal/usecase/management`, `internal/app`, `internal/access/node`, or `web/src`.
- The workspace already has unrelated dirty retention-worker edits in `internal/app/build.go`, `internal/app/channelretention.go`, `internal/runtime/channelretention/worker.go`, and `internal/runtime/channelretention/worker_test.go`. Do not revert them. When committing this feature, stage only the files named in the current task unless those existing changes are intentionally included by the user.
- Use "单节点集群" terminology in docs/comments when deployment shape is mentioned. This feature should not introduce any single-node bypass path.

## File Structure

### Backend usecase and access

- Modify: `internal/usecase/management/app.go`
  - Add a separate `MessageRetentionOperator` port to `Options` and `App` so message reads stay separate from destructive message-retention operations.
- Modify: `internal/usecase/management/messages.go`
  - Add request/response DTOs, status constants, blocked reason constants, and `AdvanceMessageRetention` usecase method.
- Modify/Test: `internal/usecase/management/messages_test.go`
  - Add TDD coverage for validation, delegation, response mapping, and reader nil behavior.
- Modify: `internal/access/manager/server.go`
  - Add `AdvanceMessageRetention` to the `Management` interface with English comment.
- Modify: `internal/access/manager/routes.go`
  - Add `POST /manager/messages/retention` under `cluster.channel:w`.
- Modify: `internal/access/manager/messages.go`
  - Add request/response DTOs and handler.
- Modify/Test: `internal/access/manager/messages_test.go`
  - Add route, auth, validation, success, and error-mapping tests.
- Modify: `internal/access/manager/server_test.go`
  - Extend `managementStub` with the new method and capture fields.

### Backend app/runtime and node RPC

- Create: `internal/app/manager_message_retention.go`
  - Implement leader-aware local/remote retention advance orchestration.
- Modify/Test: `internal/app/manager_messages_test.go` or create `internal/app/manager_message_retention_test.go`
  - Cover local leader, dry-run, blocked gates, remote forwarding, stale metadata, and no-leader cases.
- Modify: `internal/app/build.go`
  - Wire the new retention operator into management usecase and node access.
- Modify: `internal/access/node/service_ids.go`
  - Add a new RPC service ID after current diagnostics service ID.
- Create: `internal/access/node/channel_retention_rpc.go`
  - Add client/server RPC surface for channel retention advance.
- Create: `internal/access/node/channel_retention_codec.go`
  - Add binary request/response codec following `channel_messages_codec.go` patterns.
- Create/Test: `internal/access/node/channel_retention_rpc_test.go`
  - Cover codec round trip, leader-only behavior, no-leader/not-leader statuses, and client retry.
- Modify: `internal/access/node/options.go`
  - Add `ChannelRetention` provider option and register the new service handler.

### Web

- Modify: `web/src/lib/manager-api.types.ts`
  - Add input/response/status/blocked-reason types.
- Modify: `web/src/lib/manager-api.ts`
  - Add `advanceMessageRetention` API helper.
- Modify/Test: `web/src/lib/manager-api.test.ts`
  - Verify request URL, method, JSON body, and response mapping.
- Modify: `web/src/pages/messages/page.tsx`
  - Add destructive row action, confirmation dialog, pending/error/success state, and refresh after success.
- Modify/Test: `web/src/pages/messages/page.test.tsx`
  - Mock `advanceMessageRetention` and verify confirmation, success refresh, blocked/error rendering.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English strings.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese strings.

### Docs

- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Add one concise note: manager history deletion must advance `RetentionThroughSeq`; it must not directly delete message rows.

---

### Task 1: Management Usecase Contract

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/messages.go`
- Modify: `internal/usecase/management/messages_test.go`

- [ ] **Step 1: Write failing validation and delegation tests**

Add tests near existing message tests:

```go
func TestAdvanceMessageRetentionRejectsInvalidRequest(t *testing.T) {
    app := New(Options{})

    _, err := app.AdvanceMessageRetention(context.Background(), AdvanceMessageRetentionRequest{
        ChannelID: "room-1",
        ChannelType: 2,
    })

    require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestAdvanceMessageRetentionDelegatesToPort(t *testing.T) {
    operator := &fakeMessageRetentionOperator{
        result: AdvanceMessageRetentionResponse{
            ChannelID: "room-1",
            ChannelType: 2,
            RequestedThroughSeq: 10,
            AdvancedThroughSeq: 8,
            MinAvailableSeq: 9,
            Status: MessageRetentionStatusAdvanced,
        },
    }
    app := New(Options{MessageRetention: operator})

    got, err := app.AdvanceMessageRetention(context.Background(), AdvanceMessageRetentionRequest{
        ChannelID: "room-1",
        ChannelType: 2,
        ThroughSeq: 10,
        DryRun: true,
    })

    require.NoError(t, err)
    require.Equal(t, AdvanceMessageRetentionRequest{ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10, DryRun: true}, operator.lastReq)
    require.Equal(t, operator.result, got)
}
```

Add a fake:

```go
type fakeMessageRetentionOperator struct {
    lastReq AdvanceMessageRetentionRequest
    result AdvanceMessageRetentionResponse
    err error
}

func (f *fakeMessageRetentionOperator) AdvanceMessageRetention(_ context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error) {
    f.lastReq = req
    return f.result, f.err
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/usecase/management -run 'TestAdvanceMessageRetention' -count=1`

Expected: FAIL because `AdvanceMessageRetentionRequest`, `Options.MessageRetention`, or `App.AdvanceMessageRetention` is undefined.

- [ ] **Step 3: Implement minimal usecase contract**

In `internal/usecase/management/app.go`, add:

```go
// MessageRetentionOperator exposes destructive channel message retention operations.
type MessageRetentionOperator interface {
    // AdvanceMessageRetention advances one channel's history retention boundary.
    AdvanceMessageRetention(ctx context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error)
}
```

Add to `Options` and `App`:

```go
// MessageRetention provides destructive channel message retention operations.
MessageRetention MessageRetentionOperator
```

```go
messageRetention MessageRetentionOperator
```

Wire it in `New`.

In `internal/usecase/management/messages.go`, add English-commented DTOs and constants:

```go
type MessageRetentionStatus string

const (
    MessageRetentionStatusAdvanced MessageRetentionStatus = "advanced"
    MessageRetentionStatusWouldAdvance MessageRetentionStatus = "would_advance"
    MessageRetentionStatusNoop MessageRetentionStatus = "noop"
    MessageRetentionStatusBlocked MessageRetentionStatus = "blocked"
)

type MessageRetentionBlockedReason string

const (
    MessageRetentionBlockedReasonNone MessageRetentionBlockedReason = ""
    MessageRetentionBlockedReasonReplayCursor MessageRetentionBlockedReason = "replay_cursor"
    MessageRetentionBlockedReasonMinISRMatchOffset MessageRetentionBlockedReason = "min_isr_match_offset"
    MessageRetentionBlockedReasonHW MessageRetentionBlockedReason = "hw"
    MessageRetentionBlockedReasonCheckpointHW MessageRetentionBlockedReason = "checkpoint_hw"
    MessageRetentionBlockedReasonCurrentBoundary MessageRetentionBlockedReason = "current_boundary"
)
```

Implement:

```go
func (a *App) AdvanceMessageRetention(ctx context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error) {
    if req.ChannelID == "" || req.ChannelType <= 0 || req.ThroughSeq == 0 {
        return AdvanceMessageRetentionResponse{}, metadb.ErrInvalidArgument
    }
    if a == nil || a.messageRetention == nil {
        return AdvanceMessageRetentionResponse{}, nil
    }
    return a.messageRetention.AdvanceMessageRetention(ctx, req)
}
```

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/usecase/management -run 'TestAdvanceMessageRetention|TestListMessages' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit usecase contract**

```bash
git add internal/usecase/management/app.go internal/usecase/management/messages.go internal/usecase/management/messages_test.go
git commit -m "feat: add manager message retention usecase"
```

---

### Task 2: Manager HTTP Endpoint

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/messages.go`
- Modify: `internal/access/manager/messages_test.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing access tests**

Add tests to `internal/access/manager/messages_test.go`:

```go
func TestManagerMessageRetentionRequiresWritePermission(t *testing.T) {
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{
            Username: "admin",
            Password: "secret",
            Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}},
        }}),
        Management: managementStub{},
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":9}`))
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}
```

Add success and invalid body tests:

```go
func TestManagerMessageRetentionAdvancesBoundary(t *testing.T) {
    var received managementusecase.AdvanceMessageRetentionRequest
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{
            Username: "admin",
            Password: "secret",
            Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}},
        }}),
        Management: managementStub{
            retentionReqSink: &received,
            retentionResult: managementusecase.AdvanceMessageRetentionResponse{
                ChannelID: "room-1",
                ChannelType: 2,
                RequestedThroughSeq: 10,
                AdvancedThroughSeq: 8,
                MinAvailableSeq: 9,
                Status: managementusecase.MessageRetentionStatusAdvanced,
            },
        },
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":10,"dry_run":true}`))
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, managementusecase.AdvanceMessageRetentionRequest{ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10, DryRun: true}, received)
    require.JSONEq(t, `{"channel_id":"room-1","channel_type":2,"requested_through_seq":10,"advanced_through_seq":8,"min_available_seq":9,"status":"advanced"}`, rec.Body.String())
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/access/manager -run 'TestManagerMessageRetention' -count=1`

Expected: FAIL because the route and stub fields do not exist.

- [ ] **Step 3: Add interface, route, DTOs, and handler**

In `server.go`, add to `Management`:

```go
// AdvanceMessageRetention advances one channel's history retention boundary.
AdvanceMessageRetention(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error)
```

In `routes.go`, create a write group after the existing message read route group:

```go
messageWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    messageWrites.Use(s.requirePermission("cluster.channel", "w"))
}
messageWrites.POST("/messages/retention", s.handleAdvanceMessageRetention)
```

In `messages.go`, add JSON DTOs and handler:

```go
type AdvanceMessageRetentionRequestDTO struct {
    ChannelID string `json:"channel_id"`
    ChannelType int64 `json:"channel_type"`
    ThroughSeq uint64 `json:"through_seq"`
    DryRun bool `json:"dry_run"`
}

type AdvanceMessageRetentionResponseDTO struct {
    ChannelID string `json:"channel_id"`
    ChannelType int64 `json:"channel_type"`
    RequestedThroughSeq uint64 `json:"requested_through_seq"`
    AdvancedThroughSeq uint64 `json:"advanced_through_seq"`
    MinAvailableSeq uint64 `json:"min_available_seq"`
    Status managementusecase.MessageRetentionStatus `json:"status"`
    BlockedReason managementusecase.MessageRetentionBlockedReason `json:"blocked_reason,omitempty"`
}
```

Map errors consistently with `handleMessages`; return `200` for usecase responses including `blocked`, and reserve non-2xx responses for malformed requests, auth failures, missing channels, leader unavailability, or unexpected errors.

- [ ] **Step 4: Extend test stub**

In `server_test.go`, add fields to `managementStub`:

```go
retentionReqSink *managementusecase.AdvanceMessageRetentionRequest
retentionResult managementusecase.AdvanceMessageRetentionResponse
retentionErr error
```

Add method:

```go
func (s managementStub) AdvanceMessageRetention(_ context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
    if s.retentionReqSink != nil {
        *s.retentionReqSink = req
    }
    return s.retentionResult, s.retentionErr
}
```

- [ ] **Step 5: Run focused access tests**

Run: `go test ./internal/access/manager -run 'TestManagerMessageRetention|TestManagerMessages' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit access endpoint**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/messages.go internal/access/manager/messages_test.go internal/access/manager/server_test.go
git commit -m "feat: expose manager message retention endpoint"
```

---

### Task 3: App-Layer Local Retention Operator

**Files:**
- Create: `internal/app/manager_message_retention.go`
- Create/Modify: `internal/app/manager_message_retention_test.go` or `internal/app/manager_messages_test.go`

- [ ] **Step 1: Write failing local leader tests**

Create tests for local leader advance and dry-run:

```go
func TestManagerMessageRetentionAdvancesLocalLeader(t *testing.T) {
    id := channel.ChannelID{ID: "retain-local", Type: 2}
    key := channelhandler.KeyFromChannelID(id)
    now := time.Unix(2000, 0)
    runtime := &fakeManagerRetentionRuntime{views: []channel.RetentionView{{
        ChannelKey: key,
        Epoch: 7,
        LeaderEpoch: 8,
        Leader: 1,
        HW: 10,
        CheckpointHW: 10,
        MinISRMatchOffset: 10,
        CommitReady: true,
        LeaseUntil: now.Add(time.Minute),
    }}}
    metadata := &fakeManagerRetentionMetadata{}
    stores := &fakeManagerRetentionStores{cursor: 10}

    op := managerMessageRetentionOperator{
        localNodeID: 1,
        metas: managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1}},
        runtime: runtime,
        stores: stores,
        metadata: metadata,
        now: func() time.Time { return now },
    }

    got, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{ChannelID: id.ID, ChannelType: int64(id.Type), ThroughSeq: 9})

    require.NoError(t, err)
    require.Equal(t, managementusecase.MessageRetentionStatusAdvanced, got.Status)
    require.Equal(t, uint64(9), got.AdvancedThroughSeq)
    require.Equal(t, uint64(10), got.MinAvailableSeq)
    require.Equal(t, uint64(9), metadata.lastReq.RetentionThroughSeq)
    require.Equal(t, []uint64{9}, runtime.applied)
}
```

Add tests for:

- `DryRun: true` returns `would_advance` and does not call metadata/runtime apply.
- `ThroughSeq` above `CheckpointHW` advances only to the safe lower boundary.
- `ThroughSeq` above all gates but replay cursor equals current boundary returns `blocked` with `replay_cursor`.
- `requested <= current RetentionThroughSeq` returns `noop`.
- stale or no leader metadata returns the existing channel/cluster errors.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/app -run 'TestManagerMessageRetention' -count=1`

Expected: FAIL because `managerMessageRetentionOperator` is undefined.

- [ ] **Step 3: Implement focused operator and helpers**

Create `internal/app/manager_message_retention.go` with ports:

```go
type managerMessageRetentionMetas interface {
    GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

type managerMessageRetentionRuntime interface {
    RetentionView(key channel.ChannelKey) (channel.RetentionView, error)
    ApplyRetentionBoundary(ctx context.Context, key channel.ChannelKey, throughSeq uint64) error
}

type managerMessageRetentionStoreProvider interface {
    // LoadCommittedDispatchCursor reads replay progress for dry-run planning without mutating storage.
    LoadCommittedDispatchCursor(ctx context.Context, id channel.ChannelID, cursorName string) (uint64, bool, error)
    // ConfirmCommittedDispatchCursorDurable syncs replay progress before a real metadata advance.
    ConfirmCommittedDispatchCursorDurable(ctx context.Context, id channel.ChannelID, cursorName string, minSeq uint64) (uint64, error)
}

type managerMessageRetentionMetadata interface {
    AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error
}
```

Use `appChannelRetentionCursorName` for the cursor name. If the existing constant is in another file in the same package, reuse it.

Implement `AdvanceMessageRetention`:

1. Convert request to `channel.ChannelID` and `channel.ChannelKey`.
2. Load meta and route remote leaders later in Task 4.
3. Load runtime view.
4. Require local leader and `CommitReady`.
5. For dry-run requests, read the committed replay cursor with `LoadCommittedDispatchCursor` and do not mutate store, metadata, or runtime.
6. For real requests, confirm replay cursor durably with `minSeq = nextSeq(view.RetentionThroughSeq)`.
7. Re-read runtime view after durable cursor confirmation on real requests.
8. Calculate `safeThroughSeq` using requested through seq and gates.
9. Return `noop`, `blocked`, `would_advance`, or `advanced`. Treat `would_advance` as advisory because the real request still performs durable cursor confirmation.
10. For real advances, call `AdvanceChannelRetentionThroughSeq` and `ApplyRetentionBoundary`.

Keep helper functions private and small:

```go
type managerRetentionGate struct {
    seq uint64
    reason managementusecase.MessageRetentionBlockedReason
}

func managerRetentionDecision(requested uint64, current uint64, gates []managerRetentionGate) (through uint64, reason managementusecase.MessageRetentionBlockedReason) {
    through = requested
    reason = managementusecase.MessageRetentionBlockedReasonNone
    for _, gate := range gates {
        if gate.seq < through {
            through = gate.seq
            reason = gate.reason
        }
    }
    if through <= current && reason == managementusecase.MessageRetentionBlockedReasonNone {
        reason = managementusecase.MessageRetentionBlockedReasonCurrentBoundary
    }
    return through, reason
}
```

Add an adapter for real stores:

```go
type managerMessageRetentionStores struct {
    engine *channelstore.Engine
}

func (s managerMessageRetentionStores) LoadCommittedDispatchCursor(ctx context.Context, id channel.ChannelID, cursorName string) (uint64, bool, error) {
    if err := contextError(ctx); err != nil {
        return 0, false, err
    }
    if s.engine == nil {
        return 0, false, channel.ErrInvalidConfig
    }
    st := s.engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
    return st.LoadCommittedDispatchCursor(cursorName)
}

func (s managerMessageRetentionStores) ConfirmCommittedDispatchCursorDurable(ctx context.Context, id channel.ChannelID, cursorName string, minSeq uint64) (uint64, error) {
    if err := contextError(ctx); err != nil {
        return 0, err
    }
    if s.engine == nil {
        return 0, channel.ErrInvalidConfig
    }
    st := s.engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
    return st.ConfirmCommittedDispatchCursorDurable(cursorName, minSeq)
}
```

- [ ] **Step 4: Run local app tests**

Run: `go test ./internal/app -run 'TestManagerMessageRetention' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit local retention operator**

```bash
git add internal/app/manager_message_retention.go internal/app/manager_message_retention_test.go internal/app/manager_messages_test.go
git commit -m "feat: add manager message retention operator"
```

---

### Task 4: Node RPC for Remote Channel Leaders

**Files:**
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/options.go`
- Create: `internal/access/node/channel_retention_rpc.go`
- Create: `internal/access/node/channel_retention_codec.go`
- Create: `internal/access/node/channel_retention_rpc_test.go`
- Modify: `internal/app/manager_message_retention.go`
- Modify/Test: `internal/app/manager_message_retention_test.go`

- [ ] **Step 1: Write failing node RPC tests**

In `internal/access/node/channel_retention_rpc_test.go`, add codec and handler tests:

```go
func TestChannelRetentionBinaryCodecRoundTrip(t *testing.T) {
    req := channelRetentionRequest{Request: ChannelRetentionAdvanceRequest{
        ChannelID: channel.ChannelID{ID: "room-1", Type: 2},
        ThroughSeq: 10,
        DryRun: true,
    }}

    body, err := encodeChannelRetentionRequestBinary(req)
    require.NoError(t, err)
    got, err := decodeChannelRetentionRequest(body)
    require.NoError(t, err)
    require.Equal(t, req, got)
}
```

Add handler test for local provider success and not-leader status. Use the same status pattern as `channel_messages_rpc_test.go`.

- [ ] **Step 2: Run node tests and confirm failure**

Run: `go test ./internal/access/node -run 'TestChannelRetention' -count=1`

Expected: FAIL because RPC types and service are undefined.

- [ ] **Step 3: Add node RPC types and provider option**

In `options.go`, add:

```go
type ChannelRetentionProvider interface {
    AdvanceChannelRetention(ctx context.Context, req ChannelRetentionAdvanceRequest) (ChannelRetentionAdvanceResult, error)
}
```

Add `ChannelRetention ChannelRetentionProvider` to `Options` and `Adapter`, then register the service:

```go
opts.Cluster.RPCMux().Handle(channelRetentionRPCServiceID, adapter.handleChannelRetentionRPC)
```

In `service_ids.go`, add the next unused ID:

```go
channelRetentionRPCServiceID uint8 = 43
```

- [ ] **Step 4: Implement RPC codec and client**

Define access-node DTOs:

```go
type ChannelRetentionAdvanceRequest struct {
    ChannelID channel.ChannelID `json:"channel_id"`
    ThroughSeq uint64 `json:"through_seq"`
    DryRun bool `json:"dry_run,omitempty"`
}

type ChannelRetentionAdvanceResult struct {
    ChannelID channel.ChannelID `json:"channel_id"`
    RequestedThroughSeq uint64 `json:"requested_through_seq"`
    AdvancedThroughSeq uint64 `json:"advanced_through_seq"`
    MinAvailableSeq uint64 `json:"min_available_seq"`
    Status string `json:"status"`
    BlockedReason string `json:"blocked_reason,omitempty"`
}
```

Implement binary codec using existing node helper functions such as `appendChannelID`, `readChannelID`, `appendUvarint`, and `readUvarint` from nearby codec files.

Implement client retry pattern by copying the structure of `Client.QueryChannelMessages`: start with the known leader, retry `not_leader` leader hint once, normalize no-leader errors.

- [ ] **Step 5: Implement handler**

`handleChannelRetentionRPC` should:

1. Decode request.
2. Require `a.channelRetention != nil`.
3. Refresh channel metadata using `a.refreshMessageQueryMeta(ctx, req.Request.ChannelID)` or a small renamed shared helper.
4. Return `no_leader` if no leader.
5. Return `not_leader` with leader ID if refreshed meta leader is not local.
6. Delegate to `a.channelRetention.AdvanceChannelRetention(ctx, req.Request)`.
7. Encode result with `ok` status.

- [ ] **Step 6: Add app remote forwarding tests**

Add a test to app retention tests:

```go
func TestManagerMessageRetentionForwardsRemoteLeader(t *testing.T) {
    remote := &fakeManagerRetentionRemote{}
    op := managerMessageRetentionOperator{
        localNodeID: 1,
        metas: managerMessageMetasFake{meta: metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2, Leader: 9}},
        remote: remote,
    }

    _, err := op.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10})

    require.NoError(t, err)
    require.Equal(t, uint64(9), remote.nodeID)
}
```

- [ ] **Step 7: Implement app remote adapter mapping**

Add remote port to `managerMessageRetentionOperator`:

```go
type managerMessageRetentionRemote interface {
    AdvanceChannelRetention(ctx context.Context, nodeID uint64, req accessnode.ChannelRetentionAdvanceRequest) (accessnode.ChannelRetentionAdvanceResult, error)
}
```

When `meta.Leader != localNodeID`, call remote and map access-node string status/reason into management status/reason.

Also add an app-to-node provider adapter:

```go
type managerMessageRetentionNodeProvider struct {
    target *managerMessageRetentionOperator
}

func (p managerMessageRetentionNodeProvider) AdvanceChannelRetention(ctx context.Context, req accessnode.ChannelRetentionAdvanceRequest) (accessnode.ChannelRetentionAdvanceResult, error) {
    result, err := p.target.AdvanceMessageRetention(ctx, managementusecase.AdvanceMessageRetentionRequest{...})
    return toAccessNodeRetentionResult(result), err
}
```

- [ ] **Step 8: Run focused node/app tests**

Run:

```sh
go test ./internal/access/node -run 'TestChannelRetention' -count=1
go test ./internal/app -run 'TestManagerMessageRetention' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit node RPC**

```bash
git add internal/access/node/service_ids.go internal/access/node/options.go internal/access/node/channel_retention_rpc.go internal/access/node/channel_retention_codec.go internal/access/node/channel_retention_rpc_test.go internal/app/manager_message_retention.go internal/app/manager_message_retention_test.go
git commit -m "feat: forward manager message retention to channel leaders"
```

---

### Task 5: App Build Wiring

**Files:**
- Modify: `internal/app/build.go`
- Modify/Test: existing app build tests if compile failures expose required test updates

- [ ] **Step 1: Run compile-focused tests and capture failures**

Run: `go test ./internal/app -run 'TestManagerMessageRetention|TestBuild' -count=1`

Expected before wiring: possible compile failure or tests showing management/node access are missing retention operator dependencies.

- [ ] **Step 2: Wire one shared retention operator**

In `build.go`, after `app.nodeClient = accessnode.NewClient(app.cluster)` is available and before `app.nodeAccess = accessnode.New(...)`, construct:

```go
managerRetention := &managerMessageRetentionOperator{
    localNodeID: cfg.Node.ID,
    metas: app.store,
    runtime: app.isrRuntime,
    stores: managerMessageRetentionStores{engine: app.channelLogDB},
    metadata: app.store,
    remote: app.nodeClient,
    now: time.Now,
}
```

Pass it to node access:

```go
ChannelRetention: managerMessageRetentionNodeProvider{target: managerRetention},
```

Pass it to management usecase:

```go
MessageRetention: managerRetention,
```

Keep existing `Messages: managerMessageReader{...}` unchanged for read queries.

- [ ] **Step 3: Run compile/focused tests**

Run:

```sh
go test ./internal/app -run 'TestManagerMessageRetention|TestConfigDefaultsChannelMessageRetentionDisabled' -count=1
go test ./internal/access/node -run 'TestChannelRetention' -count=1
go test ./internal/usecase/management -run 'TestAdvanceMessageRetention' -count=1
go test ./internal/access/manager -run 'TestManagerMessageRetention' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit wiring**

```bash
# internal/app/build.go may already contain unrelated dirty retention-worker edits.
# Stage only the hunks introduced by this task.
git add -p internal/app/build.go
git commit -m "feat: wire manager message retention runtime"
```

---

### Task 6: Web Manager API Helper

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API test**

Add to `manager-api.test.ts`:

```ts
it("advances message retention through the manager endpoint", async () => {
  const response = {
    channel_id: "room-1",
    channel_type: 2,
    requested_through_seq: 10,
    advanced_through_seq: 8,
    min_available_seq: 9,
    status: "advanced",
  }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

  await expect(advanceMessageRetention({ channelId: "room-1", channelType: 2, throughSeq: 10, dryRun: true })).resolves.toEqual(response)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/messages/retention",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ channel_id: "room-1", channel_type: 2, through_seq: 10, dry_run: true }),
    }),
  )
})
```

- [ ] **Step 2: Run API test and confirm failure**

Run: `cd web && yarn test --run src/lib/manager-api.test.ts`

Expected: FAIL because `advanceMessageRetention` is undefined.

- [ ] **Step 3: Add types and API helper**

In `manager-api.types.ts`, add:

```ts
export type ManagerMessageRetentionStatus = "advanced" | "would_advance" | "noop" | "blocked"
export type ManagerMessageRetentionBlockedReason = "" | "replay_cursor" | "min_isr_match_offset" | "hw" | "checkpoint_hw" | "current_boundary"

export type AdvanceMessageRetentionInput = {
  channelId: string
  channelType: number
  throughSeq: number
  dryRun?: boolean
}

export type AdvanceMessageRetentionResponse = {
  channel_id: string
  channel_type: number
  requested_through_seq: number
  advanced_through_seq: number
  min_available_seq: number
  status: ManagerMessageRetentionStatus
  blocked_reason?: ManagerMessageRetentionBlockedReason
}
```

In `manager-api.ts`, import types and add:

```ts
export function advanceMessageRetention(input: AdvanceMessageRetentionInput) {
  return jsonManagerFetch<AdvanceMessageRetentionResponse>("/manager/messages/retention", {
    method: "POST",
    body: JSON.stringify({
      channel_id: input.channelId,
      channel_type: input.channelType,
      through_seq: input.throughSeq,
      dry_run: input.dryRun,
    }),
  })
}
```

- [ ] **Step 4: Run API test**

Run: `cd web && yarn test --run src/lib/manager-api.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit web API helper**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web message retention API"
```

---

### Task 7: Web Messages Page Destructive Action

**Files:**
- Modify: `web/src/pages/messages/page.tsx`
- Modify: `web/src/pages/messages/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Update the manager API mock in `messages/page.test.tsx` to expose `advanceMessageRetention`:

```ts
const advanceMessageRetentionMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getMessages: (...args: unknown[]) => getMessagesMock(...args),
    getChannelRuntimeMeta: (...args: unknown[]) => getChannelRuntimeMetaMock(...args),
    advanceMessageRetention: (...args: unknown[]) => advanceMessageRetentionMock(...args),
  }
})
```

Add success test:

```ts
test("deletes message history through a selected sequence after confirmation", async () => {
  getMessagesMock.mockResolvedValueOnce({
    items: [{ message_id: 101, message_seq: 9, client_msg_no: "c-101", channel_id: "room-1", channel_type: 2, from_uid: "u1", timestamp: 1713859200, payload: "aGVsbG8=" }],
    has_more: false,
  })
  advanceMessageRetentionMock.mockResolvedValueOnce({
    channel_id: "room-1",
    channel_type: 2,
    requested_through_seq: 9,
    advanced_through_seq: 9,
    min_available_seq: 10,
    status: "advanced",
  })
  getMessagesMock.mockResolvedValueOnce({ items: [], has_more: false })

  const user = userEvent.setup()
  renderMessagesPage("/messages?channel_id=room-1&channel_type=2")

  expect(await screen.findByText("c-101")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Delete history through seq 9" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }))

  await waitFor(() => {
    expect(advanceMessageRetentionMock).toHaveBeenCalledWith({ channelId: "room-1", channelType: 2, throughSeq: 9 })
  })
  expect(await screen.findByText(/Min available seq: 10/)).toBeInTheDocument()
  expect(getMessagesMock).toHaveBeenCalledTimes(2)
})
```

Add blocked/error test where API rejects or returns `status: "blocked"`.

- [ ] **Step 2: Run page tests and confirm failure**

Run: `cd web && yarn test --run src/pages/messages/page.test.tsx`

Expected: FAIL because UI action is missing.

- [ ] **Step 3: Implement page state and action**

In `page.tsx`:

- Import `ConfirmDialog` and `advanceMessageRetention`.
- Add state:

```ts
type RetentionActionState = {
  message: ManagerMessage | null
  pending: boolean
  error: string | null
  result: AdvanceMessageRetentionResponse | null
}
```

- Add row button beside Inspect:

```tsx
<Button
  aria-label={intl.formatMessage({ id: "messages.deleteThroughSeqAction" }, { seq: message.message_seq })}
  onClick={() => openRetentionDialog(message)}
  size="sm"
  variant="destructive"
>
  {intl.formatMessage({ id: "messages.deleteThroughSeq" })}
</Button>
```

- Add `ConfirmDialog` after `DetailSheet` or before closing `PageContainer`:

```tsx
<ConfirmDialog
  confirmLabel={intl.formatMessage({ id: "common.confirm" })}
  description={retention.message ? intl.formatMessage({ id: "messages.deleteConfirmDescription" }, { channel: retention.message.channel_id, type: retention.message.channel_type, seq: retention.message.message_seq }) : undefined}
  error={retention.error ?? undefined}
  onConfirm={() => { void confirmRetentionDelete() }}
  onOpenChange={closeRetentionDialog}
  open={retention.message !== null}
  pending={retention.pending}
  title={intl.formatMessage({ id: "messages.deleteConfirmTitle" })}
/>
```

- On success, store result and call `refresh()` if a query is submitted.
- Render a compact success/status message above the table when `retention.result` is set.
- If response status is `blocked`, do not close the dialog automatically; show blocked reason.

- [ ] **Step 4: Add i18n strings**

English examples:

```ts
"messages.deleteThroughSeq": "Delete history",
"messages.deleteThroughSeqAction": "Delete history through seq {seq}",
"messages.deleteConfirmTitle": "Delete channel history?",
"messages.deleteConfirmDescription": "Messages in channel {channel} type {type} with message_seq <= {seq} will become unavailable and may be physically trimmed.",
"messages.retentionSuccess": "History retention advanced. Min available seq: {seq}",
"messages.retentionBlocked": "History retention is blocked: {reason}",
```

Chinese examples:

```ts
"messages.deleteThroughSeq": "删除历史",
"messages.deleteThroughSeqAction": "删除到序号 {seq} 的历史消息",
"messages.deleteConfirmTitle": "确认删除频道历史消息？",
"messages.deleteConfirmDescription": "频道 {channel} 类型 {type} 中 message_seq <= {seq} 的消息将不可再读，并可能被物理裁剪。",
"messages.retentionSuccess": "历史消息保留边界已推进。最小可读序号：{seq}",
"messages.retentionBlocked": "历史消息删除被阻塞：{reason}",
```

- [ ] **Step 5: Run page tests**

Run: `cd web && yarn test --run src/pages/messages/page.test.tsx`

Expected: PASS.

- [ ] **Step 6: Commit web UI**

```bash
git add web/src/pages/messages/page.tsx web/src/pages/messages/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add message history deletion UI"
```

---

### Task 8: Project Knowledge and Final Verification

**Files:**
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Add concise project knowledge**

Add one short bullet, preserving the document's existing style:

```md
- Manager history-message deletion advances channel `RetentionThroughSeq`; it must not directly delete message rows or bypass channel leader/slot metadata semantics.
```

- [ ] **Step 2: Run full focused backend verification**

Run:

```sh
go test ./internal/usecase/management ./internal/access/manager ./internal/access/node ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run focused web verification**

Run:

```sh
cd web && yarn test --run src/lib/manager-api.test.ts src/pages/messages/page.test.tsx
```

Expected: PASS.

- [ ] **Step 4: Run broader safety tests if time allows**

Run:

```sh
go test ./internal/runtime/channelretention ./pkg/channel/... ./pkg/slot/... -count=1
```

Expected: PASS. If this is too slow, run only packages touched by imports and document skipped packages in the final response.

- [ ] **Step 5: Check worktree and commit docs**

Run: `git status --short`

Confirm only expected feature files and pre-existing unrelated retention-worker files remain dirty. Then commit project knowledge:

```bash
git add docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: record manager message retention rule"
```

- [ ] **Step 6: Final response checklist**

Before reporting complete, use `superpowers:verification-before-completion` and include:

- Backend tests run and result.
- Web tests run and result.
- Any tests skipped and why.
- Reminder that existing dirty retention-worker files were preserved if they remain uncommitted.
