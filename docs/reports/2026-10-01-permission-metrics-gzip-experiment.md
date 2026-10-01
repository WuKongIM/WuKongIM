# Fast gzip metrics repair experiment — rejected

The fastest-gzip candidate did not repair Issue #977’s original sequential p99/whole-node CPU symptom. All 24 placement/control cases (48 individual SEND windows) and all 12 burst comparisons passed, but **9 of 12 sequential comparisons failed the unchanged +5% limits**. The prior six original failed windows remain failed evidence in the [quorum/WAL report](2026-10-01-permission-quorum-boundaries.md) and its existing archive. No failed run was retried or replaced, and no production change is included in this evidence update.

## Hypotheses and the single changed variable

The ranked predictions were: safe WAL preparation would shorten rotation; a larger configured memtable would not change Pebble’s fixed 256 KiB initial allocation; current-record sync by required voters could remain the limiting wait; and cheaper full-metrics compression could lower CPU without changing measured scope. The pinned Pebble source requires a durable old-WAL EOF before publishing the next WAL and offers no supported initial-size or preparation option. No WAL ordering, sync command, durability or prefix warmup was changed.

The measured candidate changes only the production metrics handler’s gzip compression level to `gzip.BestSpeed`. It keeps promhttp gathering, all metric families and promoted aliases, formats, pinned encoding negotiation and plain-text gather errors. It streams through a pooled per-response writer and detaches the HTTP response before pooling. It does not cache or omit metrics, change the original 20 ms ingress sampler, or subtract sampling CPU. The nil Registry fallback retains its original default instrumentation.

## Frozen identities and validation

- Old source: `424d03eb298b972ec572261d617972eecb5523c5`; original immutable old binary reused.
- Candidate source: `38d53d3dc555361e1b72bde6ea49a6dd34586f17`, parent `c03fa6cc1712e886d9359822e23be716fdb4534e`. The parent includes the permission-cohort candidate and Darwin sync adapter; it is not the PR delivery tree.
- Original driver: `2a4a1f4e57a8cb3d5586f10bb1585717c37ea0ab`; all seven source hashes and the existing native CPU probe are unchanged.
- Go 1.25.11, Darwin ARM64, GOMAXPROCS=4 per node/driver; binary metadata binds clean source. CPU calibration after the completed pair series measured 51.086 ms against 51.730 ms getrusage CPU with the retained 125/3 timebase.
- Six runs in fixed `old1,new1,old2,new2,old3,new3` order; every run uses all four placements, 32 sessions, 64 sequential and 64 burst SENDs, 32 ban and 32 unban controls, 256 Hash Slots, 12 physical Slots, one metadata voter and three message voters.
- All histories contain exactly the 161 expected successful IDs. All ACK/count/topology/ownership checks passed. The 18 owned node PIDs exited; no retry, replacement, trace, profile, added gap or overhead subtraction was used.

Failure contracts and tests preceded implementation. The original handler failed eight fastest-compression header assertions. After implementation, the full `pkg/metrics` race suite and focused API Metrics tests passed. Contracts cover complete 256-Slot original/promoted series, text/protobuf content, negotiation, uncompressed gather errors, 32 concurrent valid gzip streams, broken writes and subsequent pool reuse, and nil-default instrumentation. These HTTP contracts establish compatibility, not recovery of the performance symptom.

## Complete performance result

Every row must pass **both** p99 and whole-node native CPU at +5%; for 64 SENDs p99 is the maximum. CPU includes all three owned nodes’ background work and native cut overhead. Metric sampling remains within the cuts. Whole-node CPU is not per-request CPU.

