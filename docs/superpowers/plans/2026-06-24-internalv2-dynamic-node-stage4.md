# internalv2 Dynamic Node Lifecycle Stage 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add scale-in preparation for internalv2 by marking data nodes `leaving`, stopping new placement, and reporting bounded Slot drain safety.

**Architecture:** Stage 4 introduces the shrinking-side lifecycle without allowing final removal. `MarkNodeLeaving` is a ControllerV2 `KindUpsertNode` transition, manager scale-in APIs produce fail-closed reports, and drain advancement reuses Stage 3 Slot replica move tasks to remove target nodes from Slot `DesiredPeers`.

**Tech Stack:** Go, ControllerV2 lifecycle writes, clusterv2 control snapshots, internalv2 management usecase, Slot runtime status, Stage 3 `SlotReplicaMoveWriter`, manager HTTP routes.

---

## Scope

This plan implements only the "Stage 4: Scale-In Preparation" section of:

- `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage3.md`
- Next stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`

Stage 4 does not add `MarkNodeRemoved`, channel migration, or gateway drain mode. It makes the cluster accurately report whether Slot-level scale-in preparation is safe.

## Entry Gate

- [ ] Stage 3 is implemented and committed.
- [ ] Slot onboarding e2ev2 passes:

```bash
go test ./test/e2ev2/cluster/dynamic_node_join -run TestSlotReplicaMoveKeepsSendAvailable -count=1
```

## File Structure

- Modify `pkg/controllerv2/runtime_node_lifecycle.go`
  - Adds `MarkNodeLeaving`.
- Modify `pkg/controllerv2/runtime_test.go`
  - Verifies `active -> leaving`, idempotent leaving, and controller-voter rejection.
- Modify `pkg/clusterv2/control/node_lifecycle.go`, `pkg/clusterv2/control/codec.go`, `pkg/clusterv2/control/runtime.go`, and `pkg/clusterv2/control/transport.go`
  - Adds control facade and forwarding for leaving writes.
- Modify `pkg/clusterv2/node_snapshot_test.go`
  - Strengthens no-new-placement coverage for leaving nodes after Stage 3.
- Modify `internalv2/usecase/management/nodes.go`
  - Adds control revision freshness to runtime summaries and turns `CanScaleIn` on only for active or leaving data nodes that are not controller voters.
- Create `internalv2/usecase/management/scale_in.go`
  - Adds plan/start/status/advance/cancel usecase methods and fail-closed status model.
- Create `internalv2/usecase/management/scale_in_test.go`
  - Verifies leaving transition, revision convergence gate, Slot leader/peer blockers, failed task blockers, and unknown runtime unsafe status.
- Create `internalv2/access/manager/scale_in.go`
  - Adds manager scale-in routes.
- Create `internalv2/access/manager/scale_in_test.go`
  - Verifies route parsing, permissions, status mapping, and error mapping.
- Modify `internalv2/access/manager/server.go`
  - Registers scale-in routes.
- Modify `internalv2/app/*`
  - Wires node lifecycle and Slot replica move writer into scale-in usecase options.
- Modify `internalv2/infra/cluster/management_connections.go`
  - Includes per-node observed control revision in runtime summary RPC projection.
- Modify `internalv2/*/FLOW.md`
  - Documents leaving and scale-in route ownership.

## Task 1: Add ControllerV2 MarkNodeLeaving

**Files:**
- Modify: `pkg/controllerv2/runtime_node_lifecycle.go`
- Modify: `pkg/controllerv2/runtime_test.go`
- Modify: `pkg/clusterv2/control/node_lifecycle.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/transport.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`

- [ ] **Step 1: Write failing ControllerV2 tests**

Append to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimeMarkNodeLeavingTurnsActiveDataNodeLeaving(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-leaving")
	joinAndActivateNode(t, runtime, 4, "n4")

	result, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() = %#v, want changed leaving node", result)
	}
}

func TestRuntimeMarkNodeLeavingRejectsControllerVoter(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-controller-leaving")

	_, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 1})
	if err == nil {
		t.Fatal("MarkNodeLeaving(controller voter) error = nil, want rejection")
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntimeMarkNodeLeaving' -count=1
```

Expected: FAIL because `MarkNodeLeaving` is not defined.

