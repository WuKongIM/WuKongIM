# Physical commit membership explains another durable wait boundary

The unchanged 5000-channel / 4500-SEND/s / cap32 WAL-fault loop failed at
26.482972012 seconds with EOF, 119173 SEND calls, 6370 pending messages and zero
permission-admission busy. This diagnostic directly maps exact prepared proposal
commands to logical commit requests and physical batches. It establishes actual
batch membership, closing the gap left by same-GID interval observations in the
[ownership report](2026-09-28-send-ban-ownership.md).

The effective physical coordinator MaxRequests/MaxRecords/MaxBytes are all zero
(no explicit positive group limit). The slow groups contain only 11–400 records,
with zero or one queued logical request remaining after collection. Meanwhile,
each node has 128 observed unfinished quorum workers and hundreds of accepted
quorum tasks without a worker-begin event. Thus raising a physical group cap is
unsupported: the observed backlog has not all crossed the upstream execution
boundaries. These facts identify product-side delay amplification under the
controlled perturbation; they do not establish the cause of natural disk stalls.

## Frozen scope and validation

Source revision: `5a9201b50`. No product source or public configuration changed.
The private overlay extends the previous ownership/request/manifest probes with:

- A positive process-local request token allocated at message mutation submission.
  Each prepared proposal emits its full fixed CommandID with that token.
- A physical batch token, all member request tokens, logical request/record/byte
  counts, remaining coordinator queue count and existing group limits.
- Begin/end events around the original physical `commitFunc`, called exactly once.
  Returned error/success is explicit; it does not imply Publish also succeeded.

No payload, partition, UID, channel text or arbitrary error enters these events.
Only enabled runtime tracing emits them; the existing flight recorder and output
caps bound retained evidence. Native stream extraction remained below its fixed
262144-event / 96MiB derived-output bound per process.

Serializer failure contracts and tests preceded implementation and failed RED for
missing helpers. GREEN and race10 passed. Existing commit and message storage
package suites passed. Three offline test methods cover valid/invalid schemas,
exact membership, missing results and duplicate identities/endpoints. Physical
membership is not inferred from clocks or goroutine overlap.

Binary SHA-256:
`6f113ae3d8dbe5c3cd1d6658758b49a970f6d4322919dc08e910e9a2f56be91a`.
The same three-node Docker lab, 128 append workers, 256 Hash Slots and fresh data
were used. No builds, offline trace decoding or cleanup overlapped measured SEND.
No OOM or disk guard fired; all processes reached terminal state. This is one
fault diagnosis, not a new passed control or performance comparison. The prior
zero-hold permission-admission failure remains open.

## First full shard and exact commands

| Ingress | Awaiting result /256 | Ready/unpublished | Published/fences | Oldest ordinal | Worker submit→begin |
| --- | ---: | ---: | ---: | ---: | ---: |
| node1 | 256 | 0 | 0 | 112760 | 318.666ms |
| node2 | 254 | 2 | 0 | 112755 | 319.067ms |
| node3 | 255 | 1 | 0 | 112756 | 318.660ms |

Each oldest record was about1421.3ms old, preparation-returned but result-pending.
All three commands were led by node2; Commit lock acquisition was1.664–2.048µs.
All reserved/retained observations were256, residuals0, overflow=false, subject to
the documented non-atomic counter/record observation boundary.

Full commands and all endpoints are in
[request joins](assets/send-ban-physical-20260928/fault-380/oldest-request-joins.json).
The exact physical join shows:

- On node2, commands112760/112756 belong to logical request78011; command112755
  belongs to request78009. All are physical batch78021, containing12 requests and
  388 records. Submission→collection took378.223/382.359ms, build/start interval
  1.710ms, and physical commit380.792ms.
- On node1 all three are request77652 in batch77684:36 requests,100 records.
  Submission→collection380.978ms; physical commit has no end at capture.
- On node3 all three are request78158 in batch78186:31 requests,97 records.
  Submission→collection384.036ms; physical commit has no end at capture.

The follower calls are unfinished, not zero or inferred complete durations. This
confirms an extra physical-commit generation in the selected quorum path, rather
than an undocumented mandatory local→peer→HW sequence. Local and preferred peer
submission still execute independently in the product.

## Physical groups and upstream occupancy

The following lists every retained physical batch with complete duration≥100ms
or an observed begin without end. Selection is by physical call duration, not an
assertion that one trace call equals a unique intercepted fdatasync.

| Node | Records in successive slow/unfinished batches | Logical request counts | Remaining coordinator queue |
| --- | --- | --- | --- |
| 1 | 11,75,391,100 unfinished | 8,56,8,36 | 0,1,0,0 |
| 2 | 14,71,388,95 unfinished | 11,53,12,28 | 0,0,0,0 |
| 3 | 14,62,400,97 unfinished | 10,54,10,31 | 0,0,0,0 |

Every recorded member count equals its physical batch's request count; all three
group limits are zero for every row. Completed physical calls took380.128–384.216ms.
The WAL interceptor independently retained three completed plus one active call
per node: real Fdatasync2.063–7.318ms, padded total380.004–381.161ms. This is a
controlled completion delay, not evidence of380ms physical I/O.

Observed unique request-phase begin/accepted events without matching later phase:

| Node | Begun but unfinished quorum jobs | Accepted without begin |
| --- | ---: | ---: |
| 1 | 128 | 364 |
| 2 | 128 | 564 |
| 3 | 128 | 315 |

There were no duplicate phase endpoints in this selected ordinal range. Counts
are explicit capture/ordinal-filtered lower bounds, not invented exact whole-pool
gauges. Together with the unchanged128-worker configuration and physical group
observations, they expose the blocking execution boundary before durable batching.
Worker queue time differs from the previous diagnostic because wait placement
varies; this run's exact per-request physical mapping is not substituted into the
previous run's timings.

## Consequence for implementation

A useful candidate must let already-admitted independent-Channel proposals reach
the existing local/peer batch owners without a whole-quorum wait occupying the
only preparation/execution position. It must retain original outstanding record
and byte budgets across queued, active and completed states, preserve each Channel
sequencer/authority fence, and publish success only after local plus quorum proof.
A bounded callback/state owner is a candidate; spawning a goroutine per waiting
proposal or increasing worker/queue/deadline settings is not a verified repair.

Changing EOF alone is also unsupported by the current seam: `dispatchSendFrameAsync`
intentionally closes on failed admission; transport OnData runs on a shared actor,
and the pinned public transport interface has no bounded read-pause contract.
Blocking it would stall unrelated sessions. Preserving unread data without a full
transport budget/lifecycle design would merely introduce hidden buffering.

Next implementation must address bounded quorum orchestration/batch admission and
then rerun the original failure loop. The independent permission-admission failure
and natural I/O origin remain unresolved. Original uninterrupted30-minute R2 and
three complete fresh R6 pairs remain incomplete.

Evidence, hashes, tests and repeatable scripts:
[asset manifest](assets/send-ban-physical-20260928/manifest.json),
[physical joins](assets/send-ban-physical-20260928/fault-380/physical-joins.json),
[worker observations](assets/send-ban-physical-20260928/fault-380/observed-worker-ownership.json).
All four complete native traces passed parsing and compressed SHA-256 roundtrip
verification. The archive retains each failure and all missing endpoints.
