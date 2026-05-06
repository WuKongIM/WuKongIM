# Manager Diagnostics Workbench Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only manager diagnostics workbench that lets operators query message send traces across cluster nodes from the web admin UI.

**Architecture:** Extend the existing node-local diagnostics store with result filtering, expose node-local diagnostics through a binary `internal/access/node` RPC, aggregate node results in `internal/usecase/management`, and keep `internal/access/manager` as a thin HTTP/JSON adapter. The web manager consumes only `/manager/diagnostics/...`, never `/debug/diagnostics/...`, and renders a search panel, summary, timeline, node results, event table, related links, and JSON export.

**Tech Stack:** Go, Gin manager HTTP API, WuKongIM node RPC, `internal/observability/diagnostics`, React 19, TypeScript, React Router, React Intl, Vitest, existing manager UI components.

---

## Source Documents And Guardrails

- Approved spec: `docs/superpowers/specs/2026-05-06-manager-diagnostics-workbench-design.md`
- Relevant flow doc already checked: `internal/FLOW.md`
- No package-specific `FLOW.md` exists under these touched packages:
  - `internal/access/manager`
  - `internal/access/node`
  - `internal/usecase/management`
  - `internal/observability/diagnostics`
  - `internal/app`
  - `web/src`
- Keep deployment wording as `single-node cluster`; do not introduce standalone/single-machine semantics.
- Do not expose or call `/debug/diagnostics/...` from `web`.
- Keep `internal/access/*` thin: parse/validate/convert only.
- Keep `internal/usecase/management` free of `internal/access/node` imports.
- Keep diagnostics read-only in Phase 1; no repair/retry/leader-transfer UI actions.
- Existing `wukongim.conf.example` uses wildcard manager permissions, so no config example change is expected unless implementation discovers a non-wildcard example.

## File Responsibility Map

### Diagnostics Store

- Modify `internal/observability/diagnostics/event.go`
  - Add `Result diagnostics.Result` to `Query` so result filtering happens before store truncation.
- Modify `internal/observability/diagnostics/store.go`
  - Extend `matchesQuery` to honor `Query.Result`.
- Modify `internal/observability/diagnostics/store_test.go`
  - Add tests proving result filtering does not miss errors due to manager-side post-filtering.

### Node RPC

- Modify `internal/access/node/service_ids.go`
  - Add `diagnosticsRPCServiceID uint8 = 42`.
- Modify `internal/access/node/options.go`
  - Add `DiagnosticsProvider`, `Options.Diagnostics`, adapter field, and RPC registration.
- Create `internal/access/node/diagnostics_codec.go`
  - Binary encode/decode for diagnostics request/response.
- Create `internal/access/node/diagnostics_rpc.go`
  - Adapter handler and client method for querying one node's diagnostics store.
- Create `internal/access/node/diagnostics_rpc_test.go`
  - Codec roundtrip, JSON rejection, local disabled semantics, and remote client roundtrip.

### Management Usecase

- Modify `internal/usecase/management/app.go`
  - Add `DiagnosticsReader`, `Options.Diagnostics`, and `App.diagnostics`.
- Create `internal/usecase/management/diagnostics.go`
  - Manager-facing diagnostics DTOs, target node selection, aggregation, status calculation, duration conversion, result merging.
- Create `internal/usecase/management/diagnostics_test.go`
  - Query mode, node selection, not_found, partial, timeout, error, draining/dead behavior, result filter, truncation, summary.

### App Wiring

- Modify `internal/app/build.go`
  - Pass `Diagnostics: app` to `accessnode.New`.
  - Pass `Diagnostics: managementDiagnosticsReader{localNodeID: cfg.Node.ID, local: app, remote: app.nodeClient}` to `managementusecase.New`.
- Modify `internal/app/diagnostics.go`
  - Add `managementDiagnosticsReader` adapter next to existing `QueryDiagnostics`.
- Modify or create `internal/app/diagnostics_test.go`
  - Test local-vs-remote adapter routing through the narrow client interface introduced in this task.

### Manager HTTP API

- Modify `internal/access/manager/server.go`
  - Add `QueryDiagnostics(ctx, req)` to `Management` interface.
- Modify `internal/access/manager/routes.go`
  - Register diagnostics routes under `cluster.diagnostics:r`.
- Create `internal/access/manager/diagnostics.go`
  - Handler parsing, validation, JSON DTO conversion, and error mapping.
- Create `internal/access/manager/diagnostics_test.go`
  - Valid queries, invalid parameters, permission, service unavailable, JSON shape.
- Modify `internal/access/manager/server_test.go`
  - Extend the manager stub with `QueryDiagnostics` if tests share a common stub.

### Web API / Types / Routing

- Modify `web/src/lib/manager-api.types.ts`
  - Add diagnostics response, summary, node result, event, and request param types.
- Modify `web/src/lib/manager-api.ts`
  - Add `getDiagnosticsTrace`, `getDiagnosticsMessage`, `getDiagnosticsEvents`.
- Modify `web/src/lib/manager-api.test.ts`
  - Test diagnostics URL construction and query encoding.

### Web Page

- Modify `web/src/lib/navigation.ts`
  - Add `/diagnostics` under observability.
- Modify `web/src/app/router.tsx`
  - Add the diagnostics route.
- Modify `web/src/pages/page-shells.test.tsx`
  - Include diagnostics route shell coverage.
- Modify `web/src/app/router.test.tsx` when route assertions enumerate all paths.
- Create `web/src/pages/diagnostics/page.tsx`
  - Query panel, validation, fetch lifecycle, summary, timeline, node result list, event table, related links, JSON export.
- Create `web/src/pages/diagnostics/page.test.tsx`
  - Query validation, status rendering, partial/not_found/forbidden/unavailable states, links, JSON export.
- Modify `web/src/i18n/messages/en.ts`
  - Add diagnostics messages.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Add diagnostics messages.

### Docs / Knowledge

- Modify `docs/development/PROJECT_KNOWLEDGE.md`
  - Record the new `cluster.diagnostics:r` permission and manager diagnostics query behavior in one concise line.
- Verify `wukongim.conf.example`
  - No change expected while `WK_MANAGER_USERS` uses `*:*`; update only if implementation reveals non-wildcard examples.

---

## DTO Contract To Implement

Usecase DTOs should live in `internal/usecase/management/diagnostics.go` without JSON tags. Access DTOs should live in `internal/access/manager/diagnostics.go` with JSON tags.

