# internal/bench Flow

`internal/bench` contains the reusable implementation behind `cmd/wkbench`. It is organized as a black-box benchmark runtime: configuration and planning are local, while target mutation and traffic use HTTP bench APIs and WKProto gateway clients. It must not import WuKongIM server internals.
Shared wkbench schema, plan, report, and bench/v1 API DTOs live in `pkg/bench/model` so the promoted server entrypoint can expose benchmark-only target APIs without importing legacy `internal/bench` packages.

## Package Roles

- `config`: strict YAML loading, environment expansion, and early static validation.
- `planner`: deterministic worker sharding for person profiles, group profiles, large groups, member ranges, traffic partitions, and channel owners.
- `coordinator`: top-level run orchestration, preflight, worker assignment, phase polling, failure classification, and report collection.
- `worker`: HTTP control server plus the default workload runner used by worker processes.
- `devsim`: long-running development simulator supervisor used by `wkbench dev-sim`; it derives compact simulator config into normal wkbench target/scenario/plan inputs and runs an in-process worker.
- `capacity`: maximum stable ingress QPS search used by `wkbench capacity send` and `wkbench capacity hot-channel`; it discovers target gateway addresses, generates attempt scenarios, runs a temporary local worker, and writes capacity summaries.
- `messageevent`: fixed-shape `/message/event` stream pressure runner used by `wkbench capacity message-event`; it creates channels through public `/channel`, sends stream base messages through `/message/send`, sends cache-only `stream.delta` updates, completes each stream with `stream.finish`, captures `/metrics` before/after snapshots, and writes message event reports.
- `chatlifecycle`: deterministic three-worker chat lifecycle planning, authenticated protocol-v2 worker control, coordinator-owned globally apportioned grant delivery, bounded verification/evidence, group setup, preflight, service observation, and an independent bounded natural hot-cold-reheat proof module. Coordinator assignment, start, grant, status, qualification, and final barriers use fixed concurrent worker rounds; observation spans readiness and the measured run. Empty-dataset bootstrap uses one fixed global 25-login/second stream until all 10,000 users are simultaneously online; each login still completes real WKProto CONNECT/CONNACK and a fresh version-zero full conversation sync, and each worker retains 256 concurrent starting slots. Missed or unused bootstrap credit is discarded rather than caught up in a burst. Coordinator-controlled workers remain all-new after reaching their local shares until the first global grant clears bootstrap credit and fractional remainder and begins the unchanged 250,000-new-user/day 80/20 steady stream. The first complete grant remains a pre-clock control round and uses the ordinary bounded control deadline; after it crosses all workers and fixes the measured-run start, each later fail-closed sequenced grant vector is capped to the one-second cadence. Assignments disable worker-local primary token release, and that one vector drives all three workers every logical second without resumable state. The lifecycle proof leases only current revisit timers from a fixed 12-by-100 primary owner index backed by per-Slot standby heaps whose aggregate index is bounded by Engine WorkCapacity; primary removal promotes the best valid same-Slot standby without expanding the at-most-1,200 lease scan. Each lease carries an exact generation-local timer token and post-activity version. The proof asynchronously probes three nodes without eviction, transiently merges every bounded batch before one atomic state transition, tolerates bounded staggered replica cooling, admits only that exact existing scheduled real SEND through a fenced worker call strictly before its deterministic due instant, proves sequence continuity from post-reheat probes, and reconciles fixed per-Slot metadata-create deltas against physical-hash-slot expected-unique growth. Product-transition failures use fixed identity-free reason counters whose saturating total equals the aggregate product-failure count even when an atomic observation batch rolls back. Later activity invalidates the lease and records harness evidence instead of silently approving or dropping a different timer. Its mapping boundary accepts a copied live 256-entry assignment, while current worker composition indexes against and requires the validated continuous no-migration profile. Production composition integrates lifecycle, resource, metadata-create, qualification/final report, and capacity evidence through one coordinator hook.
- `workload`: reusable connection, person traffic, and group traffic executors.
- `target`: black-box HTTP client for target health, readiness, bench capabilities, capacity target, setup snapshot, presence snapshot, conversation sync, token, channel, and subscriber APIs. Setup mutation calls use the first healthy target API address and fall back on failure; targets such as `cmd/wukongim` route real metadata writes through their cluster runtime.
- `wkproto`: benchmark WKProto client implementation.
- `metrics`: worker-local counters, histograms, bounded error samples, aggregation helpers, and low-cardinality Prometheus attribution parsing.
- `report`: deterministic report construction and report directory writing.
- `localbaseline`: strict bounded parsing, raw-evidence classification, and the
  fail-closed four-step authorization gate for the native single-node cluster
  diagnostic. It does not start workloads or infer missing evidence.

Reviewed stability scenarios separate the full generated identity pool from
the connected online prefix. Group preparation writes the requested total
subscriber count from both pools, while execution connects only the planned
online identities. The planner validates reviewed ingress QPS exactly, checks
estimated online fanout within the declared tolerance, and rejects any profile
that cannot emit at least one message per required active-channel window.

During measured scheduled churn, the worker runs traffic in bounded windows.
At each boundary it reconnects the same-UID share, replaces the identity-swap
share from deterministic offline lanes, refreshes bench tokens, adds replacement
UIDs to every affected group, removes the replaced UIDs, and builds the next
person/group workload generation. A successful generation swap proves and
joins the old receive drains, then atomically archives the completed generation and installs
the replacement; a failed rebuild leaves the old generation active and
unarchived. Archived workload counters and histograms accumulate across
generations, gauges retain their temporal maximum, and the runner keeps one
bounded cumulative snapshot. The add-before-remove order prevents a replacement
sender from temporarily losing membership, while removal keeps long-running
group sizes bounded. `OnlineIdentityIndexes` is worker-local runtime mapping state;
an empty mapping preserves the initial plan. Each measured window gets a unique
message identity namespace while report metrics normalize it back to the stable
`run` phase. Churn never requests history sync.

Group `sender_pick: weighted_80_20` emits four of each five deterministic
messages from the first 20% of online members and the fifth from the remaining
80%. This distribution is per channel and does not change channel rate.

High-concurrency group `sender_pick: round_robin` keeps each message's stable
channel, index, identity, due instant, and preferred member, but it does not
bind the message to a busy member while waiting in the admission queue. At each
due cut, the group-specific exact-window owner matches pending intents to idle
members in cyclic member order. The due watermark and one pending/admitted
ledger per channel retain `O(channel_count)` state rather than one object per
logical message; a 64-channel augmenting frontier avoids a greedy choice
stranding another simultaneously due intent. All round-robin group workloads
built for one assignment generation share 256-sharded sender credits. A credit
is retained through the complete SEND/SENDACK/retry/verification operation, so
concurrent traffic streams never admit overlapping operations for the same
simulated client, and its release directly wakes blocked windows. With no
contention the physical sender sequence remains the historical
`message_index % online_members` order. At the hard admission deadline, pending
and unstarted intents enter the existing exact drop accounting and therefore
produce a rate verdict; they are never sent during cooldown and never become a
worker hook failure. `first_online`, `weighted_80_20`, person traffic, and the
generic keyed scheduler retain their existing semantics.

