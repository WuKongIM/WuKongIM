---
scope: package
summary: Maintains hash-slot-authoritative virtual connection routes with exact fencing, conflict staging, touch, and TTL expiry.
---

# Presence Authority Runtime Flow

## Responsibility

This package is the in-memory route authority for hash slots currently led by
this node. It stores virtual gateway-owner routes, never concrete sessions.

## Boundaries

- Every operation carries an exact `RouteTarget`: Hash Slot, physical Slot,
  leader node, leader term, and configuration epoch.
- `AuthorityEpoch` is node-local diagnostic metadata, not a distributed fence.
- Connection ownership and concrete session handles live in the online runtime.

## Main Flows

1. `BecomeAuthority` installs or advances exact authority state; a new Slot
   identity clears routes and indexes, while a revision-only update retains
   them. `LoseAuthority` removes all state for that Hash Slot.
2. Registration activates conflict-free routes or stages a pending candidate;
   commit removes only acknowledged conflicts, while abort removes only the
   candidate. Owner sequences and unregister tombstones fence stale activity.
3. Touch refreshes or safely recreates exact active routes; TTL expiry pops due
   activity buckets from per-authority indexes without scanning all routes.

## Invariants and Failure Semantics

- Exact-target lookup rejects a stale group while preserving aligned successful
  sibling groups; returned route slices are copied immutable snapshots.
- Active conflicts remain visible until pending commit; new unacknowledged
  conflicts make commit fail.
- Unregister removes only its exact identity and fences register/touch at or
  behind the tombstone owner sequence.
- Expiry removes route and index membership but creates no tombstone; a valid
  current-owner heartbeat may recreate a non-conflicting route.
- A deadline equal to `now` remains active until a later expiry pass.
- Diagnostics expose bounded aggregate counters and index sizes only.

## Read First

- [Directory](directory.go)
- [Presence types](types.go)
- [Expiry index](expiry_index.go)

## Update Triggers

Update this file when authority identity, lookup alignment, conflict staging,
owner fencing, touch recreation, TTL indexes, or diagnostics change.
