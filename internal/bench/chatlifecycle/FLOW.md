---
scope: package
summary: Runs the fenced chat-lifecycle workload, evidence pipeline, and aged-data capacity proof.
---

# internal/bench/chatlifecycle Flow

## Responsibility

This package owns deterministic chat-lifecycle planning, its three-worker
protocol, bounded session/traffic engine, continuous observation, natural
Channel lifecycle proof, evidence reduction, and versioned reports. It does
not provision infrastructure, persist credentials, or bypass public APIs.

## Boundaries

Production composition injects public HTTP, WKProto, worker-control, metrics,
host-observation, clock, and report ports; `internal/bench/wkproto` owns transport.
Cloud orchestration owns paid infrastructure and cleanup; this package consumes
immutable cost, expiry, topology, and dataset evidence.

## Main Flows

```text
strict black-box preflight -> group setup and fenced worker Start
  -> fixed global 100-login/s bootstrap to 10,000 CONNECT plus fresh full-sync readiness
  -> settle initial activity, then first complete global grant
  -> measured clock, continuous grants, observation, and lifecycle proof
  -> cutoff, stable worker stop, evidence reconciliation, atomic report
  -> atomically replace bounded live diagnostic status on every evidence cut

passing 72-hour formal generation -> same aged dataset and process proof
  -> continue the same workers, grants, and observer
  -> bounded capacity staircase and 2,000 SEND/s recovery
```

## Invariants and Failure Semantics

- Initial Soak health/readiness probes share one 30-second startup deadline
  and retry at 250ms intervals. Authentication and cancellation stop the gate;
  config, metrics, cluster and capability checks remain strict and never retry.
  Capacity continuation remains single-pass. Diagnostics use fixed outcomes/positions.
- Worker control uses constant-time bearer verification and exact
  `run_id + assignment_id + generation` fences. Status and snapshots retain only
  bounded aggregates. All control rounds are bounded and attempt three workers concurrently.
- The coordinator is the sole global rate allocator. Workers apply only their
  sequenced share; delayed ticks discard credit and never catch up in bursts.
  After the immediate bootstrap grant, external grants retain seeded subsecond
  SEND deadlines. Frequent due-work advancement does not advance the one-second
  login or rate-allocation clock; overdue planned SENDs remain explicit underdelivery.
- Before CONNECT, each login upserts its APP token through the authenticated
  Bench API within five seconds and the session concurrency bound. Failure
  blocks CONNECT; Bench secrets stay worker-local and no UID history is kept.
- The measured clock starts only after all users finish real CONNECT plus a
  fresh zero-coverage conversation sync and all workers accept the first grant.
- Repository YAML keeps reviewed stage durations fixed; only the direct repair rehearsal CLI may inject and report a bounded process duration, and formal or shakeout evidence never accepts it.
- Engine heaps, maps, queues, correlations, samples, and histories are bounded.
  Planning is history-independent; no historical user or Channel owns retained work. A returning-login cold revisit never overlaps the active or pending hot set.
  Session expiry transfers to one generation-owned bounded cleanup queue;
  closing tombstones and per-SEND expiry leases prevent replacement or socket
  teardown from racing admitted retry work.
- Retry admission and owner terminal decisions linearize with direct ACK verification.
  Only an exact registered positive completion receipt may transfer to the Engine's
  existing ACK settlement while its callback is queued. Missing state proves nothing;
  strict public registration, real terminal errors and correctness budgets remain.
- Initial SENDs settle a timer-local pending count before revisit leasing, so
  late successful ACKs advance the fence before cohort selection.
- The first measured grant reserves distinct senders across person, group, and
  canary traffic at the formal population. A Channel becomes hot only after
  its first successful SENDACK; metadata-create vectors and hot/first-create/
  reheat latency evidence advance from those exact successful boundaries.
- Worker protocol v11 carries deadline registration, exact-result coverage, and
  confirmed observed-late first-attempt counts separately from explicit failures.
  Only the original attempt's clocked transport result resolves its deadline;
  missing results, unknown clocks, cancellation and expired receipts stay unknown.
  Fixed attempt slots and the existing bounded late-receipt map bound retention.
- Local transport-admission rejection stays explicit harness evidence and is
  excluded from product first-attempt failure rates. Retry exhaustion keeps a
  fixed trigger breakdown; sampled delivery loss starts only after a successful
  SENDACK proves target acceptance.
- Unroutable sampled group work remains bounded and identity-stable until expiry.
  A large-group canary retains two generation-local roster anchors; these O(1)
  anchors disappear on transport failure or shutdown and never grow with group size.
- Lifecycle proof leases at most 100 current candidates per each of 12 logical
  Slot groups. All replicas cool naturally; a fenced approval batch unlocks only
  scheduled SENDs, and post-reheat probes prove sequence continuity. Selection
  reserves 30 seconds for probe/approval jitter; rejection reasons remain closed.
- Observer rounds retain source time for resource scheduling; late samples rebase
  only verdict order, so monotonic classification keeps exact-hour evidence.
- Failure cleanup fences new work, attempts exact stop for every applicable
  worker with an independent bound, preserving the original cause.
- Verdict precedence is product, infrastructure, harness, then operator stop.
  Missing, stale, regressing, partial, overflowing, or unbounded evidence can
  never produce pass.
- Latency warmup is ten minutes independently of the formal two-hour resource
  warmup. Its first valid cumulative cut is a baseline. Positive latency evidence
  needs a rolling ten-minute interval, 1,000 samples in each hot/cold/full-sync
  class, and a cut at most five seconds old. Counter deltas belong to their end
  cut; a gap over ten minutes resets positive qualification, not negative breaches.
  The numerical five-minute breach rule and correctness gates remain unchanged.
- Report v6 / threshold v2 binds local-diagnostic, rehearsal, or formal-protocol
  scope to validated configuration. Formal-protocol scope still requires external
  Linux topology/hardware evidence. Elapsed rehearsal time alone cannot pass,
  and missing/stale latency qualification cannot yield a successful report.
- Reports and control responses use closed reason vocabularies, fixed arrays,
  checked arithmetic, and bounded redacted samples. Raw UIDs, Channel IDs,
  payloads, credentials, endpoint bodies, and arbitrary errors are forbidden.
  The running diagnostic file contains only three-worker connection gauges,
  teardown reasons, message aggregates, and at most 64 recent changes. Worker-cut logs also retain configured hot SENDACK P99 threshold/sample/above-threshold counts for interval diagnosis without altering verdicts.
- Metrics-fetch finalization failures retain a closed three-source mask/kind
  vector in declared service order, with optional fixed parser reasons only at
  parse positions. Older reports keep that vector absent; malformed optional
  reasons are discarded independently from existing fields. All sources are
  joined; parent context wins, and private child causes never change the
  aggregate harness classification.
- The native local staircase is a non-formal typed-evidence classifier; an early
  proven product failure is not downgraded before warmup qualification.
- Formal-to-capacity continuation cannot restart workers, reset the dataset,
  replace the observer, or reuse a clean cluster. Cost-stop and Lease-expiry
  risk remain terminal throughout rehearsal, formal, and capacity stages.

## Read First

- [config.go](config.go) and [coordinator.go](coordinator.go)
- [worker_server.go](worker_server.go)
- [engine.go](engine.go)
- [production_controller.go](production_controller.go)

## Update Triggers

- Worker fencing, grants, engine/session lifecycle, or cleanup semantics change.
- Lifecycle, observation, verdict, cost, continuity, or report schemas change.
