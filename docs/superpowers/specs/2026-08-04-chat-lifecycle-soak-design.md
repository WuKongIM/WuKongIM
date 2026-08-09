# Chat Lifecycle Soak Design

**Status:** Approved

**Date:** 2026-08-04

**Primary command:** `wkbench soak chat-lifecycle`

## Goal

Build a black-box, long-running chat workload that continuously introduces new
users and automatically created person channels while a bounded live working set
moves through hot, cold, and reheated states. The workload must determine
whether channel metadata growth, natural Channel runtime eviction and cold
activation, login conversation synchronization, message replication, delivery,
or process resource ownership degrades over 24 and 72 hours.

The first stage is a fixed-pressure stability Soak. A separate second stage
finds the capacity breakpoint on the 72-hour aged dataset.

## Non-goals

- Docker-based deployment or evidence.
- Gateway token-authentication correctness. The current promoted Gateway does
  not prove token persistence or validation.
- WebSocket traffic in the first version.
- Group-channel catalog growth or subscriber add/remove churn during the main
  Soak.
- Deliberate node, network, Slot migration, or disk-pressure faults during the
  main Soak.
- Message retention cleanup during the run.
- Resuming a formal run after a server, worker, or coordinator process exits.

Those dimensions require separate scenarios after the clean Soak is stable.

## Why a New Mode

The existing `wkbench run` planner assumes that the identity and channel pools
are known before execution. `dev-sim` provides long-running development traffic
over fixed user and channel sets. `capacity activate-channels` proves a bounded,
one-time cold activation set. None of them model a continuously growing person
channel catalog, bounded dynamic online population, real login synchronization,
or repeated natural hot-to-cold-to-hot transitions.

The selected design adds a dedicated `wkbench soak chat-lifecycle` mode. It
reuses the target client, WKProto client, worker control, metrics parsing, and
reporting infrastructure without forcing dynamic multi-day semantics into the
static scenario planner or overloading `dev-sim`.

Existing `wkbench run`, `dev-sim`, and capacity commands remain compatible.

## Architecture and Boundaries

The implementation lives under `internal/bench/chatlifecycle`. It remains a
black-box benchmark subsystem: it may use public product HTTP APIs,
benchmark-only read/setup APIs, metrics/debug endpoints, and WKProto, but it
must not import server internals or bypass cluster semantics.

```text
cmd/wkbench soak chat-lifecycle
  -> chat-lifecycle coordinator
       -> target preflight and configuration proof
       -> deterministic three-worker assignment
       -> 24h / 72h checkpoints
       -> aged-data capacity staircase
       -> evidence aggregation and verdict
       |
       +-> worker A
       +-> worker B
       `-> worker C
            -> session scheduler
            -> relationship planner
            -> channel lifecycle scheduler
            -> login conversation synchronizer
            -> person/group traffic engine
            -> asynchronous verifier
            -> bounded evidence recorder
