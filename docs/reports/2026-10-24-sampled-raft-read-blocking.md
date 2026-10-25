# Sampled Slot read blocking at 2,000 SEND/s

The latest short run on `ca9aa4817` completes the planned 120-second window
without observed correctness failures after correcting storage cache budgeting.
It remains unqualified: hot SENDACK P99 is in the (1,2]-second bucket, above
the original 400 ms target, and the official latency window has not started.
R27 below preserves the exact result, memory cost and remaining uncertainty.

Earlier runs on candidate `d7acdae32` still fail at about 40 seconds. Earlier
same-request observations identified per-Slot storage-count fallback occupying
the Raft owner. Yielding before Ready removes that measured fallback. R21's
independent observation measured same-Slot scheduling pause across 81.6%
of the read barrier on average in its active successful permission samples.
Bounded compatible-prefix coalescing subsequently reduces that measured pause,
but the original workload still fails. These successful samples neither trace
every failed SEND nor separate append, apply and completion consumption within
the pause. Earlier observations, controlled regressions and failed runs remain
below; none establishes a capacity pass or qualifies v3 for release. R24's
receive evidence rules in a caller-resumption contribution but does not explain
most of the remaining client/server duration gap. Current-frame receive
observation is short; this does not exclude earlier-frame work or pre-read waits.

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

## Count backpressure at the original workload

Candidate `097410e5` passes all four first automatic Linux workflows, with 16
successful jobs and two planned skips. The actual merge checkout's tracked
tree matches the candidate. All nine 60-second measurement windows across the
three 500-SEND/s scenarios complete 30,000 messages with zero errors/drops and
scheduled-total p99 below the original 400 ms gate. These are smaller workload
checks, not acceptance of the original 2,000-SEND/s workload.

R20 retains the original workload, gate thresholds and observation bounds.
At the first saved failing active cut, +35.055556292 seconds after the true
first-grant timestamp, it reports 70,010 primary sends, 63,005 ACKs and 98
first-attempt failures. Terminal, loss, duplicate, corruption and sequence
regression counters are zero at that cut. The previous +30.055524458-second
cut has no first-attempt failures. In the final five-second interval, primary
rate is 1,998.1873/s but ACK rate is 1,344.3914/s; outstanding message work
grows from 3,736 to 7,005. Those are logical message counters, not an RPC queue
census. After stopping, final terminal results include three exhausted attempt
timeouts and 7,080 session closures; those closures are not the initial failure.

Strict endpoint joining retains 2,842 same-RPC pairs, including 139 successful
permission requests with complete phase and owner coverage. All 139 have zero
budget-owner coverage: the sampled fallback has not moved to node-count or
byte rejection. One final-acquisition request contains 882.128 ms of compaction
occupancy. This does not account for all 98 failed first attempts, and the
successful samples cannot be linked back to those failed SEND operations.

The 95 permission samples from active acquisition cuts have mean control,
raw-quorum and durable-apply waits of 47.809, 128.522 and 55.688 ms. Their mean
service-queue time is 8.244 ms. Another 44 requests retained by the final
acquisition still finish before the first saved failing cut; their mean
raw-quorum wait is 230.226 ms. Per-request Save coverage of raw-quorum time
averages 95.46% in the active cuts and 84.29% in the final population. Save
remains asynchronous and includes more than physical fsync; overlap does not
prove owner blocking or distinguish required persistence from avoidable
scheduling delay. These populations must not become traffic percentiles or a
cross-run speedup claim.

At the three active metric cuts, every node reports 20 Slot scheduler workers
for 12 physical Slots and a zero sampled scheduler queue. Thus the controlled
single-worker cross-Slot progress regression does not quantify contention in
this workload. Same-Slot count pause is a separate path: it defers Step,
Tick and read controls until a whole storage lease retires, potentially after
both append and apply completions. It consumes no worker while paused and is
absent from owner coverage. Per-request pause coverage is needed before
attributing the remaining raw-quorum delay to that path.

R20's strict analysis, first-failure timeline and independent cleanup receipts
are respectively
`2466208d834c235466697139da1991efb13ee221c11dddbe8ae9e65735428b17`,
`4c34c62baae1b4176649192752bbd2175ee0e81d882acd0d1098df047fc20fe1`
and `b11ba184af7cf8800ecb35b2b30af2e15424916e21052a7982897493b8e59cd3`.
The first Linux CI audit is
`975083e449b1c4c1b7d47329a142f59fd22b636e655de8cbf47ae4131c945a8e`.
Pre-failure metric context is retained in
`199038ccb28ef4d02843910f5de3c3e05e51059137baf02094f2d093b9eec383`.

