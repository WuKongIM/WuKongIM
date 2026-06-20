# internalv2/runtime/presence Flow

## Responsibility

`internalv2/runtime/presence` is the in-memory authority directory for hash slots currently led by this node. It stores virtual routes owned by gateway nodes and does not own real gateway sessions.

## Authority Fencing

Every mutation and lookup carries a `RouteTarget`. The directory accepts the
operation only when the target matches the installed authority identity:
`(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)`.

`BecomeAuthority` installs a fresh authority identity and clears previous
active, pending, and owner-sequence state for that hash slot. Route-revision-only
updates with the same Slot Raft identity preserve state while advancing the
observed target. In-flight callers using the same Slot Raft identity are still
accepted across revision-only updates. `AuthorityEpoch` is local diagnostic
metadata and is not a distributed authority fence. `LoseAuthority` removes the
hash-slot state so stale callers receive `ErrNotLeader`.

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
registration omits it. `ExpireRoutes` scans active routes and removes entries
whose last seen time plus TTL is before the caller-provided time. TTL expiry
does not write tombstones, so a later non-conflicting touch with a fresh owner
sequence may recreate the route.

## Diagnostics

`Snapshot` counts active authority routes by hash slot and reports cumulative
target-fenced touch entries and TTL-expiry counters. It is used by bench
diagnostics only; the directory still stores virtual owner routes, not concrete
TCP sessions.
