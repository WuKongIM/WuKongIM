# Channel Runtime Execution Virtualization Design

**Date:** 2026-05-14
**Status:** Proposed

## Overview

The short-term channel-runtime capacity guardrails now protect a node from unbounded local runtime growth:

- `WK_CLUSTER_MAX_CHANNELS` fails closed before active local channel runtimes grow without limit.
- `WK_CLUSTER_CHANNEL_IDLE_TIMEOUT` and `WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL` reclaim cold local runtimes.
- pressure tests, Prometheus metrics, Grafana panels, and alert rules expose the protection path.

Those controls are necessary, but they do not make 100k active channels cheap. The current `pkg/channel/replica` model still gives each materialized channel its own single-writer loop and effect workers. A capped activation pressure run showed roughly 15k extra goroutines for 5k activated channels in the current test model, which is a strong signal that 100k concurrently materialized channels can become a scheduler, memory, and GC problem even before message volume is high.

This design targets the next step: support a deployment where about 100k channels are considered active, with 10k-30k channels having continuous light activity, without making goroutine and memory costs scale linearly with channel count.

The proposed direction is staged:

1. move per-channel execution onto shared worker pools;
2. add runtime hibernation for the cold tail;
3. batch replication lane work across many lightly active channels.

The deployment model remains cluster-only. A single-node deployment is still a single-node cluster, and none of these changes introduce a bypass path around cluster semantics.

## Goals

- Support 100k active channel metadata entries on a node without 100k always-running execution loops.
- Make goroutine count primarily proportional to configured worker pools and active work, not active channel cardinality.
- Keep channel correctness fences intact: channel epoch, leader epoch, generation, lease, write fence, tombstone, and retention boundaries.
- Preserve the `pkg/channel/runtime` public behavior for append, fetch, reconcile probe, lane poll, retention, migration drain, and close.
- Keep the first implementation phase small enough to validate with existing unit and pressure tests.
- Add new pressure tests that measure goroutine, heap, queue latency, and per-channel fairness at 10k-100k scale.
- Keep existing capacity controls as production safety backstops.

## Non-Goals

- Replacing Channel Log, Slot metadata, Controller Raft, or the cluster transport.
- Changing channel placement or replica membership algorithms.
- Introducing a separate single-node local-only execution path.
- Solving a fully hot 100k-channel workload where every channel has sustained high message throughput.
- Rewriting the whole replica state machine in one step.
- Removing `WK_CLUSTER_MAX_CHANNELS`; it remains a hard operational guardrail.

## Workload Assumption

The approved target is a medium-active workload:

- about 100k channels may be active or recently active on a node;
- about 10k-30k channels may have continuous light activity;
- most channels do not need a dedicated goroutine at every moment;
- latency should remain acceptable when a channel transitions from idle to active;
- fairness matters: a few hot channels must not starve many light channels.

If later tests show that most of the 100k channels are continuously hot, this design is not enough by itself. That scenario requires deeper batching, partitioning, and possibly protocol-level aggregation.

## Current Constraints

### Runtime materialization

`pkg/channel/runtime.EnsureChannel()` materializes a `channel` object and creates a `pkg/channel/replica.Replica` through the configured factory. The runtime then schedules follower replication, snapshot work, backpressure retries, and lane membership around that local runtime.

### Replica execution model

`pkg/channel/replica.NewReplica()` recovers durable state and starts a single-writer loop. The replica also has append and checkpoint durable-effect workers, and append group commit may create timer goroutines. This design is correct and easy to reason about, but expensive when multiplied by very large channel counts.

### Recent guardrails

The short-term capacity work adds:

- active runtime cap;
- idle runtime eviction;
- pressure test for activation footprint;
- capacity and eviction metrics;
- Grafana and Prometheus coverage.

Those guardrails should remain during all later phases.

## Approaches Considered

### Approach A: Raise limits and rely on idle eviction

This keeps the current execution model and tunes `WK_CLUSTER_MAX_CHANNELS` plus idle timeout.

Pros:

- no architectural refactor;
- already implemented;
- good for protection and cold-tail cleanup.

Cons:

- goroutine count still grows with materialized channels;
- 10k-30k continuously light-active channels still keep many loops resident;
- eviction cannot help channels that remain lightly active.

This is useful as a safety layer, but it is not enough for the target workload.

### Approach B: Shared worker pools first (Recommended Phase 1)

Keep per-channel state and public APIs, but replace per-replica always-running loops with mailbox scheduling on shared workers.

Pros:

- directly attacks the goroutine multiplier;
- preserves state-machine serialization per channel;
- can be rolled out behind a config flag;
- existing tests can be reused with minimal semantic changes.

Cons:

- channel and replica objects may still consume heap;
- requires careful fairness and queue-depth controls;
- effect-worker behavior must be preserved or adapted.

This is the recommended first phase.

### Approach C: Hibernation first

Unload cold channel runtime objects and reload them on demand.

Pros:

- reduces heap as well as goroutines;
- useful for the cold tail.

Cons:

- correctness is more complex because wake-up must preserve generation, tombstone, lease, write-fence, cursor, and retention semantics;
- does not solve the goroutine cost of the 10k-30k continuously light-active set unless combined with worker pools.