## Same-Slot pause at the original workload

R21 uses exact candidate `c5d12fc602bf9ff040ed7a271b5d28d96632cb40` and
tracked tree `4b669d7c3fd153e42a2ca1ffe8c0bfe6d4680949`. Its change adds
optional pause observation only; execution policy, workload, deadlines, queue
bounds and original gates remain unchanged. The true first grant is
`2026-10-24T07:08:01.604395+08:00`. The first saved failing cut at
+35.047859958 seconds reports 70,011 sends, 63,713 ACKs and 47 first-attempt
failures; terminal, loss, duplicate, corruption and sequence regression counts
are zero. The preceding cut at +30.047812541 seconds has no first-attempt
failures. The last five seconds sustain 1,997.581 primary SEND/s but only
1,436.586 ACK/s; logical outstanding work grows from 3,493 to 6,298 messages.
The final 6,367 terminal results comprise three exhausted attempt timeouts and
6,364 stop-induced session closures. Independent cleanup confirms zero product,
Go or test processes and zero listeners across all 64 reserved ports.

Strict joining retains 2,779 same-RPC pairs, including 162 successful permission
requests with complete five-phase observation. The active acquisition cuts
contain 112; the final acquisition retains another 50 whose endpoints both
finish strictly before the first saved failure. The two populations stay
separate. In the active 112, per-request scheduling-pause coverage of the
same longest read barrier averages **81.616%**, with sample median **86.822%**
and mean pause duration **200.732 ms**. Its mean coverage of the control and
raw-quorum phases is **90.868%** and **77.326%** respectively; for the 93
nonzero durable-apply phases it is **80.019%**. Budget and other owner occupancy
are measured zero in these active samples. Mean service-queue time is
6.783 ms, compared with 252.375 ms in the handler and 323.176 ms at the client.

The final pre-failure population has mean pause coverage 41.793% of its read
barrier and 42.668% of raw-quorum time. Its mean service-queue and handler
times are 38.312 and 417.857 ms; other owner occupancy can also be present.
These distributions do not represent all requests or the 47 failed SENDs.
They establish measured local scheduling deferral, not a complete causal
chain for the first failure. Pause extends until the owner resumes after a
whole lease retires; append, apply and pending completion consumption are not
separately attributed. The active mean Save overlap of raw-quorum time is
96.392%; Save, pause and owner intervals must not be added together.

The follow-up execution change targets a known FIFO constraint: the existing
writer can group writes across Slots, but the Slot storage pipeline admits
only one physical Save per Slot at a time. Compatible work already queued for
that same Slot cannot join the write. A bounded existing-prefix merge must
preserve each member's original response, apply fence, failure and lease
ownership, without a collection timer or increased resource limits. A public
Runtime regression must first demonstrate the separate queued Ready records
and redundant physical rounds before implementing that change.

R21's strict analysis, first-failure timeline and independent cleanup receipts
are respectively
`89cabb95a36f56503f61956c1e785cc317c4d735a26b856036f9cee9f747e6a9`,
`7dc3763108735f239779d21d00e36b6d260c65ed9b0fb10df943a54fdfc31801`
and `18931f41c53d46505af32598f643f5adc1aa089ae72ba579a2ec12097ca61b19`.
The independently checked first-failure timeline is bound by
`feae4f7feffc95b90f96eeb896c049c1fb8b7c516d73ecd21c784b72fc5b82d4`.
The strict aggregate independent review is
`c3d0008dcf812c0d71139d5867321c33f78dfd26f50f5b5066925ee05f37db1d`.
All four automatic Linux workflows again pass on their first attempts, with
16 successful jobs and two planned skips. Nine 500-SEND/s windows pass their
original gates; the actual merge checkout's tracked tree equals the candidate.
That CI audit is
`a7ac3f9010839ad21a5a5fc301239400af8dbd75ccc141b17b225f626f2edd49`.
It does not supersede R21's original-scale failure.

## Bounded same-Slot append coalescing

