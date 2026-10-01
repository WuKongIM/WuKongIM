# Darwin full-sync scheduling candidate — not qualified

Issue #977 remains open. The scheduler-aware candidate preserves full-sync semantics and passes the native storage contracts, but the unchanged original comparison still rejects **6/12 sequential rows**. All 12 burst rows and all 24 placement fixtures pass their functional controls. This is a retained failed candidate, not a verified repair or capacity qualification.

## Frozen experiment

- Delivery applies only this Darwin engine adapter and its tests/docs to protected-main source `623091def1951fea6746bd9ac3b63a8a9ba30208`. The full engine package and Go lockfiles match the measured candidate byte for byte. Permission/coalescing changes remain in Draft #981; the measurements below belong to that candidate plus the adapter, not the minimal main delivery branch.
- Old product: `424d03eb298b972ec572261d617972eecb5523c5`; candidate: `c03fa6cc1712e886d9359822e23be716fdb4534e` (parent `ec21c1a586941ffd74cfb497f0da4522941bc1a4`).
- Driver: `2a4a1f4e57a8cb3d5586f10bb1585717c37ea0ab`; all seven recorded harness-source hashes and the acceptance verifier match the original experiment byte for byte.
- Both native products use Go 1.25.11, CGO enabled, clean exact-worktree VCS stamps. The deleted original products were rebuilt, so their binary hashes are new; the calibrated native CPU probe is byte-identical to the original.
- Fixed order: old/new × three predetermined pairs, each with all four placements. Three real processes, GOMAXPROCS=4 for nodes/driver, 256 Hash Slots, 12 physical Slots, one metadata voter, three message voters, 32 devices, 64 sequential and 64 burst SENDs per placement. Full warm/ban/unban prefix and exact 161-message history remain mandatory.
- Each row must have both p99 and whole-node user+system CPU change ≤5%. With 64 SENDs, p99 equals max. CPU includes background work, ownership sampling and cut overhead. No profiles, request timelines, extra histogram flags, gap injection, subtraction or failed-window replacement.
- Shared Darwin host and one-voter metadata limit generalization. This is not the unchanged four-CPU Linux AMD64 500-SEND/s qualification. Linux engine cross-compilation passes; no fresh Linux capacity result is claimed.

## Sequential results

| Pair | Placement | p99 old → candidate (ms) | p99 change | CPU old → candidate (ms) | CPU change | Verdict |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| 1 | same-slot-remote | 26.279 → 23.705 | -9.79% | 396.575 → 368.289 | -7.13% | pass |
| 1 | two-slots-one-remote-leader | 28.948 → 40.008 | +38.21% | 418.847 → 397.432 | -5.11% | reject |
| 1 | two-remote-leaders | 21.150 → 31.883 | +50.75% | 399.593 → 495.583 | +24.02% | reject |
| 1 | two-slots-local-leader | 21.962 → 18.070 | -17.72% | 453.216 → 348.375 | -23.13% | pass |
| 2 | same-slot-remote | 28.564 → 25.073 | -12.22% | 409.921 → 498.007 | +21.49% | reject |
| 2 | two-slots-one-remote-leader | 27.941 → 40.145 | +43.68% | 344.302 → 451.946 | +31.26% | reject |
| 2 | two-remote-leaders | 18.101 → 18.813 | +3.93% | 362.213 → 350.397 | -3.26% | pass |
| 2 | two-slots-local-leader | 24.010 → 20.088 | -16.33% | 501.240 → 415.470 | -17.11% | pass |
| 3 | same-slot-remote | 34.125 → 28.186 | -17.40% | 414.845 → 513.462 | +23.77% | reject |
| 3 | two-slots-one-remote-leader | 28.389 → 36.186 | +27.46% | 420.100 → 457.522 | +8.91% | reject |
| 3 | two-remote-leaders | 20.719 → 19.823 | -4.32% | 393.736 → 351.758 | -10.66% | pass |
| 3 | two-slots-local-leader | 24.167 → 23.868 | -1.24% | 478.422 → 392.891 | -17.88% | pass |

