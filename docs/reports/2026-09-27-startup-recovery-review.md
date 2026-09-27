# Startup recovery pre-PR review

Scope: the complete working-copy change (tracked and new files) based on
`64f73d99b3b0cb8960d40825f76053f5a0000dbd`, including the accepted bounded-recovery
and certified-restart plans. Standards and Spec were reviewed independently;
the primary reviewer also audited the physical seal and restart failure paths.

## Standards

One actionable scale-design finding was fixed: restoring one physical Slot
invalidated every other Slot's certificate, so default multi-Slot upgrades could
repeatedly restore snapshots. Known disjoint FSM writes and startup installation
now invalidate only their own certificate. Unclassified writes and mixed-group
vetoes remain global. The historical first-stage plan is explicitly labeled and
links to the final plan. No unresolved standards finding remains.

## Spec

Two findings were fixed:

1. **Multi-Slot fast restart was ineffective after an older-version upgrade.**
   The new twelve-Slot black-box regression initially observed zero reused
   checkpoints on the second restart, both with twelve snapshots and with eleven
   snapshots plus a snapshotless neighbor. Targeted invalidation, per-certificate
   epoch checking and explicit snapshotless FSM classification remove that
   cross-Slot invalidation cycle. Migration maintenance in any batch position,
   uncertain ownership and overlapping migration state retain global invalidation.
2. **A dormant certificate could reappear valid after an unaware writer.**
   Invalidating only the startup in-memory seal flag was insufficient once a
   different Slot resealed the database. A failing persistent regression proved
   the sequence across four opens. An invalid startup seal now synchronously
   deletes every old certificate before scoped writes can reseal, including
   certificates for Slots not opened in that process.

The fixes were reviewed again. No unresolved Spec finding or unrelated scope
expansion remains. Startup failure/cancellation preserves the pending marker;
ordinary runtime restoration stays atomic. No new persistent format or product
configuration was introduced by these review fixes.

## Validation evidence

Evidence is retained under `/tmp/wk-startup-followup` and summarized in the
[scale report](2026-09-27-startup-recovery.md) and its JSON companion. Every new
isolated regression was observed failing before its corresponding implementation.

- `review-multislot-red.log`, `review-upgrade-red.log`: reproduced multi-Slot
  invalidation at storage and public product boundaries.
- `review-ownership-red.log`, `review-startup-ownership-red.log`: reproduced the
  migration/ownership cases before adding conservative guards.
- `review-dormant-red.log`: reproduced stale dormant-certificate resurrection.
- `review-delivery-race.log`: final engine, metadata and FSM focused integration
  regressions with the race detector.
- `review-fixed-packages.log` and `review-delivery-packages.log`: affected DB,
  Raft-log, Slot, cluster and app packages, with the final metadata/FSM delta
  checked again.
- `review-final-e2e.log`: complete startup-recovery scenario package, including
  one/twelve Slot reuse, three-node full restart, unaware-writer fallback,
  fourth-node replica migration and twelve-Slot older-version upgrades.
- `review-delivery-e2e.log`: older-writer and both twelve-Slot upgrade cases
  repeated after the final dormant-certificate fix.
- `review-delivery-matrix/`: final Linux ARM64 binary, hard 2 GiB and four CPUs,
  three million users/devices, upgrade/reuse/interrupted-install cases; each
  successful case requires full inventory and 257 WKProto authentications.
- `review-final-flow-check.log`: canonical `flow-doc-contracts`; existing warnings
  are preserved. `git diff --check` is clean.

Intermediate E2E failures remain in `review-fixed-e2e.log`: concurrent load hit
the existing ten-second first-start deadline, HTTP readiness preceded gateway
listening, and a joined node started before its seed listeners. Explicit protocol
and seed readiness now fence the latter two cases; passing final runs, rather
than these failed attempts, constitute the regression evidence. The earlier
4 GiB, CPU profiling and ABBA write pilot remain evidence for their recorded
checkpoint-v2 binary, not remeasurements of the final review delta.

Summary: Standards 1 finding fixed, 0 open; Spec 2 findings fixed, 0 open.
The shared multi-Slot finding appears on both axes. This review authorizes no
merge or release; the next delivery is a pull request.
