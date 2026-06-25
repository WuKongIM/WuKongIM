# internalv2 Dynamic Node Lifecycle Stage 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add scale-in preparation for internalv2 by marking data nodes `leaving`, stopping new placement, and reporting bounded Slot drain safety.

**Architecture:** Stage 4 introduces the shrinking-side lifecycle without allowing final removal. `MarkNodeLeaving` is a ControllerV2 `KindUpsertNode` transition, manager scale-in APIs produce fail-closed reports, and drain advancement reuses Stage 3 Slot replica move tasks to remove target nodes from Slot `DesiredPeers`. Failed Slot replica move tasks remain visible blockers; Stage 4 does not silently retry them or expose HTTP-only cancellation.

**Tech Stack:** Go, ControllerV2 lifecycle writes, clusterv2 control snapshots, internalv2 management usecase, Slot runtime status, Stage 3 `SlotReplicaMoveWriter`, manager HTTP routes.

---

## Scope

This plan implements only the "Stage 4: Scale-In Preparation" section of:

- `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage3.md`
- Next stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`

Stage 4 does not add `MarkNodeRemoved`, channel migration, gateway drain mode, or final node deletion. It makes the cluster accurately report whether Slot-level scale-in preparation is safe and provides an explicit bounded `advance` action for Slot-level drain work.

## Current-Code Corrections

This plan was updated after Stage 3 merged into local `main` at `c4275f6d`.
Execution must account for these facts:

- Stage 3 now keeps failed `slot_replica_move` tasks in ControllerV2 state and the executor skips them. Stage 4 status must treat failed tasks that reference the target node as blockers. Stage 4 `advance` must not auto-retry or overwrite them.
- `SlotReplicaMoveCommit` carries live `ObservedConfigIndex` and `ObservedVoters`. Any scale-in drain task creation must keep using `RequestSlotReplicaMove`; do not create assignment mutations directly.
- Existing placement already uses `activeDataNodeIDs(snapshot.Nodes)`. Stage 4 should add/keep regression coverage for leaving-node exclusion, but it should not introduce health-based placement filtering until durable `ReportNode` freshness exists.
- `NodeRuntimeSummary` is encoded through internalv2 node RPC. Adding `ControlRevision` requires updating `internalv2/access/node/manager_connection_codec.go` and its tests, not only the usecase type.
- `cancel` routes are deferred unless a fenced Controller command is implemented in the same stage. Do not add an HTTP route that only changes local response state.

## Entry Gate

- [x] Stage 3 is implemented, merged into local `main`, and the Stage 3 worktree is cleaned up.
- [ ] Slot onboarding e2ev2 passes:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run TestSlotReplicaMoveKeepsSendAvailable -count=1 -p=1
```

## File Structure

- Modify `pkg/controllerv2/runtime_node_lifecycle.go`
  - Adds `MarkNodeLeaving`.
- Modify `pkg/controllerv2/runtime_node_lifecycle_errors.go`
  - Reuses lifecycle conflict/not-found errors for leaving validation.
- Modify `pkg/controllerv2/runtime_test.go`
  - Verifies `active -> leaving`, idempotent leaving, and controller-voter rejection.
- Modify `pkg/clusterv2/control/node_lifecycle.go`, `pkg/clusterv2/control/control_write.go`, `pkg/clusterv2/control/codec.go`, `pkg/clusterv2/control/runtime.go`, and `pkg/clusterv2/control/transport.go`
  - Adds control facade and forwarding for leaving writes.
- Modify `pkg/clusterv2/node_management.go`
  - Exposes `Node.MarkNodeLeaving` to internalv2 infra adapters.
- Modify `pkg/clusterv2/control/runtime_test.go`, `pkg/clusterv2/control/codec_test.go`, and `pkg/clusterv2/control/transport_test.go`
  - Verifies local and forwarded `MarkNodeLeaving` writes.
- Modify `pkg/clusterv2/node_snapshot_test.go`
  - Keeps no-new-placement coverage for leaving nodes after Stage 3.
- Modify `internalv2/usecase/management/nodes.go`
  - Adds control revision freshness to runtime summaries and turns `CanScaleIn` on only for active or leaving data nodes that are not controller voters.
- Create `internalv2/usecase/management/scale_in.go`
  - Adds plan/start/status/advance usecase methods and fail-closed status model.
- Create `internalv2/usecase/management/scale_in_test.go`
  - Verifies leaving transition, revision convergence gate, Slot leader/peer blockers, failed task blockers, and unknown runtime unsafe status.
- Create `internalv2/access/manager/scale_in.go`
  - Adds manager scale-in routes.
- Create `internalv2/access/manager/scale_in_test.go`
  - Verifies route parsing, permissions, status mapping, and error mapping.
- Modify `internalv2/access/manager/server.go`
  - Registers scale-in routes.
- Modify `internalv2/access/node/manager_connection_codec.go` and `internalv2/access/node/manager_connection_codec_test.go`
  - Carries `NodeRuntimeSummary.ControlRevision` over node RPC.
- Modify `internalv2/app/runtime_summary.go`
  - Populates local `ControlRevision` from the latest local control snapshot.
- Modify `internalv2/app/*`
  - Wires node lifecycle and Slot replica move writer into scale-in usecase options.
- Modify `internalv2/infra/cluster/management_node_lifecycle.go`
  - Adapts management `MarkNodeLeaving` to clusterv2 control writes.
- Modify `internalv2/*/FLOW.md`
  - Documents leaving and scale-in route ownership.

## Task 1: Add ControllerV2 MarkNodeLeaving

**Files:**
- Modify: `pkg/controllerv2/runtime_node_lifecycle.go`
- Modify: `pkg/controllerv2/runtime_test.go`
- Modify: `pkg/clusterv2/control/node_lifecycle.go`
- Modify: `pkg/clusterv2/control/control_write.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/transport.go`
- Modify: `pkg/clusterv2/node_management.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`
- Modify: `pkg/clusterv2/control/codec_test.go`
- Modify: `pkg/clusterv2/control/transport_test.go`

