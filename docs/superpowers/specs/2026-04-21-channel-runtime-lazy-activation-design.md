# Channel Runtime Lazy Activation Design

**Date:** 2026-04-21
**Status:** Proposed

## Overview

`internal/app/channelmeta.go` currently starts `channelMetaSync` with a one-second ticker, calls `syncOnce()`, lists all authoritative `ChannelRuntimeMeta`, filters the local replicas, and then eagerly applies every local channel into runtime. In practice this turns "the set of channels this node may own" into "the set of channels this node must materialize right now".

That behavior is acceptable only when the local replica count is small. In a deployment with tens of thousands or hundreds of thousands of channels, the current loop creates an unbounded startup and steady-state tax:

- periodic authoritative scans on every node
- repeated local metadata reconciliation for cold channels
- eager `runtime.EnsureChannel(meta)` for channels with no live traffic
- unnecessary CPU, IO, and memory pressure during node start and during quiescent periods

The approved direction is to remove the steady-state full scan entirely and replace it with authoritative on-demand activation:

- `slot/meta` remains the only authoritative source of channel runtime metadata
- local nodes keep only a lightweight in-memory routing/meta cache plus a hot runtime subset
- leader activation happens on business access
- follower activation happens on replication-path access plus an explicit leader wake-up signal
- no local persistent projection is required in the first rollout

This design keeps cluster-only semantics, preserves authoritative correctness, and makes cost proportional to hot channels instead of all channels assigned to a node.

## Goals

- Remove the one-second full scan and eager prewarm behavior from `channelMetaSync`
- Make local runtime creation lazy and demand-driven
- Keep `slot/meta` as the single authoritative source of `ChannelRuntimeMeta`
- Support large channel cardinality, including 100k-channel scale, without catastrophic startup preheat
- Allow non-owner nodes to cache leader routing metadata without materializing local runtime
- Ensure cold followers can be activated automatically when replication starts
- Bound authoritative lookup load with `singleflight`, negative cache, concurrency limits, and retry backoff
- Preserve existing cluster semantics; do not add a single-node shortcut

## Non-Goals

- Introducing a local persistent channel-meta projection in this rollout
- Replacing `slot/meta` with a new distributed metadata source
- Redesigning the channel placement algorithm independently from the existing slot topology
- Converting channel replication from follower-pull to a new leader-push data plane
- Providing immediate global cleanup of all stale local runtimes after every membership change
- Solving mixed-version compatibility for old and new activation behavior in the same cluster

## Current Foundation

The repository already contains most of the primitives required for a lazy model:

- `internal/app/channelmeta.go:RefreshChannelMeta()` already performs on-demand authoritative lookup and runtime-meta bootstrap on `metadb.ErrNotFound`
- `pkg/slot/proxy/runtime_meta_rpc.go` already exposes authoritative `GetChannelRuntimeMeta` over slot RPC
- `pkg/channel/runtime/runtime.go:EnsureChannel()` already materializes a runtime channel and schedules follower replication bootstrap
- `pkg/channel/runtime/backpressure.go` and `pkg/channel/runtime/longpoll.go` already centralize replication ingress handling
- `internal/access/node/channel_append_rpc.go` and the message send retry path already know how to refresh metadata after `ErrStaleMeta` / `ErrNotLeader`

The main problem is not missing primitives. The problem is that the lifecycle is still built around a periodic full scan, and the replication ingress path still assumes the local runtime already exists.

## Problem Statement

### 1. Startup and steady-state preheat are tied to total local replica count

`internal/app/channelmeta.go:Start()` immediately calls `syncOnce()` and then runs it every second. `syncOnce()` calls `ListChannelRuntimeMeta()`, filters every meta that includes the local node in `Replicas`, reconciles it, and applies it locally.

This means a node with 100k local replica channels will repeatedly touch all 100k channels even if only a tiny fraction are hot.

### 2. Local apply conflates routing metadata with hot runtime objects

The current `channelMetaSync.apply()` path rejects non-local replica metadata with `channel.ErrStaleMeta`, while `pkg/channel/channel.go:ApplyMeta()` always updates handler metadata and then unconditionally calls runtime `UpsertMeta()`.

This makes it difficult to express the desired lazy model:

- some nodes only need leader routing information so they can forward to the true leader
- only nodes that currently host the channel and actually need it should create runtime state

### 3. Replication ingress assumes runtime already exists

The replication ingress path is currently fail-fast on local runtime miss:

- `pkg/channel/runtime/backpressure.go:ServeFetch()` -> `lookupChannel()` miss returns `ErrChannelNotFound`
- `pkg/channel/runtime/backpressure.go:ServeReconcileProbe()` -> `lookupChannel()` miss returns `ErrChannelNotFound`
- `pkg/channel/runtime/longpoll.go:ServeLanePoll()` only tracks channels already found via `lookupChannel()`

