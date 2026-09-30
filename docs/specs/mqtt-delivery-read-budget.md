# MQTT delivery read budget diagnosis

## Measured result

Two sequential diagnostic runs at `5dfa36d8871c0c606246f610c4c80339f1360bec`
keep 500 persistent clients, twenty publications, 256 hash Slots, twelve initial
Slot groups and all original assertion/deadline/worker settings. Only member
preparation is narrowed to 2,000. Fresh Slot barrier waits account for about
84% of aggregate delivery execution time in both measured steady windows.
WindowAdmission takes 56–57%, Accounting 29%, and exchange recovery 3–4%.
These are measured costs, not a verified performance repair or a capacity claim.

| Corrected probe | Steady window | Accepted enqueues | Window / Accounting / recovery share | Online barriers per enqueue | Scenario outcome |
| --- | --- | --- | --- | --- | --- |
| v2 | 155.284 s | 8,443 | 55.56% / 28.64% / 4.12% | 39.24 | Initial three-minute receipt timeout; all 500 clients open, 8,821 receipts |
| v3 | 115.214 s | 8,183 | 56.80% / 29.38% / 3.04% | 29.13 | All 10,000 initial receipts in 141.029 s; round 2 unsubscribe fails, two public unsubscribe/conflict closures |

Window read barriers, including its nested authorization, account for about
74% of its wall time. The v3 steady window has 8,610 window attempts: 8,183
successes, 48 conflicts and 379 typed Channel backpressure errors. All 17,219
exchange-recovery calls in that window return no delivery. The v2 `other`
error bucket remains unclassified; v3 cannot identify those earlier errors.

The original-size v1 probe confirms 100,000 members but fails initial SUBSCRIBE
before fanout. Its read attribution is invalid because Owner admission derives
a new dependency context; incoming labels are lost. A 2,000-member v1 run fails
fanout with eight closed incomplete clients. Preserve both failures, but do not
use their read attribution. v2/v3 explicitly carry only the diagnostic marker
across `Subscriptions.begin`, retaining the original Owner context and deadline.

[Exact commands, binary/source hashes, instruction digests and measurement limits](../reports/mqtt-delivery-stage-diagnostic/provenance.json).
[Reproduction instructions and bounded cumulative artifacts](../reports/mqtt-delivery-stage-diagnostic/README.md).
The earlier original-size all-clients-open timeout and ordinary-binary failures
remain in [the fanout inventory](mqtt-fanout-closure.md).

## Hypotheses and measurement failure inventory

Before probing, rank four falsifiable predictions: repeated authority reads
dominate Accounting/window execution; worker-queue delay exceeds execution;
empty recovery scans dominate delivery; or authorization/Channel reads dominate
Session metadata reads. Fixed stage clocks, actual ReadBarrier counts/duration,
queue clocks and typed errors distinguish them. Existing bounded CPU/goroutine
profiles motivate this probe; no profiler runs during these measurements.

- Context replacement can silently misattribute reads. Verify stage labels at
  the actual barrier, classify untagged calls explicitly and preserve the Owner
  operation's cancellation and original minimum deadline.
- Nested clocks double-count work. Compare disjoint top-level stage durations;
  authorization is included in its enclosing Accounting/window/final stage.
  Node plan clocks include their fresh metadata reads. Do not sum both.
- Queue delay counts waiting clients, not consumed worker time. Record due-to-pop,
  SubmitWait, pop-to-worker and execution separately. Here mean queue time is
  about one execution duration; due lateness grows with the fair rotation over
  500 owners. This does not establish an independent scheduler defect.
- Failed attempts inflate cost per success. Retain fixed result classes and
  normalize to accepted `DeliveryQueued` enqueues, including failed work in the
  numerator. An accepted enqueue is distinct from Paho receipt or durable ACK.
- Independent cumulative snapshots skew boundaries. Record each component's
  exact timestamps and retain raw counters/histograms. Counts, duration and
  results are separate atomic loads. A utilization ratio slightly above one
  is snapshot skew, not proof of more than sixteen workers.
- Preparation/cold anchor work can distort steady attribution. Exclude fifteen
  seconds at the start and ten at the end of fanout for the reported window;
  preserve complete cumulative records and exact failure/receipt artifacts.
- Diagnostic overhead and a smaller group cannot qualify capacity. The overlay
  changes no product source, authority, concurrency, retry, deadline or expected
  receipt. It adds fixed-size atomics, clocks, context markers and five-second
  aggregate stderr snapshots, whose overhead has not been separately measured.
- Concurrent local workload can confound results. Run one task-owned workload
  at a time; finish builds/tests before it. Do not stop unrelated processes.
- Lifecycle/control failures remain separate. v1 closures, the original-size
  SUBSCRIBE timeout and v3 unsubscribe/conflict origins are not localized by
  stage timing and cannot justify generic retries.

## Next implementation boundary

Prioritize a bounded compound Channel replay plan/original-content read.
`readAnchoredOriginals` currently makes an outer fresh placement read, a replay
plan, an anchored original read and a final fresh placement read. Inside the
Channel service, an anchored plan makes three fresh runtime-metadata reads
(including the recheck after native committed propagation), and the original
reader makes two. The measured window averages about 4.95 of these five service
reads per attempt. These Channel-owned checks remain independent of Session
and source-binding ownership even on one node.

A compound operation may share its captured fresh serving authority across
planning and original reads, with a final fresh recheck after all admitted work.
It must retain native committed propagation and the existing stable-fence,
anchor, retention and generation proofs. Do not cache authority between turns
or remove final receive authorization. First implement a regression seam for
the actual routed plan/read chain, then establish a sequential ordinary-binary
comparison before adopting any optimization. No compound operation is implemented
or proven faster by this report.

Failure cases to write **before** that implementation:

- No anchor, stale generation, a lower accepted prefix, an anchor outside the
  accounted range, corrupt content identity/hash and retired/missing originals
  yield no delivery or fabricated empty success.
- Slot mapping, leader, term, Channel epoch, route generation or write-fence
  changes before, during or after plan/read invalidate the compound result;
  an unavailable current owner never falls back to replica-local metadata.
- Native propagation remains bounded and precedes the final authority check;
  lost/failed propagation cannot claim follower coverage or source release.
- The original reader's four-slot admission, byte/page bounds and owned content
  remain intact; pressure grants no inline generic retry or retained body queue.
- Cancellation, expired/fenced Owner, changed options, revoked membership, quota
  ending and concurrent unsubscribe keep the existing admission/send behavior.
- Old exchanges recover before new admission, including across unsubscribe;
  uncertain window writes require existing recovery rather than another send.
- A typed peer request preserves complete request/result fences; an older or
  malformed peer fails closed rather than accepting incomplete evidence.
- Retention/replay progress overlapping the compound read preserves one exact
  captured anchor and original-content validation, never a mixed prefix.
- Tests count real authority calls and compare exact ordered public receipts.
  Smaller probes or fewer reads alone do not replace the unchanged full
  100,000-member/500-connection/twenty-message/churn acceptance.

Session cursor/subscription batching remains a possible smaller seam, but these
measurements do not justify prioritizing it over the repeated Channel authority
checks. The previously rejected Accounting-deferral experiment remains rejected.
