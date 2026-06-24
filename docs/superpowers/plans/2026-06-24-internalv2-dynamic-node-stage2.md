# internalv2 Dynamic Node Lifecycle Stage 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add ControllerV2-backed dynamic data-node join and activation for internalv2 without moving existing Slot replicas.

**Architecture:** Stage 2 uses ControllerV2 as the only source of durable membership. A joining node starts from seed discovery as a Controller mirror, syncs a valid control snapshot, asks the Controller leader to add a `joining` data-node record, and becomes schedulable only after an explicit `ActivateNode` write changes it to `active`.

**Tech Stack:** Go, ControllerV2 `KindUpsertNode`, clusterv2 generic control-write forwarding, internalv2 manager HTTP/usecase ports, seed-join config from Stage 1, typed node RPC, e2ev2 black-box harness.

---

## Scope

This plan implements only the "Stage 2: Dynamic Join And Activation" section of:

- `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage1.md`
- Next stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage3.md`

Stage 2 does not add Slot learner movement, onboarding APIs, leaving state, drain mode, or `removed` writes. Existing Slot `DesiredPeers` must remain unchanged when a new node joins and activates.

## Entry Gate

- [ ] Stage 1 is implemented and committed.
- [ ] These focused tests pass on the Stage 1 branch:

```bash
go test ./pkg/controllerv2/state ./pkg/clusterv2/control ./pkg/clusterv2 ./cmd/wukongimv2 ./internalv2/usecase/management
```

## File Structure

- Create `pkg/controllerv2/runtime_node_lifecycle.go`
  - Adds `JoinNode`, `ActivateNode`, request/result structs, idempotency checks, and conflict checks.
- Modify `pkg/controllerv2/runtime_test.go`
  - Adds runtime tests for join, activation, idempotency, and conflict rejection.
- Modify `pkg/clusterv2/control/codec.go`
  - Adds generic control-write request/response codec entries for lifecycle forwarding.
- Create `pkg/clusterv2/control/node_lifecycle.go`
  - Adds control facade request/result types and deterministic forwarding helpers.
- Create `pkg/clusterv2/control/control_write.go`
  - Adds `ControlWriteClient`, `ControlWriteHandler`, and `ControlWriteApplier` without changing task-result RPC semantics.
- Modify `pkg/clusterv2/control/runtime.go`
  - Exposes `JoinNode` and `ActivateNode`, forwarding to the Controller leader when local runtime is not leader.
- Modify `pkg/clusterv2/control/transport.go`
  - Leaves `TaskApplier` task-shaped and wires the separate control-write client/handler.
- Modify `pkg/clusterv2/control/runtime_test.go`
  - Verifies follower forwarding for node join and activation.
- Create `internalv2/access/node/node_lifecycle_rpc.go`
  - Adds typed seed join and readiness RPCs used by seed-join startup and activation validation.
- Create `internalv2/infra/cluster/node_lifecycle.go`
  - Adapts typed node RPC and clusterv2 control writes into internalv2 ports.
- Modify `internalv2/app/*`
  - Adds seed-join startup loop: join token validation, cluster ID validation, mirror snapshot sync, and readiness reporting.
- Create `internalv2/usecase/management/node_lifecycle.go`
  - Adds entry-independent manager usecase methods `JoinNode` and `ActivateNode`; activation validates typed node readiness before writing active.
- Create `internalv2/usecase/management/node_lifecycle_test.go`
  - Verifies validation, writer calls, idempotent response projection, and non-active node visibility.
- Modify `internalv2/usecase/management/nodes.go`
  - Adds a `NodeLifecycleWriter` port to `Options` and `App`.
- Create `internalv2/access/manager/node_lifecycle.go`
  - Adds HTTP request/response DTOs and handlers for join and activation.
- Modify `internalv2/access/manager/server.go`
  - Registers write routes under `cluster.node:w`.
- Modify `internalv2/access/manager/server_test.go`
  - Removes the old unmigrated expectation for the new node lifecycle routes and extends the management stub.
- Create `internalv2/access/manager/node_lifecycle_test.go`
  - Verifies JSON validation, permissions, response status, and error mapping.
- Modify `internalv2/infra/cluster/FLOW.md`, `internalv2/usecase/management/FLOW.md`, and `internalv2/access/manager/FLOW.md`
  - Documents the new write path and route ownership.
- Create `test/e2ev2/cluster/dynamic_node_join/dynamic_node_join_test.go`
  - Adds black-box coverage for adding a fourth data node to a running three-node internalv2 cluster.

## Task 1: Add ControllerV2 Join And Activate Runtime Methods

**Files:**
- Create: `pkg/controllerv2/runtime_node_lifecycle.go`
- Modify: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Write failing runtime tests**

Append these tests to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimeJoinNodeCreatesJoiningDataNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-node")

	result, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "127.0.0.1:10004",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 3,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !result.Created || result.Node.JoinState != NodeJoinStateJoining {
		t.Fatalf("JoinNode() = %#v, want created joining node", result)
	}

	st := waitForState(t, runtime, func(st ClusterState) bool {
		for _, node := range st.Nodes {
			if node.NodeID == 4 && node.JoinState == NodeJoinStateJoining && node.Status == NodeStatusAlive {
				return true
			}
		}
		return false
	})
	if len(st.Slots) != 1 || containsUint64(st.Slots[0].DesiredPeers, 4) {
		t.Fatalf("Slots after join = %#v, want unchanged assignments without node 4", st.Slots)
	}
}

func TestRuntimeActivateNodeTurnsJoiningNodeActive(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-activate-node")
	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}

	result, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateActive {
		t.Fatalf("ActivateNode() = %#v, want changed active node", result)
	}
}

func TestRuntimeJoinNodeRejectsAddressConflict(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-conflict")

	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode(first) error = %v", err)
	}
	_, err = runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 5, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err == nil {
		t.Fatal("JoinNode(conflicting addr) error = nil, want conflict")
	}
}
```

Add these helpers if they do not already exist in the file:

```go
func startSingleVoterRuntime(t *testing.T, clusterID string) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        clusterID,
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	waitForState(t, runtime, func(st ClusterState) bool { return st.Revision > 0 })
	return runtime
}

func containsUint64(values []uint64, want uint64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntime(JoinNodeCreatesJoiningDataNode|ActivateNodeTurnsJoiningNodeActive|JoinNodeRejectsAddressConflict)' -count=1
```

Expected: FAIL because `JoinNode`, `ActivateNode`, and request/result types are not defined.

- [ ] **Step 3: Add runtime node lifecycle implementation**

Create `pkg/controllerv2/runtime_node_lifecycle.go`:

```go
package controllerv2

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
)

// JoinNodeRequest describes a dynamic data-node join request.
type JoinNodeRequest struct {
	// NodeID is the stable non-zero node identity.
	NodeID uint64
	// Name is a human-readable node label.
	Name string
	// Addr is the stable cluster RPC address advertised by the joining node.
	Addr string
	// Roles lists durable capabilities requested by the node.
	Roles []NodeRole
	// CapacityWeight is the placement weight used after activation.
	CapacityWeight uint32
}

// JoinNodeResult describes the durable membership record after join.
type JoinNodeResult struct {
	// Created reports whether the request changed ControllerV2 state.
	Created bool
	// Node is the durable node record after the request.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
}

// ActivateNodeRequest identifies a joining node to activate.
type ActivateNodeRequest struct {
	// NodeID is the stable node identity to activate.
	NodeID uint64
}

// ActivateNodeResult describes the durable membership record after activation.
type ActivateNodeResult struct {
	// Changed reports whether the request changed ControllerV2 state.
	Changed bool
	// Node is the durable node record after the request.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
}

// JoinNode adds or refreshes a dynamic data-node membership record in joining state.
func (r *Runtime) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResult, error) {
	if req.NodeID == 0 || req.Addr == "" {
		return JoinNodeResult{}, fmt.Errorf("controllerv2: node id and addr are required")
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return JoinNodeResult{}, err
	}
	node, created, err := buildJoinNode(st, req)
	if err != nil {
		return JoinNodeResult{}, err
	}
	if !created {
		return JoinNodeResult{Created: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	if err := r.raft.Propose(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         time.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}); err != nil {
		return JoinNodeResult{}, err
	}
	st, err = r.LocalState(ctx)
	if err != nil {
		return JoinNodeResult{}, err
	}
	return JoinNodeResult{Created: true, Node: node, Revision: st.Revision}, nil
}

// ActivateNode marks a joining data node active so placement can use it.
func (r *Runtime) ActivateNode(ctx context.Context, req ActivateNodeRequest) (ActivateNodeResult, error) {
	if req.NodeID == 0 {
		return ActivateNodeResult{}, fmt.Errorf("controllerv2: node id is required")
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	node, changed, err := buildActivateNode(st, req.NodeID)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	if !changed {
		return ActivateNodeResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	if err := r.raft.Propose(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         time.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}); err != nil {
		return ActivateNodeResult{}, err
	}
	st, err = r.LocalState(ctx)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	return ActivateNodeResult{Changed: true, Node: node, Revision: st.Revision}, nil
}
```

Implement `buildJoinNode` and `buildActivateNode` in the same file. They must:

```go
// buildJoinNode rejects node-id/address conflicts and never creates controller voters dynamically.
func buildJoinNode(st ClusterState, req JoinNodeRequest) (Node, bool, error) {
	roles := normalizeJoinRoles(req.Roles)
	for _, existing := range st.Nodes {
		if existing.NodeID == req.NodeID {
			if existing.Addr != req.Addr {
				return Node{}, false, fmt.Errorf("controllerv2: node id %d already uses addr %q", req.NodeID, existing.Addr)
			}
			if existing.JoinState == NodeJoinStateActive || existing.JoinState == NodeJoinStateJoining {
				return existing, false, nil
			}
			break
		}
		if existing.Addr == req.Addr {
			return Node{}, false, fmt.Errorf("controllerv2: addr %q already belongs to node %d", req.Addr, existing.NodeID)
		}
	}
	weight := req.CapacityWeight
	if weight == 0 {
		weight = 1
	}
	return Node{
		NodeID:         req.NodeID,
		Name:           req.Name,
		Addr:           req.Addr,
		Roles:          roles,
		JoinState:      NodeJoinStateJoining,
		Status:         NodeStatusAlive,
		CapacityWeight: weight,
	}, true, nil
}

func buildActivateNode(st ClusterState, nodeID uint64) (Node, bool, error) {
	for _, existing := range st.Nodes {
		if existing.NodeID != nodeID {
			continue
		}
		if existing.JoinState == NodeJoinStateActive {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateJoining {
			return Node{}, false, fmt.Errorf("controllerv2: node %d is %q, want joining", nodeID, existing.JoinState)
		}
		next := existing
		next.JoinState = NodeJoinStateActive
		next.Status = NodeStatusAlive
		return next, true, nil
	}
	return Node{}, false, fmt.Errorf("controllerv2: node %d not found", nodeID)
}

func normalizeJoinRoles(roles []NodeRole) []NodeRole {
	out := make([]NodeRole, 0, len(roles)+1)
	hasData := false
	for _, role := range roles {
		if role == NodeRoleControllerVoter {
			continue
		}
		if role == NodeRoleData {
			hasData = true
		}
		out = append(out, role)
	}
	if !hasData {
		out = append(out, NodeRoleData)
	}
	return out
}
```

- [ ] **Step 4: Run tests and verify GREEN**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntime(JoinNodeCreatesJoiningDataNode|ActivateNodeTurnsJoiningNodeActive|JoinNodeRejectsAddressConflict)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit ControllerV2 lifecycle runtime**

```bash
git add pkg/controllerv2/runtime_node_lifecycle.go pkg/controllerv2/runtime_test.go
git commit -m "feat: add controllerv2 node join activation"
```

## Task 2: Add Generic clusterv2 Control-Write Forwarding

**Files:**
- Modify: `pkg/clusterv2/control/codec.go`
- Create: `pkg/clusterv2/control/node_lifecycle.go`
- Create: `pkg/clusterv2/control/control_write.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/transport.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`
- Modify: `pkg/clusterv2/control/codec_test.go`

- [ ] **Step 1: Write failing codec and forwarding tests**

Add to `pkg/clusterv2/control/codec_test.go`:

```go
func TestControlWriteRequestNodeLifecycleCodecRoundTrip(t *testing.T) {
	req := ControlWriteRequest{
		Action: ControlWriteActionJoinNode,
		JoinNode: JoinNodeRequest{
			NodeID:         4,
			Name:           "node-4",
			Addr:           "127.0.0.1:10004",
			Roles:          []Role{RoleData},
			CapacityWeight: 2,
		},
	}
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	got, err := DecodeControlWriteRequest(payload)
	if err != nil {
		t.Fatalf("DecodeControlWriteRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("DecodeControlWriteRequest() = %#v, want %#v", got, req)
	}
}
```

Add to `pkg/clusterv2/control/runtime_test.go`:

```go
func TestRuntimeJoinNodeReturnsControlWriteAfterForward(t *testing.T) {
	caller := newControlWriteForwardingCaller(t)
	leader := newForwardingRuntime(t, 1, caller)
	follower := newForwardingRuntime(t, 2, caller)
	follower.snapshot.ControllerID = 1

	result, err := follower.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !result.Created || result.Node.NodeID != 4 || result.Node.JoinState != NodeJoinStateJoining {
		t.Fatalf("JoinNode() = %#v, want forwarded joining result", result)
	}
	_ = leader
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/clusterv2/control -run 'TestControlWriteRequestNodeLifecycleCodecRoundTrip|TestRuntimeJoinNodeReturnsControlWriteAfterForward' -count=1
```

Expected: FAIL because node lifecycle action types and methods are not defined.

- [ ] **Step 3: Add control request/result types and codec payloads**

In `pkg/clusterv2/control/codec.go`, add a new wire kind and codec helpers:

```go
	controlKindWriteRequest
	controlKindWriteResponse
```

```go
// ControlWriteAction selects one Controller-backed control write.
type ControlWriteAction string

const (
	// ControlWriteActionJoinNode submits a dynamic node join intent.
	ControlWriteActionJoinNode ControlWriteAction = "join_node"
	// ControlWriteActionActivateNode submits a dynamic node activation intent.
	ControlWriteActionActivateNode ControlWriteAction = "activate_node"
)

// ControlWriteRequest carries one non-task Controller write.
type ControlWriteRequest struct {
	// Action selects which payload should be applied.
	Action ControlWriteAction `json:"action"`
	// JoinNode carries a dynamic node join intent.
	JoinNode JoinNodeRequest `json:"join_node,omitempty"`
	// ActivateNode carries a dynamic node activation intent.
	ActivateNode ActivateNodeRequest `json:"activate_node,omitempty"`
}

// ControlWriteResponse returns the selected lifecycle write result.
type ControlWriteResponse struct {
	// JoinNode carries the join result when Action is join_node.
	JoinNode JoinNodeResult `json:"join_node,omitempty"`
	// ActivateNode carries the activation result when Action is activate_node.
	ActivateNode ActivateNodeResult `json:"activate_node,omitempty"`
}
```

Create `pkg/clusterv2/control/node_lifecycle.go`:

```go
package control

import cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"

// JoinNodeRequest describes a dynamic node join intent.
type JoinNodeRequest struct {
	NodeID         uint64
	Name           string
	Addr           string
	Roles          []Role
	CapacityWeight uint32
}

// JoinNodeResult describes the node record after join.
type JoinNodeResult struct {
	Created  bool
	Node     Node
	Revision uint64
}

// ActivateNodeRequest identifies a joining node to activate.
type ActivateNodeRequest struct {
	NodeID uint64
}

// ActivateNodeResult describes the node record after activation.
type ActivateNodeResult struct {
	Changed  bool
	Node     Node
	Revision uint64
}

func cv2JoinNodeRequest(req JoinNodeRequest) cv2.JoinNodeRequest {
	roles := make([]cv2.NodeRole, 0, len(req.Roles))
	for _, role := range req.Roles {
		if role == RoleData {
			roles = append(roles, cv2.NodeRoleData)
		}
	}
	return cv2.JoinNodeRequest{NodeID: req.NodeID, Name: req.Name, Addr: req.Addr, Roles: roles, CapacityWeight: req.CapacityWeight}
}
```

Use the `NodeJoinState` type added by Stage 1 in `pkg/clusterv2/control/snapshot.go`. Do not define a second lifecycle type in `node_lifecycle.go`; this file only owns request/result mapping.

- [ ] **Step 4: Add separate control-write client and handler**

Create `pkg/clusterv2/control/control_write.go`:

```go
package control

import (
	"context"
	"fmt"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

// ControlWriteApplier applies non-task Controller writes.
type ControlWriteApplier interface {
	// JoinNode submits a dynamic node join intent.
	JoinNode(context.Context, JoinNodeRequest) (JoinNodeResult, error)
	// ActivateNode submits a dynamic node activation intent.
	ActivateNode(context.Context, ActivateNodeRequest) (ActivateNodeResult, error)
}

// ControlWriteClient forwards non-task Controller writes to a remote node.
type ControlWriteClient struct {
	caller clusternet.Caller
}

// NewControlWriteClient creates a control-write RPC client.
func NewControlWriteClient(caller clusternet.Caller) *ControlWriteClient {
	return &ControlWriteClient{caller: caller}
}

// Submit sends one control write request to nodeID.
func (c *ControlWriteClient) Submit(ctx context.Context, nodeID uint64, req ControlWriteRequest) (ControlWriteResponse, error) {
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		return ControlWriteResponse{}, err
	}
	resp, err := clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCControlWrite, payload)
	if err != nil {
		return ControlWriteResponse{}, err
	}
	return DecodeControlWriteResponse(resp)
}

// NewControlWriteHandler creates an RPC handler for non-task Controller writes.
func NewControlWriteHandler(applier ControlWriteApplier) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeControlWriteRequest(payload)
		if err != nil {
			return nil, err
		}
		var resp ControlWriteResponse
		switch req.Action {
		case ControlWriteActionJoinNode:
			result, err := applier.JoinNode(ctx, req.JoinNode)
			resp.JoinNode = result
			return EncodeControlWriteResponse(resp, err)
		case ControlWriteActionActivateNode:
			result, err := applier.ActivateNode(ctx, req.ActivateNode)
			resp.ActivateNode = result
			return EncodeControlWriteResponse(resp, err)
		default:
			return nil, fmt.Errorf("control write: unknown action %q", req.Action)
		}
	})
}
```

In `pkg/clusterv2/control/transport.go`, leave `TaskApplier` and `NewTaskHandler` task-shaped. Add only the new transport constant in the clusterv2 net package if it does not already exist:

```go
RPCControlWrite
```

- [ ] **Step 5: Extend runtime with control-write forwarding**

In `pkg/clusterv2/control/runtime.go`, add a `ControlWriteClient` to `RuntimeConfig` and `Runtime`, then add:

```go
// JoinNode submits a dynamic node join intent to ControllerV2.
func (r *Runtime) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return JoinNodeResult{}, err
	}
	if r == nil || r.backend == nil {
		return JoinNodeResult{}, cv2.ErrNotStarted
	}
	result, err := r.backend.JoinNode(ctx, cv2JoinNodeRequest(req))
	if shouldForwardControlWrite(err) {
		resp, err := r.forwardControlWrite(ctx, ControlWriteRequest{Action: ControlWriteActionJoinNode, JoinNode: req})
		if err != nil {
			return JoinNodeResult{}, err
		}
		return resp.JoinNode, nil
	}
	if err != nil {
		return JoinNodeResult{}, err
	}
	return JoinNodeResult{Created: result.Created, Node: controlNodeFromControllerNode(result.Node), Revision: result.Revision}, nil
}
```

Add `ActivateNode` using the same forwarding pattern with `ControlWriteActionActivateNode`.

Add helpers:

```go
func shouldForwardControlWrite(err error) bool {
	return errors.Is(err, cv2.ErrNotLeader) || errors.Is(err, cv2.ErrNotStarted)
}

func (r *Runtime) forwardControlWrite(ctx context.Context, req ControlWriteRequest) (ControlWriteResponse, error) {
	if r == nil || r.writeClient == nil {
		return ControlWriteResponse{}, cv2.ErrNotLeader
	}
	leaderID := r.LeaderID()
	if leaderID == 0 || leaderID == r.cfg.NodeID {
		return ControlWriteResponse{}, cv2.ErrNotLeader
	}
	return r.writeClient.Submit(ctx, leaderID, req)
}
```

- [ ] **Step 6: Run clusterv2 control tests and verify GREEN**

Run:

```bash
go test ./pkg/clusterv2/control -run 'TestControlWriteRequestNodeLifecycleCodecRoundTrip|TestRuntimeJoinNodeReturnsControlWriteAfterForward|TestRuntimeRequestSlotLeaderTransferReturnsTaskAfterForward' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit clusterv2 forwarding**

```bash
git add pkg/clusterv2/control
git commit -m "feat: forward node lifecycle control writes"
```

## Task 3: Add Manager Usecase And HTTP Routes

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Create: `internalv2/usecase/management/node_lifecycle.go`
- Create: `internalv2/usecase/management/node_lifecycle_test.go`
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/node_lifecycle.go`
- Create: `internalv2/access/manager/node_lifecycle_test.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write failing usecase tests**

Create `internalv2/usecase/management/node_lifecycle_test.go`:

```go
package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestJoinNodeValidatesAndDelegates(t *testing.T) {
	writer := &fakeNodeLifecycleWriter{}
	app := NewApp(Options{NodeLifecycle: writer})

	resp, err := app.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "127.0.0.1:10004",
		CapacityWeight: 2,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if writer.join.NodeID != 4 || writer.join.Addr != "127.0.0.1:10004" {
		t.Fatalf("writer join = %#v, want node 4 addr", writer.join)
	}
	if !resp.Created || resp.NodeID != 4 || resp.JoinState != "joining" {
		t.Fatalf("JoinNode() = %#v, want joining response", resp)
	}
}

func TestActivateNodeDelegates(t *testing.T) {
	writer := &fakeNodeLifecycleWriter{}
	app := NewApp(Options{NodeLifecycle: writer})

	resp, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if writer.activate.NodeID != 4 {
		t.Fatalf("writer activate = %#v, want node 4", writer.activate)
	}
	if !resp.Changed || resp.JoinState != "active" {
		t.Fatalf("ActivateNode() = %#v, want active response", resp)
	}
}

type fakeNodeLifecycleWriter struct {
	join     control.JoinNodeRequest
	activate control.ActivateNodeRequest
}

func (w *fakeNodeLifecycleWriter) JoinNode(_ context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	w.join = req
	return control.JoinNodeResult{Created: true, Node: control.Node{NodeID: req.NodeID, Addr: req.Addr, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining, CapacityWeight: req.CapacityWeight}}, nil
}

func (w *fakeNodeLifecycleWriter) ActivateNode(_ context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	w.activate = req
	return control.ActivateNodeResult{Changed: true, Node: control.Node{NodeID: req.NodeID, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, CapacityWeight: 1}}, nil
}
```

- [ ] **Step 2: Run usecase tests and verify RED**

Run:

```bash
go test ./internalv2/usecase/management -run 'Test(JoinNodeValidatesAndDelegates|ActivateNodeDelegates)' -count=1
```

Expected: FAIL because the usecase port and methods are not defined.

- [ ] **Step 3: Implement usecase port and methods**

In `internalv2/usecase/management/nodes.go`, extend `Options` and `App`:

```go
	// NodeLifecycle submits Controller-backed node lifecycle writes.
	NodeLifecycle NodeLifecycleWriter
```

```go
	nodeLifecycle NodeLifecycleWriter
```

Create `internalv2/usecase/management/node_lifecycle.go`:

```go
package management

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// NodeLifecycleWriter submits durable node lifecycle writes through clusterv2 control.
type NodeLifecycleWriter interface {
	// JoinNode adds a joining data-node record.
	JoinNode(context.Context, control.JoinNodeRequest) (control.JoinNodeResult, error)
	// ActivateNode marks a joining node active.
	ActivateNode(context.Context, control.ActivateNodeRequest) (control.ActivateNodeResult, error)
}

type JoinNodeRequest struct {
	NodeID         uint64
	Name           string
	Addr           string
	CapacityWeight uint32
}

type JoinNodeResponse struct {
	Created    bool
	NodeID     uint64
	Addr       string
	JoinState  string
	Revision   uint64
}

type ActivateNodeRequest struct {
	NodeID uint64
}

type ActivateNodeResponse struct {
	Changed   bool
	NodeID    uint64
	JoinState string
	Revision  uint64
}

func (a *App) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResponse, error) {
	if req.NodeID == 0 || req.Addr == "" || a.nodeLifecycle == nil {
		return JoinNodeResponse{}, metadb.ErrInvalidArgument
	}
	result, err := a.nodeLifecycle.JoinNode(ctx, control.JoinNodeRequest{NodeID: req.NodeID, Name: req.Name, Addr: req.Addr, Roles: []control.Role{control.RoleData}, CapacityWeight: req.CapacityWeight})
	if err != nil {
		return JoinNodeResponse{}, err
	}
	return JoinNodeResponse{Created: result.Created, NodeID: result.Node.NodeID, Addr: result.Node.Addr, JoinState: string(result.Node.JoinState), Revision: result.Revision}, nil
}

func (a *App) ActivateNode(ctx context.Context, req ActivateNodeRequest) (ActivateNodeResponse, error) {
	if req.NodeID == 0 || a.nodeLifecycle == nil {
		return ActivateNodeResponse{}, metadb.ErrInvalidArgument
	}
	result, err := a.nodeLifecycle.ActivateNode(ctx, control.ActivateNodeRequest{NodeID: req.NodeID})
	if err != nil {
		return ActivateNodeResponse{}, err
	}
	return ActivateNodeResponse{Changed: result.Changed, NodeID: result.Node.NodeID, JoinState: string(result.Node.JoinState), Revision: result.Revision}, nil
}
```

- [ ] **Step 4: Write failing HTTP tests**

Create `internalv2/access/manager/node_lifecycle_test.go`:

```go
package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerJoinNodeRoute(t *testing.T) {
	stub := &managerNodesStub{joinNodeResponse: managementusecase.JoinNodeResponse{Created: true, NodeID: 4, Addr: "n4", JoinState: "joining", Revision: 8}}
	srv := newTestServer(t, stub)

	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"name":"node-4","addr":"n4","capacity_weight":2}`))
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
}

func TestManagerActivateNodeRoute(t *testing.T) {
	stub := &managerNodesStub{activateNodeResponse: managementusecase.ActivateNodeResponse{Changed: true, NodeID: 4, JoinState: "active", Revision: 9}}
	srv := newTestServer(t, stub)

	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/activate", nil)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 5: Implement HTTP routes**

In `internalv2/access/manager/server.go`, extend the management interface:

```go
	// JoinNode submits a dynamic node join intent.
	JoinNode(ctx context.Context, req managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error)
	// ActivateNode marks a joining node active.
	ActivateNode(ctx context.Context, req managementusecase.ActivateNodeRequest) (managementusecase.ActivateNodeResponse, error)
```

Register routes in the node write group:

```go
	nodeWrites.POST("/nodes/join", s.handleJoinNode)
	nodeWrites.POST("/nodes/:node_id/activate", s.handleActivateNode)
```

Create `internalv2/access/manager/node_lifecycle.go` with handlers that parse JSON, validate positive IDs, delegate to the usecase, and return `202 Accepted` when `Created` or `Changed` is true and `200 OK` for idempotent responses.

- [ ] **Step 6: Run manager tests and verify GREEN**

Run:

```bash
go test ./internalv2/usecase/management -run 'Test(JoinNodeValidatesAndDelegates|ActivateNodeDelegates)' -count=1
go test ./internalv2/access/manager -run 'TestManager(JoinNodeRoute|ActivateNodeRoute|NodeOperationRoutesStayUnmigrated)' -count=1
```

Expected: new lifecycle route tests pass, and the unmigrated route test no longer expects `/manager/nodes/1/draining` to cover the new join/activate routes.

- [ ] **Step 7: Commit manager lifecycle routes**

```bash
git add internalv2/usecase/management internalv2/access/manager
git commit -m "feat: expose dynamic node join activation"
```

## Task 4: Add Seed Join RPC And Startup Join Loop

**Files:**
- Create: `internalv2/access/node/node_lifecycle_rpc.go`
- Create: `internalv2/access/node/node_lifecycle_rpc_test.go`
- Create: `internalv2/infra/cluster/node_lifecycle.go`
- Create: `internalv2/infra/cluster/node_lifecycle_test.go`
- Modify: `internalv2/app/*`
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`

- [ ] **Step 1: Write failing node RPC tests**

Create `internalv2/access/node/node_lifecycle_rpc_test.go`:

```go
package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestNodeLifecycleRPCJoinForwardsTokenAndClusterID(t *testing.T) {
	service := &fakeNodeLifecycleService{}
	server, client := newNodeLifecycleRPCTestPair(t, service)
	_ = server

	resp, err := client.JoinNode(context.Background(), 1, NodeJoinRequest{
		NodeID:         4,
		AdvertiseAddr:  "127.0.0.1:10004",
		ClusterID:      "cluster-a",
		JoinToken:      "secret",
		CapacityWeight: 2,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if service.join.ClusterID != "cluster-a" || service.join.JoinToken != "secret" {
		t.Fatalf("join = %#v, want token and cluster id forwarded", service.join)
	}
	if !resp.Created || resp.NodeID != 4 {
		t.Fatalf("JoinNode() = %#v, want created node 4", resp)
	}
}

type fakeNodeLifecycleService struct {
	join NodeJoinRequest
}

func (f *fakeNodeLifecycleService) JoinNode(_ context.Context, req NodeJoinRequest) (managementusecase.JoinNodeResponse, error) {
	f.join = req
	return managementusecase.JoinNodeResponse{Created: true, NodeID: req.NodeID, Addr: req.AdvertiseAddr, JoinState: "joining"}, nil
}
```

- [ ] **Step 2: Run node RPC test and verify RED**

Run:

```bash
go test ./internalv2/access/node -run TestNodeLifecycleRPCJoinForwardsTokenAndClusterID -count=1
```

Expected: FAIL because lifecycle node RPC types are not defined.

- [ ] **Step 3: Implement typed node lifecycle RPC**

Create `internalv2/access/node/node_lifecycle_rpc.go`:

```go
type NodeJoinRequest struct {
	NodeID         uint64
	AdvertiseAddr  string
	ClusterID      string
	JoinToken      string
	CapacityWeight uint32
}

type NodeReadinessRequest struct {
	NodeID    uint64
	ClusterID string
}

type NodeReadinessResponse struct {
	NodeID             uint64
	ClusterID          string
	Reachable          bool
	MirrorRevision     uint64
	MirrorClusterID    string
	TransportReady     bool
	ControlReady       bool
	RuntimeReady       bool
	Ready              bool
	LastError           string
}
```

Add client/server methods:

```go
func (c *Client) JoinNode(ctx context.Context, seedNodeID uint64, req NodeJoinRequest) (managementusecase.JoinNodeResponse, error)
func (c *Client) NodeReadiness(ctx context.Context, nodeID uint64, req NodeReadinessRequest) (NodeReadinessResponse, error)
```

The server handler must validate the join token and cluster ID before delegating to the management usecase. It must forward to the Controller leader through the management/control writer when the seed is not the leader.

- [ ] **Step 4: Add seed-join startup loop**

In `internalv2/app`, add a startup component that runs only when Stage 1 seed-join config is present:

```go
type seedJoinLoopConfig struct {
	NodeID         uint64
	AdvertiseAddr  string
	ClusterID      string
	JoinToken      string
	Seeds          []uint64
	CapacityWeight uint32
	Interval       time.Duration
}
```

Loop behavior:

```text
wait for transport start
  -> call JoinNode on seeds in stable order
  -> require matching cluster_id and join_token
  -> stop retrying after local control mirror shows this node as joining or active
  -> never call ActivateNode from the joining node itself
```

Use bounded backoff with a default interval of one second and stop cleanly with app lifecycle context.

- [ ] **Step 5: Write and run startup loop tests**

Add an app-level test:

```go
func TestSeedJoinLoopStopsAfterMirrorSeesJoiningNode(t *testing.T) {
	loop := newSeedJoinLoopForTest(seedJoinLoopConfig{NodeID: 4, AdvertiseAddr: "n4", ClusterID: "cluster-a", JoinToken: "secret"})
	loop.joinClient = &fakeSeedJoinClient{created: true}
	loop.snapshotReader = &fakeControlSnapshotSequence{states: []control.Snapshot{
		{Revision: 7},
		{Revision: 8, Nodes: []control.Node{{NodeID: 4, Addr: "n4", JoinState: control.NodeJoinStateJoining}}},
	}}
	if err := loop.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if loop.joinClient.(*fakeSeedJoinClient).calls == 0 {
		t.Fatal("join client was not called")
	}
}
```

Run:

```bash
go test ./internalv2/app -run TestSeedJoinLoopStopsAfterMirrorSeesJoiningNode -count=1
go test ./internalv2/access/node -run TestNodeLifecycleRPCJoinForwardsTokenAndClusterID -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit seed join loop**

```bash
git add internalv2/access/node internalv2/infra/cluster internalv2/app cmd/wukongimv2
git commit -m "feat: add internalv2 seed join loop"
```

## Task 5: Add Activation Readiness Gates

**Files:**
- Modify: `internalv2/usecase/management/node_lifecycle.go`
- Modify: `internalv2/usecase/management/node_lifecycle_test.go`
- Modify: `internalv2/access/node/node_lifecycle_rpc.go`
- Modify: `internalv2/infra/cluster/node_lifecycle.go`
- Modify: `internalv2/app/*`

- [ ] **Step 1: Write failing activation readiness tests**

Append to `internalv2/usecase/management/node_lifecycle_test.go`:

```go
func TestActivateNodeRejectsWhenReadinessIsUnknown(t *testing.T) {
	writer := &fakeNodeLifecycleWriter{}
	app := NewApp(Options{
		NodeLifecycle: writer,
		NodeReadiness: fakeNodeReadinessReader{resp: NodeReadiness{NodeID: 4, Unknown: true}},
	})

	_, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeNotReadyForActivation) {
		t.Fatalf("ActivateNode() error = %v, want ErrNodeNotReadyForActivation", err)
	}
}

func TestActivateNodeRequiresMirrorClusterAndRevision(t *testing.T) {
	writer := &fakeNodeLifecycleWriter{}
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: control.Snapshot{Revision: 22}},
		NodeLifecycle: writer,
		NodeReadiness: fakeNodeReadinessReader{resp: NodeReadiness{
			NodeID: 4, Reachable: true, MirrorClusterID: "cluster-a", ExpectedClusterID: "cluster-a",
			MirrorRevision: 21, TransportReady: true, ControlReady: true, RuntimeReady: true,
		}},
	})

	_, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeNotReadyForActivation) {
		t.Fatalf("ActivateNode() error = %v, want revision readiness rejection", err)
	}
}
```

- [ ] **Step 2: Implement readiness port**

In `internalv2/usecase/management/node_lifecycle.go`, add:

```go
var ErrNodeNotReadyForActivation = errors.New("node is not ready for activation")

type NodeReadinessReader interface {
	NodeReadiness(context.Context, uint64) (NodeReadiness, error)
}

type NodeReadiness struct {
	NodeID            uint64
	ExpectedClusterID string
	MirrorClusterID   string
	MirrorRevision    uint64
	Reachable         bool
	TransportReady    bool
	ControlReady      bool
	RuntimeReady      bool
	Unknown           bool
	LastError         string
}
```

`ActivateNode` must read the local control snapshot, then require:

- node exists and is `joining`;
- readiness is not unknown;
- `Reachable`, `TransportReady`, `ControlReady`, and `RuntimeReady` are true;
- `MirrorClusterID == ExpectedClusterID`;
- `MirrorRevision >= snapshot.Revision`.

Only after these checks may it call `NodeLifecycleWriter.ActivateNode`.

- [ ] **Step 3: Wire readiness through typed node RPC**

`internalv2/infra/cluster/node_lifecycle.go` must implement `NodeReadinessReader` by calling the target node's typed readiness RPC. Local app readiness must report:

```go
NodeReadiness{
	NodeID: nodeID,
	ExpectedClusterID: cfg.ClusterID,
	MirrorClusterID: localSnapshot.ClusterID,
	MirrorRevision: localSnapshot.Revision,
	Reachable: true,
	TransportReady: transportStarted,
	ControlReady: controlSnapshot.Revision > 0,
	RuntimeReady: gatewayStarted && defaultSlotsReady,
}
```

- [ ] **Step 4: Run readiness tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestActivateNode(RejectsWhenReadinessIsUnknown|RequiresMirrorClusterAndRevision|Delegates)' -count=1
go test ./internalv2/access/node -run 'TestNodeLifecycleRPC.*Readiness' -count=1
go test ./internalv2/infra/cluster -run 'TestNodeLifecycle.*Readiness' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit readiness gates**

```bash
git add internalv2/usecase/management internalv2/access/node internalv2/infra/cluster internalv2/app
git commit -m "feat: gate node activation on readiness"
```

## Task 6: Wire App And Add e2ev2 Dynamic Join Smoke

**Files:**
- Modify: `internalv2/app/*`
- Create: `test/e2ev2/cluster/dynamic_node_join/dynamic_node_join_test.go`
- Modify: `test/e2ev2/suite/*`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Write failing e2ev2 test**

Create `test/e2ev2/cluster/dynamic_node_join/dynamic_node_join_test.go`:

```go
package dynamic_node_join

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
)

func TestDynamicJoinFourthDataNode(t *testing.T) {
	cluster := suite.StartCluster(t, suite.ClusterConfig{Nodes: 3})
	client := cluster.ManagerClient(t, 1)

	before := client.MustSlots(t)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:   4,
		Seeds:    cluster.SeedAddrs(),
		JoinAddr: cluster.NodeAddr(4),
	})
	defer node4.Stop(t)

	client.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	client.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	client.MustActivateNode(t, 4)
	client.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	after := client.MustSlots(t)
	if !suite.SameSlotAssignments(before, after) {
		t.Fatalf("slot assignments changed during join: before=%#v after=%#v", before, after)
	}
}
```

- [ ] **Step 2: Run e2ev2 test and verify RED**

Run:

```bash
go test ./test/e2ev2/cluster/dynamic_node_join -run TestDynamicJoinFourthDataNode -count=1
```

Expected: FAIL because suite helpers and app wiring are not complete.

- [ ] **Step 3: Wire app usecase options**

In the internalv2 app composition root, pass the clusterv2 control runtime/facade into `management.Options{NodeLifecycle: ...}`. Keep the dependency direction `app -> infra/control -> usecase`; do not import manager HTTP into app wiring.

- [ ] **Step 4: Add e2ev2 suite helpers**

Add helpers under `test/e2ev2/suite`:

```go
func (c *Cluster) SeedAddrs() []string
func (c *Cluster) StartSeedJoinNode(t testing.TB, cfg SeedJoinNodeConfig) *Node
func (c *Cluster) NodeAddr(nodeID uint64) string
func (m *ManagerClient) EventuallyNodeReadiness(t testing.TB, nodeID uint64, ready bool, timeout time.Duration)
func (m *ManagerClient) MustActivateNode(t testing.TB, nodeID uint64)
func (m *ManagerClient) EventuallyNodeJoinState(t testing.TB, nodeID uint64, state string, timeout time.Duration)
func SameSlotAssignments(a, b []SlotDTO) bool
```

The helpers must render seed-join config with `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, `WK_CLUSTER_JOIN_TOKEN`, matching `WK_CLUSTER_ID`, and no static `WK_CLUSTER_NODES` for node 4. The seed-join node must call the typed join RPC by itself; the test must not call manager `JoinNode` directly.

- [ ] **Step 5: Run e2ev2 test and focused packages**

Run:

```bash
go test ./internalv2/app ./internalv2/usecase/management ./internalv2/access/manager ./pkg/controllerv2 ./pkg/clusterv2/control
go test ./test/e2ev2/cluster/dynamic_node_join -run TestDynamicJoinFourthDataNode -count=1
```

Expected: PASS.

- [ ] **Step 6: Update FLOW docs**

Update:

- `internalv2/infra/cluster/FLOW.md`
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`

Document the join flow:

```text
manager HTTP
  -> management.App.ActivateNode
  -> NodeReadinessReader
  -> typed node readiness RPC
  -> NodeLifecycleWriter
  -> clusterv2 control Runtime.ActivateNode
  -> ControllerV2 KindUpsertNode
```

Document the seed join flow:

```text
seed-join node startup
  -> typed node JoinNode RPC to seed
  -> seed validates join_token and cluster_id
  -> management.App.JoinNode
  -> NodeLifecycleWriter
  -> clusterv2 control Runtime.JoinNode
  -> ControllerV2 KindUpsertNode
  -> joining node mirrors control snapshot
```

Mention that Stage 2 leaves existing Slot assignments unchanged.

- [ ] **Step 7: Commit app wiring and e2ev2 smoke**

```bash
git add internalv2/app test/e2ev2 internalv2/infra/cluster/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "test: cover internalv2 dynamic node join"
```

## Exit Gate

- [ ] Run the full Stage 2 verification:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/control ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/app
go test ./test/e2ev2/cluster/dynamic_node_join -run TestDynamicJoinFourthDataNode -count=1
git diff --check
```

Expected: all commands pass.

- [ ] Confirm Stage 3 prerequisites:

```bash
rg -n "JoinNode|ActivateNode|join_node|activate_node|NodeLifecycleWriter" pkg/controllerv2 pkg/clusterv2/control internalv2
```

Expected: node lifecycle writes are Controller-backed and exposed through manager routes.
