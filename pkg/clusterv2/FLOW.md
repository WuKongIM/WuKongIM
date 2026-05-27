# pkg/clusterv2 Flow

## Responsibility

`pkg/clusterv2` is a parallel cluster runtime composition root. It wires ControllerV2 state, Slot Multi-Raft metadata storage, typed node RPC, and ChannelV2 log replication behind a small public API.

The root `Node` stays thin: it owns lifecycle, readiness, public API delegation, and snapshot fan-out only. Foreground routing, Slot propose, ChannelV2 replication, Controller state mapping, and node RPC each live in focused subpackages.

## Source Reading Path

- Start with `api.go`, `config.go`, and `node.go` for the public surface.
- Read `node_lifecycle.go`, `node_defaults.go`, `node_snapshot.go`, and `node_loops.go` for root runtime wiring details.
- Read `default_slots.go`, `default_slot_leaders.go`, and `default_slot_proposer.go` for the default single-node cluster Slot path.
- Root `Node` tests follow the same split: lifecycle, defaults, snapshot, channel, and shared helpers.
- In `channels`, read `service.go` first, then `meta.go`, `slot_meta.go`, `placement.go`, and `transport.go`.
- In `control`, `snapshot.go` is the read model, while `snapshot_validate.go` and `snapshot_clone.go` hold model mechanics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `control` | Controller abstraction, root ControllerV2 facade adapter, snapshot adapter, Raft RPC, and state sync RPC. |
| `routing` | Atomic HashSlot -> Slot -> Leader read model for hot paths. |
| `net` | Typed node-to-node RPC and discovery glue; Go package name is `clusternet`. |
| `slots` | Slot Multi-Raft runtime open/bootstrap/reconcile/status. |
| `propose` | Slot metadata propose path and leader forwarding. |
| `channels` | ChannelV2 service construction, metadata resolve/ensure, append leader forwarding, and replication transport. |
| `observe` | Low-frequency background loops and readiness snapshots. |

## Start Flow

```text
New(Config)
  -> validate v2-only config
  -> create Router and Discovery
  -> apply optional WithProposer / WithChannels overrides

Start(ctx)
  -> initialize default node RPC transport / ControllerV2 runtime / proposer / ChannelV2 service when no override was provided
  -> initialize a real single-node-cluster Slot Multi-Raft runtime for default propose when the configured Slot replica count is 1
  -> seed node RPC discovery from configured Controller voters until the first control snapshot arrives
  -> start default transport and injected lifecycle resources
  -> start ControllerV2-backed Controller or injected Controller
  -> wait for a valid initial control snapshot
  -> routing.UpdateControlSnapshot(snapshot)
  -> discovery.Update(control node addresses)
  -> slots.Reconcile(snapshot)
  -> start Controller watch loop for later snapshots
  -> start the default Slot leader observation loop when the default Slot runtime is active
  -> mark ChannelV2 ready and start the tick loop
  -> mark node started
```

`Start` requires cluster semantics even for one node. A single-node cluster uses a ControllerV2-backed single-voter control runtime instead of a bypass path. Multi-voter default startup uses `pkg/transport` one-way service messages for ControllerV2 Raft traffic and RPC responses only for state-sync requests.

`Node.Start` only establishes local-node readiness: the node has a valid local control snapshot, installed routes, reconciled local Slot runtime state, and started local ChannelV2 resources. Package tests use `WaitClusterReady` for converged local control snapshots, and tests that specifically require distributed Controller write readiness should add the separate Controller proposal probe gate. Slot and Channel write tests should add their own Slot leader or Channel metadata gates when those paths are part of the assertion.

ControllerV2 changes enter clusterv2 as strongly typed `controllerv2.ClusterState` events. `pkg/clusterv2/control` maps those events to `control.Snapshot`; `Node` then compares node, Slot, task, and hash-slot domains before touching discovery, Slot runtime reconciliation, or foreground routing.

## Stop Flow

