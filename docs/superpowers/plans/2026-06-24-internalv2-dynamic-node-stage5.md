# internalv2 Dynamic Node Lifecycle Stage 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the future-safe node removal path by adding Channel inventory, gateway drain mode, runtime drain checks, and explicit `removed` transition.

**Architecture:** Stage 5 turns Stage 4 scale-in status into a final safe-to-remove gate. The manager scans authoritative `ChannelRuntimeMeta` by physical Slot, enables gateway admission drain on the leaving node, verifies runtime summaries are fresh and empty, and calls `MarkNodeRemoved` only when every Slot, Channel, task, and connection blocker is gone.

**Tech Stack:** Go, internalv2 management usecase, metadb ChannelRuntimeMeta scans, gateway admission control, internalv2 node RPC, ControllerV2 lifecycle writes, manager HTTP routes, e2ev2 drain smoke.

---

## Scope

This plan implements only the "Stage 5: Channel And Connection Drain" section of:

- `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage4.md`
- Next stage: none

Stage 5 is the first stage that can write `removed`. It must keep `removed` behind a fail-closed report; a direct manager call cannot skip inventory or drain checks.

## Entry Gate

- [ ] Stage 4 is implemented and committed.
- [ ] Scale-in preparation tests pass:

```bash
go test ./internalv2/usecase/management -run 'TestScaleInStatus|TestAdvanceNodeScaleIn' -count=1
go test ./internalv2/access/manager -run 'TestManagerScaleIn' -count=1
```

## File Structure

- Modify `pkg/controllerv2/runtime_node_lifecycle.go`
  - Adds `MarkNodeRemoved`, allowed only for `leaving` non-controller data nodes.
- Modify `pkg/controllerv2/runtime_test.go`
  - Verifies `leaving -> removed`, active rejection, and controller-voter rejection.
- Modify `pkg/clusterv2/control/node_lifecycle.go`, `pkg/clusterv2/control/codec.go`, `pkg/clusterv2/control/runtime.go`, and `pkg/clusterv2/control/transport.go`
  - Adds control facade and forwarding for removed writes.
- Create `internalv2/usecase/management/channel_drain.go`
  - Adds full target-node Channel inventory and drain blockers.
- Create `internalv2/usecase/management/channel_drain_test.go`
  - Verifies paging by physical Slot, leader/replica/ISR counts, active migration counts, and scan-error unsafe status.
- Modify `internalv2/usecase/management/scale_in.go`
  - Merges Slot, task, Channel, gateway, and runtime blockers into `SafeToRemove`.
- Create `internalv2/usecase/management/gateway_drain.go`
  - Adds `SetNodeDrainMode` usecase and `GatewayDrainWriter` port.
- Modify `internalv2/access/node/manager_connection_codec.go`
  - Adds node RPC operation for runtime drain mode.
- Modify `internalv2/access/node/manager_connection_rpc.go`
  - Adds client/server methods for `SetManagerDrainMode`.
- Modify `internalv2/infra/cluster/management_connections.go`
  - Routes remote drain mode requests through node RPC.
- Modify `internalv2/app/*`
  - Wires local gateway drain writer and remote manager connection writer.
- Create `internalv2/access/manager/scale_in_remove.go`
  - Adds final safe-remove route handler.
- Create `internalv2/access/manager/scale_in_remove_test.go`
  - Verifies `safe_to_remove=false` blocks remove and `true` returns accepted.
- Modify `internalv2/*/FLOW.md`
  - Documents final drain and removal path.
- Create `test/e2ev2/cluster/dynamic_node_join/node_remove_drain_test.go`
  - Verifies a leaving node with no Slot/Channel/session blockers can be marked removed.

## Task 1: Add ControllerV2 MarkNodeRemoved

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
		t.Fatalf("MarkNodeRemoved() = %#v, want removed down node", result)
	}
}

