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
- `workload`: reusable connection, person traffic, and group traffic executors.
- `target`: black-box HTTP client for target health, readiness, bench capabilities, capacity target, setup snapshot, presence snapshot, token, channel, and subscriber APIs. Setup mutation calls use the first healthy target API address and fall back on failure; targets such as `cmd/wukongim` route real metadata writes through their cluster runtime.
- `wkproto`: benchmark WKProto client implementation.
- `metrics`: worker-local counters, histograms, bounded error samples, aggregation helpers, and low-cardinality Prometheus attribution parsing.
- `report`: deterministic report construction and report directory writing.

Reviewed stability scenarios separate the full generated identity pool from
the connected online prefix. Group preparation writes the requested total
subscriber count from both pools, while execution connects only the planned
online identities. The planner validates reviewed ingress QPS exactly, checks
estimated online fanout within the declared tolerance, and rejects any profile
that cannot emit at least one message per required active-channel window.

During measured scheduled churn, the worker runs traffic in bounded windows.
At each boundary it archives the completed workload metrics, reconnects the
same-UID share, replaces the identity-swap share from deterministic offline
lanes, refreshes bench tokens, adds replacement UIDs to every affected group,
removes the replaced UIDs, and rebuilds person/group workloads before traffic
resumes. The add-before-remove order prevents a replacement sender from
temporarily losing membership, while removal keeps long-running group sizes
bounded. `OnlineIdentityIndexes` is worker-local runtime mapping state;
an empty mapping preserves the initial plan. Each measured window gets a unique
message identity namespace while report metrics normalize it back to the stable
`run` phase. Churn never requests history sync.

Group `sender_pick: weighted_80_20` emits four of each five deterministic
messages from the first 20% of online members and the fifth from the remaining
80%. This distribution is per channel and does not change channel rate.

The WKProto bench client is a thin adapter over `pkg/client`. The shared client
owns CONNECT/CONNACK, optional payload encryption, socket decoding, SENDACK
matching, RECV decryption, and the single writer/reader pumps. The bench adapter
keeps the existing workload-facing `Send` / `ReadFrame` contract by converting
`pkg/client` SEND futures back into local SENDACK frames and forwarding RECV
frames from the shared reader into the same bounded queue. Worker clients are
created from an optional worker-local `client` profile. Its send queue, maximum
inflight SEND count, socket read buffer, and frame buffer capacities flow from
the selected worker assignment through the default connection manager factory;
the frame buffer capacity bounds both the adapter queue and the inner inbound
RECV queue. Omitting the complete profile retains the tooling defaults. Worker
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
Scheduled traffic uses per-key pending queues plus a ready-key queue when a
workload supplies a serialization key. That keeps one busy client or channel
from forcing a linear scan across a large pending list. Person/group timed
traffic supplies the sender UID as the key in high-concurrency mode, preserving
one in-flight `Send -> Sendack` operation per simulated TCP client. Wrapped
clients allocate connection-local monotonic ClientSeq values so each waiter
matches by ClientSeq plus ClientMsgNo.

## Coordinator Run Flow

