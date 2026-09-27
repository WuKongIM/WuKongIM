# Startup recovery: scale gate and certified restart

## Accepted objective

Complete all three follow-up steps: compare real three-million-user/device
restarts under Linux hard 2/4 GiB memory caps, decide merge eligibility from
that evidence, and implement a durable FSM checkpoint fast path with verified
fallback and crash consistency. A smaller smoke test or storage-only probe
does not satisfy the scale gate. The implementation remains unmerged while
the full evidence is incomplete.

## Scale experiment

The opt-in Linux scenario is in
`test/e2e/cluster/startup_recovery/scale_linux_test.go`. Seed through the public
`/bench/v1/users/tokens` adapter, which calls the same cluster-backed user use
case as `/user/token`. Use 3,000,000 deterministic UIDs with one device each,
256 hash slots and one physical Slot. Request a real Manager compaction, write
one additional suffix credential, stop the product normally and publish a
fixture receipt only after every batch is acknowledged.

Keep the stopped seed immutable. Copy its opaque directory tree into a fresh
Docker volume for each baseline/candidate case; mount it at the identical
path. Run the actual product binary and the small black-box test driver in a
cgroup with `--memory=2g --memory-swap=2g` or the corresponding `4g` flags and
`--cpus=4`. The test verifies `memory.max` and `memory.swap.max`; cgroup memory
also includes the driver and page cache, while `/proc` RSS refers only to the
product. No `GOMEMLIMIT` is substituted for the hard cap.

Record process-start-to-ready time, sampled RSS/HWM, CPU ticks, process read/
write counters, cgroup OOM counters and GC trace. Capture bounded heap/CPU
pprof and metrics after readiness, explicitly labeling their scope. Startup
CPU profiling uses the separate diagnostic overlay described below; a post-ready
profile is not startup attribution. Do not call Docker Desktop page-cache
conditions physical cold-disk measurements.

Validate every expected imported UID exactly once through Manager pagination,
including its device count and non-empty-token count, using a 375 KiB bitset.
Authenticate 256 deterministic samples plus the committed-suffix credential
through real WKProto. Token-value coverage remains sampled; inventory covers
all imported user/device rows. Add a recovery-stage SIGKILL/retry case and
retain its stage/timing evidence before considering the scale gate complete.

The executable seed and restart entrypoints are:

```sh
# Inside a Linux container with the artifact and /lab volumes mounted:
WK_E2E_BINARY=/artifacts/wukongim-baseline \
WK_E2E_STARTUP_SCALE_SEED=/lab/fixture \
/artifacts/scale.test -test.run '^TestStartupRecoveryScaleSeed$' -test.v -test.timeout=100m

WK_E2E_BINARY=/artifacts/wukongim-bounded \
WK_E2E_STARTUP_SCALE_FIXTURE=/lab/fixture \
WK_E2E_STARTUP_SCALE_REPORT=/artifacts/case/report.json \
/artifacts/scale-next.test -test.run '^TestStartupRecoveryScaleRestart$' -test.v -test.timeout=40m
```

Current execution receipts and exact binary hashes are under
`/tmp/wk-startup-followup`. The seed volume/container is
`wk-startup-20260927-seed`. The bounded matrix driver waits for that exact
container to exit successfully, then creates separately named baseline and
bounded 2/4 GiB cases. Keep those task-owned resources until reports are
collected and failures inspected; do not touch unrelated Docker resources.

## Checkpoint correctness design

Three independent proofs are required:

1. **Physical metadata continuity.** A final sequence seal is stored in the
   same physical Pebble batch as metadata mutations. Its value identifies the
   exact next visible sequence. Opening under exclusive admission reads that
   boundary through a pinned snapshot. A writer unaware of the protocol
   changes the engine sequence without refreshing the seal, rejecting reuse.
   This covers older programs and offline importers that leave applied indexes
   intact. Invalid startup seals durably clear all old certificates before
   resealing, including dormant Slots not opened in that process. Unclassified writes delete all checkpoint records; a veto from any
   request wins in mixed group commits. Ambiguous commits require reopen.
   A process-local epoch advances on invalidating commits and is checked under
   the physical commit lock; later business writes carrying an old in-memory
   chain cannot revive its certificate. Ownership-validated FSM requests check
   epochs per certificate and delete only their own stale proof. Fenced startup
   installation and snapshotless FSM writes delete their own proof without
   invalidating disjoint Slots. Migration maintenance, overlapping migration
   state and ownership mismatch retain conservative global invalidation. Compaction captures the epoch before
   reading the business snapshot. This epoch is never reused across opens.
