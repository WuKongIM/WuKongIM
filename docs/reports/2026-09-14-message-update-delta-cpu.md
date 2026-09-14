# Changed-delta CPU investigation

Status: completed. The original +12.3% CPU observation did not reproduce as a
stable version regression in three counterbalanced pairs. This follows the
changed-delta observation in
the [query optimization report](2026-09-13-message-updates-query-optimization.md).
Product code is frozen during the comparison. All product changes remain
uncommitted in `codex/message-updates`.

## Question and evidence route

The previous `/channel/messageupdates` changed-page window consumed 32.99 CPU
seconds before optimization and 37.06 afterward (+12.3%), summed across three
servers for 60 seconds at 100 QPS. Allocation and P99 slightly decreased. Those
were single deployments, so the observation did not establish a repeatable code
regression. The earlier mixed-workload profiles cannot attribute delta-only CPU.

This uses bounded differential evidence before attempting a product fix. The
existing full black-box loop is retained rather than reducing the data or its
preceding workload before confirming reproduction. The elapsed windows are
necessary to compare steady process CPU, scheduling, background work and RPC
counters. No correctness test alone can demonstrate this resource symptom.

## Ranked hypotheses and predictions

1. **Route/leader-dependent work.** If a different owner/forwarding path caused
   the increase, exact per-service business RPC counts should change. Historical
   windows instead have the same counts: 12,000 message-update RPC counter
   increments and 4,000 each for committed-read, channel-metadata,
   runtime-metadata and membership services, all served on node 2. These are
   transport observations, not distinct business requests. This already weakens
   that explanation.
2. **Additional computation in optimized code.** If the change caused a stable
   regression, repeated optimized runs should use consistently more CPU at equal
   completed traffic, and delta-only profiles should identify the changed path.
3. **Background storage/maintenance or GC.** If background work explains the
   difference, affected windows/profiles should show corresponding maintenance,
   storage, allocation or GC work without increased business requests. Historical
   GC counts/paused-duration counters did not establish this cause.
4. **Host/run variability.** If the original difference is not stable, CPU ranges
   should overlap or pair direction should change under counterbalanced runs.
   Such results would not identify a specific host mechanism or prove zero cost.

## Frozen experiment

- Before binary SHA-256:
  `5ff0d550760637f8298236271696d7a853662475c128d457f4a00094ab83283c`.
- After binary SHA-256:
  `c0d08e5b3423282bbb6b56636a48c1930c9cede0ea495107b3ff93dd1ee4af66`.
- Harness SHA-256:
  `96f55f1fd330dbbcf9eaf48ebaede2f5a1903cf1e5ccab626368064e698af40e`.
- Unchanged workload profile SHA-256:
  `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`.
- Run order: before-1, after-1, after-2, before-2, before-3, after-3.
  Six fresh three-node clusters run serially on the same Linux ARM64 Docker
  host, with CPUs 0–5, 6 GiB, driver/node GOMAXPROCS=2, 256 Hash Slots,
  12 physical Slots and three replicas. Loopback transport and container-local
  DB files match the preceding diagnostic. No builds or other test suites run
  during measurement.
- Preserve 600 groups, 24 readers, three 256-byte messages per group; edit all
  600 tails once through public HTTP before measurement. Preserve the three
  preceding mixed list-200/sync-60 QPS windows, cold selected-channel recovery,
  changed-delta and empty-delta windows at 100 QPS for 60 seconds each.
- Only the subsequent independent profile load changes: changed-delta at
  100 QPS for ten seconds, eight workers, eight CPU seconds sampled per node,
  allocation snapshots before/after, existing authenticated debug API, 32 MiB
  maximum per profile body. The selected channel remains resident. Exact content,
  version and cursor coverage, zero errors/drops, no additional runtime loads or
  membership writes and stable residency remain required.
- Reports mark `delta_profile=true`; profiled traffic is never included in
  unprofiled throughput or resource comparisons. Existing release thresholds
  are unchanged. Preserve refused/failed windows and all deployment attempts.

The agent-runnable loop is
`python3 tmp/message-update-delta-cpu/run_comparison.py` from the implementation
worktree. It runs each exact binary three times with the same compiled harness
and records command, timestamps and exit status in `execution.json`. Source,
binary and applicable instruction digests are retained in the same clearly
marked diagnostic directory. This is same-host diagnosis, not production
capacity, concurrent-edit/failover qualification or SDK integration.

## Repeated changed-delta results

Each row is an independent cluster's unprofiled 60-second changed-delta window.
CPU is the sum across its three servers, including their background work.
Allocation is decimal MB divided by completed successful requests, with 6,000
requests per row. P99 is the measured row percentile, not an average percentile.

