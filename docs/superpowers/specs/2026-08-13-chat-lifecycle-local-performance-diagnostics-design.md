# Chat Lifecycle Local Performance Diagnostics Design

**Status:** Approved

**Date:** 2026-08-13

## Problem Statement

The native local chat-lifecycle shakeout can keep 2,500 WKProto sessions online,
but offering 1,000 SEND/s to three WuKongIM processes on one Mac does not
produce 1,000 timely SENDACKs. The service completes only about 300-400
SENDACKs/s, request latency crosses the attempt deadline, stable-identity
retries amplify the physical traffic, and terminal cleanup then closes the
otherwise healthy connections. This makes the resulting connection drop look
like a connection-capacity failure even though the failure begins in the
durable send path.

The local topology is not equivalent to the formal Cloud topology. All three
local service nodes write their durable replica state to one nearly full APFS
SSD, while the approved formal Chat Lifecycle Run uses three independent
service hosts and independent data disks. A focused storage benchmark measures
about 4.5 ms for a one-record synchronous commit. One engine can amortize this
cost with grouped commits, but three engines concurrently writing three
replicas to the same physical device sustain only about 510-566 logical
messages/s before Raft, delivery, snapshots, and benchmark verification are
included. The observed end-to-end 300-400 SENDACKs/s is consistent with that
lower bound.

The generic single-node and three-node 1,000-Channel performance wrappers use
inconsistent commit-coordinator settings. The single-node wrapper selects a
2 ms collection window and the generic three-node wrapper selects a 1 ms
window with four shards. The chat-lifecycle shakeout itself already inherits a
200 microsecond window with one shard from the native node configurations.
Controlled local benchmarks show that the latter setting performs better on
this device. Multiple coordinators fragment grouped commits and make several
physical commits compete for the same fsync resource.

WuKongIM already exports the storage evidence needed to distinguish commit
pressure from CPU, worker-pool, network, WAL, flush, or compaction pressure.
The local wrappers retain raw Prometheus snapshots but do not turn the storage
series into an operator-readable result. Planned shutdown cancellation and
successfully recovered idempotency conflicts also generate large volumes of
logs that obscure the first causal timeout.

The project therefore needs a local diagnostic contract that:

- distinguishes online connection capacity from offered SEND rate;
- separately qualifies the load generator and the three-replica lifecycle;
- preserves synchronous durable commit and cluster semantics;
- identifies the highest clean rate supported by the actual local storage
  topology;
- captures enough bounded evidence to explain the first failure without
  opening cloud infrastructure; and
- never weakens the approved full-scale rehearsal or formal Cloud acceptance
  criteria.

## Solution

Add a two-baseline native local diagnostic around the existing benchmark and
chat-lifecycle seams.

The **single-node cluster throughput baseline** exercises the real clustered
gateway and durable message path with one physical message engine. It retains
12 logical Slot Raft Groups and 256 hash slots while using the only valid
single-node replica count of one. It holds at least 2,500 WKProto sessions
online and advances through 250, 500, 750, and 1,000 offered SEND/s. This
baseline answers whether the load generator, Gateway, Channel runtime, durable
message engine, SENDACK path, and local verification can sustain 1,000 SEND/s
when physical replica writes are not competing on one device. It is a
diagnostic baseline, not a substitute for a three-replica result.

The **three-node shared-storage lifecycle baseline** retains three native
WuKongIM processes, three Slot replicas, three Channel replicas, normal
cross-node routing, real conversation synchronization, natural hot-to-cold
eviction, and reheat verification. It advances through a low-rate staircase to
measure the clean local storage knee instead of declaring 1,000 SEND/s an
unconditional pass requirement. The staircase begins at 100 SEND/s, then tries
150, 250, 400, 500, 750, and 1,000 SEND/s until the first failed step. It then
refines the interval between the highest pass and first failure to 10%
precision. A failure at the expected shared-device boundary is a local
infrastructure limitation, not a product-capacity verdict.

