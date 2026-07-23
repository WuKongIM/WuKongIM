# Cloud Medium High-Fidelity Router Release Candidate

Date: 2026-07-23

Base cloud revision: `3d68536984a0205125a213834f63ebd216e8b0bc`

Recipient hot-path merge base: `866af77636dc667178afc462fc76f9fec5bbb5b5`

Failed cloud run: `gh-29989526911-1`

## Decision

**GO for one cohesive PR and exact-merge-SHA Nightly validation.**

**NO-GO for paid Alibaba Cloud Provision until that PR is merged, its required
CI and exact-SHA Nightly are green, and a fresh inventory check proves no live
resources.**

The local release candidate now exercises three real `cmd/wukongim` processes,
WKProto TCP sockets, Raft, Pebble, 256 physical hash slots, 10 logical Slot
groups, three replicas, mixed online/offline fanout, plugin receive batching,
and public pressure metrics. The exact old revision cannot complete the cold
prime, while the candidate sustains the 4,500/s acceptance load with both
latency limits and queue conservation satisfied. A 5,500/s diagnostic point
preserves SENDACK headroom but reaches the local receive-tail boundary, so it
is evidence of slope and margin rather than a claim about absolute cloud
capacity.

## Baseline Identity And Terminal Failure

The verified base binary was rebuilt from a tracked-clean worktree whose HEAD
was exactly `3d68536984a0205125a213834f63ebd216e8b0bc`.

- binary SHA-256:
  `583388f2757382fea6ece004e47696a51adf9b771d8769b2c46ca95e3306bf86`;
- the old singular `pluginReceiveObserver.ObserveOfflineRecipient` symbol was
  present;
- the batched `pluginReceiveObserver.ObserveOfflineRecipients` and
  `EnqueueReceiveBatch` symbols were absent.

The same current-revision harness launched that binary with the exact
256/10/3 runtime shape. Warmup succeeded in 143ms. The 250-message cold prime
then timed out after five seconds while reading the first SENDACK:

- gateway accepted all 250 prime SEND records;
- local channelappend admission was `1`;
- successful SENDACK was `1`;
- downstream recipient, append, and post-commit queues were empty;
- the only foreground Router goroutine was blocked in the serial chain
  `ResolveAppendAuthority -> EnsureChannelMeta -> Slot/Raft Propose`.

The base therefore never entered the measured phase. This is a terminal
relative failure, not a low sample that is compared numerically with candidate
throughput.

An earlier temporary binary that did not carry a provable exact-revision
identity is excluded from all evidence in this report.

## Root Cause And Fix

`Router.resolvePending` grouped items by canonical channel but resolved every
independent channel sequentially. A cold Cloud Medium-shaped batch has 129
canonical channels. First-use metadata creation can require a Slot-owned Raft
proposal, so one gateway batch multiplied independent Slot round trips into
cross-channel head-of-line blocking.

The release candidate:

- resolves independent canonical channels with a fixed per-batch bound of 16;
- preserves one resolve per canonical channel;
- stores results by original channel index before folding groups;
- keeps item-aligned success, error, retry, and invalidation behavior;
- separately submits resolved channel groups with the existing bounded worker
  primitive, now also defaulting to 16;
- keeps same-leader remote outbound lanes serialized by their configured
  per-node limit;
- creates at most `bound-1` helper goroutines per phase and no new cross-request
  queue.

The production `AuthorityResolver`, local submitter, and remote forwarder
contracts explicitly require concurrent-call safety. Focused race tests cover
the Router path.

## Deterministic Router Benchmark

Apple M4, three samples, `-benchtime=3x`. The benchmark models the exact
250-message cold slice: 125 person channels plus four group channels, for 129
independent authority resolutions. Submission remains serial so the comparison
isolates authority resolution.

| Resolve bound | Time/op range | Bytes/op range | Allocs/op range |
| --- | ---: | ---: | ---: |
| 1 | 33.30-33.58ms | 551186-551208 | 2397 |
| 16 | 2.55-2.63ms | 554981-558149 | 2434-2443 |

The bounded candidate is approximately 12.9x faster for the modeled cold
resolution stage. The extra allocation is below one percent and is dominated
by the bounded worker coordination.

## Three-Process High-Fidelity Evidence

The accepted run used 20,000 messages, 1,572,000 recipient rows, 243,600 online
routes, 250 connections, and exactly 10,400 offline plugin batches.

| Signal | 4,500/s acceptance | 5,500/s diagnostic |
| --- | ---: | ---: |
| Actual ingress | 4500.20/s | 5500.10/s |
| Completion rate | 4098.97/s | 4036.20/s |
| SENDACK P99 | 805.91ms | 865.84ms |
| RECV P99 | 1110.45ms | 2048.43ms |
| Gateway queue max | 0.67% | 0.56% |
| Recipient queue max | 0% | 0% |
| Recipient worker max | 6.25% | 16.88% |
| Post-commit backlog max | 1096 | 2393 |
| Allocated bytes | 6.61GB | 6.56GB |
| GC cycles | 87 | 86 |
| Plugin batches accepted/invoked | 10400/10400 | 10400/10400 |
| Plugin full/closed/error | 0/0/0 | 0/0/0 |
| Drained / process-continuous | true/true | true/true |

