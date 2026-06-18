# Controller V2 Log Compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement safe ControllerV2 Raft log compaction with manual manager/web triggers for one node or all Controller voters.

**Architecture:** `pkg/controllerv2/raft` owns local snapshot, restore, status, and compaction safety. `pkg/clusterv2` exposes node-local operations, while `internalv2/infra/cluster`, `internalv2/access/node`, `internalv2/usecase/management`, and `internalv2/access/manager` adapt those operations into manager APIs. `web` already has most client/page scaffolding; backend implementation and permission-aligned response mapping complete the flow.

**Tech Stack:** Go, etcd raft, local WAL/snapshot storage in `pkg/controllerv2/raft/raftstore`, internalv2 manager HTTP with gin, clusterv2 node RPC, React/Vitest manager UI.

---

## File Structure

- Modify `pkg/controllerv2/raft/status.go`: add compaction, restore, peer progress, and log watermark status structs with English comments.
- Modify `pkg/controllerv2/raft/service.go`: add `CompactLog`, a compaction request channel, status helpers, and lifecycle wiring.
- Modify `pkg/controllerv2/raft/service_run.go`: process manual compaction requests in the Raft run loop and keep status current.
- Modify `pkg/controllerv2/raft/service_snapshot.go`: refactor `maybeSnapshot` into a shared automatic/manual snapshot helper.
- Modify `pkg/controllerv2/raft/apply_scheduler.go`: restore non-empty `Ready.Snapshot` into the state machine before marking it applied.
- Modify `pkg/controllerv2/runtime.go`: expose compaction/status through the root runtime.
- Modify `pkg/clusterv2/control/log_entries.go`: add control runtime aliases and delegation for status/compaction.
- Modify `pkg/clusterv2/node_logs.go`: add local Controller Raft status and compaction methods on `Node`.
- Modify `pkg/clusterv2/net/ids.go`: add a dedicated manager Controller Raft RPC service ID and alias.
- Create `internalv2/access/node/manager_controller_raft_codec.go`: encode/decode status and compact RPC messages.
- Create `internalv2/access/node/manager_controller_raft_rpc.go`: adapter and client methods for remote node status/compaction.
- Modify `internalv2/access/node/adapter.go` or the current service registration file: register the new RPC handler.
- Create `internalv2/infra/cluster/management_controller_raft.go`: route local-vs-remote Controller Raft operations.
- Modify `internalv2/infra/cluster/management_logs.go`: keep log reader unchanged except for shared node interface reuse if needed.
- Create `internalv2/usecase/management/controller_raft.go`: request/response DTOs, fan-out logic, and health/status mapping.
- Modify `internalv2/usecase/management/app.go`: wire the new operator into the management application.
- Create `internalv2/access/manager/controller_raft.go`: HTTP DTOs and handlers for status and compaction.
- Modify `internalv2/access/manager/server.go`: extend the `Management` interface and register routes with `cluster.controller:r/w`.
- Modify `internalv2/app/wiring.go`: provide the new Controller Raft operator to the manager server.
- Modify `web/src/pages/controller/page.tsx`: minor copy/permission alignment if existing tests expose gaps.
- Modify `web/src/pages/controller/page.test.tsx`: add or adjust backend-aligned permission/result tests.
- Modify `web/src/lib/manager-api.types.ts` only if backend field names differ from existing types; otherwise leave it.
- Update `FLOW.md` files only if implementation changes their described flow.

## Task 1: Raft Status Types and Manual Compaction API