Both baselines use one commit coordinator per physical message database, a
200 microsecond collection window, synchronous commits, stable idempotency
identities, and bounded retries. The setting is explicit in local evidence; it
does not become a topology-independent product default without a separate
sustained qualification.

Each rate step has a warmup, measured interval, and drain interval. New SENDs
stop before connections close. Heartbeats, inbound RECV processing, RECVACK,
SENDACK correlation, and retry completion remain active while the system
drains. A step becomes terminal only after the drain deadline or exact queue
convergence, so benchmark cleanup cannot manufacture the primary failure.

The wrappers summarize existing storage metrics for every node and rate step:

- logical commit requests and physical commit batches;
- requests, records, and bytes per physical commit;
- collect, build, physical commit, publish, and total duration;
- caller-visible commit-request latency by lane and result;
- commit queue depth;
- message WAL input and physical bytes written;
- flush bytes and count;
- compaction bytes read and written, estimated debt, and active concurrency;
- live SSTable size and read amplification; and
- storage usage and free-space preflight.

Platform-supported host sampling records physical-device IOPS, bytes/s,
utilization, and service time. Unsupported host counters are marked unavailable
rather than fabricated. Process CPU, RSS, goroutines, runtime queues, Channel
append pressure, cluster transport, and optional CPU/heap/goroutine profiles
remain part of the same evidence directory.

The summary aligns three timelines: workload progress, product metrics, and
host storage samples. It identifies the first breach before retries or cleanup,
then records the retry and shutdown amplification separately. Cold startup,
steady state, snapshot/compaction overlap, drain, and shutdown are distinct
windows.

The standard full-scale Chat Lifecycle Run remains unchanged: the paid
rehearsal and formal Simulation Runs use three independent service machines and
data disks, 10,000 online sessions, 2,000 primary SEND/s, 12 logical Slot Raft
Groups, 256 hash slots, and replica counts of three. A local single-node pass or
shared-storage knee can authorize better-targeted cloud testing, but cannot
produce `rehearsal_pass`, an hour-24/hour-72 checkpoint, or a formal capacity
verdict.

## User Stories

1. As an operator, I want the report to distinguish online connections from
   offered SEND/s, so that a send-path timeout is not described as a connection
   failure.
2. As an operator, I want to sustain at least 2,500 local sessions while testing
   1,000 SEND/s, so that the load shape exceeds the requested 1,000 concurrent
   sessions without conflating the two dimensions.
3. As an operator, I want a single-node cluster throughput baseline, so that I
   can determine whether the generator and hot send path reach 1,000 SEND/s
   before purchasing cloud infrastructure.
4. As an operator, I want the single-node deployment to retain cluster routing
   and durable storage semantics, so that the diagnostic does not exercise a
   special standalone business path.
5. As an operator, I want the single-node baseline to retain 12 logical Slot
   Raft Groups and 256 hash slots, so that reducing the replica count does not
   also collapse the reviewed partition topology.
6. As an operator, I want a native three-node lifecycle baseline, so that
   replication, full conversation synchronization, hot/cold/reheat behavior,
   and cross-node routing remain covered locally.
7. As an operator, I want the three-node local test to find the actual clean
   shared-disk knee, so that one physical SSD is not mistaken for three
   independent production disks.
8. As an operator, I want the rate staircase to stop at the first failed step
   and refine the boundary, so that retries do not create unnecessary prolonged
   overload.
9. As an operator, I want every rate step to stop generation before closing
   sessions, so that pending SENDACKs and retries can drain cleanly.
10. As an operator, I want a bounded drain deadline and exact remaining-work
   counters, so that a test cannot wait forever or silently discard work.
11. As an operator, I want terminal cleanup cancellations separated from
    runtime failures, so that thousands of expected `context canceled` records
    do not hide the first causal timeout.
12. As an operator, I want stable `client_msg_no` reuse on retries, so that the
    diagnostic proves idempotent durable storage rather than creating duplicate
    messages.
13. As an operator, I want successfully recovered idempotency conflicts counted
    separately from unrecovered append failures, so that logs reflect the final
    product outcome.
