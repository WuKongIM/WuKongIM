# internal/runtime/channelmeta Flow

## Responsibility

`internal/runtime/channelmeta` owns node-local contracts and DTOs for channel runtime metadata resolver, bootstrap, liveness, and leader repair flows. The package is the migration target for channel runtime meta coordination that keeps authoritative slot metadata aligned with node-local channel runtime state.

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

Channelmeta behavior still lives in `internal/app/channelmeta*.go` until Tasks 10-14 move resolver, cache, bootstrap, liveness, repair, and lifecycle code into this package. During this shell task, only neutral contracts, DTOs, and adapter conversions are introduced.
