# Channel Runtime Meta Bootstrap Design

**Date:** 2026-04-17
**Status:** Proposed

## Overview

Define a narrow, explicit bootstrap path for `ChannelRuntimeMeta` so the first durable write to any channel can self-heal missing runtime metadata without turning generic metadata reads into implicit write operations.

The immediate problem is that the durable send path currently treats missing `ChannelRuntimeMeta` as stale metadata, attempts a refresh, and then fails hard when authoritative storage returns `metadb.ErrNotFound`. This blocks first-send behavior even though runtime metadata is an internal channel-log concern rather than proof that the business-level channel record was created beforehand.

This design keeps `GetChannelRuntimeMeta()` as a pure read, introduces an explicit runtime-meta ensure/bootstrap capability, and uses that capability only from the message send refresh path in the first rollout.

## Goals

- Allow first durable send to bootstrap missing `ChannelRuntimeMeta` for any channel type
- Keep `GetChannelRuntimeMeta()` pure so read paths do not silently create runtime state
- Make bootstrap deterministic across nodes by deriving the initial replica view from the current slot topology
- Respect configured cluster semantics while automatically clamping invalid initial `MinISR` values in small clusters
- Limit the first rollout to the send refresh path so the change is easy to reason about and test
- Preserve the existing send retry model: one refresh attempt, then one append retry

## Non-Goals

- Solving the full lifecycle of channel runtime metadata after bootstrap
- Redesigning per-channel placement independently from the slot topology
- Making conversation/status/facts read paths auto-create runtime metadata
- Replacing `UpsertChannelRuntimeMeta()` with a new storage transaction model in this iteration
- Introducing channel-info existence checks before runtime meta bootstrap
- Relaxing cluster-only semantics or adding single-node special branches

## Current Foundation

The repository already has the essential primitives needed for a first bootstrap implementation:

- `internal/usecase/message/retry.go` already retries durable append after metadata refresh
- `internal/app/channelmeta.go` already centralizes `RefreshChannelMeta()` and local `ApplyMeta()`
- `pkg/slot/proxy/store.go` already exposes authoritative `GetChannelRuntimeMeta()` and `UpsertChannelRuntimeMeta()`
- `pkg/cluster/api.go` already exposes `SlotForKey`, `PeersForSlot`, and `LeaderOf`
- `pkg/slot/meta/channel_runtime_meta.go` already validates `Replicas`, `ISR`, `Leader`, `MinISR`, and lease fields

The missing piece is an explicit coordinator that can turn `ErrNotFound` during the send refresh path into a deterministic authoritative bootstrap.

## Problem Statement

Today the durable send path behaves like this:

1. `message.Send()` calls durable append
2. channel handler cannot find local runtime metadata and returns `channel.ErrStaleMeta`
3. `sendWithMetaRefreshRetry()` calls `RefreshChannelMeta()`
4. `RefreshChannelMeta()` performs only `GetChannelRuntimeMeta()` against the authoritative store
5. if the authoritative store also has no runtime metadata, the path returns `metadb.ErrNotFound`
6. send fails instead of healing the missing runtime metadata

This is too strict for the runtime-meta layer.

The design assumption for this change is:

- every channel that reaches durable send requires `ChannelRuntimeMeta`
- runtime-meta creation does not depend on separate channel-info existence
- missing runtime meta should be treated as a bootstrap opportunity on the write path, not as proof that the channel is invalid

## Design Principles

### 1. Pure reads stay pure

Generic metadata reads must not create new runtime state as a hidden side effect.

### 2. Bootstrap belongs to an explicit ensure path

The system should have a named operation that means “make sure runtime metadata exists”, instead of burying creation semantics inside a read helper.

### 3. First bootstrap must be deterministic

Concurrent first sends from different nodes must converge on the same initial runtime view.

### 4. Runtime metadata is channel-log state, not business channel proof

Bootstrap must not require pre-existing channel-info rows or special-case channel types.

### 5. Preserve current retry shape

The rollout should fit into the existing “refresh once, retry append once” send model.

## Proposed Design

## 1. Add an explicit bootstrap coordinator in `internal/app`

Introduce an internal coordinator, referred to here as `channelMetaBootstrapper`, owned by `internal/app`.

Responsibility:

- derive the initial authoritative `ChannelRuntimeMeta` for a channel that is missing runtime metadata
- write that metadata through the existing store API
- return the authoritative value after reloading it from storage

