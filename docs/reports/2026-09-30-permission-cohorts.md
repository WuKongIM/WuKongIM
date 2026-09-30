# Issue #977: bounded fresh permission fact cohorts

## Result and limits

The candidate reduces repeated SEND permission envelopes and per-Slot fresh
barrier calls across independent concurrent callers. Functional, cancellation,
close, backpressure and exact-history checks pass. This is a work-count result;
it is not a demonstrated CPU cause, capacity repair or tail-latency improvement.
The latest strict no-profile pair regresses same-slot remote SENDACK p99 from
443.033 to 526.973 ms (+18.95%). Performance acceptance remains incomplete.

Implementation source is clean `d1deec2b3bc916764699ad9fcf6b9a590c183d10`;
its production Go files are identical to clean `6a14221426a418c331aea7cbee596b5350b802ab`.
The frozen old product is `424d03eb298b972ec572261d617972eecb5523c5`.
The experiment depends on the [fixed baseline](2026-09-30-permission-cohort-baseline.md)
from [PR #980](https://github.com/WuKongIM/WuKongIM/pull/980), which is still unmerged.

## Behavior and ownership

The existing node-owned Slot proxy admits callers for a fixed 1 ms collection
window, seals membership, deduplicates raw facts, then uses the existing router,
RPC and reader. Each physical Slot retains its own route fence, fresh ReadIndex,
durable apply wait and snapshot. Results return at the original caller/index;
policy decisions, successful facts and allow results are never cached between
cohorts. Later arrivals require another fresh read. Existing stale-group retry
and independent Slot failure semantics remain unchanged.

Each cohort holds at most 64 calls and 4096 input facts. Each Store bounds
collecting/executing cohorts to 64, retained calls to 1024 and conservative
memory credits to 16 MiB, reserving before copying inputs. There is no extra
execution queue. The longest still-live sealed deadline determines shared work,
capped at 30 s; individual cancellation/deadlines still control each response.
Canceled members retain credit until their work joins; final cancellation joins
before returning. Node close fences admission and joins cohorts before transport
and storage close. Receiver admission remains a separate ownership domain.

Fixed counters and `wukongim_message_permission_cohort_owned` expose work,
busy, calls/cohorts and retained-memory credits. Grafana panels distinguish these
credits from actual allocator memory and undecoded receiver queue bytes.

## Matched observations

All windows contain 64 SENDs, two mandatory facts per caller, 32 distinct TCP
sessions and two joined 32-caller waves. Plans/facts remain 64/128. Both versions
use the same harness SHA-256 `5525a1d641cd0e01a5a6444d22e8003b8c3fd9e5d203c392613de16d5037dd45`.
Three processes run on the same darwin/arm64 host, GOMAXPROCS=4 per node/driver,
256 Hash Slots, 12 physical Slots, one metadata voter per Slot and three message
replicas. Actual placement, clean binary/source/config identities, per-node
cuts, every ACK and complete history are retained in the evidence bundle.

| Pair / placement | Remote envelopes old → new | Barrier calls old → new | SENDACK p99/max ms old → new |
| --- | ---: | ---: | ---: |
| 1 / same-slot-remote | 64 → 3 | 64 → 3 | 582.816 → 511.869 |
| 1 / two-slots-one-remote-leader | 64 → 2 | 128 → 4 | 602.887 → 311.823 |
| 1 / two-remote-leaders | 128 → 4 | 128 → 4 | 596.298 → 334.216 |
| 1 / two-slots-local-leader | 0 → 0 | 128 → 8 | 610.153 → 341.778 |
| 2 / same-slot-remote | 64 → 2 | 64 → 2 | 422.724 → 403.931 |
| 2 / two-slots-one-remote-leader | 64 → 2 | 128 → 4 | 519.146 → 376.140 |
| 2 / two-remote-leaders | 128 → 4 | 128 → 4 | 405.786 → 305.957 |
| 2 / two-slots-local-leader | 0 → 0 | 128 → 4 | 360.864 → 354.102 |
| matched 3 / same-slot-remote | 64 → 2 | 64 → 2 | 443.033 → 526.973 |
| matched 3 / two-slots-one-remote-leader | 64 → 2 | 128 → 4 | 391.982 → 380.102 |
| matched 3 / two-remote-leaders | 128 → 4 | 128 → 4 | 386.022 → 387.254 |
| matched 3 / two-slots-local-leader | 0 → 0 | 128 → 6 | 400.960 → 409.001 |

Local envelopes fall from 64 to 4/2/3 respectively. Sequential windows still
perform one new envelope and independent per-Slot barriers per caller.
Every measured window has zero failed barriers/busy and drained ownership;
completed ban/unban controls preserve zero identifiers for rejection and exact
committed history. With 64 samples nearest-rank p99 equals max. These unpaced,
single-Channel diagnostic bursts do not replace the 500 SEND/s qualification.

The originally planned third pair is also retained: only the candidate added a
256-SEND/two-second profiling phase after case two, before the remaining primary
cases. Its local p99 is 584.490 versus 340.920 ms (+71.44%); later-case workload
history is not matched. A separate no-profile pair above corrects this comparison
design, without deleting the original observation or selecting a best window.
The strict correction still contains regressions; profiling is not a proven cause.

All three nodes have CPU/RSS/allocation samples in each matched cut. Summed
allocation deltas span 74.503–111.995 MB old and 62.802–91.500 MB new, including
an increase in matched same-slot (+1.89%) and pair-2 local (+2.12%). These are
whole-node, scrape-affected/cached counters, not permission-only allocations.
Latest periodic CPU gauges are not window integrals; Darwin has no standard
process CPU-seconds counter. Public 20 ms ownership sampling runs identically
for both products, at most 200 samples and always canceled/joined. Candidate
observed peaks reach 32 calls, one cohort and 77,696 credit bytes; missed
short-lived work remains a lower bound, and old gauges remain absent. Receiver
queue-owned bytes remain unknown. Sparse separate CPU/heap profiles establish
neither a latency cause nor the earlier 4500 SEND/s capacity repair.

## Validation and retained failures

- Old clean product fails the new real-process reduction assertion: all four
  layouts still produce the full 64/64/128 remote or 64 local envelopes.
- Controlled production-proxy/codec/database integration with race detection
  passes dedup/alignment, separate Slot barriers, late-arrival freshness,
  deadline/cancel isolation, last-cancel/close join and all ownership bounds.
  Credit assertions keep two members charged while a canceled member returns
  and another's barrier remains blocked. Controlled-port JSON does not identify
  a source revision; it is not clean-binary process or real-Raft proof.
- Full send_ban process E2E passes in 370.852 s, including single-node/three-node
  cluster policy matrices, all ingress, no delivery/history for rejection,
  non-replica ingress, leader transfer, parallel calls, priority, concurrent
  metadata and write recovery. Independent 100k opt-in was not run this phase.
- The original simultaneous 192-call fault no longer reaches saturation and
  fails its positive-busy assertion. The revised fault keeps 192 requests but
  releases them 5 ms apart: all return HTTP 503 with zero identifiers, 64 remote
  envelopes occupy receiver execution, 128 calls receive ingress cohort busy,
  both budgets drain and history contains only warm/recovered successful IDs.
  This fault fixture changes no performance workload or acceptance threshold.
- Named `go-unit`, `go-vet`, `go-format`, `go-mod-tidy`, `flow-doc-contracts`
  and `workflow-contracts` pass. Initial failures and corrections are retained:
  missing new-metric Grafana panels; existing slab-test sync.Pool copying
  reproduced on frozen baseline; SDK loopback 502 fixed by removing inherited
  proxy variables only from the child test environment.
- Named `docs-contracts` fails on candidate and unchanged frozen baseline:
  transport catalog omits existing RPC 91, Product HTTP contract lists 44
  registrations versus runtime 48. No new RPC or HTTP interface is introduced
  here. This unrelated baseline drift remains unresolved and recorded.
- Standards and Spec code reviews each have zero confirmed findings. Fresh
  automatic Linux three-node correctness and all three 500 SEND/s seam verdicts
  are pending publication of this candidate; no gate is relaxed or retried.

[Evidence bundle](assets/permission-cohorts-evidence.tar.gz) preserves raw JSON,
logs, profile companions and both failed and passing observations.
[Manifest](assets/permission-cohorts-manifest.json) binds each file/binary hash,
matched comparison resource coverage and known limitations.
[Review](2026-09-30-permission-cohorts-review.md) keeps the two review axes separate.
Reproduction uses the opt-in commands in [scenario instructions](../../test/e2e/message/send_ban/AGENTS.md)
and [failure-first plan](../superpowers/plans/2026-09-30-permission-cohorts.md).
Issue #977 and the earlier failed 4500 SEND/s qualification remain open.
