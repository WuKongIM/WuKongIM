# internalv2 Dynamic Node Lifecycle Stage 5A Removed Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the ControllerV2 and clusterv2 control-plane ability to tombstone an already-drained leaving data node as `removed`.

**Architecture:** This sub-stage is control-plane only. It adds `MarkNodeRemoved` to ControllerV2, clusterv2/control forwarding, and root `clusterv2.Node` management delegation, but it does not expose a manager HTTP remove route or change scale-in safety status.

**Tech Stack:** Go, ControllerV2 `KindUpsertNode`, clusterv2/control wire codec, clusterv2 foreground node management facade.

---

## Scope

Implements only Stage 5A from:

- Stage 5 index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`
- Spec: `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`

This sub-stage must not add `/scale-in/remove`, gateway drain mode, Channel inventory, or manager safe-to-remove logic.

## Entry Gate

- [x] Stage 4 is merged into local `main`.
- [x] Existing leaving lifecycle tests pass:

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 -run 'TestRuntimeMarkNodeLeaving|TestNodeMarkNodeLeaving|TestControlWrite.*MarkNodeLeaving|TestNewControlWriteHandler.*MarkNodeLeaving' -count=1
```

Expected: PASS.

## File Structure

- Modify `pkg/controllerv2/runtime_node_lifecycle.go`
  - Adds `MarkNodeRemoved`, allowed only for non-controller data nodes already in `leaving`.
- Modify `pkg/controllerv2/runtime_test.go`
  - Verifies leaving-to-removed, idempotent removed, active rejection, controller-voter rejection, and missing-node rejection.
- Modify `pkg/clusterv2/control/node_lifecycle.go`
  - Adds clusterv2 control DTOs for removed writes.
- Modify `pkg/clusterv2/control/codec.go`
  - Adds `mark_node_removed` control-write action and snake-case wire payload.
- Modify `pkg/clusterv2/control/control_write.go`
  - Adds `ControlWriteApplier.MarkNodeRemoved`.
- Modify `pkg/clusterv2/control/runtime.go`
  - Adds local and forwarded `Runtime.MarkNodeRemoved`.
- Modify `pkg/clusterv2/control/transport.go`
  - Adds transport handler dispatch for removed writes.
- Modify `pkg/clusterv2/control/*_test.go`
  - Verifies codec, runtime forwarding, and handler dispatch.
- Modify `pkg/clusterv2/node_management.go`
  - Exposes `Node.MarkNodeRemoved`.
- Modify `pkg/clusterv2/node_management_test.go`
  - Verifies root node delegation and foreground checks.

## Task 1: Add ControllerV2 MarkNodeRemoved

**Files:**
- Modify: `pkg/controllerv2/runtime_node_lifecycle.go`
- Modify: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Write failing ControllerV2 tests**

Append these tests to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimeMarkNodeRemovedTurnsLeavingNodeRemoved(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed")
	joinAndActivateNode(t, runtime, 4, "n4")
	if _, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}

	result, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateRemoved || result.Node.Status != NodeStatusDown {
		t.Fatalf("MarkNodeRemoved() = %#v, want changed removed down node", result)
	}

	second, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() second error = %v", err)
	}
	if second.Changed || second.Revision != result.Revision || second.Node.JoinState != NodeJoinStateRemoved {
		t.Fatalf("MarkNodeRemoved() second = %#v, want idempotent removed node", second)
	}
}

func TestRuntimeMarkNodeRemovedRejectsActiveNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-active")
	joinAndActivateNode(t, runtime, 4, "n4")

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeRemoved(active) error = %v, want %v", err, ErrNodeLifecycleConflict)
	}
}

func TestRuntimeMarkNodeRemovedRejectsControllerVoter(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-controller")

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 1})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeRemoved(controller voter) error = %v, want %v", err, ErrNodeLifecycleConflict)
	}
}

func TestRuntimeMarkNodeRemovedRejectsMissingNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-missing")

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 99})
	if !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("MarkNodeRemoved(missing) error = %v, want %v", err, ErrNodeLifecycleNotFound)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

```bash
GOWORK=off go test ./pkg/controllerv2 -run 'TestRuntimeMarkNodeRemoved' -count=1
```

Expected: FAIL because `MarkNodeRemoved` is not defined.

- [ ] **Step 3: Implement ControllerV2 removed transition**

In `pkg/controllerv2/runtime_node_lifecycle.go`, add request/result structs and implementation matching the Stage 4 `MarkNodeLeaving` shape:

```go
// MarkNodeRemovedRequest identifies a leaving node that passed external drain safety.
type MarkNodeRemovedRequest struct {
	// NodeID is the stable node identity to tombstone.
	NodeID uint64
}

// MarkNodeRemovedResult describes the node record after the transition.
type MarkNodeRemovedResult struct {
	// Changed reports whether the request changed ControllerV2 state.
	Changed bool
	// Node is the durable node record after the request.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
}
```

`MarkNodeRemoved` must:

- honor `ctxErr(ctx)`;
- return `ErrNotStarted` when runtime/raft is unavailable;
- read `LocalState`;
- use `buildMarkNodeRemoved`;
- propose `command.KindUpsertNode` with `ExpectedRevision`;
- call `publishFromState`;
- reread `LocalState`;
- return the final durable node and revision.

`buildMarkNodeRemoved` must:

- reject `NodeID == 0` with a descriptive error;
- reject controller voters with `ErrNodeLifecycleConflict`;
- return unchanged when already `removed`;
- reject non-`leaving` nodes with `ErrNodeLifecycleConflict`;
- set `JoinState = NodeJoinStateRemoved` and `Status = NodeStatusDown`;
- return `ErrNodeLifecycleNotFound` for missing nodes.

