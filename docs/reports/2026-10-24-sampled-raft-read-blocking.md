# Sampled Slot read blocking at 2,000 SEND/s

The unchanged local workload still fails on candidate
`8950ad75c054b4f38175dc2e762ed6fad20513ff`. Same-request observation and a
controlled public Runtime regression identify synchronous Slot persistence as
a material blocker of Raft message processing. This report establishes the
mechanism to repair; it does not establish that the repair passes capacity or
qualifies v3 for release.

## Fixed workload and result

R16 uses the exact clean Darwin/arm64 candidate, Go 1.25.11, three server
processes and three workers, 10,000 online users, 2,000 groups including one
100,000-member group, 256 hash slots and 12 physical Slots with three replicas.
The offered rate remains 2,000 primary SEND/s, with 90% person and 10% group
traffic. Server GOMAXPROCS remains 2. Workload seed, retries, deadlines and
correctness/latency gates are unchanged. The large-group canary starts after
60 seconds and cannot explain this earlier failure.

The first-grant barrier is `2026-10-24T04:14:39.794253+08:00`. At the first
saved failing periodic cut, 40.028 seconds later, the workload reports:

| Counter | Value |
| --- | ---: |
| Primary sends | 80,017 |
| SENDACKs | 71,165 |
| First-attempt failures | 203 |
| Terminal retry-exhausted attempt timeouts | 7 |
| Loss, duplicate, corruption, sequence regression | 0 each |
| Previous five seconds, primary SEND/s | 2,000.82 |
| Previous five seconds, ACK/s | 1,442.21 |

The final stop/drain totals include 11 attempt timeouts and 9,081
session-closed results. The latter are not the original failure population.
After the original diagnostic hold, independent inspection confirms no owned
processes and no listeners on all 64 reserved ports.

## Same-request evidence

All three actual startup configurations confirm RPC diagnostics enabled and
sample interval 32 before workload startup. Strict endpoint joining yields
111 permission RPC pairs with complete five-phase longest-barrier observation
and cumulative Save/Send coverage. Coverage sums disjoint intersections on
the same request; it is not the ratio of aggregate duration means.

- Save covers a mean **79.32%**, sample median **84.83%**, of raw-quorum wait.
  The same-phase Save intersection count has median 4 and range 1–9;
  99 of 111 requests exceed the previous two-detail retention limit.
- Save covers at least half of raw-quorum wait in 101 of 111 samples and at
  least 80% in 63. Send invocation coverage averages 0.0208%.
- The first three acquisition cuts contain 82 pairs, with mean Save coverage
  82.92%. The final acquisition contains 29 pairs, with mean coverage 69.13%.
  Those final server endpoints finish between +36.523 and +39.832 seconds,
  strictly before the first saved failing cut.
- These are successful sampled permission requests, not a direct trace of
  the seven terminal failures. Sample percentiles are not traffic percentiles.

Save includes preparation, writer queue residence, collection and persistence;
its interval must not be equated with fsync alone. Early differences between
saved native Raft fsync snapshots have mean 11.45–14.49 ms, with no observed
write-stall or slow-disk reports. The final metric scrape starts after the
first failing cut and is a mixed stop/drain interval, so it is not used as
pre-failure causal evidence. The storage collector refreshes asynchronously;
HTTP scrape envelopes do not identify exact individual fsync times.

## Controlled mechanism regression

Before modifying storage execution, the public Runtime integration test
`TestRuntimeHeartbeatProgressesWhileFollowerAppendSaveIsBlocked` establishes
a real three-voter follower, then holds one ordinary, uncommitted append at
the Storage port before durable Save. A same-term heartbeat carrying the
previous committed index cannot receive its response until Save is released.
This is the test's only failure. No append acknowledgment, committed index or
FSM application escapes durability; releasing Save permits append response,
commit/apply and complete Runtime shutdown.

Reproduce with:

```sh
GOWORK=off GOMAXPROCS=2 go test -tags=integration -p=1 -parallel=2 \
  ./pkg/slot/multiraft \
  -run '^TestRuntimeHeartbeatProgressesWhileFollowerAppendSaveIsBlocked$' \
  -count=1 -timeout=30s -v
```

This distinguishes safe message progress from durable replication
acknowledgment. It supports decoupling the Raft owner from storage waits using
the upstream asynchronous-storage protocol. It does not justify relaxing
Sync, fresh quorum, durable apply, queue bounds or the original workload gates.
Snapshot/configuration/compaction, term changes, local completion provenance
and close/reopen ordering remain required regression contracts.

## Selected repair contract

The repair uses Raft's asynchronous local-storage messages while retaining one
owner for each RawNode. Append completion updates the durable memory view and
delivers the original dependent replies through that owner. Application also
waits for its append prefix, including the committed HardState; stable log
entries alone are not permission to advance the durable FSM. Proposal Futures
bind to their accepted entry index and term before another leadership change.

