---
scope: package
summary: Runs the fenced chat-lifecycle workload, evidence pipeline, and aged-data capacity proof.
---

# internal/bench/chatlifecycle Flow

## Responsibility

This package owns deterministic chat-lifecycle planning, its three-worker protocol, bounded session/traffic engine,
continuous observation, natural Channel lifecycle proof, evidence reduction, and versioned reports. It neither provisions infrastructure nor persists credentials or bypasses public APIs.

## Boundaries

Production composition injects public HTTP, WKProto, worker-control, metrics, host-observation, clock, and report ports; `internal/bench/wkproto` owns transport.
Cloud orchestration owns paid infrastructure and cleanup; this package consumes immutable cost, expiry, topology, and dataset evidence.

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
  Snapshot captures Engine/generator on one owner turn; one cancelable requester
  probes clients outside it. Every queue read must succeed; Stop joins before close.
- The coordinator is the sole global rate allocator. Workers apply only their
  sequenced share; delayed ticks discard credit and never catch up in bursts.
  After the immediate bootstrap grant, external grants retain seeded subsecond
  SEND deadlines. Frequent due-work advancement does not advance the one-second
  login or rate-allocation clock; overdue planned SENDs remain explicit underdelivery.
- Before CONNECT, each login upserts its APP token through the authenticated Bench API within five seconds and the session concurrency bound. Failure blocks CONNECT; Bench secrets stay worker-local and no UID history is kept.
- Full HTTP sync has four active callers per generation (twelve across the validated three workers). Remaining callers retain bounded Starting ownership and their original context; waiting is included in sync latency. CONNECT/SEND clocks, endpoint fallback, failure evidence and retries stay unchanged.
- Each workload connection starts with one 8KiB owned read buffer, with incomplete
  frames growing on demand. Queue capacities and deadline/acceptance gates remain unchanged.
- The measured clock starts only after all users finish real CONNECT plus a fresh zero-coverage conversation sync and all workers accept the first grant.
- Repository YAML keeps reviewed stage durations fixed; only the direct repair rehearsal CLI may inject and report a bounded process duration, and formal or shakeout evidence never accepts it.
- Engine heaps, maps, queues, correlations, samples, and histories are bounded.
  A fixed executor pool calls the SDK outside owner/control turns with per-sender FIFO; queued tickets retain work/retry credit and original dispatch deadlines. Only actual execution registers SEND and its attempt clock.
  One generation-owned advance retains its time cut and total budget across slices of32 work and frozen prefixes of at most32 commands; Snapshots may see partial progress. Two-half time leases retain canceled jobs and caller postprocessing; Stop joins logical jobs before executors and clients. AdmissionPending distinguishes queued/executing SDK work.
  Planning is history-independent; no historical user or Channel owns retained work. A returning-login cold revisit never overlaps the active or pending hot set.
  Session expiry skips online-table locking while a conservative atomic deadline bound proves nothing can be due; actual due sets reserve one generation-owned bounded cleanup queue.
  Closing tombstones and per-SEND expiry leases prevent replacement or socket
  teardown from racing admitted retry work; pinned overdue sessions remain due hints.
- Retry admission and owner terminal decisions linearize with direct ACK verification.
  Only an exact registered positive completion receipt may transfer to the Engine's
  existing ACK settlement while its callback is queued. Missing state proves nothing;
  strict public registration, real terminal errors and correctness budgets remain.
- Initial SENDs settle a timer-local pending count before revisit leasing, so late successful ACKs advance the fence before cohort selection.
- The first measured grant reserves distinct person/group/canary senders at formal population.
  Channels become hot at their first successful SENDACK; metadata-create vectors and hot/first-create/reheat evidence follow that boundary.
- Worker protocol v15 keeps deadline registration, exact-result coverage and confirmed observed-late first-attempt counts separate.
  Only the original clocked transport result resolves its deadline; missing/unknown/canceled/expired results remain unknown. Fixed attempt slots and the bounded late-receipt map bound retention. Private opt-in retains 16 fresh-dispatch timing prefixes per generation.
  Added clock reads are diagnostic overhead; codes, units and gates stay fixed.
  A fixed optional yield count/wall prefix independently brackets completed owner Gosched calls; it overlaps unchanged Residual, is not actor CPU, and keeps invalid clocks unknown. Only the named optional child is sanitized; original driver/native JSON stays strict, with same-source v15 peers required for the added diagnostic field. Default-off paths add no clock reads.
  Owner timing covers active slices and bounded completion batches; logical Residual/CPU envelopes may include interleaved work. Optional CPU bounds retain16 failed logical turns with unknown clocks preserved, exported post-join; process CPU is not actor CPU. A separate default-off SDK child retains16 exact SDK-start/code38 misses using independent queue-offer/pickup/command-offer/owner clocks; unknown stays unknown, no clock is added to native time, and the final64KiB cap remains.
  Driver timing records cross only after generation Stop joins, into their bounded terminal sidecar. A separate strict attempt-link opt-in copies at most16 confirmed-late original keys, connection scopes and reused SDK timings per generation into detached live/final control cuts. Its separately bounded64KiB atomic sidecar uses existing worker fences/sequence/uptime and never enters native evidence, counts or gates; malformed optional data is discarded independently.
