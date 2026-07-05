# WuKongIM Performance Triage

Use this project-local runbook when investigating `wk-sim`, `wkbench dev-sim`, or three-node Docker Compose performance, timeout, throughput, sendack, recv, delivery, Raft, or data-plane regressions.

## Core Rule

Collect evidence before tuning or changing code. Classify the failure, write one falsifiable hypothesis, then run one minimal experiment that changes exactly one variable.

## Required Flow

1. Define the scenario, stack mode, duration, and success criteria.
2. Establish a clean baseline before high-rate or accumulated-data runs.
3. Collect evidence: status timeline, Compose logs, split node logs, metrics, pprof, Docker stats, git revision, and Compose config.
4. Classify the issue before changing anything.
5. Form one falsifiable hypothesis with one expected observation.
6. Run one minimal experiment that changes exactly one variable.
7. Change code only after evidence points to a code defect and a regression test can be written.
8. Verify with the target scenario plus `smoke-default` and `sampled-correctness`.
9. Record concise findings in `docs/development/WKSIM_STRESS_FINDINGS.md`.

## Automatic Evidence Collection

Prefer the project script for first-pass evidence capture:

```bash
scripts/dev-sim-perf-triage.sh smoke-default --duration 120
scripts/dev-sim-perf-triage.sh sampled-correctness --duration 120 --no-build
scripts/dev-sim-perf-triage.sh group-fanout --duration 180 --profile-seconds 30
```

Useful options:

- `--clean`: stop Compose and remove `docker/dev-cluster` plus `docker/dev-sim` before running.
- `--no-build`: reuse the existing image.
- `--no-up`: collect from an already running stack.
- `--out-dir DIR`: override the evidence base directory.
- `--duration SECONDS`: control status and Docker stats sampling duration.
- `--profile-seconds SECONDS`: control each node CPU pprof duration.

The script writes evidence first, then exits non-zero if final `/status` is not healthy or reports send/recv errors.
The sampled `/status` timeline now includes both the configured steady-state online pool (`connected_users`) and the latest live online count (`active_users`), plus reconnect churn (`reconnected_users`) when the simulator repairs sessions.

## Package Promotion Naming

Default evidence files and human-readable summaries use promoted runtime names.
Raw Prometheus inputs and old file aliases remain compatible so existing
dashboards and archived scripts can still be compared.

| Surface | Default output | Legacy input or alias |
| --- | --- | --- |
| Channel runtime report keys | `channel_*`, `channel/...` | `wukongim_channelv2_*`, `component="channelv2"`, `channelv2-*` pool prefixes |
| Transport runtime report labels | `transport/...` | `component="transportv2"` |
| Channel metrics summary file | `channel_metrics_summary.tsv` | `channelv2_metrics_summary.tsv` |
| wkcli top display | `channel`, `transport` | `channelv2`, `transportv2` API payload fields or alert fingerprints |

Do not rename raw Prometheus metric families in a small report cleanup. If the
server metric families are promoted later, keep a dedicated compatibility plan
with dual publish or reader fallback.

For `cmd/wukongim` local three-node Channel runtime capacity runs, do not use the
Compose dev-sim path. Use the local startup script wrapper:

```bash
scripts/bench-wukongim-three-nodes-10kch.sh
```