```go
// DiagnosticsReader queries diagnostics events from local or remote cluster nodes.
type DiagnosticsReader interface {
    QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}

// DiagnosticsQueryRequest configures one manager diagnostics query.
type DiagnosticsQueryRequest struct {
    NodeID uint64
    Query  diagnostics.Query
}

// DiagnosticsStatus is the manager-facing aggregate status.
type DiagnosticsStatus string

const (
    DiagnosticsStatusOK       DiagnosticsStatus = "ok"
    DiagnosticsStatusError    DiagnosticsStatus = "error"
    DiagnosticsStatusTimeout  DiagnosticsStatus = "timeout"
    DiagnosticsStatusPartial  DiagnosticsStatus = "partial"
    DiagnosticsStatusNotFound DiagnosticsStatus = "not_found"
)

type DiagnosticsQueryResponse struct {
    Scope       string
    Status      DiagnosticsStatus
    GeneratedAt time.Time
    Query       diagnostics.Query
    Summary     DiagnosticsSummary
    Nodes       []DiagnosticsNodeResult
    Events      []DiagnosticsEvent
    Notes       []string
}

type DiagnosticsSummary struct {
    FirstFailureStage     string
    FirstFailureResult    string
    FirstFailureErrorCode string
    SlowestStage          string
    SlowestDurationMS     int64
    InvolvedNodes         []uint64
    PeerNodes             []uint64
    SlotID                uint32
    ChannelKey            string
    ClientMsgNo           string
    MessageSeq            uint64
    EventCount            int
}

type DiagnosticsNodeResult struct {
    NodeID     uint64
    Status     string // ok | not_found | unavailable | skipped
    DurationMS int64
    EventCount int
    Notes      []string
}

type DiagnosticsEvent struct {
    TraceID        string
    SpanID         string
    ParentSpanID   string
    Stage          string
    At             time.Time
    DurationMS     int64
    NodeID         uint64
    PeerNodeID     uint64
    SlotID         uint32
    ChannelKey     string
    ClientMsgNo    string
    MessageSeq     uint64
    RangeStart     uint64
    RangeEnd       uint64
    Service        string
    Result         string
    ErrorCode      string
    Error          string
    Attempt        int
    QueueDepth     int
    ReplicaRole    string
    SampleReason   string
}
```

Status calculation must be deterministic:

1. If any merged event has `result=error`, top-level status is `error`.
2. Else if any merged event has `result=timeout`, top-level status is `timeout`.
3. Else if any merged event has `result=partial`, `result=dropped`, or `result=canceled`, top-level status is `partial`.
4. Else if any target node is `unavailable` or `skipped`, top-level status is `partial`.
5. Else if merged event count is zero, top-level status is `not_found`.
6. Else status is `ok`.
7. Event-level `result=skipped` does not by itself change top-level status; node-level `status=skipped` does.

Node selection must be deterministic:

- Explicit `node_id`: query only that node, even if the controller snapshot would classify it as dead; if the RPC fails, mark that node `unavailable`.
- No `node_id`: query controller snapshot nodes with statuses `alive`, `suspect`, and `draining`.
- `dead` nodes: do not query; emit node result `status=skipped` and a note.
- Controller snapshot unavailable: query only local node, set `scope=local_node`, and add a note that the result is incomplete.

---

## Task 1: Add Result Filtering To Node-Local Diagnostics Store

**Files:**
- Modify: `internal/observability/diagnostics/event.go`
- Modify: `internal/observability/diagnostics/store.go`
- Test: `internal/observability/diagnostics/store_test.go`

- [ ] **Step 1: Write failing tests for result filtering**

Add tests in `internal/observability/diagnostics/store_test.go`:

```go
func TestStoreQueryFiltersByResultBeforeLimit(t *testing.T) {
    now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
    store := NewStore(StoreOptions{NodeID: 1, Capacity: 16, Now: func() time.Time { return now }})

    store.Record(Event{TraceID: "tr-ok-1", Stage: Stage("gateway_send"), Result: ResultOK, At: now.Add(time.Second)})
    store.Record(Event{TraceID: "tr-err-1", Stage: Stage("channel_append"), Result: ResultError, ErrorCode: ErrorCodeUnknown, At: now.Add(2 * time.Second)})
    store.Record(Event{TraceID: "tr-ok-2", Stage: Stage("delivery"), Result: ResultOK, At: now.Add(3 * time.Second)})
    store.Record(Event{TraceID: "tr-err-2", Stage: Stage("replica_quorum"), Result: ResultError, ErrorCode: ErrorCodeUnknown, At: now.Add(4 * time.Second)})

    got := store.Query(context.Background(), Query{Result: ResultError, Limit: 1})

    require.Equal(t, StatusError, got.Status)
    require.Len(t, got.Events, 1)
    require.Equal(t, "tr-err-2", got.Events[0].TraceID)
    require.Equal(t, ResultError, got.Events[0].Result)
}

func TestStoreQueryFiltersByStageAndResult(t *testing.T) {
    now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
    store := NewStore(StoreOptions{NodeID: 1, Capacity: 16, Now: func() time.Time { return now }})

    store.Record(Event{Stage: Stage("channel_append"), Result: ResultOK, At: now.Add(time.Second)})
    store.Record(Event{Stage: Stage("channel_append"), Result: ResultTimeout, At: now.Add(2 * time.Second)})
    store.Record(Event{Stage: Stage("delivery"), Result: ResultTimeout, At: now.Add(3 * time.Second)})

    got := store.Query(context.Background(), Query{Stage: Stage("channel_append"), Result: ResultTimeout, Limit: 10})

    require.Equal(t, StatusPartial, got.Status)
    require.Len(t, got.Events, 1)
    require.Equal(t, Stage("channel_append"), got.Events[0].Stage)
    require.Equal(t, ResultTimeout, got.Events[0].Result)
}
```

If the current status builder maps timeout to `error` rather than `partial`, keep the test focused on returned events and update expected status to match existing diagnostics store semantics. The manager-level status mapping is tested in Task 3.