2. **Atomic FSM evidence.** A versioned, CRC-protected record binds an opaque
   Raft proof to the metadata database's random incarnation, physical Slot and
   applied watermark, atomically with its business mutations. A pending
   snapshot-install marker, malformed record or watermark mismatch rejects it.
   Runtime snapshots, imports and other unclassified metadata batches
   invalidate certificates automatically.
3. **Exact Raft history.** Bind cluster/node/Slot identity, current ownership,
   snapshot index/term/content digest, configuration boundary and membership,
   and a SHA-256 chain over every intervening entry (including noops and
   configuration entries). Verify the chain through the checkpoint and
   contiguous retained entries through durable commit. Reconstruct membership
   with the existing Raft configuration machinery. Identical indexes and terms
   with different command bytes must reject reuse.

Only an explicitly startup-fenced open may reuse a validated checkpoint.
It sets Raft's applied and membership boundary to the certified state and
replays the committed suffix. Missing, stale or unsupported evidence selects
the existing verified snapshot path; missing committed log data fails closed.
Compaction publishes a replacement checkpoint anchor only after its matching
Raft snapshot is durable. A crash between those publications falls back.
Runtime snapshot replacement and maintenance keep their existing admission
and atomic restore behavior.

## Current completion audit

- Completed: Linux smoke with 2,000 real users, hard 2 GiB limit, disabled swap,
  all-row inventory, 257 credential checks and diagnostic artifacts.
- Completed: full three-million-user seed, baseline/bounded 2/4 GiB matrix,
  fixed checkpoint upgrade/reuse at both limits, installation SIGKILL/retry,
  full inventory and sampled authentication. Baseline 2 GiB failed with OOM;
  all optimized acceptance cases passed. See the dated recovery report.
- Completed foundations: sequence seal, unaware-writer detection, atomic
  metadata certificate storage/invalidation, history/configuration proof
  validation. Their tests were first observed failing, then passed; engine
  and metadata foundation race checks passed.
- Implemented: product enablement, per-entry/batch proof propagation,
  checkpoint selection in Slot startup, snapshot-anchor refresh, and recovery
  progress for accepted/rejected checkpoints. One- and twelve-physical-Slot E2E
  tests each passed two restarts with no snapshot rewriting and successful
  snapshot/suffix authentication. The first twelve-Slot attempt hit the existing
  ten-second initial-ready harness deadline under concurrent compilation; an
  isolated rerun passed. Database, Slot, Raft log and cluster package tests passed,
  as did focused integration/race checks. These do not establish scale acceptance.
- Completed: the Linux scale interruption mode observes
  installation after durable range deletion/pending-marker publication, stops
  all process threads, rejects a completed
  install, then sends SIGKILL and performs the ordinary full restart validation.
- Additional completed E2E before live-epoch hardening: three-node full restart
  with certified reuse on every node, and a frozen older product writing a
  rotated credential followed by safe snapshot fallback in the candidate.
  All four scenarios passed again after live-epoch hardening, as did its focused
  race checks and affected package tests. A separate authenticated onboarding
  scenario moved a Slot replica to a fourth node, restarted all four nodes,
  proved certified reuse on the three current replicas and authenticated on each.
  The existing dynamic onboarding/send scenario independently fails before the
  move with missing-token authentication on both baseline and candidate; its
  failure is not counted as replacement coverage.
- Startup CPU attribution uses an identical diagnostic-only Go build overlay
  in the baseline/candidate. It starts profiling during program init, stops
  after the harness observes readiness (two-minute bound), and writes a scope/
  elapsed/stop-reason receipt. The source, overlay mappings and binary hashes
  remain in the artifact directory. The instrumentation is absent from product
  sources and ordinary timing cases. Its small Linux smoke passed; the separate
  profile-only scenario does not claim full inventory acceptance.
- Compatibility limitation: an uncertified snapshotless legacy database retains
  its existing applied-watermark recovery semantics. It cannot publish a new
  certificate until actual compaction establishes an anchor. This must not be
  described as full snapshotless repair of arbitrary older/offline mutations.
- Completed startup CPU attribution for baseline and fixed checkpoint reuse;
  ready-after profiles remain explicitly separate.
- Completed: paired ABBA write-overhead assessment (25,000 users/devices per
  run, two runs per version) showed no observed throughput regression. This is
  a short pilot, not a sustained-load claim.
- Merge recommendation: eligible for review and merge based on this evidence,
  followed by controlled validation in the reported environment. The implementation,
  evidence and limits are delivered in the dated recovery report; no actual merge
  or publication is part of this completed validation.