The follow-up implementation merges only a compatible prefix already queued
for one Slot generation and accepted term, bounded by the existing four owned
Ready records. It adds no collection delay, queue capacity, worker count or
resource credits. Snapshot, configuration, apply-only and response-only work,
noncontiguous entries, term changes and incompatible whole HardStates stop the
prefix. One physical Save persists the selected prefix; original member
responses, memory publication, apply fences, errors and leases remain separate
and ordered. A failed Save fences the Slot before any successor Save. Another
ready Slot keeps its turn before late arrivals to the just-served Slot.

A public Runtime regression written before implementation held the first Save,
queued three separate Ready records and observed only one entry in the second
Save instead of the expected three. Its actual behavioral RED receipt is
`ffbe01cae205b69b5386fe8b44ed0b0527192cec03feae24168c71732a0b2997`.
Tests cover prefix barriers, frozen membership, original follower ACK ordering,
whole HardState and apply ownership, late-arrival fairness, error and shutdown
retirement, and production Raft-log persistence/reopen. A retaining-input
regression also demonstrated that clearing the aggregate entry array after
Save violates the storage contract. The implementation now drops its own view
without mutating storage-retained input.

The first focused execution exposed disk-fixture preparation defects; those
failures remain recorded. The fixture now proves the first Save has entered
with exactly one entry before queuing the tail, and establishes current-term
committed, durable and applied state before both initial capture and reopen
validation. Original index, term, digest, HardState, Future and resource
assertions, the 12-second bound and the single ReadBarrier call remain intact.
The corrected real-disk case passes. The first complete integration and race
executions for `pkg/slot/multiraft` and `pkg/raftlog` both pass, with stdout
SHA-256 values
`68d8fb8644143af01b74aebcca87287ef6ee88d0f9fd5771d0bdbd47c8c6e700`
and `4c7f454972cef1f17c90f0a51d2d37d0bf2f7a609a2d9bfa17a0ce332a5eba31`.
The named `flow-doc-contracts` check passes. Independent protocol and performance
reviews retain the original durability, memory ownership, fairness and queue
bounds. These controlled regressions establish the mechanism and correctness;
the following exact candidate rerun establishes its separate workload result.

## Coalescing at the original workload

R22 uses exact candidate `e0205a54859453d1b4e044bd0f1f25b0fb0eeb82` and
tracked tree `083ff45710d51353c5976c111c68ce562881706c`. Both native binaries
come from that clean source with the same compiler, embedded assets and build
settings. Workload, gates, 32-request sample interval and runtime bounds remain
unchanged. Its true first grant is `2026-10-24T07:44:57.976305+08:00`.

The +35.055253209-second cut has one first-attempt failure among 70,012 sends,
64,379 ACKs and no terminal result; it has not yet breached the original gate.
The first saved gate failure is +40.0552975 seconds: 80,016 sends, 71,012 ACKs,
261 first-attempt failures and four terminal retry-exhausted attempt timeouts.
Loss, duplicate, corruption and sequence-regression counts are zero at that cut.
The last five seconds sustain 2,000.782 primary SEND/s but 1,326.588 ACK/s,
with 9,000 logical outstanding messages after subtracting ACKs and terminal
results. Both terminal-send and first-attempt-rate gates are violated; the
original verdict gives terminal-send priority. The final 9,240 terminal results
comprise eight exhausted attempt timeouts and 9,232 stop-induced closures;
the additional four timeouts also occur after the first saved failing cut.
Independent cleanup again confirms zero product/Go/test processes and zero
listeners across all 64 reserved ports.

Strict joining retains 2,893 same-RPC pairs, including 150 successful permission
requests with complete five-phase observations. Active cuts contain 115. Their
longest barrier averages 151.443 ms, compared with 238.595 ms in R21's active
112; same-Slot pause averages 74.606 ms versus 200.732 ms, and its per-request
mean barrier fraction falls from 81.616% to 45.192%. Mean control/raw-quorum/
durable-apply phases are 21.044/93.497/36.899 ms. Budget and other owner occupancy
are measured zero in these active samples. Save and pause coverage of raw-quorum
time are 88.289% and 37.281%; these overlapping quantities cannot be added or
subtracted to assign causes. The two runs describe different sampled requests
on a shared host, not a controlled causal estimate of overall throughput.