This wrapper starts nodes through `scripts/start-wukongim-three-nodes.sh`,
defaults to a 10,000-group-channel cardinality run, enables metrics and pprof,
and stores evidence under
`docs/development/perf-runs/<timestamp>-three-node-activate-10kch/`. It records
node configs, startup plan, logs, before/after Prometheus snapshots,
per-node `wkbench metrics classify` attribution files under `metrics/`,
goroutine/heap pprof data, server process CPU/memory samples under
`resources/`, the wkbench activation report, console output, and a top-level
`summary.md`. The server resource sampler writes
`resources/server-process.jsonl` plus `resources/server-process-summary.tsv`;
use `--resource-interval 0` to keep only before/after samples. The default
online user pool is intentionally smaller than the channel count; raise
`--users` and `--connect-rate` only when the experiment intentionally adds
gateway connection pressure. The default `--stable-p99 2s` is a cold-activation
guardrail; pass a stricter value such as `200ms` when validating a latency SLA.
The local three-node configs use three initial physical Slots so the default
10k-channel run exercises a distributed metadata path instead of one hot Slot
Raft group.
Treat `active_leader_single_node` as an invalid topology sample first: inspect
the activation report's per-node active runtime distribution before using that
run as capacity evidence.
For cold Channel runtime activation attribution, first compare the per-node
`channel_meta_resolve_p99_seconds`,
`channel_meta_create_build_p99_seconds`,
`channel_meta_create_propose_p99_seconds`,
`channel_meta_create_propose_local_p99_seconds`,
`channel_meta_create_propose_forward_p99_seconds`,
`channel_meta_create_slot_propose_submit_p99_seconds`,
`channel_meta_create_slot_propose_wait_p99_seconds`,
`channel_meta_create_slot_control_wait_p99_seconds`,
`channel_meta_create_slot_raft_commit_wait_p99_seconds`,
`channel_meta_create_slot_fsm_apply_p99_seconds`,
`channel_meta_create_slot_fsm_commit_p99_seconds`,
`channel_meta_create_slot_mark_applied_p99_seconds`,
`channel_meta_apply_p99_seconds`, and
`channel_runtime_append_p99_seconds`,
`channel_runtime_append_reserve_wait_p99_seconds`,
`channel_runtime_append_submit_p99_seconds`, and
`channel_runtime_append_wait_p99_seconds`,
`channel_append_batch_wait_p99_seconds`, and
`channel_append_batch_records_p50`,
`channel_append_store_wait_p99_seconds`, and
`channel_append_post_store_commit_wait_p99_seconds` values before using pprof to inspect
the hot path. In the default Slot-backed writer, `meta_create_propose` includes the
Slot proposal and wait-for-apply path; the Slot submit/wait sub-stages split
that default path at the Multi-Raft future boundary, then split future wait into
local scheduler/control wait, Raft commit wait, FSM apply, FSM Pebble commit,
and MarkApplied persistence. `runtime_append` remains the aggregate Channel runtime
append facade time; its sub-stages split append reservation, reactor mailbox
submit, and admitted future wait. Append batch wait and record metrics show
whether that admitted future wait is mostly batching delay; append store and
post-store commit waits split durable append from local/quorum completion. When
storage is suspected, compare
`storage_commit_request_p99_seconds` against
`storage_commit_total_p99_seconds`: a high request p99 with low grouped commit
total points at caller-visible queue/admission wait, while both rising points at
the physical batch path. If the caller-visible tail is on the `leader_append`
or `follower_apply` lane, also compare
`wukongim_channelv2_worker_batch_items{kind="store_append"}` and
`wukongim_channelv2_worker_batch_items{kind="store_apply"}` count/sum deltas to
confirm whether Channel runtime worker-level store batching is active.
When
post-store quorum wait rises, use
`channel_append_quorum_follower_pull_wait_p99_seconds`,
`channel_append_quorum_ack_offset_wait_p99_seconds`,
`channel_append_quorum_hw_advance_wait_p99_seconds`, and
`channel_append_quorum_final_complete_p99_seconds` to separate follower pull,
leader progress-ACK/AckOffset processing, HW advancement, and final waiter
completion. Then
use `channel_replication_follower_pull_hint_to_submit_p99_seconds`,
`channel_replication_follower_pull_rpc_p99_seconds`,
`channel_need_meta_pull_rpc_p99_seconds`,
`channel_replication_follower_store_apply_p99_seconds`, and
`channel_replication_follower_apply_to_ack_return_p99_seconds` to localize
the follower-side step that delayed quorum coverage. Ordinary follower progress
ACKs are sent only after follower store apply succeeds; Pull `AckOffset` remains
the fallback progress return and leader HW still advances only through normal
quorum checks. During cold activation,
also check `channel_pending_meta_current_max`,
`channel_pending_meta_created_count`,
`channel_pending_meta_converted_count`,
`channel_pending_meta_released_count`,
`channel_need_meta_pull_submitted_count`,
`channel_need_meta_pull_ok_count`,
`channel_need_meta_pull_retry_count`,
`channel_need_meta_pull_err_count`, and stable NeedMeta error-class counters
such as `channel_need_meta_pull_not_ready_err_count`,
`channel_need_meta_pull_not_replica_err_count`, and
`channel_need_meta_pull_timeout_err_count` before treating follower recovery
probes as the primary wakeup path. If those accepted PullHint
stage counts are much lower than follower apply counts, check
`channel_pull_hint_submitted_count`, `channel_pull_hint_ok_count`,
`channel_pull_hint_err_count`, and the stable error-class counters such as
`channel_pull_hint_stale_meta_err_count`,
`channel_pull_hint_channel_not_found_err_count`,
`channel_pull_hint_not_ready_err_count`, and
`channel_pull_hint_canceled_err_count`,
`channel_pull_hint_timeout_err_count`,
`channel_pull_hint_remote_err_count`, and
`channel_pull_hint_other_err_count` before assuming the accepted PullHint hot
path is slow. If leader-side PullHint errors are present, compare them with
receiver-side stage counters such as
`channel_pull_hint_receive_meta_resolve_err_count`,
`channel_pull_hint_receive_meta_hint_ok_count`,
`channel_pull_hint_receive_meta_validate_err_count`,
`channel_pull_hint_receive_meta_apply_err_count`,
`channel_pull_hint_receive_submit_err_count`,
`channel_pull_hint_receive_await_err_count`, and
`channel_pull_hint_receive_channel_not_found_err_count` to separate remote
transport failures from follower metadata propagation, lazy activation, reactor
admission, and follower future wait failures. A non-zero
`channel_pull_hint_receive_meta_hint_ok_count` means followers successfully
used leader-carried metadata while local metadata reads were still catching up.