- [ ] **Step 1: Write failing ControllerV2 tests**

Append to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimeMarkNodeLeavingTurnsActiveDataNodeLeaving(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-leaving")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}

	result, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() = %#v, want changed leaving node", result)
	}

	second, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() second error = %v", err)
	}
	if second.Changed || second.Revision != result.Revision || second.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() second = %#v, want idempotent unchanged leaving node", second)
	}
}

func TestRuntimeMarkNodeLeavingRejectsControllerVoter(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-controller-leaving")

	_, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 1})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeLeaving(controller voter) error = %v, want %v", err, ErrNodeLifecycleConflict)
	}
}

func TestRuntimeMarkNodeLeavingRejectsMissingNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-missing-leaving")

	_, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 99})
	if !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("MarkNodeLeaving(missing) error = %v, want %v", err, ErrNodeLifecycleNotFound)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 -run 'TestRuntimeMarkNodeLeaving' -count=1
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
	if err := ctxErr(ctx); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if r == nil || r.raft == nil {
		return MarkNodeLeavingResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	node, changed, err := buildMarkNodeLeaving(st, req)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if !changed {
		return MarkNodeLeavingResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	})
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	updated, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return MarkNodeLeavingResult{}, fmt.Errorf("controllerv2: node %d not found after leaving proposal", req.NodeID)
	}
	return MarkNodeLeavingResult{Changed: proposal.Changed, Node: finalNode, Revision: updated.Revision}, nil
}

func buildMarkNodeLeaving(st ClusterState, req MarkNodeLeavingRequest) (Node, bool, error) {
	if req.NodeID == 0 {
		return Node{}, false, fmt.Errorf("controllerv2: mark node leaving requires node id")
	}
	for _, existing := range st.Nodes {
		if existing.NodeID != req.NodeID {
			continue
		}
		if existing.HasRole(NodeRoleControllerVoter) {
			return Node{}, false, fmt.Errorf("%w: controller voter %d cannot be marked leaving", ErrNodeLifecycleConflict, req.NodeID)
		}
		if existing.JoinState == NodeJoinStateLeaving {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateActive {
			return Node{}, false, fmt.Errorf("%w: node %d is %s", ErrNodeLifecycleConflict, req.NodeID, existing.JoinState)
		}
		next := existing
		next.JoinState = NodeJoinStateLeaving
		return next, true, nil
	}
	return Node{}, false, fmt.Errorf("%w: node %d", ErrNodeLifecycleNotFound, req.NodeID)
}
```

- [ ] **Step 4: Add clusterv2 facade and control-write forwarding**

Extend `pkg/clusterv2/control/node_lifecycle.go`:

```go
// MarkNodeLeavingRequest describes a request to stop assigning new data to a node.
type MarkNodeLeavingRequest struct {
	// NodeID is the non-zero stable identity of the active data node.
	NodeID uint64 `json:"node_id"`
}

// MarkNodeLeavingResult describes the durable node record observed or updated by MarkNodeLeaving.
type MarkNodeLeavingResult struct {
	// Changed reports whether MarkNodeLeaving actually advanced cluster state.
	Changed bool `json:"changed"`
	// Node is the durable node record that satisfies the request.
	Node Node `json:"node"`
	// Revision is the cluster-state revision observed by the method.
	Revision uint64 `json:"revision"`
}

func cv2MarkNodeLeavingRequest(req MarkNodeLeavingRequest) cv2.MarkNodeLeavingRequest {
	return cv2.MarkNodeLeavingRequest{NodeID: req.NodeID}
}

func markNodeLeavingResultFromCV2(result cv2.MarkNodeLeavingResult) MarkNodeLeavingResult {
	return MarkNodeLeavingResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}
```

Add `MarkNodeLeaving` to `pkg/clusterv2/control/runtime.go` using the same leader-forwarding pattern as `ActivateNode`:

```go
// MarkNodeLeaving submits a node leaving intent.
func (r *Runtime) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if r == nil || r.backend == nil {
		return MarkNodeLeavingResult{}, cv2.ErrNotStarted
	}
	if r.canForwardControlWriteToLeader() {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{
			Action:          ControlWriteActionMarkNodeLeaving,
			MarkNodeLeaving: req,
		})
		if err != nil {
			return MarkNodeLeavingResult{}, err
		}
		return resp.MarkNodeLeaving, nil
	}
	result, err := r.backend.MarkNodeLeaving(ctx, cv2MarkNodeLeavingRequest(req))
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWriteAfterError(ctx, ControlWriteRequest{
			Action:          ControlWriteActionMarkNodeLeaving,
			MarkNodeLeaving: req,
		}, err)
		if err != nil {
			return MarkNodeLeavingResult{}, err
		}
		return resp.MarkNodeLeaving, nil
	}
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	return markNodeLeavingResultFromCV2(result), nil
}
```

Follow the existing `JoinNode`/`ActivateNode` forwarding pattern exactly: pre-forward when `canForwardControlWriteToLeader()` is true, local-write otherwise, and post-forward on `shouldForwardControlWrite(err)`.

Extend `pkg/clusterv2/control/control_write.go`:

```go
type ControlWriteApplier interface {
	JoinNode(context.Context, JoinNodeRequest) (JoinNodeResult, error)
	ActivateNode(context.Context, ActivateNodeRequest) (ActivateNodeResult, error)
	MarkNodeLeaving(context.Context, MarkNodeLeavingRequest) (MarkNodeLeavingResult, error)
	RequestSlotReplicaMove(context.Context, SlotReplicaMoveRequest) (SlotReplicaMoveResult, error)
}
```

Extend `NewControlWriteHandler` with:

```go
case ControlWriteActionMarkNodeLeaving:
	result, err := applier.MarkNodeLeaving(ctx, req.MarkNodeLeaving)
	if err != nil {
		return encodeControlWriteErrorResponse(err)
	}
	resp.MarkNodeLeaving = result
