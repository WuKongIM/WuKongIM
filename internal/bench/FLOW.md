---
scope: subtree
summary: Implements deterministic black-box benchmark planning, workers, traffic, and evidence.
---

# internal/bench Flow

## Responsibility

`internal/bench` is the reusable runtime behind `cmd/wkbench`. It validates
benchmark inputs, builds deterministic worker plans, drives black-box target
setup and WKProto traffic, evaluates bounded evidence, and writes reports.
Subpackages own config, planning, coordination, workers, workloads, target
clients, capacity searches, metrics, and reports.

## Boundaries

Benchmark code may use public HTTP/bench APIs, WKProto clients, and shared DTOs
from `pkg/bench/model`. It must not import WuKongIM server internals, inspect
target storage, orchestrate containers, or mutate the target outside declared
public APIs. `pkg/client` owns protocol pumps and bounded transport queues.

## Main Flows

```text
wkbench run
  -> strict target/worker/scenario validation
  -> deterministic plan and immutable worker assignments
  -> preflight
  -> prepare -> connect -> warmup -> run -> cooldown
  -> exact-assignment stop and stable evidence collection
  -> verdict and report directory

capacity command
  -> discover an already-running target
  -> run bounded attempts with one temporary worker
  -> classify actual throughput, errors, latency, and cleanup

worker assignment
  -> exact run_id + assignment_id generation
  -> bounded phase task and owned sessions
  -> synchronous admission fence, joined teardown, terminal snapshot
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
- Measured windows stop scheduling at their deadline and report achieved QPS;
  they do not extend time to drain an imagined offered schedule.
- Target and response parsing is bounded and redacted. Reports keep fixed
  counters, histograms, reason codes, and bounded samples without raw UIDs,
  Channel IDs, credentials, response bodies, or arbitrary error text.
- Static/config failures, target unavailability, worker failures, hard-limit
  failures, cancellation, and internal failures retain distinct outcomes.

## Read First

- [planner/planner.go](planner/planner.go)
- [coordinator/run.go](coordinator/run.go)
- [worker/server.go](worker/server.go)
- [target/client.go](target/client.go)
- [report/report.go](report/report.go)

## Update Triggers

- Package ownership or the black-box/public-API boundary changes.
- Assignment fencing, phase ordering, stop/join, or evidence stability changes.
- Queue, concurrency, response, retry, or report bounds change.
- Planning determinism, measured-window semantics, or verdict attribution changes.