- [ ] **Step 2: Run the focused diagnostics tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/observability/diagnostics -run 'TestStoreQueryFiltersBy(ResultBeforeLimit|StageAndResult)' -count=1
```

Expected: FAIL because `Query.Result` and result matching do not exist yet.

- [ ] **Step 3: Add `Result` to the diagnostics query**

Modify `internal/observability/diagnostics/event.go`:

```go
type Query struct {
    TraceID     string `json:"trace_id,omitempty"`
    ClientMsgNo string `json:"client_msg_no,omitempty"`
    ChannelKey  string `json:"channel_key,omitempty"`
    MessageSeq  uint64 `json:"message_seq,omitempty"`
    Stage       Stage  `json:"stage,omitempty"`
    Result      Result `json:"result,omitempty"`
    Limit       int    `json:"limit,omitempty"`
}
```

- [ ] **Step 4: Apply result matching in the store**

Modify `matchesQuery` in `internal/observability/diagnostics/store.go`:

```go
func matchesQuery(event Event, q Query) bool {
    if q.TraceID != "" && event.TraceID != q.TraceID {
        return false
    }
    if q.ClientMsgNo != "" && event.ClientMsgNo != q.ClientMsgNo {
        return false
    }
    if q.ChannelKey != "" && event.ChannelKey != q.ChannelKey {
        return false
    }
    if q.MessageSeq > 0 && !event.ContainsMessageSeq(q.MessageSeq) {
        return false
    }
    if q.Stage != "" && event.Stage != q.Stage {
        return false
    }
    if q.Result != "" && event.Result != q.Result {
        return false
    }
    return true
}
```

- [ ] **Step 5: Run the diagnostics package tests**

Run:

```bash
GOWORK=off go test ./internal/observability/diagnostics -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit diagnostics store filtering**

```bash
git add internal/observability/diagnostics/event.go internal/observability/diagnostics/store.go internal/observability/diagnostics/store_test.go
git commit -m "feat: filter diagnostics events by result"
```

---

## Task 2: Add Node Diagnostics RPC

**Files:**
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/options.go`
- Create: `internal/access/node/diagnostics_codec.go`
- Create: `internal/access/node/diagnostics_rpc.go`
- Test: `internal/access/node/diagnostics_rpc_test.go`

- [ ] **Step 1: Write failing RPC tests**

Create `internal/access/node/diagnostics_rpc_test.go` with tests covering these behaviors:

```go
func TestDiagnosticsRPCBinaryCodecRoundTrip(t *testing.T) {
    req := diagnosticsRequest{Query: diagnostics.Query{TraceID: "tr-1", Result: diagnostics.ResultError, Limit: 10}}
    body, err := encodeDiagnosticsRequestBinary(req)
    require.NoError(t, err)
    require.True(t, isDiagnosticsRequestBinary(body))

    gotReq, err := decodeDiagnosticsRequest(body)
    require.NoError(t, err)
    require.Equal(t, req, gotReq)

    resp := diagnosticsResponse{Status: rpcStatusOK, Result: diagnostics.QueryResult{
        Scope:  "local_node",
        NodeID: 2,
        Status: diagnostics.StatusError,
        Events: []diagnostics.Event{{TraceID: "tr-1", Stage: diagnostics.Stage("channel_append"), Result: diagnostics.ResultError}},
    }}
    respBody, err := encodeDiagnosticsResponse(resp)
    require.NoError(t, err)

    gotResp, err := decodeDiagnosticsResponse(respBody)
    require.NoError(t, err)
    require.Equal(t, resp, gotResp)
}

func TestDiagnosticsRPCRejectsJSONPayload(t *testing.T) {
    adapter := New(Options{Diagnostics: stubDiagnosticsProvider{}})
    _, err := adapter.handleDiagnosticsRPC(context.Background(), []byte(`{"query":{"trace_id":"tr-1"}}`))
    require.Error(t, err)
}

func TestDiagnosticsRPCReturnsLocalResult(t *testing.T) {
    want := diagnostics.QueryResult{Scope: "local_node", NodeID: 2, Status: diagnostics.StatusOK}
    adapter := New(Options{Diagnostics: stubDiagnosticsProvider{result: want}})

    body, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: diagnostics.Query{TraceID: "tr-1"}})
    require.NoError(t, err)
    respBody, err := adapter.handleDiagnosticsRPC(context.Background(), body)
    require.NoError(t, err)
    resp, err := decodeDiagnosticsResponse(respBody)
    require.NoError(t, err)

    require.Equal(t, rpcStatusOK, resp.Status)
    require.Equal(t, want, resp.Result)
}

func TestDiagnosticsRPCClientCallsRemoteNode(t *testing.T) {
    network := newFakeClusterNetwork()
    node1 := newFakeClusterNode(network, 1)
    node2 := newFakeClusterNode(network, 2)
    want := diagnostics.QueryResult{Scope: "local_node", NodeID: 2, Status: diagnostics.StatusOK}
    New(Options{Cluster: node2, Diagnostics: stubDiagnosticsProvider{result: want}})

    got, err := NewClient(node1).QueryDiagnostics(context.Background(), 2, diagnostics.Query{TraceID: "tr-1"})

    require.NoError(t, err)
    require.Equal(t, want, got)
}
```

Reuse the fake cluster helpers already used by `runtime_summary_rpc_test.go` and other node RPC tests. Define a small provider in the new test file:

```go
type stubDiagnosticsProvider struct {
    result diagnostics.QueryResult
}

