# Width16 tests-first preparation (not executed)

Gate: A must finish with a complete valid old-message_loss report and exact zero cleanup; root must separately authorize old RED and implementation. If A passes or final evidence is invalid, width comparison stops as inconclusive. No fixture has been compiled, formatted by Go, or executed during this preparation.

## Intended candidate contract, not current production

Cohorts16; distinct owned keys8192; adapter owner rounds16. Owned UID string bytes stay explicitly128MiB, page32MiB, page512 UIDs/256 targets. Owner250ms, plan5s, retry/backoff, all authority/boot/sequence fences, follower behavior and service13/14 CancelRunning=false remain unchanged. Owned bytes are not an RSS guarantee.

## Failure/control matrix frozen before test-source edits

1. Exported recovery + real Directory: sixteen distinct targets/pages must all reach controlled owner I/O. Current4 later must fail at positive entry5; do not mistake a compile failure for behavior RED. Both 1-UID and full512-UID pages exercise actual entry, not a constant mirror.
2. With16 held,17th two-UID request must reach the cancellation-aware quota wait without entering owners; cancel and join. Canceling a follower must not cancel its leader. Release one cohort and prove a fresh request enters; after all joins, no stale ownership blocks subsequent work.
3. Full pages retain8192 distinct keys. The overflow request waits without partial reservation; released capacity is reusable. This catches C16 with stale K2048.
4. Preserve the existing quota/proof-recheck/follower-cancel regression by changing only its number of held cohorts and corresponding17th target after this matrix. Install proof before releasing a held cohort; waiter must reread proof without a new owner call. Keep all old fences/coalescing/FIFO/cancellation/partial-proof tests intact.
5. Independent byte quota: four512-UID pages of65535-byte strings retain128MiB−2048; a new2048-byte UID must enter while cohort/key room remains; then one byte must wait and cancel. A mistakenly derived512MiB budget admits that byte and must fail. Release one held page; the byte then enters, proving wakeup and exact cleanup. Controlled owner returns are canceled rather than publishing massive routes.
6. Fast resource-contract guard checks unchanged128MiB and32MiB before any heavy integration execution; this supplements, never replaces, the real byte-admission case. No128MiB string is allocated during preparation. Root must admit memory headroom before later running this integration case: input+owned clones alone exceed256MiB; RSS/allocator/Directory/RPC data are outside the ledger.
7. Adapter independent gate: sixteen synchronous read hooks enter;17th never enters and cancels; release/cancel and join all, then a fresh call succeeds. Hook lifetime is controlled by the fixture parent/release, not a250ms child timeout that would rotate an old4 gate and hide the RED.

## Test tiers and future order

All timed/concurrent and real128MiB fixtures use integration build tags, finite watchdogs, buffered terminal channels and explicit cancellation/join on every exit. Unit source checks only the fixed memory contract. Existing page/packing/ownership tests remain. Future order after authorization: validate source/test hashes and memory headroom; fast budget guard; narrow old width behavior RED; preserve first result; only then root may approve production changes and identical-test GREEN. No production constants or deadlines are edited here.