The WKProto bench client is a thin adapter over `pkg/client`. The shared client
owns CONNECT/CONNACK, optional payload encryption, socket decoding, SENDACK
matching, RECV decryption, and the single writer/reader pumps. The bench adapter
keeps the existing workload-facing `Send` / `ReadFrame` contract by converting
`pkg/client` SEND futures back into local SENDACK frames and forwarding RECV
frames through independent bounded RECV, SENDACK, and error queues. Asynchronous
SEND errors preserve both `ClientSeq` and `ClientMsgNo`, so overlapping retries
can affect only their exact attempt. A full RECV
queue backpressures the shared reader and then the socket; neither layer evicts
receive evidence. `ReadFrame` acquires a one-reader arbitration permit through
the caller context and session stop signal, so a reader waiting behind another
reader remains cancelable. The permit preserves one shared preference state:
non-terminal errors precede SENDACKs, while at most four combined priority
results precede an already queued RECV. Before `SendAsync` admission, each SEND
also acquires one publication permit. `TrySend` instead returns
`client.ErrSendQueueFull` unless that publication permit and the shared
client's admission lock, writer queue, and inflight slot are all immediately
available; it never adds a waiter to the deterministic chat-lifecycle owner
loop. Its per-session cumulative rejection count is included in the numeric
queue snapshot. The `frame_buffer_size` permits bound both
publisher goroutines and pending SENDACKs; a caller waiting for a permit remains
in its own workload goroutine and observes context cancellation or session
stop. A future releases its permit only after its ACK/error enters a fixed queue
or stop aborts publication. Published frames and SEND results drain before the
original remote terminal error returns once. The numeric queue snapshot exposes
the inner and adapter depths/capacities plus publication current, capacity,
monotonic peak, and currently blocked Send callers. Both receive queue
boundaries use idempotent ownership leases. `pkg/client` retains ownership
after a RECV dequeue until the adapter accepts it; the adapter retains every
RECV, SENDACK, or error result until the matching reader registers its
in-flight ownership. Receive snapshots sample upstream queues before downstream
matching state, so work cannot disappear between a queue decrement and
downstream registration. Worker clients are created
from an optional worker-local `client`
profile. Its send queue, maximum inflight SEND count, socket read buffer, and
frame buffer capacities flow from the selected worker assignment through the
default connection manager factory. `frame_buffer_size` independently bounds
the shared client's inbound RECV queue and each of the adapter RECV, SENDACK,
and error queues, for four fixed-size inbound queues per session; no hidden
slice grows with backlog. Omitting the complete profile retains the tooling
defaults. Worker
clients may also receive an optional worker-local `tcp_source` pool. The pool
contains explicit, unique, non-unspecified IPv4 addresses plus an inclusive
port range. The planner requires its finite capacity to cover the worker's
final identity range. One shared connection-manager dialer then consumes
candidates monotonically in IP-fastest order, never reusing a candidate during
the assignment. Only a local `EADDRINUSE` conflict advances to the next
candidate; local address/permission failures and pool exhaustion remain typed
worker failures, while remote target errors retain their normal target
classification. Omitting `tcp_source` preserves ordinary `net.Dialer`
behavior. Worker clients are additionally wrapped by a matching reader that buffers unmatched frames for
foreground waiters and applies the recv-ack policy selected by the scenario.
Connection pacing measures the interval between attempt start times, so time
spent performing the previous WKProto handshake is deducted from the next
wait. An unconditional post-handshake sleep would accumulate handshake latency
across large online pools and make the coordinator's deterministic connect
schedule underestimate the real phase duration.
For chat-lifecycle correctness projection, a first-attempt rejection at the
non-waiting local SEND admission boundary is removed from the target-owned
first-attempt failure numerator and remains visible in the cumulative worker
transport-rejection evidence. Checked attribution prevents local simulator
pressure from becoming a false product failure without hiding it.
Scheduled traffic uses per-key pending queues plus a ready-key queue when a
workload supplies a serialization key. That keeps one busy client or channel
from forcing a linear scan across a large pending list. Person traffic and the
fixed-sender group policies supply the sender UID as that key. High-concurrency
group round-robin traffic instead uses the group exact-window owner described
above, which chooses an idle member only when the logical message is ready to
enter SEND admission. Both paths preserve one in-flight `Send -> Sendack`
operation per selected simulated client. Wrapped clients allocate
connection-local monotonic ClientSeq values so each waiter matches by ClientSeq
plus ClientMsgNo.

Adapter `Close` publishes the session stop signal before closing the shared
client. This releases blocked publication admission, queue publishers, and
`ReadFrame` calls. It does not itself promise to join the shared client's
internal reader/writer loops; worker teardown and receive-drain joining remain
the explicit lifecycle boundary completed in Phase 3 task 4.

## Chat Lifecycle Production Flow

The production commands are `wkbench soak chat-lifecycle` and `wkbench
capacity chat-lifecycle`. They compose exactly three authenticated workers,
three service/API observations, three host-filesystem observations, real
WKProto clients, and real zero-coverage `/conversation/list` page walks with
bounded `/conversation/retry` hydration. The
coordinator writes a non-terminal qualification cut while traffic continues,
then stops and joins all workers before the final metadata-create equality
check and atomic final report. Capacity terminal failures also join continuous
observation and run the same stop/finalize hook; they cannot return early with
moving worker or lifecycle evidence. Preflight results retain only their closed
reason code so failures such as `disk_free` remain distinguishable when no final
report can be written.

`wkbench host-metrics` is the small native helper used by local shakeouts and
the four-host cloud deployment to serve one exact filesystem's
`node_filesystem_size_bytes` and `node_filesystem_avail_bytes` series plus
`/healthz`. A formal run may instead use an existing node exporter with the
same exact device/mountpoint contract.

When `--physical-io` is enabled, `wkbench host-metrics` also publishes the
versioned `wkbench_host_block_io_*` contract for the physical device backing
the selected path. Linux derives read/write IOPS, bytes, utilization, and
service time from monotonic sysfs counters. macOS resolves the APFS physical
store and publishes the total IOPS and bytes exposed by `iostat`; unsupported
utilization, service-time, or read/write splits remain explicit availability
zeros and have no fabricated value series. Unsupported platforms or ambiguous
multi-device mappings use the same unavailable contract.

The native three-process local lifecycle wrapper also feeds each host-metrics
endpoint a closed, atomically replaced process textfile. It samples the exact
service, worker, coordinator, and host-metrics PIDs as CPU jiffies and RSS;
the local-step result fails closed when a host/process round or worker-queue
cut is missing.

`wkbench report local-chat-lifecycle-step` consumes one post-warmup
qualification report, one drained terminal report, complete normalized
storage and host-I/O evidence, a normalized post-drain service queue cut, and
a closed process-continuity cut. The local wrapper samples that queue cut after
the coordinator's bounded worker drain/stop completes and waits for the
cluster-wide queue and inflight totals to return below their sealed
post-warmup totals. Cluster aggregation permits ordinary ownership work to
migrate between service nodes without hiding any work from the fixed total.
The terminal
report's service-resource projection is the last active observation before
worker shutdown; it must not be reused as post-drain queue evidence. The
command emits only the
non-formal local outcomes `clean`, `rate_failed`, `product_failure`,
`storage_confounded`, `host_confounded`, or `insufficient_evidence`. It cannot
emit or satisfy a rehearsal, formal Soak, or capacity verdict.

An explicit worker stop establishes a planned-shutdown fence only after every
admitted SEND and sampled correlation has drained. Scheduled activities that
never crossed that fence are retained as `planned_cancellations` and remain
neutral; runtime expiry, a failed drain, or an unexpected stop still records
`offered_underdelivery` and harness-invalid evidence.

A complete per-node storage cut must also contain nonzero measured physical
commits, logical requests/records/bytes, request samples, and WAL input/output;
the request result and lane partitions must each reconcile to the total request
count. Present-but-static or unrecognized-label metrics fail closed.

The normalized storage row deliberately joins two bounded label domains:
commit-coordinator families use `store="message"`, while physical Pebble/WAL,
flush, and compaction families use `store="channel_log"`. Treating them as one
Prometheus store label makes real evidence permanently incomplete.

## Coordinator Run Flow

```text
cmd/wkbench run
  -> config.LoadTarget / LoadWorkerSet / LoadScenario
  -> config.ValidateStaticConfig / ValidateTargetScenario
  -> planner.Build
  -> coordinator.Preflight.Check
       -> when scenario.run.external_terminal_cut=true, require exactly one
          effective Bench API address and exactly one worker before any network probe
       -> target /healthz, /readyz, /bench/v1/capabilities
       -> when scenario.run.external_terminal_cut=true, require the target's
          terminal_fence_prepare capability before assigning any worker
       -> worker /v1/info
       -> gateway checker
  -> coordinator.assignWorkers
       -> copy only each selected worker's client capacity profile and TCP source pool into its assignment
       -> bind a required immutable assignment_id generation within the run
       -> omit worker control credentials from the assignment payload
       -> POST worker /v1/assign
  -> phases: prepare -> connect -> warmup -> run -> cooldown
       -> POST worker /v1/phase/<phase>
       -> poll worker /v1/status until completed_phase catches up
  -> POST /v1/stop to every assigned worker before terminal report collection
       -> synchronously fence new phase and owner-channel preparation admission
       -> cancel and join the active worker lifecycle task
       -> release assignment-scoped connections and background receive drains
  -> collect worker metrics and reports
  -> collect target setup snapshots and supported presence snapshots
  -> report.Build and report.WriteDir
```

Coordinator terminal statuses map directly to CLI exit codes:

- `completed` -> `0`
- `config_failed` -> `1`
- `preflight_failed` -> `2`
- `hard_limit_failed` -> `3`
- `worker_failed` -> `4`
- `target_unavailable` -> `5`
- `canceled` or `internal_failed` -> `6`