New queued ownership is bounded by both count and bytes: at most
`min(4, MaxApplyingTasks)` Ready owners and 8 MiB per Slot, and `4 * Workers`
owners and `8 MiB * Workers` per Runtime. Queued, executing and completed work
retains its credit until all associated completion records are consumed or
joined and retired. These limits do not bound RawNode inputs or total RSS.
An oversized legal Ready, saturation, snapshot or configuration change uses
one borrowed synchronous fallback after joining prior work. Both paths share
the original worker-sized physical Save limit; fallback does not add parallel
disk writers or reject previously legal large entries.

Close retains the generation until all entered storage calls return; queued
append work that has not entered Save is skipped, while accepted apply work
drains. A failed append seals that generation before the next append starts,
preventing a later committed
HardState from overtaking an undurable predecessor. Snapshots, configuration
and compaction join both storage lanes before replacing their durable boundary.
Fatal retirement releases completed mailbox records without waiting for a
stopped owner; unfinished Save/apply work retains its ownership credit.

A new quorum proof for an already durable-applied index can complete while an
unrelated later entry is still being saved. It still requires the actual
current leader and term, and a read beyond durable apply must wait. With the
new protocol, overlapping Save time can represent useful parallel progress;
coverage alone must no longer be interpreted as owner blocking. Runtime
regressions and the unchanged workload must establish the resulting behavior.

## Async candidate read-admission regression

Candidate `411272ab` passes the controlled storage and restart correctness
checks but fails its first Linux mixed-send prewarm (22 NodeNotMatch SENDACKs)
and lifecycle script (325 first-attempt failures out of 3,000 messages).
Its unchanged local 2,000-primary-SEND/s run, R17b, also fails the original
first-attempt gate. The first saved active cut at 5.039 seconds already has
128 first-attempt failures out of 10,012 messages; there is no earlier healthy
cut. Final draining totals are 355 failures out of 22,001, all eventually
acknowledged, with zero terminal/loss/duplicate/corruption/sequence counters.
Eventual acknowledgment does not satisfy the first-attempt gate.

The sole diagnostic scrape starts after that first saved failing cut. It
contains 56 Slot read-busy results and 56 permission-barrier unavailable
results across the three nodes, with approximately 0.236 seconds cumulative
duration for each. These cumulative aggregates include setup and do not bind
all failed messages to a specific request or establish pre-failure timing.

A public Runtime regression separately reproduces a concrete compatibility
defect: in three voters, two remote durable acknowledgments can advance quorum
commit while the leader's next local Save remains held. An established durable
current-term committed prefix still exists, but asking durable memory for the
latest quorum commit's term returns unavailable and incorrectly rejects a fresh
read as Slot busy. One withheld remote acknowledgment prevents that advancement,
providing a control for the same test.

Admission now proves the term at the lesser of actual quorum commit and local
durable last index. The subsequent ReadIndex remains fresh and its returned
index is never lowered to that older admission witness: the read and proposal
remain incomplete until the newer entry is durably applied. Current-term,
leadership, cancellation and pending-count fences remain unchanged. This fixes
the reproduced admission defect; unchanged full-workload and Linux gates must
still determine its contribution to the observed failures and any remaining
performance limits.

R17b's independently verified cleanup receipt is
`4198c14a3c8630d8f2db813d0b157a442effc3ad539549f68b7a04b19f4f7aaf`;
its first-failure timeline is
`3828c5e16aa3509e3e37b8a126979c3b9572acf95fddd7fa5553ad0fe3ae4a1b`.
The first Linux CI audit for `411272ab` is
`3821721299d894504a1009aa5f671d07aec3d41c1744842ba25988756c9e5bf5`.
Those failures remain retained independently of subsequent validation.

## Retained evidence

Private local receipts retain raw samples without committing identities or
credentials. Their SHA-256 bindings are:

- R16 first-failure timeline:
  `6ea1ae00b8a7597d56eafaa65830e73f32356b7846934d74e6306cc3093fe66d`
- Independent cleanup:
  `48fe5a6974ecb958226eb1962bf4a26194f5900cf131f9651c8f033edf6a426e`
- Raw-quorum cumulative counts:
  `53b6e4142ba988d7e1bf9cbceb6845e0543706ff712ce666827febde8b0f448e`
- Native storage metric analysis receipt:
  `281f13466ac732fbe456b7fc5d542a83c9230d97a80019a6bae6002be1612fc4`
- Public Runtime behavioral RED receipt:
  `9d833161dc5d78cd84b965b21d8dc2ad42db9ff5c2e644d11f4980989951b0a8`

All four first automatic Linux CI workflows pass for `8950ad75c`; the tested
merge `daa2b50f` has the same tracked tree. Those checks include the existing
500-SEND/s cases and do not supersede this failed 2,000-SEND/s run, Linux
capacity qualification or the outstanding long-stability run.
