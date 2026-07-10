# Channel Entry Lease Lifecycle Design

**Date:** 2026-07-11

## Status

Approved for implementation planning.

## Problem

Channel runtime eviction releases the reactor-owned store handle, but the
message DB keeps three process-lifetime channel-indexed objects:

- `MessageDB.logs` retains one `ChannelLog` per channel;
- `Engine.stores` retains one compatibility `ChannelStore` per channel;
- `MessageDBFactory.checkpointLocks` retains one checkpoint mutex per channel.

The adapter's `Close` method is currently a no-op. Memory therefore scales with
every channel observed since process start instead of the channels that still
have active users.

Deleting these entries directly during reactor eviction is unsafe.
`ChannelLog.appendMu` and its cached LEO are the single serialization point for
append, follower apply, truncate, and retention. The checkpoint mutex also
protects a read-modify-write update. Recreating either object while an older
borrower is still active can create two independent locks for the same durable
channel and allow sequence reuse or checkpoint regression.

Commit coordinator requests add another ownership boundary: a caller may
return after context cancellation while an already admitted request still
holds and later publishes through the channel object.

## Goals

1. Bound channel-indexed memory by currently referenced channel entries.
2. Preserve one canonical append/LEO/checkpoint synchronization state for a
   channel while any borrower remains active.
3. Release reactor, worker, read-helper, and background-commit references on
   every success, error, cancellation, eviction, and shutdown path.
4. Keep channel close independent from the shared Pebble engine lifecycle.
5. Make double close safe and reject operations through an already closed
   lease.
6. Expose low-cardinality entry and lease counts for tests and observability.

## Non-goals

- Changing durable message rows, keys, sequence semantics, or commit quorum.
- Closing Pebble when one channel is evicted.
- Adding an idle TTL, unbounded grace cache, or LRU in this change.
- Choosing a new default for `channel.max_channels`.
- Optimizing commit coordinator shard count.
- Reintroducing any legacy runtime package.

## Considered Approaches

### Delete cached objects during reactor eviction

This is rejected. Temporary readers and admitted commit requests may still
reference the old object. A later acquire could then create a second append
mutex, LEO cache, or checkpoint mutex for the same channel.

### Fixed striped locks and stateless channel handles

This bounds lock memory but forces repeated LEO recovery after handle creation
and introduces unrelated-channel lock collisions. Keeping a per-handle LEO
cache would reintroduce the split-state problem. This is not selected.

### Explicit channel-entry leases

This is selected. A registry retains one canonical mutable channel entry while
its reference count is positive. The last release removes only the registry's
strong reference; durable data and the shared engine remain open.

An idle LRU may be considered later only if metrics show that immediate
zero-reference reclamation causes material LEO reload cost. Such a cache must
have a hard entry limit.

## Ownership Model

`MessageDB` owns a registry keyed by `ChannelKey`:

```text
channelRegistry
  mutex
  closed
  entries[channelKey] -> channelEntry

channelEntry
  immutable channel key and channel ID
  canonical append mutex and cached LEO state
  canonical immutable append-key prefixes
  canonical checkpoint mutex
  positive lease count
```

The mutable fields currently stored directly on the cached `ChannelLog` move
behind the canonical entry. Each acquisition returns a distinct lightweight
lease handle that points to that entry and owns exactly one release token.

The last release performs compare-delete under the same registry mutex used by
acquire. The delete must verify the entry pointer or generation so an older
release cannot remove a newer canonical entry.

The registry does not keep zero-reference entries. Reacquiring a reclaimed
channel creates a new entry and reconstructs LEO from durable storage before
the next append. No old borrower can coexist with that new entry because
reclamation requires the old reference count to reach zero.

## API And Adapter Changes

- The typed message channel acquisition API returns a per-acquisition lease
  handle with an idempotent `Close` method.
- The compatibility `Engine.ForChannel` surface returns a lease-backed
  `ChannelStore` instead of a process-cached wrapper.
- `Engine.stores` is removed.
- `MessageDBFactory.checkpointLocks` is removed; the lease exposes the
  canonical entry checkpoint mutex to the adapter.
- `messageDBChannelStoreAdapter.Close` releases its lease exactly once.
- Methods called after adapter close return the existing closed-store error
  mapped to the channel error surface.
- Batch lock deduplication uses canonical entry identity rather than the
  per-acquisition wrapper pointer, so duplicate items for the same channel do
  not lock the same mutex twice.

All production callers that acquire a temporary handle must close it:

- Channel worker store tasks;
- Node committed-message and idempotency reads;
- routed last-visible reads;
- retention/catalog helpers;
- transfer/import and inspect paths;
- batch append/apply helpers.

Reactor loaded and pending channel state owns a lease for its complete runtime
lifetime. It releases that lease only after asynchronous store close finishes.

## Background Commit Pinning

Before a commit coordinator request captures a channel store, it retains an
additional internal lease. That pin is released only by the request's terminal
completion/finalization path after build, commit, and publish can no longer
run.

Caller cancellation may stop waiting but cannot release the coordinator pin.
Admission failure releases the pin immediately. Every coordinator branch must
have one terminal release, including build error, physical commit error,
publish error, coordinator close, and queue rejection.

## Shutdown

Channel reactor shutdown must close every loaded, loading, and pending store
handle after failing or draining its futures. Worker pools must finish or
cancel accepted store work before the factory closes the message engine.

Factory close proceeds in this order:

1. mark the registry closed and reject new acquisitions;
2. stop and drain the commit coordinator;
3. verify or wait until internal background pins are released;
4. detach remaining registry entries;
5. close the shared engine once.

Channel lease close never calls shared engine close.

## Observability

Expose a snapshot without channel identifiers:

- active canonical entries;
- total outstanding leases;
- background commit pins;
- acquire/release/reclaim totals;
- invalid double-release or use-after-close attempts in tests only.

Channel IDs and keys must not become metric labels.

## Testing

Tests must be written before implementation and prove:

1. the first close of two leases keeps the canonical entry alive;
2. the last close immediately removes the registry entry;
3. close is idempotent and post-close operations fail;
4. reacquire after reclamation restores the correct durable LEO and messages;
5. concurrent high/low checkpoint updates cannot regress HW;
6. context cancellation does not reclaim an entry while an admitted commit is
   still able to build or publish;
7. every worker success/error/cancellation branch releases temporary leases;
8. reactor eviction returns entry counts to the baseline and reload preserves
   LEO, HW, and data;
9. group close releases loaded and pending stores;
10. creating, closing, and collecting at least 100,000 distinct channel
    handles does not leave entry count proportional to historical channels.

Race verification covers `pkg/db/message`, `pkg/channel/store`,
`pkg/channel/worker`, and `pkg/channel/reactor`. Benchmarks compare steady
append throughput before and after the lease indirection and measure cold
reacquire cost separately.
