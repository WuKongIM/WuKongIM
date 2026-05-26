# clusterv2 ControllerV2 Integration Design

Date: 2026-05-26
Status: Design approved by user, pending implementation plan
Owner: Codex

## 1. Purpose

`pkg/clusterv2` should become the distributed entry package for the new cluster runtime. It should own lifecycle, node RPC registration, routing snapshots, Slot runtime wiring, ChannelV2 wiring, and readiness reporting. `pkg/controllerv2` should remain the reusable Controller implementation package that owns durable control-plane state, Controller Raft, FSM apply, bootstrap planning, and full-file state sync.

The integration should connect these packages without merging their responsibilities. `clusterv2.Node` becomes the composition root that starts ControllerV2 and consumes its snapshots. `controllerv2` stays independent from `clusterv2` and exposes implementation primitives through small interfaces.

This design covers the first production-shaped integration slice:

- `clusterv2` can start with a ControllerV2-backed control adapter.
- A single-node cluster can bootstrap ControllerV2 state through the same cluster semantics used by larger topologies.
- ControllerV2 snapshots drive `clusterv2` routing, discovery, and Slot reconciliation.
- Node-to-node RPC gains enough ControllerV2 endpoints for control state sync and Controller Raft message transport.
- Existing test override options remain available for smoke tests and unit tests.

## 2. Non-Goals

- Do not move `pkg/controllerv2` code into `pkg/clusterv2`.
- Do not make `pkg/controllerv2` import `pkg/clusterv2`.
- Do not replace the old `pkg/cluster` startup path in this slice.
- Do not add a bypass path for a single-node cluster. A single node remains a single-node cluster.
- Do not implement dynamic Controller voter membership changes.
- Do not implement node onboarding, scale-in, drain, repair, rebalance, or full operator APIs.
- Do not store high-frequency runtime observations in `cluster-state.json`.
- Do not optimize ControllerV2 RPC codecs beyond a stable versioned envelope in this slice.

## 3. Package Boundary

The packages keep a one-way dependency graph:

```text
pkg/clusterv2
  -> pkg/controllerv2/{state,statefile,fsm,raft,server,sync,planner,command}
  -> pkg/slot/multiraft
  -> pkg/channelv2
  -> pkg/transport

pkg/controllerv2
  -> no dependency on pkg/clusterv2
```

`pkg/clusterv2/control` is the adapter boundary. Other `clusterv2` packages consume only `control.Controller` and `control.Snapshot`; they should not import `pkg/controllerv2` directly.

Recommended layout addition:

```text
pkg/clusterv2/control/
  controller.go          // Existing stable control.Controller contract.
  snapshot.go            // Existing clusterv2 control read model.
  controllerv2.go        // Existing state snapshot mapper.
  runtime.go             // New ControllerV2 runtime wrapper.
  transport.go           // New ControllerV2 Raft/sync RPC adapters.
  codec.go               // New versioned ControllerV2 control RPC payloads.
```

`runtime.go` should be small. If it grows, split voter, mirror, transport, and watch-loop helpers into separate files under `control`.

## 4. Configuration

Extend `clusterv2.ControlConfig` rather than introducing a second public cluster config surface:

```go
type ControlConfig struct {
    // StateDir stores ControllerV2 cluster-state files for this node.
    StateDir string
    // ClusterID is the stable cluster identity used by ControllerV2 state and sync.
    ClusterID string
    // Role declares whether this node is a Controller voter or state mirror.
    Role ControlRole
    // Voters lists Controller voter node IDs and Controller RPC addresses.
    Voters []ControlVoter
    // AllowBootstrap permits this node to initialize an empty ControllerV2 Raft log.
    AllowBootstrap bool
}
```

Potential supporting types:

```go
type ControlRole string

const (
    ControlRoleVoter  ControlRole = "voter"
    ControlRoleMirror ControlRole = "mirror"
)

type ControlVoter struct {
    NodeID uint64
    Addr   string
}
```

Defaulting rules:

- If `Control.StateDir` is empty, use `DataDir/controller`.
- If `Control.Role` is empty, default to `voter` for the first integration slice. This supports single-node cluster bootstrap without separate app wiring.
- If `Control.Voters` is empty, use a single voter `{NodeID: cfg.NodeID, Addr: cfg.ListenAddr}`. This is still a single-node cluster, not a bypass path.
- `AllowBootstrap` must be explicit for multi-node clusters. For the single-node default, `AllowBootstrap` can default to true only when no state file and no voter list are provided.

Validation rules:

- `ClusterID` must be non-empty before production app wiring enables clusterv2.
- `StateDir` must be non-empty after defaults.
- Every voter must have a non-zero node ID and non-empty address.
- Voter node IDs must be unique.
- A voter node must include the local node ID when `Role == voter`.
- `Role == mirror` must not start Controller Raft.
- `AllowBootstrap` must not create a second cluster if a valid `cluster-state.json` already exists.