The 35 final-acquisition permission pairs remain a separate successful
population, with client ends between +31.984 and +35.824 seconds. They do not
cover the last roughly 4.23 seconds before the gate failure and cannot directly
explain the 261 failed SENDs. No missing interval is filled with zero. The
paired non-OK observations are nine presence-owner calls whose client timed
out while the server completed successfully; they are not linked to SEND
identity. Further same-candidate profiling is needed before another execution
change; the original-scale capacity result remains **FAIL**.

The R22 timeline and independent timeline review are bound by
`3d1bfb3eb783fa54badba88de585a4138035333e8d21ba35cc66e4e8e4116cf4`
and `a1339b0c813148d682289485298885d05967d7099fcf2f57fbd0d06909797dcb`.
The strict analysis findings receipt is
`70be0a645827c352f72bfed7cc796f1b668480604fe13322e396f25eae40a8b8`;
its R21/R22 same-category comparison is
`01a28ce24c68833aea5e8d944a3427e7faed9bb99588fa5cd2abbd4ee7ecc0f9`.
The independent strict-aggregate review is
`8695b48cab9a859e25dac3977d8990089db3fa98209e3b6ff23eca1341e55027`.
Independent cleanup is
`6c21c5f0360fb16e696237be6f5133f57ebe148e2b33df00951edd5f1bbe51fb`.

## Same-candidate profile diagnostic

R23 reuses R22's exact binaries, original workload, gates and bounded runtime.
Its only observation change adds one 15-second CPU capture and one goroutine
snapshot per server, using six bounded HTTP requests. This profile overhead
makes R22/R23 descriptive observations, not a controlled performance comparison.
The true first grant is `2026-10-24T08:03:48.155793+08:00`.

The first saved failing cut is +40.032781625 seconds: 80,022 primary sends,
70,009 ACKs, 587 first-attempt failures and 31 exhausted attempt timeouts.
The previous cut has four first-attempt failures but no terminal result and
still passes the original correctness gate. During the final five seconds,
primary SEND remains 2,000.773/s while ACK falls to 1,245.183/s. Loss,
duplication, corruption and sequence regression remain zero at that cut.
The final 10,077 terminal results comprise 34 exhausted timeouts and 10,043
stop-induced closures. The latter and the three additional timeouts must not
be attributed to the earlier first-failure population. Independent cleanup
again confirms zero owned processes and all 64 reserved ports clear.

Strict endpoint joining retains 2,896 pairs. All 134 permission pairs succeed;
93 belong to active acquisitions and 41 to the final acquisition. Active
longest-barrier mean is 158.899 ms, with control/raw-quorum/durable-apply means
20.971/96.282/41.643 ms. Same-Slot pause averages 99.249 ms and its per-request
barrier fraction averages 57.600%; measured budget/other owner occupancy is zero.
Final permission client endpoints end no later than +35.970580916 seconds,
leaving 4.062200709 seconds before the first saved failing cut unobserved by
those pairs. These successful requests do not trace the 587 failed SENDs.
Five presence-owner pairs have client timeout with server success and remain
separate from successful pairs.

All six profile files pass bounded gzip/protobuf validation and reconcile with
the cached pprof reader. CPU windows run approximately +15 to +30.264 seconds;
goroutine snapshots occur around +30.294 to +30.387 seconds. They precede the
gate-passing +35.033-second cut but extend past the last saved zero-first-failure
cut, so they cannot be called entirely failure-free. Sampled CPU averages
0.562/0.582/0.576 cores per server; this does not support sustained saturation
of each server's two execution threads, and does not measure whole-host
availability or exclude short spikes. Transport writeLoop accounts for
34.1%–38.4% of sampled CPU, almost entirely writev; it is the largest CPU path,
not evidence that writing caused every delay.

At those snapshots all 60 Slot owners await a scheduler task. Fourteen append
workers await Raft-log submit results and fifteen apply workers await metadata
commit results; other workers in these cohorts await work. RPC pumps include
208 waiting for new requests and two waiting for service tokens. This does not
show an exhausted global RPC executor, but neither does it measure queue lengths
or the duration of those waits. Count-paused Slots and queued per-key work have
no dedicated goroutine. Connection writers all await queued frames at the
snapshot; most readers are in network polling and two readers await observer
locks. No snapshot alone explains the stable 80–91 ms client/server duration
residual of several short-handler RPC services.

