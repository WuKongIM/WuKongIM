# Sampled Slot read blocking at 2,000 SEND/s

The unchanged local workload still fails on candidate `77c7a88b`. Same-request
observations now identify full per-Slot storage ownership and its synchronous
fallback as a material blocker of the shared Raft worker. Earlier observations,
controlled regressions and failed runs remain below. These findings support
the next scheduling repair; they do not establish a capacity pass or qualify
v3 for release.

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
A full per-Slot count now yields before accepting another Ready and resumes
when the first complete lease returns. Other budget saturation, an oversized
legal Ready, snapshot or configuration change uses one borrowed synchronous
fallback after joining prior work. Both paths share the original worker-sized
physical Save limit; fallback does not add parallel disk writers or reject
previously legal large entries.

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

## Read-admission repair at the original workload

Candidate `5cb27a45` passes all four first automatic Linux CI workflows,
including the two previously failed tests. The mixed-send benchmark completes
three 60-second windows at 500 primary SEND/s, each with 30,000 planned,
started and completed messages, zero errors/drops and scheduled-total p99
between 16.96 and 19.73 ms. This does not qualify the larger local workload.

The unchanged R18 local run still fails. At the first saved failing active cut
(35.049 seconds), 70,012 messages have been sent, 21 first attempts have failed
and no message has reached a terminal outcome. Across the preceding five
seconds, actual primary rate is 1,999.369/s while acknowledgment rate falls to
1,251.781/s. Sent minus acknowledged minus terminal grows from 2,916 to 6,654;
this is outstanding message work, not a measurement of an RPC queue. The final
6,043 terminal outcomes include three exhausted attempt timeouts and 6,040
session closures during stopping, which must not be treated as the original
failure population.

The fixed collectors retain 2,845 unambiguous same-RPC pairs before the first
saved failing cut. All 152 matched permission requests succeed. In the 97
permission samples from the active cuts, mean control/raw-quorum/durable-apply
waits are 117.18/134.10/20.31 ms; same-phase Send coverage averages only 0.0534%
of control wait. The final scrape retains another 55 completed pre-failure
permission samples whose mean raw-quorum wait is 227.05 ms. These selected
populations do not establish whole-traffic latency percentiles or a direct
cross-run speedup. Two service-21 pairs show caller timeout despite receiver
success. No busy/unavailable series is exported in the twelve snapshots;
missing series remain an observation limit rather than a claimed full-run zero.

The remaining evidence gap is actual Raft-owner occupancy. Async Save coverage
cannot distinguish useful concurrent I/O from the synchronous fallback used
when storage ownership is full, nor from snapshot/configuration/compaction
fences. Same-request owner-scope observation must distinguish these before
another execution-path change. Queue limits, deadlines and workload gates
remain unchanged.

R18's independent cleanup and first-failure timeline receipts are
`8aff67b2f55a6b9cfec84f400b465c44acf25c04828ed9b35681698bbe55894f`
and `22a39fa04e9bf674cc6e3b0369fcdd43639eb36e31acdc2024efbfad2adae875`.
Its strict analysis receipt is
`4b31768e0239951dbc29b9cbe9c1ac0b0dec537e5466df90ea30e687f50ad02f`.
The successful first Linux CI audit for `5cb27a45` is
`ed3f3c01d9ab6e3460e24c21b96c908da60048a49d45aa41fe14dd1c2632ca64`.

## Distinguishing owner storage occupancy

The next observation adds two fixed records to each of the same five selected
barrier phases. One records synchronous fallback after an actual storage-budget
rejection, including the rejection bits captured under the admission lock. Its
drain-wait, Save-permit and synchronous-work intervals are disjoint subsets of
that owner scope. The other records oversized work, snapshot/configuration,
compaction, snapshot capture or embedding fallback. Those two owner scopes do
not overlap. Ordinary asynchronous Save receives no owner token.

