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

The generic single-node-cluster and three-node 1,000-Channel performance wrappers use
inconsistent commit-coordinator settings. The single-node-cluster wrapper selects a
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
single-node-cluster replica count of one. It holds at least 2,500 WKProto sessions
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
Groups, 256 hash slots, and replica counts of three. A local single-node-cluster pass or
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
4. As an operator, I want the single-node-cluster deployment to retain cluster routing
   and durable storage semantics, so that the diagnostic does not exercise a
   special standalone business path.
5. As an operator, I want the single-node-cluster baseline to retain 12 logical Slot
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
14. As an operator, I want untouched prepared groups excluded from runtime
    metadata-create expectations, so that a short local run cannot report a
    product write failure for a group that never received a message.
15. As an operator, I want physical commit count and batch size in every local
    report, so that I can see whether higher throughput came from better
    batching or weakened durability.
16. As an operator, I want commit stage and request latency reported separately,
    so that collection delay, physical fsync, and caller queueing are not mixed
    together.
17. As an operator, I want WAL, flush, compaction, read-amplification, and debt
    evidence, so that background storage work can be correlated with latency
    cliffs.
18. As an operator, I want physical-device utilization evidence when the host
    exposes it, so that low CPU utilization cannot be mistaken for general
    resource headroom.
19. As an operator, I want missing host metrics declared unavailable, so that a
    report never invents zero IOPS or zero utilization.
20. As an operator, I want low-free-space evidence attached to the run, so that
    APFS or filesystem pressure is not silently treated as product performance.
21. As an operator, I want a local run with less than 10% filesystem free space
    marked storage-confounded, so that it cannot become a clean performance
    baseline.
22. As an operator, I want the diagnostic to preserve synchronous commit, so
    that a SENDACK continues to imply crash-safe durable storage.
23. As an operator, I want RAM disks, disabled fsync, and multiple directories
    on one physical SSD clearly labeled diagnostic-only, so that they cannot be
    promoted as durable capacity proof.
24. As an operator, I want snapshot and compaction windows labeled separately,
    so that they can be analyzed as tail-latency amplifiers without being
    assumed to be the original bottleneck.
25. As an operator, I want the existing bounded pprof capture reused only when
    a measured threshold is crossed, so that profiling overhead does not create
    the failure it is intended to diagnose.
26. As an operator, I want the effective commit window and shard count recorded
    in each result, so that two runs with different durability scheduling are
    never compared as equivalent.
27. As a maintainer, I want one commit coordinator per physical message DB by
    default in the local baseline, so that independent coordinators do not
    fragment batches and compete for fsync without evidence.
28. As a maintainer, I want the storage summarizer to consume existing bounded
    Prometheus labels, so that diagnostic evidence does not add UID, Channel,
    or message cardinality.
29. As a maintainer, I want static script-contract tests for the diagnostic
    defaults and artifact schema, so that later refactors cannot silently
    remove the evidence.
30. As a maintainer, I want focused unit tests for outcome-aware log
    classification, so that recovered and shutdown-neutral events remain
    distinguishable from product failures.
31. As a maintainer, I want integration tests for process start, sampling,
    drain, and cleanup under the integration build tag, so that default unit
    tests remain fast.
32. As a maintainer, I want local baseline results excluded from formal verdict
    generation, so that a single-node-cluster or shared-disk test cannot claim a paid
    rehearsal or formal Soak pass.
33. As an operator, I want a successful single-node-cluster 1,000-SEND/s baseline to
    authorize only the next three-node or cloud diagnostic step, so that it
    does not waive three-replica verification.
34. As an operator, I want the formal Cloud workload to remain 2,000 SEND/s on
    three independent service/data hosts, so that local hardware limitations do
    not redefine the approved goal.
35. As an operator, I want no cloud resource acquisition from these local
    commands, so that discussion and diagnosis cannot incur Alibaba Cloud cost.
36. As an operator, I want the local test to fail closed when another WuKongIM
    workload overlaps the measured interval, so that shared-host interference
    does not produce a clean comparison.
37. As an operator, I want each result to retain source revision, effective
    config, workload identity, host filesystem identity, and artifact checksums,
    so that the comparison is auditable.

## Implementation Decisions

- Reuse the existing native single-node-cluster benchmark wrapper as the highest
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
- Use at least 2,500 online sessions for the single-node-cluster baseline. The rate
  dimension is offered SEND/s and must always be labeled separately from the
  online-connection dimension.