Reports additionally carry a `stability_verdict`: `passed`,
`product_failure`, `infrastructure_failure`, `harness_invalid`,
`operator_modified`, or `insufficient_evidence`. Diagnostic durations cannot
produce a standard passed verdict. Worker/harness evidence and external Cloud
View purity classification take precedence over product-limit inference.
The bounded final summary also records the successful send acknowledgement
count from the measured `run` phase so storage calibration can use the exact
workload denominator even when a run terminates early.
It derives actual ingress QPS from that count and the configured measured-run
duration. A reviewed objective with `ingress_qps` is a hard lower-bound gate at
`target * (1 - tolerance_ratio)`, so a stable but under-delivering run cannot be
reported as passed.
It also records connection attempt, success, and error counts so a failed
connect phase can be diagnosed without reconstructing integers from a rounded
error rate.
The retained report deep-copies effective target and worker configuration and
removes Bench API and worker-control credentials. Reviewed local single-node
closures reject any report that retains those credentials, require canonical
numeric loopback API, metrics, Gateway, and worker endpoints, and bind the
sealed target to the effective single-node cluster listeners during completion
verification; a foreign endpoint cannot borrow local process evidence.
`report.WriteDir` additionally writes `diagnostic-summary.json`, a bounded,
redacted machine contract with actual coordinator phase windows and structured
worker failures. Typed person/group session failures retain an optional
low-cardinality `operation` such as `group_sendack`; unknown values are omitted,
and raw UIDs, URLs, paths, and error text never enter this projection.
The same retention rule applies to `report.json`, worker report payloads,
worker-metrics JSONL, and bounded error-sample JSONL. Error samples contain
only a closed operation name and a closed reason such as `timeout` or
`operation_failed`; legacy or caller-supplied free text is normalized at
aggregation and again at report write. Metric snapshots keep only series whose
labels belong to the fixed low-cardinality registry contract; legacy series
with UID, channel, or message labels are omitted from worker and retained
report JSON. Free-form coordinator text is not retained because structured
worker failures already carry phase, reason, and safe operation evidence.
`summary.md` remains human-readable and is not the analysis machine contract.

Explicit TCP source pool errors are worker-local configuration or capacity
failures and therefore resolve to `worker_failed`, never
`target_unavailable`.

Terminal stop applies to successful, fail-fast, and non-fail-fast runs. In a
non-fail-fast run, a failed worker is skipped by later phases, so the common
terminal stop is what prevents its phase context, connections, and background
receive drains from outliving the coordinator result. Stop retains the latest
assignment and bounded metrics so report collection can still identify the
exact assignment generation.

`wkbench run --phase-poll-timeout` controls the base worker phase poll wait.
When it is omitted, the coordinator default is used. The coordinator then adds
the expected phase schedule duration for connect, warmup, run, and cooldown;
for example, connect waits for `phase_poll_timeout + total_users/connect_rate`.
Warmup adds the shared warmup-operation deadline tail so its last scheduled
operation can settle without consuming the control-plane grace. A measured
group-operation tail includes SENDACK followed by every full or sampled RECV
wait performed serially for that message. Split groups use the largest actual
worker-local online member shard rather than the global member count. A churned
run adds that tail for every sequential measured window, because each window
joins its active operations before reconnect maintenance and the next window
begins. Measured traffic with zero or one configured concurrency checks
the window before every operation and never starts another operation after it
closes, so only one operation tail can remain instead of an entire overloaded
schedule. All workload shards on one worker share one runner-owned absolute
SEND-admission deadline. `Run` completes at that boundary while a runner-owned
task retains operations already admitted; `Cooldown` joins that task within the
configured drain budget, then uses only the remaining portion of that same
budget to require two separated healthy zero-work cuts across the matching
reader, bounded WKProto receive/adapter queues, publication work, and RECVACK
writes. A blocked socket read waiting for a future frame is idle, while any
non-idle read or RECVACK failure permanently invalidates the assignment proof.
A drain observes all clients at an adaptive bounded cadence: the 2,500-client
baseline uses at most four full cuts per second and still completes two already
empty cuts in under one second. Live status snapshots share the same per-handle
stable-zero state machine: after late work changes any counter or coverage, one
healthy zero cut starts a new proof and only an identical cut after the bounded
interval completes it. High-frequency status requests cannot advance time or
forge the second cut; incomplete evidence or a real read/RECVACK failure
permanently invalidates that generation. A planned churn replacement uses
`DrainAndStop`: it first establishes the live two-cut proof, then cancels and
joins the old generation and returns both boundary snapshots. Cancellation
owned by that planned stop is not a synthetic read or RECVACK failure, while
incomplete evidence on either side remains permanently incomplete. Any new
frame, failure, counter change, or pending work between the live proof and the
joined snapshot invalidates that stop boundary.
A drain deadline cancels the measured task, preserves the last bounded receive
snapshot, and fails the phase. Successful `Cooldown` does not close sessions:
exact-assignment stop may freeze one `terminal_pre_close` lifecycle cut only
when both SEND remaining work and the receive-drain proof are complete, then
`EndAssignment` closes sessions. The stop response and later stopped status
reads expose that immutable, provenance-marked cut; a new assignment clears it.
An externally captured terminal cut additionally requires a grant-bound,
server-confirmed ingress fence before receive readers are joined. The WKProto
adapter preserves the legacy no-grant `SealIngress` typed unsupported seam and
also exposes `SealIngressWithFence(TerminalFenceGrant)`: the shared client
quiesces SEND/PING, joins previously admitted SENDACKs, writes the bounded
request marker, and waits for the exact peer-decoded epoch/128-bit-nonce ACK.
The adapter orders session replacement against this cut and permanently rejects
later `Connect`, `Send`, `TrySend`, and `Ping` admission. Low-level `ReadFrame`
and `RecvAck` remain callable for compatibility, but the reviewed manager flow
must finish its receive proof and all RECVACK writes, join heartbeat, and wait
for target Delivery Quiesce before the grant is published; therefore no
RECVACK is legal after the marker.
Capability and nonce never enter logs or metric labels. A missing grant or
server handler keeps the stop fail-closed without `terminal_pre_close`, and
ordinary teardown still runs. TCP half-close, gateway async-write callbacks,
and local buffer state are explicitly not accepted as proof.

Before the server admits that ACK, the product-side two-cut convergence proof
must include the complete delivery runtime, not only channel append: fixed
families include `wukongim_delivery_recipient_worker_queue_depth`,
`wukongim_delivery_recipient_worker_inflight`,
`wukongim_delivery_actor_inflight_routes`,
`wukongim_delivery_ack_bindings`, and
`wukongim_delivery_retry_queue_depth` in addition to the existing typed queue
set. After every target session is fenced, one final product cut must prove no
new drop/result failure and no new queued/in-flight work before the benchmark
accepts a clean terminal boundary. The client/protocol seam alone cannot prove
that product convergence.
If a status request itself reaches that child deadline, the coordinator reports
`phase_timeout` instead of the ambiguous `phase_wait_failed` fallback. A worker
whose status endpoint never produced a valid response is attributed to the
`worker_status` operation. Each ordinary status request has an independent
short bound, so it cannot consume a long phase schedule by itself. When a GET
straddles the total phase deadline, the coordinator performs one independent
bounded final probe. A final active status is attributed to `phase_completion`,
because the exhausted phase budget is then the terminal fact; a final probe
that also blocks is attributed to `worker_status` as a real control-plane stall.
The run phase additionally adds the deterministic reconnect pacing between
scheduled churn windows for the busiest worker. Churn maintenance and the
final measured operation tail therefore do not consume the base control-plane
grace or create a false `phase_timeout`.

`PhasePrepare` has one extra coordinator step for split large groups: before normal prepare, the coordinator calls `/v1/prepare/channels` on workers that own split group channels. This creates owner channels before all workers append subscribers.

## Capacity Send Flow

```text
cmd/wkbench capacity send
  -> capacity.DiscoverTarget
       -> target /healthz, /readyz, /bench/v1/capabilities, /bench/v1/capacity-target
       -> build model.Target with discovered gateway TCP addrs
  -> start temporary local worker
  -> capacity.Search
       -> capacity.BuildScenario per offered QPS
       -> coordinator.Run
       -> report.SendRunSummaryFromMetrics
       -> classify pass/fail by actual QPS, sendack error rate, connect error rate, and p99
  -> capacity.WriteResult and console summary
```