- [ ] **Step 4: Verify GREEN**

```bash
GOWORK=off go test ./pkg/controllerv2 -run 'TestRuntimeMarkNodeRemoved|TestRuntimeMarkNodeLeaving' -count=1
```

Expected: PASS.

## Task 2: Add clusterv2 Control Forwarding

**Files:**
- Modify: `pkg/clusterv2/control/node_lifecycle.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/control_write.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/transport.go`
- Modify: `pkg/clusterv2/control/codec_test.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`
- Modify: `pkg/clusterv2/control/transport_test.go`

- [ ] **Step 1: Add failing control facade tests**

Add tests equivalent to the existing `MarkNodeLeaving` tests, using `MarkNodeRemovedRequest{NodeID: 4}` and `ControlWriteActionMarkNodeRemoved`. Required assertions:

- JSON uses `mark_node_removed`, not Go field names;
- follower forwards `Runtime.MarkNodeRemoved`;
- `NewControlWriteHandler` calls `ControlWriteApplier.MarkNodeRemoved`;
- `ProbePropose` not-started path returns `cv2.ErrNotStarted` for removed writes.

- [ ] **Step 2: Implement control DTO and wire codec**

Add to `pkg/clusterv2/control/node_lifecycle.go`:

```go
// MarkNodeRemovedRequest identifies a leaving node that should become a removed tombstone.
type MarkNodeRemovedRequest struct {
	// NodeID is the stable node identity to mark removed.
	NodeID uint64 `json:"node_id"`
}

// MarkNodeRemovedResult describes the removed node record.
type MarkNodeRemovedResult struct {
	// Changed reports whether control state changed.
	Changed bool `json:"changed"`
	// Node is the durable node record after the request.
	Node Node `json:"node"`
	// Revision is the observed control-state revision.
	Revision uint64 `json:"revision"`
}
```

Add conversion helpers matching existing lifecycle conversions.

- [ ] **Step 3: Implement control-write dispatch**

Add `ControlWriteActionMarkNodeRemoved = "mark_node_removed"`, extend request/response wire structs, and add a `ControlWriteApplier.MarkNodeRemoved` method. Dispatch the action in `SubmitControlWrite`.

- [ ] **Step 4: Implement runtime local/forwarded path**

`Runtime.MarkNodeRemoved` must use the same leader-forwarding pattern as `Runtime.MarkNodeLeaving`, returning the forwarded `MarkNodeRemoved` result when local runtime is not Controller leader.

- [ ] **Step 5: Verify control tests**

```bash
GOWORK=off go test ./pkg/clusterv2/control -run 'TestRuntimeMarkNodeRemoved|TestControlWrite.*MarkNodeRemoved|TestEncodeControlWrite.*MarkNodeRemoved|TestNewControlWriteHandler.*MarkNodeRemoved' -count=1
```

Expected: PASS.

## Task 3: Expose clusterv2 Node Management Delegation

**Files:**
- Modify: `pkg/clusterv2/node_management.go`
- Modify: `pkg/clusterv2/node_management_test.go`

- [ ] **Step 1: Add failing root node delegation tests**

Add tests mirroring `TestNodeMarkNodeLeavingDelegatesToControl`:

```go
func TestNodeMarkNodeRemovedDelegatesToControl(t *testing.T) {
	controller := &recordingMarkNodeRemovedController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		result: control.MarkNodeRemovedResult{
			Changed: true,
			Node:     control.Node{NodeID: 4, JoinState: control.NodeJoinStateRemoved, Status: control.NodeDown},
			Revision: 22,
		},
	}
	node := &Node{foreground: newForegroundNode(foregroundNodeOptions{controller: controller})}

	got, err := node.MarkNodeRemoved(context.Background(), control.MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !got.Changed || got.Node.JoinState != control.NodeJoinStateRemoved || controller.requests[0].NodeID != 4 {
		t.Fatalf("MarkNodeRemoved() = %#v requests=%#v, want removed node 4", got, controller.requests)
	}
}
```

- [ ] **Step 2: Implement delegation**

Add to `pkg/clusterv2/node_management.go`:

```go
// MarkNodeRemoved submits a Controller-backed node removed intent.
func (n *Node) MarkNodeRemoved(ctx context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	if err := n.checkForeground(ctx); err != nil {
		return control.MarkNodeRemovedResult{}, err
	}
	foreground := n.foreground
	if foreground == nil || foreground.controller == nil {
		return control.MarkNodeRemovedResult{}, ErrNotStarted
	}
	writer, ok := foreground.controller.(interface {
		MarkNodeRemoved(context.Context, control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error)
	})
	if !ok {
		return control.MarkNodeRemovedResult{}, ErrNotStarted
	}
	return writer.MarkNodeRemoved(ctx, req)
}
```

- [ ] **Step 3: Run Stage 5A verification**

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 -run 'TestRuntimeMarkNodeRemoved|TestNodeMarkNodeRemoved|TestControlWrite.*MarkNodeRemoved|TestEncodeControlWrite.*MarkNodeRemoved|TestNewControlWriteHandler.*MarkNodeRemoved' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 4: Commit Stage 5A**

```bash
git add pkg/controllerv2 pkg/clusterv2/control pkg/clusterv2/node_management.go pkg/clusterv2/node_management_test.go
git commit -m "feat: mark drained nodes removed"
```

## Exit Gate

```bash
GOWORK=off go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2 -count=1
git diff --check
rg -n "MarkNodeRemoved|mark_node_removed|NodeJoinStateRemoved" pkg/controllerv2 pkg/clusterv2
```

Expected: all commands pass, and no manager HTTP remove route exists yet.