Node logs are collected from `internal/log` output under `docker/dev-cluster/node*/logs`:

- `logs/app/`: copied from node `app.log`.
- `logs/error/`: copied from node `error.log`.
- `logs/debug/`: copied from node `debug.log` when present.
- `logs/warn/`: copied from node `warn.log`; if the file is missing on older runs, derived from `app.log` warn records.
- `logs/compose/`: raw `docker compose logs` output for nodes and `wk-sim`.
- `status.jsonl`: sampled `/status` timeline with `connected_users`, `active_users`, and `reconnected_users`.

When reviewing `status.jsonl`, look for `active_users` dipping below `connected_users` or `reconnected_users` increasing between samples; that usually indicates reconnect churn rather than a pure throughput issue.

## Scenario Matrix

| Scenario | Suggested Environment | Purpose |
| --- | --- | --- |
| `smoke-default` | Compose defaults | Prove the stack is healthy before stress runs. |
| `sampled-correctness` | Low users/channels, `WK_SIM_VERIFY_RECV=sampled` | Catch cross-node delivery and recv correctness issues. |
| `person-hotpath` | Person channels only | Isolate personal channel send, metadata refresh, sendack, and routing. |
| `group-fanout` | Group channels only | Isolate subscriber expansion, delivery tag, and cross-node fanout. |
| `mixed-highrate` | Person + group, higher rate/concurrency | Exercise contention between gateway, data-plane RPC, Raft/store, delivery, and GC. |
| accumulated-data | Do not clean `docker/dev-cluster` | Find history-sensitive storage, replay, and metadata refresh issues. |
| `custom` | Explicit `WK_SIM_*` overrides | Reproduce a user-provided workload exactly. |

Do not start with `mixed-highrate` when the path-specific failure is unknown. Run isolated person/group scenarios first.

## Suggested Scenario Presets

`scripts/dev-sim-perf-triage.sh` applies these presets and generates a unique `WK_SIM_UID_PREFIX` by default. Use direct `docker compose` commands only when you need manual control.