`capacity send` does not start Docker Compose, build images, stop services, or
clean data directories. It only connects to already-running target API nodes.
The reported QPS is ingress sendack QPS during the measured `run` phase; group
fanout adds delivery work but is not the primary QPS denominator.

Report p99 limit checks use the maximum worker-local `run`-phase histogram;
explicit warmup and cooldown series do not affect the measured-capacity verdict.
Unlabeled histograms remain a compatibility fallback for older metric snapshots.
For local three-node evidence runs, `--profile-seconds` polls the worker status
and captures all node CPU profiles only while the expected run ID has
`active_phase=run`. After the profiles finish, the sampler reads worker status
again and accepts the capture only when the run ID still matches,
`active_phase=run`, and `last_error` remains empty. Each QPS attempt keeps its
own `pprof/run/<qps-tag>/` directory with the triggering status in
`worker-status.json`, the completion status in `worker-status-end.json`, and
both observations in `sampler.tsv`, so a missed, overwritten, incomplete, or
run-to-cooldown capture cannot be mistaken for valid hot-path evidence.

`capacity hot-channel` uses the same discovery, temporary worker, search, and
report flow, but every attempt fixes `channels.profiles[0].count` to one group
channel and sets the offered QPS as that channel's `rate_per_channel`. Its
`--senders` value controls how many online group members fan into the one
logical channel, and group traffic uses `sender_pick: round_robin` to spread
sendack waits across those senders.

Capacity attempt cleanup reuses the exact `run_id + assignment_id` returned by
the coordinator and validates the worker's terminal stop response. Final local
worker shutdown first reads `/v1/status`, and only issues a stop when that
status supplies both identifiers. A missing or non-terminal identity is an
explicit cleanup failure; capacity runners never fall back to a run-only or
empty stop request.

## Capacity Activate-Channels Flow

`capacity activate-channels` is a fixed-size evidence run, not a QPS search. It
discovers an already-running target, verifies every target API node supports the
Channel runtime snapshot and probe bench APIs, starts one temporary local
worker, builds a group scenario whose run phase schedules exactly one SEND per
generated channel, captures cold and active runtime snapshots, holds the cluster
without new sends, probes generated channel ranges through the all-node runtime
API, optionally evicts the generated runtime state, and writes
`activation_report.json` plus `summary.md`.

The activation verdict also records per-node active runtime distribution from
the active snapshot. On multi-node targets, a run with active leaders
concentrated on exactly one node is marked with `active_leader_single_node` so
bad startup or route placement samples are not treated as normal capacity
evidence.

The default shape is a channel-cardinality proof: 10,000 generated group
channels, a reusable online user pool, bounded prepare/connect rates, and a
longer activation window so the result reflects live channel runtime pressure
instead of a pure login or burst-ingress test. Increase `--users`,
`--connect-rate`, `--activation-concurrency`, or shorten `--activation-window`
only when the experiment intentionally adds those pressure dimensions.

When the three-node helper script captures before/after Prometheus snapshots,
`wkbench metrics classify` reports gateway dispatch wait, message append error
classes such as route-not-ready, short-result, invalid-config, and timeout,
Controller Raft Step queue/enqueue pressure, channel runtime append and
cold-activation stages, worker queue/current in-flight/peak in-flight by pool,
and storage commit request p99/tail counts by `leader_append` /
`follower_apply` lane plus batch p99s. The
Prometheus attribution reader accepts promoted `wukongim_channel_*` Channel
runtime metric families and falls back to the legacy `wukongim_channelv2_*`
families at read time, so the runtime hot path does not need compatibility
double-write during package promotion. The
10,000-channel helper also
fails the run when the
classification cannot prove a healthy channel runtime bootstrap: PendingMeta must
drain to zero with no releases, NeedMeta submitted and ok counts must match
with no retry/error counts, and PullHint send/receive error counts must remain
zero. Channel runtime high-level stage labels include `meta_resolve`,
`meta_apply`, and `runtime_append`; runtime append sub-stages include
`runtime_append_reserve_wait`, `runtime_append_submit`, and
`runtime_append_wait`; append batch metrics include `append_batch_wait` and
`append_batch_records`; gateway attribution includes async dispatch wait,
SEND batch handler duration, and batch records, while channelappend router
attribution separates successful local/remote group duration from complete
router-batch duration and retains item-weighted batch latency; message
attribution uses item-weighted permission, pre-append, and submitter stages;
admitted
future wait metrics include
`store_append_wait`, `post_store_commit_wait`,
`quorum_follower_pull_wait`, `quorum_ack_offset_wait`,
`quorum_hw_advance_wait`, and `quorum_final_complete_wait`; follower replication
metrics include `follower_pull_hint_to_submit`, `follower_pull_rpc`,
`follower_need_meta_pull_rpc`, `follower_store_apply`, and
`follower_apply_to_ack_return`, where the final label covers either the
post-apply progress ACK RPC or the fallback Pull `AckOffset` return; leader-side
PullBatch metrics report item, returned-record, and payload-byte p50/p99 plus
submit, all-await, maximum sequential-await, and total p99. The maximum
sequential-await value is the longest blocking `Await` call in collection
order, not the end-to-end completion latency of a specific Future. The Channel
RPC worker queue-wait p99 covers accepted-task wait through subgroup start,
including time behind an earlier subgroup in the same collected window;
leader Pull classification separately reports mailbox-wait, synchronous-handler,
and AckOffset-apply p99 plus completed append-waiter p50/p99. Mailbox wait can
include earlier handlers, cancellation sweeps, due work, and observer callbacks;
the synchronous handler stage excludes asynchronous store reads after their
submission. App Prometheus metrics deterministically sample these leader Pull
stages and completed-waiter shapes at one of every sixteen Pull op IDs;
PendingMeta
and NeedMeta counters include the
current outstanding PendingMeta gauge, created/converted/released shell counts,
NeedMeta submitted/ok/retry/err counts, and stable NeedMeta error classes such
as timeout and not ready; PullHint result counters include submitted, ok, total
err, and stable error classes such as
stale meta, channel not found, not ready, canceled, timeout, remote error, and other. Metadata resolve
sub-stages include `meta_slot_read`, `meta_create_build`, `meta_create_propose`,
`meta_create_propose_local`, `meta_create_propose_forward`,
`meta_create_slot_propose_submit`, `meta_create_slot_propose_wait`,
`meta_create_slot_control_wait`, `meta_create_slot_raft_commit_wait`,
`meta_create_slot_fsm_apply`, `meta_create_slot_fsm_commit`,
`meta_create_slot_mark_applied`, `meta_create_write`, and `meta_final_read` so
the report stays low-cardinality while still separating Slot metadata reads,
missing metadata placement/build, origin-side local vs forwarded Slot proposals,
Slot proposal submit, Slot future wait, Slot scheduler/control wait, Raft commit
wait, FSM apply, FSM Pebble commit, MarkApplied persistence, final rereads,
runtime create/apply, append admission, reactor mailbox submit, admitted future
wait, append batching behavior, durable append wait, and post-store local/quorum
commit wait. The follower replication split localizes quorum tails after a
leader has already durably stored an append.

The 1,000-channel real-QPS helper overrides channel runtime append batching with
`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=128` and
`WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=250us`, runs with 5,000 send
concurrency, uses a 15s sendack timeout, gives wkbench worker phases a 30s base
poll timeout, and starts the local gateway with 512 async SEND dispatch workers
and a 15s gateway send timeout. In that scenario each channel is relatively low
frequency while global QPS is high, so the shorter due-flush window avoids
per-channel tail latency while preserving the runtime's normal batch-size
ceiling. The longer timeout budget covers rare quorum tails after the measured
p99 remains healthy. General configs keep the runtime defaults unless this
benchmark-specific environment override is set.

The single-node and three-node 1,000-channel helpers also bind between-attempt
cleanup to the worker's current `/v1/status` assignment generation. They POST
that exact pair as JSON and require a matching terminal response before the next
QPS attempt; an idle worker is the only case where no stop is sent. This keeps a
failed prior attempt from poisoning later samples without reintroducing an
unsafe run-only stop.

The delivery and three-node presence helpers apply the same status-bound exact
stop contract in their temporary-worker cleanup traps.

## Capacity Message-Event Flow

`capacity message-event` is a fixed-size pressure run for the migrated message
event stream path. It does not use `/message/eventsync`, does not start workers,
and does not depend on `/bench/v1/*`; it only requires already-running target
API nodes with `/channel`, `/message/send`, `/message/event`, and `/metrics`.