Further offline extraction keeps the same 2,896 strict pairs. Across services
14, 15, 80 and 84, request-write means are only 0.036–0.229 ms while response-wait
means are 93.547–106.567 ms. The active-cut client/server duration residual is
62.124–66.169 ms, rising to 146.911–175.628 ms in the final acquisition. Those
duration differences are not one-way network measurements. The old server
window begins after full-frame receipt and synchronous byte observation, and
the old client window does not separate response receipt, notification and
caller resumption. Offline extraction and its independent review are bound by
`65857e30bc3f719ffacbd126aed395cd246569bdf26d5e554c8faf7db2e7db0f`
and `f3de98c209db5958f81664c4161de9ad7c431cec60bf54ae1ff50b0919c852be`.

The next candidate adds bounded receive evidence on those existing sampled
requests: a server prefix before its unchanged Trace.At, plus three client
intervals for synchronous receive observation, response dispatch and caller
resumption. Initial receive identity validation remains outside the new window;
the server's existing dispatch revalidation is inside its total prefix. The
notification point is immediately before the existing buffered send, so resume
includes that send and scheduling rather than measuring only scheduler delay.
These observations neither change admission nor extend business deadlines.
The R24 result below records this candidate's original-scale failure and the
newly retained receive evidence.

R23's timeline, independent timeline review and independent aggregate review
are bound by
`8c20ca32d52c06c6cf73817d1bfb58b065a8a68b7d2d59d595740a6e4c01dec0`,
`43c3561405b81a0a1af904377c901676ff3e0b0b457d75d42fc398b6e3e62528`
and `73dae398a1299b20fea6dd972e30a47edae485ff1affec00b54db53dceaf55ef`.
Profile findings and current-source idle-site validation are
`2126323451d24844b715df69950fac636e7c1e76f9ec129125e3a7816ead0823`
and `5a9a3ea85485d6bf0fc1efef3c71d919454264ffac6166fc69fcd80d0827dc82`.

All four first-attempt Linux workflows pass for `e0205a54`: 16 jobs succeed and
two conditionally skip. The actual merge checkout's tracked tree equals the
candidate, and all nine 500-SEND/s windows pass their original gates. CI audit
`26e748a8e2708760fbc3438608e07e0f1948bf7da319ed8c2402fcbc8033423b`
preserves those results separately from R22/R23's failed original-scale gates.
PR #1038 remains Draft; this candidate does not qualify v3 for release.

## Retained evidence

### R24 receive evidence and original-scale result

The clean `d7acdae32` build preserves the workload, gates, sampling rate and
resource limits. Full transport/rpctrace integration and race checks pass, as
does app diagnostics wiring. All four first automatic Linux workflows pass:
16 jobs succeed and two conditionally skip. Nine 500-SEND/s windows pass their
original gates; those results do not supersede the failed 2,000-SEND/s run.

R24's first grant is `2026-10-24T08:41:57.539961+08:00`. The first saved failing
cut is +40.056671791 seconds, with 80,014 sends, 71,349 ACKs, 307 first-attempt
failures and 14 retry-exhausted attempt timeouts. The preceding five seconds
sustain 1,996.808 SEND/s and 1,502.406 ACK/s. Loss, duplicate, corruption and
sequence-regression counters are zero. Final terminal outcomes are 19 attempt
timeouts and 8,863 stop-induced session closures, which remain separate.
Independent cleanup confirms zero product/test processes and reserved listeners.

The collector retains all five bounded cuts, including the added actual-gate
cut. That acquisition crosses stopping, so strict selection uses both endpoint
completion times before the saved failure boundary. It retains 3,466 pairs:
2,185 from active cuts, 704 from the early warning cut and 577 from the gate
cut. All 99/52/43 permission pairs in those groups succeed. The latest gate
permission pair finishes only 79.383541 ms before the boundary, narrowing the
previous approximately four-second observation gap without claiming that the
failed SENDs themselves were traced.

For services 14/15/80/84, active successful receive groups contain 158/435/127/82
pairs. Their caller-resumption means are 6.43/7.80/5.67/5.91 ms while their
client/server duration differences remain 63.13/64.59/64.14/69.56 ms. Gate-cut
services 15/80/84 retain 88/36/22 successful pairs, with resumption means
38.59/30.21/24.67 ms and duration differences 169.93/150.03/171.32 ms.
Current-frame receive observer and dispatch stage means are each below one
microsecond in these groups, and server receive-prefix means remain below one
microsecond. These are sample means, not workload percentiles or maximum waits.