```text
Stop(ctx)
  -> mark stopping and reject new foreground calls
  -> stop Controller watch loop
  -> stop ChannelV2 tick loop
  -> close hosted ChannelV2 service
  -> stop ControllerV2-backed Controller or injected Controller
  -> stop injected lifecycle resources in reverse order
```

## Propose Hot Path

```text
Node.Propose
  -> propose.Service
  -> routing.Router atomic table lookup
  -> encode [version:1][hashSlot:2][command]
  -> if local leader: SlotRuntime.Propose
  -> else: clusternet RPCSlotForwardPropose
  -> remote ForwardHandler re-checks local Slot leadership
  -> SlotRuntime.Propose
```

The propose path returns typed not-ready/no-leader/not-leader errors and does not synchronously call Controller APIs.

## ChannelV2 Flow

```text
Node.AppendChannel / AppendChannelBatch
  -> channels.Service
  -> Append: EnsureChannelMeta from append-only ChannelMetaEnsurer when available
      -> SlotMetaSource reads authoritative ChannelRuntimeMeta from Slot metadata storage
      -> if missing: derive initial replicas/leader from Slot placement and persist through RuntimeMetaWriter
      -> reread final authoritative ChannelRuntimeMeta and project it to ChannelV2 Meta
      -> if local node is channel leader: ApplyMeta to local ChannelV2 runtime, then Append locally
      -> else: RPCChannelAppend / RPCChannelAppendBatch forward to the resolved channel leader
  -> local reactor and store worker pools
  -> clusterv2 channel RPC client
  -> remote channel RPC handler
  -> follower reactor Pull / Apply / Ack
```

`WithProposer` and `WithChannels` are public override options for tests, smoke harnesses, and app-level composition. If callers do not provide them, `Node.Start` creates a default ControllerV2 runtime, proposer, and ChannelV2 service, backs ChannelV2 with the message DB under `DataDir/channellog`, registers ChannelV2 replication/append-forward handlers on the default node RPC transport, and owns the ChannelV2 tick loop plus default store factory cleanup. For single-node clusters with `ReplicaCount=1`, the default proposer is backed by a real local Slot Multi-Raft runtime, durable Slot Raft log storage under `DataDir/slotraft`, and metadata FSM storage under `DataDir/slotmeta`; multi-replica Slot runtime auto-wiring remains outside this default path.

`channels.Service` keeps a combined runtime interface because the public ChannelV2 `Cluster` surface and replication `transport.Server` surface are separate. `StaticMetaSource` is available for tests and smoke runs. `SlotMetaSource` adapts authoritative `pkg/db/meta` `ChannelRuntimeMeta` records into ChannelV2 metadata for production wiring. `ResolveChannelMeta` remains read-only; `EnsureChannelMeta` is the append-only path that may create the initial ChannelRuntimeMeta through the Slot-owned metadata writer before any ChannelV2 append is attempted.

## Non-Goals For V1

- Do not replace or modify `pkg/cluster`.
- Do not add compatibility with old cluster data or old cluster config.
- Do not add hash-slot migration, onboarding, drain, scale-in, or full operator APIs.
- Do not add bypass branches that treat a single-node cluster as anything other than a cluster.

## V1 Limitations

- ControllerV2 integration supports ControllerV2-backed runtime startup, single-node cluster bootstrap, mirror sync, and multi-voter Raft transport wiring through `pkg/transport`. Production app config wiring and operator workflows remain outside this package-level slice.
- Slot coverage now uses the real default single-node-cluster Slot runtime for default propose. Multi-replica Slot auto-wiring is still limited; older route/forward smoke coverage may use in-process Slot runtimes where distributed Slot convergence is not the assertion. Destructive Slot cleanup remains disabled.
- ChannelV2 append forwarding and first-append metadata creation require a configured Slot-backed ChannelMetaSource and Forward client; without them, pre-applied local runtime state is required and non-leader appends return ChannelV2 typed errors.
- Channel RPC codecs use a version byte plus JSON payload as a temporary v1 format; replace with binary codecs before optimizing this data path.
- Observe loops are intentionally small and low-frequency; foreground write paths only read atomic route/channel state.
