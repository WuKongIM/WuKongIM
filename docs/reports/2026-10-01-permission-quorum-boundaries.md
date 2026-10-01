# Actual quorum boundaries expose WAL rotation in the sequential tail

Issue #977 now has request-correlated evidence inside the submitted quorum task,
after the Darwin sync adapter. In this fixed diagnostic pair, each version's
slowest two-Slot same-remote-Leader SEND crosses a WAL rotation on the leader and
its required follower. The candidate's follower spends about 16.2 ms rotating,
then 8.47 ms syncing the current record. Terminal quorum delivery to the reactor
takes 0.027 ms. This explains that captured request's long task interval; it does
not establish the cause of all six original comparative failures or a repair.

## Fixed sources and experiment

| Role | Parent | Clean instrumented source |
| --- | --- | --- |
| Old product | `424d03eb298b972ec572261d617972eecb5523c5` | `e8545e8646adaf4a92e516e615d18a1403b6d71a` |
| Candidate with Darwin adapter | `c03fa6cc1712e886d9359822e23be716fdb4534e` | `3a185279618f89cf7d5d960eb9f68be1120a0f32` |
| Full-prefix driver | `2975d2106921e70f818117b563253038e116cb69` | `39f3f5633474dd4d72f34547ea947f97b3b3b59a` |

Both products receive a byte-identical observer patch. A private copied Pebble
v2.1.4 carries the same bounded WAL observers for both builds; no shared module
cache is changed. Go 1.25.11 Darwin/ARM64 builds retain exact source revisions,
clean-tree stamps and binary hashes. Applicable instructions and FLOW digests,
the source deltas and private-dependency bindings are retained.

The predeclared order is old, then candidate, with no live retry or replacement.
Each run preserves all four original placements in order, 32 devices, 256 Hash
Slots, 12 physical Slots, one metadata voter and three message voters. Each
placement completes warmup, 64 sequential SENDs, 64 burst SENDs, 32 denied SENDs,
32 unbanned SENDs and exactly 161 committed history entries. The existing 20-ms
ingress metrics sampler remains inside every original window.

Only the `two-slots-one-remote-leader` sequential window starts three public
four-second runtime trace requests. Each has a six-second/16-MiB bound and is
joined on failure. A fixed 150-ms headroom does not prove trace start; the
offline contract requires all 64 target proposals in all three captures. The
three node trace sizes are 661–1,091 KiB old and 672–1,078 KiB candidate. All six
are retained in the full local artifact. Diagnostic latency/CPU does not qualify
performance, and these instrumented binaries are kept on marked debug branches.

## Test-first contracts and complete lineage

The artifact contract rejected prior uninstrumented-boundary evidence before
the new observers. A new process-level coverage test first failed because the
old harness supplied no traces, after all four functional/history cases passed.
Its exact pre-format test source, executable hash, report and failure log remain.

Each of 128 SENDs binds its synthetic ordinal to one CommandID, digest and exact
range, actual local and distinct-voter proofs, terminal quorum callback, receipt,
worker result and reactor publication. Each of 384 node lineages also binds the
storage request to physical batch, DB/sequence, WAL stream and record-end
offset, covering write/sync and sync waiter release. Dynamic identities remain
trace fields, never metric labels; no raw sender, payload or token fields are
emitted. Observer caps are explicit and a cap marker rejects the artifact.

The first offline verifier rejected multiple exchanges for one proposal in both
versions. Exact request-ID joins show queued trailing replays and foreground
repair exchanges for the same command. Old ordinal 3 additionally records a
rejected nondurable third-voter completion before two successful proofs. The
corrected verifier retains every exchange and outcome, matches request ID/wire
class at both ends, and counts only valid distinct durable proofs toward quorum.
It preserves those initial failures; neither source nor capture was rerun.

Other retained verifier corrections account for a three-vote quorum result
permitted by the protocol, asynchronous WAL-marker ordering, Go's dual parsed
stacks, exact policy/topology integrity and JSON integer-map serialization. The
final artifact rejects 12 corrupt copies, including missing terminal/sync,
foreign identities, false local/quorum proof, unsafe fields, cap markers,
wrong exchange, wrong product and changed history. Primitive event hashes are
recomputed in structural mutations so rejection is not merely a checksum test.

## Observed critical path

The reporting rule selects the maximum ACK elapsed from all 64 target SENDs;
the complete per-request table remains in the artifact.

| Boundary, ms | Old ordinal 3 | Candidate ordinal 38 |
| --- | ---: | ---: |
| SEND to ACK | 29.757 | 28.998 |
| Reactor submission to publication | 26.563 | 25.692 |
| Worker admission wait | 0.019 | 0.012 |
| Actual durability round | 26.475 | 25.640 |
| Quorum terminal to reactor receipt | 0.041 | 0.027 |
| Reactor receipt to publication | 0.017 | 0.005 |
| Leader WAL rotation statistic | 13.351 | 16.222 |
| Required follower WAL rotation statistic | 17.181 | 16.197 |
| Leader current-record sync API | 7.666 | 3.843 |
| Required follower current-record sync API | 8.211 | 8.466 |