This is recommended as the second phase.

### Approach D: Batch replication lanes first

Make follower fetch/probe/long-poll work operate on larger batches by peer and lane.

Pros:

- reduces RPC and scheduler pressure;
- well suited to many light-active channels.

Cons:

- touches the data plane and lane protocol more deeply;
- harder to validate before reducing the per-channel goroutine cost;
- still needs execution virtualization underneath for local processing.

This is recommended as the third phase.

## Proposed Design

## Phase 1: Shared Replica Execution Workers

### 1.1 Add an execution mode

Introduce a channel replica execution mode behind configuration, initially defaulting to the current behavior.

Proposed internal configuration shape:

- `WK_CLUSTER_CHANNEL_EXECUTION_MODE=dedicated|pooled`
- `WK_CLUSTER_CHANNEL_EXECUTION_WORKERS=<n>`
- `WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE=<n>`

The exact names can be refined during implementation, but the operator-facing meaning should stay clear:

- `dedicated` keeps current per-replica loop behavior;
- `pooled` routes replica loop commands through shared workers;
- queue size bounds memory and fails closed under overload.

All config fields must have English comments and be mirrored in `wukongim.conf.example`.

### 1.2 Preserve per-channel single-writer semantics

The key invariant is unchanged: one channel replica state machine must process one event at a time, in order.

In pooled mode:

- each replica owns a lightweight mailbox;
- submitting a command appends to the mailbox;
- if the mailbox was previously idle, the replica key is put in a global ready queue;
- a worker pops a ready replica, processes a bounded number of events, then either requeues it or marks it idle;
- only one worker may own a replica mailbox at a time.

This turns per-channel goroutines into per-worker goroutines while keeping per-channel serialization.

### 1.3 Bound fairness and tail latency

Workers should process a small bounded batch per channel, for example:

- max events per turn;
- max bytes or records per turn;
- max wall-clock budget per turn.

If a channel still has work after its budget, it is requeued behind other ready channels. This prevents hot channels from starving light-active channels.

### 1.4 Treat timers as scheduled events, not goroutines per channel

Append group-commit timers are a known source of goroutine growth. In pooled mode, timers should be centralized:

- replicas request a flush deadline;
- a shared timer wheel or heap wakes due replicas;
- due flushes enqueue mailbox events;
- stale deadlines are fenced by generation or local timer version.

The first implementation may use a small centralized scheduler rather than a full timer wheel, as long as timer goroutines do not scale with channel count.

### 1.5 Keep durable effects bounded

The current replica separates loop processing from durable effects. Pooled mode must preserve storage correctness:

- durable mutations remain fenced by effect ID, channel key, epoch, role generation, and leader/leader-epoch where applicable;
- each channel still has one durable mutation lane, either through an in-channel lock or a per-channel durable mailbox;
- global durable effect workers are bounded;
- per-channel durable ordering is preserved.

A practical first step is to pool only the single-writer loop and keep durable-effect behavior compatible. After that is green, durable-effect workers can be pooled or multiplexed.

### 1.6 Runtime integration

`pkg/channel/runtime` should not need to know whether a replica is dedicated or pooled. It should continue to call the same `replica.Replica` interface:

- `Append`
- `Fetch`
- `ApplyFetch`
- `ApplyMeta`
- `BecomeLeader`
- `BecomeFollower`
- `Tombstone`
- `Close`
- retention and migration methods

The replica factory decides which execution mode to use.

### 1.7 Metrics

Add low-cardinality metrics for pooled mode:

- ready queue depth;
- mailbox enqueue total by result;
- worker busy ratio;
- mailbox wait duration histogram;
- per-node dropped/rejected mailbox events by reason.

Do not add channel ID labels.

### 1.8 Rollout gate

Pooled mode should ship behind a disabled-by-default config flag. The first rollout criteria:

- all existing `pkg/channel/replica` and `pkg/channel/runtime` tests pass in dedicated mode;
- a selected suite runs in pooled mode;
- pressure test shows goroutine growth is bounded by worker count rather than channel count;
- no correctness differences in append, fetch, retention, migration drain, and tombstone behavior.

## Phase 2: Runtime Hibernation

### 2.1 Add hibernatable channel states

After pooled execution is stable, add a lightweight hibernated state for cold channels.

Proposed local states:

- `resident`: full channel runtime and replica state are loaded;
- `hibernating`: close/unload is in progress; new work must wait or retry;
- `hibernated`: only lightweight metadata remains;
- `waking`: reload is in progress; concurrent wake-ups coalesce.

### 2.2 Hibernation safety checks

A channel can hibernate only when:

- no active API call is using it;
- no scheduler work is pending;
- no peer request is in flight or queued;
- no snapshot waiter is active;
- no append, durable effect, checkpoint, or drain is in progress;
- no lease-sensitive leader work would be lost incorrectly.

This builds on the existing idle eviction guards, but hibernation keeps lightweight state instead of dropping all local memory.

### 2.3 Rehydrate on demand

Any business, replication, retention, or migration access to a hibernated channel rehydrates it through the authoritative activation path.