func (p stubDiagnosticsProvider) QueryDiagnostics(context.Context, diagnostics.Query) diagnostics.QueryResult {
    return p.result
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/access/node -run DiagnosticsRPC -count=1
```

Expected: FAIL because diagnostics RPC types/functions/service registration do not exist.

- [ ] **Step 3: Add service id and provider wiring**

Modify `internal/access/node/service_ids.go`:

```go
const (
    // Preserve the existing service IDs above this line.
    connectionRPCServiceID   uint8 = 41
    diagnosticsRPCServiceID  uint8 = 42
)
```

Modify `internal/access/node/options.go`:

```go
type DiagnosticsProvider interface {
    QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
}

type Options struct {
    // Preserve the existing fields in this type.
    Diagnostics DiagnosticsProvider
}

type Adapter struct {
    // Preserve the existing fields in this type.
    diagnostics DiagnosticsProvider
}
```

Initialize and register in `New`:

```go
adapter := &Adapter{
    // Preserve the existing fields in this type.
    diagnostics: opts.Diagnostics,
}

if opts.Cluster != nil && opts.Cluster.RPCMux() != nil {
    // Preserve the existing RPC registrations above this line.
    opts.Cluster.RPCMux().Handle(diagnosticsRPCServiceID, adapter.handleDiagnosticsRPC)
}
```

- [ ] **Step 4: Implement binary codec**

Create `internal/access/node/diagnostics_codec.go`. Mirror the style used by `runtime_summary_codec.go`: magic prefix, append/read helpers, no JSON fallback.

Minimum fields that must be encoded for `diagnostics.Query`:

```go
TraceID string
ClientMsgNo string
ChannelKey string
MessageSeq uint64
Stage string
Result string
Limit int
```

Minimum fields that must be encoded for `diagnostics.QueryResult` and events:

```go
Scope string
NodeID uint64
TraceID string
ClientMsgNo string
ChannelKey string
MessageSeq uint64
Query diagnostics.Query
Status string
StartedAt time.Time
DurationMS int64
Summary diagnostics.QuerySummary
Events []diagnostics.Event
Notes []string
```

For `diagnostics.Event`, encode the redacted fields used by manager:

```go
TraceID, SpanID, ParentSpanID, Stage, At, Duration, NodeID, PeerNodeID,
SlotID, ChannelKey, ClientMsgNo, MessageSeq, RangeStart, RangeEnd,
Service, Result, ErrorCode, Error, Attempt, QueueDepth, ReplicaRole, SampleReason
```

- [ ] **Step 5: Implement RPC handler and client**

Create `internal/access/node/diagnostics_rpc.go`:

```go
type diagnosticsRequest struct {
    Query diagnostics.Query
}

type diagnosticsResponse struct {
    Status string
    Result diagnostics.QueryResult
}

func (a *Adapter) handleDiagnosticsRPC(ctx context.Context, body []byte) ([]byte, error) {
    req, err := decodeDiagnosticsRequest(body)
    if err != nil {
        return nil, err
    }
    if a == nil || a.diagnostics == nil {
        return encodeDiagnosticsResponse(diagnosticsResponse{
            Status: rpcStatusOK,
            Result: diagnostics.QueryResult{
                Scope:  "local_node",
                Query:  req.Query,
                Status: diagnostics.StatusNotFound,
                Events: []diagnostics.Event{},
                Notes:  []string{"diagnostics store is disabled on this node"},
            },
        })
    }
    return encodeDiagnosticsResponse(diagnosticsResponse{
        Status: rpcStatusOK,
        Result: a.diagnostics.QueryDiagnostics(ctx, req.Query),
    })
}

func (c *Client) QueryDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
    if c == nil || c.cluster == nil {
        return diagnostics.QueryResult{}, fmt.Errorf("access/node: cluster not configured")
    }
    body, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: query})
    if err != nil {
        return diagnostics.QueryResult{}, err
    }
    respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, diagnosticsRPCServiceID, body)
    if err != nil {
        return diagnostics.QueryResult{}, err
    }
    resp, err := decodeDiagnosticsResponse(respBody)
    if err != nil {
        return diagnostics.QueryResult{}, err
    }
    if resp.Status != rpcStatusOK {
        return diagnostics.QueryResult{}, fmt.Errorf("access/node: unexpected diagnostics status %q", resp.Status)
    }
    return resp.Result, nil
}
```

- [ ] **Step 6: Run node RPC tests**

Run:

```bash
GOWORK=off go test ./internal/access/node -run DiagnosticsRPC -count=1
```

Expected: PASS.

Then run the whole node package:

```bash
GOWORK=off go test ./internal/access/node -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit node diagnostics RPC**

```bash
git add internal/access/node/service_ids.go internal/access/node/options.go internal/access/node/diagnostics_codec.go internal/access/node/diagnostics_rpc.go internal/access/node/diagnostics_rpc_test.go
git commit -m "feat: expose diagnostics over node rpc"
```

---

## Task 3: Add Management Diagnostics Aggregation Usecase

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/diagnostics.go`
- Test: `internal/usecase/management/diagnostics_test.go`

- [ ] **Step 1: Write failing management tests**

Create `internal/usecase/management/diagnostics_test.go` with table-driven tests for:

Use these exact test names and assertions:

- `TestQueryDiagnosticsAggregatesSuccessfulNodes`: two reachable nodes return events out of order; response events are sorted by `At`, summary includes both node IDs, and status is `ok`.
- `TestQueryDiagnosticsReturnsNotFoundWhenAllNodesAreEmpty`: every target returns `diagnostics.StatusNotFound` with no events; response status is `not_found` and `events` is empty.
- `TestQueryDiagnosticsQueriesAliveSuspectAndDrainingNodes`: controller snapshot includes alive, suspect, draining, and dead nodes; fake diagnostics reader records calls for alive/suspect/draining only.
- `TestQueryDiagnosticsSkipsDeadNodesAndMarksPartial`: dead node appears in `Nodes` with `status=skipped`; top-level status is `partial` when no failure event exists.
- `TestQueryDiagnosticsReturnsPartialWhenRemoteNodeFails`: one node returns an error and another returns an ok event; response keeps the ok event and includes an unavailable node.
- `TestQueryDiagnosticsReturnsPartialWhenAllTargetsUnavailable`: every queried node returns an error; response status is `partial`, `events` is empty, and every node result is `unavailable`.
- `TestQueryDiagnosticsStatusPrefersErrorThenTimeoutThenPartial`: table cases prove `error` beats `timeout`, timeout beats partial, partial beats ok/not_found.
- `TestQueryDiagnosticsPropagatesResultFilterToNodeQueries`: request `Query.Result=diagnostics.ResultError`; fake reader sees the same result value for each queried node.
- `TestQueryDiagnosticsUsesLocalNodeWhenControllerSnapshotUnavailable`: cluster `ListNodesStrict` returns an error; only `LocalNodeID` is queried, `Scope` is `local_node`, and notes mention controller snapshot unavailability.
- `TestQueryDiagnosticsTruncatesMergedEventsToLimit`: three nodes return more events than `Limit`; response keeps the most recent `Limit` after global sort.

Use a small fake diagnostics reader:

```go
type fakeDiagnosticsReader struct {
    results map[uint64]diagnostics.QueryResult
    errors  map[uint64]error
    queries map[uint64]diagnostics.Query
}

