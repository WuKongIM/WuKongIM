# Exact proposal WAL stages and sequential cadence

Issue #977 now has a verified proposal-to-WAL chain for the preselected ordinary `two-remote-leaders-c1-030` SEND on all three nodes. In the unpaced diagnostic pair, the old runtime finishes Leader-local sync last; the candidate finishes its foreground follower last. Both are dominated by measured WAL sync API intervals. Adding a fixed 2 ms caller gap does not establish an improvement. The original comparison still fails six sequential rows.

This is diagnostic evidence following [PR #991](https://github.com/WuKongIM/WuKongIM/pull/991), not a product fix or a replacement qualification.

## Frozen sources and inputs

- Old parent: `424d03eb298b972ec572261d617972eecb5523c5`; isolated instrumented source: `69015d536b0ba3d4ee88059963f33d706f8c7823`.
- Candidate parent: `ec21c1a586941ffd74cfb497f0da4522941bc1a4`; isolated instrumented source: `8ab25b60817671da3871c0c9f155454f5f2dc230`.
- Driver parent: `c0e95b5a45babbd1907c590a9dcdab83e74634a9`; isolated cadence driver: `b4edb947210a243a73b070cae899f86a6b56b8d5`.
- Both runtime patches are byte-identical; Go 1.25.11 builds identify their exact clean source. The original candidate and baseline branches remain unchanged. A private copied Pebble v2.1.4 module carries the same five-file diagnostic delta for both builds; cached Go and Pebble ZIPs pass canonical module Hash1 verification. No shared dependency-cache file was edited.
- Each fixture retains the complete four-placement prefix, 64 sequential and 64 burst requests per placement, 32 ban/32 unban controls, and exact 161-message histories. Three real node processes use 256 hash slots, 12 physical Slots, three message voters and one metadata voter; one-voter metadata is not an HA proof. Nodes and driver use `GOMAXPROCS=4` on a shared Darwin host.
- The fixed order is old/unpaced, candidate/unpaced, old/gap, candidate/gap. Each captures one four-second runtime trace on each node, with a six-second HTTP deadline and 16 MiB bound, and one bounded exact debug query per node. There were no fixture retries or replacement observations.

## Diagnostic contract before instrumentation

The contract first rejected a previously captured uninstrumented real-process trace. It requires identical CommandID, digest and range 31→32 across all nodes; sender/receiver request and wire priority alignment; storage submission/build/publication; physical batch identity and Pebble DB/sequence; the exact standalone WAL stream and record-end offset; a covering write and sync; and terminal sync waiter release. Missing/error/cap/unsafe cases fail rather than becoming zero cost. Ten mutations of real captured evidence are rejected by the verifier.

Four ranked hypotheses were registered before probes: local sync dominates; follower queue or sync dominates; cadence changes the wait distribution; receiver scheduling dominates. The same instrumentation is used in both versions, only while runtime tracing is enabled. Bounded helper counters and complete observed custom-event counts are retained; the exact debug selector is fixed to one synthetic fixture request, never a metric label.

The original validator incorrectly used mutation-class ordinals for wire priority. Source `ExchangePriorityForeground` is 0 and background is 1. The corrected verifier uses those wire values; its original source/hash and all four initial failed verdicts are retained. This changes no runtime, capture, target or timing. Store begin/end events reuse a numeric `priority` field for mutation class (Leader=0, foreground follower=1, trailing=2); wire tables and checks use only peer/receive events, while the recorded commit lane independently identifies storage class.

## Raw diagnostic timings

p99 is the maximum of only 64 sequential observations. CPU is the unadjusted native whole-node sum and includes background work, profiling, the fixed ownership sampler and window boundaries. These numbers cannot pass or waive the original 5% comparison or the Linux500 gate.

| Fixture | Target SEND→ACK ms | Window p99 ms | Three-node CPU ms | Window wall ms | Last required observed completion |
| --- | ---: | ---: | ---: | ---: | --- |
| old | 11.783 | 16.041 | 347.791 | 736 | leader_local |
| new | 16.000 | 24.465 | 497.663 | 1105 | foreground_peer |
| old-gap | 9.560 | 18.183 | 430.554 | 928 | foreground_peer |
| new-gap | 18.967 | 38.153 | 600.828 | 1126 | leader_local |

The gap is inserted after each completed ACK and before the next SEND, only in the `two-remote-leaders-c1` window: 63 requested 2 ms timers. Earlier placements, bursts and policy controls are unchanged. Raw request latency excludes this caller gap; window CPU/wall time includes it. Completed loops and the frozen source/plan establish timer execution; the retained wall-minus-SEND-latency residual can accommodate all 126 ms. Client per-gap timestamps are absent because client timeline mode was disabled equally in every run; the residual includes timer, loop and scheduling overhead, with millisecond window rounding, and is not a measured per-gap distribution.

## Exact physical durability lineage

| Fixture | Node / role | Before build ms | Physical commit ms | Covering Write API ms | Covering Sync API ms |
| --- | --- | ---: | ---: | ---: | ---: |
| old | 1 / Leader | 0.576 | 7.664 | 0.012 | 7.633 |
| old | 2 / foreground follower | 0.593 | 3.646 | 0.011 | 3.619 |
| old | 3 / background follower | 0.654 | 11.178 | 3.330 | 7.825 |
| new | 1 / Leader | 0.599 | 7.636 | 0.018 | 7.577 |
| new | 2 / foreground follower | 0.560 | 11.661 | 0.009 | 11.630 |
| new | 3 / background follower | 0.652 | 15.169 | 3.402 | 11.747 |
| old-gap | 1 / Leader | 0.603 | 5.371 | 1.274 | 4.069 |
| old-gap | 2 / foreground follower | 0.646 | 5.369 | 1.272 | 4.068 |
| old-gap | 3 / background follower | 0.538 | 11.954 | 3.427 | 8.499 |
| new-gap | 1 / Leader | 0.654 | 8.861 | 0.928 | 7.906 |
| new-gap | 2 / foreground follower | 0.590 | 5.058 | 0.774 | 4.236 |
| new-gap | 3 / background follower | 0.717 | 4.114 | 0.032 | 4.014 |

Before-build time includes coordinator admission/queue/collection and does not isolate those components. The node-local replication queue and sender peer queue have separate exact proposal events; per-node phase pairs use one runtime clock. Across the twelve physical commits, recorded WAL rotation and WAL allocation-queue waits are zero. The Sync API measurement encloses `w.s.Sync()` and includes its wrappers, syscall, scheduling and logging overhead; it is not certified exclusive kernel fsync time. Pebble commit wait also gates ordered publication, so the larger commit/publish wait must not be relabeled wholly as sync.

The physical group ID is attached by the target storage Build closure to its exact engine Batch. Commit completion supplies its Pebble DB and sequence. The private Pebble hook joins that DB/sequence to the exact standalone LogWriter stream and returned record-end offset. Actual byte counts from Write advance a flusher-owned written frontier; the covering Sync begins at or beyond the target record end, succeeds and releases waiters before the physical commit returns. This is an identity/byte-coverage proof, rather than temporal association with a neighboring proposal.

For the unpaced pair, node 2 receives the exact target as wire foreground 0; node 3 receives it as background 1 after the required local/foreground completions. The background sender queue is about 81–100 ms, but it begins after this request has its required evidence. Its slower store/write/sync work therefore cannot be counted as a required quorum dependency for this target. Later requests and whole-node CPU may include background activity; this run does not establish its effect on those requests.

Required foreground sender queue waits and exact critical-interval runnable spans are small compared with the measured Sync API intervals. This supports storage sync as the dominant observed durability wait for these selected requests. Pre-handler transport/receiver queue and parts of ingress SEND→ACK remain unallocated; do not label their residual as network or scheduling without an additional identity hook. Runtime clock snapshot spreads are retained, not asserted as certified cross-node clock-error bounds.

## Cadence result and remaining work

The candidate gap window has higher raw p99 and whole-node CPU than its unpaced diagnostic window. The old gap window also has higher raw p99/CPU, despite a lower selected request ACK. Thus this single quartet does not support a stable benefit from caller spacing. It is not a repeated comparative benchmark and cannot prove why the original CPU or p99 regression occurs.

The next useful isolated probe is the underlying WAL Sync implementation and matched OS I/O evidence on the original Linux qualification topology, keeping synchronous durability unchanged. Any subsequent product repair must pass the original paired p99/whole-node CPU comparison and exact-source Linux500 gate; this diagnosis does not authorize their replacement, background-cost subtraction, or reduced workload.

## Validation and reproducible artifacts

- Both isolated runtime copies pass the directly related replication, engine and message DB test packages. All four real-process fixtures pass all four placements, exact histories and policy controls; all twelve target lineages pass the corrected full event contract.
- The declared incomplete, identity, priority, write/sync error, release, cap and unsafe-field mutations remain rejected. The original sequential archive is extracted into a fresh directory and its unchanged verifier still exits 1 with six failed sequential rows; the burst rows remain passed.
- A first build attempt failed for insufficient local disk before any fixture. Its logs remain. Cleanup touched only task-owned reproducible decoded texts and duplicate toolchain extraction; original raw traces and sealed archives remained unchanged. Both binaries were then built and all checks completed before live fixtures.
- No runtime-trace decoding overlapped a live fixture. Full and selected archives round-trip byte-identically and their offline verifiers pass in separate extraction directories. A second independent replay of all 12 raw traces reproduces every custom-event file, bounded scheduling record and summary byte-for-byte.

The tracked [selected archive](2026-10-01-permission-proposal-wal-diagnostics.tar.gz) contains positive exact proposal/physical/WAL events, source deltas, complete E2E reports, bounded scheduling evidence, prior failed verifier outcomes and validation receipts. It omits raw traces and full custom-event streams; it cannot independently prove complete-capture coverage or cap-marker absence. The full artifact contains all 12 raw traces and full custom logs. Neither archive includes runtime binaries, the whole copied module, or full expanded trace text; binaries are hash/source-bound and text decoding is repeatable from raw traces.

Archive sizes, hashes and source bindings are recorded in the [archive index](2026-10-01-permission-proposal-wal-archive.json). The full local artifact is `tmp/issue-977-proposal-wal-20261001/permission-proposal-wal-full.tar.gz` (6015743 bytes, SHA-256 `52b7bb9193380659e6217c1c6642f6d2f7ee700cb82af10350608a3611da9803`). Preserve it with the original evidence; the Git artifact is deliberately selected evidence.

Offline: extract the selected archive into an empty directory and run `python3 verify-proposal-evidence.py --selected`; extract the full artifact and run `python3 verify-proposal-evidence.py`. For an independent raw trace replay, copy the 12 raw traces plus verifier/decoder/summarizer scripts and required plan/report/contract/source receipts to a fresh directory, run `decode-proposal-trace.py LABEL` for each fixed label, then the per-label contract verifier and summarizer; compare derived event, scheduling and summary hashes to the retained receipts. Never overwrite a recorded run.

This PR changes reports and repository knowledge only. The isolated diagnostic branches are intentionally retained, unmerged and not deployment candidates. There is no user-visible product behavior change and no release claim; `skip-changelog` applies.