- Use 250, 500, 750, and 1,000 SEND/s for the single-node-cluster staircase. Each step
  receives a 60-second warmup, a five-minute measured interval, and an at-most
  90-second drain.
- Run every single-node-cluster rate step in its own WuKongIM process generation. The
  first generation alone may clean the configured data directory; later
  generations reuse the same durable data so the staircase still observes
  accumulated storage state. A generation starts before that step's warmup and
  stops only after its terminal transport fence, final typed cuts, logs, and
  process evidence are captured. Process continuity is required within each
  step, while an intentional restart between sealed steps is neutral. This
  keeps the terminal Gateway/channelappend/delivery fence one-way and avoids a
  high-risk reopen path after write admission has been closed.
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
- The reviewed group step reconciles recipient delivery over the complete
  warmup-plus-measured logical SENDACK population. Exact aggregate RECV and
  successful RECVACK counts are necessary but not sufficient: a fixed-memory,
  assignment-local witness compares expected, physically received, and
  successfully acknowledged `(message, channel, sender, recipient)` multisets
  through two independently keyed 256-bit projections. A missing recipient
  cannot be hidden by a duplicate to another recipient. Missing or malformed
  witness state is insufficient evidence; complete unequal state is a product
  correctness failure. Raw message and user identities must not enter status
  or retained artifacts.
- Every typed step derives its reviewed online count, group membership,
  traffic, retry, rate, duration, and topology settings from the checksummed
  scenario, plan, coordinator report, and resolved product configuration that
  actually ran. Independently supplied summary flags cannot authorize a step.
  Cross-run evidence, topology-changing environment overrides, extra workers
  or targets, and non-reviewed shard shapes fail closed.
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
  availability fields. A platform command that needs an elapsed sampling
  interval must not run synchronously in the observer request path. The macOS
  adapter returns its last bounded sample immediately and single-flights a
  background refresh; the first request may therefore declare physical-I/O
  fields unavailable without making the complete host sample invalid.
- Preserve raw before/periodic/after Prometheus snapshots alongside normalized
  summaries. The normalized report must remain derivable from retained raw
  evidence. Full-registry periodic snapshots use a 30-second cadence so they do
  not continuously compete with the product's own five-second observer.
- After one staircase step has written its checksums and typed result, discard
  its reproducible binaries, node databases, and empty worker-state directory.
  Retain configuration, reports, logs, raw metrics, normalized summaries, and
  evidence so later diagnosis remains possible without cumulative disk pressure.
- Record workload, product, and host samples on a shared UTC timeline. The
  report marks warmup, measured, drain, snapshot/compaction overlap, and
  shutdown boundaries.
- The benchmark stop sequence first closes SEND admission, then waits for
  retries, SENDACK correlation, inbound delivery, RECVACK, and product queues
  to converge, captures terminal evidence, and only then closes sessions and
  processes.
- A clean terminal cut requires a server-confirmed transport fence; two sampled
  zero-work cuts, TCP half-close, a Ping/Pong exchange, an `AsyncWrite`
  callback, or a zero server-side outbound-buffer gauge is not sufficient.
  The dedicated benchmark target first closes and drains Gateway SEND
  admission, then channelappend admission and accepted post-commit handoff,
  then Online Delivery plans, owner pushes, and pending RECVACK bindings. The
  order is fixed because closing a downstream queue before its upstream
  producer can create new work loses the proof.
- After the target has published one immutable terminal-fence epoch, every
  assignment-owned client sends one bounded benchmark EVENT containing that
  epoch and a random nonce. The owner-local session seals ordinary outbound
  writes before enqueueing the exact EVENT acknowledgement under the same
  write serialization boundary. The client must decode the matching
  epoch/nonce acknowledgement before its final receive-drain cuts. A missing,
  duplicate, stale, wrong-session, or post-seal ordinary write permanently
  fails that epoch. The fence is terminal and cannot reopen admission; normal
  assignment cleanup still owns connection and process teardown.
- Once the server accepts a session's fence EVENT, it rejects any later
  ordinary inbound frame from that session before invoking SEND, delivery, or
  other product use cases. The final outbound acknowledgement alone is not an
  admission fence: discovering an outbound-sealed error only after persisting
  a later SEND would invalidate the terminal proof.
- The terminal product cut includes the Online Delivery plan queue, delivery
  worker in-flight count, and pending RECVACK bindings in addition to Gateway,
  Channel, channelappend, and storage queues. Queue convergence cannot hide a
  cumulative delivery or post-commit failure recorded after the post-warmup
  baseline.