All three two-Slot/one-remote-Leader sequential rows fail p99 (candidate 36.186–40.145 ms). Two same-Slot/remote rows fail CPU despite lower p99; the first two-remote-Leader row fails both. All 12 burst comparisons pass both limits. Counts, process-start identities, raw Mach ticks/timebase, exact ACKs, public ban/unban decisions, placement and complete histories are checked before the performance verdict.

## Implementation and failure contracts

- The Darwin-only Pebble FS adapter wraps native Create/Open/OpenReadWrite/ReuseForWrite/OpenDir results. `Sync`, `SyncData` and `SyncTo` preserve upstream Darwin full-file sync and fullSync/error behavior; all other file operations remain delegated.
- `x/sys/unix.FcntlInt(F_FULLFSYNC, 0)` enters the runtime syscall boundary. Only ENOTSUP permits fsync fallback; EINTR retries the entire operation, including an interrupted fallback. I/O, permission, invalid-command and descriptor errors cannot silently downgrade durability.
- `RawConn.Control` pins descriptors through the complete operation, including concurrent Close. Closed-file acquisition errors preserve public `os.ErrClosed` and sync PathError identity. Directory handles do not rely on Pebble’s InvalidFd. Memory files keep their own sync implementation.
- The adapter sits below one owned disk-health wrapper, using the existing default 5s or explicit threshold; Open failure and Close release it. Other platforms keep Pebble’s upstream FS defaults. The existing Darwin 16-MiB BytesPerSync, coordinator, ordered publication and permission paths are unchanged.
- All new isolated failure cases were written before implementation. Initial compilation fails on the missing adapter; the original performance loop was already red. Native file/directory sync, forced descriptor-close overlap, default/custom health ownership and synced commit/reopen pass under race detection. All directly related `pkg/db/...` race tests pass.
- Ten injected syscall cases and four connection/control/error cases pass. Actual unsupported mounts, kernel EINTR and power loss were not exercised; injected errno contracts and reopened state do not establish power-loss survival.
- The first native integration run incorrectly expected RawConn’s private closing error to match public os.ErrClosed. That raw-only assertion was corrected; the public adapter still must return os.ErrClosed. The failed log is retained. FLOW check initially rejects a stale generated index; regeneration and the named check pass (13 existing length warnings).

## Provenance correction and retained evidence

Artifacts: [selected replay bundle](2026-10-01-permission-darwin-sync-candidate.tar.gz) and [archive hashes](2026-10-01-permission-darwin-sync-archive.json).

The first restored driver (`c0e95b5a45babbd1907c590a9dcdab83e74634a9`) differed from the original in two disabled diagnostic paths. An outer shell continued after its hash assertion failed, so six pairs ran with that driver. They are retained as **auxiliary only** (6 sequential rejects, 12 burst passes), with their original plan, binary identity, command log and full reports. They cannot substitute for the exact-driver run. The corrected driver was found by content hashes, then a new complete fixed plan ran once with unchanged candidate and thresholds. No failed window was replaced.

- The selected archive contains all six exact-driver reports, commands, full verdict, unchanged verifier, source copies, instruction digests, checks and auxiliary receipts. The larger local bundle additionally keeps both drivers’ raw reports and native binaries.
- `python3 verify-reviewed-pairs.py` intentionally exits 1 for the six performance rejects. `python3 verify-bundle.py /absolute/archive.tar.gz` checks every member and independently reproduces that exact rejected verdict without the workspace or module cache.
- Six corrupted-artifact controls (execution, revision, CPU ticks, history, profiles and counts) fail assertions independently of the known performance failure. The original historical archive remains unchanged at SHA-256 `2eb068e94ce358e333bd43691d546895449a8cb907c1e12bb94f9b0b1e219c5f`.

## Decision

The missing Darwin syscall boundary is established by the prior visibility probe and corrected at this seam, but it is insufficient to explain or repair the permission candidate’s regression. Retain this change as a Draft candidate. The next diagnosis should target the still-rejected two-Slot/one-remote-Leader sequential commit cadence and independently account for the CPU-only same-Slot failures, using the fixed complete prefix and bounded request-correlated profiles. Do not infer CPU from runtime Running state or weaken fsync semantics, acceptance, replicas or workload.