All observed client receive groups lie within their actual response-wait span.
They must not be added to that span or to the client/server duration difference.
Resumption begins immediately before the notification attempt and may include
send-path scheduling or earlier still-blocked caller work; it is not exclusively
runnable scheduling delay. Initial identity validation, earlier-frame deferred
observation, socket readiness and full-frame reading remain outside this new
window. Eleven early and three gate-cut service-14 client timeouts lack receive
groups and remain separate unknowns, not zero-duration successful receipts.

R24 timeline, result review and first Linux CI audit are bound respectively by
`2fda62ed3b3014363f4ae77009e0859332720c3c556a0dd2b31635997fac99de`,
`3c813aca3809da36d9e8512de092495ad6caa216962da6fdec58e382d1cbbcee`
and `a0c6ab450bed60b4ca95f0198d1eebd93bfac8c502490b91f78a617e0a875fa2`.
The analysis retained its first pretty-JSON output-bound failure, then encoded
the identical fields and values compactly within the unchanged 1 MiB bound.

Twelve saved metric responses provide a separate pre-gate context. Per-node
TotalAlloc rates rise from 245–259 MB/s between the +5 and +19 second cuts to
746–774 MB/s between +19 and the +35 second warning cut. Go heap allocation
reaches 1.49–1.61 GiB per node. These are allocation traffic and heap gauges,
respectively, not evidence of a leak. The warning cut already contains one
first-attempt failure while still passing the original gate.

Successful message-batch Build means rise from 0.74–0.81 ms to 11.83–12.56 ms;
native completed fsync means and completed GC pause totals do not show a
corresponding rise. Build measures wall time and does not distinguish encoding,
locks, scheduling or GC assist. Batch composition also changes. The sampled
global RPC task aggregate peaks at 630 busy out of 15,234 registered capacity,
with zero sampled queue depth and no recorded executor rejections. This does
not rule out a saturated local service; node 3 send-authority occupancy reaches
444 of 512 at the warning cut.

Sparse runtime scheduler buckets do not establish a worsening distribution.
Their exported histogram sums in client_golang v1.16.0 are estimates from
bucket lower bounds, not exact sums. The separate GC summary uses actual
completed PauseTotal, but neither measures concurrent GC CPU or assist.
Allocation sources therefore remain a hypothesis to investigate with a bounded
same-binary allocation delta profile before making another execution change.
Metric analysis and independent review are bound by
`7aec379ecc90999ad728f7269c5bb5fa7ef7e6c58a6fd1e864bb8720789fc08b`
and `9e1156908907148a73ec428fabb94d6a60aacb1da5ff0ec7703c5d5d6c07e4f6`.

### R25 allocation capture canceled before completion

R25 reuses R24's exact binaries and original workload/gates, adding only three
bounded allocation delta requests planned for +25 to +40 seconds. Its first
grant is `2026-10-24T09:03:27.354003+08:00`. The first saved failing cut is
+35.046731458 seconds: 70,011 primary sends, 63,528 ACKs and 12 first-attempt
failures, crossing the original overall first-attempt-rate gate. Terminal and
loss counters are zero at that cut. The preceding interval sustains 1,992.999
primary SEND/s and 1,359.599 ACK/s.

All three allocation requests start at approximately +25 seconds, then cancel
when the coordinator terminates before their 15-second delta completes. Each
retains zero profile bytes; all collector threads join. This is an incomplete
diagnostic, not a valid allocation profile or a successful capacity result.
The failed run remains intact. The next diagnostic moves only the same
15-second capture to +15 through +30 seconds, retaining the original workload,
gates and cancellation behavior. R25's independently checked cleanup is bound
by `9e263abb4348493d894e2f3a0e38d7cba0b1f2d7dc4c51a0931905e57237ffe5`.

### R26 valid allocation profiles and remaining capacity failure

R26 changes only that capture schedule. The first grant is
`2026-10-24T09:13:25.170884+08:00`; its first saved failing cut is
+40.036411417 seconds, with 80,012 sends, 70,279 ACKs, 661 first-attempt
failures and 37 exhausted attempt timeouts. The preceding interval sustains
2,000.998 SEND/s and 1,390.398 ACK/s. The +35-second cut already contains seven
first-attempt failures but still passes the original gate. Final terminal
results separate 40 attempt timeouts from 9,749 stop-induced closures.