14. As an operator, I want physical commit count and batch size in every local
    report, so that I can see whether higher throughput came from better
    batching or weakened durability.
15. As an operator, I want commit stage and request latency reported separately,
    so that collection delay, physical fsync, and caller queueing are not mixed
    together.
16. As an operator, I want WAL, flush, compaction, read-amplification, and debt
    evidence, so that background storage work can be correlated with latency
    cliffs.
17. As an operator, I want physical-device utilization evidence when the host
    exposes it, so that low CPU utilization cannot be mistaken for general
    resource headroom.
18. As an operator, I want missing host metrics declared unavailable, so that a
    report never invents zero IOPS or zero utilization.
19. As an operator, I want low-free-space evidence attached to the run, so that
    APFS or filesystem pressure is not silently treated as product performance.
20. As an operator, I want a local run with less than 10% filesystem free space
    marked storage-confounded, so that it cannot become a clean performance
    baseline.
21. As an operator, I want the diagnostic to preserve synchronous commit, so
    that a SENDACK continues to imply crash-safe durable storage.
22. As an operator, I want RAM disks, disabled fsync, and multiple directories
    on one physical SSD clearly labeled diagnostic-only, so that they cannot be
    promoted as durable capacity proof.
23. As an operator, I want snapshot and compaction windows labeled separately,
    so that they can be analyzed as tail-latency amplifiers without being
    assumed to be the original bottleneck.
24. As an operator, I want the existing bounded pprof capture reused only when
    a measured threshold is crossed, so that profiling overhead does not create
    the failure it is intended to diagnose.
25. As an operator, I want the effective commit window and shard count recorded
    in each result, so that two runs with different durability scheduling are
    never compared as equivalent.
26. As a maintainer, I want one commit coordinator per physical message DB by
    default in the local baseline, so that independent coordinators do not
    fragment batches and compete for fsync without evidence.
27. As a maintainer, I want the storage summarizer to consume existing bounded
    Prometheus labels, so that diagnostic evidence does not add UID, Channel,
    or message cardinality.
28. As a maintainer, I want static script-contract tests for the diagnostic
    defaults and artifact schema, so that later refactors cannot silently
    remove the evidence.
29. As a maintainer, I want focused unit tests for outcome-aware log
    classification, so that recovered and shutdown-neutral events remain
    distinguishable from product failures.
30. As a maintainer, I want integration tests for process start, sampling,
    drain, and cleanup under the integration build tag, so that default unit
    tests remain fast.
31. As a maintainer, I want local baseline results excluded from formal verdict
    generation, so that a one-node or shared-disk test cannot claim a paid
    rehearsal or formal Soak pass.
32. As an operator, I want a successful single-node 1,000-SEND/s baseline to
    authorize only the next three-node or cloud diagnostic step, so that it
    does not waive three-replica verification.
33. As an operator, I want the formal Cloud workload to remain 2,000 SEND/s on
    three independent service/data hosts, so that local hardware limitations do
    not redefine the approved goal.
34. As an operator, I want no cloud resource acquisition from these local
    commands, so that discussion and diagnosis cannot incur Alibaba Cloud cost.
35. As an operator, I want the local test to fail closed when another WuKongIM
    workload overlaps the measured interval, so that shared-host interference
    does not produce a clean comparison.
36. As an operator, I want each result to retain source revision, effective
    config, workload identity, host filesystem identity, and artifact checksums,
    so that the comparison is auditable.

## Implementation Decisions

- Reuse the existing native single-node benchmark wrapper as the highest
  throughput-diagnostic seam. It continues to start a single-node cluster and
  a real wkbench worker rather than introducing a storage-only pass criterion.
  Override its legacy 1-Group/16-hash-slot tuning so this baseline uses 12
  logical Slot Raft Groups and 256 hash slots. The online population comes from
  the scenario's online-user count; message `concurrency` remains a separate
  sender scheduling bound and must not be reported as connection count.
- Reuse the existing native three-node chat-lifecycle shakeout as the highest
  lifecycle-correctness seam. The local rate staircase is an operator-invoked
  diagnostic profile and cannot emit a standard verdict.
