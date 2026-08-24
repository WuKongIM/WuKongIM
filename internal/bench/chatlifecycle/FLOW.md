---
scope: package
summary: Runs the fenced chat-lifecycle workload, evidence pipeline, and aged-data capacity proof.
---

# internal/bench/chatlifecycle Flow

## Responsibility

This package owns deterministic formal, rehearsal, capacity, and local
chat-lifecycle planning; the dedicated three-worker protocol; the bounded
session/traffic engine; continuous cluster and resource observation; natural
Channel lifecycle proof; evidence reduction; and versioned reports. It does
not provision infrastructure, persist credentials, implement protocol pumps,
or bypass public target APIs.

## Boundaries

Production composition injects public HTTP, WKProto, worker-control, metrics,
host-observation, clock, and report ports. `internal/bench/wkproto` owns client
transport. Cloud Lease and orchestration own paid infrastructure and cleanup;
this package consumes their immutable cost, expiry, topology, and dataset
evidence and emits terminal classifications.

## Main Flows

```text
strict config and black-box preflight
  -> fixed group setup
  -> exact three-worker assignment and Start rounds
  -> 10,000 CONNECT plus fresh full-sync readiness
  -> first complete global grant
  -> measured clock, continuous grants, observation, and lifecycle proof
  -> cutoff, stable worker stop, evidence reconciliation, atomic report
  -> atomically replace bounded live diagnostic status on every evidence cut

passing 72-hour formal generation
  -> prove the same live aged dataset and process generation
  -> continue the same workers, grants, and observer
  -> bounded capacity staircase and 2,000 SEND/s recovery
```

## Invariants and Failure Semantics

- Current worker protocols use constant-time bearer verification and exact
  `run_id + assignment_id + generation` fences. Status retains the active and
  latest cached grant sequence/result, while snapshots expose only bounded
  scalar aggregates and fixed metadata-create and latency partitions. Assignment, Start, grant,
  status, checkpoint, rate, and stop rounds are bounded and attempt all three
  workers concurrently.
- The coordinator is the sole global rate allocator. Workers apply only their
  sequenced share; delayed ticks discard credit and never catch up in bursts.
- The measured clock starts only after all users finish real CONNECT plus a
  fresh zero-coverage conversation sync and all workers accept the first grant.
- Engine heaps, maps, queues, correlations, samples, and histories have checked
  capacities. Planning is history-independent; no historical user or Channel
  owns retained goroutines, timers, or map rows.
  Session expiry transfers to one generation-owned bounded cleanup queue;
  closing tombstones and per-SEND expiry leases prevent replacement or socket
  teardown from racing admitted retry work.
- The first measured grant reserves distinct senders across person, group, and
  canary traffic at the formal population. A Channel becomes hot only after
  its first successful SENDACK; metadata-create vectors and hot/first-create/
  reheat latency evidence advance from those exact successful boundaries.
- Local transport-admission rejection stays explicit harness evidence and is
  excluded from product first-attempt failure rates. Retry exhaustion keeps a
  fixed trigger breakdown; sampled delivery loss starts only after a successful
  SENDACK proves target acceptance.
- Lifecycle proof leases at most 100 current candidates per each of 12 logical
  Slot groups. It never evicts runtimes: all replicas must cool naturally, one
  fenced approval unlocks the already scheduled real SEND, and post-reheat
  probes must prove monotonic sequence continuity.
- Failure cleanup fences new work, attempts exact stop for every applicable
  worker with an independent bound, and never overwrites the original cause.
- Verdict precedence is product, infrastructure, harness, then operator stop.
  Missing, stale, regressing, partial, overflowing, or unbounded evidence can
  never produce pass.
- Reports and control responses use closed reason vocabularies, fixed arrays,
  checked arithmetic, and bounded redacted samples. Raw UIDs, Channel IDs,
  payloads, credentials, endpoint bodies, and arbitrary errors are forbidden.
  The running diagnostic file contains only three-worker connection gauges,
  teardown reasons, message aggregates, and at most 64 recent changes.
- The native local staircase is a non-formal typed-evidence classifier; an early
  proven product failure is not downgraded before warmup qualification.
- Formal-to-capacity continuation cannot restart workers, reset the dataset,
  replace the observer, or reuse a clean cluster. Cost-stop and Lease-expiry
  risk remain terminal throughout rehearsal, formal, and capacity stages.

## Read First

- [config.go](config.go)
- [coordinator.go](coordinator.go)
- [worker_server.go](worker_server.go)
- [engine.go](engine.go)
- [production_controller.go](production_controller.go)

## Update Triggers

- Worker fencing, grants, engine/session lifecycle, or cleanup semantics change.
- Lifecycle, observation, verdict, cost, continuity, or report schemas change.