The actual leader is node 2; node 1 supplies the successful follower proof for
every target SEND. In the old run the last durable vote is follower/local for
37/27 requests; in the candidate it is follower/local for 48/16. Node 3's
trailing persistence can finish after quorum and is not a successful vote in
these recorded rounds. A late replica's sync must therefore not be substituted
for the critical quorum wait.

Across all 64 candidate requests, worker admission wait is at most 0.046 ms,
terminal-to-reactor receipt at most 0.052 ms, and reactor publication at most
0.017 ms. These captures do not show a long post-quorum result-publication gap.
They do not rule out such a gap in other workloads. Nested task, round, physical
commit and sync intervals are not added as independent elapsed costs.

## What rotation actually contains

Each node has one positive `WALRotationDuration` among the 64 exact proposals,
at the request selected as slowest in its version. Pebble `makeRoomForWrite`
times `rotateWAL`: close the old WAL, create/reuse the next WAL, then install the
new writer. `LogWriter.closeInternal` emits and synchronizes the EOF trailer;
`StandaloneManager.Create` synchronizes the directory after creating/reusing
the file. Memtable allocation is outside that rotation statistic.

Independent raw re-decoding identifies the exact physical committer goroutine
inside the pre-append interval. For the candidate slow request:

| Observed syscall interval, ms | Leader node 2 | Required follower node 1 |
| --- | ---: | ---: |
| Old-WAL close sync API marker | 8.462 | 4.101 |
| New-WAL `open` syscall state | 0.149 | 4.488 |
| Directory sync `fcntl` syscall state | 7.520 | 7.537 |

The sync syscall stacks reach `x/sys/unix.FcntlInt` through the Darwin adapter;
their exact callers distinguish old-WAL close from directory sync. Syscall
state duration is elapsed blocked/runtime state, not CPU consumption. Arguments
and fallback outcomes were not separately instrumented. The API markers include
observer/wrapper overhead; they are not power-loss certification or device-only
latency.

The complete candidate pre-append intervals contain about 16.15 ms of `Syscall`
on both required nodes. The corresponding old intervals largely appear
`Running`, as expected from the previously demonstrated Darwin trace blind
spot; that label is not evidence of CPU work. All 384 current-record sync
intervals and six selected rotation intervals have complete, non-overlapping
goroutine-state coverage. Trace-to-wall clock mappings remain within the fixed
2-ms spread bound; node-local phase conclusions do not require subtracting
different nodes' clocks.

The observation supports storage rotation plus required local/follower sync as
the dominant phases of these two captured tails. It does not isolate a cohort
regression, justify weakening durability, prove metrics-sampling causality, or
justify changing the original workload to hide a rotation. Pebble starts at a
256-KiB memtable before growing toward the configured limit; merely increasing
the configured 32-MiB limit is not a supported initial-size control here.

## Validation, CI and replay

Both real-process runs pass eight placement/policy/history cases. Existing
replication, worker, reactor and engine tests pass. Six owned node PIDs are no
longer live. Both sealed archives pass independent extraction, manifest and
lineage replay. The full archive additionally re-decodes all six immutable raw
traces twice through the pinned Go tool, reproducing the original decode hashes,
128-request derivations, sync states and rotation stacks. No product is executed
during offline replay.

The original six unprofiled reports and unchanged validator are included. A
fresh disposable replay still exits failed with six sequential rejections and
all twelve burst comparisons passing. Original p99/whole-node CPU gates and
their metrics-scrape overhead remain unchanged.

Automatic CI on [Draft #994](https://github.com/WuKongIM/WuKongIM/pull/994)
also records a first-attempt mixed-send failure: arrival window 2 completes
30,000 requests, zero errors/drops, but scheduled p99 is 545.55 ms. Windows 1/3
are 197.69/181.68 ms. Its merge-preview tree equals that exact documentation
head; it is a different product source from this instrumented candidate. The
[failed job](https://github.com/WuKongIM/WuKongIM/actions/runs/36844761166/job/110312425542)
log is retained without a retry or causal attribution. Other non-skipped
messaging/correctness/channel-append/tcp-sendack checks passed; aggregate
regression is failed. This is not a new fixed Linux paired qualification.

The [selected archive](2026-10-01-permission-quorum-boundaries.tar.xz) retains all
custom events, decoded state/stack evidence, full functional and original
comparative reports, failure receipts, verifiers and frozen source bindings.
It omits raw traces and cannot independently re-decode them. The larger sealed
artifact at
`/Users/tt/.codex/artifacts/issue-977-quorum-boundaries-20261001/quorum-boundaries-full-final.tar.gz`
contains all six raw captures. Exact sizes, hashes and replay receipts are in
the [archive index](2026-10-01-permission-quorum-boundaries-archive.json).

Extract either archive into a fresh directory and run `python3 verify-package.py`.
For independent raw replay of the full archive, run
`python3 verify-package.py --redecode --go /absolute/go1.25.11/bin/go`.
`python3 mutate-boundaries.py` repeats the twelve offline rejection checks.
No network, running cluster or Go compiler is needed for selected-mode replay.

The next repair experiment must preserve old-WAL EOF durability, directory
durability, exact proposal publication and restart recovery while addressing
rotation cost through a supported seam. Any precreation/recycling or ownership
change needs failure-first rotation/restart coverage and the unchanged three
original p99/whole-node CPU pairs. This delivery changes documentation and
diagnostic evidence only; it makes no product-fix, merge or release claim.
