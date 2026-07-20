# internal/runtime/presence Flow

## Responsibility

`internal/runtime/presence` is the in-memory authority directory for hash slots currently led by this node. It stores virtual routes owned by gateway nodes and does not own real gateway sessions.

## Authority Fencing

Every mutation and lookup carries a `RouteTarget`. The directory accepts the
operation only when the target matches the installed authority identity:
`(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)`.

`BecomeAuthority` installs a fresh authority identity and clears previous
active, pending, owner-sequence, and expiry-index state for that hash slot.
Route-revision-only updates with the same Slot Raft identity retain the existing
slot object and its expiry index while advancing the observed target. In-flight
callers using the same Slot Raft identity are still accepted across revision-only
updates. `AuthorityEpoch` is local diagnostic metadata and is not a distributed
authority fence. `LoseAuthority` removes the complete hash-slot state, including
its expiry index, so stale callers receive `ErrNotLeader`.

`EndpointsByUIDs` validates one exact target once, holds the corresponding
shard read lock once, and returns routes in UID input order with deterministic
route ordering inside each UID. A stale target rejects the whole exact-target
group; callers isolate that error from groups carrying other targets.

## Register And Conflicts

Routes without conflicts become active immediately. Conflicting routes are
stored in the pending index and return a `PendingRouteToken` plus `RouteAction`
values. Active conflicting routes stay visible until `CommitRoute` promotes the
pending route; `AbortRoute` deletes only the pending candidate. Commit removes
only the conflicts acknowledged when the pending route was created and rejects
new unacknowledged conflicts with `ErrRouteNotReady`.

## Owner Sequence Fencing

The directory stores the last owner sequence and explicit unregister tombstone
per exact route identity. `UnregisterRoute` records a tombstone and removes only
that identity when the tombstone is at least as new as the active route.
Register rejects routes whose `OwnerSeq` is at or behind the stored tombstone.
Touch ignores routes that are older than the latest owner sequence or at or
behind the explicit unregister tombstone.

## Touch And TTL

`TouchRoutes` validates the same `RouteTarget` fence as other mutations, then
refreshes exact active routes or recreates missing non-conflicting routes from
owner activity. Touch does not create pending candidates or return owner
actions; conflicting missing routes are ignored so live registration remains
the only conflict-resolution path.

Each active route carries `LastSeenUnix`, initialized from `ConnectedUnix` when
registration omits it. Each authority slot owns a min-heap of non-empty activity-
second buckets, a second-to-bucket lookup, and an exact identity-to-bucket lookup.
An indexed route identity belongs to exactly one bucket; touch and replacement
remove the old membership before scheduling the final route, and removing the
last identity removes the bucket from the heap. Routes with no activity time are
kept active but are not indexed.

`ExpireRoutesDetailed` checks only the oldest bucket in each authority slot and
pops buckets whose activity second plus TTL is strictly before the caller's
`now`. A deadline exactly equal to `now` remains active until a later pass.
With `S` directory shards, `A` authority slots, `B_due` due buckets, and `R_due`
routes in those buckets, a pass costs
`O(S + A + B_due log B + R_due)` instead of scanning all active routes; index
memory is `O(R + B)`. `ExpireRoutes` is the compatibility wrapper that returns
only the detailed result's expired count.

TTL expiry removes active and index membership without changing `ownerSeq` or
`tombstoneSeq` and does not create a tombstone. A delayed touch below the last
owner sequence therefore remains fenced after expiry. An equal owner sequence
heartbeat from the still-active owner, or a higher owner sequence touch, may
recreate the non-conflicting route; an explicit unregister tombstone continues
to reject activity at or behind its owner sequence.

## Diagnostics

`Snapshot` counts active authority routes by hash slot, reports cumulative
target-fenced touch entries and TTL-expiry counters, and exposes bounded aggregate
expiry-index route and bucket counts. It is used by bench diagnostics only; the
directory still stores virtual owner routes, not concrete TCP sessions.