| Run order | Variant | CPU seconds | Allocated MB / request | P99 ms |
| --- | --- | ---: | ---: | ---: |
| 1 | Before-1 | 40.87 | 0.17120 | 28.77 |
| 2 | After-1 | 38.60 | 0.16918 | 29.22 |
| 3 | After-2 | 38.72 | 0.16915 | 29.02 |
| 4 | Before-2 | 39.27 | 0.17029 | 26.79 |
| 5 | Before-3 | 41.88 | 0.17112 | 27.71 |
| 6 | After-3 | 43.11 | 0.16964 | 27.33 |

Before CPU averaged **40.67 seconds**, range **39.27–41.88**, sample standard
deviation 1.32. After averaged **40.14 seconds**, range **38.60–43.11**, sample
standard deviation 2.57. Paired changes were **−5.6%, −1.4%, +2.9%**. The mean
difference is −1.3%, but ranges overlap and pair direction changes. This is not
evidence for a reliable 1.3% improvement, and does not reproduce a stable 12.3%
regression. Three deployments per variant do not justify a population confidence
interval or a claim of zero regression in every environment.

Allocation was more stable: about 0.17087 MB/request before and 0.16932 afterward
(−0.9%). Before P99 ranged 26.79–28.77 ms; after ranged 27.33–29.22 ms. Neither
CPU nor latency is presented as a universal improvement. Historical 32.99/37.06
CPU observations remain in the preceding report and are not pooled into this
counterbalanced sample.

## Attribution evidence

All six changed-delta windows had identical business RPC counter increments:
12,000 for message updates and 4,000 each for committed reads, channel metadata,
runtime metadata and membership. They also sustained 99.983 QPS as measured
inside the window, with all 6,000 scheduled requests completing successfully.
No changed-delta window loaded another runtime or wrote a membership; residency
stayed at one after its separately recorded cold recovery. The same checks
passed for empty-delta windows. Conversation windows retained zero residency,
loads and membership writes.

Empty-delta CPU also varied: before 28.80–31.71 seconds, after 29.81–32.90.
Paired empty-delta differences changed direction too. This weakens an explanation
specific to extra changed-message work. It does not identify a particular host
or background mechanism.

The nine per-node eight-second CPU profiles for each binary were merged without
mixing binary versions. Their independent profiled workloads each used changed
delta only. Selected cumulative sampled times across the three deployments:

| Stack | Before sampled CPU s | After sampled CPU s |
| --- | ---: | ---: |
| Metadata `ReadMessageUpdates` | 0.63 | 0.64 |
| Slot read worker | 0.68 | 0.67 |
| `encoding/json.Unmarshal` | 0.31 | 0.23 |
| Transport RPC service handler | 2.18 | 1.76 |

These stack totals overlap and cannot be added. Flat system-call and futex
samples were similar (2.40/1.01 s before, 2.37/1.06 s afterward). Total sampled
CPU was 13.99 versus 14.61 seconds, a different direction from the unprofiled
one-minute counters. Short sampling runs taken after the measurement windows
are useful for locating work, not a replacement for repeated full-window CPU
measurement. Their variability prevents attributing a precise resource delta
to an individual function.

No stable added RPC work or localized 12.3% regression in the changed paths was
established. Allocation measurements decreased slightly, and GC count/paused-time
counters did not establish a GC explanation. The collected metric set did not
contain storage-compaction/maintenance counters; short profiles do not exclude
background work outside their sample intervals. A specific source of host/run
variability remains unproven.

The defensible finding is **not reproduced as a stable code regression**,
consistent with run/measurement variability. This closes the original single-run
regression claim at the current evidence level, without claiming a verified fix,
zero overhead, or a generally faster delta endpoint. No product change is
justified by this comparison alone.

## Validation and delivery

- All six fresh-cluster runs exited successfully. All 30 unprofiled windows
  (48 endpoint results) passed the unchanged acceptance checks, including exact
  response validation, rate, latency and zero errors/drops. All six separate
  changed-delta profiling workloads also passed their correctness, load and
  residency checks. All 54 original profile bodies were retained.
- Focused default-tier tests passed:
  `GOWORK=off go test ./test/e2e/message/conversation_qps -count=1`.
  The Linux ARM64 E2E harness compiled and executed the real three-process
  workloads. No repository-wide green claim is made; prior unrelated validation
  findings remain in the preceding reports.
- Changes in this pass are limited to the opt-in delta profile path, its
  scenario/domain documentation and diagnostic reports. Public APIs, product
  code, consistency barriers and CMD restrictions are unchanged.
- [Portable comparison](2026-09-14-message-update-delta-cpu.json) contains every
  window, per-node CPU/GC/allocation counter deltas, per-service RPC counts,
  binary/harness/workload identities and the full execution receipt. Raw
  reports, logs, profiles and clearly marked one-off analysis scripts remain
  in `tmp/message-update-delta-cpu/`.
- The serial experiment ran from 00:10:23 to 00:59:36 on 2026-09-14
  (Asia/Shanghai). Temporary containers exited and were removed. The underlying
  feature remains uncommitted and has not been merged or deployed.

The next separate acceptance phase is sustained concurrent edits/reads and
failover under load. It was not executed as part of this bounded CPU inquiry.