Wake-up must be fenced by:

- channel key;
- local generation;
- channel epoch;
- leader epoch;
- tombstone generation;
- write-fence version;
- retention boundary.

Concurrent wake-ups should use singleflight-like coalescing to avoid stampedes.

### 2.4 Hibernation metrics

Add metrics:

- hibernated channel count;
- hibernation total by result;
- rehydrate total by result;
- rehydrate latency;
- hibernation rejected/skipped by reason.

Again, no high-cardinality labels.

## Phase 3: Batch Replication Lane Work

### 3.1 Batch by peer and lane

After local execution and hibernation are stable, optimize many light-active channels by batching replication selections:

- group ready channels by peer and lane;
- apply a byte/channel/time budget per poll;
- preserve per-channel epoch and generation fences in each item;
- return partial batches with `MoreReady` when work remains.

This extends the existing lane long-poll model instead of replacing cluster transport.

### 3.2 Prioritize fairness

Replication should avoid starving low-throughput channels behind a small set of hot channels:

- round-robin or weighted fair selection per lane;
- separate urgent reset/truncate/snapshot signals from ordinary fetch data;
- bounded per-channel contribution per batch.

### 3.3 Keep fallbacks

If a peer does not support the optimized batch behavior, retain the existing request/response path. Mixed-version behavior should fail closed for semantics-sensitive features and avoid silent data loss.

## Failure Handling

### Queue overload

If pooled execution queues are full:

- fail fast with a clear runtime error;
- increment low-cardinality rejection metrics;
- do not create unbounded goroutines as a fallback;
- let upper layers retry according to existing backpressure behavior.

### Stale wake-up or response

Any stale wake-up, fetch response, probe response, snapshot result, or durable effect must be discarded when its generation/epoch/fence no longer matches.

### Worker panic

Workers should recover only at process-supervision boundaries already accepted by the project. Do not hide state-machine corruption. If a worker exits unexpectedly, fail closed and surface metrics/logs.

### Config rollback

Dedicated mode remains available during Phase 1 rollout. Operators can switch back to dedicated mode if pooled execution exposes unexpected production behavior.

## Testing Strategy

### Unit tests

- Run existing replica tests in dedicated mode.
- Add a pooled-mode variant for core replica API tests.
- Test mailbox serialization with concurrent append/fetch/apply operations.
- Test fairness: one hot channel must not starve many light channels.
- Test close/tombstone while mailbox and durable effects are pending.
- Test group-commit timer behavior without per-channel timer goroutine growth.

### Runtime tests

- Ensure `EnsureChannel`, `RemoveChannel`, idle eviction, retention, migration drain, and replication ingress behave the same in pooled mode.
- Verify `ErrTooManyChannels` and capacity metrics still work.
- Verify no stale response can mutate a channel after hibernation or generation change.

### Pressure tests

Add explicit opt-in stress tests:

- activate 100k channels in pooled mode;
- simulate 10k-30k light-active channels with periodic append/fetch/probe events;
- report goroutine delta, heap delta, queue wait latency, worker busy ratio, and throughput;
- compare dedicated and pooled mode on a smaller count to prove the goroutine curve changes.

These tests should remain skipped by default and enabled through environment variables.

### Integration tests

- Single-node cluster append/fetch in pooled mode.
- Multi-node replication in pooled mode.
- Leader change while channels have queued mailbox work.
- Retention and migration drain while pooled workers are busy.

## Rollout Plan

1. Implement pooled execution behind config, disabled by default.
2. Add pooled-mode tests and pressure tests.
3. Run staging pressure at 5k, 20k, 50k, and 100k channels.
4. Enable pooled mode on one staging node in a multi-node cluster.
5. Enable pooled mode across staging.
6. Keep `WK_CLUSTER_MAX_CHANNELS` and idle eviction enabled as safety rails.
7. Only after pooled mode is stable, start Phase 2 hibernation.

## Open Questions

1. What latency target is acceptable for a light-active channel when it waits in the pooled mailbox queue?
2. Should pooled mode be enabled per node, or require all nodes in a cluster to use the same mode?
3. Should durable-effect pooling happen in Phase 1 or be a separate follow-up after loop pooling?
4. What is the minimum staging hardware profile for the 100k-channel pressure baseline?
5. Which metrics should become default alerts versus dashboard-only diagnostics?

## Success Criteria

Phase 1 is successful when:

- 100k active channel pressure in pooled mode keeps goroutine count near a configured worker envelope rather than scaling linearly with channel count;
- 10k-30k light-active channel simulation remains stable under bounded queue depth;
- append/fetch/replication correctness tests pass in both dedicated and pooled modes;
- no high-cardinality metrics are introduced;
- operational rollback to dedicated mode remains available.

Phase 2 is successful when:

- cold-tail heap usage decreases after hibernation;
- wake-up latency remains within the agreed target;
- stale wake-up and stale response tests prove generation/epoch fences are effective.

Phase 3 is successful when:

- peer/lane replication RPC and scheduler overhead decrease under 10k-30k light-active channels;
- fairness improves or remains stable;
- mixed-version or fallback behavior remains safe.
