# Cloud Medium Channel RPC Release Candidate

Date: 2026-07-24

Base revision: `5eda8b3d9b67d4473bfce4044ab66bacd815c7f2`

Failed cloud run: `gh-30033893599-1`

## Decision

**LOCAL GO for one cohesive PR.**

**NO-GO for paid Alibaba Cloud Provision until the PR is merged, exact-main
CI and Nightly are green, and a fresh inventory check proves that no live
resources exist.**

The candidate removes the observed saturated boundary without increasing the
bounded worker count and passes the real three-process Medium-shaped
acceptance scenario at 4,500 messages/s. The remaining gates are repository
delivery gates, not missing product-path evidence.

## Released Baseline And Diagnosis

The failed run was finalized by Cleanup workflow `30039891674`. Independent
Analysis workflow `30040211493` proved `provider.state=released` and
`resources.length=0`.

The measured cloud window completed 5,829,500 sends and all 10,500 connects,
but reached only 3,238.61 messages/s and a 7.198s SENDACK P99. RECV P99 was
46.244ms. All seven targets and all three WuKongIM processes were continuous,
OOM remained zero, and simulator CPU and memory peaked at 57.13% and 22.38%.
Recipient workers, channelappend handoff, append correctness, Slot apply, and
PreferredLeader reconciliation were healthy.

The bounded Channel replication RPC pool was the saturated boundary:

- runtime queue pressure repeatedly reached 100%;
- Gateway SEND backlog peaked at 1,335;
- the Medium contract fixed Channel RPC workers at 50;
- one node owned four actual logical Slot leaders while the other nodes owned
  three each;
- Pull/PullHint work used batches of four and blocked those bounded workers.

As a diagnostic correlation, using the measured 61.75ms service time and the
old observed batch of four gives:

`50 workers * 4 items / 0.06175s = 3238.87 items/s`

That differs from measured ingress by only 0.01%. It is strong corroboration
of the saturated-pool diagnosis, but it is not used as an ingress-capacity
proof: Pull and PullHint are grouped separately by kind and target, and their
task amplification is not one item per accepted message. The acceptance proof
is the real 3/4/3 three-process scenario below.

## Cohesive Remediation

- Channel Pull and PullHint use a configurable bounded batch, defaulting to
  eight; the Cloud Medium sealed runtime contract explicitly requires eight.
- The worker bound remains 50. The change does not add unbounded goroutines or
  increase remote-call concurrency.
- Collected RPC target groups rotate their first subgroup across handler
  batches, preventing a less-common target from repeatedly occupying the tail
  when actual logical Slot leadership is distributed 3/4/3.
- Configuration loading, normalized effective-value reporting, example files,
  deployment templates, sealed bundle, Bootstrap Gate, and contract tests all
  carry the same value.
- Analysis documentation exposes the existing bounded
  `runtime_queue_pressure_by_pool` query so an agent can retrieve historical
  pressure by node, component, pool, queue, and priority without introducing
  high-cardinality runtime labels.
- The Medium E2E acceptance harness now derives expected totals from its
  configured bounded round count; a short diagnostic run can no longer pass
  the workload and then fail against a hard-coded 80-round total.

The batch value is acceptance-driven by the real three-process evidence below,
not by equating ingress and RPC items. The focused benchmark only establishes
the isolated batching gain with the same worker bound.

## Deterministic Before/After Evidence

Apple M4, three samples, 800 Pull tasks, the same 50-worker bound:

| Signal | Batch 4 | Batch 8 | Result |
| --- | ---: | ---: | ---: |
| Throughput | 27,934-36,769 items/s | 56,551-61,008 items/s | about 89% higher median |
| RPC calls/op | 200.0 | 100.0-101.0 | about 49.9% fewer |
| Queue-wait P99 | 17.21-26.01ms | 5.77-9.19ms | about 75% lower median |
| Bytes/op | 2.130-2.182MB | 2.101-2.147MB | about 1.3% lower median |
| Allocs/op | 7,512-8,287 | 5,843-6,315 | about 22% lower median |

A queue-prefilled deterministic regression test verifies that batch eight
uses 101 calls for 801 items versus 201 calls for batch four with the same
worker bound. A separate deterministic test proves subgroup priority rotates
under a topology-derived 4/3 target skew.

## Three-Process High-Fidelity Acceptance

The bounded E2E scenario ran three real `cmd/wukongim` processes with WKProto
TCP sockets, Raft, Pebble, 256 physical hash slots, 10 logical Slot groups,
three replicas, and the actual 3/4/3 leader distribution.

- actual ingress: 4,500.85/s;
- SENDACK P99/max: 900.46ms / 932.19ms;
- RECV P99/max: 1,238.81ms / 1,367.76ms;
- messages: 5,000;
- recipient rows: 393,000;
- online routes: 60,900;
- connections: 250;
- maximum gateway/recipient/worker queue ratios:
  0.00653 / 0 / 0.0625;
- processes continuous: true;
- queues drained: true;
- metric errors: zero;
- maximum heap: 154,768,464 bytes;
- allocated bytes: 1,647,717,000;
- GC cycles: 22.

This gives 10.0% SENDACK P99 margin and 38.1% RECV P99 margin at the absolute
Cloud Medium ingress requirement on the local machine.

## Verification

- Focused functional and capacity tests: passed.
- Focused race tests: passed.
- Medium-shaped three-process acceptance: passed in 70.66s.
- Configuration, sealed contract, Bootstrap Gate, and script tests: passed.
- Acceptance rejects fewer than 4,500 actual ingress messages/s at the
  Cloud Medium point and requires actual logical Slot leaders to be distributed
  3/3/4 across the three nodes.
- `git diff --check`: passed.
- Explicit-root repository Go gate, without root `./...`: passed.
- User `.gitignore` change remains outside this candidate.

Independent Standards and Spec reviews, PR delivery, exact-main CI/Nightly,
and the final no-live-resource proof remain mandatory before changing the paid
cloud decision to GO.