At the exact acceptance point, actual ingress has no pacing deficit, SENDACK
P99 has 19.4% margin, and RECV P99 has 44.5% margin. The 5,500/s point is 22.2%
above target and still keeps SENDACK P99 below one second, but RECV P99 is 48ms
above the two-second cloud criterion. This bounds the local knee instead of
overstating it.

## Gate And Review Results

- Router functional and result-alignment tests: passed.
- Router focused race tests: passed.
- E2E Prometheus parser focused race tests: passed.
- Machine acceptance contract, including exact schema, 256/10/3 topology,
  message/recipient/route/connection shape, latency, queues, plugin
  conservation, drain, and process continuity: passed.
- Bounded three-process acceptance run: passed in about 66 seconds.
- Nightly workflow contract: passed.
- `git diff --check`: passed.
- Explicit-root repository Go gate, without root `./...`: passed.
- Standards review: no documented-standard breach or unresolved code smell.
- Spec review: the initial acceptance function did not explicitly reject a
  changed round/topology shape; the contract was tightened and its negative
  cases now pass. No remaining semantic finding.

The higher-fidelity scenario remains opt-in E2E coverage and runs as a separate
five-minute-bounded Nightly step. It does not lengthen ordinary unit tests.
The user's `.gitignore` change is intentionally excluded from the candidate.

## Exact-SHA Nightly Calibration

PR #625 merged as `da46d3b2c6523322cb499942d72929ed444f52ca`.
Exact-SHA Nightly run `30013189801` passed smoke, integration, all four race
groups, and the existing full E2E suite. Its new Medium step completed all
20,000 messages with healthy product signals but failed the original harness
contract:

- actual paced ingress was 4499.694/s, only 0.0068% below 4500/s;
- SENDACK P99 was 429ms;
- plugin batches were exactly 10400 accepted and 10400 invoked with zero
  rejection/error;
- queues drained and all processes remained continuous;
- the shared two-core runner completed online fanout at about 1160/s, so the
  deliberately oversubscribed three-node single-host test produced a 12.75s
  RECV P99.

The failure classified the absolute CI acceptance rule as a harness defect.
The runner is evidence for relative behavior, conservation, and regressions;
it is not a Cloud Medium capacity substitute.

The corrected Nightly contract pins a 1000/s CI-scaled rate while retaining the
full 20,000-message, 1,572,000-recipient-row, 256/10/3 shape and the same 1s
SENDACK / 2s RECV limits. Pacing accepts at least 99.5% of the fixed offered
rate so sub-millisecond scheduler jitter is not classified as product failure.
It also enforces:

- no gateway/recipient queue saturation;
- exact plugin admission/invocation conservation;
- at most 360,000 product-path allocation bytes per message plus a bounded
  30MB/s background-runtime allowance;
- at most 0.0075 GC cycles per message and at most 512MiB heap;
- complete drain and process continuity.

Prometheus sampling changed from 100ms to 250ms. The earlier interval caused
the three in-process metrics encoders to contribute about 1.5GB of additional
allocation during the slower 20-second CI phase. The new interval still
collects about 240 cross-node snapshots without dominating the workload.

The local CI-scaled proof passed with 1000.05/s ingress, 499ms SENDACK P99,
431ms RECV P99, 104ms completion drain, 243 metric samples, 7.59GB total
allocation, 117 GC cycles, zero queue rejection, exact 10400/10400 plugin
conservation, and continuous processes.

The first exact-main run of that 1000/s correction (`30015680979`) then exposed
remaining runner variance rather than a send-path regression. SENDACK P99 was
122ms, but the shared runner completed receive fanout at only 951/s. Sustained
1000/s input accumulated a 928-record post-commit tail, extended drain to
1027ms, and pushed RECV P99 to 3.47s. All queues still drained, plugin
conservation remained exact, and processes remained continuous. A branch pass
followed by an exact-main failure is not an acceptable paid-cloud gate.

The CI rate is therefore fixed at 500/s, about 47 percent below that worst
observed receive completion rate, while retaining the 1s SENDACK and 2s RECV
limits. Its sampler interval is 500ms so the doubled 40-second phase retains
roughly the same 240 cross-node samples and allocation interference as the
20-second 1000/s calibration. Normal local acceptance remains fixed at 4500/s
with 250ms sampling.

Three consecutive local 500/s probes then produced 500.02/s ingress,
SENDACK P99 357-432ms, RECV P99 236-343ms, 48-72ms drain, post-commit backlog
21-27, and exact 10400/10400 plugin conservation. Their raw allocation was
403,396-405,594 bytes/message. The prior fixed per-message ceiling incorrectly
treated the doubled phase's background runtime as product-path work. The final
allocation rule therefore separates a 360,000-byte/message budget from a
bounded 30MB/s allowance over the fixed 40-second paced duration; at 500/s this
permits 420,000 bytes/message, leaving only about 3.4 percent margin over the
worst repeated observation. Actual drain time cannot enlarge this allowance.

## Remaining Cost Gate

1. Land the CI-scaling harness correction without the user's `.gitignore`
   change.
2. Require the branch Nightly to pass before merging the harness correction.
3. Run Nightly again on the exact merged main SHA and require the bounded Cloud
   Medium evidence artifact to pass.
4. Recheck Monitor/Cleanup/provider inventory and prove there is no live run or
   resource.
5. Only then change paid validation to GO and dispatch exactly one Cloud Medium
   30-minute Provision.

No Alibaba Cloud resource is started by this report.