```

Extend `pkg/clusterv2/control/codec.go`:

```go
// ControlWriteActionMarkNodeLeaving submits a node leaving intent.
ControlWriteActionMarkNodeLeaving ControlWriteAction = "mark_node_leaving"

type ControlWriteRequest struct {
	Action          ControlWriteAction     `json:"action"`
	JoinNode        JoinNodeRequest        `json:"join_node,omitempty"`
	ActivateNode    ActivateNodeRequest    `json:"activate_node,omitempty"`
	MarkNodeLeaving MarkNodeLeavingRequest `json:"mark_node_leaving,omitempty"`
	SlotReplicaMove SlotReplicaMoveRequest `json:"slot_replica_move,omitempty"`
}

type controlWriteRequestJSON struct {
	Action          ControlWriteAction      `json:"action"`
	JoinNode        *JoinNodeRequest        `json:"join_node,omitempty"`
	ActivateNode    *ActivateNodeRequest    `json:"activate_node,omitempty"`
	MarkNodeLeaving *MarkNodeLeavingRequest `json:"mark_node_leaving,omitempty"`
	SlotReplicaMove *SlotReplicaMoveRequest `json:"slot_replica_move,omitempty"`
}

type ControlWriteResponse struct {
	JoinNode        JoinNodeResult        `json:"join_node,omitempty"`
	ActivateNode    ActivateNodeResult    `json:"activate_node,omitempty"`
	MarkNodeLeaving MarkNodeLeavingResult `json:"mark_node_leaving,omitempty"`
	SlotReplicaMove SlotReplicaMoveResult `json:"slot_replica_move,omitempty"`
}
```

Update `MarshalJSON` request/response branch selection for `ControlWriteActionMarkNodeLeaving`. Add codec and transport tests that round-trip JSON with snake-case `mark_node_leaving` and assert `NewControlWriteHandler` calls the applier's `MarkNodeLeaving`.

Add to `pkg/clusterv2/node_management.go`:

```go
// MarkNodeLeaving submits a Controller-backed node leaving intent.
func (n *Node) MarkNodeLeaving(ctx context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.MarkNodeLeavingResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.MarkNodeLeavingResult{}, err
	}
	if n.control == nil {
		return control.MarkNodeLeavingResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)
	})
	if !ok {
		return control.MarkNodeLeavingResult{}, ErrNotStarted
	}
	return writer.MarkNodeLeaving(ctx, req)
}
```

Do not add lifecycle writes to `TaskApplier`.

- [ ] **Step 5: Run lifecycle tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 -run 'TestRuntimeMarkNodeLeaving|TestRuntime.*MarkNodeLeaving|TestControlWrite.*MarkNodeLeaving|TestEncodeControlWrite.*MarkNodeLeaving|TestNewControlWriteHandler.*MarkNodeLeaving|TestLocalControlSnapshotReturnsClone' -count=1
```

Expected: PASS.

Commit:

```bash
git add pkg/controllerv2 pkg/clusterv2/control pkg/clusterv2/node_management.go
git commit -m "feat: mark data nodes leaving"
```

## Task 2: Lock Leaving No-New-Placement And Manager Action Hints

