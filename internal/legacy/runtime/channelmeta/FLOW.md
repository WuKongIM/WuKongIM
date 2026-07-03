# internal/runtime/channelmeta Flow

## Responsibility

`internal/runtime/channelmeta` owns node-local contracts, neutral DTOs, activation cache primitives, authoritative metadata bootstrap/lease renewal, liveness cache, local state watcher primitives, the channel runtime metadata resolver, and authoritative channel leader repair/transfer/evaluation behavior. The resolver keeps authoritative slot metadata aligned with routing metadata and node-local channel runtime state while app remains responsible for composition. `internal/runtime/channelplane` consumes the resolver as the durable-send routing authority; message usecase does not refresh or redirect directly.

## Cluster-First Semantics

All channel runtime metadata decisions follow cluster semantics. A deployment with one node is still a single-node cluster, so bootstrap, resolver, liveness, and repair contracts must not introduce a separate non-cluster business branch.

## Dependency Rules

This package must not import these application or adapter layers:

- `internal/access/*`
- `internal/usecase/*`
- `internal/app`
- `pkg/gateway/*`

Runtime-owned DTOs stay neutral. Adapter-specific RPC DTO conversion belongs at the adapter edge, currently `internal/access/node`. `internal/app` only wires runtime ports, lifecycle, and dependency composition.

## Repair Ownership

`LeaderRepairer` owns authoritative channel leader repair and explicit safe-transfer decisions behind neutral ports for the metadata store, slot leadership lookup, remote repair/transfer/evaluate client, and local evaluator. `LeaderPromotionEvaluator` owns local durable log inspection and peer probe collection for promotion safety. Node RPC handlers in `internal/access/node` remain transport adapters: they decode access DTOs, convert them to runtime DTOs, call the injected runtime interfaces, and encode RPC responses.

## Runtime Flow

`internal/app` builds `ChannelMetaSync`, the resolver, and the repair/evaluate ports, then starts this package as the `channelmeta` lifecycle component after `managed_slots_ready` and before `presence`. `Start` only runs lightweight active-slot leader watchers and refresh workers for hot channels. `StopWithoutCleanup` cancels and waits for those background tasks, but it does not delete channel runtime state; cluster and channel runtime resource shutdown owns that cleanup later in the app stop sequence.

Business refresh paths may reuse a short-lived positive activation cache only when the cached routing view is active, has non-zero channel and leader epochs, has a leader with a lease beyond the refresh lead time, has no active channel write fence, is not known dead or draining in liveness cache, and retains the original authoritative `ChannelRuntimeMeta` for repair-policy validation. The activation cache is sharded, TTL-pruned, and capacity-bounded so hot-channel lookups do not share one global lock or grow without bound. Cache misses and unhealthy entries still read authoritative `ChannelRuntimeMeta`, bootstrap missing metadata from the slot topology when needed, renew a valid channel leader lease from the current channel leader, and ask the current slot leader to repair missing, dead, draining, non-replica, or expired leaders. Applying authoritative metadata invalidates any cached routing view when the authoritative write-fence token/version/reason/deadline or `RouteGeneration` changes, so migrations cannot leave business paths on an unfenced cached view or stale RPC epoch. Expired leader lease repair must evaluate the current leader before renewing it; if that leader cannot prove it is still safe, repair promotes another safe ISR candidate instead of blindly extending a stale lease. Slot metadata writes are monotonic, so stale channel/leader epochs, shorter same-epoch leases, lower `RetentionThroughSeq` values, and older write-fence generations cannot overwrite a newer authoritative record. After authoritative metadata is read or repaired, the resolver applies the full projected metadata, including `RouteGeneration`, `RetentionThroughSeq`, and `WriteFence`, to the local ISR runtime before returning the routing view to callers. Channelplane append paths invalidate this cache and force one fresh refresh before retrying exactly once on stale metadata, not-leader, lease-expired, or reroute append errors.

## Migration Interaction

Channel replica migration remains slot-authoritative. This package reads, repairs, and applies `ChannelRuntimeMeta`, but it does not decide migration phases, infer cutover readiness from local channel status, or clear write fences locally. The slot task and Slot Raft commands are the source of truth for task phase, owner lease, fence token/version, drain proof, learner promotion, and final clear/reset.

During replica replace, `learner = Replicas - ISR`. The resolver may apply learners in `Replicas` so channel runtime can receive replication and catch-up probes, but leader repair and promotion evaluation only consider `ISR` candidates until the slot layer promotes the learner into `ISR`. A learner must therefore not become a repaired leader or affect write availability while it is outside `ISR`.

Write fences fail closed across the metadata cache. Cache reuse requires no active fence, and any authoritative fence token/version/reason/deadline change invalidates cached routing. Fence TTL expiry is only a recovery signal for the slot executor; business refresh paths must wait for an authoritative higher `WriteFenceVersion` reset/clear before admitting writes again.

Leader repair can run while a migration task exists, but it must preserve authoritative fences and monotonic epochs. If repair changes `LeaderEpoch`, leader, or lease state, the migration executor must reread the task and runtime meta before continuing; stale same-leader drain proofs are rejected later by the slot proof guards.

## Tests

Use the package test suite for resolver, bootstrap, watcher, liveness, and repair behavior:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -count=1
GOWORK=off go test ./internal/runtime/channelmeta -run 'TestChannelMeta.*Repair|TestChannelLeader.*Repair|TestChannelLeader.*Evaluate|TestChannelLeaderPromotionEvaluator' -count=1
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```