If these configuration fields later become app-level config keys, `wukongim.conf.example` must be updated in the same implementation change.

## 5. Runtime Shape

Add a ControllerV2-backed implementation of `control.Controller`:

```go
type Runtime struct {
    // owns statefile.Store, optional FSM, optional Raft service,
    // ControllerV2 facade, optional sync client, watch channel, and transport adapters
}
```

For Controller voter nodes:

```text
Start
  -> open statefile.Store at StateDir/cluster-state.json
  -> create fsm.StateMachine(store)
  -> create Controller Raft transport adapter
  -> create controllerv2/raft.Service
  -> create controllerv2/server.Server with proposer + FSM state source
  -> start Raft service
  -> propose InitClusterState when bootstrap is allowed and no state exists
  -> run bounded planner ticks until initial slot assignments are created or blocked
  -> load FSM snapshot
  -> publish adapted clusterv2 control snapshot
```

For mirror nodes:

```text
Start
  -> open statefile.Store at StateDir/cluster-state.json
  -> load local state when present
  -> create full-file sync client over clusterv2 RPC
  -> create controllerv2/server.Server with sync client
  -> run SyncOnce when possible
  -> publish adapted clusterv2 control snapshot
```

The runtime implements:

- `Start(ctx)` and `Stop(ctx)` for lifecycle.
- `LocalSnapshot(ctx)` by adapting the latest ControllerV2 `ClusterState`.
- `LeaderID()` from Raft service for voters or sync client hint for mirrors.
- `Watch()` as non-blocking snapshot notifications after FSM apply, successful sync, or explicit refresh.
- `ReportNode` and `ReportSlots` as best-effort no-ops until ControllerV2 report commands exist.

The existing `ControllerV2Adapter` can remain for tests that inject a simple `ControllerV2StateSource`. The new runtime should reuse `SnapshotFromControllerV2` instead of duplicating mapping logic.

## 6. Node Start Flow

`clusterv2.Node` remains thin. Its `Start` sequence should become:

```text
Start(ctx)
  -> ensure default proposer and ChannelV2 service
  -> ensure default ControllerV2 control runtime when no controller override exists
  -> start injected lifecycle resources
  -> start clusterv2 transport server/client when configured
  -> register ControllerV2 RPC handlers before Controller Raft starts
  -> start control.Controller
  -> read LocalSnapshot
  -> applySnapshot:
       routing.UpdateControlSnapshot
       discovery.Update
       slots.Reconcile
       local readiness snapshot update
  -> start Controller watch loop
  -> mark channels ready
  -> start ChannelV2 tick loop
  -> mark node started
```

Stop order should reverse ownership:

```text
Stop(ctx)
  -> reject foreground work
  -> stop Controller watch loop
  -> stop ChannelV2 tick loop
  -> close ChannelV2 service
  -> stop control.Controller
  -> stop clusterv2 transport client/server if Node owns them
  -> stop injected resources in reverse order
```

No foreground path should synchronously call ControllerV2. `RouteKey`, `RouteHashSlot`, `Propose`, and ChannelV2 append paths should continue to read atomic route/channel snapshots only.

## 7. ControllerV2 Network Integration

Use `pkg/clusterv2/net` as the only node-to-node communication boundary.

Add or finalize message/service IDs:

```go
const (
    MsgControlRaft uint8 = ...
    MsgControlRaftBatch uint8 = ...
)

const (
    RPCControlStateSync uint8 = ...
    RPCControlReportNode uint8 = ...
    RPCControlReportSlots uint8 = ...
)
```

Controller Raft transport:

```text
controllerv2/raft.Transport.Send(messages)
  -> group messages by destination node
  -> encode versioned MsgControlRaftBatch payload
  -> send through clusterv2 typed transport
  -> remote handler decodes and calls raft.Service.Step(ctx, msg)
```

State sync RPC:

```text
mirror sync.Client.SyncOnce
  -> peer picker resolves initial voter IDs from Control.Voters
  -> RPCControlStateSync
  -> leader sync.Server.GetState
  -> response payload installs cluster-state.json through statefile.Store
  -> runtime publishes adapted clusterv2 snapshot
```

The control runtime must not require an already-applied control snapshot to contact Controller voters. Bootstrap and mirror sync should use the explicit `Control.Voters` addresses first. After a valid snapshot is applied, normal `clusterv2` discovery can take over for data-plane and later control-plane calls.

The RPC envelope should include:

- Version byte.
- Message kind or service ID.
- Node IDs needed to validate source/destination.
- Encoded raftpb message batch or sync request/response.

JSON is acceptable for the initial control RPC envelope if tests pin versioning and error behavior. Binary codecs can be deferred until control-plane traffic becomes measurable.