With a lazy model, these misses must become activation opportunities for valid local replicas.

### 4. Append only heats the leader

The write path is leader-centric. A business append activates the leader, but not necessarily the followers. Since steady-state replication is follower-pull, a cold follower with no local runtime may never issue the pull that would warm itself up.

Therefore, removing periodic prewarm without adding a follower wake-up path would break cold follower recovery.

## Approaches Considered

### Approach A: Keep the periodic full scan and optimize it

Examples:

- reduce scan frequency
- scan per slot page
- cache more state between scans

Pros:

- smallest conceptual change
- keeps a global reconciliation loop

Cons:

- cost still scales with total local replica count
- startup preheat problem remains fundamentally unsolved
- still treats cold channels as runtime objects that must exist locally

This approach was rejected.

### Approach B: Authoritative on-demand lookup only, no follower wake-up

Examples:

- remove full scan
- load metadata on demand for business traffic
- let runtime activation happen only when a local request touches the channel

Pros:

- much cheaper than a full scan
- simple correctness story

Cons:

- cold followers may never activate because they do not yet have runtime state to initiate follower-pull replication
- does not fully replace the current prewarm behavior

This approach was rejected.

### Approach C: Authoritative on-demand lookup plus follower wake-up and hot-cache runtime (Recommended)

Examples:

- remove periodic full scan entirely
- keep `slot/meta` authoritative
- activate on business access or replication ingress
- wake cold followers from the leader via a lightweight probe
- maintain only in-memory routing cache and hot runtime objects

Pros:

- removes startup and steady-state preheat cost
- preserves authoritative correctness
- supports cold follower activation without local persistent projection
- scales cost with hot channels rather than full channel population

Cons:

- requires explicit activation control in both business and replication ingress paths
- requires local anti-stampede controls

This is the approved direction.

## Design Principles

### 1. Authority and activation are separate concerns

`slot/meta` is authoritative metadata. Local runtime is only an execution cache.

### 2. Routing metadata and runtime state are not the same thing

A node may need leader routing metadata without needing a local runtime.

### 3. Cold-path correctness is more important than proactive preheat

The first request for a cold channel may pay an extra metadata lookup. That is acceptable.

### 4. Activation must be fail-closed and bounded

A cold-channel flood must not become a new implicit prewarm storm.

### 5. Eventual cleanup is sufficient for inactive channels

Without a periodic full scan, stale inactive runtimes may linger briefly. They must be corrected on access and reclaimed eventually via idle eviction rather than global background reconciliation.

## Proposed Design

## 1. Remove `channelMetaSync` steady-state full scan

The first rollout removes the normal-path use of:

- `channelMetaSync.Start()` ticker-driven `syncOnce()`
- `channelMetaSync.syncOnce()` as a steady-state reconciliation loop

New behavior:

- `RefreshChannelMeta()` remains as the business-path on-demand authoritative refresh entry point
- `syncOnce()` is removed from the normal lifecycle; if retained temporarily for tests or operational debugging, it is not started automatically and is not part of steady-state behavior
- node startup no longer scans all authoritative channel metadata

This change is the primary startup and CPU win.

## 2. Narrow `channelMetaSync` into an activation coordinator

To minimize churn, the first rollout may keep the existing type name internally, but its responsibility changes from "sync all local runtime metadata" to "activate metadata on demand".

The activation coordinator exposes two logical entry points:

- `ActivateByID(ctx, id channel.ChannelID)` for business paths
- `ActivateByKey(ctx, key channel.ChannelKey)` for replication ingress paths

`RefreshChannelMeta()` can remain as the public business-facing entry point and call `ActivateByID(...)` internally.

`ActivateByKey(...)` is new and is only used from runtime replication ingress and wake-up paths.

## 3. Split local routing metadata from local runtime activation

The current model effectively forces `ApplyMeta` to mean both:

- store routing metadata in the handler service
- materialize local runtime

The lazy model requires those two actions to be separated.

### New local apply semantics

When authoritative metadata is loaded:

1. always update the local handler/routing metadata cache
2. if the local node is in `Replicas`, ensure or update local runtime
3. if the local node is not in `Replicas`, do not create runtime
4. if a runtime for that key already exists locally but the local node is no longer in `Replicas`, remove that runtime

This gives the node enough information to:

- forward client/business requests to the true leader even when it is not a channel replica
- keep local runtime limited to channels the node should actually host

### Consequence for current behavior

`RefreshChannelMeta()` must no longer return `channel.ErrStaleMeta` simply because the local node is not in `Replicas`. Instead, it should return the authoritative metadata after updating the local routing cache and leave runtime absent on that node.

## 4. Add `ChannelKey` reverse parsing for replication-path activation

Replication ingress currently receives `channel.ChannelKey`, not `channel.ChannelID`.

