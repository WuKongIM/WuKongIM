# pkg/clusterv2 Flow

## Responsibility

`pkg/clusterv2` is a parallel cluster runtime composition root. It wires ControllerV2 state, Slot Multi-Raft metadata storage, typed node RPC, and ChannelV2 log replication behind a small public API.

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

## Hot Paths

```text
Propose -> routing snapshot -> local Slot runtime or forward RPC -> Slot FSM
AppendChannel -> ChannelV2 service -> local reactor -> clusterv2 channel RPC -> follower reactor
```

Foreground hot paths must not synchronously call Controller APIs.

## Non-Goals For V1

- Do not replace or modify `pkg/cluster`.
- Do not add compatibility with old cluster data or old cluster config.
- Do not add hash-slot migration, onboarding, drain, scale-in, or full operator APIs.
- Do not add bypass branches that treat single-node deployment as anything other than a single-node cluster.
