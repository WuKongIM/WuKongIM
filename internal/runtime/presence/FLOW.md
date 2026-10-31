---
scope: package
summary: Maintains hash-slot-authoritative routes, committed generation completeness, exact activation receipts, conflict staging and bounded TTL uncertainty.
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
- The complete-slot protocol additionally carries a committed `GenerationClaim`,
  exact owner boot/cut vector, ordered bounded pages and terminal owner receipts.
  This runtime checks shape and identity; cluster adapters prove the claim is
  committed/current and revoke old admission before publishing a changed gate.

## Main Flows

1. `BecomeAuthority` installs or advances exact authority state; a new Slot
   identity clears routes and indexes, while a revision-only update retains
   them. `LoseAuthority` removes all state for that Hash Slot.
2. Registration activates conflict-free routes or stages a pending candidate;
   commit removes only acknowledged conflicts, while abort removes only the
   candidate. Owner sequences and unregister tombstones fence stale activity.
3. Production lookups require bounded per-UID owner reconstruction after authority
   changes; exact-target completion retains tombstones and caches both present
   and absent UID proofs in a bounded FIFO. New lookups require a new proof after eviction.
   A bounded request readback may retain initially known proof across FIFO eviction,
   but initially missing UIDs still need current proof. A separate nonzero generation
   marker rejects replaced local authority instances, including identical-fence ABA.
   Readback copies current routes; it never retains an old route table or snapshot.
4. Touch refreshes or safely recreates exact active routes; TTL expiry pops due
   activity buckets from per-authority indexes without scanning all routes.
5. Full recovery uses `BeginGeneration` → bounded `InstallSlotPage` → every
   `EndGenerationOwner` → atomic `CompleteGeneration`. No pending activation,
   legacy UID proof, missing page or failed owner can manufacture completeness.
   The current complete slot answers unseen offline UIDs without FIFO entries.
6. Strict Register/Commit produce `ActivationReceipt` in the same critical
   section as the actual final route mutation. Pending results carry the exact
   `PendingClaim`, not a receipt. Old Register/Commit/Abort/Touch cannot bypass
   the Full protocol; exact unregister identity/sequence remains tombstoned.
   A well-shaped owner/boot mismatch atomically revokes Complete before returning
   its typed error. Boot IDs have equality only; request data cannot replace the
   vector. Malformed routes and stale claims do not revoke a current certificate.
7. Full TTL expiry removes delivery routes but retains known identities as
   suspect. Only exact close, newer real activity, or a complete all-owner UID
   recheck discharges uncertainty. Equal old activity never renews TTL. A shared
   nonwaiting 8,192 identity / 128 MiB metadata ledger bounds suspects; overflow
   revokes the affected slot before releasing uncertainty. Unrelated UIDs do
   not wait on a suspect UID. Legacy TTL expiry invalidates its UID proof too.
   Deletes refund live rows and string lengths; each generation retains the
   original 256-byte logical structure allowance at its table high water until
   both tables are retired. Partial tables can therefore conservatively exhaust
   the same byte limit. This is not an exact Go allocator or RSS measurement.

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
- Generation high water survives local Lose/Become; aborted/rebuilt generations
  cannot be reused. Distributed restart/transfer ordering still requires the
  committed claim provider, not this in-memory high water.
- Staging row/string credits are retained by the usecase until Complete/Abort.
  Exact Abort also drops an unfinished scan already closed by overflow; a
  generation that was ever Complete and any successor remain protected.
  Final Directory maps and allocator overhead are outside the decoded-result
  ledger. The runtime accepts at most 256 obligated owners and 4,096 rows/page;
  oversized vectors/pages fail closed, never drop required owners.
- Diagnostics expose bounded aggregate counters and index sizes only.

## Read First

- [Directory](directory.go)
- [Presence types](types.go)
- [Expiry index](expiry_index.go)
- [Full generation state](generation.go)
- [Claim, receipt and page contracts](generation_types.go)

## Update Triggers

Update this file when authority identity, lookup alignment, conflict staging,
owner fencing, touch recreation, TTL indexes, or diagnostics change.