All three allocation profiles complete and decode within the original limits.
Their snapshot-completion windows fall approximately between +15.10 and
+30.31 seconds, before the saved failing cut. Estimated, sampling-scaled
allocation totals are 4.810/4.864/5.308 GB and 24.94/24.18/26.74 million objects.
All six explicit `alloc_space` pprof totals match independent decoding.
Memory profiles can lag collection by two GC cycles; these are neither exact
allocation-event windows nor comparable to the different +19-to-35 metric
window. They do not identify individual failed SENDs or establish GC causality.

Pebble `manual.New` accounts for 21.23%/22.27%/22.77% of sampled allocation
bytes. Mutually exclusive leaf-stack classification attributes
14.66%/15.59%/16.33% of each node's total allocation to filter-block reads.
The corresponding product paths include MQTT activation/source-state reads,
durable proposal/entry identity checks and checkpoint reads. Presence recovery
admission accounts for 4.21%/4.41%/4.46%, and the replication batch pool's
collection buffer for 3.78%/3.69%/3.83%. Cumulative caller percentages overlap
and must not be added together.

The first decoder rejected Go's omitted optional default-sample field; the
second rejected legitimate long generic function names. Both failures remain
preserved. Narrow compatibility fixes retain the four sample types, exact
capture identity, explicit sample selection, signed values, bounded input and
output, and full-name aggregation. No recollection or extra pprof run occurs.

The timeline review, allocation review and cleanup bind respectively to
`f3ebddb35b3236821e03c25a330eac6945c64619656a4c8eeb220ddbfec6a94c`,
`4e522cb257f62a86b3cb1601c2b5ef2f0f253385480e0c068e4c0c7bdc3d20ff`
and `a772544ea48118d244196fd8271b7d89e35dda2e411a73b83db0bbefa1ba71ae`.
PR #1038 remains Draft; the original capacity gate remains **FAIL**.

### Cache reservation mechanism and bounded correction

A real Pebble integration fixture now isolates one mechanism behind those
filter-block allocations. With the original 128 MiB combined cache capacity,
one 64 MiB active memtable and one retained 64 MiB recycled arena consume the
cache reservation allowance. Thirty-two repeated, warmed, in-range negative
lookups perform 128 block-cache misses and no hits. The same fixture with
32 MiB arenas records 128 hits and no misses. Both cases retain the Bloom
checks and verify durable values across close/reopen. Earlier fixture setup
failures remain preserved; only the final established reservation state is
used as the behavioral RED.

The engine now treats its cache option as requested read headroom R and budgets
the combined Pebble capacity as `R + (T + 2) * M`, with the existing write-stop
threshold T=4 and configured memtable size M. The six-table allowance covers
ordinary mixed-size rotation plus one recycled arena. Native-integer overflow
rejects before filesystem creation. The same rule applies to read-only opens,
which may still replay WALs into memtables. Long-lived reader pins, large
flushable batches and arbitrary read-only WAL replay remain outside this
ordinary allowance; it is neither an RSS cap nor an unconditional cache floor.

Message and metadata combined capacities change from 128 MiB each to
512/320 MiB per node. This permits an additional 576 MiB per node, or
1,728 MiB across the three acceptance servers. It is lazy allocation, but
unused reservation allowance can become cached blocks and resident memory.
Raft's separate adapter and cache budget are unchanged. No memtable size,
durability check, queue, deadline, workload or acceptance gate is relaxed.

The production 64 MiB fixture passes after the correction, as do normalization,
overflow and open-before-side-effects checks. The mechanism receipt and first
GREEN are bound by
`a47647f7838415721cf1e0727c98dffb2d11f4c42a1d3dc545d2b1440cc8429e`
and `a8988fd1312fb38430c0fdbb36911e596f8466ce35c976e3a5d80ca4b2b559ec`.
This establishes cache reuse, not the cause of every failed SEND. The next
original-scale run must report both the unchanged gates and actual memory
use; passing this fixture alone does not qualify the candidate for release.