- A measured local step does not let the formal first-attempt-rate evaluator
  terminate the run before the fixed interval. Recovered first attempts remain
  in the evidence, while the post-drain local classifier owns the rate verdict;
  terminal sends and correctness failures remain product failures.
- A validated terminal product or correctness failure during warmup remains a
  product failure even though no qualification baseline exists yet. Its typed
  step result sets `qualification_reached=false`, reports only cumulative
  terminal message counts, leaves measured expected/rate values unset, and
  keeps normalized before/after evidence incomplete. Process continuity and a
  valid final filesystem observation are still required; the ten-percent
  storage-confounded rule retains precedence.
- The qualification cut may contain warmup SENDs awaiting SENDACK. After final
  drain proves that pre-cut population terminal, measured SENDACK accounting
  subtracts the qualification SEND boundary rather than its earlier SENDACK
  counter so late warmup acknowledgements cannot inflate measured throughput.
- Product metric completeness is interval-relative: both boundary cuts must be
  closed, resource/worker/cluster sample counters must advance, and cumulative
  missing-sample counters must not increase. A new cadence gap emits a bounded,
  identity-free diagnostic with its source, timestamps, elapsed gap, configured
  cadence, and cumulative count.
- An observation-source failure records a bounded identity-free JSON event with
  its fixed stage/commit substage, stable error class, failed service/host target
  bitmasks, and remaining deadline. A run that fails before qualification takes
  one non-converged queue snapshot rather than spending the full drain timeout
  comparing against a baseline that cannot exist.
- Planned cancellation after the shutdown boundary is neutral evidence. A
  cancellation before that boundary retains its runtime failure
  classification.
- An idempotency conflict that is durably resolved to the same stable message
  identity is a recovered outcome. It increments a bounded counter and may
  retain a limited sample, but does not emit one error record per recovered
  attempt. A mismatch or unresolved conflict remains an error.
- Group setup prepares business channel and subscriber metadata but does not
  create Channel runtime metadata. The owning worker therefore adds a group to
  the fixed 256-hash-slot metadata-create expectation only after that fixed
  catalog group's first successful SENDACK, using bounded generation-local
  deduplication. Both worker and controller vectors must be monotonic.
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
  utilization when available, unsupported-field handling, and non-blocking
  single-flight refresh without starting a real sampler.
- Drain tests exercise the real worker control boundary with pending SENDACK,
  retry, receive, and transport work. They prove that stopping generation does
  not immediately close connections and that a bounded timeout preserves the
  remaining-work evidence.
- Terminal-fence tests prove the strict Gateway SEND -> channelappend -> Online
  Delivery drain order; exact epoch/capability/assignment/session/nonce
  validation; seal-before-marker ordering; and rejection of any late ordinary
  session write. A transport integration test must block the client reader and
  prove that all preceding RECV frames are decoded before the matching EVENT
  acknowledgement. It must also cover truncated frames, marker enqueue or
  decode failure, timeout, and cleanup without orphaned goroutines. Tests must
  not treat an asynchronous write callback or outbound-buffer snapshot as the
  remote receipt proof.
- Outcome-classification tests cover runtime timeout, retry exhaustion,
  recovered idempotency conflict, unresolved conflict, planned shutdown
  cancellation, and unexpected early cancellation.
- Metadata-create tests cover untouched prepared groups, first successful group
  SEND, duplicate successful SENDs, unsuccessful SENDs, per-Slot deficits,
  vector regression, and checked overflow.
- Process-start, periodic sampling, pprof capture, real sleeps, listeners, and
  shutdown-loop tests remain integration-tagged.
- Existing message DB benchmarks remain the fast comparative seam for commit
  window, batch size, and shared-device engine count. Performance numbers are
  evidence artifacts and not hard-coded into portable unit tests.
- The native three-node lifecycle E2E remains the correctness seam for first
  SEND creation, full conversation sync, natural unload, reheat, sequence
  continuity, replication, and metric reconciliation. It is not converted into
  a local capacity gate.
- The local single-node-cluster 1,000-SEND/s baseline must run before a new paid Chat
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
- The persistent short-run reproduction prepared 500 groups but produced only
  270 group runtime metadata rows. Static catalog accounting incorrectly
  expected every prepared group and classified the untouched remainder as a
  product deficit; successful-first group accounting matches the real runtime
  creation boundary.
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
