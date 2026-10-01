# Remaining sequential SEND diagnosis after Darwin sync

Issue #977 remains unresolved: the unchanged original driver still rejects **6/12 sequential comparisons**, while all 12 burst comparisons pass. This diagnosis locates sampled CPU and narrows the request boundary; it changes no product code, durability, workload, thresholds or qualification.

## What the evidence establishes

The native Mach tick cuts in all six original reports rederive exactly into per-node user/system CPU for all 48 windows. In the two CPU-only same-Slot failures, ingress node 1 contributes 65.03 and 74.26 ms of the cluster increases of 88.09 and 98.61 ms. For two Slots on one remote Leader, that node contributes -30.32, +88.67 and +33.89 ms across the three pairs; the remote Leader contributes +10.29, +12.38 and +3.03 ms. These are whole-node integrals including background/scrape overhead, not per-request CPU.

The original 20-ms ownership sampler fetches a full `/metrics` response from the ingress. Four separate full-prefix profile fixtures preserve that sampler during the additional traffic or omit it, in the declared order below. In both sampler-on captures, the ingress CPU profile is dominated by metrics serving and gzip compression. Neither function receives a sample in the corresponding sampler-off capture:

| Frozen product / separate phase | Ingress sampled CPU | `/metrics` cumulative share | gzip deflate cumulative share | Ownership observations |
| --- | ---: | ---: | ---: | ---: |
| old / sampler on | 1,410 ms | 71.63% | 45.39% | 183 |
| candidate / sampler on | 1,070 ms | 75.70% | 55.14% | 153 |
| old / sampler off | 400 ms | 0% | 0% | 0 |
| candidate / sampler off | 290 ms | 0% | 0% | 0 |

These percentages are nested CPU-profile samples, not additive costs or native CPU integrals. Fixed four-second captures may cover only part of the 256-SEND traffic; memprofilerate=4096 applies throughout these fixtures. Darwin runtime/kernel sampling artifacts remain possible. The observations establish significant scrape work in these captures; they do not prove that it explains every original regression. Original acceptance still includes the entire scrape overhead. No subtraction or noise waiver is applied.

## Request boundary and misleading legacy stage names

The candidate's target timeline retains all 64 sequential requests at all three nodes. Its slowest SENDACK is 61.354 ms (`two-slots-one-remote-leader-c1-036`). Permission encloses 0.342 ms and ordered-append admission 0.004 ms; `replica.leader.local_durable` encloses 56.192 ms, followed by `replica.leader.quorum_wait` of 0.035 ms. These are diagnostic timings with full sampling, not acceptance replacements.

**The legacy stage names do not mean isolated local fsync and peer quorum wait in DurableQuorumLog mode.** Source inspection binds the first span from reactor task submission until `handleQuorumCommitResult` receives the exact quorum receipt. It includes queued/deferred task execution, local and peer durability and result delivery. The second starts after that completion and ends at reply publication. A short second span cannot rule out peer delay inside the first. Reported zero `queue_wait` is clamped elapsed timing, not proof of absent queueing. Nested spans must not be summed.

The positive capture therefore narrows the dominant wait to the submitted durable-quorum task, while excluding permission and ordered-append admission as the dominant spans of that request. It cannot distinguish local WAL sync, quorum peer persistence, physical commit publication or task scheduling inside that task. The next useful probe must bind exact proposal identities to those actual boundaries; the metrics CPU finding alone does not establish the latency cause.

## Frozen fixtures and validation

- Old runtime `424d03eb298b972ec572261d617972eecb5523c5`; candidate `c03fa6cc1712e886d9359822e23be716fdb4534e`, unchanged from the Darwin comparison. Same Go 1.25.11 native products and calibrated CPU probe; nodes and driver use GOMAXPROCS=4.
- Diagnostic driver `2975d2106` descends from the exact original `2a4a1f4e57a8cb3d5586f10bb1585717c37ea0ab`. Its E2E coverage assertion was written and run first: the original driver profiles the wrong same-Slot placement, so the outer coverage test fails while all four functional placements finish. The failed receipt/log and harness executable are preserved.
- All four original placements run in order, with the complete same-Slot prefix before the target. Three real nodes, 256 Hash Slots, 12 physical Slots, one metadata voter, three message voters, 32 devices, unchanged 64 sequential/64 burst windows and 32 ban/32 unban controls. A sampler-on profile phase adds the same bounded 20-ms public sampler and cancels/joins it at traffic completion; sampler-off omits only that added diagnostic phase sampler.
- Fixed order: old profile/on, candidate profile/on, old profile/off, candidate profile/off, candidate timeline, old timeline negative control. No retries or replaced fixtures. Four profile fixtures and the candidate timeline pass. All six retain exact functional receipts for all 24 placements: 417 target messages in profile fixtures, 161 in the others. Six CPU/allocation captures per profile fixture are unique and within four-second/8-second/8-MiB bounds. Timeline queries preserve the existing ring, privacy, identity, count and size bounds.
- The old timeline intentionally fails coverage: all 64 permission and all 64 append-wait spans are absent in that old product. Full ACKs, policy controls, placement and four exact 161-message histories remain available. It is not a passing timeline or performance result.
- All 18 owned node PIDs have exited. Both archives independently revalidate the 24 receipts, CPU tick identities, four ingress CPU profiles and original rejected verdict. Six native-counter and ten diagnostic-corruption controls reject invalid artifacts. The unchanged performance verifier still exits 1, with exactly six sequential rejects and twelve burst passes.

## Replay and limits

[Selected standalone evidence](2026-10-01-permission-sequential-remainder.tar.gz) and [archive identities](2026-10-01-permission-sequential-remainder-archive.json) retain all six diagnostics, six original unprofiled reports/verifier, four ingress CPU profiles, profile metadata, bound driver/source copies, failed coverage receipt and offline rederivation scripts. Eight other CPU files, twelve allocation files and the full failed-driver report/profile files/executables are retained in the larger local archive only; the selected bundle makes no allocation attribution claim.

Extract `verify-bundle.py` from either archive, then run `python3 verify-bundle.py /absolute/archive.tar.gz`. It verifies every member, rederives CPU/timeline/profile evidence and byte-reproduces the unchanged rejected verdict without a product process, module cache or network. Shared Darwin host, fixed ordering, diagnostic overhead and one-voter metadata limit inference. No new Linux capacity, HA, power-loss, performance repair, merge or release result is claimed.