```bash
# Baseline health, Compose defaults.
docker compose --profile dev-sim up -d --build wk-sim

# Correctness-oriented sampled run for smaller laptops.
WK_SIM_USERS=40 \
WK_SIM_PERSON_CHANNELS=10 \
WK_SIM_GROUP_CHANNELS=3 \
WK_SIM_GROUP_MEMBERS=12 \
WK_SIM_RATE=0.5/s \
WK_SIM_TRAFFIC_CONCURRENCY=16 \
WK_SIM_VERIFY_RECV=sampled \
WK_SIM_UID_PREFIX=sampled-$(date +%s) \
  docker compose --profile dev-sim up -d --build wk-sim

# Person send-path isolation.
WK_SIM_USERS=500 \
WK_SIM_PERSON_CHANNELS=250 \
WK_SIM_GROUP_CHANNELS=0 \
WK_SIM_RATE=1/s \
WK_SIM_TRAFFIC_CONCURRENCY=256 \
WK_SIM_VERIFY_RECV=none \
WK_SIM_UID_PREFIX=person-$(date +%s) \
  docker compose --profile dev-sim up -d --build wk-sim

# Group fanout isolation.
WK_SIM_USERS=500 \
WK_SIM_PERSON_CHANNELS=0 \
WK_SIM_GROUP_CHANNELS=250 \
WK_SIM_GROUP_MEMBERS=10 \
WK_SIM_RATE=1/s \
WK_SIM_TRAFFIC_CONCURRENCY=256 \
WK_SIM_VERIFY_RECV=none \
WK_SIM_UID_PREFIX=group-$(date +%s) \
  docker compose --profile dev-sim up -d --build wk-sim
```

## Evidence Directory

Store every triage run under `docs/development/perf-runs/<timestamp>-<scenario>/`.

```text
docs/development/perf-runs/20260521-153000-group-fanout/
  summary.md
  env.txt
  git.txt
  compose-config.yml
  status.jsonl
  docker-stats.jsonl
  logs/
    compose/
      wk-node1.log
      wk-node2.log
      wk-node3.log
      wk-sim.log
    app/
      wk-node1.log
      wk-node2.log
      wk-node3.log
    error/
      wk-node1.log
      wk-node2.log
      wk-node3.log
      wk-sim.log
    warn/
      wk-node1.log
      wk-node2.log
      wk-node3.log
      wk-sim.log
    debug/
      wk-node1.log
      wk-node2.log
      wk-node3.log
  metrics/
    node1.prom
    node2.prom
    node3.prom
  pprof/
    node1-cpu.pb.gz
    node1-heap.pb.gz
    node1-goroutine.txt
```

Minimum evidence:

```bash
git rev-parse HEAD > git.txt
git status --short >> git.txt
docker compose config > compose-config.yml
curl -fsS http://127.0.0.1:19091/status >> status.jsonl
docker stats --no-stream >> docker-stats.jsonl
docker compose logs --no-color wk-node1 > logs/compose/wk-node1.log
docker compose logs --no-color wk-node2 > logs/compose/wk-node2.log
docker compose logs --no-color wk-node3 > logs/compose/wk-node3.log
docker compose logs --no-color wk-sim > logs/compose/wk-sim.log
cp docker/dev-cluster/node1/logs/app.log logs/app/wk-node1.log
cp docker/dev-cluster/node1/logs/error.log logs/error/wk-node1.log
cp docker/dev-cluster/node1/logs/warn.log logs/warn/wk-node1.log
cp docker/dev-cluster/node1/logs/debug.log logs/debug/wk-node1.log
curl -fsS http://127.0.0.1:15001/metrics > metrics/node1.prom
curl -fsS http://127.0.0.1:15002/metrics > metrics/node2.prom
curl -fsS http://127.0.0.1:15003/metrics > metrics/node3.prom
curl -fsS http://127.0.0.1:15001/debug/pprof/goroutine?debug=2 > pprof/node1-goroutine.txt
curl -fsS http://127.0.0.1:15002/debug/pprof/goroutine?debug=2 > pprof/node2-goroutine.txt
curl -fsS http://127.0.0.1:15003/debug/pprof/goroutine?debug=2 > pprof/node3-goroutine.txt
```

CPU profiles are useful when the run is actively reproducing the issue:

```bash
curl -fsS 'http://127.0.0.1:15001/debug/pprof/profile?seconds=30' > pprof/node1-cpu.pb.gz
curl -fsS 'http://127.0.0.1:15002/debug/pprof/profile?seconds=30' > pprof/node2-cpu.pb.gz
curl -fsS 'http://127.0.0.1:15003/debug/pprof/profile?seconds=30' > pprof/node3-cpu.pb.gz
curl -fsS http://127.0.0.1:15001/debug/pprof/heap > pprof/node1-heap.pb.gz
curl -fsS http://127.0.0.1:15002/debug/pprof/heap > pprof/node2-heap.pb.gz
curl -fsS http://127.0.0.1:15003/debug/pprof/heap > pprof/node3-heap.pb.gz
```

## Prometheus Bottleneck Attribution

Use Prometheus metrics as the primary evidence source for bottleneck attribution. Keep `/bench/v1/snapshot` limited to benchmark setup counters; it is not a performance attribution surface.

For `cmd/wukongim`, enable metrics through the normal API listener:

```ini
WK_API_LISTEN_ADDR=127.0.0.1:5001
WK_METRICS_ENABLE=true
```

Then scrape:

```bash
curl -fsS http://127.0.0.1:5001/metrics > metrics/wukongim.prom
```

For lightweight local testing without a Prometheus server, capture one snapshot
before the measured window and one after it:

```bash
curl -fsS http://127.0.0.1:5001/metrics > metrics/before.prom
# run wkbench capacity send or another measured SEND -> SENDACK workload
curl -fsS http://127.0.0.1:5001/metrics > metrics/after.prom
go run ./cmd/wkbench metrics classify --before metrics/before.prom --after metrics/after.prom
```

The classifier is a first-pass attribution helper. Use the raw `.prom` files,
pprof, and logs before changing configuration or code.

When a Prometheus server is scraping the target, use these starter queries.

Gateway async SEND pressure:

```promql
wukongim_gateway_async_send_queue_depth
wukongim_gateway_async_send_queue_depth / clamp_min(wukongim_gateway_async_send_queue_capacity, 1)
histogram_quantile(0.99, rate(wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket[1m]))
histogram_quantile(0.99, rate(wukongim_gateway_async_send_batch_wait_duration_seconds_bucket[1m]))
histogram_quantile(0.50, rate(wukongim_gateway_async_send_batch_records_bucket[1m]))
```

Channel runtime append pressure:

```promql
sum by (reactor_id, priority) (wukongim_channelv2_reactor_mailbox_depth)
sum by (pool) (wukongim_channelv2_worker_queue_depth)
sum by (pool) (wukongim_channelv2_worker_inflight)
sum by (pool) (wukongim_channelv2_worker_inflight_peak)
sum by (result) (rate(wukongim_channelv2_rpc_pull_total[1m]))
sum by (reason, result, error) (rate(wukongim_channelv2_pull_hint_total[1m]))
histogram_quantile(0.99, sum by (le, commit_mode) (rate(wukongim_channelv2_append_duration_seconds_bucket[1m])))
histogram_quantile(0.99, sum by (le, stage, result) (rate(wukongim_channelv2_append_stage_duration_seconds_bucket[1m])))
histogram_quantile(0.99, sum by (le, stage, commit_mode, result) (rate(wukongim_channelv2_append_wait_stage_duration_seconds_bucket[1m])))
histogram_quantile(0.99, sum by (le, stage, result) (rate(wukongim_channelv2_replication_stage_duration_seconds_bucket[1m])))
histogram_quantile(0.99, sum by (le, kind, result) (rate(wukongim_channelv2_worker_task_duration_seconds_bucket[1m])))
histogram_quantile(0.50, rate(wukongim_channelv2_append_batch_records_bucket[1m]))
```

ChannelAppend authority and ants pool pressure:

```promql
wukongim_channelappend_writer_admission_depth
wukongim_channelappend_writer_pool_running
sum by (stage, result) (rate(wukongim_channelappend_effect_pool_submit_total[1m]))
sum by (stage) (wukongim_channelappend_effect_pool_inflight)
sum by (stage) (wukongim_channelappend_effect_pool_saturated)
histogram_quantile(0.99, sum by (le, stage, result) (rate(wukongim_channelappend_effect_duration_seconds_bucket[1m])))
```

