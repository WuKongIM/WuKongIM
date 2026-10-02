# Issue #977 fixed-load v2: pre-code failure contract

Base: `a30cd0764d76cbabcf9ff69f5cf9715035e885a9` in the isolated
`.worktrees/issue-977-fixed-load-v2` checkout. Artifacts go to the new
`tmp/issue-977-fixed-load-v2-20261001` lab. Historical V1 source, receipts,
failures, acceptance thresholds and the observer-only ABBA remain unchanged.
This is a separately declared diagnostic protocol, not a historical waiver.

## Red before implementation

First add only this plan and `permission_fixed_load_v2_test.go`. The real-process
wrapper is `TestPermissionCallerFixedLoadV2/two-slots-one-remote-leader`.
`WK_E2E_PERMISSION_FIXED_V2_RED=1` selects the original V1 driver by clearing
`WK_E2E_PERMISSION_FIXED_LOAD_V2` and enabling the original fixed-load opt-in.
`WK_E2E_PERMISSION_FIXED_V2_REPORT` must be a new absolute report path. The
wrapper runs the complete original fixture, then requires the v2 protocol,
monotonic arrival/CPU/scrape evidence and retained actual/canonical configs.
It collects every schema error into a `.v2-contract.json` companion and exits
failed. Missing v2 evidence must fail even if V1's functional work succeeds.

Root reviews/freezes the plan/wrapper, builds and runs this expected-red process
test serially with the original calibrated native probe. No v2 implementation
or baseline dispatch switch may be written until its failed receipt is saved.
The green wrapper subsequently uses `WK_E2E_PERMISSION_FIXED_LOAD_V2=1` with
the same name/report environment. V1 remains separately executable.

## Literal input, queue and deadline rules

- Existing real three-node fixture: 256 Hash Slots, 12 physical Slots, metadata
  replica1, message replica3, GOMAXPROCS4, one system UID, 32 preconnected devices,
  one offline history subscriber, the same remote-leader placement and warm SEND.
- Two windows: 30s absolute arrivals plus fixed 2s drain. Sequential: 750
  arrivals at 40ms. Concurrent: 468 full waves of 32 at 64ms, exactly 14,976
  arrivals and 499.2 offered SEND/s. No final partial wave or rate rounding.
- Exactly one persistent worker and one bounded FIFO per caller. Capacity8 is
  **queued-only**, plus at most one active request on that connection: at most
  9 owned tasks/caller and 288 across 32 callers. Queue saturation immediately
  fails that ordinal; the producer never blocks to wait for capacity.
- Scheduler lateness >=40ms/64ms drops/fails the ordinal. Expiry is strictly
  `scheduled -> worker_started <400ms`, including scheduler lateness; it is
  **not** measured from offered time. Record `offered -> worker_started` queue
  wait separately. Queue-full, expiry, late/drop/error/failed ACK or an ordinal
  incomplete at 32s fails the complete window. No retry, catch-up, extra worker,
  new independent connection or measured-window reconnect.
- Each caller's short submit mutex covers seal/deadline checks and the public
  SendFrame call only, never ACK waiting. At 32s seal dispatch/start permission,
  cross every caller submit mutex as a barrier, and mark every queued/active
  ordinal incomplete or completed after 32s as unfinished permanently. Do not
  let a late ACK erase that marker. No queued SEND may start after the seal or
  CPUAfter. If a submit blocks the barrier, its actual cut drift remains invalid.
- Capture CPUAfter before closing still-reading owned clients. Then close and
  join active reads, cancel all queued ordinals explicitly and join the sampler.
  Deferred failure cleanup must do the same. Original post-window controls may
  reconnect a forcibly closed owned caller only outside measurement, recorded
  separately; this cannot rescue a failed window or add independent callers.

## Monotonic arrivals and complete latency population

Every predeclared ordinal retains caller/ID, scheduled/offered/worker-started/
completed UTC companions and signed monotonic relative-to-window-start fields:
`scheduled_offset_ns`, `offered_offset_ns`, `worker_started_offset_ns`,
`completed_offset_ns`, `queue_wait_ns`, `scheduled_to_worker_ns`, and
`scheduled_to_completed_ns`. Missing phases use explicit unavailable markers,
not zero-valued success. Keep all 750/14,976 entries and their failures.

Window `arrival_schedule` and `acks` retain those full populations. Report
nearest-rank `queue_wait_p99_ns` and `scheduled_to_completed_p99_ns` plus their
population counts. A missing/failed/incomplete ordinal makes that full-population
P99 invalid rather than permitting successful-subset qualification. Existing
caller SENDACK timing is retained separately; it never hides queue residence.
UTC is corroborating evidence only, with signed UTC/monotonic differences kept.

## CPU cuts and observation artifacts

- Preserve original `permissionCPUQuery`, C fixture, raw before/after cuts,
  three PID/start identities, raw counters and 125/3 timebase. Calibration first.
  Whole-node CPU includes all observer/background work; subtract nothing.
