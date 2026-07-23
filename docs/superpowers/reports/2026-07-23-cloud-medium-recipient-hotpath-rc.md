# Cloud Medium Recipient Hot-Path Release Candidate

Date: 2026-07-23

Base: `origin/main` at `3d68536984a0205125a213834f63ebd216e8b0bc`

Failed cloud evidence: Run `gh-29989526911-1`

## Decision

**NO-GO for paid Cloud Medium validation.**

The integrated benchmark now includes the real offline plugin observer, bounded
plugin worker, and no-running-Receive-plugin usecase exit. It processes the
complete 5,000-message/s-equivalent 50ms recipient volume in a 5.85ms local
median in the original serial admission mode. A stronger full-slice burst gate
then exposed a deterministic cross-pool deadlock: all advance workers could
wait for append workers while append completions waited to resubmit into the
advance pool. The pre-fix candidate timed out after 20 seconds with both sides
of that cycle in the goroutine dump. A dedicated activation dispatcher breaks
the cycle; the same burst completes in a 5.72ms local median.

This is strong evidence for a repository-owned liveness defect that can produce
the observed multi-second queueing. It still does not include the real network,
Raft, Pebble, and socket backpressure. The final repository gates and independent
reviews passed, but that missing tail-latency coverage keeps paid validation at
NO-GO.

## Evidence And Scope

The failed run completed without failed workers but reached only 1761.09 ingress
messages/s and SENDACK P99 15.86s. Simulator headroom, process continuity,
recipient workers, channelappend handoff, Presence bulk lookup, ACK batching,
Slot scheduling, and append correctness were excluded. Node-2 allocation
evidence attributed about 312GB to recipient dispatch, 161GB to routed
conversation admission, 127GB to offline-recipient observation, and 76.7GB to
authority grouping. Runtime queue pressure averaged 0.839 and reached 1.0.

This candidate changes that confirmed path as one batch:

- one offline UID page now becomes one plugin-worker admission and one owned
  payload copy instead of one admission and payload copy per UID;
- no running Receive plugin returns before UID binding reads or wire mapping;
- a bound Receive plugin keeps ordered per-UID binding, dedupe, invocation, and
  independent timeout semantics;
- authority normalization uses an inline UID index for the normal 512-row page,
  and exact target grouping uses the 256 physical hash-slot index without
  weakening the 10 logical Slot or Raft fence semantics;
- exact-target delivery plans aggregate offline UIDs into one observer event per
  bounded plan instead of one event per physical authority target;
- a dedicated activation dispatcher is now the only blocking submitter to the
  advance pool, eliminating the advance-pool/append-pool circular wait while
  preserving configured writer concurrency;
- single-leader Presence target groups borrow the immutable caller slice, the
  production batch usecase avoids a discarded result allocation, and async
  delivery plans borrow capacity-limited grouping-owned recipient windows;
- Analysis exposes queue pressure by node, component, pool, queue, and priority.

## Deterministic Local Benchmarks

Apple M4, `GOWORK=off`, three samples. Values below use representative medians.
The plugin rows compare scalar and batch implementations in the candidate
revision. The authority and integrated rows compare captured `origin/main`
baseline output with the candidate output using the same benchmark definitions.

