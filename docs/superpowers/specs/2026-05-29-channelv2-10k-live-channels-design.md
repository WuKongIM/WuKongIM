# ChannelV2 10k Live Channels Design

## Goal

Support a three-node ChannelV2 deployment where 10,000 channels can remain locally active without creating high idle CPU, unbounded memory growth, or metadata-storage pressure on every append.

This design covers the first phase only: keep many channels alive quietly and preserve the current per-channel single-writer reactor model. Node-pair batch replication RPCs are a follow-up project after this phase establishes a stable 10k-channel baseline.

## Context

`pkg/channelv2` already uses a multi-reactor model. Each loaded channel runtime is owned by one reactor goroutine, while store and RPC effects run through bounded worker pools. This is the right baseline for 10k live channels because it avoids one goroutine per channel and keeps per-channel state mutation serialized.

The current pressure points for 10k live channels are:

- Follower runtimes continue to schedule periodic pulls after they are caught up.
- Append admission resolves ChannelRuntimeMeta on each append through `clusterv2/channels.Service`.
- `ErrTooManyChannels` and `reactor.Limits.MaxChannels` exist but are not wired into channel activation.
- Several per-channel maps are allocated at runtime creation even when they stay empty.
- Existing high-channel evidence covers 1,000 channels, not 10,000. The 1,000-channel run already shows high append and storage p99 while ChannelV2 reactor and worker queues remain shallow.

## Success Criteria

- A three-node 10,000-channel benchmark can warm every channel and keep all expected local ChannelV2 runtimes active.
- During an idle window after warmup, ChannelV2 pull RPC rate stays near zero except for configured low-frequency recovery probes.
- Sparse traffic over 1%-5% active channels does not cause inactive channels to generate meaningful RPC or store work.
- Runtime activation is bounded by a configured per-node limit and fails closed with `channelv2.ErrTooManyChannels`.
- Hot append paths reuse authoritative metadata from a fenced cache instead of reading Slot metadata on every append.
- Existing single-node cluster semantics stay unchanged; no bypass path is introduced.

## Non-Goals

- Do not implement `PullBatch`, `PullHintBatch`, or `ProgressAckBatch` in this phase.
- Do not replace the per-channel state machine or the reactor ownership model.
- Do not acknowledge durable SEND before crash-safe message commit.
- Do not change ChannelRuntimeMeta authority; Slot metadata remains authoritative.
- Do not add high-cardinality Prometheus labels for channel IDs.

## Architecture

The phase keeps the current `access -> usecase -> clusterv2 -> channelv2` path and adds three focused capabilities:

1. Runtime capacity accounting inside ChannelV2 reactors.
2. Parked follower replication with low-frequency jittered recovery probes.
3. A fenced ChannelMeta cache in `pkg/clusterv2/channels` for append admission.

The reactor remains the only writer of each `runtimeChannel`. Follower replication state remains inside `runtimeChannel.replication`; the change is in scheduling rules, not ownership. Metadata caching lives outside ChannelV2 in the clusterv2 channel service because ChannelRuntimeMeta authority and append forwarding decisions are clusterv2 responsibilities.

## Component Design

### 10k Benchmark Baseline

Add a non-integration unit benchmark and a documented wkbench/dev-sim scenario:

- In-package benchmark: `pkg/channelv2` or `pkg/clusterv2` memory-transport benchmark that applies 10,000 three-node metas, warms one append per channel, then measures idle tick and sparse append behavior.
- External benchmark scenario template under `docs/development/perf-runs/20260529-channelv2-10k-live-channels/` for real message DB and clusterv2 transport.

The benchmark must record:

- Active runtime count by node and role.
- Pull RPC rate and empty-pull rate.
- PullHint rate and dropped PullHint count.
- Append p95/p99 by commit mode.
- ChannelV2 worker p99 by task kind.
- Store commit p99 and grouped commit batch size.
- Heap allocation and goroutine delta.

### Runtime Capacity Limit

Wire `reactor.Limits.MaxChannels` into `reactor.Config`, `service.Config`, `channelv2.Config`, and `clusterv2.ChannelConfig`.

Each reactor enforces a local partition limit derived from the group-wide node limit:

- If `MaxChannels <= 0`, preserve current unlimited behavior.
- If a new runtime would exceed the reactor's assigned budget, `ensureChannel` returns `channelv2.ErrTooManyChannels`.
- Existing loaded channels can receive metadata updates even when the node is at capacity.
- Capacity release happens only after successful runtime eviction and store handle close.

This avoids a global hot lock on every activation and keeps admission decisions local to the owning reactor.

### Follower Park and Recovery Probe

When a follower receives an empty pull response showing `LeaderLEO <= local LEO` and `hintedLeaderLEO <= local LEO`, it enters a parked state:

- `dirty=false`
- `parked=true`
- no ordinary periodic pull due is scheduled
- `nextPullAt` is set only for a low-frequency recovery probe

The recovery probe interval is configurable and jittered per channel. The default base interval is 60 seconds and the default jitter window is 30 seconds. A `0` interval disables recovery probes only in tests or explicit operator tuning; production defaults keep probes enabled.

`PullHint` remains the primary wake-up path:

- A valid newer hint clears the parked state and schedules immediate pull.
- Stale hints complete without waking the follower.
- Hints that report `LeaderLEO <= local LEO` clear `hintedLeaderLEO` and leave the follower parked.