```

The worker runtime retains only the online sessions, active hot set, scheduled
revisit indexes, bounded verifier state, and bounded error evidence. Historical
UIDs, person relationships, channel identities, payload markers, and sampling
choices are derived from `run_id`, `worker_id`, a stable seed, and monotonic
indexes. It must not retain one heap object for every historical user or
channel.

The coordinator runs on a fourth, lightweight host. Three independent load
hosts each carry about one third of the online population. Load hosts and
service nodes must be separate in formal evidence runs. Worker CPU, memory,
socket, or network saturation makes the result `harness_invalid`, not a product
capacity result.

## Native Cluster Topology

The formal target is three native `cmd/wukongim` processes on three independent
machines or VMs. No Docker orchestration is added.

The reviewed topology is:

- 3 cluster nodes;
- 12 logical Slot Raft Groups (`cluster.initial_slot_count = 12`);
- 256 stable hash slots (`cluster.hash_slot_count = 256`);
- Slot replica count 3;
- Channel replica count 3;
- `cluster.max_channels = 50000` on each node;
- an initial 1 TB dedicated SSD data volume on each node;
- Bench, metrics, and debug/pprof endpoints enabled only on a restricted
  network;
- a non-empty Bench API bearer token.

The three Gateway entrypoints and three API entrypoints receive balanced load.
Gateway placement and API placement are selected independently. API requests
are not sent directly to a known UID authority or channel leader; normal
cross-node routing remains part of the test.

The main Soak does not deliberately restart a process, interrupt the network,
or move a Slot. A server process exit is an immediate `product_failure`.
Automatic service restart must be disabled for the formal run.

## Run Tiers and Checkpoints

The workflow has three tiers:

1. A 2-hour shakeout uses independent data directories and validates the
   generator, observation, storage projection, and report shape. Its data is
   discarded and it cannot produce a standard verdict.
2. The formal run starts from empty data. Hour 24 emits a qualification
   checkpoint. A failed checkpoint stops the run.
3. A passing run continues without restart or cleanup for another 48 hours.
   Hour 72 emits the final Soak checkpoint on the same cluster and namespace.

The formal run does not reset metrics, identities, channel indexes, or data
between the 24-hour and 72-hour checkpoints. It should contain about one million
historical person channels at hour 24 and three million at hour 72.

## Online Population and Login Flow

The target online population is held near 10,000 connections. Historical users
continue to accumulate; online connections do not.

Login events are split as follows:

- 80% use a never-before-seen UID;
- 20% select a deterministic UID from the historical offline pool;
- about 250,000 new UIDs are introduced per day, or about 2.9 new UIDs/second;
- total login rate is about 3.6 logins/second;
- the resulting average online session is about 46 minutes.

The empty-dataset bootstrap is a separate pre-clock admission phase. It fills
the first 10,000 online sessions at one fixed global rate of 25 login attempts
per second, partitioned exactly across the three workers. Every attempt still
performs WKProto CONNECT/CONNACK followed by a fresh version-zero full
conversation synchronization; bootstrap does not create synthetic online
state, reuse a cursor, or skip synchronization. Each worker retains 256 bounded
in-flight login slots, so the reviewed bootstrap rate uses about 83 slots per
worker even at the ten-second single-anomaly bound. The deterministic scheduler
must reach 10,000 simultaneously online sessions within 15 minutes while the
normal session-expiry distribution is already active. Unused admission credit
is discarded at each scheduler step: a delayed tick or temporarily full
starting pool must not produce a catch-up burst above the fixed global rate.
Each UTC-aligned one-second bucket gives the three workers immutable 9/8/8
shares. Keeping the extra position on one worker ensures even subsecond skew
across a UTC boundary cannot combine adjacent partitions above 25; a worker
that misses a whole bucket discards its positions. The deterministic
three-worker churn model reaches 10,000 simultaneous sessions in 421 seconds.

Once the target is reached, the scheduler discards unused bootstrap credit and
the bootstrap bucket and unequal attempt phases, then restarts the steady login
plan ordinal at zero and switches to the ordinary 250,000-new-UID/day, 80/20
new/returning stream below. Bootstrap users and their real product data remain;
only the measured steady-state rate begins at the workload clock. A worker
using coordinator grants remains in all-new bootstrap mode after reaching its
local share until the first grant, which the coordinator sends only after all
three workers have reported their synchronized shares ready.

Session duration has a deterministic, seeded distribution:

| Share | Duration |
| --- | --- |
| 25% | 5-15 minutes |
| 50% | 15-45 minutes |
| 20% | 45-120 minutes |
| 5% | 2-6 hours |

Every login performs a real client startup flow:

```text
WKProto CONNECT / CONNACK
  -> POST /conversation/list
       completed_coverage = 0
       cursor = empty on the first page
       limit = 200 candidates per page
       follow next_cursor until done = true
  -> POST /conversation/retry for bounded unresolved keys
  -> begin realtime send and receive work
