# ClusterV2 Controller Probe Readiness Design

## Goal

Make `pkg/clusterv2` unit and integration tests deterministically know when startup is complete enough to submit data. The readiness signal must work for both a single-node cluster and a multi-node distributed cluster without adding bypass semantics for single-node deployments.

The design adds a non-mutating ControllerV2 proposal probe. Tests can use it as a distributed startup barrier: if the ControllerV2 leader can commit and apply an empty proposal, the control plane is ready to accept real Controller proposals. `pkg/clusterv2` then composes that signal with local runtime gates such as snapshots, routes, slots, and channels.

## Scope

In scope:

- Add a ControllerV2 Raft probe that verifies proposal commit/apply without mutating `cluster-state.json` logical state.
- Expose the probe through `pkg/clusterv2/control.Runtime` so clusterv2 tests do not import ControllerV2 Raft internals.
- Add clear test helpers for local node readiness and multi-node cluster readiness.
- Keep `Node.Start` readable and focused on starting one node.
- Add unit tests for the probe and clusterv2 readiness helper behavior.

Out of scope:

- Adding a persistent `command.KindProbe` business command.
- Making `Node.Start` wait for all peers in the distributed cluster.
- Proving Slot data-plane leaders or ChannelV2 quorum replication are ready unless a test explicitly asks for those gates.
- New configuration fields.
- Production operator health APIs.

## Current Problem

`pkg/clusterv2.Node.Start` currently waits for a valid initial control snapshot, installs routes, reconciles local slots, starts watch/tick loops, and marks the node started. In multi-node tests, each node can return from `Start` while Controller leader election, Raft proposal readiness, or peer convergence is still in progress.

Existing tests compensate with polling on snapshots or route status. That proves some local state is visible, but it does not explicitly prove the distributed ControllerV2 write path can commit a proposal. Tests that submit data immediately after startup can therefore be timing-sensitive.

## Recommended Approach

Add a probe at `pkg/controllerv2/raft.Service`:

```go
// ProbePropose appends an empty Raft entry and waits until it is applied.
// It verifies local leader proposal, quorum commit, WAL persistence, and apply scheduling
// without mutating ControllerV2 cluster state.
func (s *Service) ProbePropose(ctx context.Context) error
```

The implementation should call `rawNode.Propose(nil)` from the Raft run loop. The existing apply scheduler already treats empty normal entries as apply-boundary updates only:

- no command decode;
- no FSM mutation;
- no logical `Revision` increment;
- no `cluster-state.json` business state change;
- applied metadata advances after the empty entry is applied.

This keeps probe semantics inside Raft where they belong. It avoids adding a test command to `pkg/controllerv2/command` and avoids teaching the FSM about a non-business operation.

The probe still writes an empty Raft log entry to the ControllerV2 WAL. That is acceptable and intentional: a readiness probe that proves proposals can commit must exercise the append/replicate/commit/apply path. If a future use case needs a read-only quorum check, that should be a separate ReadIndex-style API and should not replace this write-path probe.

## Public Package Boundaries

### `pkg/controllerv2/raft`

Add a new `ProbePropose` method on `Service`. It should reuse the existing proposal channel and proposal tracker pattern with a small internal distinction between command proposals and empty probe proposals.

Suggested internal request shape:

```go
type proposalRequest struct {
    ctx   context.Context
    cmd   command.Command
    probe bool
    resp  chan error
}
```

In the run loop:

```go
var data []byte
if !req.probe {
    encoded, err := command.Encode(req.cmd)
    if err != nil {
        req.resp <- err
        continue
    }
    data = encoded
}
if err := rawNode.Propose(data); err != nil {
    req.resp <- err
    continue
}
tracker.enqueue(trackedProposal{resp: req.resp})
```

A nil or empty `data` value produces the empty Raft entry. The apply scheduler will complete the tracked proposal when that entry reaches the applied boundary.

### `pkg/clusterv2/control`

Add a narrow optional capability interface:

```go
// ProposeProbe verifies whether the local control runtime can commit a Controller proposal.
type ProposeProbe interface {
    // ProbePropose commits a non-mutating Controller proposal probe.
    ProbePropose(context.Context) error
}
```

Implement it on `control.Runtime` by delegating to the hosted ControllerV2 Raft service:

```go
func (r *Runtime) ProbePropose(ctx context.Context) error {
    if err := ctxErr(ctx); err != nil {
        return err
    }
    if r == nil || r.raft == nil {
        return cv2raft.ErrNotStarted
    }
    return r.raft.ProbePropose(ctx)
}
```

Injected `control.Controller` implementations do not need to implement the interface. Test helpers can detect support and skip the proposal probe for static controllers unless the caller requires it.

### `pkg/clusterv2`

Keep `Node.Start` a per-node lifecycle method. Do not make it responsible for global distributed readiness.

Add package-level readiness helpers, preferably in a test-focused helper package such as `pkg/clusterv2/testkit` if we do not want to expand the production API surface:

