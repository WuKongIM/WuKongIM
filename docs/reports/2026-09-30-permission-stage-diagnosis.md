# Permission-cohort latency diagnosis

## Fixed evidence scope

This follow-up retains the earlier [three matched pairs and their failures](2026-09-30-permission-cohorts.md). The old 443.033/new 526.973 ms same-Slot regression remains part of that evidence. Do not replace it with this diagnostic pair.

Existing Linux mixed-send flight evidence comes from automatic run 36714723571, merge preview `6aac53621cf0d962cdd5b28363709811508d63bc` (same tracked tree as PR 981 head `e8981154a857667a04696d42df430ac2dd5f7c4c`). Its complete trace covers about 8.56 seconds and was triggered by six early one-second-cohort SENDs above 400 ms. The three later fixed 60-second 500-QPS windows passed unchanged. This is a different run and host from the earlier Darwin regression. Its counter window omitted permission duration histograms; the flight marked counter collection incomplete.

Aggregate runnable delay was 15.924 seconds: 55.89% select nonblocking sends, 20.69% channel sends and 7.85% condition signaling. Cohort execution accounted for 0.93% flat / 1.84% cumulative runnable delay. Aggregate syscall wait was 14.84 seconds, with 84.51% in writes. These sums span many goroutines; they establish neither per-SEND critical paths nor CPU percentages or disk causation. Much synchronization delay is ordinary idle background waiting.

## One preselected diagnostic pair

Before measuring, the plan fixed one old-then-new pair with existing clean binaries (`424d03eb298b...` and `d1deec2b3bc9...`), identical three-process topology and GOMAXPROCS=4, 64 sends per placement, two 32-caller waves, the same 20 ms ownership sampler, and no profiling or extra in-window scrapes. `WK_E2E_PERMISSION_STAGES=1` adds six fixed histogram families to the already-existing boundary metrics cuts. Both runs passed exact ACK, policy and full-history assertions. Harness SHA-256: `d80d7442355e922ff564a63e48103ad266b9b6c3b15a30834d5a1d2e01bf9065`.

| Placement | Old p99/max ms | New p99/max ms | Change | Remote envelopes old → new | Local envelopes old → new |
| --- | ---: | ---: | ---: | ---: | ---: |
| Same remote Slot | 428.674 | 498.208 | +16.22% | 64 → 4 | 0 → 0 |
| Two Slots, one remote leader | 402.337 | 403.677 | +0.33% | 64 → 3 | 0 → 0 |
| Two remote leaders | 363.785 | 365.145 | +0.37% | 128 → 4 | 0 → 0 |
| Two local Slots | 372.160 | 379.777 | +2.05% | 0 → 0 | 64 → 2 |

With 64 observations, nearest-rank p99 is the maximum. This pair is diagnostic, not a replacement three-run non-regression or capacity qualification. Sequential permission counts remain unchanged; all policy/history checks pass.

For same-Slot bursts, node 1 recorded 64 plans/evaluations in each version, each within the 0.5 ms histogram bucket. Old RPC count=64, mean=0.53 ms, all within the 1 ms bucket; new RPC count=4, mean=2.69 ms, all within the 25 ms bucket. Those new samples measure envelopes, not 64 individual callers. Node 2 recorded all 64 completed append waits: old mean=11.76 ms, new mean=11.81 ms, both within the 25 ms bucket. Store-append wait dominates those measured waits, while reserve/submit and post-store/quorum waits remain within 0.5 ms buckets. Histogram bounds are not exact latency samples and must not be summed into an end-to-end percentile.

The same-Slot client tail regressed again while these measured stage populations remained much shorter. This narrows the next instrumentation target to uncovered wait before stage entry and request-correlated progress, including gateway/authority queues and scheduling. It does not establish whether cohort submission cadence, scheduling/GC/I/O, or another queue caused the regression. The sampler cannot observe every short-lived cohort; lack of a sampled owner is not proof that every caller already completed permission work.

## Result and reproducibility

Root cause remains **unconfirmed**; no production tuning was made. RPC/barrier reduction is established, but the ≤5% comparative p99/CPU acceptance requirement and Issue 977 completion remain unsatisfied. The existing passed 500 SEND/s CI windows are evidence for their exact earlier source and scope; they do not negate this local regression.

The [evidence archive](assets/permission-stage-diagnosis-evidence.tar.gz) and [hash manifest](assets/permission-stage-diagnosis-manifest.json) retain both raw reports/logs, exact invocations, frozen instruction digests, per-node histogram deltas and the compact comparison. The archive also includes existing flight window/top files; the original execution trace SHA-256 is recorded in the manifest without copying its 17 MB payload. Run the two documented opt-in E2E modes with the same frozen binaries and `WK_E2E_PERMISSION_STAGES=1` to reproduce the setup; results may vary and all outcomes must be retained.
