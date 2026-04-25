# internal/runtime/channelmeta Flow

## Responsibility

`internal/runtime/channelmeta` owns node-local contracts, DTOs, activation cache primitives, authoritative metadata bootstrap/lease renewal, liveness cache, local state watcher primitives, and the channel runtime metadata resolver. The resolver keeps authoritative slot metadata aligned with routing metadata and node-local channel runtime state while app remains responsible for composition.

## Cluster-First Semantics

All channel runtime metadata decisions follow cluster semantics. A deployment with one node is still a single-node cluster, so bootstrap, resolver, liveness, and repair contracts must not introduce a separate non-cluster business branch.

## Dependency Rules

This package must not import these application or adapter layers:

- `internal/access/*`
- `internal/gateway/*`
- `internal/usecase/*`
- `internal/app`

Runtime-owned DTOs stay neutral. Adapter-specific RPC DTO conversion belongs at the adapter edge, currently `internal/access/node` or `internal/app` while migration is in progress.

## Temporary Migration State

Channelmeta resolver orchestration now lives in this package. `internal/app` keeps a thin compatibility wrapper for wiring, lifecycle, and the concrete channel leader repair implementation until Task 14 moves repair behavior behind runtime-owned ports.
