# internalv2 Dynamic Node Lifecycle Stage 5C Gateway Drain Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let managers put a leaving node gateway into drain mode and make scale-in status fail closed until runtime drain counters are empty.

**Architecture:** Stage 4 already reports `NodeRuntimeSummary` with gateway session counts, online counts, `AcceptingNewSessions`, and `Draining`. This sub-stage adds a management write path for drain mode, routes remote drain writes through the existing manager connection RPC service, and merges runtime drain blockers into scale-in status.

**Tech Stack:** Go, internalv2 management usecase, manager HTTP route, internalv2 node RPC binary codec, clusterv2 RPC forwarding, pkg/gateway admission control.

---

## Scope

Implements only Stage 5C from:

- Stage 5 index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5.md`
- Previous sub-stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage5b-channel-drain-inventory.md`

This sub-stage must not call `MarkNodeRemoved` and must not add `/scale-in/remove`.

## Entry Gate

- [ ] Stage 5B is merged into local `main`.
- [ ] Channel drain status tests pass:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestNodeChannelDrainInventory|TestScaleInStatus' -count=1
```

Expected: PASS.

## File Structure

- Modify `internalv2/usecase/management/nodes.go`
  - Adds `PendingActivations` to `NodeRuntimeSummary`.
- Create `internalv2/usecase/management/gateway_drain.go`
  - Adds `GatewayDrainWriter` and `SetNodeDrainMode`.
- Create `internalv2/usecase/management/gateway_drain_test.go`
  - Verifies validation, delegation, and response mapping.
- Modify `internalv2/usecase/management/scale_in.go`
  - Adds runtime drain blockers and `SafeToRemove`.
- Modify `internalv2/usecase/management/scale_in_test.go`
  - Verifies runtime drain blockers and safe runtime state.
- Modify `internalv2/access/node/manager_connection_codec.go`
  - Adds `set_drain_mode` request operation and `Draining` request field.
- Modify `internalv2/access/node/manager_connection_codec_test.go`
  - Verifies codec round-trip and `PendingActivations`.
- Modify `internalv2/access/node/manager_connection_rpc.go`
  - Adds server dispatch and client call for drain mode.
- Modify `internalv2/access/node/manager_connection_rpc_test.go`
  - Verifies remote drain RPC calls service.
- Modify `internalv2/infra/cluster/management_connections.go`
  - Adds `SetNodeDrainMode` to the cluster-routed manager connection adapter.
- Modify `internalv2/infra/cluster/management_connections_test.go`
  - Verifies remote drain mode routing.
- Modify `internalv2/app/runtime_summary.go`
  - Populates `PendingActivations`.
- Modify `internalv2/app/*`
  - Wires local gateway drain writer into management options.
  - Fails closed when a remote target node has no remote drain writer.
- Create `internalv2/access/manager/scale_in_drain.go`
  - Adds manager HTTP drain route.
- Create `internalv2/access/manager/scale_in_drain_test.go`
  - Verifies route parsing, permissions, and DTO mapping.
- Modify `internalv2/access/manager/server.go`
  - Registers drain route.
- Modify `internalv2/access/manager/server_test.go`
  - Extends manager test stub.

## Task 1: Add Gateway Drain Usecase

**Files:**
- Modify: `internalv2/usecase/management/nodes.go`
- Create: `internalv2/usecase/management/gateway_drain.go`
- Create: `internalv2/usecase/management/gateway_drain_test.go`

- [ ] **Step 1: Write failing usecase tests**

Create `internalv2/usecase/management/gateway_drain_test.go`:

```go
package management

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSetNodeDrainModeDelegatesToWriter(t *testing.T) {
	writer := &gatewayDrainWriterStub{summary: NodeRuntimeSummary{
		NodeID: 4, Draining: true, AcceptingNewSessions: false, GatewaySessions: 2, ActiveOnline: 1, PendingActivations: 1,
	}}
	app := New(Options{GatewayDrain: writer})

	resp, err := app.SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true})
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}
	if writer.nodeID != 4 || !writer.draining {
		t.Fatalf("writer node=%d draining=%v, want node 4 draining", writer.nodeID, writer.draining)
	}
	if !resp.Draining || resp.AcceptingNewSessions || resp.GatewaySessions != 2 || resp.ActiveOnline != 1 || resp.PendingActivations != 1 {
		t.Fatalf("response = %#v, want mapped drain summary", resp)
	}
}

func TestSetNodeDrainModeRejectsInvalidInputAndMissingWriter(t *testing.T) {
	if _, err := New(Options{GatewayDrain: &gatewayDrainWriterStub{}}).SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("SetNodeDrainMode(invalid) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true}); !errors.Is(err, ErrNodeScaleInUnavailable) {
		t.Fatalf("SetNodeDrainMode(missing writer) error = %v, want ErrNodeScaleInUnavailable", err)
	}
}

type gatewayDrainWriterStub struct {
	nodeID   uint64
	draining bool
	summary  NodeRuntimeSummary
	err      error
}

func (w *gatewayDrainWriterStub) SetNodeDrainMode(_ context.Context, nodeID uint64, draining bool) (NodeRuntimeSummary, error) {
	w.nodeID = nodeID
	w.draining = draining
	return w.summary, w.err
}
```

- [ ] **Step 2: Verify RED**

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestSetNodeDrainMode -count=1
```

Expected: FAIL because `GatewayDrain`, `PendingActivations`, and `SetNodeDrainMode` do not exist.

- [ ] **Step 3: Implement usecase port and response**

Extend `Options` and `App` in `internalv2/usecase/management/nodes.go`:

```go
// GatewayDrain mutates gateway admission drain mode locally or remotely.
GatewayDrain GatewayDrainWriter
```

Add `gatewayDrain GatewayDrainWriter` to `App` and wire it in `New`.

Extend `NodeRuntimeSummary`:

```go
// PendingActivations counts local sessions accepted but not yet authority-active.
PendingActivations int
```

Create `internalv2/usecase/management/gateway_drain.go`:

```go
package management

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// GatewayDrainWriter toggles gateway admission for a target node and returns fresh runtime counters.
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
	TotalOnline          int
	PendingActivations   int
	Unknown              bool
}

func (a *App) SetNodeDrainMode(ctx context.Context, req SetNodeDrainModeRequest) (SetNodeDrainModeResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SetNodeDrainModeResponse{}, err
	}
	if req.NodeID == 0 {
		return SetNodeDrainModeResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.gatewayDrain == nil {
		return SetNodeDrainModeResponse{}, ErrNodeScaleInUnavailable
	}
	summary, err := a.gatewayDrain.SetNodeDrainMode(ctx, req.NodeID, req.Draining)
	if err != nil {
		return SetNodeDrainModeResponse{}, err
	}
	return SetNodeDrainModeResponse{
		NodeID: summary.NodeID, Draining: summary.Draining, AcceptingNewSessions: summary.AcceptingNewSessions,
		GatewaySessions: summary.GatewaySessions, ActiveOnline: summary.ActiveOnline, ClosingOnline: summary.ClosingOnline,
		TotalOnline: summary.TotalOnline, PendingActivations: summary.PendingActivations, Unknown: summary.Unknown,
	}, nil
}
```

- [ ] **Step 4: Verify usecase GREEN**

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestSetNodeDrainMode -count=1
```

Expected: PASS.

## Task 2: Add Manager Connection RPC Drain Operation

**Files:**
- Modify: `internalv2/access/node/manager_connection_codec.go`
- Modify: `internalv2/access/node/manager_connection_codec_test.go`
- Modify: `internalv2/access/node/manager_connection_rpc.go`
- Modify: `internalv2/access/node/manager_connection_rpc_test.go`
- Modify: `internalv2/infra/cluster/management_connections.go`
- Modify: `internalv2/infra/cluster/management_connections_test.go`

- [ ] **Step 1: Add failing codec/RPC tests**

Add codec test:

```go
func TestManagerConnectionDrainModeCodecRoundTrip(t *testing.T) {
	encoded, err := encodeManagerConnectionRequest(managerConnectionRPCRequest{
		Op: managerConnectionOpSetDrainMode, NodeID: 4, Draining: true,
	})
	if err != nil {
		t.Fatalf("encode request error = %v", err)
	}
	got, err := decodeManagerConnectionRequest(encoded)
	if err != nil {
		t.Fatalf("decode request error = %v", err)
	}
	if got.Op != managerConnectionOpSetDrainMode || got.NodeID != 4 || !got.Draining {
		t.Fatalf("request = %#v, want set drain mode node 4", got)
	}
}
```

Add RPC test:

```go
func TestManagerConnectionRPCSetDrainMode(t *testing.T) {
	service := &fakeManagerConnectionService{runtimeSummary: managementusecase.NodeRuntimeSummary{
		NodeID: 4, Draining: true, AcceptingNewSessions: false, PendingActivations: 1,
	}}
	adapter := New(AdapterOptions{ManagerConnections: service})
	body, err := encodeManagerConnectionRequest(managerConnectionRPCRequest{Op: managerConnectionOpSetDrainMode, NodeID: 4, Draining: true})
	if err != nil {
		t.Fatalf("encode request error = %v", err)
	}

	respBody, err := adapter.HandleManagerConnectionRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerConnectionRPC() error = %v", err)
	}
	resp, err := decodeManagerConnectionResponse(respBody)
	if err != nil {
		t.Fatalf("decode response error = %v", err)
	}
	if service.drainNodeID != 4 || !service.draining || !resp.Summary.Draining || resp.Summary.PendingActivations != 1 {
		t.Fatalf("service=%#v response=%#v, want drain summary", service, resp.Summary)
	}
}
```

- [ ] **Step 2: Implement codec and RPC**

In `manager_connection_codec.go`:

- add `managerConnectionOpSetDrainMode = "set_drain_mode"`;
- add a new op ID;
- add `Draining bool` to `managerConnectionRPCRequest`;
- encode/decode `Draining` after `Limit`;
- encode/decode `NodeRuntimeSummary.PendingActivations`.

In `manager_connection_rpc.go`:

- extend `ManagerConnectionReader` interface in `presence_rpc.go` or the local type definition to include `SetNodeDrainMode`;
- dispatch `managerConnectionOpSetDrainMode` to `a.managerConnections.SetNodeDrainMode(ctx, req.NodeID, req.Draining)`;
- add client method `SetManagerDrainMode(ctx, nodeID uint64, draining bool)`.

In `internalv2/infra/cluster/management_connections.go`, add:

```go
func (r *ManagementConnectionReader) SetNodeDrainMode(ctx context.Context, nodeID uint64, draining bool) (managementusecase.NodeRuntimeSummary, error) {
	if r == nil || r.remote == nil {
		return managementusecase.NodeRuntimeSummary{}, managementusecase.ErrNodeScaleInUnavailable
	}
	return r.remote.SetManagerDrainMode(ctx, nodeID, draining)
}
```

- [ ] **Step 3: Verify RPC GREEN**

```bash
GOWORK=off go test ./internalv2/access/node -run 'TestManagerConnection.*Drain|TestManagerConnectionRuntimeSummaryCodecCarriesControlRevision' -count=1
GOWORK=off go test ./internalv2/infra/cluster -run 'TestManagementConnectionReader.*Drain|TestManagementConnectionReaderRoutesRuntimeSummary' -count=1
```

Expected: PASS.

## Task 3: Wire Local Gateway Drain Writer And Manager Route

**Files:**
- Modify: `internalv2/app/runtime_summary.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app_test.go`
- Create: `internalv2/access/manager/scale_in_drain.go`
- Create: `internalv2/access/manager/scale_in_drain_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Add failing app and manager tests**

Add app test verifying local drain toggles gateway admission:

```go
func TestManagementGatewayDrainWriterTogglesLocalGateway(t *testing.T) {
	gateway := &stateGateway{accepting: true}
	app := &App{gateway: gateway}
	writer := managementGatewayDrainWriter{app: app, localNodeID: 1}

	summary, err := writer.SetNodeDrainMode(context.Background(), 1, true)
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}
	if gateway.AcceptingNewSessions() || !summary.Draining || summary.AcceptingNewSessions {
		t.Fatalf("gateway accepting=%v summary=%#v, want local drain", gateway.AcceptingNewSessions(), summary)
	}
}
```

Add manager test for:

```text
POST /manager/nodes/:node_id/scale-in/drain
```

Body:

```json
{"draining":true}
```

Expected: requires `cluster.node:w`, calls `SetNodeDrainMode`, returns `200 OK` with `draining`, `accepting_new_sessions`, `gateway_sessions`, `active_online`, `closing_online`, `total_online`, `pending_activations`, and `unknown`.

- [ ] **Step 2: Implement local writer**

In app wiring, create `managementGatewayDrainWriter`:

```go
type managementGatewayDrainWriter struct {
	app         *App
	localNodeID uint64
	remote      interface {
		SetNodeDrainMode(context.Context, uint64, bool) (managementusecase.NodeRuntimeSummary, error)
	}
}

func (w managementGatewayDrainWriter) SetNodeDrainMode(ctx context.Context, nodeID uint64, draining bool) (managementusecase.NodeRuntimeSummary, error) {
	if nodeID != w.localNodeID {
		if w.remote == nil {
			return managementusecase.NodeRuntimeSummary{NodeID: nodeID, Unknown: true}, managementusecase.ErrNodeScaleInUnavailable
		}
		return w.remote.SetNodeDrainMode(ctx, nodeID, draining)
	}
	if w.app == nil {
		return managementusecase.NodeRuntimeSummary{NodeID: nodeID, Unknown: true}, managementusecase.ErrNodeScaleInUnavailable
	}
	if gatewayRuntime, ok := w.app.gateway.(interface{ SetAcceptingNewSessions(bool) }); ok && gatewayRuntime != nil {
		gatewayRuntime.SetAcceptingNewSessions(!draining)
	}
	return managementRuntimeSummaryReader{app: w.app, localNodeID: w.localNodeID, remote: w.remote}.localRuntimeSummary(ctx, nodeID), nil
}
```

Add an app test that proves remote drain cannot fall through to the local gateway:

```go
func TestManagementGatewayDrainWriterFailsClosedWhenRemoteMissing(t *testing.T) {
	local := &gatewayAdmissionStub{}
	writer := managementGatewayDrainWriter{app: &App{gateway: local}, localNodeID: 1}

	summary, err := writer.SetNodeDrainMode(context.Background(), 4, true)
	if !errors.Is(err, managementusecase.ErrNodeScaleInUnavailable) {
		t.Fatalf("SetNodeDrainMode(remote missing) error = %v, want ErrNodeScaleInUnavailable", err)
	}
	if !summary.Unknown || summary.NodeID != 4 {
		t.Fatalf("summary = %#v, want unknown target node 4", summary)
	}
	if local.acceptingChanged {
		t.Fatalf("remote drain unexpectedly changed local gateway admission")
	}
}

type gatewayAdmissionStub struct {
	acceptingChanged bool
	accepting        bool
}

func (s *gatewayAdmissionStub) SetAcceptingNewSessions(accepting bool) {
	s.acceptingChanged = true
	s.accepting = accepting
}
```

Populate `PendingActivations` in `managementRuntimeSummaryReader.localRuntimeSummary` from `online.Snapshot().Pending`.

- [ ] **Step 3: Implement manager drain route**

Create `internalv2/access/manager/scale_in_drain.go` with DTOs:

```go
type ManagerNodeScaleInDrainRequest struct {
	Draining bool `json:"draining"`
}

type ManagerNodeScaleInDrainResponse struct {
	NodeID               uint64 `json:"node_id"`
	Draining             bool   `json:"draining"`
	AcceptingNewSessions bool   `json:"accepting_new_sessions"`
	GatewaySessions      int    `json:"gateway_sessions"`
	ActiveOnline         int    `json:"active_online"`
	ClosingOnline        int    `json:"closing_online"`
	TotalOnline          int    `json:"total_online"`
	PendingActivations   int    `json:"pending_activations"`
	Unknown              bool   `json:"unknown"`
}
```

Register:

```go
nodeWrites.POST("/nodes/:node_id/scale-in/drain", s.handleNodeScaleInDrain)
```

Reuse scale-in error mapping for unavailable and invalid arguments.

- [ ] **Step 4: Verify app and manager**

```bash
GOWORK=off go test ./internalv2/app -run 'TestManagementGatewayDrainWriter|TestManagementRuntimeSummary' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleInDrain|TestManagerScaleIn' -count=1
```

Expected: PASS.

## Task 4: Merge Runtime Drain Blockers Into Scale-In Status

**Files:**
- Modify: `internalv2/usecase/management/scale_in.go`
- Modify: `internalv2/usecase/management/scale_in_test.go`

- [ ] **Step 1: Add failing runtime drain status tests**

Add tests:

```go
func TestScaleInStatusBlocksWhenRuntimeDrainIsNotEmpty(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snap},
		RuntimeSummary:     fakeNodeRuntimeSummaryReader{summaries: map[uint64]NodeRuntimeSummary{4: {NodeID: 4, ControlRevision: snap.Revision, Draining: true, AcceptingNewSessions: false, GatewaySessions: 1}}},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: emptyChannelDrainMetaReader{},
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if status.SafeToRemove || !status.BlockedByRuntimeDrain || status.GatewaySessions != 1 {
		t.Fatalf("status = %#v, want runtime drain blocker", status)
	}
}

func TestScaleInStatusSafeToRemoveAfterChannelAndRuntimeDrainClear(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
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
	})

	status, err := app.NodeScaleInStatus(context.Background(), NodeScaleInStatusRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeScaleInStatus() error = %v", err)
	}
	if !status.SafeToRemove {
		t.Fatalf("status = %#v, want safe to remove", status)
	}
}
```

- [ ] **Step 2: Extend status response**

Add:

```go
SafeToRemove           bool
BlockedByRuntimeDrain bool
RuntimeUnknown         bool
GatewayDraining        bool
AcceptingNewSessions  bool
GatewaySessions        int
ActiveOnline           int
ClosingOnline          int
TotalOnline            int
PendingActivations     int
```

`SafeToRemove` must require:

- Stage 5B `SafeToProceed == true`;
- Channel inventory known and clear;
- runtime summary for target known;
- `Draining == true`;
- `AcceptingNewSessions == false`;
- `GatewaySessions == 0`;
- `ActiveOnline == 0`;
- `ClosingOnline == 0`;
- `PendingActivations == 0`.

Keep `SafeToProceed` and `SafeToRemove` distinct in code comments and tests. `SafeToProceed` remains the progress gate for Slot/task/Channel blockers, while `SafeToRemove` is the final gate for the manager remove route after gateway drain and runtime counters are clear.

- [ ] **Step 3: Verify Stage 5C**

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'TestSetNodeDrainMode|TestScaleInStatus' -count=1
GOWORK=off go test ./internalv2/access/node -run 'TestManagerConnection.*Drain|TestManagerConnectionRuntimeSummaryCodecCarriesControlRevision' -count=1
GOWORK=off go test ./internalv2/infra/cluster -run 'TestManagementConnectionReader' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'TestManagerScaleInDrain|TestManagerScaleIn' -count=1
GOWORK=off go test ./internalv2/app -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 4: Commit Stage 5C**

```bash
git add internalv2/usecase/management internalv2/access/node internalv2/infra/cluster internalv2/app internalv2/access/manager
git commit -m "feat: add manager gateway drain mode"
```

## Exit Gate

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/access/manager ./internalv2/app -count=1
git diff --check
rg -n "SetNodeDrainMode|set_drain_mode|PendingActivations|SafeToRemove|BlockedByRuntimeDrain|scale-in/drain" internalv2
```

Expected: all commands pass. `SafeToRemove` is true only after Slot, Channel, and runtime drain blockers are clear.