func TestRuntimeMarkNodeRemovedRejectsActiveNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-active")
	joinAndActivateNode(t, runtime, 4, "n4")

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err == nil {
		t.Fatal("MarkNodeRemoved(active) error = nil, want rejection")
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntimeMarkNodeRemoved' -count=1
```

Expected: FAIL because `MarkNodeRemoved` is not defined.

- [ ] **Step 3: Implement MarkNodeRemoved**

In `pkg/controllerv2/runtime_node_lifecycle.go`, add:

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

// MarkNodeRemoved marks a drained leaving data node as removed.
func (r *Runtime) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResult, error) {
	if req.NodeID == 0 {
		return MarkNodeRemovedResult{}, fmt.Errorf("controllerv2: node id is required")
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	node, changed, err := buildMarkNodeRemoved(st, req.NodeID)
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	if !changed {
		return MarkNodeRemovedResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	if err := r.raft.Propose(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         time.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}); err != nil {
		return MarkNodeRemovedResult{}, err
	}
	st, err = r.LocalState(ctx)
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	return MarkNodeRemovedResult{Changed: true, Node: node, Revision: st.Revision}, nil
}

func buildMarkNodeRemoved(st ClusterState, nodeID uint64) (Node, bool, error) {
	for _, existing := range st.Nodes {
		if existing.NodeID != nodeID {
			continue
		}
		if existing.HasRole(NodeRoleControllerVoter) {
			return Node{}, false, fmt.Errorf("controllerv2: controller voter %d cannot be removed", nodeID)
		}
		if existing.JoinState == NodeJoinStateRemoved {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateLeaving {
			return Node{}, false, fmt.Errorf("controllerv2: node %d is %q, want leaving", nodeID, existing.JoinState)
		}
		next := existing
		next.JoinState = NodeJoinStateRemoved
		next.Status = NodeStatusDown
		return next, true, nil
	}
	return Node{}, false, fmt.Errorf("controllerv2: node %d not found", nodeID)
}
```

- [ ] **Step 4: Add clusterv2 control-write forwarding**

Add `MarkNodeRemovedRequest`, `MarkNodeRemovedResult`, `ControlWriteActionMarkNodeRemoved`, control-write handler switch branch, `ControlWriteApplier.MarkNodeRemoved`, and `Runtime.MarkNodeRemoved` using the same pattern as `MarkNodeLeaving`. Do not add `removed` lifecycle writes to the task RPC.

- [ ] **Step 5: Run lifecycle tests and commit**

Run:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/control -run 'TestRuntimeMarkNodeRemoved|TestControlWriteRequest.*Removed' -count=1
```

Expected: PASS.

Commit:

```bash
git add pkg/controllerv2 pkg/clusterv2/control
git commit -m "feat: mark drained nodes removed"
```

## Task 2: Add Full Channel Drain Inventory

**Files:**
- Create: `internalv2/usecase/management/channel_drain.go`
- Create: `internalv2/usecase/management/channel_drain_test.go`
- Modify: `internalv2/usecase/management/scale_in.go`

- [ ] **Step 1: Write failing Channel inventory tests**

Create `internalv2/usecase/management/channel_drain_test.go`:

```go
func TestNodeChannelDrainInventoryCountsTargetRolesAcrossSlots(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{pages: map[uint32][][]metadb.ChannelRuntimeMeta{
		1: {{
			{ChannelID: "leader", ChannelType: 1, Leader: 4, Replicas: []uint64{2, 3, 4}, ISR: []uint64{2, 3, 4}},
			{ChannelID: "replica", ChannelType: 1, Leader: 2, Replicas: []uint64{2, 3, 4}, ISR: []uint64{2, 3}},
		}},
		2: {{
			{ChannelID: "isr", ChannelType: 1, Leader: 2, Replicas: []uint64{2, 3, 5}, ISR: []uint64{3, 4}},
		}},
	}}
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}, {SlotID: 2}}}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 2})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if inv.Unknown || inv.LeaderCount != 1 || inv.ReplicaCount != 2 || inv.ISRCount != 2 {
		t.Fatalf("inventory = %#v, want counted target roles", inv)
	}
}