The first rollout uses the existing encoding contract from `pkg/channel/handler/key.go`:

- key format: `channel/<type>/<base64raw(channelID)>`

The design adds the inverse helper:

- `ParseChannelKey(key channel.ChannelKey) (channel.ChannelID, error)`

This is preferred over changing the wire protocol because:

- it keeps the initial rollout smaller
- it avoids adding more fields to fetch/probe/long-poll envelopes
- it avoids introducing another local projection just to map key back to ID

If future protocols need more flexibility, they can carry `ChannelID` explicitly. That is not required in the first rollout.

## 5. Business-path activation flow

Business-path activation continues to use the existing authoritative refresh path.

Recommended flow:

1. request path hits `ErrStaleMeta`, `ErrNotLeader`, or an equivalent metadata mismatch
2. call `ActivateByID(...)`
3. load authoritative `ChannelRuntimeMeta`
4. if authoritative meta is missing, retain the existing explicit bootstrap behavior for the business send path
5. update the local routing cache
6. ensure or update local runtime only if the local node is in `Replicas`
7. retry the business request once

This preserves the already approved write-path bootstrap model while removing the full-scan dependency.

## 6. Replication-path activation flow

Replication ingress becomes activation-aware.

Affected entry points:

- `pkg/channel/runtime/backpressure.go:ServeFetch()`
- `pkg/channel/runtime/backpressure.go:ServeReconcileProbe()`
- `pkg/channel/runtime/longpoll.go:ServeLanePoll()`

New miss behavior:

1. `lookupChannel()` miss occurs
2. do not immediately return `ErrChannelNotFound`
3. call `ActivateByKey(...)`
4. `ActivateByKey(...)` parses the key, loads authoritative metadata, and checks replica membership
5. if the local node belongs to `Replicas`, ensure/update local runtime and retry the original operation exactly once
6. if the local node does not belong to `Replicas`, record a short-lived negative cache entry and return the original miss/error
7. if authoritative metadata is missing, record a short-lived negative cache entry and return the original miss/error

`ServeLanePoll()` needs the same activation logic during session open or membership processing so long-poll lanes can adopt newly awakened follower channels without requiring global prewarm.

## 7. Cold follower activation via leader wake-up

Removing the full scan is not enough because append traffic only heats the leader.

The design adds a lightweight leader-initiated wake-up signal for cold followers. The first rollout reuses the existing `ReconcileProbe` semantics rather than defining a new RPC type.

### Wake-up trigger

Leader sends a wake-up probe when:

- a channel transitions from cold to hot on the leader
- or the leader observes that a follower has no recent progress / lane session for a channel that should be replicated

### Wake-up follower behavior

When a follower receives the probe:

1. the local runtime may be absent
2. the ingress path invokes `ActivateByKey(...)`
3. authoritative metadata is loaded
4. if the follower is still in `Replicas`, it calls `EnsureChannel(meta)`
5. `EnsureChannel(meta)` schedules the normal follow-up fetch / long-poll bootstrap toward the leader
6. the follower transitions into the normal steady-state replication loop

This preserves the follower-pull replication design while still allowing followers to cold-start on demand.

## 8. Consistency model without periodic full scan

The steady-state full scan is removed, so the system no longer attempts to keep every possible local channel runtime continuously aligned in the background.

The new consistency contract is:

- active channels are corrected on access
- inactive channels may remain locally stale for a time
- stale inactive runtime is acceptable as long as it is removed or corrected on the next access or by idle eviction

### Access-path correction

Whenever a channel is accessed through business or replication paths, authoritative metadata is reloaded when needed and local state is corrected.

### Version-mismatch correction

When the runtime or handler surfaces metadata mismatch signals such as:

- `ErrStaleMeta`
- `ErrGenerationMismatch`
- `ErrNotLeader`

the caller triggers one authoritative refresh and one retry.

### Replica-removal correction

If authoritative metadata says the local node no longer belongs to `Replicas`:

- do not create local runtime
- remove existing local runtime if it exists
- keep only routing metadata if the node still needs to forward traffic

## 9. Activation cache, anti-stampede control, and backpressure

Authoritative on-demand lookup is only safe if cold-path concurrency is bounded.

The activation coordinator therefore maintains:

- per-channel `singleflight` for concurrent activation attempts
- short TTL positive in-memory metadata cache for hot channels
- short TTL negative cache for `not found` and `not local replica`
- global activation concurrency limit
- authoritative RPC timeout
- retry backoff for repeated wake-up attempts on the same `(channel, follower)` pair

### Why negative cache is required

Without negative cache, a misrouted or stale request storm could repeatedly hammer the authoritative slot leader for the same cold invalid channel.

### Why global concurrency control is required

Without a global limit, a large cold-channel burst could turn the lazy path into a thundering herd against the authoritative store and the local runtime factory.

