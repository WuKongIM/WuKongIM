# internalv2 Dynamic Node Lifecycle Stage 5D Safe Remove Route Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the final manager remove route while guaranteeing `MarkNodeRemoved` is called only when `NodeScaleInStatus.SafeToRemove` is true.

**Architecture:** This sub-stage connects the Stage 5A control-plane removed tombstone to internalv2 management and manager HTTP. The remove usecase reuses the Stage 5B/5C fail-closed status report; unsafe remove attempts return conflict and do not call the lifecycle writer.

**Tech Stack:** Go, internalv2 management usecase, clusterv2 management lifecycle adapter, manager HTTP routes and DTOs.

---

## Scope

Implements only Stage 5D from:

- Stage 5 index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`
- Previous sub-stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5c-gateway-drain-mode.md`

This sub-stage must not add e2ev2 remove smoke or FLOW documentation beyond route-local comments. Stage 5E owns black-box validation and docs.

## Entry Gate

- [ ] Stage 5C is merged into local `main`.
- [ ] Runtime drain status and drain route tests pass:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestSetNodeDrainMode' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleInDrain|TestManagerScaleIn' -count=1
```

Expected: PASS.

## File Structure

- Modify `internalv2/usecase/management/nodes.go`
  - Extends `NodeLifecycleWriter` with `MarkNodeRemoved`.
- Modify `internalv2/usecase/management/node_lifecycle.go`
  - Adds `MarkNodeRemoved` response mapping and lifecycle error mapping.
- Modify `internalv2/usecase/management/scale_in.go`
  - Adds `MarkNodeRemoved` safe gate and `ErrNodeScaleInUnsafe`.
- Modify `internalv2/usecase/management/scale_in_test.go`
  - Verifies unsafe remove blocks writer and safe remove delegates.
- Modify `internalv2/infra/cluster/management_node_lifecycle.go`
  - Adapts management `MarkNodeRemoved` to clusterv2.
- Modify `internalv2/infra/cluster/management_node_lifecycle_test.go`
  - Verifies adapter delegation.
- Modify `internalv2/app/app_test.go`
  - Updates fake manager cluster method set.
- Modify `internalv2/access/manager/scale_in.go`
  - Exposes `safe_to_remove` and Stage 5 blocker fields in status DTO.
- Create `internalv2/access/manager/scale_in_remove.go`
  - Adds final safe remove handler.
- Create `internalv2/access/manager/scale_in_remove_test.go`
  - Verifies unsafe conflict, safe accepted, idempotent OK, permission, and error mapping.
- Modify `internalv2/access/manager/server.go`
  - Registers `POST /manager/nodes/:node_id/scale-in/remove`.
- Modify `internalv2/access/manager/server_test.go`
  - Extends manager stub with `MarkNodeRemoved`.

## Task 1: Add Management MarkNodeRemoved Safe Gate

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/usecase/management/node_lifecycle.go`
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Write failing usecase tests**

Add to `internalv2/usecase/management/scale_in_test.go`:

```go
func TestMarkNodeRemovedRequiresSafeToRemove(t *testing.T) {
	writer := &nodeLifecycleWriterStub{}
	snap := scaleInSnapshotWithLeavingNode()
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{snapshot: snap},
		NodeLifecycle: writer,
	})

	_, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeScaleInUnsafe) {
		t.Fatalf("MarkNodeRemoved() error = %v, want ErrNodeScaleInUnsafe", err)
	}
	if writer.removedReq.NodeID != 0 {
		t.Fatalf("writer removed request = %#v, want not called", writer.removedReq)
	}
}

func TestMarkNodeRemovedDelegatesWhenSafe(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	writer := &nodeLifecycleWriterStub{removedResult: control.MarkNodeRemovedResult{
		Changed:  true,
		Node:     control.Node{NodeID: 4, JoinState: control.NodeJoinStateRemoved},
		Revision: snap.Revision + 1,
	}}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{
			1: {NodeID: 1, ControlRevision: snap.Revision},
			2: {NodeID: 2, ControlRevision: snap.Revision},
			3: {NodeID: 3, ControlRevision: snap.Revision},
			4: {NodeID: 4, ControlRevision: snap.Revision, Draining: true, AcceptingNewSessions: false},
		}},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: emptyChannelDrainMetaReader{},
		NodeLifecycle:      writer,
	})

	resp, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !resp.Changed || resp.NodeID != 4 || resp.JoinState != "removed" || writer.removedReq.NodeID != 4 {
		t.Fatalf("response=%#v request=%#v, want removed node 4", resp, writer.removedReq)
	}
}
```

