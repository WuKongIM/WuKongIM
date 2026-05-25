# pkg/clusterv2 Flow

## Responsibility

`pkg/clusterv2` is a parallel cluster runtime composition root. It wires ControllerV2 state, Slot Multi-Raft metadata storage, typed node RPC, and ChannelV2 log replication behind a small public API.

The root `Node` stays thin: it owns lifecycle, readiness, public API delegation, and snapshot fan-out only. Foreground routing, Slot propose, ChannelV2 replication, Controller state mapping, and node RPC each live in focused subpackages.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `control` | Controller abstraction and ControllerV2 snapshot adapter. |
| `routing` | Atomic HashSlot -> Slot -> Leader read model for hot paths. |
| `net` | Typed node-to-node RPC and discovery glue; Go package name is `clusternet`. |
| `slots` | Slot Multi-Raft runtime open/bootstrap/reconcile/status. |
| `propose` | Slot metadata propose path and leader forwarding. |
| `channels` | ChannelV2 service construction, metadata resolver, and replication transport. |
| `observe` | Low-frequency background loops and readiness snapshots. |

## Start Flow

```text
New(Config)
  -> validate v2-only config
  -> create Router and Discovery
  -> apply optional WithProposer / WithChannels overrides

Start(ctx)
  -> initialize default proposer / ChannelV2 service when no override was provided
  -> start injected lifecycle resources
  -> start Controller adapter when configured
  -> load initial control snapshot
  -> routing.UpdateControlSnapshot(snapshot)
  -> discovery.Update(snapshot.Nodes)
  -> slots.Reconcile(snapshot)
  -> start Controller watch loop for later snapshots
  -> mark node started
```

`Start` requires cluster semantics even for one node. A single-node deployment should provide a valid single-node control snapshot instead of using a bypass path.

## Stop Flow

```text
Stop(ctx)
  -> mark stopping and reject new foreground calls
  -> stop Controller watch loop
  -> close hosted ChannelV2 service
  -> stop Controller adapter
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
Node.AppendChannel / AppendChannelBatch / FetchChannel
  -> channels.Service
  -> channelv2 service facade
  -> local reactor and store worker pools
  -> clusterv2 channel RPC client
  -> remote channel RPC handler
  -> follower reactor Pull / Apply / Ack
```

`WithProposer` and `WithChannels` are public override options for tests, smoke harnesses, and app-level composition. If callers do not provide them, `Node.Start` creates default proposer and ChannelV2 service instances and owns the ChannelV2 tick loop.

`channels.Service` keeps a combined runtime interface because the public ChannelV2 `Cluster` surface and replication `transport.Server` surface are separate. `StaticMetaSource` is available for tests and smoke runs. `SlotMetaSource` adapts authoritative Slot-backed `ChannelRuntimeMeta` records into ChannelV2 metadata for production wiring.

## Non-Goals For V1

- Do not replace or modify `pkg/cluster`.
- Do not add compatibility with old cluster data or old cluster config.
- Do not add hash-slot migration, onboarding, drain, scale-in, or full operator APIs.
- Do not add bypass branches that treat single-node deployment as anything other than a single-node cluster.

## V1 Limitations

- ControllerV2 integration currently focuses on snapshot mapping and an adapter shell; full Raft bootstrap is left to the production wiring phase.
- Slot smoke coverage uses an in-process fake Slot runtime for route/forward validation; destructive Slot cleanup remains disabled.
- ChannelV2 append forwarding is not automatic. Calls to a non-channel-leader return ChannelV2 typed errors.
- Channel RPC codecs use a version byte plus JSON payload as a temporary v1 format; replace with binary codecs before optimizing this data path.
- Observe loops are intentionally small and low-frequency; foreground write paths only read atomic route/channel state.