func (f *fakeDiagnosticsReader) QueryNodeDiagnostics(_ context.Context, nodeID uint64, q diagnostics.Query) (diagnostics.QueryResult, error) {
    if f.queries == nil {
        f.queries = map[uint64]diagnostics.Query{}
    }
    f.queries[nodeID] = q
    if err := f.errors[nodeID]; err != nil {
        return diagnostics.QueryResult{}, err
    }
    return f.results[nodeID], nil
}
```

Use the existing fake cluster pattern from management tests, or define a minimal fake with `ListNodesStrict` implemented and other interface methods returning zero values if required by `ClusterReader`.

- [ ] **Step 2: Run management diagnostics tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run QueryDiagnostics -count=1
```

Expected: FAIL because the usecase does not exist.

- [ ] **Step 3: Add DiagnosticsReader to the management app**

Modify `internal/usecase/management/app.go`:

```go
type DiagnosticsReader interface {
    QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}

type Options struct {
    // Preserve the existing fields in this type.
    Diagnostics DiagnosticsReader
}

type App struct {
    // Preserve the existing fields in this type.
    diagnostics DiagnosticsReader
}

func New(opts Options) *App {
    // Preserve the existing setup in New.
    return &App{
        // Preserve the existing fields in this type.
        diagnostics: opts.Diagnostics,
    }
}
```

- [ ] **Step 4: Implement usecase DTOs and aggregation**

Create `internal/usecase/management/diagnostics.go`.

Implementation requirements:

- Normalize `req.Query.Limit` to default `100`, max `500`.
- If `req.NodeID != 0`, target only that node and set `scope=local_node`.
- If `req.NodeID == 0`, call `a.cluster.ListNodesStrict(ctx)`.
- Include `alive`, `suspect`, and `draining` nodes as query targets.
- Add a `skipped` node result for `dead` nodes.
- If `ListNodesStrict` fails, query only `a.localNodeID`, set `scope=local_node`, and add a note.
- Query target nodes concurrently with a small semaphore, e.g. 8.
- Use a per-node timeout of 2 to 3 seconds. Keep the timeout duration as a package constant or small helper so tests can override it without sleeping.
- Convert each `diagnostics.Event.Duration` to `DurationMS` with `event.Duration.Milliseconds()`.
- Merge all events, sort by `At` ascending, and keep the most recent `limit` events if over limit.
- Compute summary from merged events.
- Compute top-level status using the deterministic rules in this plan.

Keep helper functions small:

```go
func (a *App) diagnosticsTargets(ctx context.Context, requested uint64) (scope string, targets []diagnosticsTarget, notes []string)
func managerDiagnosticsEvent(event diagnostics.Event) DiagnosticsEvent
func managerDiagnosticsStatus(events []DiagnosticsEvent, nodes []DiagnosticsNodeResult) DiagnosticsStatus
func managerDiagnosticsSummary(events []DiagnosticsEvent) DiagnosticsSummary
func truncateDiagnosticsEvents(events []DiagnosticsEvent, limit int) []DiagnosticsEvent
```

- [ ] **Step 5: Run management tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run QueryDiagnostics -count=1
```

Expected: PASS.

Then run the whole management package:

```bash
GOWORK=off go test ./internal/usecase/management -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit management diagnostics usecase**

```bash
git add internal/usecase/management/app.go internal/usecase/management/diagnostics.go internal/usecase/management/diagnostics_test.go
git commit -m "feat: aggregate manager diagnostics queries"
```

---

## Task 4: Wire Diagnostics Through App Composition Root

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/diagnostics.go`
- Test: `internal/app/diagnostics_test.go`

- [ ] **Step 1: Write failing adapter tests**

Add tests to `internal/app/diagnostics_test.go` or extend the existing file:

```go
func TestManagementDiagnosticsReaderUsesLocalNode(t *testing.T) {
    local := &fakeLocalDiagnostics{result: diagnostics.QueryResult{NodeID: 1, Status: diagnostics.StatusOK}}
    reader := managementDiagnosticsReader{localNodeID: 1, local: local}

    got, err := reader.QueryNodeDiagnostics(context.Background(), 1, diagnostics.Query{TraceID: "tr-1"})

    require.NoError(t, err)
    require.Equal(t, uint64(1), got.NodeID)
    require.Equal(t, diagnostics.Query{TraceID: "tr-1"}, local.query)
}

func TestManagementDiagnosticsReaderUsesRemoteNode(t *testing.T) {
    // Use a small fake remote if accessnode.Client cannot be faked directly.
    // If accessnode.Client is concrete-only, keep this as a build/wiring test instead.
}
```

If the concrete `*accessnode.Client` makes the remote test awkward, adjust the adapter to depend on a small interface:

```go
type nodeDiagnosticsClient interface {
    QueryDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}
```

- [ ] **Step 2: Run app diagnostics tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/app -run ManagementDiagnosticsReader -count=1
```

Expected: FAIL because the adapter does not exist or is not interface-friendly.

- [ ] **Step 3: Add the app-level management diagnostics adapter**

Modify `internal/app/diagnostics.go`:

```go
type nodeDiagnosticsClient interface {
    QueryDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}

type localDiagnosticsReader interface {
    QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
}

type managementDiagnosticsReader struct {
    localNodeID uint64
    local       localDiagnosticsReader
    remote      nodeDiagnosticsClient
}

func (r managementDiagnosticsReader) QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
    if nodeID == r.localNodeID {
        if r.local == nil {
            return diagnostics.QueryResult{}, fmt.Errorf("app: local diagnostics reader not configured")
        }
        return r.local.QueryDiagnostics(ctx, query), nil
    }
    if r.remote == nil {
        return diagnostics.QueryResult{}, fmt.Errorf("app: remote diagnostics client not configured")
    }
    return r.remote.QueryDiagnostics(ctx, nodeID, query)
}
```

Never return local diagnostics for a different node. Remote-node query failures must surface as errors so `internal/usecase/management` can mark that node `unavailable`.

- [ ] **Step 4: Wire access node and management app**

Modify `internal/app/build.go`:

```go
app.nodeAccess = accessnode.New(accessnode.Options{
    // Preserve the existing fields in this type.
    Diagnostics: app,
})
```

Inside `managementusecase.New` options:

```go
Diagnostics: managementDiagnosticsReader{
    localNodeID: cfg.Node.ID,
    local:       app,
    remote:      app.nodeClient,
},
```

- [ ] **Step 5: Run app tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'ManagementDiagnosticsReader|BuildWiresDiagnostics' -count=1
```

Expected: PASS.

Then run:

```bash
GOWORK=off go test ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit app wiring**

