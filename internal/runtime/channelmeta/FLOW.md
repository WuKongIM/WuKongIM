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

## Runtime Flow

`internal/app` builds `ChannelMetaSync`, the resolver, and the repair/evaluate ports, then starts this package as the `channelmeta` lifecycle component after `managed_slots_ready` and before `presence`. `Start` only runs lightweight active-slot leader watchers and refresh workers for hot channels. `StopWithoutCleanup` cancels and waits for those background tasks, but it does not delete channel runtime state; cluster and channel runtime resource shutdown owns that cleanup later in the app stop sequence.

Refresh and append-retry paths read authoritative `ChannelRuntimeMeta`, bootstrap missing metadata from the slot topology when needed, renew a valid channel leader lease from the current channel leader, and ask the current slot leader to repair missing, dead, draining, non-replica, or expired leaders. After authoritative metadata is read or repaired, the resolver applies it to the local ISR runtime and returns the applied routing view to callers.

## Tests

Use the package test suite for resolver, bootstrap, watcher, liveness, and repair behavior:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -count=1
GOWORK=off go test ./internal/runtime/channelmeta -run 'TestChannelMeta.*Repair|TestChannelLeader.*Repair|TestChannelLeader.*Evaluate|TestChannelLeaderPromotionEvaluator' -count=1
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```