```text
cmd/wkbench capacity message-event
  -> messageevent.DefaultConfig / flag overrides
  -> optionally warm generated channels before the measured metrics window
  -> optionally warm Channel append runtime with one normal SEND per channel
  -> capture before Prometheus snapshots from every --api node
  -> create generated group channels through POST /channel when not warmed
  -> run stream workflows with bounded concurrency
       -> POST /message/send with the legacy stream setting bit
       -> POST /message/event stream.delta for each lane/delta
       -> POST /message/event stream.finish once per stream
  -> capture after Prometheus snapshots from every --api node
  -> metrics.AnalyzeWukongIMPrometheus for message event cache/propose counters and append/propose stage p99s
  -> messageevent.WriteResult(message_event_report.json, summary.md)
```

Each stream workflow preserves `base -> deltas -> finish` order. Concurrency is
across streams, not within one stream, so the run exercises the Slot-leader
stream cache and proves the intended batching shape: deltas remain cache-only,
and the durable proposal count should match finished streams while durable
event count should be `streams * (lanes_per_stream + 1)`. The default shape is a
small smoke run; large cardinality and high-frequency pressure are opt-in
through flags such as `--channels`, `--streams-per-channel`,
`--deltas-per-lane`, and `--concurrency`.

For local three-node evidence runs, `scripts/bench-wukongim-three-nodes-message-event.sh`
wraps the command, starts the local `cmd/wukongim` cluster, builds `wkbench` when
needed, and stores logs, before/after Prometheus snapshots, during-run metrics
samples, per-node `metrics classify` output, pprof snapshots, server process
resource samples, and the `messageevent` report in one timestamped output
directory. Its `smoke`, `medium`, and `pressure` profiles pin 32, 1,000, and
10,000 channel shapes so follow-up baselines are comparable.

`messageevent` report gates are hard validation, not advisory text:
`append_count{path="cache"}` must match delta request count,
`propose_count{path="finish_batch"}` must match finished stream count, and
request errors, cache misses, and message-event backpressure must remain zero.
This protects the benchmark from passing when a future change accidentally
proposes every delta or loses stream-cache state.

## Worker Control Flow

Workers expose a small HTTP control API:

```text
GET  /healthz
GET  /v1/info
POST /v1/assign
POST /v1/prepare/channels
POST /v1/phase/prepare
POST /v1/phase/connect
POST /v1/phase/warmup
POST /v1/phase/run
POST /v1/phase/cooldown
POST /v1/stop
GET  /v1/status
GET  /v1/metrics
GET  /v1/report
```

`worker.State` stores the active assignment and lifecycle phase. Every assignment
has a required `assignment_id`, which is an immutable generation token within
`run_id`; reusing a run ID never makes two assignment generations equivalent.
The in-process state retains the complete Assignment for runner execution,
teardown, and report collection. `/v1/status` and other status-shaped control
responses serialize only `run_id`, `assignment_id`, and `worker_id` from that
assignment, so their response size does not grow with ChannelOwners, Plan,
Target, or Scenario. Status decoding also accepts legacy expanded Assignment
objects and preserves their additional fields during rolling upgrades.
Phase and owner-channel preparation requests carry both values in their JSON
body, stop carries both values in `StopRequest`, and metrics/report evidence
reads carry both as query parameters. Missing identifiers are rejected, and a
request must match both before it can observe or mutate state. Phase hooks are
asynchronous when they take longer than the short start grace period. In that
case the worker returns `202 Accepted`, sets `active_phase`, and later updates
`completed_phase` after the hook finishes. Synchronous error responses and
asynchronous status failures both carry the same stable `reason_code` and, for
typed session failures, an allowlisted person/group `operation`; the coordinator
preserves those codes instead of classifying failures from human-readable text.
Duplicate phase requests are idempotent when the requested phase is already
active or complete for the same assignment generation.

When a worker work directory is configured, `current-run.json` is only a
recovery hint. Persistence serializes a value copy with the Bench API token
removed and private file permissions; the live in-memory assignment retains
the credential needed by the runner. The native single-node cluster wrapper
stops and joins its owned worker, then removes that exact recovery file and its
now-empty `worker-state` directory before computing the final artifact
manifest. Worker runtime state therefore cannot become retained benchmark
evidence or a secondary credential store.

`GET /v1/status` stamps each live response with server UTC time and may include
one fixed-size `lifecycle` projection. The projection contains the current
active-session gauge, reconnect churn, and aggregate SEND, SENDACK,
terminal-error, remaining-work, and retry evidence. It never contains per-user
or per-message identities. A runner that cannot prove the retry lifecycle
leaves that part incomplete, so local authorization remains closed.

The native single-node cluster diagnostic samples that projection throughout
warmup, measured traffic, cooldown, and one stopped-assignment terminal cut.
The parent shell owns and joins that sampler, retains its stderr in the step,
and atomically publishes a bounded sampler status containing the PID/start
token, capture attempts/completions, exit status, and a closed reason. An
attempt without a matching completion identifies a capture that is still
blocked and cannot publish a clean stopped status. The asynchronous sampler
appends each validated one-line projection with Bash builtins; invoking an
external writer through `command` there can replace the background shell on
Bash 3.2 before its completion counter and loop advance. Any unexpected
sampler exit appends a lifecycle capture error and forces local exit 6, so the
typed step evaluator cannot authorize partial sampling evidence.
During measured sampling the local workload-overlap observer treats the main
wrapper shell as an owned process root and closes ownership over its complete
descendant tree. This includes short-lived typed `wkbench evidence` helpers;
unrelated WuKongIM or wkbench process trees remain host-confounding evidence.
Each sample is joined to independently observed server and worker PID start
tokens. The typed evaluator requires strictly increasing sample times,
monotonic aggregate traffic counters, a non-empty measured projection, and an
exact match between the stopped cut and the final traffic record. Its
reviewed group-fanout gate reconciles the entire warmup-plus-measured logical
population without retaining a per-message set. Strict `phase=warmup` SENDACK
successes plus measured SENDACK successes are multiplied by
`group_members - 1`; physical RECV observations and successfully written
RECVACK frames must both equal that value. In addition, one assignment-local
fixed-memory witness compares the exact expected, received, and acknowledged
`(ClientMsgNo, group channel, sender UID, recipient UID)` multisets through two
independently keyed anonymous projections. Retry attempts retain one logical
SEND identity and do not increase the denominator. A duplicate delivery cannot
compensate for a different missing recipient merely because total counts are
equal. Every well-formed physical group RECV remains actual evidence, including
an unexpected delivery back to the sender, and a successful RECVACK joins that
same actual tuple to the acknowledged multiset. Missing, malformed, or
overflowing proof state is insufficient evidence, while complete unequal
multisets are a product failure. Product-side
zero ACK bindings and the server-confirmed terminal fence separately prove
consumption after the client writer accepted each RECVACK.
Its post-warmup and terminal product-queue snapshots embed the exact
worker-observed UTC time, run, assignment generation, completed phase, and
active phase before scraping metrics; a timestamp-only shell cut cannot
authorize the result. Required queue families cover Gateway SEND admission,
Channel runtime and channelappend state, storage commit, the canonical Online
Delivery plan queue and in-flight workers, and owner-local pending RECVACK
bindings. Missing delivery families or a terminal delivery depth above its
post-warmup baseline fail closed, so an empty append queue cannot hide
recipient work that has not reached or been acknowledged by clients. The same
channelappend worker-pool boundary uses the production-wired
`wukongim_ants_pool_running{component="channelappend"}` family, summing its
fixed pool partitions. It does not substitute the dormant legacy effect-pool
family or manufacture a zero series, so an absent production pool observation
still fails closed. The same
two Prometheus cuts retain separate, closed cumulative result partitions for
accepted Online Delivery plan terminals and channelappend post-commit final
completions. Every fixed result series must exist exactly once at both
boundaries, counters must remain monotonic, and every non-`ok` total must equal
its post-warmup value. Successful `ok` work may grow. Unknown labels, counter
resets, a post-warmup delivery failure, or a post-warmup post-commit failure
fail closed; cumulative counters are never represented as queue depths. The
same measured timeline retains bounded Slot snapshot
inventories and Pebble compaction counters through the shared local-storage
capture helper. `localbaseline` strictly verifies every inventory digest,
file/byte total, monotonic compaction counter, measured boundary/cadence, and
post-drain terminal sample. Missing rows, counter resets, unsafe inventory
paths, or cadence gaps fail closed. Measured snapshot inventory changes and
compaction activity are emitted as separate additive typed observations. They
remain explanatory tail-latency correlations and do not change a clean step or
four-step authorization by themselves; neither observation asserts the
original bottleneck. Each step also retains immutable header-plus-one-row
storage and physical-device I/O summaries extracted from the append-only global
tables. Their exact AWK schemas, rate tag, node/host identity, and full-row
digests are typed closure inputs. A clean storage row must have complete
evidence; non-zero physical commits, logical requests, records, bytes, request
samples, and WAL input/output; result and request-lane partitions equal to the
request total; and complete positive ordered commit-size distributions. Host
I/O must be either explicitly complete with at least one available physical
device signal or explicitly platform-unavailable with none; absent, malformed,
or contradictory availability fails closed.