The system should prefer failing closed and letting callers retry later over overloading the control path.

## 10. Idle eviction and eventual reclamation

The design goal is hot runtime, not permanent runtime.

Removing full prewarm solves startup cost, but a node may still gradually accumulate many channels if it touches many of them over time. Therefore the lazy model pairs naturally with idle eviction.

The first rollout may land the activation model before a full eviction policy if necessary, but the design target is:

- track recent business/replication activity per local runtime channel
- evict channels that are idle beyond a configured threshold
- never evict channels in the middle of critical reconcile / replication work

Idle eviction becomes the eventual reclamation mechanism for:

- long-cold local replicas
- stale local runtimes that are no longer needed
- channels activated once and never touched again

The hard ceiling from existing runtime limits remains as a final protection layer.

## 11. Failure semantics

### Authoritative metadata unavailable

If the authoritative slot leader is unavailable or the lookup times out:

- activation fails closed
- the original business or replication request returns an error
- no speculative local runtime is created

### `not found`

If authoritative metadata does not exist:

- business-path refresh may still use the already approved bootstrap behavior where appropriate
- replication-path activation does not bootstrap on its own; it records negative cache and fails

### Not a local replica

If authoritative metadata exists but the local node is not in `Replicas`:

- keep or update routing metadata
- ensure runtime is absent
- negative-cache the activation result briefly

### Repeated wake-up noise

Leader wake-up must be deduplicated and rate limited per `(channel, follower)` so a cold or removed follower does not create an endless probe storm.

## 12. Observability

The rollout should add or update metrics and logs so operators can confirm the new behavior:

- activation attempts by source (`business`, `fetch`, `probe`, `lane_open`)
- activation results (`hit`, `singleflight_shared`, `not_found`, `not_local_replica`, `ensured`, `refreshed_only`, `failed`)
- authoritative lookup latency and timeout counts
- wake-up probe sent / deduplicated / failed counts
- runtime channel count, activation concurrency saturation, and idle eviction counts
- negative-cache hit counts

This observability is important because the old system's behavior was easy to reason about but expensive; the new system is cheaper but more event-driven.

## 13. Phased rollout

### Phase 1: Remove full scan and add on-demand activation

- stop automatic `syncOnce()` startup/ticker behavior
- keep `RefreshChannelMeta()` as business-path activation
- add `ActivateByKey(...)`
- add routing-cache-only apply semantics for non-local replicas
- add replication ingress activation shim
- add `singleflight`, negative cache, and activation concurrency limits

### Phase 2: Add leader wake-up for cold followers

- trigger lightweight wake-up probe from leader to follower
- deduplicate and rate limit wake-up attempts
- ensure followers self-bootstrap into fetch / long-poll replication after activation

### Phase 3: Add idle eviction and tuning

- add runtime idle tracking and eviction
- tune TTLs, activation concurrency, wake-up backoff, and observability thresholds based on load testing

## 14. Testing Strategy

The implementation should be verified with:

- unit tests for `ParseChannelKey(...)`
- unit tests for activation coordinator positive cache, negative cache, and `singleflight`
- unit tests proving non-local replica metadata updates routing cache without creating runtime
- unit tests for replication ingress "miss -> activate -> retry once" behavior
- integration tests proving node startup no longer scans and prewarms all local channels
- integration tests proving a cold follower can activate after leader append plus wake-up
- integration tests proving removed replicas do not recreate local runtime and are eventually reclaimed
- regression tests for the existing business-path runtime-meta bootstrap behavior

## 15. Risks and Mitigations

### Risk: Cold-path latency regression

Mitigation:

- accept one extra lookup on first access
- use small hot metadata cache
- share concurrent activation via `singleflight`

### Risk: Authoritative lookup hotspot

Mitigation:

- negative cache
- activation concurrency cap
- wake-up deduplication
- fail-closed timeout behavior

### Risk: Runtime leaks after removing full scan

Mitigation:

- remove runtime immediately when authoritative refresh shows local node is no longer a replica
- add idle eviction
- retain `MaxChannels` as hard backstop

### Risk: Follower never wakes

Mitigation:

- add explicit leader wake-up probe
- trigger on cold-to-hot transition and missing follower progress
- reuse existing reconcile-oriented ingress path

## Summary

The approved design replaces the current "periodically scan and prewarm everything local" model with "activate only what is touched, and only when it is needed".

The key shifts are:

- remove the one-second full scan
- keep `slot/meta` authoritative
- split routing metadata from runtime materialization
- activate leaders on business access
- activate followers on replication ingress plus leader wake-up
- bound cold-path load with caching, `singleflight`, and concurrency control
- reclaim long-cold runtime via idle eviction rather than global background prewarm

This keeps correctness tied to the authoritative slot metadata while making runtime cost proportional to hot channels instead of the full local channel population.
