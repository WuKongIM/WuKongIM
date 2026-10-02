# Permission repair: main integration qualification

Issue #977 / Draft PR #981, evidence recorded on 2026-10-01 Asia/Shanghai.

Clean candidate `a991974f8d34423b5e59a12bd24e15de21b1a723` integrates main `623091def1951fea6746bd9ac3b63a8a9ba30208`. All twelve fixed concurrency-32 comparisons pass the unchanged ≤5% p99/whole-node CPU regression threshold. **Nine sequential-control comparisons still exceed 5% on p99 or CPU; overall non-regression and Issue #977 completion remain unverified.** Fresh first-attempt Linux 500 SEND/s PR qualification passes independently. Neither evidence set proves higher-rate capacity or ten-minute nightly qualification.

## Source and integration boundary

The [previous timeline report](2026-10-01-permission-request-timeline.md) and its unchanged 127-file bundle describe source `5b8379497` and the historical `639b7c35f` delivery snapshot. Its twelve burst passes, eight sequential regressions, earlier failures and invalid VCS stamps remain intact; they are not transferred to this candidate.

Main advanced before `639b` CI could start, producing a merge-preview conflict. This worktree merges exact main `623091def` as the second parent of `a991974f8`; only CHANGELOG and generated FLOW_INDEX required content resolution. Changelog entries from both parents remain and FLOW_INDEX was regenerated. Incoming stream-event composition is retained verbatim. SEND permission/tracing, Router and OrderedSubmitter implementations remain identical to `639b`. Protected `.github`/`.agents` files match main; no policy was edited.

A clean build explicitly binds Git to this task worktree, verifies `vcs.revision=a991974f8d34423b5e59a12bd24e15de21b1a723` and `vcs.modified=false`, and records binary SHA-256 `63ecbcd167f5aef4eacf493cb616558e6c3e2fbe1609faa5edb6b43e2120f6ad`. First-attempt CI uses merge preview `ec392bd9845ec89e36ff892e0a67328f40a1b01d`; its parents were verified and its entire tree matches the tested candidate tree. Subsequent additions of this report/bundle are documentation only and require their own final-head automatic CI status; the source-qualified results below remain bound to `a991`.

## Fresh local validation

- Full named go-unit and go-vet checks pass. Local check subprocesses remove only proxy variables to avoid the already-recorded loopback-mock interference; no product configuration changes.
- go-format and flow-doc-contracts pass. The controlled ordered-hotlane and independent authority-deadline integration checks pass under race.
- Full process-level send-ban E2E passes in 363.661 s, retaining main and all companion reports. Original opt-in 100k/characterization modes remain separate and skipped in this default functional run.
- The incoming public stream-online E2E passes in 17.602 s, including single-node/three-node group/person routing, terminal/private events and offline recovery.
- Independent Standards and Spec integration reviews each report zero confirmed findings. They certify static integration safety only; they do not relabel old performance evidence or substitute for these fresh runs.

## Fresh fixed old/new comparison

The runner preselects three old-then-new pairs, old `424d03eb298b972ec572261d617972eecb5523c5` → clean `a991974f8`. All six runs complete successfully without retries or replacements. Both sides use the same frozen driver/native probe, three Darwin/arm64 nodes, GOMAXPROCS=4, 256 Hash Slots, 12 physical Slots, one metadata voter and three message replicas. Each placement retains 64 sequential controls, two joined waves of 32 independent sessions, ban/unban controls and exactly 161 committed history items. Placement/configuration/topology and Channel runtime metadata match per pair.

No profiling, request tracing or extra histogram families run in these comparison windows. The original 20 ms ownership sampler and CPU-cut overhead remain. Our own functional/unit tests finish before these windows; one other product process is observed, so the host is shared and not certified exclusive. Raw process-start identities, Mach ticks/timebase, all ACKs and both cuts remain archived; the verifier independently recomputes all 48 CPU intervals and nearest-rank p99 (equal to max at 64 observations).

