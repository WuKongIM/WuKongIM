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

## Remaining Cost Gate

1. Commit the cohesive Router plus high-fidelity-gate batch without the user's
   `.gitignore` change.
2. Open one PR, wait for every required check, and merge.
3. Run Nightly on the exact merged main SHA and require the bounded Cloud Medium
   evidence artifact to pass.
4. Recheck Monitor/Cleanup/provider inventory and prove there is no live run or
   resource.
5. Only then change paid validation to GO and dispatch exactly one Cloud Medium
   30-minute Provision.

No Alibaba Cloud resource is started by this report.