Existing storage snapshots now expose block-cache retained bytes, cumulative
hits/misses and memtable arena reservations without additional polling or
background work. Review caught an upstream `ZombieSize` unsigned underflow
while large flushable batches are queued. Two real WAL replay fixtures retain
the failing value and verify the corrected, independently expected 256 KiB
reservation. Adding `Size + ZombieSize` from the same native snapshot reverses
the subtraction in unsigned arithmetic; it includes active/recycled/pinned
arenas but excludes flushable-batch backing and is not an RSS measurement.
The 17 focused checks across nine packages pass; their final receipt is
`94d588e43ca46ab7591ce09506a2f2f2694b076cee1d4362857c99fa73ad74a4`.

### R27 completes the short window; latency remains unqualified

Candidate `ca9aa48175fea8572a8e4f762236e43f85ac4008` uses the same original
distribution, 2,000 primary SEND/s, deadlines and gates. The unprofiled RPC
collector is byte-identical to R24. First grant is
`2026-10-25T15:15:05.569937+08:00`. The supervisor requests the original planned
stop at +120.208 seconds; final reporting includes drain through +122.709.
The final totals are 242,003 primary sends and 242,003 ACKs, with zero
first-attempt failures, rejects, terminal messages, loss, duplication,
corruption or sequence regression. All 2,588 sampled delivery correlations
complete. The 8,380 retry attempts remain recorded. Saved periodic intervals
sustain 1,998.799–2,002.199 SEND/s. No original correctness breach is observed.

This is **not** an acceptance PASS. The original verdict is `operator_stop`.
All samples remain inside the configured two-hour warmup, so the verdict
engine has no latency-window samples: zero warnings do not establish healthy
latency. The final histogram records 67,756 successful hot SENDACKs, of which
67,378 (99.442%) exceed 400 ms. Hot mean is 817.887 ms and P99 lies in the
(1,2]-second bucket; cold P99 exceeds two seconds. On the common attempt
population, pending-to-write mean is 1.347 ms and write-to-ACK mean is
947.245 ms. This locates most observed waiting after the client write; it does
not attribute it to one server phase or establish a global RPC queue limit.

At the available matched +5/+12/+19-second metric cuts, allocation rates are
similar to R24. R27's next native snapshot is after the stop request, not an
active +35/+40-second cut. Its +19-to-post-stop message-cache delta shows
99.426–99.489% hits with 82.1–94.0 MiB retained blocks and 128 MiB reserved
arenas. The separate Raft adapter still shows zero retained cache and hits;
that is a remaining observation, not proof of the latency cause.

In the first 40 seconds, the sampled simultaneous three-server RSS peak is
4,481.45 MiB, versus R24's 5,699.98 MiB; their last sample times differ by
about 1.2 seconds. R27's full active-window peak is 6,794.58 MiB and must not
be compared directly with R24's shorter active run. Host free-memory readings
also differ (82% versus 68%); runtime swap changes are unavailable. The
controlled engine RED/GREEN establishes the cache mechanism, but these shared
Mac runs cannot assign every improvement to that change or qualify Linux
capacity. All owned processes and all 64 reserved listeners are cleared.

The timeline, cache/memory comparison and independent cleanup are bound by
`1b5a812c6a77d1985d9a94ff222510c8708a31558f02802211222129cca436d3`,
`4b678373f07666eeab6e80c9cba7ea18b9868df39120c0b18a66493254fb8079`
and `a85ba3e18a6a4183f019f5049664ece6271f3cd6c166c159a5848dd638465bb6`.
PR #1038 remains Draft; original latency, Linux capacity and long stability
remain unresolved.

Strict pairing retains 2,722 eligible RPC pairs with both endpoints wholly
inside the first-grant-to-120-second window. Channel-authority SEND (service 21)
has 191 client durations over 400 ms among 508 pairs. That slow group's mean
client/server/handler times are 510.700/417.263/398.265 ms, with 14.489 ms in
the service queue. All 508 server samples retain only the five transport/handler
spans with zero drops. The missing append subdivision is a coverage gap, not
zero storage time; a quorum-exchange RPC from a different request cannot fill it.
Presence authority (service 13) also retains recovery FIFO and service-queue
waiting, while most shared owner work has no original-caller subdivision.
These observations prioritize Channel append ownership and preserve the
remaining presence uncertainty; they do not establish global RPC pool saturation.
The independent pairing receipt is
`6de96a88c9dc3919d6a1a6b341734794500e9a1109d0b03149a5559c7a509e03`.

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