| Cloud Medium-shaped path | Before | Candidate | Result |
| --- | ---: | ---: | ---: |
| Plugin worker admission, 512 UIDs | 76.0µs, 524291B, 512 allocs | 0.833µs, 10496B, 2 allocs | 91x faster; 98.0% fewer bytes |
| Receive usecase, no plugin, 512 UIDs | 23.3µs, 12288B, 512 allocs | 0.035µs, 24B, 1 alloc | 666x faster; binding reads eliminated |
| Receive usecase, one bound plugin, 512 UIDs | 331.9µs, 1004168B, 5634 allocs | 238.1µs, 708137B, 2545 allocs | 28.3% faster; 54.8% fewer allocs |
| Authority snapshot/grouping, 250 messages and 19650 recipient rows | 2.240ms, 13.559MB, 6924 allocs | 1.527ms, 7.377MB, 2810 allocs | 31.8% faster; 45.6% fewer bytes |
| Presence client, 256 exact targets / 19650 UIDs / one leader | 16.51µs, 58448B, 24 allocs | 9.26µs, 10944B, 2 allocs | 43.9% faster; 81.3% fewer bytes; only aligned result allocations remain |
| Integrated routing/recipient feedback before adding plugin coverage | 7.44ms, 27.44MB, 66143 allocs | 6.15ms, 17.54MB, 47723 allocs | 17.3% faster; 36.1% fewer bytes; 27.9% fewer allocs |
| Final integrated feedback including plugin observer/worker/no-plugin exit | Not comparable: `origin/main` had per-UID plugin work and this benchmark did not include it | 5.85ms, 19.84MB, 49738 allocs, 130 plugin batches | complete 250-message / 19650-recipient volume; 8.5x under its 50ms window |
| Full-slice burst admission with advance/append pool pressure | Timed out after 20s; goroutine dump proved circular pool admission wait | 5.72ms, 20.43MB, 49898 allocs, bounded recipient inflight below capacity, 130 plugin batches | liveness restored; more than 3,400x below the pre-fix timeout bound |

The failed cloud scenario had no running Receive plugin, so the exact no-plugin
benchmark and final integrated path are directly relevant. The final candidate
kept recipient queue depth at zero and reduced the physical-target plugin
amplification to exactly 130 plan-level batches for the 130 group-message plans
that actually had offline recipients. Burst admission reached 32-51 concurrent
recipient workers in the primary samples and remained below capacity in
independent review samples without deadlock or queue loss. The 5.72ms local
median is not divided
into 250 and reported as production QPS. It proves the intended batching and
allocation changes plus removal of the circular scheduling wait. Independent
review concluded that this proof is not sufficiently faithful to justify a paid
cloud run.

## Verification

- Focused functional packages: passed.
- Focused race tests for plugin batch, authority grouping, recipient dispatch,
  and app composition: passed.
- Final integrated benchmark includes plugin observer, worker admission, and the
  real no-running-Receive-plugin usecase path: passed.
- Burst regression with one advance and one append worker: passed 20
  repetitions; focused race passed.
- Full Cloud Medium-shaped burst integration: passed five repetitions and
  reports exactly 130 plugin batches.
- Slow first Receive UID timeout does not starve the following UID: passed.
- `git diff --check`: passed.
- Repository Go gate with explicit roots and without root `./...`: passed on
  the final candidate.
- Independent Standards review: passed with no remaining finding after
  observer isolation, Presence empty-group alignment, dispatcher pressure
  publication, queue-tail clearing, and terminal-zero fixes.
- Independent Spec review: no implementation-semantic finding; paid cloud
  validation remains NO-GO because the harness does not cover real
  network/Raft/Pebble/socket tail latency.
- User `.gitignore` change remains outside this candidate.

The first repository gate exposed one test-harness-only timing race: the fake
start process could miss a two-second readiness window under full parallel
load. The test-only window is now ten seconds while its two-second
delayed-auto-join assertion is unchanged. The exact test passed ten repetitions,
then the full repository gate passed.

## Remaining Local Work

1. Land the reviewed cohesive correctness/performance batch through required
   PR CI without starting cloud resources.
2. Add a higher-fidelity local gate or gather additional repository-owned
   evidence that exercises the missing network/Raft/Pebble/socket backpressure
   and can project ingress and SENDACK P99 with meaningful margin.
3. Continue cohesive local diagnosis and optimization until the written
   projection supports a GO. Do not Provision merely because the current
   microbenchmarks and PR gates pass.
4. Only after a future GO: merge one cohesive PR with required CI and Nightly
   green, prove there are no live resources, and consider exactly one paid
   Cloud Medium run.

Cloud acceptance remains: completed/passed/exit 0, no failed worker, ingress
at least 4500/s, SENDACK error at most 0.01%, RECV error at most 0.1%, connect
error at most 0.01%, SENDACK P99 at most 1s, RECV P99 at most 2s, pure run,
7/7 continuous targets, no restart/OOM, simulator memory below 80%, and healthy
queue/cache/authority/handoff signals.