```go
func WaitNodeReady(ctx context.Context, node *clusterv2.Node) error
func WaitClusterReady(ctx context.Context, nodes ...*clusterv2.Node) error
```

If same-package tests need access to unexported control fields, the helpers can first live in `pkg/clusterv2` test files. If external tests need them, expose a small production method on `Node` instead:

```go
// ProbeControlPropose verifies the local ControllerV2 proposal path when supported.
func (n *Node) ProbeControlPropose(ctx context.Context) error
```

The recommended production surface is to add `ProbeControlPropose` only if external tests or app-level smoke harnesses need it. Otherwise keep probe aggregation in test helpers.

## Readiness Semantics

### Local node readiness

A node is locally ready when:

- `Start` has returned successfully.
- `Snapshot().StateRevision > 0` when a controller is configured.
- `Snapshot().RoutesReady == true`.
- `Snapshot().SlotsReady == true`.
- `Snapshot().ChannelsReady == true` when ChannelV2 is expected.
- The node is not stopping.

This means the node has consumed a valid control snapshot and initialized local foreground runtime gates. It does not prove that every peer is ready.

### Distributed cluster readiness

A test cluster is ready to submit Controller-backed data when:

1. Every node satisfies local node readiness.
2. Every node observes a valid control snapshot revision and compatible routing table shape.
3. At least one Controller voter successfully completes `ProbePropose(ctx)`.
4. Non-leader Controller voters may return `controllerv2/raft.ErrNotLeader`; the helper should continue trying until a leader succeeds or the context expires.

The successful probe proves:

- a Controller leader exists;
- the local probe caller is that leader;
- a quorum can commit a new Raft entry;
- the leader persists the entry to WAL;
- the apply scheduler advances through the entry;
- the proposal tracker can notify the caller.

### Slot and Channel write readiness

Controller readiness is not the same as Slot or Channel data-plane readiness. Tests that will immediately call `Node.Propose`, `AppendChannel`, or `AppendChannelBatch` should request extra gates as needed.

For Slot metadata proposals, a stronger helper can also require route leaders:

```go
func WaitClusterReadyForSlotPropose(ctx context.Context, nodes ...*clusterv2.Node) error
```

This helper should confirm each physical Slot in the route table has a non-zero leader in the router before allowing `Node.Propose` tests to proceed.

For ChannelV2 quorum append tests, readiness should remain scenario-specific because a channel append may depend on static channel meta, ensured channel meta, channel leader forwarding, and replica catch-up. Those tests should keep explicit channel-level waits.

## Error Handling

`ProbePropose` should preserve existing `raft.Service` semantics:

- `ErrNotStarted`: service has not started.
- `ErrStopped`: service is stopping or stopped.
- `ErrNotLeader`: the local Controller voter is not the current leader.
- `context.Canceled` or `context.DeadlineExceeded`: caller deadline elapsed before commit/apply completed.
- durable WAL or apply scheduler errors: return the concrete error and mark service status degraded.

The clusterv2 cluster helper should return a descriptive aggregate error on timeout. The error should include each node's latest readiness snapshot and, when available, Controller role/leader information. This makes failed tests explain what gate did not converge instead of only reporting `condition not met before deadline`.

## Test Plan

Add ControllerV2 Raft tests:

- Single-node `ProbePropose` succeeds after startup.
- `ProbePropose` does not increment ControllerV2 logical `Revision` and does not mutate business state.
- Follower `ProbePropose` in a three-voter cluster returns `ErrNotLeader`.
- In a three-voter cluster, repeatedly probing all voters eventually finds one leader that succeeds.
- `ProbePropose` returns `ErrNotStarted` before `Start` and `ErrStopped` after `Stop`.

Add clusterv2 tests:

- `WaitNodeReady` succeeds for a started single-node cluster.
- `WaitClusterReady` succeeds for three default ControllerV2 voters without manual sleeps.
- `WaitClusterReady` returns a useful error when no node can complete the Controller probe before context timeout.
- Existing three-node clusterv2 tests use the helper before submitting data where startup timing matters.

## Documentation Updates

Update `pkg/controllerv2/FLOW.md`:

- Document that empty Controller Raft entries are used as non-mutating proposal probes.
- Clarify that they advance Raft applied metadata but do not change logical cluster state.

Update `pkg/clusterv2/FLOW.md`:

- Clarify that `Node.Start` is local-node readiness only.
- Document the recommended test readiness flow: all nodes local-ready, then ControllerV2 probe succeeds, then optional Slot/Channel gates.

## Non-Goals And Constraints

- Do not introduce any branch that treats single-node deployment as non-cluster mode.
- Do not store probe data in `cluster-state.json`.
- Do not add a probe command kind unless empty Raft entries become impossible to track correctly.
- Do not hide readiness waits inside foreground write APIs; tests should call explicit waits.
- Keep the code readable: one small Raft method, one narrow control capability, and simple polling helpers with clear gate names.