func TestNodeChannelDrainInventoryFailsClosedOnScanError(t *testing.T) {
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: &fakeChannelRuntimeMetaReader{err: errors.New("scan failed")},
	})
	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 2})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if !inv.Unknown || inv.Safe {
		t.Fatalf("inventory = %#v, want unknown unsafe", inv)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory' -count=1
```

Expected: FAIL because inventory types and methods are not defined.

- [ ] **Step 3: Implement inventory scan**

Create `internalv2/usecase/management/channel_drain.go`:

```go
const (
	DefaultChannelDrainScanLimit = 256
	MaxChannelDrainScanLimit     = 1024
)

type NodeChannelDrainInventoryRequest struct {
	NodeID    uint64
	PageLimit int
}

type NodeChannelDrainInventoryResponse struct {
	NodeID               uint64
	Safe                 bool
	Unknown              bool
	ScannedSlotCount     int
	LeaderCount          int
	ReplicaCount         int
	ISRCount             int
	ActiveMigrationCount int
	LastError            string
}

func (a *App) NodeChannelDrainInventory(ctx context.Context, req NodeChannelDrainInventoryRequest) (NodeChannelDrainInventoryResponse, error) {
	if req.NodeID == 0 {
		return NodeChannelDrainInventoryResponse{}, metadb.ErrInvalidArgument
	}
	limit := normalizeChannelDrainLimit(req.PageLimit)
	resp := NodeChannelDrainInventoryResponse{NodeID: req.NodeID, Safe: true}
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		resp.Safe = false
		resp.Unknown = true
		return resp, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeChannelDrainInventoryResponse{}, err
	}
	for _, slotID := range sortedSnapshotSlotIDs(snapshot.Slots) {
		after := metadb.ChannelRuntimeMetaCursor{}
		for {
			page, next, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, limit)
			if err != nil {
				resp.Safe = false
				resp.Unknown = true
				resp.LastError = err.Error()
				return resp, nil
			}
			for _, meta := range page {
				countChannelDrainMeta(&resp, req.NodeID, meta)
			}
			if done || next == after {
				break
			}
			after = next
		}
		resp.ScannedSlotCount++
	}
	resp.Safe = !resp.Unknown && resp.LeaderCount == 0 && resp.ReplicaCount == 0 && resp.ISRCount == 0 && resp.ActiveMigrationCount == 0
	return resp, nil
}
```

`countChannelDrainMeta` must count target node as leader, replica, and ISR independently. Active channel migration tasks involving the target must also increment `ActiveMigrationCount` when the metadata reader exposes them through the existing metadb task records.

- [ ] **Step 4: Merge inventory into scale-in status**

Extend `NodeScaleInStatusResponse`:

```go
	SafeToRemove           bool
	BlockedByChannels     bool
	BlockedByRuntimeDrain bool
	RuntimeUnknown         bool
	GatewayDraining        bool
	AcceptingNewSessions  bool
	GatewaySessions        int
	ActiveOnline           int
	ClosingOnline          int
	PendingActivations     int
	ChannelLeaderCount    int
	ChannelReplicaCount   int
	ChannelISRCount       int
	ActiveChannelTaskCount int
```

`SafeToRemove` is true only when all final blockers are clear:

- Stage 4 `SafeToProceed` is true;
- Channel inventory is known and safe;
- runtime summary is known and fresh;
- `GatewayDraining == true`;
- `AcceptingNewSessions == false`;
- `GatewaySessions == 0`;
- `ActiveOnline == 0`;
- `ClosingOnline == 0`;
- `PendingActivations == 0`.

If runtime summary is missing, stale, or unknown, set `RuntimeUnknown = true`, `BlockedByRuntimeDrain = true`, and keep `SafeToRemove = false`.

- [ ] **Step 5: Run usecase tests and commit**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory|TestScaleInStatus' -count=1
```

Expected: PASS.

Commit:

```bash
git add internalv2/usecase/management
git commit -m "feat: add channel drain inventory"
```

## Task 3: Add Gateway Drain Mode Through Management RPC

**Files:**
- Create: `internalv2/usecase/management/gateway_drain.go`
- Create: `internalv2/usecase/management/gateway_drain_test.go`
- Modify: `internalv2/access/node/manager_connection_codec.go`
- Modify: `internalv2/access/node/manager_connection_rpc.go`
- Modify: `internalv2/access/node/manager_connection_rpc_test.go`
- Modify: `internalv2/infra/cluster/management_connections.go`
- Modify: `internalv2/infra/cluster/management_connections_test.go`
- Modify: `internalv2/app/*`

- [ ] **Step 1: Write failing gateway drain usecase test**

Create `internalv2/usecase/management/gateway_drain_test.go`:

```go
func TestSetNodeDrainModeDelegatesToWriter(t *testing.T) {
	writer := &fakeGatewayDrainWriter{}
	app := NewApp(Options{GatewayDrain: writer})

	resp, err := app.SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true})
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}
	if writer.nodeID != 4 || !writer.draining {
		t.Fatalf("writer node=%d draining=%v, want node 4 draining", writer.nodeID, writer.draining)
	}
	if !resp.Draining || resp.AcceptingNewSessions {
		t.Fatalf("response = %#v, want draining and not accepting", resp)
	}
}
```

- [ ] **Step 2: Implement usecase port**

In `internalv2/usecase/management/nodes.go`, extend the existing `NodeRuntimeSummary` from Stage 4:

```go
	Draining             bool
	AcceptingNewSessions bool
	GatewaySessions      int
	ActiveOnline         int
	ClosingOnline        int
	PendingActivations   int
	Fresh                bool
```

Create `internalv2/usecase/management/gateway_drain.go`:

```go
type GatewayDrainWriter interface {
	SetNodeDrainMode(context.Context, uint64, bool) (NodeRuntimeSummary, error)
}

type SetNodeDrainModeRequest struct {
	NodeID   uint64
	Draining bool
}

type SetNodeDrainModeResponse struct {
	NodeID               uint64
	Draining             bool
	AcceptingNewSessions bool
	GatewaySessions      int
	ActiveOnline         int
	ClosingOnline        int
	PendingActivations   int
	Unknown              bool
}

func (a *App) SetNodeDrainMode(ctx context.Context, req SetNodeDrainModeRequest) (SetNodeDrainModeResponse, error) {
	if req.NodeID == 0 || a.gatewayDrain == nil {
		return SetNodeDrainModeResponse{}, metadb.ErrInvalidArgument
	}
	summary, err := a.gatewayDrain.SetNodeDrainMode(ctx, req.NodeID, req.Draining)
	if err != nil {
		return SetNodeDrainModeResponse{}, err
	}
	return SetNodeDrainModeResponse{
		NodeID: summary.NodeID, Draining: summary.Draining, AcceptingNewSessions: summary.AcceptingNewSessions,
		GatewaySessions: summary.GatewaySessions, ActiveOnline: summary.ActiveOnline, ClosingOnline: summary.ClosingOnline,
		PendingActivations: summary.PendingActivations, Unknown: summary.Unknown,
	}, nil
}
```

- [ ] **Step 3: Add node RPC operation**

In `internalv2/access/node/manager_connection_codec.go`, add an operation:

```go
managerConnectionOpSetDrainMode = "set_drain_mode"
```

Extend request encoding with:

```go
Draining bool
```

In `internalv2/access/node/manager_connection_rpc.go`, add:

```go
func (c *Client) SetManagerDrainMode(ctx context.Context, nodeID uint64, draining bool) (managementusecase.NodeRuntimeSummary, error) {
	resp, err := c.callManagerConnection(ctx, nodeID, managerConnectionRPCRequest{Op: managerConnectionOpSetDrainMode, NodeID: nodeID, Draining: draining})
	if err != nil {
		return managementusecase.NodeRuntimeSummary{}, err
	}
	if resp.Err != "" {
		return managementusecase.NodeRuntimeSummary{}, errors.New(resp.Err)
	}
	return resp.Summary, nil
}
```

Server handling must call the local app gateway wrapper:

```go
summary, err := a.managerConnections.SetNodeDrainMode(ctx, req.NodeID, req.Draining)
```

- [ ] **Step 4: Wire local gateway**

In internalv2 app, implement local drain writer:

```go
func (w gatewayDrainWriter) SetNodeDrainMode(ctx context.Context, nodeID uint64, draining bool) (management.NodeRuntimeSummary, error) {
	if nodeID != w.localNodeID {
		return w.remote.SetManagerDrainMode(ctx, nodeID, draining)
	}
	w.gateway.SetAcceptingNewSessions(!draining)
	summary := w.runtimeSummary()
	summary.Draining = draining
	summary.AcceptingNewSessions = w.gateway.AcceptingNewSessions()
	summary.PendingActivations = w.presence.PendingActivations(nodeID)
	return summary, nil
}
```