```bash
git add internal/app/build.go internal/app/diagnostics.go internal/app/diagnostics_test.go
git commit -m "feat: wire diagnostics into manager runtime"
```

---

## Task 5: Add Manager Diagnostics HTTP API

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Create: `internal/access/manager/diagnostics.go`
- Test: `internal/access/manager/diagnostics_test.go`
- Modify: `internal/access/manager/server_test.go` to extend the shared manager stub with the new method.

- [ ] **Step 1: Write failing manager route tests**

Create `internal/access/manager/diagnostics_test.go` with tests for:

Use these exact test names and expected behavior:

- `TestManagerDiagnosticsTraceReturnsAggregate`: `GET /manager/diagnostics/trace/tr-1?limit=50&node_id=2` calls usecase with `TraceID=tr-1`, `Limit=50`, `NodeID=2` and returns JSON with `duration_ms`.
- `TestManagerDiagnosticsMessageQueriesClientMsgNo`: `/message?client_msg_no=c-1` calls usecase with `ClientMsgNo=c-1`.
- `TestManagerDiagnosticsMessageQueriesChannelSeq`: `/message?channel_key=2:g1&message_seq=9` calls usecase with `ChannelKey=2:g1` and `MessageSeq=9`.
- `TestManagerDiagnosticsMessageRejectsMixedLookupModes`: `/message?client_msg_no=c-1&channel_key=2:g1&message_seq=9` returns `400`.
- `TestManagerDiagnosticsEventsQueriesStageAndResult`: `/events?stage=channel_append&result=error` calls usecase with both filters.
- `TestManagerDiagnosticsRejectsInvalidResult`: `/events?result=bad` returns `400`.
- `TestManagerDiagnosticsRejectsInvalidLimit`: `limit=0`, `limit=bad`, and `limit=501` return `400`.
- `TestManagerDiagnosticsRejectsInvalidNodeID`: `node_id=0` and `node_id=bad` return `400`.
- `TestManagerDiagnosticsRequiresPermission`: authenticated user with only `cluster.node:r` receives `403`; user with `cluster.diagnostics:r` receives `200`.
- `TestManagerDiagnosticsReturnsUnavailableWhenManagementMissing`: server without management dependency returns `503`.

Use a response fixture with `duration_ms` fields, not `duration`.

