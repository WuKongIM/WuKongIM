# Presence Expiry Index And Touch Drain Design

**Date:** 2026-07-11

## Status

Approved for implementation planning.

## Problem

`Directory.ExpireRoutes` currently acquires every presence shard's write lock
once per second and scans every active authority route. Register, unregister,
touch, and endpoint reads share these locks. At high online cardinality this
creates an `O(all local routes)` periodic CPU and lock-latency spike even when
no route is due.

The owner touch worker also drains at most 512 dirty routes per second by
default. With a 90-second TTL, that can refresh at most 46,080 different routes
before expiry. Optimizing the authority-side scan without increasing bounded
touch drain capacity would still expire live routes in a 100,000-user test.

## Goals

1. Make an expiry tick proportional to due time buckets and actually expired
   routes, not all active routes.
2. Keep Register and Touch index maintenance near constant time under the
   existing shard lock.
3. Preserve authority fencing, conflict resolution, owner-sequence fencing,
   tombstones, and exact TTL boundary behavior.
4. Increase owner touch drain capacity with explicit bounded work.
5. Resolve a touch batch against one routing snapshot instead of one UID lookup
   at a time.
6. Add low-cardinality expiry and touch-work observations.

## Non-goals

- Changing route conflict or device-level semantics.
- Changing hard authority identity fields.
- Replacing owner-provided wall-clock activity with authority receipt time.
- Deleting `ownerSeq` or `tombstoneSeq` during TTL expiry.
- Creating one goroutine or timer per route.
- Adding unbounded work to the one-second touch tick.
- Changing Presence RPC wire compatibility.

## Considered Approaches

### Indexed heap entry per route

Expiry is direct, but every heartbeat moves one heap node in `O(log routes)`
while holding the shard write lock. This moves the cost from expiry into the
higher-frequency touch path and is not selected.

### Fixed timing wheel

Updates and expiry can be constant time, but the directory would need to own
TTL and tick granularity and handle timer jumps, missed ticks, and dynamic TTL
changes. That is unnecessary complexity for second-resolution timestamps.

### Per-authority-slot second buckets plus a bucket heap

This is selected. Routes sharing `LastSeenUnix` share one bucket. Moving a
route between existing second buckets is constant time; heap work occurs only
when a second bucket is created or removed. The number of live buckets is
normally bounded by the activity window rather than route count.

## Expiry Data Structure

Each `authoritySlot` owns:

```text
expiryByKey[identityKey] -> expiryBucket
expiryBuckets[seenUnix]  -> expiryBucket
expiryQueue              -> min-heap ordered by seenUnix

expiryBucket
  seenUnix
  heapIndex
  keys[identityKey]
```

The index belongs to the authority slot rather than the directory shard:

- a route-revision-only update preserves the same slot and index;
- leader, term, config epoch, physical Slot, or authority loss replaces or
  deletes the complete slot and index in constant time from the shard map;
- no stale heap item from an old authority can affect a new authority.

## Mutation Semantics

All index mutations occur while holding the same shard write lock as the
active/byUID maps.

- Register or committed conflict replacement inserts the final active route
  into its normalized seen-time bucket.
- Touch moves an existing route from its old bucket to the new bucket after
  owner-sequence and tombstone checks. A recreated non-conflicting route is
  inserted normally.
- Unregister and conflict removal remove the identity from its bucket.
- Removing the last identity deletes the bucket from the map and heap.
- A route with both `LastSeenUnix == 0` and `ConnectedUnix == 0` is not indexed
  and remains non-expiring, matching current behavior.
- No touch appends a lazy duplicate heap record; index cardinality remains
  bounded by active route cardinality.

Expiry examines the heap root while:

```text
time.Unix(bucket.seenUnix, 0).Add(ttl).Before(now)
```

Equality is not expired. For each due bucket it removes only identities still
mapped to that exact bucket. TTL expiry removes active/byUID/index membership
but retains owner sequence and tombstone fences so a delayed stale owner event
cannot regain authority.

The expected tick complexity is:

```text
O(shards + authority slots + due buckets * log(bucket count) + expired routes)
```

## Touch Drain Capacity

`TouchBatchSize` remains the maximum routes processed as one routing/RPC
chunk, with the current default of 512.

Add `TouchMaxRoutesPerFlush`, defaulting to 65,536. Its configuration contract
is:

- TOML: `presence.touch_max_routes_per_flush`;
- environment: `WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH`;
- positive and at least `TouchBatchSize`;
- documented in `wukongim.toml.example` with detailed English comments.

One flush repeatedly drains `min(TouchBatchSize, remainingBudget)` until the
registry returns fewer routes than requested, the total budget is consumed, or
the worker context ends.

For each chunk, unique UIDs are passed through the cluster's `RouteKeys` batch
surface so all targets come from one installed routing snapshot. Returned
targets stay aligned with the input UIDs. Invalid or unresolved entries are
requeued individually. Routes are grouped by the full fenced target and sent
through the existing bounded Presence RPC collection contract.

The worker does not start unbounded goroutines. Target groups may remain
sequential in the first implementation; measurements must justify any future
bounded dispatch concurrency.

## Observability

Expose bounded observations without UID, session, or hash-slot labels:

- expiry duration and result;
- due buckets;
- candidate routes examined;
- routes expired;
- active expiry index routes and buckets;
- touch routes drained, resolved, sent, and requeued;
- touch chunks and target groups;
- flush duration and whether the maximum route budget was reached.

The bench snapshot retains existing active/touch/expired totals.

## Testing

Tests must be written before implementation and cover:

1. 100,000 fresh routes plus 10 due routes examine exactly the due candidates;
2. touching a route moves it out of its old expiry bucket;
3. unregister removes index membership but preserves tombstone behavior;
4. expire followed by a fresh non-conflicting touch can recreate a route;
5. equality at the TTL deadline is retained and the next instant expires;
6. revision-only authority updates preserve the index;
7. distributed authority identity replacement drops the old index;
8. conflict replacement removes old identities from their buckets;
9. a flush drains multiple chunks but never exceeds
   `TouchMaxRoutesPerFlush`;
10. batch route resolution preserves UID/result alignment and requeues only
    failed entries;
11. context cancellation requeues all drained but unsent routes.

Benchmarks cover fresh and due route sets at 10,000, 100,000, and 1,000,000
routes with allocation, mutex, and block profiles. A fresh tick must examine
zero routes and should grow by no more than 3x from 10,000 to 1,000,000 fresh
routes. The 100,000-user three-node presence benchmark runs longer than twice
the configured TTL and verifies route counts remain stable.