This measures time inside the existing owner call path and its synchronization
calls, not physical fsync latency or proof that a goroutine parked. Save coverage
may overlap owner coverage and must not be added to it. A missing owner record
remains unknown; a present zero is an observed zero. Cancellation clips all
records at one caller cutoff, and later completion cannot rewrite that sample.
The fixed records follow the whole longest barrier group rather than combining
different requests or Slot groups.

Public Runtime regressions first fail because those observations are absent,
then pass for ordinary asynchronous Save exclusion, budget-drain cancellation,
Save-permit waiting and oversized fallback. The bounded JSON response fits the
existing 4 MiB collector budget. This change leaves scheduling, ownership
limits, deadlines and workload gates unchanged. The next original-scale run
must establish the dominant owner path before a further execution-path change;
the observation tests alone do not establish the workload's root cause.

## Owner occupancy at the original workload

The exact `77c7a88b` candidate passes all four first automatic Linux workflows.
Their actual merge checkout has the candidate's tracked tree. Three 60-second
500-SEND/s windows each complete all 30,000 messages with zero errors/drops;
scheduled-total p99 is 18.65, 20.99 and 24.28 ms. These checks do not replace
the original-scale local workload or Linux capacity qualification.

R19 retains the same workload, gates and bounded collectors. Its first saved
failing active cut is +35.042566541 seconds: 70,013 primary sends, 63,509 ACKs,
21 first-attempt failures and zero terminal outcomes. In the preceding five
seconds, primary rate is 2,001.7653/s and ACK rate is 1,364.97634/s. Outstanding
message work grows from 3,320 to 6,504; this is not an RPC queue measurement.
Final totals contain three exhausted attempt timeouts and 6,389 stop-induced
session closures. Loss, duplicate, corruption and sequence counters remain
zero, but the original first-attempt gate fails.

Strict endpoint pairing yields 2,844 pairs and 163 successful permission RPCs.
Of those permission requests, 140 have positive budget owner occupancy. All
169 positive phase records carry only the per-Slot count rejection bit; these
are phase-presence counts, not counts of rejection operations. In the 118
permission samples from active acquisition cuts, budget owner occupancy
averages 162.73 ms, comprising 114.51 ms draining prior work and 48.20 ms of
synchronous processing; Save-permit waiting averages 0.000858 ms. The mean
per-request share of the full read barrier is 57.88%, with median 62.41%.
The final acquisition contributes 45 additional pre-failure permission pairs;
two of those also record compaction occupancy.

The samples distinguish an actual owner-held path from ordinary asynchronous
Save overlap. They do not individually attribute the 21 first-attempt failures:
all 163 sampled permission RPCs succeed. Zero same-Slot owner coverage cannot
exclude another Slot occupying the shared worker. Selected samples also do
not establish whole-traffic percentiles or a cross-run speedup.

The selected follow-up is admission before `Ready()` when this Slot's count
is full. It must return the worker without accepting another Ready and resume
the existing FIFO when the first complete lease returns. A paused owner pass
must consume reliable completions before new inputs and avoid self-requeue
spinning. Same-Slot message-only work may wait; node/byte/oversize and
maintenance fallbacks keep their original contracts. No queue limit or worker
count increases. Parked time is not owner occupancy, and reducing the old owner
metric alone cannot establish a repair. Public progress/safety regressions
and the unchanged 2,000-SEND/s workload remain required.

R19's strict analysis, first-failure timeline and independent cleanup receipts
are respectively
`0a029cf6a7dcf0ea22d88142f64a5c5e454e507fdb5b79ee064eb270fed884f2`,
`0aa945d5eec270c1d0bfebda49cff2d4012a60576625e575970ee5bf19bb8d5c`
and `ea3b9831cc9a960d11bb372da3d33857b61796a37120d2889fb30fa5cdbe1a24`.
The first Linux CI audit is
`a74ca0a8f4ab9e18920e70ad11b0a139bc35c2799a8b253646629d7c85c31c14`.

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