```

The benchmark intentionally stores no completed directory coverage, directory
cursor, or per-channel message cursor between sessions. Every login synchronizes
all conversations from zero coverage and validates the server-provided last
message projection. The generator must keep each virtual user's total
conversation count below 500. Reaching 500 is a scenario-invalidating failure
rather than silent truncation.

## Person Relationship Graph

New users form relationships inside deterministic arrival cohorts. Every new
user owns 3-5 unique, undirected person edges generated with a forward-only
cohort rule, so symmetric duplicates cannot reduce the declared channel budget.
The standard distribution averages four owned edges per new UID: about one
million unique channels for 250,000 new UIDs per day. Receiving edges give a
typical user about 6-10 person conversations and avoid a small set of artificial
celebrity recipients.

Both endpoints are online for the initial conversation burst. Later they may
disconnect independently. Returning users select real edges from this graph,
not newly invented historical channels.

Person channels must never be pre-created through `/bench/v1/channels`. Their
metadata is created only by the first real WKProto SEND. Finding an expected new
person channel before its first SEND is a namespace collision or dirty-data
failure.

## Channel Population and Lifecycle

The standard profile creates about one million unique person channels per day.
Every new relationship sends an initial 2-8-message burst, averaging about five
messages over 5-30 seconds. Seventy percent of person conversations are
bidirectional and alternate senders; 30% are one-way.

New person channels receive one deterministic lifecycle class:

| Share | Lifecycle |
| --- | --- |
| 60% | Initial burst, then permanently cold for the remainder of the run |
| 25% | Initial burst, natural eviction, then a revisit after 10-60 minutes |
| 10% | Rotating hot channel for 20-40 minutes, then natural cooling |
| 5% | Longer-lived hot channel for 2-4 hours, then natural cooling |

At the reviewed arrival rate, the 10% and 5% classes produce about 8,000
simultaneously hot person channels. The runtime also temporarily contains
one-shot and revisit channels touched within the natural five-minute idle
eviction window.

Returning users synchronize first, then choose 1-2 old person conversations.
Eighty percent of choices prefer the preceding 24 hours; 20% choose older
history. A revisit sends 2-5 messages. It counts as cold activation evidence
only when all-node runtime probes first prove that the runtime was unloaded.

Natural runtime eviction is mandatory. The standard workload must not call the
Bench eviction endpoint to manufacture a cold state.

## Group Background Traffic

Group channels are a fixed auxiliary workload and do not contribute to the
historical channel growth target. Preparation creates 2,000 channels:

| Count | Members |
| --- | --- |
| 1,600 | 5-20 |
| 300 | 100-500 |
| 99 | 1,000-10,000 |
| 1 | 100,000 |

Group membership is fixed for the full Soak. Login and disconnect activity
changes which members are online, but the main scenario does not call subscriber
add/remove APIs.

The primary group stream contributes 200 SEND/s. Eighty percent of group sends
target small groups, 15% target medium groups, and 5% target the 1,000-10,000
member groups. The 100,000-member group receives one additional correctness
probe per minute. That probe is reported separately and is excluded from the
primary 2,000 SEND/s capacity denominator.

## Traffic Shape and Payloads

The primary offered load averages 2,000 SEND/s:

- 1,800 person SEND/s;
- 200 group SEND/s;
- one separate 100,000-member group canary SEND per minute.

A global token bucket allows up to two seconds of credit, so short bursts may
reach 4,000 scheduled messages while the long-term primary rate remains 2,000
SEND/s. Per-channel arrival times and burst membership use a fixed seed rather
than perfectly even timers.

Payload sizes use a deterministic weighted distribution:

| Share | Payload size |
| --- | --- |
| 70% | 256 B |
| 25% | 1 KiB |
| 4% | 4 KiB |
| 1% | 16 KiB |

Payloads contain a compact self-verifying marker derived from run, worker,
channel, and message indexes. Reports count both messages and payload bytes.

All first-send, revisit, rotating-hot, long-hot, and fixed-group streams draw
from the same global rate budget except the separately reported 100,000-member
canary.

## Retry and Idempotency

Every logical message has one stable `client_msg_no`. Retriable timeout or
temporary failures reuse that identity for up to three retries with delays of
100 ms, 500 ms, and 2 seconds plus deterministic jitter. A non-retriable
ReasonCode fails immediately.

Reports separate:

- first-attempt failures;
- retry attempts;
- retry successes;
- final failures;
- sampled duplicate-persistence checks.

A retry success must still correspond to one durable message. Generating a new
message identity on retry is forbidden.

## Correctness Verification

Verification is asynchronous so it does not serialize the 2,000 SEND/s path:

1. Every SEND must match exactly one SENDACK waiter.
2. Every online client drains every received RECV, validates the embedded
   marker, sender, and channel, checks `message_seq` monotonicity per channel,
   and sends RECVACK.
3. One percent of messages receive an exact SEND -> SENDACK -> RECV correlation
   check.
4. Sampled channel-tail reads verify payload ownership, absence of duplicate
   persistence, and continuous sequence after a cold reactivation.
5. Full conversation sync responses are validated for completion, channel
   uniqueness, bounds, last-message identity, and payload decoding.

Any confirmed loss, duplicate durable message, payload corruption, or sequence
regression is an immediate product failure. Correctness errors have no tolerance
ratio.

## Hot-Cold-Reheat Proof

Every ten minutes, the coordinator selects 1,200 deterministic person channels
in total, with coverage weighted across the 12 logical Slot Raft Groups. The
evidence cycle is:

```text
record loaded role and committed sequence
  -> stop traffic for the selected channels
  -> wait beyond the natural five-minute idle threshold
  -> probe all three nodes until every selected runtime is absent
  -> SEND on the selected channel
  -> prove leader/follower runtime loading
  -> prove the new message sequence follows the recorded sequence