**Files:**
- Modify: `pkg/controllerv2/raft/status.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Test: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Write the failing status/API test**

Add a test that compiles only after `CompactLog` and the new status fields exist:

```go
func TestServiceCompactLogReturnsSkippedWhenNotStarted(t *testing.T) {
    sm := newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json"))
    svc, err := NewService(Config{
        NodeID:       1,
        Peers:        []uint64{1},
        RaftDir:      filepath.Join(t.TempDir(), "controller-raft"),
        StateMachine: sm,
        AllowBootstrap: true,
    })
    require.NoError(t, err)

    result, err := svc.CompactLog(context.Background())

    require.ErrorIs(t, err, ErrNotStarted)
    require.False(t, result.Compacted)
    require.Equal(t, LogCompactionSkipNotStarted, result.SkippedReason)
    st := svc.Status()
    require.Equal(t, uint64(1), st.NodeID)
    require.Equal(t, RoleUnknown, st.Role)
    require.Equal(t, LogCompactionSkipNotStarted, st.Compaction.SkippedReason)
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./pkg/controllerv2/raft -run TestServiceCompactLogReturnsSkippedWhenNotStarted -count=1
```

Expected: compile failure mentioning `CompactLog`, `LogCompactionSkipNotStarted`, or `Status.Compaction`.

- [ ] **Step 3: Implement the smallest API surface**

Add commented types to `status.go`:

```go
const (
    // LogCompactionSkipNotStarted reports that compaction was requested before service start.
    LogCompactionSkipNotStarted = "not_started"
    // LogCompactionSkipNoAppliedIndex reports that no materialized applied index is available.
    LogCompactionSkipNoAppliedIndex = "no_applied_index"
    // LogCompactionSkipNoMaterializedState reports that no state-machine snapshot revision exists.
    LogCompactionSkipNoMaterializedState = "no_materialized_state"
    // LogCompactionSkipUpToDate reports that the current snapshot already covers the target index.
    LogCompactionSkipUpToDate = "up_to_date"
)

// LogCompactionResult describes one local ControllerV2 Raft log compaction attempt.
type LogCompactionResult struct {
    NodeID uint64
    AppliedIndex uint64
    BeforeSnapshotIndex uint64
    AfterSnapshotIndex uint64
    Compacted bool
    SkippedReason string
    Error string
}

// LogCompactionStatus describes the latest local ControllerV2 Raft compaction attempt.
type LogCompactionStatus struct {
    LastTrigger string
    LastAttemptAt time.Time
    LastSuccessAt time.Time
    LastAppliedIndex uint64
    BeforeSnapshotIndex uint64
    AfterSnapshotIndex uint64
    Compacted bool
    SkippedReason string
    LastError string
}
```

Extend `Status` with `Compaction LogCompactionStatus`.

Add `CompactLog(ctx)` to `service.go` that returns `ErrNotStarted` before the service is running and records the skipped status.

- [ ] **Step 4: Run the test and verify GREEN**

Run:

```bash
go test ./pkg/controllerv2/raft -run TestServiceCompactLogReturnsSkippedWhenNotStarted -count=1
```

Expected: PASS.

## Task 2: Shared Snapshot Helper and Manual Compaction Behavior

**Files:**
- Modify: `pkg/controllerv2/raft/service_snapshot.go`
- Modify: `pkg/controllerv2/raft/service_run.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Test: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Write the failing manual compaction test**

Add a test that bootstraps one node with `SnapshotCount` high enough that automatic compaction does not run, proposes one real command, then forces compaction:

```go
func TestServiceCompactLogForcesSnapshotBelowAutomaticThreshold(t *testing.T) {
    ctx := context.Background()
    dir := t.TempDir()
    sm := newTestStateMachine(t, filepath.Join(dir, "cluster-state.json"))
    svc, err := NewService(Config{
        NodeID:       1,
        Peers:        []uint64{1},
        RaftDir:      filepath.Join(dir, "controller-raft"),
        StateMachine: sm,
        AllowBootstrap: true,
        SnapshotCount: 1000,
        SnapshotCatchUpEntries: 1,
    })
    require.NoError(t, err)
    require.NoError(t, svc.Start(ctx))
    t.Cleanup(func() { require.NoError(t, svc.Stop()) })
    waitForLeader(t, svc, 1)

    require.NoError(t, svc.Propose(ctx, command.UpsertNode{NodeID: 1, Name: "node-1"}))

    result, err := svc.CompactLog(ctx)

    require.NoError(t, err)
    require.True(t, result.Compacted)
    require.GreaterOrEqual(t, result.AfterSnapshotIndex, result.AppliedIndex)
    require.Empty(t, result.SkippedReason)

    store, err := raftstore.Open(ctx, raftstore.Config{Dir: filepath.Join(dir, "controller-raft"), NodeID: 1})
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, store.Close()) })
    snap, err := store.Snapshot()
    require.NoError(t, err)
    require.Equal(t, result.AfterSnapshotIndex, snap.Metadata.Index)
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./pkg/controllerv2/raft -run TestServiceCompactLogForcesSnapshotBelowAutomaticThreshold -count=1
```

Expected: FAIL because `CompactLog` does not yet enter the run loop or does not snapshot.

- [ ] **Step 3: Add run-loop compaction request handling**

Add:

```go
type compactRequest struct {
    ctx  context.Context
    resp chan compactResponse
}

type compactResponse struct {
    result LogCompactionResult
    err    error
}
```

Add `compact chan compactRequest` to `Service`, initialize it in `Start`, clear it in `Stop`, and select it in `run`:

```go
case req := <-compactCh:
    result, err := s.compactLogNow(req.ctx, store, snapshotTriggerManual, true)
    req.resp <- compactResponse{result: result, err: err}
```

Refactor `maybeSnapshot` so automatic threshold checks call the same helper:

```go
func (s *Service) maybeSnapshot(ctx context.Context, store *raftstore.Store, applied uint64) error {
    if !s.shouldAutoSnapshot(ctx, store, applied) {
        return nil
    }
    _, err := s.compactLogNow(ctx, store, snapshotTriggerAutomatic, false)
    return err
}
```

`compactLogNow` should record status for success, skip, and error.

- [ ] **Step 4: Run the manual compaction test and the existing raft package tests**

Run:

```bash
go test ./pkg/controllerv2/raft -run 'TestServiceCompactLogForcesSnapshotBelowAutomaticThreshold|TestServiceCompactLogReturnsSkippedWhenNotStarted' -count=1
```

Expected: PASS.

Then run:

```bash
go test ./pkg/controllerv2/raft -count=1
```

Expected: PASS.

## Task 3: Ready Snapshot Restore

**Files:**
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`
- Modify: `pkg/controllerv2/raft/status.go`
- Test: `pkg/controllerv2/raft/apply_scheduler_test.go`

- [ ] **Step 1: Write the failing restore test**

Add a test that sends an apply job with a snapshot containing a different state and asserts the state machine is restored:

```go
func TestApplySchedulerRestoresReadySnapshot(t *testing.T) {
    ctx := context.Background()
    dir := t.TempDir()
    sm := newTestStateMachine(t, filepath.Join(dir, "cluster-state.json"))
    store, err := raftstore.Open(ctx, raftstore.Config{Dir: filepath.Join(dir, "controller-raft"), NodeID: 1})
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, store.Close()) })

    restored := state.ClusterState{
        Revision: 3,
        AppliedRaftIndex: 10,
        Nodes: []state.Node{{ID: 2, Name: "node-2"}},
    }
    data, err := state.Encode(restored)
    require.NoError(t, err)

    scheduler := newApplyScheduler(applySchedulerConfig{}, sm, store, nil)
    scheduler.start(ctx)
    t.Cleanup(func() { scheduler.stop() })

    err = scheduler.enqueue(ctx, toApply{
        snapshot: raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: 10, Term: 2}},
    })
    require.NoError(t, err)
    require.Eventually(t, func() bool {
        st := sm.Snapshot(ctx)
        return st.Revision == 3 && len(st.Nodes) == 1 && st.Nodes[0].ID == 2
    }, time.Second, 10*time.Millisecond)
}
```

- [ ] **Step 2: Run the restore test and verify RED**

Run:

```bash
go test ./pkg/controllerv2/raft -run TestApplySchedulerRestoresReadySnapshot -count=1
```

Expected: FAIL because the scheduler marks snapshot applied without restoring the state machine.

- [ ] **Step 3: Implement snapshot restore in the apply scheduler**

In the snapshot branch:

```go
if !raftpb.IsEmptySnap(job.snapshot) {
    st, err := state.Decode(job.snapshot.Data)
    if err != nil {
        return err
    }
    if st.AppliedRaftIndex < job.snapshot.Metadata.Index {
        st.AppliedRaftIndex = job.snapshot.Metadata.Index
    }
    if err := a.sm.Restore(ctx, st); err != nil {
        return err
    }
    if err := a.store.MarkAppliedBatch(ctx, []raftstore.AppliedEntry{{Index: job.snapshot.Metadata.Index}}); err != nil {
        return err
    }
    a.notifyApplied(ctx, job.snapshot.Metadata.Index)
    return nil
}
```

Keep existing committed-entry apply behavior unchanged.

- [ ] **Step 4: Run restore and raft package tests**

Run:

```bash
go test ./pkg/controllerv2/raft -run TestApplySchedulerRestoresReadySnapshot -count=1
go test ./pkg/controllerv2/raft -count=1
```

Expected: PASS.

## Task 4: Runtime and clusterv2 Local Boundary

**Files:**
- Modify: `pkg/controllerv2/runtime.go`
- Modify: `pkg/clusterv2/control/log_entries.go`
- Modify: `pkg/clusterv2/node_logs.go`
- Test: `pkg/clusterv2/node_logs_test.go`

- [ ] **Step 1: Write the failing local boundary test**

Add a test with a fake control runtime implementing status and compact methods, then assert `Node` delegates locally. If `Node` construction is too heavy, use the existing node log test helper pattern and add a compile-time interface assertion:

```go
func TestNodeLocalControllerRaftOperationsDelegateToControlRuntime(t *testing.T) {
    var _ interface {
        LocalControllerRaftStatus(context.Context) (ControllerRaftStatus, error)
        LocalCompactControllerRaftLog(context.Context) (ControllerRaftCompactionResult, error)
    } = (*Node)(nil)
}
```

Add a runtime-level unit test when a suitable lightweight constructor exists:

```go
func TestRuntimeControllerRaftOperationsDelegateToBackend(t *testing.T) {
    backend := &fakeControllerBackend{
        status: controllerv2.RaftStatus{NodeID: 1, Role: "leader"},
        result: controllerv2.LogCompactionResult{NodeID: 1, Compacted: true},
    }
    rt := &Runtime{backend: backend}

    status, err := rt.ControllerRaftStatus(context.Background())
    require.NoError(t, err)
    require.Equal(t, uint64(1), status.NodeID)

    result, err := rt.CompactControllerRaftLog(context.Background())
    require.NoError(t, err)
    require.True(t, result.Compacted)
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./pkg/clusterv2 -run TestNodeLocalControllerRaftOperationsDelegateToControlRuntime -count=1
```

Expected: compile failure for missing methods/types.

- [ ] **Step 3: Add local status and compaction aliases**

Expose aliases:

```go
type ControllerRaftStatus = cv2.RaftStatus
type ControllerRaftCompactionResult = cv2.LogCompactionResult
```

Add methods on `control.Runtime`, `controllerv2.Runtime`, and `clusterv2.Node`:

```go
func (n *Node) LocalControllerRaftStatus(ctx context.Context) (ControllerRaftStatus, error)
func (n *Node) LocalCompactControllerRaftLog(ctx context.Context) (ControllerRaftCompactionResult, error)
```

- [ ] **Step 4: Run clusterv2 tests**

Run:

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/... -count=1
```

Expected: PASS.

## Task 5: Dedicated Node RPC for Controller Raft Operations

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Create: `internalv2/access/node/manager_controller_raft_codec.go`
- Create: `internalv2/access/node/manager_controller_raft_rpc.go`
- Modify: `internalv2/access/node/adapter.go`
- Test: `internalv2/access/node/manager_controller_raft_codec_test.go`
- Test: `internalv2/access/node/manager_controller_raft_rpc_test.go`

- [ ] **Step 1: Write codec round-trip tests**

Create tests:

```go
func TestManagerControllerRaftRPCRoundTripStatus(t *testing.T) {
    resp := managerControllerRaftRPCResponse{
        Status: rpcStatusOK,
        RaftStatus: managementusecase.ControllerRaftStatus{
            NodeID: 1,
            Role: "leader",
            LeaderID: 1,
            Term: 4,
            FirstIndex: 2,
            LastIndex: 10,
            SnapshotIndex: 2,
        },
    }
    encoded, err := encodeManagerControllerRaftResponse(resp)
    require.NoError(t, err)
    decoded, err := decodeManagerControllerRaftResponse(encoded)
    require.NoError(t, err)
    require.Equal(t, resp, decoded)
}

func TestManagerControllerRaftRPCRoundTripCompact(t *testing.T) {
    req := managerControllerRaftRPCRequest{Op: managerControllerRaftOpCompact, NodeID: 2}
    encoded, err := encodeManagerControllerRaftRequest(req)
    require.NoError(t, err)
    decoded, err := decodeManagerControllerRaftRequest(encoded)
    require.NoError(t, err)
    require.Equal(t, req, decoded)
}
```

- [ ] **Step 2: Run codec tests and verify RED**

Run:

```bash
go test ./internalv2/access/node -run 'TestManagerControllerRaftRPC' -count=1
```

Expected: compile failure for missing codec/types.

- [ ] **Step 3: Implement codec and RPC client/handler**

Add service ID:

```go
const RPCManagerControllerRaft uint8 = <next-free-id>
```

Add aliases in `ids.go` so observability labels show `manager controller raft`.

Implement request fields:

```go
type managerControllerRaftRPCRequest struct {
    Op string
    NodeID uint64
}
```

Implement response fields:

```go
type managerControllerRaftRPCResponse struct {
    Status string
    RaftStatus managementusecase.ControllerRaftStatus
    Compaction managementusecase.ControllerRaftCompactionResult
}
```

Add `Client.GetManagerControllerRaftStatus` and `Client.CompactManagerControllerRaftLog`.

- [ ] **Step 4: Run node RPC tests**

Run:

```bash
go test ./internalv2/access/node -count=1
```

Expected: PASS.

## Task 6: Cluster Infra Routing and Management Usecase Fan-out

**Files:**
- Create: `internalv2/infra/cluster/management_controller_raft.go`
- Create: `internalv2/usecase/management/controller_raft.go`
- Modify: `internalv2/usecase/management/app.go`
- Test: `internalv2/infra/cluster/management_controller_raft_test.go`
- Test: `internalv2/usecase/management/controller_raft_test.go`

- [ ] **Step 1: Write failing management fan-out tests**

Add tests that fake three nodes, two Controller voters, and one failing remote:

```go
func TestControllerRaftCompactLogsFansOutToControllerVoters(t *testing.T) {
    op := &fakeControllerRaftOperator{
        voters: []uint64{1, 2},
        results: map[uint64]ControllerRaftCompactionResult{
            1: {NodeID: 1, Compacted: true},
            2: {NodeID: 2, SkippedReason: "up_to_date"},
        },
    }
    app := New(AppOptions{ControllerRaft: op})

    summary, err := app.CompactControllerRaftLogs(context.Background())

    require.NoError(t, err)
    require.Len(t, summary.Items, 2)
    require.True(t, summary.Items[0].Success)
    require.True(t, summary.Items[1].Success)
    require.Equal(t, []uint64{1, 2}, op.called)
}

func TestControllerRaftCompactLogsPreservesPartialFailure(t *testing.T) {
    op := &fakeControllerRaftOperator{
        voters: []uint64{1, 2},
        results: map[uint64]ControllerRaftCompactionResult{
            1: {NodeID: 1, Compacted: true},
        },
        errors: map[uint64]error{2: errors.New("target unavailable")},
    }
    app := New(AppOptions{ControllerRaft: op})

    summary, err := app.CompactControllerRaftLogs(context.Background())

    require.NoError(t, err)
    require.Len(t, summary.Items, 2)
    require.True(t, summary.Items[0].Success)
    require.False(t, summary.Items[1].Success)
    require.Contains(t, summary.Items[1].Error, "target unavailable")
}
```

- [ ] **Step 2: Run usecase tests and verify RED**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestControllerRaftCompactLogs' -count=1
```

Expected: compile failure for missing usecase types/methods.

- [ ] **Step 3: Implement usecase DTOs and routing**

Add DTOs with English comments:

```go
type ControllerRaftStatus struct { ... }
type ControllerRaftCompactionResult struct { ... }
type ControllerRaftCompactNodeResult struct { ... }
type ControllerRaftCompactionSummary struct {
    Items []ControllerRaftCompactNodeResult
}
```

`App.CompactControllerRaftLogs` should get voters from the operator's cluster-state view, sort by node ID, execute each target, and preserve per-node errors.

Implement infra router with local fast path:

```go
if req.NodeID == r.node.NodeID() {
    return controllerRaftStatusFromCluster(r.node.LocalControllerRaftStatus(ctx))
}
return r.remote.GetManagerControllerRaftStatus(ctx, req.NodeID)
```

- [ ] **Step 4: Run usecase and infra tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/infra/cluster -count=1
```

Expected: PASS.

## Task 7: Manager HTTP API

**Files:**
- Create: `internalv2/access/manager/controller_raft.go`
- Modify: `internalv2/access/manager/server.go`
- Test: `internalv2/access/manager/controller_raft_test.go`

- [ ] **Step 1: Write failing route and permission tests**

Add tests:

```go
func TestManagerControllerRaftStatusRequiresReadPermission(t *testing.T) {
    srv := newTestServerWithPermissions(t, []PermissionConfig{{Resource: "cluster.controller", Actions: []string{"r"}}})
    req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
    rec := httptest.NewRecorder()

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
}

func TestManagerControllerRaftCompactRequiresWritePermission(t *testing.T) {
    srv := newTestServerWithPermissions(t, []PermissionConfig{{Resource: "cluster.controller", Actions: []string{"r"}}})
    req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/controller-raft/compact", nil)
    rec := httptest.NewRecorder()

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}
```

Add a response shape test for fan-out:

```go
func TestManagerControllerRaftCompactAllResponse(t *testing.T) {
    mgmt := &fakeManagement{controllerRaftCompactSummary: managementusecase.ControllerRaftCompactionSummary{
        Items: []managementusecase.ControllerRaftCompactNodeResult{{NodeID: 1, Success: true, Compacted: true}},
    }}
    srv := New(Options{ListenAddr: "127.0.0.1:0", Management: mgmt})
    req := httptest.NewRequest(http.MethodPost, "/manager/controller-raft/compact", nil)
    rec := httptest.NewRecorder()

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.JSONEq(t, `{"items":[{"node_id":1,"success":true,"compacted":true}]}`, rec.Body.String())
}
```

- [ ] **Step 2: Run manager tests and verify RED**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerControllerRaft' -count=1
```

Expected: FAIL or compile failure for missing routes/methods.

- [ ] **Step 3: Implement handlers and routes**

Extend `Management` in `server.go`:

```go
ControllerRaftStatus(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error)
CompactControllerRaftLog(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error)
CompactControllerRaftLogs(ctx context.Context) (managementusecase.ControllerRaftCompactionSummary, error)
```

Register:

```go
controllerRaftRead := manager.Group("/nodes/:node_id/controller-raft")
controllerRaftRead.Use(s.requirePermission("cluster.controller", "r"))
controllerRaftRead.GET("", s.handleControllerRaftStatus)

controllerRaftWrite := manager.Group("")
controllerRaftWrite.Use(s.requirePermission("cluster.controller", "w"))
controllerRaftWrite.POST("/nodes/:node_id/controller-raft/compact", s.handleCompactControllerRaftLog)
controllerRaftWrite.POST("/controller-raft/compact", s.handleCompactControllerRaftLogs)
```

Map invalid node ID to `400`, unavailable operator to `503`, and unexpected errors to `500`.

- [ ] **Step 4: Run manager tests**

Run:

```bash
go test ./internalv2/access/manager -count=1
```

Expected: PASS.

## Task 8: App Wiring and FLOW Updates

**Files:**
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/FLOW.md` if the manager wiring description changes
- Modify: `internalv2/access/manager/FLOW.md` if route list changes
- Modify: `internalv2/access/node/FLOW.md` if RPC list changes
- Modify: `internalv2/usecase/management/FLOW.md` if usecase responsibilities change
- Test: `internalv2/app` package tests if present

- [ ] **Step 1: Write or extend a wiring test**

If there is an existing manager wiring test, add an assertion that the manager receives a non-nil Controller Raft operator. If there is no practical wiring test, compile-time verification is acceptable through the package tests.

- [ ] **Step 2: Run the app compile test and verify RED if wiring is incomplete**

Run:

```bash
go test ./internalv2/app -count=1
```

Expected before implementation: compile failure if the new manager interface is not wired.

- [ ] **Step 3: Wire the operator**

Construct the infra router:

```go
controllerRaftOperator := clusterinfra.NewManagementControllerRaftOperator(node)
managementApp := management.New(management.AppOptions{
    LogReader: logReader,
    ControllerRaft: controllerRaftOperator,
    ...
})
```

Update `FLOW.md` files only when their route/RPC/usecase descriptions are stale.

- [ ] **Step 4: Run app and impacted internalv2 tests**

Run:

```bash
go test ./internalv2/app ./internalv2/access/manager ./internalv2/access/node ./internalv2/usecase/management ./internalv2/infra/cluster -count=1
```

Expected: PASS.

## Task 9: Web Alignment

**Files:**
- Modify: `web/src/pages/controller/page.tsx`
- Modify: `web/src/pages/controller/page.test.tsx`
- Modify: `web/src/lib/manager-api.types.ts` only if backend response names need alignment
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Run existing web controller tests first**

Run:

```bash
cd web && bun test src/pages/controller/page.test.tsx src/lib/manager-api.test.ts
```

Expected before backend work: existing frontend tests should already pass or reveal type drift. Do not use Jest-only flags.

- [ ] **Step 2: Add a permission/result test if missing**

If not already covered, add:

```tsx
it("hides compaction actions without controller write permission", async () => {
  renderControllerPage({ permissions: [{ resource: "cluster.controller", actions: ["r"] }] })
  expect(screen.queryByRole("button", { name: /trigger compaction/i })).not.toBeInTheDocument()
})
```

Add a result rendering assertion for partial failures:

```tsx
compactControllerRaftLogsMock.mockResolvedValueOnce({
  items: [
    { node_id: 1, success: true, compacted: true, skipped_reason: "" },
    { node_id: 2, success: false, compacted: false, skipped_reason: "", error: "target unavailable" },
  ],
})
```

- [ ] **Step 3: Run web tests and verify RED if changes are needed**

Run:

```bash
cd web && bun test src/pages/controller/page.test.tsx
```

Expected: FAIL only if the new permission/result behavior is missing.

- [ ] **Step 4: Align page behavior and messages**

Keep API paths unchanged:

```ts
GET  /manager/nodes/${nodeId}/controller-raft
POST /manager/nodes/${nodeId}/controller-raft/compact
POST /manager/controller-raft/compact
```

Use existing types unless backend implementation proves a mismatch. Prefer changing backend DTOs to match existing frontend where possible.

- [ ] **Step 5: Run web targeted tests**

Run:

```bash
cd web && bun test src/pages/controller/page.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS.

## Task 10: Final Verification

**Files:**
- Verify all modified packages.

- [ ] **Step 1: Run backend targeted tests**

Run:

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/... ./internalv2/infra/cluster ./internalv2/access/node ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run web targeted tests**

Run:

```bash
cd web && bun test src/pages/controller/page.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 3: Inspect generated or unrelated changes**

Run:

```bash
git status --short
git diff --stat
```

Expected: source/test/doc changes only. `web/dist/index.html` should not be changed unless a build command explicitly generated it and the user asked to keep it.

- [ ] **Step 4: Update knowledge docs only if new durable rules were discovered**

If implementation uncovers a concise project rule that affects future work, add it to `docs/development/PROJECT_KNOWLEDGE.md`. If unrelated quality issues are found, record them in `docs/development/CODE_QUALITY.md` and continue.

## Self-review

- The plan covers the spec requirements: raft manual compaction, shared helper, snapshot restore, status, clusterv2 boundary, node RPC, manager usecase/API, web trigger, and verification.
- Each production task starts with a failing test or a compile-level interface test before implementation.
- The plan stays on `pkg/controllerv2`, `pkg/clusterv2`, `internalv2`, and `web`; it does not add legacy `internal` paths.
- No new `WK_` configuration keys are introduced.
- Route and permission names match the existing web client and manager conventions.
