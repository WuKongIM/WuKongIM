# ChannelV2 Replica Placement Decoupling Design

## Context

ChannelV2 currently derives initial channel replicas from `routing.Route.Peers`.
Those peers are the physical Slot metadata replica set from
`control.SlotAssignment.DesiredPeers`. This couples two different ownership
domains:

- Slot peers own and replicate channel runtime metadata.
- Channel replicas own and replicate the channel log data plane.

That shortcut works for the current three-node local benchmark, but it blocks
topologies where Slot metadata and ChannelV2 data have different replica sets,
different replica counts, or different placement goals.

## Goal

Decouple initial ChannelV2 data placement from Slot metadata placement.

After this change:

- Slot routing still answers `channelID -> hashSlot -> physical Slot -> Slot
  leader/peers` for metadata reads and proposals.
- Channel placement answers `channelID -> ChannelV2 leader/replicas/minISR`.
- `routing.Route.Peers` remains Slot metadata peers and is no longer treated as
  the default ChannelV2 replica set.
- The default three-node behavior remains operationally compatible: a
  three-data-node cluster with replica count 3 still creates three ChannelV2
  replicas, but the code no longer relies on Slot peers as the source of that
  list.

## Non-Goals

This phase does not implement:

- remote Slot metadata reads for entry nodes that do not host the target Slot
  metadata shard,
- `ForwardAppend` metadata envelopes,
- PullHint `MetaRef`,
- controller-managed Channel placement pools,
- Channel replica migration,
- dynamic placement changes for existing channels.

Those are follow-up phases. This design only separates first-create ChannelV2
replica selection from Slot route peers.

## Proposed Approach

Introduce a Channel data placement resolver that has two inputs:

- Slot route lookup, used only to identify the metadata Slot and the preferred
  data leader hint already exposed by routing.
- A data-node candidate provider, sourced from the current control snapshot.

The resolver picks ChannelV2 replicas from alive data nodes instead of
`route.Peers`.

Initial algorithm:

1. Build the candidate list from control snapshot nodes where:
   - `Status == alive`
   - `Roles` contains `data`
2. Sort candidates by a stable rendezvous/hash score derived from
   `channelID + nodeID`.
3. Select `ChannelReplicaCount` candidates.
4. Choose leader from the selected replicas:
   - prefer `route.PreferredLeader` if it is selected,
   - otherwise use the first selected candidate.
5. Set `MinISR = replicaCount/2 + 1`.

Configuration:

- Add `Config.Channel.ReplicaCount`.
- If unset or zero, default to `Config.Slots.ReplicaCount`.
- If the number of alive data candidates is less than the effective replica
  count, fail the initial placement with a typed invalid-config/not-ready error.
  Do not silently reduce the replica count.

## Data Flow

First append on any node:

```text
Append / AppendBatch
  -> SlotMetaSource.EnsureChannelMeta
  -> read existing ChannelRuntimeMeta from Slot metadata
  -> if missing:
       ChannelPlacementResolver.ResolveChannelPlacement(channelID)
         -> route channelID to metadata Slot
         -> choose ChannelV2 replicas from alive data nodes
         -> choose ChannelV2 leader from selected replicas
       persist ChannelRuntimeMeta through Slot metadata proposal
  -> append path uses persisted ChannelRuntimeMeta as authority
```

The persisted `ChannelRuntimeMeta` remains the only authority for an existing
channel. Placement is used only when creating a missing channel runtime meta
row.

## Components

### Channel Placement Resolver

Replace the current Slot-peer-derived resolver behavior with a resolver that
depends on a candidate provider:

```go
type DataNodeProvider interface {
    DataNodes() []uint64
}
```

The provider should expose a snapshot copy of alive data node IDs. The resolver
must not mutate control snapshot slices.

The existing `PlacementRouter` remains useful for metadata Slot routing and
preferred leader hints:

```go
type PlacementRouter interface {
    RouteKey(string) (routing.Route, error)
}
```

### Node Wiring

`pkg/clusterv2.Node` already receives control snapshots and updates routing.
It should also maintain a small atomic/read-locked data-node view for ChannelV2
placement. This view is updated from the same control snapshot path that
updates routing and discovery.

### Route Semantics

`routing.Route.Peers` stays as Slot metadata peers. Tests and comments should
make this explicit.

`routing.Route.PreferredLeader` may be used as a data leader hint only when
that node is selected into the ChannelV2 replica set. It must not force a
non-replica leader.

## Error Handling

- No route table: return the existing route not-ready error.
- No Slot leader: return the existing no-leader error.
- No alive data candidates: return ChannelV2 invalid configuration/not-ready.
- Fewer candidates than effective replica count: return invalid
  configuration/not-ready.
- Preferred leader not selected into ChannelV2 replicas: ignore it and choose
  the highest-ranked selected replica.

The first phase intentionally fails fast when the desired replica count cannot
be met. This keeps quorum semantics explicit.

## Testing

Unit tests:

- A placement resolver with Slot peers `[1,2,3]` and data candidates
  `[4,5,6]` returns ChannelV2 replicas from `[4,5,6]`, proving it no longer
  uses Slot peers.
- Preferred leader is used only if it is part of the selected ChannelV2
  replicas.
- Insufficient data candidates fail placement.
- Candidate order is deterministic for a channel ID.
- Existing `SlotMetaSource` creation persists the placement returned by the
  resolver.

Focused Go tests:

```bash
GOWORK=off go test -count=1 ./pkg/clusterv2/routing ./pkg/clusterv2/channels ./pkg/clusterv2
```

Target validation after implementation:

```bash
GOWORK=off scripts/bench-wukongimv2-three-nodes-10kch.sh
```

Expected benchmark result for the current three-node setup:

- activation success remains `10000/10000`,
- activation errors remain `0`,
- active leader node count remains `3`,
- max leader share remains close to one third,
- PullHint receive errors do not regress.

## Follow-Up Phases

After this phase is stable:

1. Add a routed Slot metadata facade so any entry node can resolve/ensure
   ChannelRuntimeMeta even when it is not a Slot metadata peer.
2. Add ForwardAppend metadata envelopes so a ChannelV2 leader can be activated
   without assuming it can locally read Slot metadata.
3. Add PullHint MetaRef so follower wakeups default to lightweight metadata
   references and only request inline metadata when needed.

These phases depend on the semantic split introduced here.