- [ ] **Step 2: Run manager diagnostics tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run Diagnostics -count=1
```

Expected: FAIL because handlers/routes/interface methods do not exist.

- [ ] **Step 3: Extend the manager `Management` interface**

Modify `internal/access/manager/server.go`:

```go
// QueryDiagnostics returns a manager-facing diagnostics aggregate query result.
QueryDiagnostics(ctx context.Context, req managementusecase.DiagnosticsQueryRequest) (managementusecase.DiagnosticsQueryResponse, error)
```

Update any test stubs with a method that returns configured response/error.

- [ ] **Step 4: Add route registration**

Modify `internal/access/manager/routes.go`:

```go
diagnosticsRoutes := s.engine.Group("/manager")
if s.auth.enabled() {
    diagnosticsRoutes.Use(s.requirePermission("cluster.diagnostics", "r"))
}
diagnosticsRoutes.GET("/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
diagnosticsRoutes.GET("/diagnostics/message", s.handleDiagnosticsMessage)
diagnosticsRoutes.GET("/diagnostics/events", s.handleDiagnosticsEvents)
```

- [ ] **Step 5: Implement handler parsing and DTO conversion**

Create `internal/access/manager/diagnostics.go`.

Constants:

```go
const (
    defaultDiagnosticsLimit = 100
    maxDiagnosticsLimit     = 500
)
```

Validation rules:

- `limit`: optional, integer `1..500`.
- `node_id`: optional, positive uint64.
- `/trace/:trace_id`: non-empty.
- `/message`: either `client_msg_no` or `channel_key + message_seq`, never both.
- `message_seq`: positive uint64.
- `result`: allow empty or one of `ok`, `error`, `timeout`, `canceled`, `partial`, `dropped`, `skipped`; reject any other value with `400`.

DTO fields must match spec:

```go
type DiagnosticsResponse struct {
    Scope       string                 `json:"scope"`
    Status      string                 `json:"status"`
    GeneratedAt time.Time              `json:"generated_at"`
    Query       DiagnosticsQueryDTO    `json:"query"`
    Summary     DiagnosticsSummaryDTO  `json:"summary"`
    Nodes       []DiagnosticsNodeDTO   `json:"nodes"`
    Events      []DiagnosticsEventDTO  `json:"events"`
    Notes       []string               `json:"notes"`
}
```

`DiagnosticsEventDTO` must use `duration_ms`.

- [ ] **Step 6: Run manager route tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run Diagnostics -count=1
```

Expected: PASS.

Then run:

```bash
GOWORK=off go test ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit manager HTTP API**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/diagnostics.go internal/access/manager/diagnostics_test.go internal/access/manager/server_test.go
git commit -m "feat: add manager diagnostics api"
```

---

## Task 6: Add Web Diagnostics API Client, Route, Navigation, And i18n

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Test: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing API client tests**

Extend `web/src/lib/manager-api.test.ts`:

```ts
it("builds diagnostics trace query URLs", async () => {
  mockFetchJson({ scope: "cluster", status: "not_found", generated_at: "2026-05-06T00:00:00Z", query: {}, summary: emptySummary(), nodes: [], events: [], notes: [] })

  await getDiagnosticsTrace("tr 1", { nodeId: 2, limit: 50 })

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/trace/tr%201?node_id=2&limit=50", expect.any(Object))
})

it("builds diagnostics message query URLs", async () => {
  mockFetchJson(emptyDiagnosticsResponse())

  await getDiagnosticsMessage({ clientMsgNo: "c-1", limit: 25 })

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/message?client_msg_no=c-1&limit=25", expect.any(Object))
})

it("builds diagnostics events query URLs", async () => {
  mockFetchJson(emptyDiagnosticsResponse())

  await getDiagnosticsEvents({ stage: "channel_append", result: "error", nodeId: 3 })

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/events?node_id=3&stage=channel_append&result=error", expect.any(Object))
})
```

Use existing test helpers in the file; do not introduce a second fetch mocking style.

- [ ] **Step 2: Run manager API tests and verify they fail**

Run:

```bash
cd web && yarn test src/lib/manager-api.test.ts
```

Expected: FAIL because the diagnostics functions/types do not exist.

- [ ] **Step 3: Add TypeScript diagnostics types**

Modify `web/src/lib/manager-api.types.ts` with exact shapes from the spec:

```ts
export type ManagerDiagnosticsStatus = "ok" | "error" | "timeout" | "partial" | "not_found"

export type ManagerDiagnosticsEvent = {
  trace_id?: string
  span_id?: string
  parent_span_id?: string
  stage: string
  at: string
  duration_ms?: number
  node_id?: number
  peer_node_id?: number
  slot_id?: number
  channel_key?: string
  client_msg_no?: string
  message_seq?: number
  range_start?: number
  range_end?: number
  service?: string
  result: string
  error_code?: string
  error?: string
  attempt?: number
  queue_depth?: number
  replica_role?: string
  sample_reason?: string
}

export type ManagerDiagnosticsSummary = {
  first_failure_stage?: string
  first_failure_result?: string
  first_failure_error_code?: string
  slowest_stage?: string
  slowest_duration_ms?: number
  involved_nodes: number[]
  peer_nodes: number[]
  slot_id?: number
  channel_key?: string
  client_msg_no?: string
  message_seq?: number
  event_count: number
}

export type ManagerDiagnosticsNodeResult = {
  node_id: number
  status: "ok" | "not_found" | "unavailable" | "skipped"
  duration_ms: number
  event_count: number
  notes: string[]
}

export type ManagerDiagnosticsResponse = {
  scope: "cluster" | "local_node"
  status: ManagerDiagnosticsStatus
  generated_at: string
  query: Record<string, unknown>
  summary: ManagerDiagnosticsSummary
  nodes: ManagerDiagnosticsNodeResult[]
  events: ManagerDiagnosticsEvent[]
  notes: string[]
}
```

Also add request param types:

```ts
export type DiagnosticsCommonParams = { nodeId?: number; limit?: number }
export type DiagnosticsMessageParams = DiagnosticsCommonParams & { clientMsgNo?: string; channelKey?: string; messageSeq?: number }
export type DiagnosticsEventsParams = DiagnosticsCommonParams & { stage?: string; result?: string }
```

- [ ] **Step 4: Add API client functions**

Modify `web/src/lib/manager-api.ts`:

```ts
function applyDiagnosticsCommonParams(search: URLSearchParams, params?: DiagnosticsCommonParams) {
  if (typeof params?.nodeId === "number") search.set("node_id", String(params.nodeId))
  if (typeof params?.limit === "number") search.set("limit", String(params.limit))
}

export function getDiagnosticsTrace(traceId: string, params?: DiagnosticsCommonParams) {
  const search = new URLSearchParams()
  applyDiagnosticsCommonParams(search, params)
  const query = search.toString()
  const path = `/manager/diagnostics/trace/${encodeURIComponent(traceId)}`
  return jsonManagerFetch<ManagerDiagnosticsResponse>(query ? `${path}?${query}` : path)
}

export function getDiagnosticsMessage(params: DiagnosticsMessageParams) {
  const search = new URLSearchParams()
  applyDiagnosticsCommonParams(search, params)
  if (params.clientMsgNo) search.set("client_msg_no", params.clientMsgNo)
  if (params.channelKey) search.set("channel_key", params.channelKey)
  if (typeof params.messageSeq === "number") search.set("message_seq", String(params.messageSeq))
  return jsonManagerFetch<ManagerDiagnosticsResponse>(`/manager/diagnostics/message?${search.toString()}`)
}

export function getDiagnosticsEvents(params?: DiagnosticsEventsParams) {
  const search = new URLSearchParams()
  applyDiagnosticsCommonParams(search, params)
  if (params?.stage) search.set("stage", params.stage)
  if (params?.result) search.set("result", params.result)
  const query = search.toString()
  return jsonManagerFetch<ManagerDiagnosticsResponse>(query ? `/manager/diagnostics/events?${query}` : "/manager/diagnostics/events")
}
```

- [ ] **Step 5: Add diagnostics translations used by the page**

Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts` with at least these IDs:

```text
nav.diagnostics.title
nav.diagnostics.description
diagnostics.title
diagnostics.description
diagnostics.query.title
diagnostics.query.trace
diagnostics.query.clientMsgNo
diagnostics.query.channelSeq
diagnostics.query.recentErrors
diagnostics.status.ok
diagnostics.status.error
diagnostics.status.timeout
diagnostics.status.partial
diagnostics.status.notFound
```

- [ ] **Step 6: Run web API tests**

Run:

```bash
cd web && yarn test src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 7: Commit web diagnostics API client**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add diagnostics web api client"
```

---

## Task 7: Build Web Diagnostics Page

**Files:**
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/app/router.test.tsx` when route assertions enumerate all paths
- Create: `web/src/pages/diagnostics/page.tsx`
- Test: `web/src/pages/diagnostics/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/diagnostics/page.test.tsx` covering:

Use these exact test names and expected behavior:

- `runs a trace query and renders the summary`: submit trace `tr-1`, mock `getDiagnosticsTrace`, assert status, event count, and first failure stage render.
- `validates channel sequence input`: choose channel sequence mode with empty channel key or non-positive seq, assert API is not called and validation text renders.
- `renders partial node results`: mock one `unavailable` node and one `ok` node, assert partial banner and node notes render.
- `renders not_found empty state`: mock `status=not_found`, assert the empty-state guidance renders.
- `renders forbidden state from ManagerApiError`: reject with `new ManagerApiError(403, "forbidden", "forbidden")`, assert forbidden resource state.
- `builds related slot log and connection links`: mock event with `node_id=2` and `slot_id=9`, assert links to `/slot-logs?slot_id=9&node_id=2` and `/connections?node_id=2`.
- `exports the current diagnostics JSON`: mock clipboard, click export, assert clipboard receives `JSON.stringify(response, null, 2)`.

Mock only `@/lib/manager-api` functions used by the page. Reuse existing `renderWithProviders` or project test setup patterns.

- [ ] **Step 2: Run page tests and verify they fail**

Run:

```bash
cd web && yarn test src/pages/diagnostics/page.test.tsx
```

Expected: FAIL because the page does not exist or is still empty.

- [ ] **Step 3: Implement the query form state and validation**

Create `web/src/pages/diagnostics/page.tsx`.

Core local types:

```ts
type DiagnosticsQueryMode = "trace" | "client_msg_no" | "channel_seq" | "recent_errors"

type DiagnosticsQueryForm = {
  mode: DiagnosticsQueryMode
  traceId: string
  clientMsgNo: string
  channelKey: string
  messageSeq: string
  stage: string
  result: "" | "error" | "timeout" | "partial" | "dropped" | "canceled" | "skipped"
  nodeId: string
  limit: string
}
```

Validation rules:

- Trace mode requires `traceId.trim()`.
- Client message mode requires `clientMsgNo.trim()`.
- Channel sequence mode requires `channelKey.trim()` and positive integer `messageSeq`.
- Node id is optional; if set, positive integer.
- Limit defaults to `100`; if set, integer `1..500`.

- [ ] **Step 4: Implement data loading**

Use existing manager API functions:

```ts
const response = await getDiagnosticsTrace(traceId, commonParams)
const response = await getDiagnosticsMessage({ clientMsgNo, nodeId, limit })
const response = await getDiagnosticsMessage({ channelKey, messageSeq, nodeId, limit })
const response = await getDiagnosticsEvents({ stage, result, nodeId, limit })
```

State shape:

```ts
type DiagnosticsState = {
  response: ManagerDiagnosticsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
  queried: boolean
}
```

- [ ] **Step 5: Implement status, summary, timeline, nodes, and table**

Use existing components:

- `PageContainer`
- `PageHeader`
- `SectionCard`
- `ResourceState`
- `Button`
- `StatusBadge` if it maps cleanly; otherwise use local badge classes.

Display:

- Header badges: `scope`, `generated_at`, `event_count`.
- Summary cards: `status`, `first_failure_stage`, `slowest_stage`, `involved_nodes`.
- Timeline list sorted by `at` ascending.
- Node list with `ok`, `not_found`, `unavailable`, `skipped`.
- Event table with `duration_ms`.

- [ ] **Step 6: Implement related links and JSON export**

Related links rules:

- `slot_id + node_id`: generate `/slot-logs?slot_id=<slot>&node_id=<node>` as a contextual link; do not assume the target page auto-filters unless verified during implementation.
- `node_id`: generate `/connections?node_id=<node>` and `/nodes` as contextual links; do not assume the target page auto-filters unless verified during implementation.
- `channel_key`: only generate message/channel links if safely parseable; otherwise show plain channel key.

JSON export:

- Use `JSON.stringify(response, null, 2)`.
- Prefer `navigator.clipboard.writeText` when available.
- If adding file download is easier, use a generated `Blob` URL in tests. Keep one export path simple.

- [ ] **Step 7: Add navigation and router wiring**

Modify `web/src/lib/navigation.ts`:

- Import a suitable icon such as `SearchCode`, `Bug`, or `Activity` from `lucide-react`.
- Add `/diagnostics` to the observability group before `/network`.

Modify `web/src/app/router.tsx`:

```tsx
import { DiagnosticsPage } from "@/pages/diagnostics/page"
{ path: "diagnostics", element: <DiagnosticsPage /> },
```

Update route/page shell tests so `/diagnostics` renders the diagnostics page title.

- [ ] **Step 8: Run page and routing tests**

Run:

```bash
cd web && yarn test src/pages/diagnostics/page.test.tsx src/app/router.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 9: Run all web tests**

Run:

```bash
cd web && yarn test
```

Expected: PASS.

- [ ] **Step 10: Commit diagnostics page**

```bash
git add web/src/lib/navigation.ts web/src/app/router.tsx web/src/pages/page-shells.test.tsx web/src/app/router.test.tsx web/src/pages/diagnostics/page.tsx web/src/pages/diagnostics/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add diagnostics manager page"
```

---

## Task 8: Update Docs And Knowledge

**Files:**
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Verify: `wukongim.conf.example`

- [ ] **Step 1: Check manager permission examples**

Run:

```bash
rg -n 'WK_MANAGER_USERS|cluster\.diagnostics|cluster\.node|cluster\.slot|permissions' wukongim.conf.example docs/development/PROJECT_KNOWLEDGE.md
```

Expected: `wukongim.conf.example` currently uses wildcard `*:*`, so no config example update is required.

- [ ] **Step 2: Record concise project knowledge**

Add one concise line to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- Manager diagnostics routes require `cluster.diagnostics:r`; cluster-wide diagnostics queries include alive, suspect, and draining nodes, skip dead nodes, and return `partial` when results are incomplete.
```

Keep the file short; do not paste the full design.

- [ ] **Step 3: Commit docs knowledge**

```bash
git add docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: record manager diagnostics permission"
```

---

## Task 9: Final Verification

**Files:**
- No new files unless tests reveal fixes.

- [ ] **Step 1: Run backend focused tests**

Run:

```bash
GOWORK=off go test ./internal/observability/diagnostics ./internal/access/node ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run web tests**

Run:

```bash
cd web && yarn test
```

Expected: PASS.

- [ ] **Step 3: Run broader relevant Go tests**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run targeted integration test only if implemented**

If Task 5 or Task 4 added an integration test named `TestManagerDiagnosticsAggregatesSendTrace`, run:

```bash
GOWORK=off go test -tags=integration ./internal/app -run TestManagerDiagnosticsAggregatesSendTrace -count=1
```

Expected: PASS. Do not run full integration suite during normal development.

- [ ] **Step 5: Verify clean worktree**

Run:

```bash
git status --short
```

Expected: no output.

- [ ] **Step 6: Summarize commits and verification**

Run:

```bash
git log --oneline --max-count=10
```

Expected: recent commits include diagnostics store filter, node RPC, management aggregation, manager API, web page, and docs knowledge.

---

## Subagent Execution Notes

When executing this plan with subagents:

- Worker 1 owns Tasks 1 and 2: `internal/observability/diagnostics` and `internal/access/node`.
- Worker 2 owns Tasks 3, 4, and 5 after Worker 1 lands: `internal/usecase/management`, `internal/app`, and `internal/access/manager`.
- Worker 3 owns Tasks 6 and 7 after manager API types are clear: `web/src/**` only.
- One final integrator owns Tasks 8 and 9.
- Workers are not alone in the codebase. They must not revert others' edits and must adapt to changes already present.
- If multiple workers run in parallel, keep write sets disjoint until integration.