- Keep the storage microbenchmarks as lower-level explanatory evidence. Their
  throughput is not a substitute for a black-box SEND-to-SENDACK result.
- Use at least 2,500 online sessions for the single-node baseline. The rate
  dimension is offered SEND/s and must always be labeled separately from the
  online-connection dimension.
- Use 250, 500, 750, and 1,000 SEND/s for the single-node staircase. Each step
  receives a 60-second warmup, a five-minute measured interval, and an at-most
  90-second drain.
- Use 100, 150, 250, 400, 500, 750, and 1,000 SEND/s for the three-node local
  staircase. Each search step receives a 60-second warmup, a two-minute
  measured interval, and an at-most 90-second drain. Repeat the highest clean
  step for ten measured minutes before recording the local knee.
- Stop the staircase after the first failed step. Refine only the interval
  between the highest clean step and first failed step, using increments no
  larger than 10% of the interval.
- A clean rate step requires no terminal SEND failure, no correctness failure,
  exact eventual SENDACK accounting after drain, no remaining benchmark work,
  no process exit, no product queue that fails to return to its baseline floor,
  and at least 90% actual/offered measured throughput. Latency is recorded
  against the existing diagnostic threshold but does not overwrite the formal
  hot/cold latency gates.
- A local result below 10% filesystem free space, with overlapping WuKongIM
  processes, or with missing required product metrics is
  `storage_confounded`, `host_confounded`, or `insufficient_evidence`; it is not
  a clean performance pass or product failure.
- Configure a 200 microsecond commit collection window and one coordinator
  shard per physical message DB for both local baselines. Retain the existing
  batch record/byte bounds and synchronous commit unless another independently
  controlled experiment changes one variable and records a separate result.
- Keep one physical message DB per node. Do not combine the three node stores
  to improve local batching because that would invalidate node failure and
  storage ownership semantics.
- Separate APFS volumes or directories on the same physical device remain one
  shared-storage topology. Only independent physical devices or machines may
  be described as independent storage.
- RAM disk and disabled-sync variants may be used only as explicitly named
  falsification probes. They cannot satisfy a durable baseline, rehearsal, or
  formal gate.
- Reuse the existing Prometheus storage families. Add report summarization and
  coverage tests before considering new product metrics.
- Sample host storage through a platform adapter. Linux and macOS may use
  different native commands, but they must emit one normalized, versioned
  sample schema. Missing commands or unsupported counters produce explicit
  availability fields.
- Preserve raw before/periodic/after Prometheus snapshots alongside normalized
  summaries. The normalized report must remain derivable from retained raw
  evidence.
- Record workload, product, and host samples on a shared UTC timeline. The
  report marks warmup, measured, drain, snapshot/compaction overlap, and
  shutdown boundaries.
- The benchmark stop sequence first closes SEND admission, then waits for
  retries, SENDACK correlation, inbound delivery, RECVACK, and product queues
  to converge, captures terminal evidence, and only then closes sessions and
  processes.
- Planned cancellation after the shutdown boundary is neutral evidence. A
  cancellation before that boundary retains its runtime failure
  classification.
- An idempotency conflict that is durably resolved to the same stable message
  identity is a recovered outcome. It increments a bounded counter and may
  retain a limited sample, but does not emit one error record per recovered
  attempt. A mismatch or unresolved conflict remains an error.
- Do not lower the standard Chat Lifecycle Run's 2,000 SEND/s, 10,000 online
  sessions, three independent service hosts, replica counts, 12 logical Slot
  Raft Groups, or 256 hash slots.
- Do not let a local diagnostic command invoke a Provisioning Workflow, Cloud
  Lease Acquire, Deployment Action, or any other billable operation.

## Testing Decisions

- The primary feedback loop is one operator-runnable native command for each
  baseline. It must assert actual/offered throughput, exact SEND/SENDACK
  accounting after drain, terminal failures, correctness failures, queue
  convergence, process continuity, and evidence completeness.