- [ ] **Step 3: Implement MarkNodeLeaving**

In `pkg/controllerv2/runtime_node_lifecycle.go`, add:

```go
// MarkNodeLeavingRequest identifies a data node that should stop receiving new assignments.
type MarkNodeLeavingRequest struct {
	// NodeID is the stable node identity to mark leaving.
	NodeID uint64
}

// MarkNodeLeavingResult describes the node record after the transition.
type MarkNodeLeavingResult struct {
	// Changed reports whether the request changed ControllerV2 state.
	Changed bool
	// Node is the durable node record after the request.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
}

// MarkNodeLeaving marks an active data node as leaving so planners stop new placement.
func (r *Runtime) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	if req.NodeID == 0 {
		return MarkNodeLeavingResult{}, fmt.Errorf("controllerv2: node id is required")
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	node, changed, err := buildMarkNodeLeaving(st, req.NodeID)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if !changed {
		return MarkNodeLeavingResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	if err := r.raft.Propose(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         time.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	st, err = r.LocalState(ctx)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	return MarkNodeLeavingResult{Changed: true, Node: node, Revision: st.Revision}, nil
}

func buildMarkNodeLeaving(st ClusterState, nodeID uint64) (Node, bool, error) {
	for _, existing := range st.Nodes {
		if existing.NodeID != nodeID {
			continue
		}
		if existing.HasRole(NodeRoleControllerVoter) {
			return Node{}, false, fmt.Errorf("controllerv2: controller voter %d cannot be marked leaving", nodeID)
		}
		if existing.JoinState == NodeJoinStateLeaving {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateActive {
			return Node{}, false, fmt.Errorf("controllerv2: node %d is %q, want active", nodeID, existing.JoinState)
		}
		next := existing
		next.JoinState = NodeJoinStateLeaving
		return next, true, nil
	}
	return Node{}, false, fmt.Errorf("controllerv2: node %d not found", nodeID)
}
```

- [ ] **Step 4: Add clusterv2 control-write forwarding**

Extend `pkg/clusterv2/control/node_lifecycle.go`:

```go
type MarkNodeLeavingRequest struct {
	NodeID uint64
}

type MarkNodeLeavingResult struct {
	Changed  bool
	Node     Node
	Revision uint64
}
```

Extend the Stage 2 generic `ControlWriteAction` with:

```go
	ControlWriteActionMarkNodeLeaving ControlWriteAction = "mark_node_leaving"
```

Add `MarkNodeLeaving` to `ControlWriteApplier`, `ControlWriteRequest`, `ControlWriteResponse`, `NewControlWriteHandler`, and `Runtime` using the same forwarding pattern as `ActivateNode`. Do not add lifecycle writes to `TaskApplier`.

- [ ] **Step 5: Run lifecycle tests and commit**

Run:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/control -run 'TestRuntimeMarkNodeLeaving|TestControlWriteRequest.*Leaving' -count=1
```

Expected: PASS.

Commit:

```bash
git add pkg/controllerv2 pkg/clusterv2/control
git commit -m "feat: mark data nodes leaving"
```

## Task 2: Strengthen No-New-Placement For Leaving Nodes

**Files:**
- Modify: `pkg/clusterv2/node_snapshot_test.go`
- Modify: `pkg/clusterv2/node_snapshot.go`
- Modify: `pkg/clusterv2/control/snapshot_validate.go`

- [ ] **Step 1: Write placement regression test**

Add to `pkg/clusterv2/node_snapshot_test.go`:

```go
func TestChannelDataNodesExcludeLeavingDataNodes(t *testing.T) {
	snap := control.Snapshot{
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		},
	}
	got := channelDataNodes(snap)
	if !reflect.DeepEqual(got, []uint64{1, 3}) {
		t.Fatalf("channelDataNodes() = %v, want [1 3]", got)
	}
}
```

- [ ] **Step 2: Run regression test**

Run:

```bash
go test ./pkg/clusterv2 -run TestChannelDataNodesExcludeLeavingDataNodes -count=1
```

Expected: PASS. A failure means Stage 1's active-only placement gate is incomplete and must be fixed before continuing Stage 4.

- [ ] **Step 3: Commit placement regression**

Run:

```bash
git add pkg/clusterv2/node_snapshot_test.go
git commit -m "test: cover leaving placement exclusion"
```

If Step 2 failed, stop this Stage and repair the Stage 1 candidate filtering before this commit:

```bash
git add pkg/clusterv2/node_snapshot.go pkg/clusterv2/control/snapshot_validate.go pkg/clusterv2/node_snapshot_test.go
git commit -m "fix: exclude leaving nodes from placement"
```

## Task 3: Add Fail-Closed Scale-In Status Usecase

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Create: `internalv2/usecase/management/scale_in.go`
- Create: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Write failing status tests**

Create `internalv2/usecase/management/scale_in_test.go`:

```go
func TestScaleInStatusFailsClosedWhenControlRevisionUnknown(t *testing.T) {
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: scaleInSnapshot(17)},
		RuntimeSummary: fakeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{
			1: {NodeID: 1, ControlRevision: 17},
			2: {NodeID: 2, Unknown: true},
		}},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed {
		t.Fatalf("SafeToProceed = true, want false")
	}
	if !status.BlockedByControlRevision {
		t.Fatalf("BlockedByControlRevision = false, want true")
	}
}