- Surround each native helper with unmodified time.Now monotonic values. Save
  `cpu_before_call_started_offset_ns`, `cpu_before_call_finished_offset_ns`,
  `cpu_after_call_started_offset_ns`, `cpu_after_call_finished_offset_ns` and
  both call durations. Actual before-start/finish gaps and after-start/finish
  deadline gaps must each be >=0 and strictly <20ms. Native inner/outer spans
  independently must be in [32s,32.04s), derived from <20ms on each side.
  Missing/reused/regressing/native-out-of-bounds samples fail. `cpu_valid` is
  false until original interval validation returns and every actual bound passes.
  Fatal helper/interval exits must keep the window explicitly incomplete.
- Exactly 32 fresh full identity GET /metrics requests at offsets 0.5s..31.5s.
  Outer network deadline is target+250ms; no nested timeout extends it. Reuse
  the original observer raw acquisition/validation helpers without editing them.
  Their >=20ms request-start-lateness failure is retained (stricter than the
  250ms observation budget). No implicit GET retry, cache/filter/decompression.
- `cohort_ownership_samples` has 32 entries with `scheduled_offset_ns`, true
  monotonic `started_offset_ns` and `network_finished_offset_ns`, full `response`
  metadata and post-cut `owned`/`valid`. Start denotes outer Fetch invocation;
  network finish is the original complete-raw-read monotonic boundary. Do not
  reconstruct either from UTC. Preserve all missed/error slots without catch-up.
- Each sample additionally saves real outer monotonic
  `analysis_started_offset_ns`, `analysis_finished_offset_ns`,
  `retention_started_offset_ns`, and `retention_finished_offset_ns`. These prove
  CPUAfter-call finish <= analysis start/finish <= retention start/finish without
  reconstructing phase order from UTC.
- Retain each full body with the original 8MiB response guard and a fixed
  <=256MiB sum of retained full raw payload bytes (32 times 8MiB). This is not an
  io.ReadAll backing-capacity, metadata-allocation or RSS bound; add no pool or
  optimization to conceal that distinction. CPUAfter precedes hash/expfmt validation, owned
  gauge extraction from that same raw body and os.O_EXCL raw writes. All 32 must
  fully validate and be saved; acquisition/analysis/storage failures stay failed.
  Use `${report}.raw/${case}/metrics/block-{1|32}-identity-NNN.metrics` so windows
  and placements cannot collide. A prefix is explicitly truncated/failed.
- Keep full all-node metric cuts before/after, ownership limits/drain-to-zero,
  unchanged fresh-read/envelope/count assertions, all 32 ban/unban decisions,
  complete exact 15,759-message history with 158 planned pages/hard cap200,
  source/binary identity, stable topology and verified process cleanup.

## Actual config and scope binding (root-owned wiring)

Each `node_configs[id]` keeps `sha256`/`overrides` and adds
`retained_config_path`, `retained_config_sha256`, `canonical_config_path`,
`canonical_config_sha256`, and explicit `normalization_rules`. Copy the actual
rendered process config bytes directly before cleanup; never re-render them.
Hash raw and canonical bytes separately. Normalize only predeclared ephemeral
loopback ports and workspace/socket/data paths; keep every substantive setting
and unknown key. Canonical equivalence cannot conceal different product flags.

Root owns baseline dispatch/report/config retention and scenario catalog updates;
this subtask owns the new wrapper, new v2 driver and this plan. No V1/suite/native
helper file changes. Preserve AA1/AA2 before A1/B1/B2/A2/A3/B3, every failed run,
and the original +5% CPU/P99 limits; no averaging or removed failed population
manufactures product qualification. All green live runs/builds/commits are root
owned and serial. No live/build/test/commit is authorized in this editing task.

Only root's pinned Go may run:
`tmp/issue-977-darwin-sync-20261001/go1.25.11/bin`, with GOTOOLCHAIN=local,
GOWORK=off, GOPROXY=off, GOMAXPROCS=4 and root's frozen modcache/buildcache.

## Instruction digests frozen from the exact clean base before edits

- AGENTS.md: `6df6655788a5dc019c78c330728aee6a21e7183ebbec9f2583d7f318a26df2df`
- test/e2e/AGENTS.md: `b3a9e0fed6019fc741f7829b9917d4ce288c7108a9207cdcb718e07238797505`
- test/e2e/message/AGENTS.md: `8ea7d9df983c7c16d637cee48c4e1069ac3a008fa95f4d8f2235bd30ed1842b1`
- test/e2e/message/send_ban/AGENTS.md: `1c5de355e9eca7a931ebfb87c8008d0e7a7b3a199919f88926aea1a7fb3ddaa6`
- test/e2e/suite/FLOW.md: `8101e4379711cc2700416ae801461de9bc2cb3570053278cd0bdf0a94a9d3d14`