**Files:**
- Modify: `pkg/clusterv2/node_snapshot_test.go`
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/usecase/management/nodes_test.go`

- [ ] **Step 1: Write placement regression test**

Add to `pkg/clusterv2/node_snapshot_test.go`:

```go
func TestActiveDataNodeIDsExcludeLeavingAndRemovedNodes(t *testing.T) {
	got := activeDataNodeIDs([]control.Node{
		{NodeID: 1, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining},
		{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
		{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateRemoved},
		{NodeID: 5, Roles: []control.Role{control.RoleData}, Status: control.NodeSuspect, JoinState: control.NodeJoinStateActive},
		{NodeID: 6, Roles: []control.Role{control.RoleController}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
	})
	want := []uint64{1, 5}
	if !equalUint64s(got, want) {
		t.Fatalf("activeDataNodeIDs() = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Write manager action hint tests**

Add to `internalv2/usecase/management/nodes_test.go`:

```go
func TestListNodesReportsLifecycleActionHints(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
					{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
					{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
					{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining},
					{NodeID: 5, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateRemoved},
				},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	actions := map[uint64]NodeActions{}
	for _, item := range got.Items {
		actions[item.NodeID] = item.Actions
	}
	if actions[1].CanScaleIn || actions[1].CanOnboard {
		t.Fatalf("controller voter actions = %#v, want lifecycle actions disabled", actions[1])
	}
	if !actions[2].CanScaleIn || !actions[2].CanOnboard {
		t.Fatalf("active data actions = %#v, want scale-in and onboard enabled", actions[2])
	}
	if !actions[3].CanScaleIn || actions[3].CanOnboard {
		t.Fatalf("leaving data actions = %#v, want scale-in enabled and onboard disabled", actions[3])
	}
	if actions[4].CanScaleIn || actions[4].CanOnboard || actions[5].CanScaleIn || actions[5].CanOnboard {
		t.Fatalf("inactive data actions = %#v/%#v, want lifecycle actions disabled", actions[4], actions[5])
	}
}
```

- [ ] **Step 3: Run tests and verify RED/PASS split**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 -run TestActiveDataNodeIDsExcludeLeavingAndRemovedNodes -count=1
GOWORK=off go test ./internalv2/usecase/management -run TestListNodesReportsLifecycleActionHints -count=1
```

Expected: placement test PASS because Stage 3 already uses `activeDataNodeIDs`; action hint test FAIL because `Actions` is still empty.

- [ ] **Step 4: Implement action hints**

In `internalv2/usecase/management/nodes.go`, add helper code near `buildNode`:

```go
func nodeActions(node control.Node, controllerVoter bool) NodeActions {
	role := managerNodeRole(node.Roles)
	joinState := managerNodeJoinState(node.JoinState)
	dataOnly := role == "data" && !controllerVoter
	return NodeActions{
		CanScaleIn: dataOnly && (joinState == "active" || joinState == "leaving"),
		CanOnboard: dataOnly && joinState == "active",
	}
}
```

Replace `Actions: NodeActions{},` in `buildNode` with:

```go
Actions: nodeActions(opts.node, controllerVoter),
```

Update the existing `TestListNodesBuildsReadOnlyNodeInventory` expectation so it only asserts the controller-voter node has disabled lifecycle actions; do not keep a blanket assertion that all actions are false.

- [ ] **Step 5: Run tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 -run 'TestActiveDataNodeIDsExcludeLeavingAndRemovedNodes|TestNodeAppliesActiveDataNodesForChannelPlacement' -count=1
GOWORK=off go test ./internalv2/usecase/management -run 'TestListNodesBuildsReadOnlyNodeInventory|TestListNodesReportsLifecycleActionHints' -count=1
```

Expected: PASS. This task must not add health-based placement filtering; `activeDataNodeIDs` intentionally ignores `NodeStatus` until durable `ReportNode` freshness exists.

Commit:

```bash
git add pkg/clusterv2/node_snapshot_test.go internalv2/usecase/management/nodes.go internalv2/usecase/management/nodes_test.go
git commit -m "feat: expose node lifecycle action hints"
```

## Task 3: Add Fail-Closed Scale-In Status Usecase

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/usecase/management/node_lifecycle.go`
- Modify: `internalv2/usecase/management/node_lifecycle_test.go`
- Modify: `internalv2/infra/cluster/management_node_lifecycle.go`
- Modify: `internalv2/infra/cluster/management_node_lifecycle_test.go`
- Create: `internalv2/usecase/management/scale_in.go`
- Create: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Write failing MarkNodeLeaving usecase tests**

Append to `internalv2/usecase/management/node_lifecycle_test.go`:

```go
func TestMarkNodeLeavingDelegates(t *testing.T) {
	writer := &nodeLifecycleWriterStub{
		leavingResult: control.MarkNodeLeavingResult{
			Changed:  true,
			Revision: 23,
			Node: control.Node{
				NodeID:    4,
				Addr:      "10.0.0.4:11110",
				JoinState: control.NodeJoinStateLeaving,
			},
		},
	}
	app := New(Options{NodeLifecycle: writer})

	response, err := app.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !response.Changed || response.NodeID != 4 || response.JoinState != "leaving" || response.Revision != 23 {
		t.Fatalf("MarkNodeLeaving() = %#v, want changed leaving node response", response)
	}
	if writer.leavingReq.NodeID != 4 {
		t.Fatalf("writer leaving request = %#v, want node 4", writer.leavingReq)
	}
}

func TestMarkNodeLeavingRejectsInvalidInputAndMissingWriter(t *testing.T) {
	if _, err := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{}}).MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("MarkNodeLeaving() invalid error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); !errors.Is(err, ErrNodeLifecycleUnavailable) {
		t.Fatalf("MarkNodeLeaving() missing writer error = %v, want ErrNodeLifecycleUnavailable", err)
	}
}

func TestMarkNodeLeavingMapsControlLifecycleErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "missing", err: cv2.ErrNodeLifecycleNotFound, want: ErrNodeLifecycleNotFound},
		{name: "conflict", err: cv2.ErrNodeLifecycleConflict, want: ErrNodeLifecycleConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{leavingErr: tt.err}})

			_, err := app.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})

			if !errors.Is(err, tt.want) {
				t.Fatalf("MarkNodeLeaving() error = %v, want %v", err, tt.want)
			}
		})
	}
}
```

Extend `nodeLifecycleWriterStub` in the same test file:

```go
type nodeLifecycleWriterStub struct {
	joinReq        control.JoinNodeRequest
	joinResult     control.JoinNodeResult
	joinErr        error
	activateReq    control.ActivateNodeRequest
	activateResult control.ActivateNodeResult
	activateErr    error
	leavingReq     control.MarkNodeLeavingRequest
	leavingResult  control.MarkNodeLeavingResult
	leavingErr     error
}

func (s *nodeLifecycleWriterStub) MarkNodeLeaving(_ context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	s.leavingReq = req
	return s.leavingResult, s.leavingErr
}
```

- [ ] **Step 2: Run usecase lifecycle tests and verify RED**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestMarkNodeLeaving -count=1
```

Expected: FAIL because `MarkNodeLeavingRequest`, `MarkNodeLeavingResponse`, and `App.MarkNodeLeaving` are not defined, and `NodeLifecycleWriter` does not yet include `MarkNodeLeaving`.

- [ ] **Step 3: Implement MarkNodeLeaving usecase**

Extend `NodeLifecycleWriter` in `internalv2/usecase/management/nodes.go`:

```go
// MarkNodeLeaving submits a node leaving request to the control writer.
MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)
```

Add to `internalv2/usecase/management/node_lifecycle.go`:

```go
// MarkNodeLeavingRequest is the manager-facing scale-in start intent.
type MarkNodeLeavingRequest struct {
	// NodeID is the non-zero stable identity of the active data node.
	NodeID uint64
}

// MarkNodeLeavingResponse is returned after submitting or observing a leaving transition.
type MarkNodeLeavingResponse struct {
	// Changed reports whether the control writer advanced cluster state.
	Changed bool
	// NodeID is the durable node identity returned by control state.
	NodeID uint64
	// Addr is the durable cluster control-plane address returned by control state.
	Addr string
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Revision is the control-state revision observed by the writer.
	Revision uint64
}

// MarkNodeLeaving validates and submits a data-node leaving intent.
func (a *App) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeLeavingResponse{}, err
	}
	if req.NodeID == 0 {
		return MarkNodeLeavingResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeLifecycle == nil {
		return MarkNodeLeavingResponse{}, ErrNodeLifecycleUnavailable
	}
	result, err := a.nodeLifecycle.MarkNodeLeaving(ctx, control.MarkNodeLeavingRequest{NodeID: req.NodeID})
	if err != nil {
		return MarkNodeLeavingResponse{}, mapNodeLifecycleError(err)
	}
	return MarkNodeLeavingResponse{
		Changed:   result.Changed,
		NodeID:    result.Node.NodeID,
		Addr:      result.Node.Addr,
		JoinState: string(result.Node.JoinState),
		Revision:  result.Revision,
	}, nil
}
```