Leader idle stop and checkpoint lifecycle remain intact. Stopped-follower ACK still uses the existing standalone ACK path.

### ChannelMeta Append Cache

Add a small cache inside `pkg/clusterv2/channels.Service`:

- Key: `channelv2.ChannelID`
- Value: full `channelv2.Meta`
- Fence fields: `Epoch`, `LeaderEpoch`, `Leader`, `Status`, `Replicas`, `ISR`, `MinISR`
- Source: successful `EnsureChannelMeta` or `ResolveChannelMeta`

Append admission uses the cache when:

- cached meta exists for the exact channel ID
- status is active or creating
- leader is non-zero
- cached meta still targets the current local node or a known remote leader

On `ErrStaleMeta`, `ErrChannelNotFound`, `ErrNotLeader`, or route-not-ready style errors, invalidate the cache entry and retry metadata resolution once. This preserves correctness while removing repeated Slot metadata reads from steady-state appends.

The cache must not invent metadata. First append for a missing channel still goes through `EnsureChannelMeta`, which may create authoritative metadata through Slot ownership.

### Per-Channel Memory Tightening

Reduce always-on per-runtime heap:

- Lazily allocate `waiters`, `pullWaiters`, `appendTimings`, and cancellation maps.
- Keep `lifecycle.followers` nil or compact for follower role.
- For leaders with at most four remote followers, represent follower lifecycle state with a compact slice keyed by node ID lookup; retain map behavior for larger replica sets.
- Keep recent record cache behavior unchanged in this phase except for lazy allocation and existing configuration limits.

These changes are implementation details behind reactor-owned state and must not change public behavior.

## Data Flow

### Warm Channel Activation

1. Append request enters `clusterv2/channels.Service`.
2. Service checks the ChannelMeta cache.
3. Cache miss calls `EnsureChannelMeta`.
4. Local leader applies metadata to ChannelV2 and activates runtime if needed.
5. Runtime activation checks the local capacity budget.
6. Append proceeds through the existing reactor append queue and store worker path.

### Caught-Up Follower

1. Follower pulls from leader.
2. Leader returns no records with `LeaderLEO <= follower LEO`.
3. Follower records parked state.
4. No ordinary pull is scheduled.
5. A future PullHint wakes the follower immediately, or a jittered recovery probe runs later.

### Sparse Write After Idle

1. Leader appends records on an active channel.
2. Leader sends PullHint to followers that need progress.
3. Parked followers wake and pull records.
4. Apply completes and follower progress reaches leader LEO.
5. Follower parks again without an immediate empty pull loop.

## Error Handling

- Capacity exhaustion returns `channelv2.ErrTooManyChannels` and does not create a store handle.
- Cache-stale append errors invalidate only the affected channel entry and retry metadata resolution once.
- PullHint failures use existing retry behavior but must not re-enable periodic empty pulls for parked followers.
- Recovery probe failures use the existing replication backoff bounds.
- Metadata changes that fence the runtime reset parked state, follower stop state, and recent record cache through existing metadata-fence behavior.

## Configuration

Expose and document the following through ChannelV2 service and clusterv2 config layers:

- `MaxChannels`: maximum loaded ChannelV2 runtimes per node; `0` preserves current unlimited behavior.
- `FollowerRecoveryProbeInterval`: base interval for parked follower recovery probes.
- `FollowerRecoveryProbeJitter`: bounded jitter added to the base interval.
- Existing idle and pull settings remain supported; this phase changes caught-up follower defaults so ordinary idle pull is not the steady-state keepalive.

If config keys are added to `cmd/wukongimv2`, align `wukongim.conf.example` and command-specific example configs.

## Observability

Add low-cardinality metrics through the existing ChannelV2 observer path:

- active runtime count by role
- runtime activation failures by reason
- follower parked count
- recovery probe total by result
- pull total by result and empty/non-empty classification
- metadata cache hit/miss/invalidate counts

Do not label by channel ID. Per-channel diagnosis should use existing diagnostics sampling paths, not Prometheus labels.

## Testing Strategy

Unit tests:

- Reactor capacity accepts updates for existing runtimes but rejects new activations over limit.
- Follower parks after an empty caught-up pull.
- Parked follower wakes on valid PullHint.
- Stale PullHint does not wake parked follower.
- Recovery probe schedules with jitter and respects retry backoff on failure.
- Metadata cache hit avoids resolver calls.
- Stale append error invalidates cache and retries metadata resolution once.

Benchmarks and scenario tests:

- In-memory 10k three-node warmup and idle benchmark.
- Sparse traffic benchmark over 1%-5% active channels.
- Real message DB wkbench/dev-sim scenario for 10k channel preparation and sparse steady traffic.

Full integration tests remain opt-in through existing integration or perf workflows because 10k real-store scenarios can be slow.

## Rollout

1. Add metrics and 10k baseline benchmark before behavior changes.
2. Add capacity limit with default unlimited behavior.
3. Add metadata cache with one-retry stale invalidation.
4. Add follower parked scheduling and recovery probes.
5. Tighten per-channel memory allocations.
6. Re-run the 10k benchmark and compare idle RPC rate, heap, goroutines, append p99, and storage p99.

## Follow-Up Project

After this phase proves that 10,000 live channels are cheap while idle, design node-pair batch replication:

- `PullBatch`
- `PullHintBatch`
- ordinary `ProgressAckBatch`
- binary Channel RPC codec

That follow-up should keep stopped-follower lifecycle ACK separate until ordinary progress batching is stable.