```

The report records transition success, timeout, role disagreement, unexpected
continued loading, reactivation latency, and sequence proof. Aggregate runtime
gauges show working-set trends; per-channel samples prove that actual lifecycle
transitions occurred.

## Durable Channel Creation Evidence

The current Bench snapshot counts setup mutations and cannot prove person
channels created by SEND. Add one bounded product metric:

```text
wukongim_channelv2_meta_created_total{slot_id,result}
```

`slot_id` is one of the 12 logical Slot IDs. `result` uses a closed outcome set
that distinguishes a new durable create, an already-existing create race, and
an error. The observation is emitted once at the authoritative metadata-create
decision, not once per replica apply. No UID or channel label is allowed.

The coordinator compares successful unique first SENDs with successful durable
creates. A cold reactivation must not increase the successful-create total.
Creation distribution is compared with the expected share derived from each
logical Slot's assigned hash slots, not assumed to be exactly one twelfth.

## Observation and Evidence

The restricted test network exposes Bench APIs, `/metrics`, debug configuration,
runtime probes, and pprof. Credentials, concrete UIDs, raw payloads, and
unbounded error text must not enter reports.

Collection cadence is:

- every 5 seconds: health, readiness, and Prometheus;
- every 30 seconds: process resources, Channel runtime, reactors, and worker
  queues;
- every 10 minutes: the 1,200-channel lifecycle proof;
- hours 2, 24, 48, and 72: heap, goroutine, effective configuration, and
  checkpoint evidence;
- on threshold breach: bounded CPU, heap, allocs, goroutine, log-window, trace,
  and runtime captures.

An evidence recorder writes bounded structured samples continuously. It must
prefer counts, hashes, indexes, error classes, and bounded tails. Losing the
evidence required to support a verdict yields `insufficient_evidence`.

## Acceptance Gates

### Correctness and Error Gates

- Final SEND failures after retry: exactly 0.
- Message loss, duplicate persistence, payload corruption, or sequence
  regression: exactly 0.
- Whole-run first-attempt SEND failure rate: below 0.01%.
- Any one-minute first-attempt failure rate: at most 0.1%.
- Any runtime activation rejection caused by `max_channels`: failure.
- Successful durable person-channel creates must reconcile with unique
  successful first SENDs.

### Latency Gates

| Path | p99 | p99.9 |
| --- | --- | --- |
| Loaded hot-channel SEND -> SENDACK | 200 ms | 1 s |
| New or unloaded cold-channel activation | 2 s | 5 s |
| Login full conversation sync | 1 s | 3 s |

Any operation beyond ten seconds is a captured long-tail anomaly. A latency
gate exceeded for five consecutive minutes fails the run; whole-run aggregation
must not hide a sustained bad window.

### Cluster Health Gates

- Poll health, readiness, and cluster state every five seconds.
- A node not ready for 30 consecutive seconds fails the run.
- Every one of the 12 logical Slots must retain an elected leader and three
  valid replicas.
- A hot-channel ISR anomaly, missing leader, or follower lag persisting for
  30 seconds fails the run.
- Channel leader placement deviating more than 20% from its expected node share
  for ten consecutive minutes is a placement failure.

### Resource Trend Gates

The first two hours form the resource warmup baseline. Afterward:

- loaded Channel runtime count must track the bounded hot set plus channels
  touched inside the eviction window; it must not track historical channel
  count;
- forced-GC live heap growth must stay within 5% in every rolling six-hour
  window;
- goroutine baseline growth must stay within 5% at hour 24;
- worker queues, pending appends, pending metadata, checkpoints, and replication
  inflight must repeatedly return to a stable floor;
- RSS, Pebble cache, and file mappings are analyzed separately and are not
  automatically called Go heap leaks.

Crossing a resource gate triggers evidence capture before the terminal verdict
when the process remains responsive.

### Disk Gate

The first formal deployment uses 1 TB per node. The run does not enable message
physical retention cleanup or modify load to fit the disk. When a node's data
volume reaches 5% free space, the coordinator stops safely and classifies the
result as `infrastructure_failure`. The operator may expand storage and restart
the formal run; a partial result cannot be promoted to a 72-hour pass.

## Failure Classification

- `product_failure`: server crash, correctness violation, terminal message
  failure, sustained latency/health failure, runtime leak, failed natural
  cooling, or failure to recover after overload.
- `infrastructure_failure`: exhausted disk or another declared external host
  capacity limit.
- `harness_invalid`: worker/coordinator failure, load-host saturation, offered
  load under-delivery, or a generator invariant violation.
- `insufficient_evidence`: missing observations required to support another
  verdict.
- `operator_modified`: reviewed workload or target settings changed during the
  run.

Worker or coordinator failure invalidates the formal run. The system does not
splice evidence across a resumed generator. A server crash stops the run and is
not hidden by a process supervisor restart.

## Aged-Data Capacity Stage

After the hour-72 Soak checkpoint passes, freeze its report and keep the aged
dataset in place. The capacity stage changes only the primary offered SEND rate;
online users, login sync, channel growth, lifecycle mix, group traffic ratio,
payload distribution, and verification remain enabled.

Capacity steps are:

1. start at 2,000 SEND/s;
2. increase each step by 25%;
3. run each step for 30 minutes;
4. treat the first ten minutes as stabilization and the next twenty as the
   measured window;
5. after the first failing step, refine between the last pass and first failure
   to 10% precision.

The breakpoint is the highest step that satisfies every correctness, error,
latency, queue, resource, and health gate. The overload result does not rewrite
the already frozen Soak verdict.

After the first failed capacity step, stop increasing load and return to 2,000
SEND/s for 30 minutes without restart or cleanup. Readiness, latency, errors,
queues, and lifecycle activity must return to their Soak bounds. Failure to
recover is a separate product defect. A clean-data capacity run is optional and
serves only as a comparison.

## Configuration and Command Surface

The command accepts the existing target and worker files plus one new strict
configuration document:

```bash
wkbench soak chat-lifecycle \
  --target target.yaml \
  --workers workers.yaml \
  --config chat-lifecycle.yaml