| Pair / placement | p99/max ms old → new | CPU ms old → new | RPC old → new | Barriers old → new |
| --- | ---: | ---: | ---: | ---: |
| 1 / same-slot-remote | 443.067 → 70.813 | 312.643 → 57.940 | 64 → 2 | 64 → 2 |
| 1 / two-slots-one-remote-leader | 342.559 → 35.236 | 286.252 → 32.566 | 64 → 2 | 128 → 4 |
| 1 / two-remote-leaders | 352.856 → 39.969 | 278.150 → 29.174 | 128 → 4 | 128 → 4 |
| 1 / two-slots-local-leader | 357.814 → 50.308 | 314.480 → 39.183 | 0 → 0 | 128 → 6 |
| 2 / same-slot-remote | 494.283 → 64.939 | 373.250 → 65.001 | 64 → 3 | 64 → 3 |
| 2 / two-slots-one-remote-leader | 369.003 → 67.632 | 308.660 → 44.387 | 64 → 2 | 128 → 4 |
| 2 / two-remote-leaders | 298.296 → 45.037 | 248.695 → 35.353 | 128 → 4 | 128 → 4 |
| 2 / two-slots-local-leader | 392.803 → 46.737 | 404.341 → 40.867 | 0 → 0 | 128 → 4 |
| 3 / same-slot-remote | 514.197 → 57.785 | 405.029 → 58.840 | 64 → 3 | 64 → 3 |
| 3 / two-slots-one-remote-leader | 345.786 → 67.061 | 299.639 → 64.272 | 64 → 2 | 128 → 4 |
| 3 / two-remote-leaders | 367.856 → 53.405 | 315.190 → 43.528 | 128 → 4 | 128 → 4 |
| 3 / two-slots-local-leader | 354.052 → 49.395 | 293.389 → 39.824 | 0 → 0 | 128 → 4 |

Across the twelve burst rows p99 falls 80.61–89.71%, whole-node CPU falls 78.55–89.89%, and the highest candidate burst p99 is 70.813 ms. All exact histories, policy controls, counts and drained ownership observations pass. These unpaced hot-Channel bursts cannot establish production capacity, metadata HA or recovery of the earlier 4500 SEND/s failure.

| Pair / placement | Sequential p99 change | Sequential CPU change |
| --- | ---: | ---: |
| 1 / same-slot-remote | +32.94% | +22.55% |
| 1 / two-slots-one-remote-leader | +41.01% | +24.09% |
| 1 / two-remote-leaders | +73.25% | +45.86% |
| 2 / same-slot-remote | +31.27% | +19.29% |
| 2 / two-slots-one-remote-leader | +31.88% | +15.67% |
| 2 / two-slots-local-leader | +6.44% | +15.08% |
| 3 / same-slot-remote | +19.83% | +20.10% |
| 3 / two-slots-one-remote-leader | +4.10% | +23.79% |
| 3 / two-remote-leaders | +40.14% | +54.13% |

All twelve sequential controls are retained in the raw verdict, including the three within threshold. The nine over-threshold rows above are **failed comparative evidence**, not discarded controls. They remain a required follow-up before any overall non-regression claim.

## First-attempt Linux PR qualification

[Automatic regression run 36744694111](https://github.com/WuKongIM/WuKongIM/actions/runs/36744694111) completes successfully at the exact merge preview above. All three fresh seams keep the original 60-second warmup and three 60-second windows, four visible AMD64 CPUs, Go 1.25.11, offered/started 500 SEND/s, the 400 ms scheduled-arrival-to-completion p99 budget and zero errors/drops. All nine windows complete exactly 30,000 operations each; no baseline reuse or retry replaces them.

| Linux 500 SEND/s seam | Three measured p99 windows (ms) | Completions / errors / drops |
| --- | ---: | ---: |
| channel-append | 4.411 / 4.364 / 4.821 | 90,000 / 0 / 0 |
| mixed-send | 11.950 / 12.326 / 11.809 | 90,000 / 0 / 0 |
| tcp-sendack | 178.237 / 178.250 / 178.215 | 90,000 / 0 / 0 |

The prescribed mixed-send seam includes its existing flight recorder and one-second sampler; the retained receipt marks instrumentation enabled. This absolute Linux gate is separate from the unprofiled comparative Darwin CPU evidence. Skipped nightly/diagnostic jobs are not claimed as passes. Manager Chromium and native package-preview/lifecycle automatic checks also pass at `a991`; a package preview is not a published release.

## Delivery and remaining scope

PR981 remains Draft and depends on Draft PR980. The source-head diff against main is still 62 files before this three-file supplement (65 after it), exceeding the protected Review Agent limit of 50. Formal dispatch requires dependency integration or a scope split; limits were not changed. PR982/983 were independently moved to review and remain at their original heads. No auto-merge review command, merge, release, paid resource or manual failed-CI retry was invoked.

The [manifest](2026-10-01-permission-main-integration-manifest.json) and [bundle](2026-10-01-permission-main-integration-evidence.tar.gz) retain 102 files, including all six paired receipts, every functional companion, native/source identities, local checks, independent reviews and the three original CI performance artifact directories. Product/native binaries remain local with recorded hashes. From the extracted bundle, run `verify-reviewed-pairs.py` and `verify-ci-500qps.py` to recompute the comparative and Linux verdicts. The prior evidence bundle is unchanged.
