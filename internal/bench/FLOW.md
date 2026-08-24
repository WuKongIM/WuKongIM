---
scope: subtree
summary: Implements deterministic black-box benchmark planning, workers, traffic, and evidence.
---

# internal/bench Flow

## Responsibility

`internal/bench` is the reusable runtime behind `cmd/wkbench`. It validates
inputs, builds deterministic worker plans, drives black-box target setup and
WKProto traffic, evaluates bounded evidence, writes reports, and owns strict
local-baseline gates. It does not provision targets or bypass public APIs.

## Boundaries

Benchmark code may use public HTTP/bench APIs, WKProto clients, and DTOs from
`pkg/bench/model`. It must not import server internals, inspect target storage,
or orchestrate containers. `pkg/client` owns protocol pumps and bounded queues.

## Main Flows

```text
wkbench run
  -> strict target/worker/scenario validation
  -> deterministic plan and immutable worker assignments
  -> preflight
  -> exact fenced assignment with bounded owned sessions
  -> prepare -> connect -> warmup -> run -> cooldown
  -> exact-assignment stop, joined teardown, and stable evidence collection
  -> verdict and report directory

chat lifecycle
  -> fixed global 100-login/s bootstrap to 10,000 synchronized online users
  -> first global grant, measured clock, sequenced grants, observation, and proof
  -> stable worker stop, evidence reconciliation, and terminal classification

reviewed external terminal cut
  -> require one target, one worker, and advertised prepare capability
  -> target grant + joined SEND/receive proof + product queue convergence
  -> exact server marker ACK + immutable terminal_pre_close cut

native local baseline
  -> fresh product generations for fixed 250/500/750/1,000 SEND/s steps
  -> typed closure, process/storage/I/O evidence, and sealed attestation
  -> optional fresh ten-minute 1,000 SEND/s soak only after four clean steps
```

## Invariants and Failure Semantics

- Planning is deterministic from validated config and seed; identity, Channel,
  traffic, and worker partitions do not overlap or retain unbounded history.
- Every worker mutation and evidence read is fenced by exact run and assignment
  identity. Reusing a run ID never aliases another assignment generation.
- Coordinator terminal paths attempt exact stop for every assigned worker
  before reading stable metrics/reports. Moving or unconfirmed evidence yields
  a harness-invalid result rather than a best-effort report.
- Client publisher, RECV, SENDACK, error, inflight, and worker queues are
  explicitly bounded and lossless. Backpressure or cancellation is visible;
  evidence is not evicted to keep offered load moving.
  Queue-to-consumer handoff leases keep dequeued work observable until the
  matching reader owns it; terminal receive proof requires two separated,
  stable zero-work cuts and complete physical RECV/RECVACK counters.
- Measured windows stop scheduling at their deadline and report achieved QPS;
  admitted work drains only inside the same bounded cooldown. Explicit generic
  retry uses one logical identity, fresh ClientSeq values, and only the fixed
  100 ms, 500 ms, and 2 s delays.
- High-concurrency round-robin group traffic selects an idle member only at
  admission and shares sharded per-session credits. Reviewed group runs also
  prove expected, received, and acknowledged fanout with bounded anonymous
  multiset witnesses; equal totals cannot hide a duplicate and a missing peer.
- External terminal proof is not a TCP half-close or buffer observation. It
  requires the grant-bound marker plus Gateway, append, delivery, ACK-binding,
  and client receive convergence; a failed seal still performs ordinary stop.
- Report schemas keep hot, first-create, and reheat SENDACK histograms distinct.
  Running status and retained artifacts contain closed reason vocabularies,
  fixed aggregates, and redacted credentials, never raw identities or errors.
- Observer rounds retain their source clock for resource schedules while late
  verdict samples rebase; exact-hour resource evidence is not shifted or lost.
- Local-baseline authorization replays no-follow manifests, typed closures,
  process generations, immutable config/binary digests, and complete storage
  and host evidence. Missing, stale, contradictory, or transplanted evidence
  fails closed and never becomes a formal or capacity verdict.
- Target and response parsing is bounded and redacted. Reports keep fixed
  counters, histograms, reason codes, and bounded samples without raw UIDs,
  Channel IDs, credentials, response bodies, or arbitrary error text.
- Static/config, target, worker, hard-limit, cancellation, and internal failures retain distinct outcomes.

## Read First

- [planner/planner.go](planner/planner.go)
- [coordinator/run.go](coordinator/run.go)
- [worker/server.go](worker/server.go)
- [target/client.go](target/client.go)
- [report/report.go](report/report.go)

## Update Triggers

- Package ownership, public-API boundaries, assignment fencing, or evidence stability changes.
- Queue, retry, report, planning, measured-window, or verdict semantics change.