Every rate step is published as one closure manifest after its raw checksum
manifest, reconstructed typed evidence, and derived result are durable. The
staircase and baseline gate consume only that closure; they never trust a
caller-authored result. Checksum verification streams every potentially large
raw payload and retains only its relative path and digest. Bounded typed inputs
are reopened through the same no-follow artifact-root descriptor and rehashed
immediately before parsing, so verification memory does not grow with retained
metrics, logs, or binaries and a changed input cannot cross the check/use
boundary. The aggregate baseline document has an 8 MiB bound so all periodic
lifecycle samples from the four reviewed five-minute steps fit without making
the parser unbounded. The raw closure also binds the coordinator-emitted canonical
scenario, deterministic plan, and run report. Replay requires their run
identity and report verdict projection to match the diagnostic summary, rejects
hash-Slot-spread variants, and requires the reviewed single target and exact
one-worker report/plan shape.

One native baseline invocation creates exactly one random 128-bit lowercase
hex identity after validating its dedicated output directory and before any
preflight result is published. Every step run ID, typed step execution seal,
baseline evidence, artifact identity, and completion marker bind that same
identity. Each execution seal also authenticates the frozen source
configuration, exact effective configuration, and tested WuKongIM and wkbench
binary digests. Authorization
requires all four seals to agree, requires four distinct server PID/start-token
generations in strictly non-overlapping chronological order, and deliberately
allows the same owned worker generation to serve all four steps. Completion
replays those seals and requires their config/binary digests to equal the
global artifact identity, including equality between the attested source digest
and `original_config_sha256`, so closures from another invocation, source
configuration, or tested binary set cannot be transplanted into a newly sealed
result.

Before any owned product generation starts, the wrapper copies the exact
external WuKongIM TOML into a private directory outside the artifact root. The
source digest before and after copying and the destination digest must agree;
all generations, startup dry runs, structured redaction, and original-config
identity then use only that immutable snapshot. Product helpers run with an
empty inherited environment plus a fixed allowlist, and every reviewed product
performance setting is a literal rather than an inherited `WK_*` override.
The source path is authorizing only when typed baseline setting
`canonical_source_config` is true, which the wrapper derives only for the canonical repository
`scripts/wukongim/wukongim.toml` and its snapshot digest equals that file at
the frozen source revision; custom TOML remains diagnostic-only. Each rate
step retains a bounded executable attestation binding its rate/generation,
snapshot digest, and equal pre-spawn, post-stop, and sealed WuKongIM binary
digests. The plaintext snapshot is precisely removed after all product writers
stop and before either preflight or measured artifact checksums are published;
EXIT cleanup removes the same private file on failure.

After all closed steps, the wrapper publishes sealed baseline evidence and its
recomputed authorization, then a global checksum inventory, and finally one
exclusive atomic `local-baseline.json` marker outside that inventory. The same
path publishes terminal preflight denials, which can never authorize because
they contain no reviewed step closures. The completion consumer verifies the
identity and effective configuration, then branches on the parsed identity
`seal_scope`. A preflight completion must have no step closures and does not
require measured summary, storage, or host artifacts. A measured completion
replays every nested closure from the global inventory and requires the
marker's exact canonical `summary.tsv`, `storage_metrics_summary.tsv`, and
`host_io_summary.tsv` paths to be no-follow manifest members. It strictly
validates the three schemas and ordered row set;
the global storage and host rows must equal their per-step typed evidence by
value and full-row digest. Baseline evidence, artifact identity, and completion
marker also bind the absolute cleaned effective `WK_NODE_DATA_DIR` plus the
observed filesystem/device identity, total blocks, and block size. This binding
is independent of a superseded source-TOML `node.data_dir`. The consumer
recomputes authorization and requires deep equality before returning the typed
exit status. Before any output-directory side effect, the wrapper rejects
inherited node identity, static membership, and seed topology overrides. Its
redacted effective configuration appends the actual fixed runtime scalar
overrides used at startup; completion requires a non-zero `node.id`, exactly one
`cluster.nodes` member with that same ID, no seeds, and the sealed runtime
projection to equal the reviewed settings. The wrapper's final process status
is that consumer status.

Cooldown sampling retains the worker's bounded receive-drain snapshot in every
lifecycle cut. A drain no longer than the maximum sample gap may have zero or
one periodic sample only when the exact terminal pre-close identity, traffic,
receive-drain, and bounded-gap proof is present. Longer drains still require
periodic coverage; neither rule fabricates a missing observation.

The same single-node cluster timeline drives an optional bounded diagnostic
profile without parsing logs or error prose. A snapshot-safe typed reducer
selects only the first threshold bracket formed by two adjacent complete
measured samples: either actual SENDACK/offered throughput below 90 percent or
a new terminal/correctness failure. The wrapper then invokes the shared local
CPU/heap/goroutine helper once for node 1. No threshold produces an explicit
`not_triggered` status and no profile blobs. A trigger that starts too late,
crosses into drain, is interrupted, or has missing metadata, blob, or checksum
evidence closes the step as `insufficient_evidence`; a complete capture is
additive evidence and never changes the existing rate/product attribution.
The helper receives the benchmark bearer token only through its environment,
and the parent closes the live phase at SEND-admission completion before it
joins the bounded capture and seals the step payload.

The in-process dev simulator also uses the worker runner's traffic recovery hook after a runtime traffic error. This repairs only failed WKProto sessions when the workload can identify them, then rebuilds person/group workload objects with a new client message prefix while preserving healthy sessions. This avoids full online-user churn for a single send/recv failure.

The monotonic phase order is:

```text
idle -> assigned -> prepare -> connect -> warmup -> run -> cooldown -> stopped
```

`/v1/stop` requires the exact `run_id` and `assignment_id` and synchronously
publishes an assignment-generation-bound stopping gate before starting
background finalization. New assignment work, phase admission, and owner-only
channel preparation are rejected promptly while the gate is active. Phase hooks
and `/v1/prepare/channels` publish one shared lifecycle-task shape keyed by both
identifiers, with a unique task sequence, cancellation function, and completion
signal; stop cancels the matching published task, waits for its hook to exit,
tears down runner-owned connections/background drains, and only then commits
`stopped`. A stopped assignment identity is terminal and cannot be reactivated
or mutated by a delayed `/v1/assign`. The cleanup runs independently of the HTTP caller deadline;
concurrent retries for the same two-part identity join one finalizer, and a
timed-out caller receives no false acknowledgement while cleanup continues. The
task sequence prevents an older operation from erasing a newer task's
cancellation handle. A teardown error leaves the assignment non-stopped and
admission fenced; the default runner preserves the error across retries and
requires worker recovery/restart rather than falsely acknowledging resource
release. A stopped worker may accept only a distinct assignment identity,
including a new generation of the same run, which clears the old stopping gate
and stop task.

For an acknowledged external terminal cut, stop first rebuilds the live receive
proof and joins its readers, then always runs `EndAssignment`. A failed receive
seal cannot publish `terminal_pre_close`: the assignment still becomes stopped
after successful cleanup, while `/v1/stop` and exact retries retain the fixed
`terminal_receive_seal_failed` reason. The coordinator preserves that reason
instead of replacing it with the final retry timeout. Its default 15-second
stop budget matches the native single-node wrapper's reserved final 15 seconds
of the reviewed at-most-90-second cooldown, leaving bounded time for receive
reproof, reader join, and closing up to 2,500 sessions without extending SEND
admission.

The coordinator sends exact-assignment stop requests to all assigned workers
before terminal report collection on both successful and failed runs. It
requires matching `run_id` and `assignment_id`, `phase=stopped`, and an empty
`active_phase`; successful evidence reads carry the same pair and are atomically
rejected after another generation starts, even when it reuses the run ID. If
stop is not confirmed, the coordinator does not read moving worker
metrics/reports and writes only a minimal harness-invalid result with
`phase=stop` and either the generic `worker_stop_failed` reason or the narrower
`terminal_receive_seal_failed` reason returned by the worker.