- [ ] **Step 5: Merge runtime drain blockers into scale-in status**

Add to `internalv2/usecase/management/scale_in_test.go`:

```go
func TestScaleInStatusBlocksWhenRuntimeDrainIsNotEmpty(t *testing.T) {
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: stage4SafeToProceedSnapshot()},
		ChannelRuntimeMeta: emptyChannelRuntimeMetaReader{},
		RuntimeSummary: fakeRuntimeSummaryReader{summary: NodeRuntimeSummary{
			NodeID: 4, Draining: true, AcceptingNewSessions: false, GatewaySessions: 1,
		}},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToRemove || !status.BlockedByRuntimeDrain || status.GatewaySessions != 1 {
		t.Fatalf("status = %#v, want runtime drain blocker", status)
	}
}

func TestScaleInStatusSafeOnlyAfterRuntimeDrainEmpty(t *testing.T) {
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: stage4SafeToProceedSnapshot()},
		ChannelRuntimeMeta: emptyChannelRuntimeMetaReader{},
		RuntimeSummary: fakeRuntimeSummaryReader{summary: NodeRuntimeSummary{
			NodeID: 4, Draining: true, AcceptingNewSessions: false,
		}},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.SafeToRemove {
		t.Fatalf("status = %#v, want safe after full drain", status)
	}
}
```

The `NodeScaleInStatus` implementation must call `RuntimeSummaryReader` after Channel inventory and merge runtime fields into the same response. Any of these conditions must block final removal: `Unknown`, stale runtime freshness, not draining, still accepting new sessions, active gateway sessions, active online sessions, closing online sessions, or pending presence activations.

- [ ] **Step 6: Run tests and commit**

Run:

```bash
go test ./internalv2/usecase/management -run TestSetNodeDrainModeDelegatesToWriter -count=1
go test ./internalv2/usecase/management -run 'TestScaleInStatus.*RuntimeDrain' -count=1
go test ./internalv2/access/node -run 'TestManagerConnectionRPC.*Drain|TestManagerConnectionCodec.*Drain' -count=1
go test ./internalv2/infra/cluster -run TestManagementConnectionReader -count=1
go test ./internalv2/app -count=1
```

Expected: PASS.

Commit:

```bash
git add internalv2/usecase/management internalv2/access/node internalv2/infra/cluster internalv2/app
git commit -m "feat: add manager gateway drain mode"
```

## Task 4: Gate Final Remove Behind Safe-To-Remove

**Files:**
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`
- Create: `internalv2/access/manager/scale_in_remove.go`
- Create: `internalv2/access/manager/scale_in_remove_test.go`
- Modify: `internalv2/access/manager/server.go`

- [ ] **Step 1: Write failing safe remove tests**

Add to `internalv2/usecase/management/scale_in_test.go`:

```go
func TestMarkNodeRemovedRequiresSafeToRemove(t *testing.T) {
	writer := &fakeNodeLifecycleWriter{}
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: unsafeScaleInSnapshot()},
		NodeLifecycle: writer,
	})

	_, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeScaleInUnsafe) {
		t.Fatalf("MarkNodeRemoved() error = %v, want ErrNodeScaleInUnsafe", err)
	}
}

func TestMarkNodeRemovedDelegatesWhenSafe(t *testing.T) {
	writer := &fakeNodeLifecycleWriter{}
	app := NewApp(Options{
		Control: fakeControlSnapshotReader{snap: safeRemoveSnapshot()},
		RuntimeSummary: safeRuntimeSummaryReader{},
		ChannelRuntimeMeta: emptyChannelRuntimeMetaReader{},
		NodeLifecycle: writer,
	})

	resp, err := app.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !resp.Changed || writer.removed.NodeID != 4 {
		t.Fatalf("response=%#v writer=%#v, want removed node 4", resp, writer.removed)
	}
}
```

- [ ] **Step 2: Implement safe remove usecase**

Add:

```go
var ErrNodeScaleInUnsafe = errors.New("node scale-in is unsafe")

type MarkNodeRemovedRequest struct {
	NodeID uint64
}

type MarkNodeRemovedResponse struct {
	Changed   bool
	NodeID    uint64
	JoinState string
	Revision  uint64
}

