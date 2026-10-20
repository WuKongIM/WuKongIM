# Existing mixed-load latency evidence, 2026-10-20

Read-only analysis of the three retained reports. No workflow, benchmark, cloud resource, network download, or repository edit was performed. This report concerns product `b469c6e5e2b4ff65611a8fe3389fb5034de3f152` and beta.24 `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`; it does not qualify the later migration fixes.

Inputs and reproducibility:

- Original failed run: `/tmp/wukongim-v3-rc-20261020/qps/report.json`, [run 39368295521](https://github.com/WuKongIM/WuKongIM/actions/runs/39368295521).
- Matched candidate: `/tmp/wukongim-v3-rc-20261020/qps-comparison/report.json`; matched beta.24: `/tmp/wukongim-v3-rc-20261020/qps-comparison/comparison/report.json`, [run 39369947381](https://github.com/WuKongIM/WuKongIM/actions/runs/39369947381).
- `latency-data.json` retains source SHA-256 hashes, identities, timing tables, all drop/slow-second indices, bounded slow samples, and ten-second groups. Reproduce with `python3 /tmp/wukongim-performance-followup-20261020/latency-analyze.py`.
- Source references below are frozen at `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`, not the current worktree HEAD. No applicable FLOW covers this scenario directory; scenario/domain/root E2E AGENTS were consulted.

## What the timing fields actually measure

[conversation_qps_test.go:312](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/conversation_qps_test.go#L312) timestamps the scheduled arrival, worker start, and completion of `request()`. Thus `driver_wait_p99_ms` covers scheduler plus driver-queue waiting; it does not distinguish those two components. `request_p99_ms` includes request construction, HTTP exchange, body reading, JSON decoding, exact row/message-identity validation and any CPU scheduling delay during that call. It is not a server-only latency metric. See [request():213](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/conversation_qps_test.go#L213) and [conversationResponse():528](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/conversation_qps_test.go#L528).

The percentiles come from independently sorted populations and cannot be added or subtracted to allocate the total P99. A per-request wait/request fraction is valid only for the explicitly sampled request. Timeline bins use **scheduled** second, including requests that complete later. `completed` in a bin is not completion throughput during that second. Drops represent rejected driver arrivals when its bounded queue is full; they are not lost persisted messages.

`slow_arrivals` contains the first at most 16 observations received with total latency >200 ms, in worker-completion order. It is neither a random sample nor the slowest 16 requests. Window-wide percentiles include all executed observations; there were no request errors in these windows. See [lines 361–411](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/conversation_qps_test.go#L361).

## Original failed run: persistent list queueing, not merely first-window startup

All values below are milliseconds; W1–W3 are successive 60-second windows.

| Endpoint/window | Total P50 | Total P99 | Driver-wait P99 | Request-call P99 | Driver drops |
|---|---:|---:|---:|---:|---:|
| list W1 | 595.76 | 813.44 | 750.03 | 82.84 | 1,441 / 12,000 |
| list W2 | 587.04 | 671.01 | 619.83 | 77.37 | 1,142 / 12,000 |
| list W3 | 578.75 | 656.81 | 607.65 | 78.98 | 1,032 / 12,000 |
| sync W1 | 123.81 | 654.88 | 515.75 | 178.95 | 6 / 3,600 |
| sync W2 | 117.37 | 169.41 | 27.21 | 164.82 | 0 |
| sync W3 | 116.70 | 372.06 | 238.87 | 174.37 | 0 |

List drops persisted in **every** scheduled second from seconds 4–59 in W1/W2 and 1–59 in W3. These are zero-based seconds. A >500 ms observation occurred in all seconds 3–59, 3–59 and 1–59 respectively. Drops were 12.01%, 9.52% and 8.60% of scheduled list arrivals. Each window still had 104/106/105 successful list completions after its cutoff, so late drain is correctly excluded from actual QPS. The last ten seconds still dropped 245/276/182 arrivals. There is no within-window return to a healthy list regime.

The bounded original list slow samples begin around 0.62–1.35 seconds after scheduled start, well before the full queue/drop regime; their wait shares average 69.5–77.3%. Because sampling stops at 16 observations, these shares should not be extrapolated to all requests or to the largest P99.

Sync had a different temporal pattern. W1 >500 ms observations were confined to scheduled seconds 20–24, with all six drops in second 22; >200 ms observations spanned 17–27 and 59. W2 had no >200 ms observation at all. W3 >200 ms observations were confined to seconds 0–4, with no drops or >500 ms observation. This supports a transient sync queue disturbance against a persistently overloaded list stream. The receipt does not isolate its origin to server CPU, driver validation, GC, or host scheduling.

## Matched same-host run: observed candidate tail cost is inside the request call

Both versions completed the offered load with zero drops, errors or >200 ms observations in all three windows. The following are **medians of three per-window percentiles**, not a pooled percentile:

| Endpoint | Beta total P50 | Candidate total P50 | Beta total P99 | Candidate total P99 | Beta wait P99 | Candidate wait P99 | Beta request P99 | Candidate request P99 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| list | 4.89 | 5.40 | 16.30 | 24.00 | 1.54 | 2.29 | 15.45 | 22.57 |
| sync | 12.07 | 13.23 | 32.75 | 53.45 | 1.54 | 2.02 | 32.08 | 51.66 |

Candidate total P99 is +47.3% list / +63.2% sync, versus total P50 +10.5% / +9.6%. Request-call P99 is +46.1% / +61.0%. Driver-wait P99 remains about two milliseconds. This distinguishes the matched slowdown from the original hundreds-of-milliseconds driver backlog. It points to cost/latency inside the request call but cannot identify the server as the sole contributor.

Candidate's per-window request P99 is consistently above beta's: list 23.78/22.34/22.57 vs 14.78/15.45/15.63; sync 54.58/47.97/51.66 vs 31.30/32.08/35.04. Neither a single slow window nor first-request coldness explains that pattern. The per-second maxima are bounded summary observations, not request distributions: they cannot reconstruct a P99 for a first/last ten-second segment.

## Order, warmup, and the next discriminating experiment

The original windows share one prepared cluster/fixture. Every window—including W1—first performs 48 sequential warmup requests covering both endpoints and all 24 users before metrics and the scheduled start. All fixture runtimes are evicted before the first window; persistent storage caches are intentionally warm. This is **not a cold-disk benchmark**. The next windows reuse the same process/fixture. See [stress_test.go:146](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/stress_test.go#L146) and [mixed_attribution_test.go:74–92](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/mixed_attribution_test.go#L74).

Within the matched run, beta's measured windows occurred 04:34:35–04:37:36 UTC, then its separate profile phase 04:37:37–04:37:47. Candidate's measured windows occurred 04:38:21–04:41:22, with a fresh product-process cluster and fresh fixture. Candidate W1 is 5 min 46 sec after beta W1. Beta P99 drifts upward by 6.4% list / 10.7% sync from W1 to W3; candidate has no monotone P99 drift. That variation is smaller than the observed beta/candidate gap, but source and ordering are still confounded in one sequential pair.

A single **predeclared reversed-order same-host pair**, first b469 then beta.24, is therefore useful and not contradicted by these receipts. Preserve exact source/binary/harness/profile identities, workload and all rejected results; do not retry to obtain green. Compare matched ratios and per-window timing/resource deltas, not absolute milliseconds between different runner models. If b469 remains slower when first, version-specific cost gains support; if whichever version is second slows, order/host evolution gains support. A smaller/disappearing gap remains inconclusive rather than proving equality. One reversal improves discrimination but does not by itself establish broad statistical certainty or release qualification.

The existing evidence should also retain the original failed hardware run: successful matched windows cannot erase its sustained overload. Separate profiling occurs after the three windows and starts before that profile workload's warmup; eight-second server captures do not span the complete ten-second workload or any earlier unprofiled window. See [mixed_attribution_test.go:93–148](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/mixed_attribution_test.go#L93). Profile attribution is supporting evidence, not a time-resolved explanation of the original W1 seconds 20–24.