Extend `nodeLifecycleWriterStub` in `node_lifecycle_test.go` or a shared test file with:

```go
removedReq    control.MarkNodeRemovedRequest
removedResult control.MarkNodeRemovedResult
removedErr    error

func (s *nodeLifecycleWriterStub) MarkNodeRemoved(_ context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	s.removedReq = req
	return s.removedResult, s.removedErr
}
```

- [ ] **Step 2: Verify RED**

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestMarkNodeRemoved -count=1
```

Expected: FAIL because management `MarkNodeRemoved` does not exist.

- [ ] **Step 3: Implement management safe gate**

Extend `NodeLifecycleWriter` in `nodes.go`:

```go
// MarkNodeRemoved submits a node removed request to the control writer.
MarkNodeRemoved(context.Context, control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error)
```

Add to `scale_in.go`:

```go
var ErrNodeScaleInUnsafe = errors.New("internalv2/usecase/management: node scale-in unsafe")

type MarkNodeRemovedRequest struct {
	NodeID uint64
}

type MarkNodeRemovedResponse struct {
	Changed   bool
	NodeID    uint64
	JoinState string
	Revision  uint64
}
```

Implement:

```go
func (a *App) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeRemovedResponse{}, err
	}
	if req.NodeID == 0 {
		return MarkNodeRemovedResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeLifecycle == nil {
		return MarkNodeRemovedResponse{}, ErrNodeLifecycleUnavailable
	}
	status, err := a.NodeScaleInStatus(ctx, NodeScaleInStatusRequest{NodeID: req.NodeID})
	if err != nil {
		return MarkNodeRemovedResponse{}, err
	}
	if !status.SafeToRemove {
		return MarkNodeRemovedResponse{}, ErrNodeScaleInUnsafe
	}
	result, err := a.nodeLifecycle.MarkNodeRemoved(ctx, control.MarkNodeRemovedRequest{NodeID: req.NodeID})
	if err != nil {
		return MarkNodeRemovedResponse{}, mapNodeLifecycleError(err)
	}
	return MarkNodeRemovedResponse{
		Changed: result.Changed,
		NodeID: result.Node.NodeID,
		JoinState: string(result.Node.JoinState),
		Revision: result.Revision,
	}, nil
}
```

- [ ] **Step 4: Verify usecase GREEN**

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestMarkNodeRemoved|TestScaleInStatus' -count=1
```

Expected: PASS.

## Task 2: Wire Infra And App Test Fakes

**Files:**
- Modify: `internalv2/infra/cluster/management_node_lifecycle.go`
- Modify: `internalv2/infra/cluster/management_node_lifecycle_test.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Add adapter tests**

Extend `TestManagementNodeLifecycleAdapterUsesControlWriter` to call:

```go
removed, err := adapter.MarkNodeRemoved(context.Background(), control.MarkNodeRemovedRequest{NodeID: 4})
if err != nil {
	t.Fatalf("MarkNodeRemoved() error = %v", err)
}
if !removed.Changed || node.removedRequest.NodeID != 4 {
	t.Fatalf("MarkNodeRemoved() = %#v request=%#v, want changed removed node 4", removed, node.removedRequest)
}
```

- [ ] **Step 2: Implement adapter**

Extend `ManagementNodeLifecycleNode` and `ManagementNodeLifecycleAdapter` with `MarkNodeRemoved`. Nil adapter should return `ErrNodeLifecycleUnavailable`, matching join/activate/leaving behavior.

- [ ] **Step 3: Update app fake cluster**

In `internalv2/app/app_test.go`, add `MarkNodeRemoved` fields and method to `fakeManagerCluster` so it continues to satisfy the expanded lifecycle interface:

```go
markNodeRemovedRequest control.MarkNodeRemovedRequest
markNodeRemovedResult  control.MarkNodeRemovedResult
markNodeRemovedErr     error

func (f *fakeManagerCluster) MarkNodeRemoved(_ context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	f.markNodeRemovedRequest = req
	return f.markNodeRemovedResult, f.markNodeRemovedErr
}
```

- [ ] **Step 4: Verify infra/app**

```bash
GOWORK=off go test ./internalv2/infra/cluster -run TestManagementNodeLifecycleAdapterUsesControlWriter -count=1
GOWORK=off go test ./internalv2/app -run TestManagerServerJoinsNodeFromClusterControl -count=1
```

Expected: PASS.

## Task 3: Add Manager Remove Route And Status DTO Fields

**Files:**
- Modify: `internalv2/access/manager/scale_in.go`
- Create: `internalv2/access/manager/scale_in_remove.go`
- Create: `internalv2/access/manager/scale_in_remove_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Add failing manager route tests**