```text
cmd/wkbench run
  -> config.LoadTarget / LoadWorkerSet / LoadScenario
  -> config.ValidateStaticConfig / ValidateTargetScenario
  -> planner.Build
  -> coordinator.Preflight.Check
       -> target /healthz, /readyz, /bench/v1/capabilities
       -> worker /v1/info
       -> gateway checker
  -> coordinator.assignWorkers
       -> copy only each selected worker's client capacity profile and TCP source pool into its assignment
       -> omit worker control credentials from the assignment payload
       -> POST worker /v1/assign
  -> phases: prepare -> connect -> warmup -> run -> cooldown
       -> POST worker /v1/phase/<phase>
       -> poll worker /v1/status until completed_phase catches up
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
It also records connection attempt, success, and error counts so a failed
connect phase can be diagnosed without reconstructing integers from a rounded
error rate.
`report.WriteDir` additionally writes `diagnostic-summary.json`, a bounded,
redacted machine contract with actual coordinator phase windows and structured
worker failures. Typed person/group session failures retain an optional
low-cardinality `operation` such as `group_sendack`; unknown values are omitted,
and raw UIDs, URLs, paths, and error text never enter this projection.
`summary.md` remains human-readable and is not the analysis machine contract.

Explicit TCP source pool errors are worker-local configuration or capacity
failures and therefore resolve to `worker_failed`, never
`target_unavailable`.

`wkbench run --phase-poll-timeout` controls the base worker phase poll wait.
When it is omitted, the coordinator default is used. The coordinator then adds
the expected phase schedule duration for connect, warmup, run, and cooldown;
for example, connect waits for `phase_poll_timeout + total_users/connect_rate`.
The run phase also adds the deterministic reconnect pacing between scheduled
churn windows for the busiest worker. Churn maintenance therefore does not
consume the base control-plane grace or create a false `phase_timeout`.

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
`append_batch_records`; admitted future wait metrics include
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

`worker.State` stores the active assignment and lifecycle phase. Phase hooks are asynchronous when they take longer than the short start grace period. In that case the worker returns `202 Accepted`, sets `active_phase`, and later updates `completed_phase` after the hook finishes. Synchronous error responses and asynchronous status failures both carry the same stable `reason_code` and, for typed session failures, an allowlisted person/group `operation`; the coordinator preserves those codes instead of classifying failures from human-readable text. Duplicate phase requests are idempotent when the requested phase is already active or complete.

The in-process dev simulator also uses the worker runner's traffic recovery hook after a runtime traffic error. This repairs only failed WKProto sessions when the workload can identify them, then rebuilds person/group workload objects with a new client message prefix while preserving healthy sessions. This avoids full online-user churn for a single send/recv failure.

The monotonic phase order is:

```text
idle -> assigned -> prepare -> connect -> warmup -> run -> cooldown -> stopped
```

`/v1/stop` cancels the active phase hook when possible and marks the worker stopped. A stopped worker may accept a new assignment.

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

When any traffic stream sets `recv_ack: true`, the runner starts a background receive-ack drainer for connected clients. The drainer buffers drained recv frames only when receive verification is enabled (`full` or `sampled`); send-only simulator traffic with receive verification disabled acknowledges and drops drained recv frames to avoid building a verification backlog that no workload will consume.

The shared `pkg/client` session owns WKProto CONNECT reads, socket decoding,
crypto, pending SENDACK matching, and the bounded RECV queue. The benchmark
adapter preserves the old workload-facing frame API by converting send futures
back into local `SendackPacket` frames and forwarding decrypted RECV packets
through the wrapper queue. The receive-ack drainer consumes that wrapper queue
and briefly yields to foreground sendack/recv matchers when they are queued.

### Warmup, Run, Cooldown

Warmup, run, and cooldown execute all stored person and group workloads concurrently. Warmup uses a reduced rate but schedules at least one message per assigned channel so cold runtime metadata is activated before measured traffic starts. Warmup also raises per-message sendack/recv waits to at least the warmup duration, preventing cold bootstrap latency from being cut off by the shorter measured-run operation timeout. Every send, sendack, recv, and recvack failure is bound to its session and low-cardinality operation, including explicit SENDACK rejection and receive payload mismatch; a typed per-session warmup operation failure is recorded and does not terminate the hook, so the report's declared error-rate limits own the final verdict. Non-session warmup failures remain fail-fast. Run uses each traffic entry's own `rate_per_channel`, adjusted by split traffic partitions for large groups. Timed run windows stop scheduling new messages when the window expires and then wait only for already-started send operations to finish; overloaded attempts therefore report lower actual QPS instead of extending the measured window to drain the full original schedule. Cooldown waits for the configured drain period without new sends.

Timed measured run windows record individual send, SENDACK, receive-verification,
and RECVACK failures in workload metrics and continue scheduling. Warmup does
the same for typed session-operation failures while keeping structural errors
fail-fast. The declared error-rate limits own the final verdict; one operation
failure must not turn the worker into a `phase_hook_failed` harness result.
Parent phase cancellation never contributes send or receive error counters.
Untimed direct operations remain fail-fast.

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
  -> first online member sends to frame.ChannelTypeGroup
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
- `GET /bench/v1/capabilities`
- `GET /bench/v1/snapshot`
- `POST /bench/v1/users/tokens`
- `POST /bench/v1/channels`
- `POST /bench/v1/channels/subscribers`
- `POST /bench/v1/channels/subscribers/remove`

The server-side implementation lives outside this package. Keep request/response types in `pkg/bench/model` aligned with the bench API surface and avoid depending on internal server usecases from wkbench code.

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