In `internalv2/infra/cluster/management_node_lifecycle.go`, extend `ManagementNodeLifecycleNode` and adapter in the same task so wider packages keep compiling:

```go
// MarkNodeLeaving submits a node leaving intent to cluster control.
MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)

func (a *ManagementNodeLifecycleAdapter) MarkNodeLeaving(ctx context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	if a == nil || a.node == nil {
		return control.MarkNodeLeavingResult{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return a.node.MarkNodeLeaving(ctx, req)
}
```

Update `internalv2/infra/cluster/management_node_lifecycle_test.go` so `TestManagementNodeLifecycleAdapterUsesControlWriter` asserts the adapter delegates `MarkNodeLeaving` to the fake node and returns the fake `control.MarkNodeLeavingResult`.

- [ ] **Step 4: Write failing scale-in status tests**

Create `internalv2/usecase/management/scale_in_test.go`:

```go
package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestScaleInStatusFailsClosedWhenRuntimeSummaryUnknown(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: scaleInSnapshot(17)},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: map[uint64]NodeRuntimeSummary{
				1: {NodeID: 1, ControlRevision: 17},
				2: {NodeID: 2, Unknown: true, ControlRevision: 17},
				3: {NodeID: 3, ControlRevision: 17},
			},
		},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.UnknownRuntime || status.BlockedByControlRevision {
		t.Fatalf("status = %#v, want fail-closed unknown runtime without stale control revision blocker", status)
	}
}

func TestScaleInStatusBlocksWhenControlRevisionIsStale(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: scaleInSnapshot(17)},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: map[uint64]NodeRuntimeSummary{
				1: {NodeID: 1, ControlRevision: 17},
				2: {NodeID: 2, ControlRevision: 16},
				3: {NodeID: 3, ControlRevision: 17},
			},
		},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedByControlRevision || status.UnknownControlRevision {
		t.Fatalf("status = %#v, want stale control revision blocker", status)
	}
}

func TestScaleInStatusBlocksWhenSlotDesiredPeersContainTarget(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: scaleInSnapshot(17)},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: scaleInRuntimeSummaries(17),
		},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedBySlots || status.SlotReplicaCount != 1 {
		t.Fatalf("status = %#v, want unsafe with one target slot replica", status)
	}
}

func TestScaleInStatusBlocksWhenRuntimeVotersStillContainTargetAfterDesiredPeersMoved(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, control.Node{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive})
	snap.Slots[0].DesiredPeers = []uint64{1, 3, 4}
	app := New(Options{
		Cluster:        fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToProceed || !status.BlockedBySlotRuntime || status.SlotReplicaCount != 1 {
		t.Fatalf("status = %#v, want unsafe with live runtime voter blocker", status)
	}
}
```

Append the test helpers in the same file:

```go
func scaleInSnapshot(revision uint64) control.Snapshot {
	return control.Snapshot{
		Revision: revision,
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
		},
	}
}

func scaleInRuntimeSummaries(revision uint64) map[uint64]NodeRuntimeSummary {
	return scaleInRuntimeSummariesFor(revision, 1, 2, 3)
}

func scaleInRuntimeSummariesFor(revision uint64, nodeIDs ...uint64) map[uint64]NodeRuntimeSummary {
	summaries := make(map[uint64]NodeRuntimeSummary, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		summaries[nodeID] = NodeRuntimeSummary{NodeID: nodeID, ControlRevision: revision}
	}
	return summaries
}
```

Extend `NodeRuntimeSummary` in `internalv2/usecase/management/nodes.go`:

```go
	// ControlRevision is the local control snapshot revision observed by the node.
	ControlRevision uint64
```

- [ ] **Step 5: Run status tests and verify RED**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatus' -count=1
```

Expected: FAIL because scale-in status types and methods are not defined.

- [ ] **Step 6: Implement fail-closed status model**

Create `internalv2/usecase/management/scale_in.go`:

```go
package management

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrNodeScaleInUnavailable reports that scale-in dependencies are unavailable.
	ErrNodeScaleInUnavailable = errors.New("internalv2/usecase/management: node scale-in unavailable")
)

// NodeScaleInStatusRequest selects one target node's scale-in safety report.
type NodeScaleInStatusRequest struct {
	// NodeID is the data node being drained.
	NodeID uint64
}

// NodeScaleInStatusResponse is a fail-closed scale-in safety report.
type NodeScaleInStatusResponse struct {
	NodeID                   uint64
	JoinState                string
	GeneratedAt              time.Time
	StateRevision            uint64
	SafeToProceed            bool
	BlockedByMissingNode     bool
	BlockedByJoinState       bool
	BlockedByControlRevision bool
	BlockedByControllerRole  bool
	BlockedBySlots           bool
	BlockedBySlotLeadership  bool
	BlockedBySlotRuntime     bool
	BlockedByTasks           bool
	UnknownRuntime           bool
	UnknownControlRevision   bool
	SlotReplicaCount         int
	SlotLeaderCount          int
	ActiveTaskCount          int
	FailedTaskCount          int
}