| Pair | Placement | Concurrency | Old → new p99 (ms) | p99 change | Old → new CPU (ms) | CPU change | Verdict |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| 1 | same-slot-remote | 1 | 27.680 → 101.817 | +267.84% | 449.704 → 779.838 | +73.41% | FAIL |
| 1 | two-slots-one-remote-leader | 1 | 38.741 → 40.017 | +3.29% | 421.256 → 308.873 | -26.68% | PASS |
| 1 | two-remote-leaders | 1 | 33.951 → 27.651 | -18.56% | 480.792 → 301.600 | -37.27% | PASS |
| 1 | two-slots-local-leader | 1 | 48.188 → 17.709 | -63.25% | 706.186 → 287.012 | -59.36% | PASS |
| 1 | same-slot-remote | 32 | 539.939 → 47.431 | -91.22% | 476.671 → 42.107 | -91.17% | PASS |
| 1 | two-slots-one-remote-leader | 32 | 584.560 → 52.036 | -91.10% | 468.025 → 33.900 | -92.76% | PASS |
| 1 | two-remote-leaders | 32 | 657.639 → 41.282 | -93.72% | 560.058 → 31.853 | -94.31% | PASS |
| 1 | two-slots-local-leader | 32 | 826.679 → 80.305 | -90.29% | 739.355 → 50.550 | -93.16% | PASS |
| 2 | same-slot-remote | 1 | 21.945 → 36.099 | +64.50% | 384.026 → 516.935 | +34.61% | FAIL |
| 2 | two-slots-one-remote-leader | 1 | 28.082 → 43.918 | +56.39% | 474.976 → 452.221 | -4.79% | FAIL |
| 2 | two-remote-leaders | 1 | 20.283 → 34.124 | +68.24% | 465.410 → 465.067 | -0.07% | FAIL |
| 2 | two-slots-local-leader | 1 | 27.919 → 36.929 | +32.27% | 388.776 → 566.182 | +45.63% | FAIL |
| 2 | same-slot-remote | 32 | 522.097 → 53.650 | -89.72% | 444.446 → 37.187 | -91.63% | PASS |
| 2 | two-slots-one-remote-leader | 32 | 481.873 → 82.258 | -82.93% | 388.490 → 59.545 | -84.67% | PASS |
| 2 | two-remote-leaders | 32 | 578.780 → 75.084 | -87.03% | 482.695 → 47.732 | -90.11% | PASS |
| 2 | two-slots-local-leader | 32 | 421.192 → 93.070 | -77.90% | 381.140 → 69.703 | -81.71% | PASS |
| 3 | same-slot-remote | 1 | 28.420 → 29.990 | +5.52% | 366.021 → 344.204 | -5.96% | FAIL |
| 3 | two-slots-one-remote-leader | 1 | 27.839 → 45.028 | +61.74% | 340.284 → 362.174 | +6.43% | FAIL |
| 3 | two-remote-leaders | 1 | 18.111 → 48.878 | +169.88% | 346.557 → 629.821 | +81.74% | FAIL |
| 3 | two-slots-local-leader | 1 | 20.013 → 23.364 | +16.74% | 381.825 → 303.442 | -20.53% | FAIL |
| 3 | same-slot-remote | 32 | 583.590 → 53.063 | -90.91% | 505.725 → 56.468 | -88.83% | PASS |
| 3 | two-slots-one-remote-leader | 32 | 358.969 → 160.877 | -55.18% | 363.372 → 89.315 | -75.42% | PASS |
| 3 | two-remote-leaders | 32 | 368.003 → 58.083 | -84.22% | 344.516 → 42.812 | -87.57% | PASS |
| 3 | two-slots-local-leader | 32 | 369.956 → 98.921 | -73.26% | 342.866 → 66.875 | -80.50% | PASS |

## Compression tradeoff, separate from acceptance

The fixed-source full 256-Slot registry benchmark retained three 200-iteration samples per handler, including the slow first product sample. Median endpoint wall time was **2.754 → 1.910 ms (−30.66%)**, wire size **19,818 → 22,117 bytes (+11.60%)**, and allocated bytes **1,381,316 → 1,616,842 (+17.05%)**. Allocation count was effectively unchanged. This is an httptest HTTP benchmark including Go/process collectors and formatting; it does not measure native whole-node CPU, and its dynamic collector values are not a byte-identical performance payload.

A faster compression microbenchmark did not make the original experiment pass. No cause is assigned to the additional CPU or p99 failures from these unprofiled runs alone. The candidate remains on an explicitly experimental branch and is rejected as an Issue #977 repair. WAL/required-voter sync and whole-node CPU attribution remain unresolved; this shared Darwin experiment is not the unchanged Linux 500 SEND/s qualification.

## Replayable artifacts

[Selected experiment archive](2026-10-01-permission-metrics-gzip-experiment.tar.xz) contains all six complete fresh reports, raw CPU cuts/ACK timings and original source hash controls, plan/execution, original unchanged verifier, all benchmark samples, red/green contracts, exact candidate patch/source and frozen instruction digests. `original/` preserves the previous plan/verdict and source binding; its six earlier reports remain in the existing quorum/WAL archive rather than being duplicated here. Binary identities are recorded, while binaries and the pinned native toolchain are retained outside the selected source archive.

Extract to a disposable directory and run `python3 verify-experiment.py`. It checks the manifest, replays the unchanged verifier in a disposable copy, requires the same nine rejected rows, recomputes the benchmark medians, and rejects altered functional/identity/CPU/p99 evidence. Expected performance rejection remains a successful artifact verification; it is not turned into a passing product gate.

Local retained evidence: `/Users/tt/.codex/artifacts/issue-977-metrics-gzip-20261001`. The exact candidate binary and original-driver instructions are retained there; original old binary/driver/probe remain bound to the prior Darwin artifacts.
