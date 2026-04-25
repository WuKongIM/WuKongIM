# internal/runtime/channelmeta Flow

## Responsibility

`internal/runtime/channelmeta` owns node-local contracts, neutral DTOs, activation cache primitives, authoritative metadata bootstrap/lease renewal, liveness cache, local state watcher primitives, the channel runtime metadata resolver, and authoritative channel leader repair/evaluation behavior. The resolver keeps authoritative slot metadata aligned with routing metadata and node-local channel runtime state while app remains responsible for composition.

## Cluster-First Semantics

All channel runtime metadata decisions follow cluster semantics. A deployment with one node is still a single-node cluster, so bootstrap, resolver, liveness, and repair contracts must not introduce a separate non-cluster business branch.

## Dependency Rules

This package must not import these application or adapter layers:

- `internal/access/*`
- `internal/gateway/*`
- `internal/usecase/*`
- `internal/app`

Runtime-owned DTOs stay neutral. Adapter-specific RPC DTO conversion belongs at the adapter edge, currently `internal/access/node`. `internal/app` only wires runtime ports, lifecycle, and dependency composition.

## Repair Ownership

`LeaderRepairer` owns authoritative channel leader repair decisions behind neutral ports for the metadata store, slot leadership lookup, remote repair/evaluate client, and local evaluator. `LeaderPromotionEvaluator` owns local durable log inspection and peer probe collection for promotion safety. Node RPC handlers in `internal/access/node` remain transport adapters: they decode access DTOs, convert them to runtime DTOs, call the injected runtime interfaces, and encode RPC responses.