## Dev-Sim Flow

```text
cmd/wkbench dev-sim
  -> devsim.LoadConfig
       -> strict YAML decode
       -> laptop-safe defaults
       -> WK_SIM_* environment overrides
  -> devsim.Run
       -> start /healthz and /status
       -> derive model.Target + model.Scenario + one in-process worker
       -> planner.Build
       -> poll target /healthz, /readyz, /bench/v1/capabilities
       -> worker.NewDefaultWorkloadRunner
       -> prepare -> connect -> warmup
       -> loop run windows until canceled
       -> on prepare/connect/warmup target error: record status, back off, retry readiness/connect
       -> on traffic window error: record status, back off, rebuild traffic identity, keep sessions, continue next window
```

`devsim` is intentionally a supervisor around existing wkbench primitives. It keeps the same black-box boundary as coordinator/worker runs: target mutation goes through `internal/bench/target`, traffic goes through WKProto clients, and no WuKongIM server internals are imported. `docker compose --profile dev-sim` uses this command for the optional `wk-sim` service; normal `docker compose up` does not start simulator traffic.

The `/status` endpoint distinguishes the configured steady-state online pool (`connected_users`) from the latest sampled live count (`active_users`) and reconnect churn (`reconnected_users`) so disconnect/reconnect flapping is visible during triage.

## Default Worker Runner Flow

The default runner is assembled by `worker.NewDefaultWorkloadRunner`. The private `worker.newDefaultWorkloadRunner` wrapper is kept for package-local server construction.

### Prepare

```text
Prepare
  -> when external_terminal_cut=true, reject an assignment without any
     receive-drain traffic or recv_ack traffic before target mutation
  -> prepareBenchTokens when identity.token.mode == bench_api
  -> prepareGroupData
       -> group channel upsert batches
       -> group subscriber batches
```

Group preparation uses the target bench API only. Small group profiles create
owned channels and append their subscribers; non-split small groups batch many
channel subscriber items into one `/bench/v1/channels/subscribers` request so
real metadata setup does not issue one HTTP POST per channel. Split large group
profiles create the logical group channel only on the deterministic owner, then
every worker appends its member range as subscribers.

### Connect

```text
Connect
  -> buildPersonExecutionPlan
  -> buildGroupExecutionPlan
  -> merge connection users with the worker identity range
  -> apply the assignment's optional worker client capacity profile
  -> create one optional shared monotonic TCP source dialer for the assignment
  -> workload.ConnectionManager.Connect
  -> optional heartbeat pings keep idle online users active
  -> wrap clients for concurrent frame matching
  -> build person workloads
  -> build group workloads
```

Each worker keeps its assigned `online.total_users` identity range connected even when some generated users are not referenced by a traffic profile. Profile-derived users are still merged in so existing group overlap behavior remains compatible.

The client wrapper serializes access to each underlying queued `ReadFrame` call and buffers unmatched frames. This lets concurrent person/group workloads on the same UID wait for different sendack or recv frames without stealing each other's frames. The wrapper also allocates monotonically increasing ClientSeq values per simulated TCP client, and each waiter still matches the exact ClientSeq and ClientMsgNo.

When any traffic stream sets `recv_ack: true`, the runner starts a background receive-ack drainer for connected clients. The drainer buffers drained recv frames only for channel types whose traffic enables receive verification (`full` or `sampled`). Other channel types are acknowledged and dropped immediately, so a mixed person-verification/group-fanout scenario cannot retain an unconsumed group-frame backlog.
Receive-drain handles support both cancellation and joining. Traffic generation
replacement starts new readers only after old readers exit, while terminal
teardown cancels drains, closes their owning connections to unblock reads, and
waits for every drain before acknowledging stop. Their bounded snapshots carry
only aggregate counts: active readers, queue-source coverage and depth,
queue-to-consumer handoffs, matching/in-flight work, cumulative receive
progress, and cumulative read or RECVACK failures. An assignment with no receive workload emits the explicit
canonical not-required proof; an omitted zero-value snapshot never authorizes a
terminal cut. Receive progress includes one count per physical RECV and one
count only after the corresponding protocol RECVACK write succeeds; overflow
permanently marks the generation incomplete. These counters are merged across
planned churn generations and participate in the stopped-boundary fingerprint,
so late delivery, duplicate delivery, or late acknowledgement invalidates the
previous zero-work proof.

For the reviewed single-worker group scenario, the runner creates one fanout
witness per assignment and reuses it across any traffic-generation rebuild.
Each successful logical group SENDACK contributes the current channel's exact
online recipient set minus its sender. The matching client binds its connected
UID to each physical group RECV and reuses the resulting opaque receipt only
after the bottom client writer completes RECVACK. The witness snapshot is part
of every receive-drain fingerprint; a new assignment creates a new secret and
an empty witness, while churn never resets the current assignment's history.

The shared `pkg/client` session owns WKProto CONNECT reads, socket decoding,
crypto, pending SENDACK matching, and a bounded lossless RECV queue. The
benchmark adapter preserves the old workload-facing frame API by converting
send futures back into local `SendackPacket` frames and forwarding decrypted
RECV packets through its independent bounded queue. The receive-ack drainer
consumes the adapter queues through the fixed priority arbitration: errors
precede SENDACKs, and their combined burst yields to a queued RECV after four
results. It briefly yields to foreground sendack/recv matchers when they are
queued.

### Warmup, Run, Cooldown

Warmup, run, and cooldown execute all stored person and group workloads concurrently. Warmup uses a reduced rate but schedules at least one message per assigned channel so cold runtime metadata is activated before measured traffic starts. After the last scheduled warmup admission settles, each workload keeps the phase open until the configured warmup boundary; the coordinator therefore records the full declared window rather than ending one scheduler interval early. Warmup raises per-message sendack/recv waits to at least the warmup duration so early cold bootstrap work is not cut off by the shorter measured-run timeout, but every wait shares a final deadline at `warmup end + the traffic's original operation timeout`; late messages therefore cannot extend the phase by another full warmup duration. The worker owns these deadlines on the process monotonic clock. Retained cross-process timestamps use wall time, so the typed timeline accepts only the fixed coordinator boundary tolerance for wall-clock slew at a proven minimum deadline; larger under-runs remain incomplete. Every send, sendack, recv, and recvack failure is bound to its session and low-cardinality operation, including explicit SENDACK rejection and receive payload mismatch; a typed per-session warmup operation failure is recorded and does not terminate the hook, so the report's declared error-rate limits own the final verdict. Non-session warmup failures remain fail-fast. Run uses each traffic entry's own `rate_per_channel`, adjusted by split traffic partitions for large groups. Timed measured run windows stop scheduling new messages at one shared absolute deadline and move only already-admitted operations into the bounded cooldown drain. Overloaded attempts therefore report lower actual QPS instead of extending measured admission. The zero/one-concurrency path enforces the same hard window between sequential operations and cannot accumulate every planned SENDACK/RECV timeout after the deadline. Cooldown starts no sends, returns immediately after exact admitted-work convergence, and fails if that work has not settled by its configured upper bound. Connections and background receive processing stay active through the post-drain lifecycle cut and close only at exact-assignment stop.

The typed session error keeps its UID only as private in-process recovery state;
its `Error` text exposes only an allowlisted operation. Worker phase responses
and `/v1/status` likewise map failures to closed reason and operation codes.
Raw UID, ChannelID, ClientMsgNo, frame summaries, or nested transport text must
not cross those JSON boundaries, while `errors.Is`/`errors.As` and
`SessionErrorUIDs` continue to support exact retry and session repair.

Timed measured run windows record individual send, SENDACK, receive-verification,
and RECVACK failures in workload metrics and continue scheduling. Warmup does
the same for typed session-operation failures while keeping structural errors
fail-fast. The declared error-rate limits own the final verdict; one operation
failure must not turn the worker into a `phase_hook_failed` harness result.
Parent phase cancellation never contributes send or receive error counters.
Untimed direct operations remain fail-fast.