// NodeScaleInStatus returns whether a leaving node has drained enough for a later remove stage.
func (a *App) NodeScaleInStatus(ctx context.Context, req NodeScaleInStatusRequest) (NodeScaleInStatusResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeScaleInStatusResponse{}, err
	}
	if req.NodeID == 0 {
		return NodeScaleInStatusResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return NodeScaleInStatusResponse{}, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeScaleInStatusResponse{}, err
	}
	response := NodeScaleInStatusResponse{
		GeneratedAt:   a.now(),
		NodeID:        req.NodeID,
		StateRevision: snapshot.Revision,
	}
	node, ok := findControlNode(snapshot, req.NodeID)
	if !ok {
		response.BlockedByMissingNode = true
		return response, nil
	}
	response.JoinState = string(managerControlJoinState(node.JoinState))
	if hasRole(node.Roles, control.RoleController) {
		response.BlockedByControllerRole = true
	}
	if managerControlJoinState(node.JoinState) != control.NodeJoinStateLeaving {
		response.BlockedByJoinState = true
	}
	markScaleInRuntimeRevisionBlockers(ctx, a, snapshot, &response)
	markScaleInSlotBlockers(ctx, a, snapshot, req.NodeID, &response)
	markScaleInTaskBlockers(snapshot.Tasks, req.NodeID, &response)
	response.SafeToProceed = !response.BlockedByMissingNode &&
		!response.BlockedByJoinState &&
		!response.BlockedByControllerRole &&
		!response.BlockedByControlRevision &&
		!response.BlockedBySlots &&
		!response.BlockedBySlotLeadership &&
		!response.BlockedBySlotRuntime &&
		!response.BlockedByTasks &&
		!response.UnknownRuntime
	return response, nil
}
```

Implement the helpers in the same file with these exact rules:

```go
func markScaleInRuntimeRevisionBlockers(ctx context.Context, a *App, snapshot control.Snapshot, response *NodeScaleInStatusResponse) {
	for _, node := range snapshot.Nodes {
		status := managerNodeStatus(node.Status)
		if status != "alive" && status != "suspect" {
			continue
		}
		summary := a.nodeRuntimeSummary(ctx, node.NodeID)
		if summary.Unknown {
			response.UnknownRuntime = true
		}
		if summary.ControlRevision == 0 || summary.ControlRevision < snapshot.Revision {
			response.UnknownControlRevision = summary.ControlRevision == 0
			response.BlockedByControlRevision = true
		}
	}
}