- Static script tests assert that both wrappers record the effective commit
  settings, preserve synchronous commit, expose the rate-step contract, retain
  raw metrics, and generate the normalized storage summary.
- Storage-summary tests use fixed Prometheus fixtures containing histogram
  buckets, counters, gauges, missing series, counter resets, and multiple nodes.
  They assert physical batch count, logical records, batch-size distribution,
  commit latency, queue peak, WAL amplification, flush/compaction deltas, and
  explicit missing-evidence outcomes.
- Host-sampler tests use captured Linux and macOS command output. They verify
  normalized device identity, IOPS, bytes/s, service time when available,
  utilization when available, and unsupported-field handling without starting
  a real sampler.
- Drain tests exercise the real worker control boundary with pending SENDACK,
  retry, receive, and transport work. They prove that stopping generation does
  not immediately close connections and that a bounded timeout preserves the
  remaining-work evidence.
- Outcome-classification tests cover runtime timeout, retry exhaustion,
  recovered idempotency conflict, unresolved conflict, planned shutdown
  cancellation, and unexpected early cancellation.
- Process-start, periodic sampling, pprof capture, real sleeps, listeners, and
  shutdown-loop tests remain integration-tagged.
- Existing message DB benchmarks remain the fast comparative seam for commit
  window, batch size, and shared-device engine count. Performance numbers are
  evidence artifacts and not hard-coded into portable unit tests.
- The native three-node lifecycle E2E remains the correctness seam for first
  SEND creation, full conversation sync, natural unload, reheat, sequence
  continuity, replication, and metric reconciliation. It is not converted into
  a local capacity gate.
- The local single-node 1,000-SEND/s baseline must run before a new paid Chat
  Lifecycle Run. Its pass proves only generator and one-engine path readiness.
- The three-node shared-storage staircase must either record a clean local knee
  or produce a typed confounded/insufficient result before paid testing. It
  need not pass 1,000 SEND/s on one physical disk.
- No local result can satisfy the paid two-hour rehearsal, hour-24/hour-72
  formal checkpoints, aged-data capacity staircase, recovery, cost, or exact
  zero-inventory requirements.

## Out of Scope

- Reducing the formal 2,000 SEND/s workload or its 10,000 online population.
- Replacing three independent formal service/data hosts with a single-node
  cluster.
- Disabling synchronous commit, acknowledging before fsync, or weakening
  replica durability.
- Treating a RAM disk, one SSD split into several volumes, or several
  directories as independent durable storage.
- Automatically deleting local files to create free disk space.
- Automatically buying external NVMe devices or cloud servers.
- Declaring the current Mac SSD defective based on small synchronous-write IOPS.
- General Pebble, filesystem, Raft, Gateway, or Channel runtime optimization
  without evidence from the new baseline.
- Changing the paid Chat Lifecycle Run's budget, CNY 1,350 operational stop,
  Lease lifecycle, Deployment Action, Analysis MCP, or cleanup contracts.
- Publishing a new formal verdict from local diagnostic artifacts.

## Further Notes

- Existing evidence already shows that connection stability and send
  throughput are separate: 2,500 sessions remained connected under a lower
  sustained rate, while 1,000 offered SEND/s accumulated SENDACK work before
  cleanup closed sessions.
- Snapshot writes correlate with some latency spikes but do not precede every
  failure. They remain a ranked tail-latency amplifier rather than the sole
  root cause.
- Successfully recovered idempotency conflicts also occur in a fully drained
  low-rate run. Their current log level is therefore not a reliable terminal
  failure signal.
- Increasing the collection window to 1 or 2 milliseconds did not improve the
  tested device. Four coordinator shards also regressed the relevant local
  matrix. Any future tuning must change one variable at a time and retain the
  before/after evidence.
- The local disk was approximately 95% full during the reproduced failure.
  Results from that condition remain useful for root-cause diagnosis but cannot
  establish a clean performance baseline under this specification.
- This specification extends local qualification only. The approved Chat
  Lifecycle Soak and automated Cloud Simulation designs remain authoritative
  for paid rehearsal, formal Soak, capacity/recovery, budget, and cleanup.