Create `internalv2/access/manager/scale_in_remove_test.go`:

```go
func TestManagerScaleInRemoveBlocksUnsafeStatus(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{Username: "admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"w"}}}}}),
		Management: managerNodesStub{markNodeRemovedErr: managementusecase.ErrNodeScaleInUnsafe},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestManagerScaleInRemoveReturnsAcceptedWhenChanged(t *testing.T) {
	var seen managementusecase.MarkNodeRemovedRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{Username: "admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"w"}}}}}),
		Management: managerNodesStub{
			markNodeRemovedReqSink: &seen,
			markNodeRemoved: managementusecase.MarkNodeRemovedResponse{Changed: true, NodeID: 4, JoinState: "removed", Revision: 33},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted || seen.NodeID != 4 {
		t.Fatalf("status=%d request=%#v body=%s, want accepted node 4", rec.Code, seen, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"changed":true,"node_id":4,"join_state":"removed","revision":33}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Extend manager interface and stubs**

Add to `Management` in `server.go`:

```go
// MarkNodeRemoved marks a fully drained node removed.
MarkNodeRemoved(ctx context.Context, req managementusecase.MarkNodeRemovedRequest) (managementusecase.MarkNodeRemovedResponse, error)
```

Extend `managerNodesStub` with response, error, sink, and method.

- [ ] **Step 3: Expose Stage 5 status fields**

Add these JSON fields to `ManagerNodeScaleInStatusResponse` in `scale_in.go` and map them from usecase response:

```go
SafeToRemove            bool `json:"safe_to_remove"`
BlockedByChannels      bool `json:"blocked_by_channels"`
UnknownChannelInventory bool `json:"unknown_channel_inventory"`
BlockedByRuntimeDrain  bool `json:"blocked_by_runtime_drain"`
RuntimeUnknown          bool `json:"runtime_unknown"`
GatewayDraining         bool `json:"gateway_draining"`
AcceptingNewSessions   bool `json:"accepting_new_sessions"`
GatewaySessions         int  `json:"gateway_sessions"`
ActiveOnline            int  `json:"active_online"`
ClosingOnline           int  `json:"closing_online"`
TotalOnline             int  `json:"total_online"`
PendingActivations      int  `json:"pending_activations"`
ChannelLeaderCount      int  `json:"channel_leader_count"`
ChannelReplicaCount     int  `json:"channel_replica_count"`
ChannelISRCount         int  `json:"channel_isr_count"`
```

- [ ] **Step 4: Implement remove handler**

Create `scale_in_remove.go`:

```go
type ManagerNodeScaleInRemoveResponse struct {
	Changed   bool   `json:"changed"`
	NodeID    uint64 `json:"node_id"`
	JoinState string `json:"join_state"`
	Revision  uint64 `json:"revision"`
}
```

Handler:

- parse `node_id`;
- call `s.management.MarkNodeRemoved`;
- map `ErrNodeScaleInUnsafe` to `409 conflict`;
- return `202 Accepted` when `Changed=true`;
- return `200 OK` when `Changed=false`.

Register:

```go
nodeWrites.POST("/nodes/:node_id/scale-in/remove", s.handleNodeScaleInRemove)
```

- [ ] **Step 5: Verify manager**

```bash
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleInRemove|TestManagerScaleInStatus' -count=1
```

Expected: PASS.

## Task 4: Run Stage 5D Verification And Commit

- [ ] **Step 1: Run focused verification**

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestMarkNodeRemoved|TestScaleInStatus' -count=1
GOWORK=off go test ./internalv2/infra/cluster -run TestManagementNodeLifecycleAdapterUsesControlWriter -count=1
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleInRemove|TestManagerScaleIn' -count=1
GOWORK=off go test ./internalv2/app -run TestManagerServerJoinsNodeFromClusterControl -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 2: Commit Stage 5D**

```bash
git add internalv2/usecase/management internalv2/infra/cluster internalv2/access/manager internalv2/app/app_test.go
git commit -m "feat: gate node removal on drain safety"
```

## Exit Gate

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/infra/cluster ./internalv2/access/manager ./internalv2/app -count=1
git diff --check
rg -n "MarkNodeRemoved|ErrNodeScaleInUnsafe|safe_to_remove|scale-in/remove" internalv2
```

Expected: all commands pass. The only manager route that can mark `removed` is `/manager/nodes/:node_id/scale-in/remove`, and it is gated by `SafeToRemove`. Direct lower-level `pkg/controllerv2` and `pkg/clusterv2/control` lifecycle writers remain primitives used by the management usecase, not operator entrypoints.