func markScaleInSlotBlockers(ctx context.Context, a *App, snapshot control.Snapshot, target uint64, response *NodeScaleInStatusResponse) {
	assignments := append([]control.SlotAssignment(nil), snapshot.Slots...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	for _, assignment := range assignments {
		targetDesired := containsUint64(assignment.DesiredPeers, target)
		if targetDesired {
			response.SlotReplicaCount++
			response.BlockedBySlots = true
		}
		if a != nil && a.slotRuntimeStatus != nil {
			status, err := a.slotRuntimeStatus.SlotRuntimeStatus(ctx, assignment.SlotID, append([]uint64(nil), assignment.DesiredPeers...))
			if err != nil {
				response.BlockedBySlotRuntime = true
				continue
			}
			if status.LeaderID == target {
				response.SlotLeaderCount++
				response.BlockedBySlotLeadership = true
			}
			if !targetDesired && containsUint64(status.CurrentVoters, target) {
				response.BlockedBySlotRuntime = true
				response.SlotReplicaCount++
			}
		} else {
			response.BlockedBySlotRuntime = true
		}
	}
}

func markScaleInTaskBlockers(tasks []control.ReconcileTask, target uint64, response *NodeScaleInStatusResponse) {
	for _, task := range tasks {
		if !scaleInTaskReferencesNode(task, target) {
			continue
		}
		switch task.Status {
		case control.TaskStatusPending, control.TaskStatusRunning:
			response.ActiveTaskCount++
			response.BlockedByTasks = true
		case control.TaskStatusFailed:
			response.FailedTaskCount++
			response.BlockedByTasks = true
		}
	}
}

func scaleInTaskReferencesNode(task control.ReconcileTask, target uint64) bool {
	return task.SourceNode == target || task.TargetNode == target || containsUint64(task.TargetPeers, target)
}
```

- [ ] **Step 7: Run usecase tests and commit**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestMarkNodeLeaving|TestScaleInStatus' -count=1
GOWORK=off go test ./internalv2/infra/cluster -run TestManagementNodeLifecycleAdapterUsesControlWriter -count=1
GOWORK=off go test ./internalv2/app -run TestManagerServerListsNodesFromClusterSnapshot -count=1
```

Expected: PASS.

Commit:

```bash
git add internalv2/usecase/management internalv2/infra/cluster/management_node_lifecycle.go internalv2/infra/cluster/management_node_lifecycle_test.go
git commit -m "feat: report fail-closed scale-in status"
```

## Task 4: Wire Runtime Revision Reporting And Slot Drain Advance

**Files:**
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`
- Modify: `internalv2/access/node/manager_connection_codec.go`
- Create: `internalv2/access/node/manager_connection_codec_test.go`
- Modify: `internalv2/app/runtime_summary.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing runtime revision codec tests**

Create `internalv2/access/node/manager_connection_codec_test.go`:

```go
package node

import (
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerConnectionRuntimeSummaryCodecCarriesControlRevision(t *testing.T) {
	want := managementusecase.NodeRuntimeSummary{
		NodeID:               2,
		ActiveOnline:         7,
		GatewaySessions:      9,
		SessionsByListener:   map[string]int{"tcp": 9},
		AcceptingNewSessions: true,
		ControlRevision:      42,
	}

	encoded, err := encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: "ok", Summary: want})
	if err != nil {
		t.Fatalf("encodeManagerConnectionResponse() error = %v", err)
	}
	got, err := decodeManagerConnectionResponse(encoded)
	if err != nil {
		t.Fatalf("decodeManagerConnectionResponse() error = %v", err)
	}
	if got.Summary.ControlRevision != 42 || got.Summary.NodeID != 2 {
		t.Fatalf("summary = %#v, want control revision 42 for node 2", got.Summary)
	}
}
```

Add or update an app-layer test in `internalv2/app/app_test.go`:

```go
func TestManagementRuntimeSummaryReportsLocalControlRevision(t *testing.T) {
	app := &App{
		cluster: &fakeManagerCluster{
			nodeID: 2,
			snapshot: control.Snapshot{Revision: 42},
		},
	}
	reader := managementRuntimeSummaryReader{app: app, localNodeID: 2}

	got, err := reader.NodeRuntimeSummary(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeRuntimeSummary() error = %v", err)
	}
	if got.ControlRevision != 42 {
		t.Fatalf("runtime summary = %#v, want control revision 42", got)
	}
}
```

- [ ] **Step 2: Implement runtime revision propagation**

In `internalv2/usecase/management/nodes.go`, `NodeRuntimeSummary` already gained:

```go
// ControlRevision is the local control snapshot revision observed by the node.
ControlRevision uint64
```

In `internalv2/access/node/manager_connection_codec.go`, write `ControlRevision` immediately after `NodeID` so future fields remain append-only:

```go
func appendNodeRuntimeSummary(dst []byte, summary managementusecase.NodeRuntimeSummary) []byte {
	dst = appendUvarint(dst, summary.NodeID)
	dst = appendUvarint(dst, summary.ControlRevision)
	dst = appendManagerConnectionInt(dst, summary.ActiveOnline)
	// keep the existing fields in their current order after this point
	return dst
}
```

And decode it in the same position:

```go
if value, offset, err = readUvarint(body, offset); err != nil {
	return summary, offset, err
}
summary.NodeID = value
if value, offset, err = readUvarint(body, offset); err != nil {
	return summary, offset, err
}
summary.ControlRevision = value
```

In `internalv2/app/runtime_summary.go`, read the latest local control snapshot before returning:

```go
func (r managementRuntimeSummaryReader) localRuntimeSummary(nodeID uint64) managementusecase.NodeRuntimeSummary {
	summary := managementusecase.NodeRuntimeSummary{
		NodeID:             nodeID,
		SessionsByListener: map[string]int{},
		Unknown:            true,
	}
	if r.app != nil {
		if snapshots, ok := r.app.cluster.(interface {
			LocalControlSnapshot(context.Context) (control.Snapshot, error)
		}); ok && snapshots != nil {
			if snapshot, err := snapshots.LocalControlSnapshot(context.Background()); err == nil {
				summary.ControlRevision = snapshot.Revision
			}
		}
	}
	// keep the existing online/gateway summary population below this block.
}
```

- [ ] **Step 3: Write failing scale-in advance tests**

Append to `internalv2/usecase/management/scale_in_test.go`:

```go
func TestAdvanceNodeScaleInCreatesMoveAwayFromLeavingNode(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, control.Node{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive})
	writer := &fakeSlotReplicaMoveWriter{result: control.SlotReplicaMoveResult{Created: true}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:  fakeNodeRuntimeSummaryReader{summaries: scaleInRuntimeSummariesFor(17, 1, 2, 3, 4)},
		SlotReplicaMove: writer,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}}},
		},
	})

	got, err := app.AdvanceNodeScaleIn(context.Background(), NodeScaleInAdvanceRequest{NodeID: 2, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("AdvanceNodeScaleIn() error = %v", err)
	}
	if got.Created != 1 || len(writer.requests) != 1 {
		t.Fatalf("advance = %#v requests=%#v, want one created move", got, writer.requests)
	}
	req := writer.requests[0]
	if req.SlotID != 1 || req.SourceNode != 2 || req.TargetNode != 4 || req.StateRevision != 17 || req.ConfigEpoch != 7 {
		t.Fatalf("move request = %#v, want slot 1 source 2 target 4 revision 17 epoch 7", req)
	}
	if !sameUint64Slice(req.TargetPeers, []uint64{1, 4, 3}) {
		t.Fatalf("target peers = %v, want [1 4 3]", req.TargetPeers)
	}
}
```

- [ ] **Step 4: Implement plan and advance**

In `internalv2/usecase/management/scale_in.go`, add `NodeScaleInCandidate`, `NodeScaleInPlanResponse`, and `NodeScaleInAdvanceResponse` mirroring the Stage 3 onboarding response style but with `SourceNodeID` fixed to the leaving node and `TargetNodeID` as the replacement active data node.

Implement `PlanNodeScaleIn` and `AdvanceNodeScaleIn` with these rules:

```text
PlanNodeScaleIn
  - validates node_id and reads the control snapshot
  - requires target node join_state == leaving and not controller voter
  - calls NodeScaleInStatus
  - returns no candidates when BlockedByControlRevision, UnknownRuntime, BlockedByTasks, BlockedBySlotRuntime, BlockedBySlotLeadership, or BlockedByControllerRole is true
  - does not treat BlockedBySlots alone as an advance blocker because DesiredPeers containing the leaving node is the work this method drains
  - scans Slot assignments ordered by slot_id
  - only considers assignments whose DesiredPeers contain the leaving node
  - skips assignments with any active or failed task on the same Slot
  - chooses the lowest active data node not already in DesiredPeers as replacement
  - replaces the leaving node in TargetPeers without changing peer order except that position
  - returns at most normalizeNodeOnboardingMaxMoves(max_slot_moves) candidates

AdvanceNodeScaleIn
  - calls PlanNodeScaleIn with MaxSlotMoves
  - for each candidate, calls SlotReplicaMoveWriter.RequestSlotReplicaMove
  - uses SourceNode = leaving node, TargetNode = replacement node, StateRevision = plan.StateRevision, ConfigEpoch = candidate.ConfigEpoch
  - does not retry failed Controller tasks and does not overwrite existing tasks
```

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestAdvanceNodeScaleIn' -count=1
GOWORK=off go test ./internalv2/access/node -run TestManagerConnectionRuntimeSummaryCodecCarriesControlRevision -count=1
GOWORK=off go test ./internalv2/app -run TestManagementRuntimeSummaryReportsLocalControlRevision -count=1
GOWORK=off go test ./internalv2/infra/cluster -run TestManagementNodeLifecycleAdapterUsesControlWriter -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit runtime revision and scale-in advance**

```bash
git add internalv2/usecase/management internalv2/access/node internalv2/app internalv2/infra/cluster
git commit -m "feat: advance scale-in slot drain"
```

## Task 5: Add Manager Scale-In Routes And FLOW Docs

**Files:**
- Create: `internalv2/access/manager/scale_in.go`
- Create: `internalv2/access/manager/scale_in_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Write failing manager route tests**

Create `internalv2/access/manager/scale_in_test.go` following the existing `slot_onboarding_test.go` style:

```go
func TestManagerScaleInStartMarksNodeLeaving(t *testing.T) {
	var seen managementusecase.MarkNodeLeavingRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{Username: "admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"w"}}}}}),
		Management: managerNodesStub{
			markNodeLeavingReqSink: &seen,
			markNodeLeaving: managementusecase.MarkNodeLeavingResponse{Changed: true, NodeID: 4, JoinState: "leaving", Revision: 22},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/start", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	if seen.NodeID != 4 {
		t.Fatalf("request = %#v, want node 4", seen)
	}
}

func TestManagerScaleInStartReturnsOKWhenAlreadyLeaving(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{Username: "admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"w"}}}}}),
		Management: managerNodesStub{
			markNodeLeaving: managementusecase.MarkNodeLeavingResponse{Changed: false, NodeID: 4, JoinState: "leaving", Revision: 22},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/start", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

func TestManagerScaleInStatusRequiresReadPermission(t *testing.T) {
	var seen managementusecase.NodeScaleInStatusRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{Username: "reader", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}}}}),
		Management: managerNodesStub{
			scaleInStatusReqSink: &seen,
			scaleInStatus: managementusecase.NodeScaleInStatusResponse{
				NodeID: 4, JoinState: "leaving", StateRevision: 22, BlockedBySlots: true, SlotReplicaCount: 1,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/scale-in/status", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if seen.NodeID != 4 || !strings.Contains(rec.Body.String(), `"safe_to_proceed":false`) {
		t.Fatalf("request=%#v body=%s, want node 4 unsafe report", seen, rec.Body.String())
	}
}
```

- [ ] **Step 2: Implement route registration without cancel**

Extend the manager `Management` interface in `internalv2/access/manager/server.go` with:

```go
MarkNodeLeaving(context.Context, managementusecase.MarkNodeLeavingRequest) (managementusecase.MarkNodeLeavingResponse, error)
PlanNodeScaleIn(context.Context, managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInPlanResponse, error)
AdvanceNodeScaleIn(context.Context, managementusecase.NodeScaleInAdvanceRequest) (managementusecase.NodeScaleInAdvanceResponse, error)
NodeScaleInStatus(context.Context, managementusecase.NodeScaleInStatusRequest) (managementusecase.NodeScaleInStatusResponse, error)
```

Register exactly these routes:

```text
POST /manager/nodes/:node_id/scale-in/plan
POST /manager/nodes/:node_id/scale-in/start
GET  /manager/nodes/:node_id/scale-in/status
POST /manager/nodes/:node_id/scale-in/advance
```

Do not register `/scale-in/cancel` in Stage 4.

- [ ] **Step 3: Implement DTOs, handlers, and test stubs**

In `internalv2/access/manager/scale_in.go`, mirror `slot_onboarding.go` naming:

```text
ManagerNodeScaleInRequest { max_slot_moves }
ManagerNodeScaleInStatusResponse
ManagerNodeScaleInPlanResponse
ManagerNodeScaleInAdvanceResponse
```

`ManagerNodeScaleInStatusResponse` must expose every safety bit from `management.NodeScaleInStatusResponse` with snake-case JSON names:

```text
node_id, join_state, generated_at, state_revision, safe_to_proceed
blocked_by_missing_node, blocked_by_join_state, blocked_by_control_revision
blocked_by_controller_role, blocked_by_slots, blocked_by_slot_leadership
blocked_by_slot_runtime, blocked_by_tasks
unknown_runtime, unknown_control_revision
slot_replica_count, slot_leader_count, active_task_count, failed_task_count
```

Handler behavior:

```text
plan    -> PlanNodeScaleIn, returns 200
start   -> MarkNodeLeaving, returns 202 when Changed is true and 200 when Changed is false
status  -> NodeScaleInStatus, returns 200
advance -> AdvanceNodeScaleIn, returns 202
```

Error mapping:

```text
metadb.ErrInvalidArgument      -> 400 invalid_request
ErrNodeLifecycleConflict       -> 409 conflict
ErrNodeLifecycleNotFound       -> 404 not_found
ErrNodeLifecycleUnavailable    -> 503 service_unavailable
ErrNodeScaleInUnavailable      -> 503 service_unavailable
unexpected errors              -> 500 internal_error
```

Extend `managerNodesStub` in `internalv2/access/manager/server_test.go` with fields and methods for `MarkNodeLeaving`, `PlanNodeScaleIn`, `AdvanceNodeScaleIn`, and `NodeScaleInStatus`, following the existing `nodeOnboarding*ReqSink` pattern.

- [ ] **Step 4: Run manager route tests**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update FLOW docs**

Document:

```text
manager scale-in route
  -> management.App.MarkNodeLeaving / NodeScaleInStatus / AdvanceNodeScaleIn
  -> control snapshot + runtime summaries
  -> SlotReplicaMoveWriter
  -> Stage 3 slot_replica_move tasks
```

State that Stage 4 cannot remove a node and returns unsafe when data is unknown.

- [ ] **Step 6: Commit manager scale-in routes and docs**

```bash
git add internalv2/access/manager internalv2/infra/cluster/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "feat: expose scale-in preparation routes"
```

## Exit Gate

- [ ] Run full Stage 4 verification:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable' -count=1 -p=1
git diff --check
```

Expected: all commands pass.

- [ ] Confirm Stage 5 prerequisites:

```bash
rg -n "MarkNodeLeaving|mark_node_leaving|NodeScaleInStatus|AdvanceNodeScaleIn|ControlRevision|safe_to_proceed" pkg internalv2
```

Expected: leaving state, fail-closed status, and bounded Slot drain are wired without any `removed` mutation.