Generic person and group traffic keeps its historical one-attempt behavior
unless `traffic.retry.enabled` is explicit. An enabled stream owns each logical
SEND through an initial attempt and no more than three retries at the fixed
100ms, 500ms, and 2s delays. Every retry allocates a fresh connection-local
`ClientSeq` while reusing the exact logical `client_msg_no`. The matcher accepts
a success SENDACK from any issued attempt, ignores a delayed retriable rejection
from an older attempt while the current one is pending, and terminates on any
non-retriable rejection. Its low-cardinality phase/profile/traffic counters
separate logical identities, physical attempts, retries, SENDACKs, exhaustion,
terminal failures, planned-shutdown cancellations, identity mismatches, and
exact remaining work; no UID,
channel, or message identity becomes a label. Sequential and concurrent
schedulers publish the immutable planned count before first SEND admission can
block, then publish dispatched and bounded-window outcome counts when the
scheduler returns. This keeps periodic lifecycle evidence truthful during the
measured window instead of first exposing its denominator in cooldown.

The default worker's bounded `LifecycleStatus` projects those counters only
from `run` and `run-window-*` phases. It derives stable-identity and evidence
completeness from exact counter invariants rather than fixed booleans, and
retains maximum-attempt policy/observation gauges as maxima across workload
streams. Warmup evidence is excluded. Coordinator warmup and measured phase
budgets include four ACK waits plus the three fixed delays when retry is enabled,
so a valid final attempt cannot be cut off by the outer phase deadline. Unlike
legacy one-attempt warmup traffic, retry-enabled warmup retains the configured
per-attempt ACK timeout instead of expanding each of four attempts to the full
warmup duration; all four share the explicit warmup-plus-retry-tail boundary.

## Planner Flow

`planner.Build` validates inputs, computes identity ranges, and creates a `model.Plan`. The total generated online identity pool is weighted across workers as `WorkerPlan.IdentityRange`; worker connect uses this range as the baseline online population. After the weighted ranges are final, the planner verifies each configured TCP source pool has at least `IdentityRange.Len()` candidates. Workers without an explicit pool are not assigned an inferred operating-system capacity.

Person profiles:

```text
profile.count channels
  -> two generated users per channel
  -> weighted channel ranges per worker
  -> participant ranges derived from channel ranges
```

Group profiles:

```text
normal group
  -> weighted channel ranges per worker
  -> member ranges are shared when members.overlap is allowed
  -> member ranges are disjoint when members.overlap is disallowed

split_members_and_traffic group
  -> requires profile.count == 1
  -> weighted member ranges per worker
  -> weighted traffic partitions per worker
  -> deterministic channel owner map
```

Allowed-overlap group members are selected from a shared identity pool by deterministic hash. Disallowed-overlap group members reserve disjoint user ranges. Person participants are distinct from their own profile ranges, but allowed-overlap groups may reuse the global identity pool.

When a group profile enables `hash_slot_spread`, its channel count must equal
`hash_slot_count`. Preparation and traffic construction use the same
deterministic channel-ID search so channel index `n` hashes to physical hash
slot `n`; reviewed stability presets use this for one `max-group` channel in
each of the 256 physical hash slots.

## Workload Flow

### Person Traffic

```text
PersonWorkload.RunWindow
  -> pick deterministic pair
  -> build payload and client_msg_no with phase/profile/traffic/channel/message markers
  -> send WKProto frame.ChannelTypePerson packet
  -> wait for matching sendack
  -> optionally wait for matching recv and send recvack
  -> record metrics and bounded error samples
```

Person channel IDs are encoded deterministically from both UIDs. The workload does not import `internal/runtime/channelid`.

### Group Traffic

```text
GroupWorkload.RunWindow
  -> pick deterministic group channel and message index
  -> apply sender_pick; high-concurrency round_robin matches an idle member at admission
  -> selected online member sends to frame.ChannelTypeGroup
  -> wait for matching sendack
  -> optionally verify full or sampled recipients
  -> optionally send recvack
  -> record metrics and bounded error samples
```

For split traffic, message indexes are partitioned by `TrafficPartitionCount` and `OwnedTrafficPartitions`, so workers emit non-overlapping message identity streams.

## Target Bench API Boundary

`internal/bench/target.Client` is the only target preparation client used by wkbench. It calls:

- `GET /healthz`
- `GET /readyz`
- `GET /debug/config`
- `GET /debug/cluster`
- `GET /debug/pprof/heap?gc=1`
- `GET /metrics`
- `GET /bench/v1/capabilities`
- `GET /bench/v1/snapshot`
- `GET /bench/v1/channel-runtime/snapshot`
- `POST /bench/v1/channel-runtime/probe`
- `POST /bench/v1/channel-runtime/evict`
- `POST /bench/v1/users/tokens`
- `POST /bench/v1/channels`
- `POST /bench/v1/channels/subscribers`
- `POST /bench/v1/channels/subscribers/remove`
- `POST /conversation/list`
- `POST /conversation/retry`

The server-side implementation lives outside this package. Keep request/response types in `pkg/bench/model` aligned with the bench API surface and avoid depending on internal server usecases from wkbench code.
The observation calls use the same optional Bench bearer as the restricted
Bench API. Debug/config responses, per-node live Slot snapshots, forced-GC
responses, and Prometheus scrapes have independent byte, row, Slot, replica,
line, and series bounds. Metrics decoding retains only the fixed Go/process,
runtime queue/inflight, Channel queue/rejection, and metadata-create families;
status failures and parse errors never include response bodies.
Channel runtime probes preserve both selector modes from the shared DTO. The
client sends concrete identities and bearer authentication unchanged to every
configured target and decodes ordered detailed runtime evidence; it does not
log request identities or credentials. Probe status errors omit server response
bodies before aggregation so an echoed identity or bearer capability cannot
enter client error strings. Successful probe bodies are read through a 32 MiB
endpoint-specific cap. An all-missing response may repeat the current 10 MiB
target request identity payload in both compatibility and detailed fields; the
cap covers both copies plus fixed evidence overhead for 1,200 rows without
permitting unbounded JSON allocation. Before transport, explicit selectors must
contain between 1 and 1,200 identities. Their responses must contain exactly one
detailed row per requested identity in the same order, with an exact matching
channel ID and type at every index. Generated responses must not contain detailed
rows. These validation errors contain no request or response identities.
Eviction remains generated-range only.

Conversation synchronization uses product routes, not Bench API routes, so the
target client never attaches its Bench bearer token. Every login starts with
empty cursor and `completed_coverage=0`, follows opaque `/conversation/list`
cursors with the product's 200-candidate page bound until `done=true`, and
hydrates bounded unresolved keys through `/conversation/retry`. It deduplicates
cross-page moves, never retains coverage for a later login, and decodes each
address attempt into fresh storage so a malformed response cannot pollute a
later fallback. Each successful page is bounded at 256 MiB. Status and decode
errors omit request UIDs, channel IDs, payloads, credentials, and server error
bodies.

## Failure Handling

- Static configuration and planning errors become `config_failed`.
- Target, worker, capability, or gateway preflight errors become `preflight_failed`.
- Worker phase errors become `worker_failed` unless the error is classified as target unavailable.
- Worker assignment failures are recorded with phase `assign`; assignment failures that happen before workload phases still write a terminal diagnostic summary when `run.report_dir` is configured, without polling worker metrics or reports that may belong to an older assignment.
- Missing worker metrics or reports are recorded with phase `collect`.
- Target connection failures during worker execution are wrapped as `target_unavailable`.
- Hard limit violations in report evaluation become `hard_limit_failed`.
- Context cancellation becomes `canceled`.
- Report collection or unexpected coordinator errors become `internal_failed` when no more specific status applies.

When `run.fail_fast` is true, the coordinator stops remaining workers after the first unrecoverable phase error.

## Code Reading Guide

- CLI behavior: start at `cmd/wkbench/main.go`, then follow into `config`, `planner`, and `coordinator`.
- Scenario schema or YAML behavior: read `pkg/bench/model/config.go` and `internal/bench/config/config.go`.
- Sharding bugs: start with `internal/bench/planner/planner.go`, then inspect `worker/person_runner.go` or `worker/group_runner.go`.
- Worker phase issues: read `internal/bench/worker/state.go` and `internal/bench/worker/server.go`.
- Send/recv correctness: read `internal/bench/workload/person.go`, `internal/bench/workload/group.go`, and `internal/bench/wkproto/client.go`.
- Report output and limit checks: read `internal/bench/report/report.go` and `internal/bench/metrics/metrics.go`.

## Current Boundaries

- `cmd/wkbench report` is reserved and currently returns an internal failure.
- Cleanup configuration exists in the scenario model, but cleanup execution is not implemented in the current runner.
- The split-group channel prepare barrier is minimal: channel owners create channels before subscriber prepare. It is not a full per-channel prepared status and reassignment system.
- Metrics scraping from target metrics endpoints is modeled but not yet used as a full scrape pipeline.
