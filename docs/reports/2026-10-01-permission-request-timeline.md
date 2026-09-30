# Request timeline and hot-Channel append repair
#977 / Draft PR #981; evidence recorded across 2026-09-30 and 2026-10-01.

The source-bound diagnostic establishes dominant admitted-append waiting in the fixed hotspot fixture. A bounded same-Channel queue-prefix repair and independent deadline repair are implemented at clean `5b8379497f1127c2e298d27925888fb701167f58`. All twelve concurrency-32 comparisons pass the unchanged ≤5% p99/whole-node CPU regression threshold. **Sequential controls still contain regressions; this does not establish overall non-regression, capacity, HA or Issue #977 completion.** Prior failures remain retained.

## Diagnosis and source identity

Identically instrumented old `49aac13c2c23b0c517af4f9dcd9faf1813132d6b` (trace-only backport onto `424d03eb298b972ec572261d617972eecb5523c5`) and candidate `c6bd20f0f5f7b4085f16c9507026828044f82a42` pass 64 burst ACKs, all required stages, 192 bounded public queries and each exact 161-item history. Their harness hashes match.

| Slowest request boundary | Old ms | Candidate ms |
| --- | ---: | ---: |
| `gateway.async_dispatch_wait` | 1.069 | 24.677 |
| `message.permission` | 0.419 | 1.219 |
| `message.append_admission_wait` | 570.748 | 469.645 |
| `gateway.messages_send` | 591.169 | 486.765 |
| `gateway.write_sendack` | 0.011 | 0.011 |

Nested/shared spans are not added. These are diagnostic timings, not qualification. A post-repair traced run of `4a5f66744` retains a 9.605 ms admitted wait and 45.348 ms p99; its driver includes the subsequently added inactive CPU option and has a different harness hash, so it is supporting stage evidence rather than a strictly matched old/new performance pair.

The first two traced builds incorrectly carried outer-root `1982a56b5` VCS stamps. Both reports/logs remain archived with `source_identity_valid=false`; they certify no source comparison. Corrected clean builds bind `GIT_DIR` and `GIT_WORK_TREE` to each exact checkout and independently verify revision, clean flag and SHA-256. The clean diagnostic worktree was removed after preserving its exact source ref and patch.

## Repair and correctness

The existing OrderedSubmitter may combine an already-admitted pure same-Channel prefix only when exactly one job is ready. Existing record/byte targets and multi-Channel dependencies stop it; no timer, fill wait or original-job splitting is added. FIFO item order and original callback order remain; selected successors cannot be dispatched twice. Each callback keeps its record/payload reservation until return, and the last callback fences later same-Channel routing. Independent-ready-prefix behavior stays unchanged.

Review exposed that authority resolution previously borrowed the first item context. Controlled failure-first integration proves both live-peer deadline poisoning and failure to stop when all item-only deadlines expire. Resolution now reuses the existing per-Channel all-items lifetime, stops/releases terminal watcher registrations and preserves each expired item's own deadline result before append admission. Already-running short cancellation callbacks are not described as joined; routing workers and completion ownership do join.

Two negative black-box probes reproduce receipt loss before the harness repair. After repair, injected post-window HTTP 503 remains a failed scenario but retains all 64 ACKs, 191 safe node replies, one explicit failed node and exact 161-item history. An unavailable CPU after-cut remains failed but retains all 64 completed ACKs, timestamps and canceled/joined ownership samples; its CPU interval stays absent, not zero. Decoded JSON keys reject private sender/payload/token fields; unsafe replies are never archived as raw data.

## Fixed unprofiled comparisons

Three preselected old-then-new pairs use old `424d03eb298b972ec572261d617972eecb5523c5` and clean repaired `5b8379497f1127c2e298d27925888fb701167f58`, an identical frozen harness/native probe, three same-host Darwin/arm64 processes, GOMAXPROCS=4, 256 Hash Slots, 12 physical Slots, one metadata voter and three message replicas. Each case keeps 64 sequential controls, two joined 32-session waves, fixed policy controls and full exact history. No tracing, additional histogram families, profile phase or retry is enabled. The original 20 ms ownership sampler remains. Public placement, full topology and Channel runtime metadata match per pair.

The prebuilt Darwin probe uses public process counters for exactly the three owned PIDs, with process-start identity, raw Mach ticks/timebase and both cut bounds preserved. Calibration records 51.840 ms from getrusage versus 50.825 ms from the probe (timebase 125/3). Every raw interval is independently recomputed. Whole-node CPU includes background and scrape/snapshot scheduling overhead. This shared host is not certified exclusive; other tasks were observed and left untouched.