func TestScaleInStatusBlocksWhenSlotDesiredPeersContainTarget(t *testing.T) {
	app := NewApp(Options{Control: fakeControlSnapshotReader{snap: scaleInSnapshot(17)}})
	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || status.SlotReplicaCount == 0 {
		t.Fatalf("status = %#v, want unsafe with slot replicas", status)
	}
}
```

Extend `NodeRuntimeSummary` in `internalv2/usecase/management/nodes.go`:

```go
	// ControlRevision is the local control snapshot revision observed by the node.
	ControlRevision uint64
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestScaleInStatus' -count=1
```

Expected: FAIL because scale-in status types and methods are not defined.

- [ ] **Step 3: Implement status model**

Create `internalv2/usecase/management/scale_in.go`:

```go
type NodeLifecycleWriter interface {
	JoinNode(context.Context, control.JoinNodeRequest) (control.JoinNodeResult, error)
	ActivateNode(context.Context, control.ActivateNodeRequest) (control.ActivateNodeResult, error)
	MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)
}

type NodeScaleInStatusRequest struct {
	NodeID uint64
}

type NodeScaleInStatusResponse struct {
	NodeID                   uint64
	JoinState                string
	StateRevision            uint64
	SafeToProceed            bool
	BlockedByControlRevision bool
	BlockedByControllerRole  bool
	BlockedBySlots           bool
	BlockedBySlotLeadership  bool
	BlockedByTasks           bool
	UnknownRuntime           bool
	SlotReplicaCount         int
	SlotLeaderCount          int
	ActiveTaskCount          int
	FailedTaskCount          int
}
```

`NodeScaleInStatus` must return unsafe when:

- target node is missing or not `leaving`;
- target has controller role;
- any alive or suspect node has unknown runtime summary;
- any alive or suspect node reports `ControlRevision < snapshot.Revision`;
- any Slot `DesiredPeers` contains the target;
- live Slot status reports the target as leader;
- any active or failed Controller task references the target.

- [ ] **Step 4: Add MarkNodeLeaving usecase**

Add:

```go
type MarkNodeLeavingRequest struct {
	NodeID uint64
}

type MarkNodeLeavingResponse struct {
	Changed   bool
	NodeID    uint64
	JoinState string
	Revision  uint64
}

func (a *App) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResponse, error) {
	if req.NodeID == 0 || a.nodeLifecycle == nil {
		return MarkNodeLeavingResponse{}, metadb.ErrInvalidArgument
	}
	result, err := a.nodeLifecycle.MarkNodeLeaving(ctx, control.MarkNodeLeavingRequest{NodeID: req.NodeID})
	if err != nil {
		return MarkNodeLeavingResponse{}, err
	}
	return MarkNodeLeavingResponse{Changed: result.Changed, NodeID: result.Node.NodeID, JoinState: string(result.Node.JoinState), Revision: result.Revision}, nil
}
```

- [ ] **Step 5: Run usecase tests and commit**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestMarkNodeLeaving' -count=1
```

Expected: PASS.

Commit:

```bash
git add internalv2/usecase/management
git commit -m "feat: report fail-closed scale-in status"
```

## Task 4: Add Manager Scale-In Routes