- A nil-by-default local callback notifies only after an authenticated, current, valid, uncanceled harness-failure Snapshot GET or coordinator Checkpoint POST, including cached final Snapshot GETs, outside the lifecycle mutex.
  The worker CLI owns its independent opt-in recorder and one asynchronous export; Snapshot, Stop and UnexpectedExit never join that diagnostic writer.
  Enabled runtime tracing adds only static regions around the two existing session-expiry calls and an existing anonymous turn when available.
  These are wall intervals, not pure CPU attribution; absent, partial or evicted capture keeps coverage unknown. Native fields, deadlines, queues, evidence and gates remain unchanged.
- Under-delivery retains the five-field redacted evidence shape. Append-only
  codes distinguish sampled/fresh dispatch deadlines, activity/lifecycle eligibility,
  person/group routing, deferred-group/canary eligibility, and both cleanup paths.
  Historical code 27 identifies no emitter. Branch codes preserve original numeric
  units and harness verdicts; they do not attribute scheduler or server causes.
  CONNECT codes 47–53 add closed phase/kind or rejection reason and a packet digest.
  Legacy code 30 stays unknown; no raw cause, identity or credential is retained.
  Terminal worker-evidence v3 carries these codes; the collector also reads
  historical v1/v2 with their original vocabularies. Local snapshot/status
  capture is bounded at 256 KiB, below the worker API's 4 MiB response limit;
  three-worker terminal evidence remains bounded at 1 MiB.
- Local transport-admission rejection stays explicit harness evidence and is
  excluded from product first-attempt failure rates. Retry exhaustion keeps a
  fixed trigger breakdown; sampled delivery loss starts only after a successful
  SENDACK proves target acceptance.
- Unroutable sampled group work remains bounded and identity-stable until expiry.
  A large-group canary retains two generation-local roster anchors; these O(1)
  anchors disappear on transport failure or shutdown and never grow with group size.
- Lifecycle proof leases 12x100 current Slot candidates with a 30-second probe/
  approval reserve and closed rejection reasons. Natural cooling, fenced scheduled
  SENDs and post-reheat probes prove cold absence and sequence continuity.
- Each batch uses its admitted RPC start/end interval. Boundary-crossing absence
  needs fresh proof by the original deadline; definite early absence and existing
  runtime/watermark/reload gates remain. Approval uses a fresh request cut and
  the worker's strict due-time fence.
- Observer rounds retain resource source time; only late verdict order rebases, preserving exact-hour evidence.
- Failure cleanup fences new work and independently bounds every worker Stop, preserving the original cause.
  Exact product TerminalSend errors permit release; other terminal mutation errors stay harness failures. Stop preserves earlier failures.
- Grant failures retain three fixed worker sequence/attempt/closed-boundary entries.
  Only observed HTTP/context causes enter one bounded CLI line; summary, retries, deadlines, cleanup and reports stay unchanged.
- Verdict precedence is product, infrastructure, harness, then operator stop.
  Missing, stale, regressing, partial, overflowing, or unbounded evidence can
  never produce pass.
- Latency warmup is ten minutes independently of the formal two-hour resource
  warmup. Its first valid cumulative cut is a baseline. Positive latency evidence
  needs a rolling ten-minute interval, 1,000 samples in each hot/cold/full-sync
  class, and a cut at most five seconds old. Counter deltas belong to their end
  cut; a gap over ten minutes resets positive qualification, not negative breaches.
  The five-minute breach/correctness gates stay fixed; hot attempt/retry wait is2s independently of400ms/1sSLOs, cold wait5s.
- Report v8 / threshold v2 binds local-diagnostic, rehearsal, or formal-protocol scope to validated configuration.
  Formal scope still needs external Linux hardware/topology; elapsed rehearsal and missing/stale positive latency evidence cannot pass.
  Baseline/start/end elapsed positions share one monotonic clock; serialized duration/order/freshness use them. Wall times are audit fields.
  Diagnostic readers never reconstruct offsets or upgrade old reports; legacy v7 has no hot-budget override and stays original/unknown. Current result/capacity requires v8; Formal v8 declares2s; old strict readers cannot consume v8.
  The fixed three-class native latency_gate copies actual five-minute sums, continuous breach state and unchanged threshold fractions.
  Its first terminal cut freezes only the child; captured latency_age/status do not claim publication freshness (report elapsed minus evaluated elapsed).
  Absent/null proof stays unknown; present proof is strict and bounded, independently of positive ten-minute qualification; no new clock or numerical gate.
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
- [worker_server.go](worker_server.go), [engine.go](engine.go), and [production_controller.go](production_controller.go)

## Update Triggers

- Worker fencing, grants, engine/session lifecycle, or cleanup semantics change.
- Lifecycle, observation, verdict, cost, continuity, or report schemas change.