## 8. Bootstrap Semantics

The first integration should support single-node cluster bootstrap through ControllerV2, not through a bypass branch.

Single-node cluster bootstrap:

```text
Config:
  NodeID=1
  ListenAddr=...
  DataDir=...
  Control.Role=voter
  Control.Voters=[{1, ListenAddr}]
  Control.AllowBootstrap=true

Start:
  -> Controller Raft bootstraps one voter
  -> control runtime proposes InitClusterState when no state file exists
  -> bootstrap planner proposes initial slot assignment commands
  -> FSM writes cluster-state.json
  -> clusterv2 applies snapshot
  -> routing and Slot reconcile become ready
```

Multi-node bootstrap should require explicit voters and explicit `AllowBootstrap` on the intended bootstrap voter. Later work can add operator-driven cluster initialization, but this slice should keep bootstrap rules simple and testable.

## 9. Error Handling

Map lower-level errors at the package boundary:

- Invalid ControllerV2 config maps to `clusterv2.ErrInvalidConfig` with wrapped detail.
- Missing or invalid control snapshot causes `Start` to fail before `Node` is marked started.
- Controller Raft not-leader errors stay inside control runtime unless they affect bootstrap planner proposals.
- State sync `NotLeader` updates the mirror leader hint and retries another peer.
- Corrupt local `cluster-state.json` should fail startup rather than silently bootstrap a new cluster.
- Watch delivery should be non-blocking; missed events are acceptable because the next refresh publishes a full snapshot.

Degraded voter behavior:

- If FSM durable save fails, ControllerV2 Raft service reports degraded and must not serve the stale state as authoritative leader sync data.
- `clusterv2.Node.Snapshot()` should expose readiness as not ready when no valid adapted control snapshot exists.

## 10. Testing Strategy

Unit tests:

- `control.Runtime` validates config defaults and rejects invalid voter lists.
- Voter runtime starts with a temp state dir and publishes a valid adapted snapshot.
- Mirror runtime installs a leader state through an in-process sync endpoint.
- Controller Raft transport adapter encodes, decodes, and routes raftpb message batches.
- `SnapshotFromControllerV2` remains the single mapping path and preserves roles, slot assignments, hash-slot ranges, tasks, and revision.

Node tests:

- `clusterv2.New` still accepts injected static controllers for unit tests.
- `Node.Start` creates a default ControllerV2 runtime when no override exists.
- `Node.Start` applies the ControllerV2 snapshot before marking routes ready.
- `Node.Stop` stops default control runtime and transport without leaking goroutines.

Integration tests:

- Single-node cluster starts from an empty temp dir, writes `cluster-state.json`, installs routes, and can route a key.
- Restart from the same temp dir reuses the existing state and does not bootstrap a second cluster.
- Three in-process nodes can exchange Controller Raft messages and converge on the same control snapshot.
- Mirror node can sync full state from the current Controller leader.

Use short timeouts and in-process transports for unit tests. Longer real-network and multi-process tests should be tagged as integration.

## 11. Documentation Updates

Update these files when implementation changes land:

- `pkg/clusterv2/FLOW.md`: remove the limitation that ControllerV2 integration is only snapshot mapping once runtime wiring exists.
- `pkg/controllerv2/FLOW.md`: mention that production-shaped integration is hosted by `pkg/clusterv2`, while `controllerv2` stays reusable.
- `AGENTS.md`: update directory structure only if new directories are added.
- `wukongim.conf.example`: update only if app-level config keys are added.
- `docs/development/PROJECT_KNOWLEDGE.md`: add a short note if a new durable rule is discovered during implementation.

## 12. Implementation Slices

Recommended sequence:

1. Add `ControlConfig` defaults and validation without changing public behavior.
2. Add ControllerV2 runtime wrapper for single-node voter startup.
3. Wire `Node.Start` to create the default control runtime when no test override exists.
4. Add control RPC codecs and in-process transport tests.
5. Add Controller Raft transport over `clusterv2/net`.
6. Add mirror sync runtime.
7. Add three-node in-process integration coverage.

Each slice should keep `Node` as the lifecycle composition root only. If a slice requires complex logic, move it into `pkg/clusterv2/control`, `net`, or another focused subpackage instead of expanding `node.go`.

## 13. Open Questions

- Should the first public `ControlConfig` expose mirror mode now, or keep mirror mode internal until multi-node tests need it?
- Should the single-node default require an explicit `ClusterID`, or may tests derive a deterministic temporary cluster ID?
- Should Controller Raft messages use typed RPC services or one-way transport messages for the first integration?
- Should planner bootstrap happen inside `control.Runtime.Start`, or should `clusterv2.Node` call an explicit `BootstrapIfNeeded` hook after transport is ready?