| Pair / placement | p99/max ms old → new | CPU ms old → new | RPC envelopes old → new | Barriers old → new |
| --- | ---: | ---: | ---: | ---: |
| 1 / same-slot-remote | 501.257 → 41.872 | 413.662 → 38.327 | 64 → 3 | 64 → 3 |
| 1 / two-slots-one-remote-leader | 394.779 → 53.707 | 298.330 → 47.310 | 64 → 2 | 128 → 4 |
| 1 / two-remote-leaders | 572.464 → 50.634 | 375.636 → 48.373 | 128 → 4 | 128 → 4 |
| 1 / two-slots-local-leader | 563.967 → 63.373 | 468.004 → 55.518 | 0 → 0 | 128 → 6 |
| 2 / same-slot-remote | 495.393 → 65.689 | 379.321 → 86.557 | 64 → 4 | 64 → 4 |
| 2 / two-slots-one-remote-leader | 523.825 → 53.162 | 435.301 → 39.692 | 64 → 2 | 128 → 4 |
| 2 / two-remote-leaders | 590.286 → 42.684 | 457.826 → 36.465 | 128 → 4 | 128 → 4 |
| 2 / two-slots-local-leader | 602.918 → 60.674 | 521.776 → 51.903 | 0 → 0 | 128 → 6 |
| 3 / same-slot-remote | 428.524 → 64.243 | 366.020 → 56.775 | 64 → 2 | 64 → 2 |
| 3 / two-slots-one-remote-leader | 352.860 → 38.148 | 290.184 → 37.281 | 64 → 2 | 128 → 4 |
| 3 / two-remote-leaders | 455.782 → 26.966 | 368.765 → 22.211 | 128 → 4 | 128 → 4 |
| 3 / two-slots-local-leader | 326.629 → 57.191 | 285.429 → 47.979 | 0 → 0 | 128 → 6 |

Across these twelve burst rows, p99 falls 82.49–94.08% and whole-cluster CPU time falls 77.18–93.98%. With 64 observations nearest-rank p99 is the maximum. Local envelopes also fall and all measured busy/failed-barrier counts stay zero; ownership drains. All 24 measured windows in each old/new aggregate retain their actual outcomes; all cases complete ban/unban and exact history.

Eight of the twelve sequential-control comparisons exceed 5% on p99 or CPU. They are fully retained and are not converted into passed performance evidence:

| Pair / placement | Sequential p99 change | Sequential CPU change |
| --- | ---: | ---: |
| 1 / same-slot-remote | +49.60% | +73.07% |
| 1 / two-slots-one-remote-leader | +87.95% | +24.12% |
| 1 / two-remote-leaders | +15.22% | +14.53% |
| 1 / two-slots-local-leader | +43.99% | +44.84% |
| 2 / two-slots-one-remote-leader | +24.95% | -28.96% |
| 3 / same-slot-remote | -7.45% | +8.48% |
| 3 / two-slots-one-remote-leader | +22.35% | +24.20% |
| 3 / two-remote-leaders | +56.47% | +50.85% |

The first repair source `4a5f66744` also ran all three pairs. Those data remain separate and cannot qualify the final deadline repair. Earlier +18.95%, unmatched-profile +71.44% and stage-diagnostic +16.22% observations remain in the [previous cohort report](2026-09-30-permission-cohorts.md) and [stage report](2026-09-30-permission-stage-diagnosis.md). Lower RPC counts, these short bursts and earlier passed 500 SEND/s windows do not erase those observations or the earlier 4500 SEND/s failure.

## Validation and delivery

Controlled hot-lane target/FIFO/multi-key/capacity/Close integration and independent-resolution deadline checks pass under race. go-format, go-vet and flow-doc-contracts pass. Separate Standards and Spec reviews report zero remaining confirmed findings; their initial findings are retained alongside the repaired reports.

Full send-ban process E2E passes in 403.170 s on the frozen repaired product, covering single-node/three-node admission, connected devices, policy combinations, non-replica priority, quorum faults, leader transfer, parallel envelopes, metadata recovery, delivery absence and exact histories. The full go-unit replay passes after removing only proxy variables from its test subprocess. Its initial complete run with inherited proxies has five Alibaba loopback-mock 502 failures; both that failed run and the successful affected-package/full replays remain archived. No product code was changed to fix this environment interference.

PR981 stays Draft, depending on Draft PR980. At this delivery its diff against main contains 62 files, exceeding the protected Review Agent's 50-file limit. Dependency integration or a scope split is required before formal Review Agent dispatch; policy limits were not changed. PR982 and PR983 are OPEN and ready for review at unchanged heads ed412e325f and 79ce5fb270; their existing automatic checks pass. No review command with an auto-merge path, merge, release, paid resource or manual failed-CI retry was invoked. Automatic final-head CI results are tracked on [PR981](https://github.com/WuKongIM/WuKongIM/pull/981), independently of this local comparative proof.

The evidence manifest/bundle retain raw source-bound and invalid-provenance reports, all six final and six superseded pair outcomes, bounded negative probes, build/instruction hashes, scripts and check/review receipts. Native product binaries remain local with recorded hashes. See [manifest](2026-10-01-permission-request-timeline-manifest.json) and [evidence bundle](2026-10-01-permission-request-timeline-evidence.tar.gz). Recompute final comparisons with verify-reviewed-pairs.py and negative evidence with verify-negative-receipts.py from the archive.
