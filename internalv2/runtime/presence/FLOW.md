# internalv2/runtime/presence Flow

## Responsibility

`internalv2/runtime/presence` is the in-memory authority directory for hash slots currently led by this node. It stores virtual routes owned by gateway nodes and does not own real gateway sessions.

## Authority Fencing

Every mutation and lookup carries a `RouteTarget`. The directory accepts the operation only when the target exactly matches the installed `(HashSlot, SlotID, LeaderNodeID, RouteRevision, AuthorityEpoch)`.

`BecomeAuthority` installs a fresh epoch and clears previous active, pending, and owner-sequence state for that hash slot. `LoseAuthority` removes the hash-slot state so stale callers receive `ErrNotLeader`.

## Register And Conflicts

Routes without conflicts become active immediately. Conflicting routes are stored in the pending index and return a `PendingRouteToken` plus `RouteAction` values. Active conflicting routes stay visible until `CommitRoute` promotes the pending route; `AbortRoute` deletes only the pending candidate.

## Owner Sequence Fencing

The directory stores the last owner sequence per exact route identity. `UnregisterRoute` records a tombstone and removes only that identity when the tombstone is at least as new as the active route. Register and rehydrate reject routes whose `OwnerSeq` is older than the stored tombstone.

## Rehydrate

`RehydrateRoutes` reuses the same register/conflict path as live registration
and returns per-route accept or reject results. Conflicting rehydrate candidates
return the pending token so the app worker can apply owner actions and then
commit or abort the candidate. This package intentionally has no lease
heartbeat, digest mismatch protocol, or replay lease state.
