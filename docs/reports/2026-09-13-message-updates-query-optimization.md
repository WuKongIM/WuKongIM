# Message update query CPU and allocation optimization

Status: completed on 2026-09-14 (Asia/Shanghai). This extends the
[three-node comparison](2026-09-13-message-updates-three-node-performance.md).
The feature and all changes remain uncommitted in `codex/message-updates`.

Follow-up: the [three-pair changed-delta CPU investigation](2026-09-14-message-update-delta-cpu.md)
did not reproduce the single-run +12.3% as a stable code regression. The original
observation below remains preserved; the repeat does not claim zero overhead.

## Evidence and changes

The prior repaired candidate used 109.82 CPU seconds per minute / 3.391 MB per
mixed request without edit rows, and 117.01 / 3.816 with every tail edited. The
exact feature-free baseline used 70.48 / 2.784. All are three-server totals,
measured under the same list 200 QPS + sync 60 QPS load.

Merged three-node profiles attributed approximately 8–9% of sampled server CPU
to metadata edit reads; edit-path JSON decoding accounted for approximately 8%
of the edited profile. The overlay join allocated many payload-bearing maps.
These cumulative profile percentages overlap; they must not be added.

Three bounded changes were isolated with tests and microbenchmarks:

1. **Pinned empty edit state.** A zero channel update sequence proves that no
   dependent latest/pending rows exist in the same atomic snapshot. Exact-ID
   lookups now stop there, retaining the fresh Slot ReadIndex and snapshot on
   every call. A test checks missing → initialized → edited state through the
   same query, including pending rows. No negative result is cached.
2. **Overlay matching.** Empty pages return immediately. Pages with at most
   eight updates use bounded direct matching; larger pages build ID-to-index
   maps. No payload-bearing structs are copied into maps. Tests preserve both
   channel and original sequence identity, repeated targets, growth limits,
   forward/reverse continuation and cross-channel batching.
3. **Sparse read JSON.** Internal read requests and pages omit default zero
   fields. Field names and format version remain unchanged; nonzero generation,
   replica proof, cursor, pending selector and uint64 values are preserved.
   Fully populated legacy messages still decode. Durable storage encodings and
   public HTTP response DTOs are unchanged.

The corresponding regression assertions failed before their changes. No quorum
barrier, restore epoch fence, retention check, request bound or CMD restriction
was removed or relaxed. Profiles did not identify payload byte copying as the
primary remaining cost, so this pass does not change buffer ownership.

## Local isolated measurements

Darwin ARM64 microbenchmarks, three repetitions, 200 ms per case; these isolate
functions and are not production throughput evidence.

| Case | Before | After |
| --- | ---: | ---: |
| No edit head, 200 IDs | ~69 μs; 85,339 B; 1,420 allocations | ~0.78 μs; 440 B; 20 allocations |
| No edit head, 3 IDs | ~1.80 μs; 1,714 B; 41 allocations | ~0.78 μs; 440 B; 20 allocations |
| Match 100 edited tails | ~17.6 μs; 112,105 B; 203 allocations | ~1.35 μs; 0 B; 0 allocations |
| Match 198 recents / 66 channels | ~13.0 μs; 75,657 B; 135 allocations | ~2.17 μs; 0 B; 0 allocations |
| Match 200 edits in one channel | ~8.35 μs; 32,856 B | ~4.19 μs; 4,960 B |
| JSON roundtrip, 16 unedited channels | ~36.7 μs; 4,134 wire B; 13,339 allocated B | ~13.1 μs; 1,019 wire B; 9,602 allocated B |
| JSON roundtrip, 16 edited channels | ~76.3 μs; 12,594 wire B | ~67.7 μs; 11,335 wire B |

Raw results, red/green logs and merged profiles are retained under
`tmp/message-update-optimization/`. The complete system comparison uses the
unchanged precompiled Linux harness, six assigned CPUs, 6 GiB, three processes,
256 Hash Slots, 12 physical Slots and three replicas. Each comparison retains
three unprofiled 60-second mixed windows and separate bounded profiling. The
final edited run additionally measures changed/empty delta pages at 100 QPS.

## Complete-system measurements

CPU is the mean three-server total per 60-second mixed window. Allocation uses
total bytes across those windows divided by successful requests across both
endpoints, in decimal MB. Each latency is the worst window percentile, not an
average of percentiles. Fixed offered rates are list 200 QPS and sync 60 QPS.

| Variant | List P95 / P99 ms | Sync P95 / P99 ms | CPU seconds / minute | Allocated MB / request |
| --- | ---: | ---: | ---: | ---: |
| Feature-free baseline | 16.87 / 19.42 | 29.99 / 34.07 | 70.48 | 2.784 |
| Before this pass, unedited | 16.71 / 19.41 | 32.43 / 35.95 | 109.82 | 3.391 |
| Empty-state and overlay changes, unedited | 16.33 / 18.66 | 31.86 / 34.59 | 101.67 | 3.308 |
| All three changes, unedited | 16.24 / 18.37 | 31.73 / 34.36 | 97.58 | 3.259 |
| Before this pass, all 600 tails edited | 16.69 / 19.81 | 32.68 / 36.16 | 117.01 | 3.816 |
| All three changes, all 600 tails edited | 16.82 / 19.55 | 32.25 / 35.23 | 109.25 | 3.666 |