`scripts/bench-wukongim-three-nodes-1000ch.sh` writes final pool pressure
headlines into `summary.txt` and `summary.md`. `over90_pools` counts node-local
pool instances whose observed queue or worker/pool utilization reached at
least 90% capacity during sampling. `peak_internal_mib_s` in
`cluster_transport_peak_summary.tsv` is the higher side of the highest sampled
cluster-wide inter-node outbound/inbound byte rate, derived from adjacent deltas
of `wukongim_transport_sent_bytes_total` and
`wukongim_transport_received_bytes_total`; use `peak_duplex_mib_s` in the same
file when full-duplex NIC load is more useful.

Storage commit pressure:

```promql
wukongim_runtime_pool_queue_depth{component="db",pool="message_commit"}
wukongim_runtime_pool_queue_depth{component="db",pool="message_commit"} / wukongim_runtime_pool_queue_capacity{component="db",pool="message_commit"}
wukongim_storage_commit_queue_depth
histogram_quantile(0.99, sum by (le, store, lane, result) (rate(wukongim_storage_commit_request_duration_seconds_bucket[1m])))
histogram_quantile(0.99, sum by (le, store, stage, result) (rate(wukongim_storage_commit_batch_duration_seconds_bucket[1m])))
histogram_quantile(0.50, sum by (le, store) (rate(wukongim_storage_commit_batch_requests_bucket[1m])))
histogram_quantile(0.50, sum by (le, store) (rate(wukongim_storage_commit_batch_records_bucket[1m])))
```

Interpretation matrix:

| Evidence | Classification | Next check |
| --- | --- | --- |
| Gateway queue ratio is high and gateway async dispatch wait p99 rises, while Channel runtime queues are low | gateway dispatch bottleneck | gnet event loops, async SEND worker count, handler CPU, pprof |
| Gateway async dispatch wait is low, but Channel runtime reactor mailbox or worker queues grow | Channel runtime bottleneck | append p99, worker kind p99, store/RPC pprof |
| Channel runtime worker queue is low but `channel_worker_inflight_peak{pool=...}` reaches the configured worker count | blocking worker saturation | compare store append/apply worker task p99, storage commit request lane tails, goroutine pprof |
| `channelappend_effect_pool_submit_total{result="full"}` rises or `channelappend_effect_pool_saturated` stays high | channelappend ants stage pool saturation | compare stage-specific worker count, `channelappend_effect_duration_seconds`, appender pprof, recipient post-commit fanout |
| ChannelAppend effect queue grows while pool saturation is low | reactor scheduling or downstream channel-state drain issue | inspect reactor mailbox, append in-flight limit, channel busy counts, goroutine pprof |
| `channel_meta_resolve_p99_seconds` rises | metadata control path bottleneck | slot/controller metadata read or create path, meta cache behavior |
| `channel_meta_create_build_p99_seconds` rises | metadata placement/build bottleneck | channel placement resolver and route snapshot reads |
| `channel_meta_create_propose_p99_seconds` rises | Slot metadata propose/apply bottleneck | Slot proposal wait, Multi-Raft apply, metadata FSM commit |
| `channel_meta_create_propose_local_p99_seconds` rises | local Slot proposal bottleneck at origin | local Slot submit/wait split |
| `channel_meta_create_propose_forward_p99_seconds` rises | remote Slot leader forwarding bottleneck at origin | node RPC forward path and leader-side Slot wait |
| `channel_meta_create_slot_propose_submit_p99_seconds` rises | Slot runtime submit bottleneck | Multi-Raft proposal enqueue/scheduler pressure |
| `channel_meta_create_slot_propose_wait_p99_seconds` rises | Slot proposal wait bottleneck | Raft commit, apply, metadata FSM batch, Pebble commit |
| `channel_meta_create_slot_control_wait_p99_seconds` rises | Slot worker scheduling/control queue bottleneck | Multi-Raft worker scheduling, Slot control backlog |
| `channel_meta_create_slot_raft_commit_wait_p99_seconds` rises | Raft commit wait bottleneck | Slot Raft append/replication/leader loop, follower ack latency |
| `channel_meta_create_slot_fsm_apply_p99_seconds` rises | Slot FSM apply bottleneck | command decode/apply batch cost and nested FSM commit |
| `channel_meta_create_slot_fsm_commit_p99_seconds` rises | metadata Pebble commit bottleneck | meta DB write batch, fsync/storage latency |
| `channel_meta_create_slot_mark_applied_p99_seconds` rises | Slot applied-index persistence bottleneck | Raft log MarkApplied write path |
| `channel_meta_apply_p99_seconds` rises | cold runtime create/apply bottleneck | Channel runtime ensure/load, store open, mailbox/worker pressure |
| `channel_runtime_append_p99_seconds` rises | append wait bottleneck | reactor append p99, worker kind p99, storage commit p99 |
| `channel_runtime_append_reserve_wait_p99_seconds` rises | per-channel append admission bottleneck | same-channel append reservation contention |
| `channel_runtime_append_submit_p99_seconds` rises | reactor mailbox admission bottleneck | mailbox capacity, reactor scheduling, event queue pressure |
| `channel_runtime_append_wait_p99_seconds` rises | admitted append future bottleneck | append batching wait, store append, quorum replication, worker result handling |
| `channel_append_batch_wait_p99_seconds` rises | append batching delay | append batch max wait, batch formation, per-reactor channel distribution |
| `channel_append_store_wait_p99_seconds` rises | durable append wait | store append worker queue/run time, message DB group commit, fsync/storage latency |
| `storage_commit_request_p99_seconds` rises while `storage_commit_total_p99_seconds` stays low | commit queue/admission wait | commit queue depth, lane-specific `leader_append` / `follower_apply` request tails, caller context budget, submit goroutine pprof |
| `storage_commit_request_over_10s_count{lane="leader_append"}` or `{lane="follower_apply"}` rises | rare caller-visible storage tail | match lane to `channel_worker_inflight_peak`, worker task p99, and blocked goroutine stacks |
| `storage_commit_request_p99_seconds` and `storage_commit_total_p99_seconds` both rise | grouped commit path bottleneck | batch collect/build/publish split, Pebble sync/fsync, storage device latency |
| `channel_append_post_store_commit_wait_p99_seconds` rises | post-store commit wait | follower pull/apply cadence, AckOffset handling, HW advancement, quorum completion |
| `channel_append_quorum_follower_pull_wait_p99_seconds` rises | follower pull service wait | pull hints, follower parking/recovery probe, leader recent cache/store-read path |
| `channel_append_quorum_ack_offset_wait_p99_seconds` rises | follower apply or ack return wait | follower store apply, progress ACK RPC, fallback next-pull AckOffset path |
| `channel_append_quorum_hw_advance_wait_p99_seconds` rises | leader HW advancement wait | follower match progress, ISR/MinISR rules, leader ack processing |
| `channel_append_quorum_final_complete_p99_seconds` rises | waiter completion wait | reactor reply completion, future wakeup, caller-side blocked receive |
| `channel_replication_follower_pull_hint_to_submit_p99_seconds` rises | follower wakeup/scheduling wait | PullHint delivery, parked follower state, inflight pull suppression, due scheduler |
| `channel_replication_follower_pull_rpc_p99_seconds` rises | follower pull RPC wait | leader pull handling, recent cache/store read path, transport RPC latency |
| `channel_need_meta_pull_rpc_p99_seconds` rises | follower bootstrap metadata pull wait | NeedMeta leader pull handling, metadata clone path, transport RPC latency |
| `channel_replication_follower_store_apply_p99_seconds` rises | follower durable apply wait | store-apply worker queue/run time, `store_apply` worker batch size, follower message DB commit latency |
| `channel_replication_follower_apply_to_ack_return_p99_seconds` rises | follower progress return wait | progress ACK RPC, fallback next-pull AckOffset path, leader ack response path |
| `channel_pending_meta_current_max` remains non-zero or releases rise | follower bootstrap leak or rejection | NeedMeta ok/retry/err counters, error classes, local replica membership |
| `channel_need_meta_pull_retry_count` or `channel_need_meta_pull_err_count` rises | NeedMeta bootstrap instability | stable NeedMeta error-class counters, leader pull path, transport errors |
| `channel_pull_hint_err_count` rises or PullHint submitted/ok counts are far below follower apply counts | PullHint wakeup loss or rejection | stable PullHint error-class counters, follower recovery probe counts, route/meta readiness |
| Gateway wait and Channel runtime queues both rise | downstream backpressure visible at gateway | determine whether Channel runtime or host CPU saturates first |
| Queues stay low but SENDACK latency is high | synchronous path outside observed queues | message usecase, metadata ensure/apply, routing, pprof |
| Batch records p50/p99 stay near 1 while queue wait is high | batching is not forming under load | shard distribution, workload channel cardinality, batch wait/record limits |