**Files:**
- Create: `internalv2/access/manager/scale_in.go`
- Create: `internalv2/access/manager/scale_in_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Create `internalv2/access/manager/scale_in_test.go`:

```go
func TestManagerScaleInStartMarksNodeLeaving(t *testing.T) {
	stub := &managerNodesStub{markNodeLeavingResponse: managementusecase.MarkNodeLeavingResponse{Changed: true, NodeID: 4, JoinState: "leaving", Revision: 22}}
	srv := newTestServer(t, stub)

	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/start", nil)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
}

func TestManagerScaleInStatusReturnsUnsafeReport(t *testing.T) {
	stub := &managerNodesStub{scaleInStatusResponse: managementusecase.NodeScaleInStatusResponse{
		NodeID: 4, JoinState: "leaving", StateRevision: 22, SlotReplicaCount: 1, BlockedBySlots: true,
	}}
	srv := newTestServer(t, stub)

	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/scale-in/status", nil)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"safe_to_proceed":false`) {
		t.Fatalf("body=%s, want unsafe report", rec.Body.String())
	}
}
```

- [ ] **Step 2: Implement route registration**

Register:

```text
POST /manager/nodes/:node_id/scale-in/plan
POST /manager/nodes/:node_id/scale-in/start
GET  /manager/nodes/:node_id/scale-in/status
POST /manager/nodes/:node_id/scale-in/advance
POST /manager/nodes/:node_id/scale-in/cancel
```

Route behavior:

- `plan`: returns `NodeScaleInStatus` plus candidate Slot moves, read permission.
- `start`: calls `MarkNodeLeaving`, write permission.
- `status`: returns fail-closed status, read permission.
- `advance`: creates bounded Slot replica move tasks away from target, write permission.
- `cancel`: fails pending scale-in tasks with reason `operator_cancelled`, write permission.

- [ ] **Step 3: Run manager route tests**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit manager scale-in routes**

```bash
git add internalv2/access/manager
git commit -m "feat: expose scale-in preparation routes"
```

## Task 5: Wire Runtime Revision Reporting And Slot Drain Advance

**Files:**
- Modify: `internalv2/infra/cluster/management_connections.go`
- Modify: `internalv2/app/*`
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Add control revision to runtime summary projection**

In the local and remote runtime summary producers, set:

```go
summary.ControlRevision = currentControlSnapshot.Revision
```

When the summary cannot read a local control snapshot, return `Unknown: true` so `NodeScaleInStatus` fails closed.

- [ ] **Step 2: Implement scale-in advance with Slot move writer**

`AdvanceNodeScaleIn` must:

- require target node `leaving`;
- call `NodeScaleInStatus`;
- skip if `BlockedByControlRevision` or `UnknownRuntime`;
- find Slot assignments containing target;
- choose up to `max_slot_moves` Slots;
- choose an active data target not already in the Slot `DesiredPeers`;
- call `RequestSlotReplicaMove` with `SourceNode` equal to the leaving node.

- [ ] **Step 3: Run focused tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestAdvanceNodeScaleIn' -count=1
go test ./internalv2/infra/cluster -run TestManagementConnectionReader -count=1
go test ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 4: Update FLOW docs**

Document:

```text
manager scale-in route
  -> management.App.MarkNodeLeaving / NodeScaleInStatus / AdvanceNodeScaleIn
  -> control snapshot + runtime summaries
  -> SlotReplicaMoveWriter
  -> Stage 3 slot_replica_move tasks
```

State that Stage 4 cannot remove a node and returns unsafe when data is unknown.

- [ ] **Step 5: Commit wiring and docs**

```bash
git add internalv2/app internalv2/infra/cluster internalv2/usecase/management internalv2/infra/cluster/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "feat: advance scale-in slot drain"
```

## Exit Gate

- [ ] Run full Stage 4 verification:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/infra/cluster ./internalv2/app
go test ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable' -count=1
git diff --check
```

Expected: all commands pass.

- [ ] Confirm Stage 5 prerequisites:

```bash
rg -n "MarkNodeLeaving|mark_node_leaving|NodeScaleInStatus|AdvanceNodeScaleIn|ControlRevision|safe_to_proceed" pkg internalv2
```

Expected: leaving state, fail-closed status, and bounded Slot drain are wired without any `removed` mutation.
