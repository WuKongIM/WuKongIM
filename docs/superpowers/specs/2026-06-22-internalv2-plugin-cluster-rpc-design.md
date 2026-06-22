# Internalv2 Plugin Cluster RPC Design

## Goal

Migrate the PDK-compatible plugin-origin host RPCs `/cluster/config` and
`/cluster/channels/belongNode` into `internalv2`.

## Scope

- Add `/cluster/config` and `/cluster/channels/belongNode` to
  `internalv2/access/plugin`.
- Add `ClusterConfig` and `ClusterChannelsBelongNode` orchestration to
  `internalv2/usecase/plugin`.
- Add narrow cluster read adapters in `internalv2/infra/cluster`.
- Wire the v2 app plugin subsystem to these adapters when the clusterv2 runtime
  exposes the required surfaces.
- Cover usecase, access, app wiring, infra adapter behavior, and benchmarks.

Out of scope:

- `/conversation/channels`, `/plugin/httpForward`, and stream host RPCs.
- Guessing API server addresses that are not present in the v2 control
  snapshot.
- Direct plugin usecase dependency on clusterv2 or management read models.

## Compatibility Rules

- Existing `internal/usecase/plugin/pluginproto` messages remain the wire
  contract.
- `/cluster/config` maps an authoritative cluster snapshot to
  `pluginproto.ClusterConfig`.
- Nodes are sorted by ID. `ClusterAddr` maps from control snapshot node address.
  `ApiServerAddr` remains empty unless a future adapter provides a real source.
- `Online` is true only when the control snapshot status is `alive`.
- Slots are sorted by physical Slot ID. The plugin-compatible `Leader` is the
  preferred Slot leader from the control assignment and `Term` is the config
  epoch saturated to `uint32`.
- Replica lists are cloned from desired Slot peers.
- `/cluster/channels/belongNode` rejects empty requests and blank channel IDs.
- Channel ownership is authoritative: the adapter returns the ChannelV2 append
  authority leader. A zero owner is an error rather than a local fallback guess.
- Response groups are sorted by owner node ID while preserving request order
  within each owner group.

## Architecture

```text
plugin process
  -> wkrpc /cluster/config
  -> internalv2/access/plugin.Server.handleClusterConfig
  -> internalv2/usecase/plugin.App.ClusterConfig
  -> ClusterReader.ClusterSnapshot
  -> infra/cluster.PluginClusterReader
  -> clusterv2 LocalControlSnapshot

plugin process
  -> wkrpc /cluster/channels/belongNode
  -> internalv2/access/plugin.Server.handleClusterChannelsBelongNode
  -> internalv2/usecase/plugin.App.ClusterChannelsBelongNode
  -> ChannelOwnerReader.ChannelOwnerNode
  -> infra/cluster.PluginChannelOwnerReader
  -> clusterv2 ResolveChannelAppendAuthority
```

`internalv2/access/plugin` stays a protocol adapter: route registration, proto
decode/encode, body limits, timeout context, and caller UID propagation.

`internalv2/usecase/plugin` owns legacy PDK grouping and error semantics through
two narrow ports. It does not import clusterv2.

`internalv2/app` wires the ports only when the cluster supports the required
interfaces. Missing ports are still reported by the plugin usecase if the host
RPC is called.

## Performance Notes

`/cluster/config` performs one snapshot read and deterministic slice sorting.
`/cluster/channels/belongNode` performs one authority lookup per requested
channel and allocates only the owner grouping map plus response slices. This is
acceptable for plugin-side routing queries; benchmarks should pin the mapping
overhead for 1, 16, and 128 channel requests.

## Test Plan

- `internalv2/usecase/plugin`:
  - cluster config maps and sorts nodes/slots deterministically
  - missing cluster reader returns `ErrClusterReaderRequired`
  - belong-node groups by owner with stable owner ordering
  - missing owner reader and zero owner return explicit errors
  - empty request and blank channel IDs return `ErrChannelRequired`
- `internalv2/access/plugin`:
  - route registration includes both cluster paths
  - handlers apply timeout, decode where needed, pass caller UID, and write proto responses
  - shorter incoming deadlines are preserved
- `internalv2/infra/cluster`:
  - control snapshot maps to plugin cluster snapshot
  - channel owner reader maps ChannelV2 metadata leader to owner node
- `internalv2/app`:
  - plugin subsystem wires cluster reader and channel owner reader from clusterv2
- Benchmarks:
  - `BenchmarkClusterConfigFromSnapshot`
  - `BenchmarkClusterChannelsBelongNode`
  - `BenchmarkClusterHostRPCHandlers`

## Self Review

- Scope is limited to two read-only host RPCs.
- No API address inference is introduced.
- The usecase boundary remains `access -> usecase -> port`, with `app` owning
  infra wiring.