Avoid high-cardinality Prometheus labels such as `uid`, `channel_id`, or `client_msg_no`. Use `pkg/observability/sendtrace` and diagnostics sampling for per-message investigation after Prometheus identifies the subsystem.

## Classification Guide

| Evidence | Likely Class | First Check |
| --- | --- | --- |
| `smoke-default` fails | baseline health failure | readiness, config, recent diff, Compose logs |
| early-window timeout with `channelmeta.bootstrap` | cold start or warmup gap | warmup duration and channel coverage |
| `send_errors > 0` | send/append/forwarding path | leader routing, channel runtime, Raft append/apply |
| `recv_errors > 0` with sendack success | delivery path | presence, delivery tag, cross-node fanout, recv matcher |
| clean passes, accumulated fails | data/history-sensitive issue | storage, replay, metadata refresh, scans |
| service idle, `wk-sim` busy | benchmark client bottleneck | simulator CPU, concurrency, RTT |
| `connected_users` is stable but `active_users` dips or spikes | online churn or reconnect flapping | reconnect path, gateway stability, session repair, target logs |
| all containers saturated | local Docker capacity | host CPU, concurrent tests, container limits |
| pprof hot in send/store/delivery | server hot path | targeted unit test or package benchmark |
| goroutines blocked on RPC/apply/fetch | distributed runtime lag | data-plane pending/inflight and Raft/fetch logs |

## Minimal Experiment Rules

Change one variable per run.

- Workload variables: `WK_SIM_RATE`, `WK_SIM_TRAFFIC_CONCURRENCY`, `WK_SIM_WARMUP`, `WK_SIM_VERIFY_RECV`, person/group channel counts, group members.
- Environment variables: clean vs accumulated data, build freshness, concurrent host load, metrics/diagnostics enabled.
- Service config: data-plane pool, gateway event loops, append batching, Channel runtime store append/apply worker caps, delivery ack batching, cache TTLs.

Use `WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS` and
`WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS` only as a bounded-concurrency
experiment when `channel_worker_inflight_peak` reaches the store pool limit
and storage request tails show `leader_append` / `follower_apply` pressure. The
caps do not change durable commit, sync, quorum, or ACK semantics; they only
control how many blocking store calls enter the shared message DB commit
coordinator at once.

Prefer workload experiments before configuration experiments. Prefer configuration experiments before code changes.

## Fix Eligibility

Only change code when all are true:

- Evidence points to a specific subsystem and failure mode.
- Configuration/workload changes cannot explain the issue.
- A focused regression test can fail before the fix.
- The fix preserves cluster semantics; do not add local-only deployment branches.
- The target scenario plus `smoke-default` and `sampled-correctness` can verify the result.

## Report Template

```markdown
## Scenario
- workload:
- clean or accumulated:
- duration:
- success criteria:

## Evidence
- status:
- logs:
- metrics:
- pprof:
- docker stats:

## Classification
- category:
- confidence:
- reason:

## Hypothesis
- hypothesis:
- falsification test:

## Next Experiment
- one variable to change:
- expected result:
- stop condition:

## Fix Eligibility
- code change needed: yes/no
- reason:
- required regression test:
```