func (a *App) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResponse, error) {
	status, err := a.NodeScaleInStatus(ctx, NodeScaleInStatusRequest{NodeID: req.NodeID})
	if err != nil {
		return MarkNodeRemovedResponse{}, err
	}
	if !status.SafeToRemove {
		return MarkNodeRemovedResponse{}, ErrNodeScaleInUnsafe
	}
	result, err := a.nodeLifecycle.MarkNodeRemoved(ctx, control.MarkNodeRemovedRequest{NodeID: req.NodeID})
	if err != nil {
		return MarkNodeRemovedResponse{}, err
	}
	return MarkNodeRemovedResponse{Changed: result.Changed, NodeID: result.Node.NodeID, JoinState: string(result.Node.JoinState), Revision: result.Revision}, nil
}
```

- [ ] **Step 3: Add manager route**

Register:

```text
POST /manager/nodes/:node_id/scale-in/remove
```

Handler behavior:

- calls `management.App.MarkNodeRemoved`;
- returns `409 Conflict` with the current unsafe report when `ErrNodeScaleInUnsafe`;
- returns `202 Accepted` when changed and `200 OK` for idempotent removed.

- [ ] **Step 4: Run usecase and manager tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestMarkNodeRemoved|TestScaleInStatus' -count=1
go test ./internalv2/access/manager -run 'TestManagerScaleInRemove' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit final remove gate**

```bash
git add internalv2/usecase/management internalv2/access/manager
git commit -m "feat: gate node removal on drain safety"
```

## Task 5: Add e2ev2 Remove Drain Smoke And FLOW Docs

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_join/node_remove_drain_test.go`
- Modify: `test/e2ev2/suite/*`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/access/node/FLOW.md`

- [ ] **Step 1: Write e2ev2 smoke**

Create `test/e2ev2/cluster/dynamic_node_join/node_remove_drain_test.go`:

```go
func TestLeavingNodeCanBeRemovedAfterDrain(t *testing.T) {
	cluster := suite.StartCluster(t, suite.ClusterConfig{Nodes: 4})
	manager := cluster.ManagerClient(t, 1)

	manager.MustStartScaleIn(t, 4)
	manager.MustSetDrainMode(t, 4, true)
	manager.EventuallyNodeDrainEmpty(t, 4, 30*time.Second)
	manager.MustAdvanceScaleInUntilNoSlotBlockers(t, 4, 1, 2*time.Minute)
	manager.EventuallyScaleInSafeToRemove(t, 4, 2*time.Minute)
	manager.MustRemoveNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
}
```

- [ ] **Step 2: Run e2ev2 smoke**

Run:

```bash
go test ./test/e2ev2/cluster/dynamic_node_join -run TestLeavingNodeCanBeRemovedAfterDrain -count=1
```

Expected: PASS.

- [ ] **Step 3: Update FLOW docs**

Document:

```text
manager scale-in remove route
  -> management safe-to-remove status
  -> ChannelRuntimeMeta full inventory
  -> gateway drain summary
  -> ControllerV2 MarkNodeRemoved
  -> control snapshot removed tombstone
```

Also document node RPC drain mode:

```text
manager usecase
  -> infra ManagementConnectionReader.SetNodeDrainMode
  -> access/node manager connection RPC
  -> local gateway.SetAcceptingNewSessions(false)
  -> runtime summary
```

- [ ] **Step 4: Run final Stage 5 verification**

Run:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/control ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app
go test ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable|TestLeavingNodeCanBeRemovedAfterDrain' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 5: Commit e2ev2 and docs**

```bash
git add test/e2ev2 internalv2/infra/cluster/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md internalv2/access/node/FLOW.md
git commit -m "test: verify drained node removal"
```

## Exit Gate

- [ ] Run broad focused verification:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/... ./internalv2/... ./test/e2ev2/cluster/dynamic_node_join
git diff --check
```

Expected: all commands pass.

- [ ] Confirm final lifecycle path:

```bash
rg -n "MarkNodeRemoved|mark_node_removed|SafeToRemove|NodeChannelDrainInventory|SetNodeDrainMode|set_drain_mode|removed" pkg internalv2 test/e2ev2
```

Expected: `removed` is reachable only through the safe-to-remove usecase path.