The final unedited run reduced CPU by 11.1% and allocation by 3.9% relative to
the preceding feature candidate. Relative to the feature-free baseline it still
uses 38.5% more CPU and 17.1% more allocated bytes. Microbenchmark improvements
do not translate into equivalent whole-system reductions. All three windows
passed with zero errors, drops, channel-runtime loads, residency or membership
writes. Minimum achieved rates were 199.967 and 59.983 QPS.

With every tail edited, CPU fell 6.6% and allocation fell 3.9%. The same workload
still costs 55.0% more CPU and 31.7% more allocation than the feature-free
baseline. These three windows also had no errors, drops, runtime loads,
residency or membership writes, with the same minimum achieved rates. The small
latency differences are observations from this fixed-load diagnostic, not proof
of a production latency improvement. Earlier tail variability and incomplete
collections remain in the preceding report.

The independent merged CPU samples are consistent with reducing unused reads:
unedited metadata `ReadMessageUpdates` cumulative sampled time fell from 0.98 s
to 0.61 s; JSON decoding fell from 0.45 s to 0.21 s. Edited JSON decoding fell
from 0.96 s to 0.75 s, while edited metadata reads did not improve (0.90 s to
1.01 s). Each profile sampled three nodes for eight seconds under a separate
mixed workload. These short samples explain candidates for work; unprofiled
counter deltas above remain the resource comparison. Existing conversation-head
hydration, transport, storage and allocation remain substantial costs.

## Single-channel delta

The first cold query took 34.266 ms, loaded one channel runtime (0 → 1) and
wrote no memberships. This is one observation, not a cold-start percentile.
Subsequent windows ran at 100 QPS for 60 seconds, with fixed-cursor replay to
isolate changed versus empty reads; this is not a recommendation to poll an SDK
at that frequency.

| Result | Achieved QPS | P95 / P99 ms | Allocated MB / request | CPU seconds |
| --- | ---: | ---: | ---: | ---: |
| One changed message | 99.983 | 26.25 / 28.05 | 0.169 | 37.06 |
| Empty update page | 99.983 | 18.51 / 20.53 | 0.127 | 28.78 |

Both windows had zero errors, drops, additional runtime loads or membership
writes, and retained one resident runtime. Response payload, version, sequence,
cursor and coverage were checked. The resident runtime was evicted before the
separate conversation profile capture.

Changed-delta CPU rose from 32.99 to 37.06 seconds (+12.3%) despite slightly lower
latency and allocation. This remains an unresolved observation. All three nodes
contributed to the increase; GC count/paused-duration counters do not establish
its cause, and the separately collected mixed profile cannot diagnose this
delta-only interval. Empty-delta CPU fell from 29.37 to 28.78 seconds. This pass
does not claim a universal CPU improvement or a completed production performance
qualification.

## Validation and preserved failures

Focused unit tests passed in `pkg/db/meta`, `pkg/slot/fsm`, `pkg/slot/proxy`,
`pkg/cluster`, `internal/usecase/message`, `internal/usecase/conversation` and
`internal/access/api`. The initial concurrent validation run failed the existing
`TestWaitNodeReadySucceedsForStartedSingleNodeCluster` startup deadline;
`pkg/cluster` subsequently passed its full unit suite in a serial rerun. The
failure remains in `final-unit.log`; this does not establish its root cause.

Message-update race tests passed in metadata, proxy and cluster packages. The
Darwin linker emitted its previously observed LC_DYSYMTAB warnings. Integration
tests for the app HTTP flow, three-node quorum/leader transfer and ReadBarrier
passed in a serial run. The initial leader-transfer test hit a refused connection
after stopping the former leader. Its helper waited for observed Raft leadership
but did not ensure that published routing had converged. The fixture now waits
for every node to publish the target leader before fault injection; it passed
three consecutive repetitions. Product operations are not retried by this fix.

The `flow-doc-contracts` check passed with 80 compliant files, no invalid files
and eight advisory length warnings. The earlier repository-wide Grafana metric
coverage failure remains documented in the preceding reports; no full-repository
green result is claimed for this pass. Commands used `GOWORK=off`, with
`integration` tags for the cluster integration tests.

## Evidence and delivery

- [Portable results and source identities](2026-09-13-message-updates-query-optimization.json)
  includes every comparison window, the earlier source-manifest chain, fixed
  workload hash and final binary hash. Raw reports, red/green tests, microbenchmarks,
  validation logs and profiles remain under `tmp/message-update-optimization/`.
- Final product binary SHA-256:
  `c0d08e5b3423282bbb6b56636a48c1930c9cede0ea495107b3ff93dd1ee4af66`.
  The same precompiled final harness ran the intermediate A/B candidate and both
  final variants; final source hashes are in `final-source.json`. Earlier baseline
  harness fixes and their scope are retained in the preceding report and manifests.
- Run order in this pass: intermediate empty-state/overlay candidate, final
  unedited candidate, final edited candidate. Each used a fresh cluster. Three
  windows within one cluster are not independent deployment replicates. No
  compilation or concurrent test suite ran during the measured windows.
- All temporary performance containers exited and were removed. `git diff --check`
  passed. The feature and optimization remain uncommitted in the existing
  `codex/message-updates` worktree, without merge or deployment.

This completes the bounded query CPU/allocation pass. Remaining acceptance work
includes a repeated/profiling check of changed-delta CPU, sustained concurrent
edits and reads, failover under load, cross-host latency and live EVENT fanout.
CMD remains non-editable; SDK integration has not been changed in this pass.