```

The configuration version is `wkbench/chat-lifecycle/v1`. A reviewed standard
profile declares the values in this design. Scale-reduced shakeouts may override
counts and durations, but cannot emit a standard stability verdict. Unknown
keys, invalid ratios, unbounded capacities, non-three-worker formal profiles,
or settings that cannot achieve the declared channel-growth and login rates
must fail static validation.

The worker control server receives a dedicated chat-lifecycle assignment and
lifecycle. It must not change existing assignment behavior. Public package
interfaces should expose one narrow configuration/run boundary; schedulers,
state machines, and evidence details remain internal to the module.

Provide native-process example configuration, a local three-process shakeout
helper, and a formal multi-host runbook. Do not add a Docker entrypoint.

## Testing Strategy

Default unit tests use fake clocks and deterministic random sources. They cover:

- strict config validation and standard-profile classification;
- identity and relationship disjointness across workers;
- one-million-per-day channel-rate math without allocating one million objects;
- login ratios, session-duration buckets, and constant online population;
- relationship degree bounds and conversation-sync limit protection;
- lifecycle state transitions and revisit selection;
- global rate limiting, burst capacity, and payload distribution;
- bidirectional sender selection;
- stable retry identity and retry accounting;
- asynchronous receive and sequence verification;
- bounded evidence retention and verdict precedence;
- checkpoint and capacity-staircase state machines.

Product-side focused tests cover the new channel creation metric and prove that
it increments once for a successful durable create, does not increment for an
ordinary cold reactivation, and uses bounded labels. An `internal/app` wiring
test proves the observer is connected through the composition root.

Add `test/e2e/message/chat_lifecycle` with its own `AGENTS.md` and one primary
test file. The explicit `e2e` test uses real three-node `cmd/wukongim`
processes, real WKProto sessions, public HTTP, and the natural five-minute idle
eviction. It reduces users, channels, and rates but must prove first-send create,
login full sync, natural unload, reheat, sequence continuity, and metric
reconciliation. It is a correctness regression, not a local capacity test.

The 24/72-hour runs remain operator-invoked evidence and do not enter CI.
Scripts that build binaries, start processes, wait for readiness, or use real
deadlines follow the repository integration/e2e tagging rules.

Update the applicable `internal/bench/FLOW.md`, Channel/cluster flow docs,
`test/e2e/AGENTS.md`, `test/e2e/message/AGENTS.md`, example configurations,
the native runbook, and `docs/development/PROJECT_KNOWLEDGE.md` when the stable
command and metric contracts land.

## Completion Criteria

The feature is complete when:

1. a scale-reduced native three-node E2E proves the complete lifecycle path;
2. the standard config passes strict validation and deterministically plans
   three workers without historical-cardinality memory growth;
3. report artifacts distinguish product, infrastructure, harness, evidence,
   and operator-modification outcomes;
4. the command can produce auditable hour-24 and hour-72 checkpoints;
5. the aged-data capacity staircase and post-overload recovery phase are
   executable without restarting the cluster;
6. existing wkbench commands and default unit tests remain compatible.