This coordinator belongs in `internal/app`, not `pkg/slot/proxy`, because bootstrap policy depends on:

- cluster topology
- runtime defaults such as bootstrap `MinISR`
- bootstrap lease policy
- the send-path refresh behavior already coordinated in `internal/app/channelmeta.go`

`pkg/slot/proxy` remains a storage facade, not the owner of runtime bootstrap policy.

## 2. Keep `GetChannelRuntimeMeta()` pure

`pkg/slot/proxy/store.go:GetChannelRuntimeMeta()` remains unchanged in meaning:

- if runtime metadata exists, return it
- if runtime metadata does not exist, return `metadb.ErrNotFound`

This ensures conversation/status/facts/owner lookup reads remain side-effect free.

## 3. Change `RefreshChannelMeta()` to bootstrap on authoritative miss

`internal/app/channelmeta.go:RefreshChannelMeta()` changes from “read-only refresh” to “read, optionally ensure, then read again”.

New behavior:

1. call `GetChannelRuntimeMeta()`
2. if found, project and apply as today
3. if `metadb.ErrNotFound`, call `bootstrapper.EnsureChannelRuntimeMeta(...)`
4. after ensure succeeds, re-read authoritative runtime metadata
5. project and apply the authoritative value
6. return the projected `channel.Meta`

This keeps bootstrap restricted to the refresh path already used by durable send retry.

## 4. Initial runtime-meta derivation

When bootstrap is needed, the coordinator derives the initial runtime meta from the current slot topology for the channel key.

Derived inputs:

- `slotID = cluster.SlotForKey(channelID)`
- `replicas = cluster.PeersForSlot(slotID)`
- `leader = cluster.LeaderOf(slotID)`

Initial runtime-meta fields:

- `ChannelID = requested channelID`
- `ChannelType = requested channelType`
- `Replicas = replicas`
- `ISR = replicas`
- `Leader = leader`
- `ChannelEpoch = 1`
- `LeaderEpoch = 1`
- `Status = Active`
- `Features = MessageSeqFormatLegacyU32`
- `LeaseUntilMS = now + bootstrapLease`

### Why bootstrap from slot topology

The slot topology is not used because channel and slot are conceptually identical. It is used because bootstrap needs a deterministic, cluster-wide consistent initial placement rule, and the current slot topology is the only authoritative input already available everywhere.

Using slot topology for the initial seed provides:

- deterministic concurrent first-send behavior
- consistency with current hash-slot routing
- no new control-plane dependency for per-channel placement in this rollout
- no single-node bypass branch

More precisely, the bootstrap uses slot topology as the initial placement seed for channel runtime metadata. It does not claim that future per-channel runtime evolution must remain permanently identical to slot topology.

## 5. Bootstrap `MinISR`

Add a new config value:

- `WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR`
- default value: `2`

Bootstrap computes:

```text
configuredDefaultMinISR = 2
initialReplicaCount = len(replicas)
effectiveInitialMinISR = min(configuredDefaultMinISR, initialReplicaCount)
```

Examples:

- 3-node replica set -> `MinISR = 2`
- 2-node replica set -> `MinISR = 2`
- single-node cluster -> `MinISR = 1`

This preserves the configured default while ensuring bootstrap metadata remains valid under the repository's existing `MinISR <= len(Replicas)` validation rules.

## 6. Bootstrap lease

Bootstrap also assigns an initial leader lease.

In the first rollout:

- bootstrap lease remains an internal constant, not a user-facing config knob
- it should be conservative and short-lived
- the design intentionally treats ongoing lease/leader lifecycle maintenance as future work

This design therefore solves initialization, not the complete runtime-meta lifecycle.

## 7. Ensure semantics

The first rollout uses the existing store primitives and implements ensure semantics at the coordinator level:

1. try read
2. on miss, derive a candidate meta
3. write through `UpsertChannelRuntimeMeta()`
4. re-read authoritative meta
5. return the authoritative result

Important rule:

- callers must always trust the post-write authoritative read, not the locally generated candidate

This is intentionally weaker than a dedicated create-if-absent/CAS primitive, but is sufficient for the first rollout and keeps the interface small.

## Failure Semantics

### 1. Retry model stays the same

The send path continues to do at most:

- one refresh attempt
- one append retry after refresh

Bootstrap happens inside that single refresh attempt.

### 2. Retryable bootstrap failures

The following failures are treated as retryable send-time failures:

- slot leader not available for the channel's slot
- no replica peers available for the channel's slot
- hash-slot ownership still unstable

These should return the existing retry-oriented error path rather than fabricate partial metadata.

### 3. Hard bootstrap failures

The following fail immediately:

- generated initial metadata is invalid
- authoritative runtime-meta write fails
- write succeeds but authoritative re-read fails
- local `ApplyMeta()` fails after successful ensure and reload

### 4. Bootstrap does not force current-node write ownership

Bootstrap is not responsible for making the current node the leader.

If the derived slot leader is some other node, then after bootstrap the append retry may still return `ErrNotLeader`. This is correct and preferable to writing conflicting leader assignments during concurrent first-send races.

## Config Changes

### New config field in `internal/app/config.go`

Add to `ClusterConfig`:

- `ChannelBootstrapDefaultMinISR int`

Defaulting and validation:

- default to `2`
- validate `> 0`
- clamp only when deriving bootstrap metadata, not at config parse time

### New config key in `cmd/wukongim/config.go`

Add parsing for:

- `WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR`

### Example config update

Update `wukongim.conf.example` to include:

```text
WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR=2
```

## File-Level Changes

### `internal/app/channelmeta.go`

- extend `channelMetaSync` dependencies so refresh can invoke bootstrap
- change `RefreshChannelMeta()` to perform `Get -> bootstrap on not found -> re-Get -> Apply`

### `internal/app/channelmeta_bootstrap.go`

New file responsible for:

- topology lookup
- candidate runtime-meta derivation
- bootstrap ensure flow
- bootstrap config/lease defaults

### `internal/app/build.go`

- wire the bootstrap coordinator into `channelMetaSync`

### `internal/app/config.go`

- add config field and defaults for bootstrap default `MinISR`

### `cmd/wukongim/config.go`

- parse new config key

### `wukongim.conf.example`

- document the new config item

## Logging

Add focused structured logs around bootstrap:

- `channel runtime meta missing, bootstrapping`
- `channel runtime meta bootstrapped`
- `channel runtime meta bootstrap failed`

Recommended fields:

- `event`
- `channelID`
- `channelType`
- `slotID`
- `leader`
- `replicaCount`
- `minISR`
- `created` when applicable

## Testing Plan

### Unit tests

### `internal/app/channelmeta_test.go`

Add coverage for:

- refresh returns existing meta without bootstrap
- refresh bootstraps on `metadb.ErrNotFound`
- authoritative post-bootstrap re-read is used instead of the local candidate
- bootstrap derives `Replicas`, `Leader`, and `ISR` from slot topology
- bootstrap `MinISR` defaults to `2`
- bootstrap `MinISR` clamps to `1` for single-node clusters
- bootstrap fails when `PeersForSlot` is empty
- bootstrap fails when `LeaderOf` fails
- bootstrap failure does not apply partial local metadata

### Message-path tests

### `internal/usecase/message/send_test.go`

Add coverage for:

- send succeeds when runtime metadata is initially missing but bootstrap succeeds
- send still returns current retry/not-leader behavior when bootstrap succeeds but current node is not the derived leader
- bootstrap path is channel-type agnostic

### Integration tests

### `internal/app/integration_test.go`

Add coverage for:

- single-node cluster first send succeeds without pre-seeded runtime metadata
- direct read paths still do not create runtime metadata implicitly

## Risks and Follow-Up Work

### 1. Lease lifecycle remains incomplete

Bootstrap assigns an initial lease, but this design does not yet define the full mechanism that keeps channel runtime metadata aligned with leader and lease evolution over time.

### 2. First rollout ensure semantics are not true create-if-absent

The initial implementation uses `read -> upsert -> re-read`. If concurrent first-send churn reveals instability, a stronger authoritative ensure primitive may be added later.

### 3. Read paths remain non-healing by design

This is intentional for the first rollout. If later experience shows some read paths should self-heal missing runtime metadata, they should opt into the explicit ensure path one by one.

## Decision Summary

The approved design is:

- all channel types require runtime metadata for durable send
- runtime-meta bootstrap does not depend on business channel-info existence
- bootstrap is explicit and limited to the send refresh path in the first rollout
- generic `GetChannelRuntimeMeta()` remains pure read
- initial placement derives from slot topology
- bootstrap default `MinISR` is configurable, default `2`
- effective initial `MinISR` automatically clamps to replica count, including `1` in a single-node cluster
