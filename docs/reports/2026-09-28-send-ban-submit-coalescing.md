# Coalescing ready ordered submissions

The product pipeline intervention alone still failed the original 380ms WAL
completion-floor loop at 26.451s. Post-failure snapshots showed all 128 submission
workers on each node in single-item Router calls. Source inspection confirmed a
worker always selected one original job, even with independent ready work queued.
This motivates a bounded coalescing experiment; it does not yet prove the cause
of every natural EOF or establish R2/R6 qualification.

The worker now takes only an already-ready prefix up to the existing Gateway
record/payload micro-batch targets. It never waits for more work, splits an
admitted job, changes an item's context/deadline, or selects a blocked canonical
Channel successor. Distinct ready jobs have no unresolved mutual predecessor.
Unconfigured submitters keep the one-job behavior; negative targets are rejected.
App composition supplies normalized existing limits, with no new public setting.

One merged Router execution counts as one busy worker. Original jobs receive
aligned capacity-limited result slices and retire individually only after their
callbacks return. Borrowed payload references in the merged descriptor slice
are cleared before returning each job's budget, even if a later callback blocks.
Worker count, admission record/byte capacities, durability and timeouts do not
change. Existing Router grouping and per-item cancellation remain authoritative.

Failure contracts and integration tests preceded the code. After adding only
option fields, RED explicitly observed [1] instead of [1 2 3] / [1 2] for queued
work and acceptance of negative targets. Regression checks cover record/byte
bounds, intact oversized jobs, canonical predecessor callback retention,
per-job results/context/deadline, worker pressure and callback-owned capacity.
Linux race checks of ordered submission/app composition passed 20 repetitions;
related default suites passed. The final race run includes payload-reference
clearing. FLOW contracts pass with the existing advisory line-count deviation:
retaining the complete runtime ownership/drain invariants takes priority over
the 100-line navigation target; the validator is unchanged.

The clean and diagnostic binaries and all overlay source hashes are frozen in
`assets/send-ban-submit-coalescing-20260928/`. Real-process functional and wire
ordering checks, then the original control/fault diagnostic remain required.
R2/R6 are not qualified by these component checks.

## Actual-process and original-load results

The same clean binary passed both authenticated single-node scenarios, 1,200
person plus 1,200 group messages checked on actual TCP ACK/receive order, and
single/three-node ban matrices plus all four Gateway UID/Channel distributions.
The final related default suites passed again after payload-reference clearing.

The diagnostic control passed 60s / 270,000 SENDs, P99 58ms, zero pending and
permission busy. The 380ms/5s WAL completion-floor arm still failed with EOF at
26.467098s: 119,101 calls, 6,343 pending, permission busy=0. The observed Router
peak rose from the pipeline-only candidate's 128 to its existing 512 limit;
Store append worker utilization reached 1, with queue ratio 0.461914. This is
not a successful root-cause repair. There was no OOM, disk guard or concurrent
build/cleanup/trace parsing during measured traffic.

All three nodes completed three padded syncs and had a fourth active at capture;
actual completed syncs took 2.408–9.343ms, padded to 380.005–381.614ms. Both arms'
eight complete traces passed Go trace parsing and verified gzip roundtrips.
The compressed traces retain all data; raw copies were removed only after
byte/hash comparison. The failure remains in the archive and R2/R6 remain open.

## Next evidence boundary

`pkg/channel/worker` deliberately selects one task for TaskQuorumCommit and its
worker stays in QuorumLog.Commit through the durable result; legacy StoreAppend
batching does not apply to this task kind. That is a concrete deeper capacity
boundary, not proof that changing it would meet the original load. Do not infer
physical I/O root cause from the artificial floor, or declare saturated pools
bugs merely because their protective bounds work as designed.

Before another repair, capture the existing Gateway reservations by ownership
at first rejection: unprepared, waiting for a result, ready but waiting for a
session predecessor, and published but awaiting preparation/completion fences.
Join the oldest result-waiting request to its exact Channel quorum proposal and
worker/commit progress. Use bounded diagnostic-only state and preserve all
existing capacity, durability and timeout contracts. The two failed interventions
rule out presenting the outer execution split or ready-job coalescing alone as
proof that the original EOF is repaired.
