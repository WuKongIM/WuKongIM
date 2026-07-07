# wkbench

`wkbench` is a black-box benchmark driver for WuKongIM. It talks to a running WuKongIM cluster through public HTTP, benchmark-only HTTP, and WKProto gateway endpoints. It does not import server internals or bypass cluster semantics; a single-node deployment is treated as a single-node cluster.

## Commands

```bash
go run ./cmd/wkbench <command> [flags]
```

| Command | Purpose |
| --- | --- |
| `worker` | Starts one worker control process. Workers hold WKProto clients and execute assigned workload shards. |
| `validate` | Loads target, workers, and scenario YAML and validates static config plus deterministic planning. |
| `doctor` | Validates target and workers, then checks target health, bench API capabilities, worker control APIs, and gateway reachability. |
| `run` | Runs the full coordinator flow: validate, preflight, assign workers, prepare, connect, warmup, run, cooldown, and report. |
| `dev-sim` | Runs a long-lived development simulator that keeps users online and emits low-rate person/group messages. |
| `capacity send` | Searches maximum stable ingress send QPS against already-running target APIs. |
| `capacity hot-channel` | Searches maximum stable ingress QPS for one fixed group channel with configurable sender fan-in. |
| `capacity activate-channels` | Activates a fixed number of group channels through real SEND traffic, holds them live, and probes Channel runtime state. |
| `capacity message-event` | Runs fixed-shape `/message/event` stream pressure, captures message event metrics, and writes a report. |
| `metrics classify` | Compares before/after Prometheus snapshots and prints gateway, Controller Raft, Channel runtime, and storage attribution hints. |
| `report` | Reserved for future standalone report rendering. It is not implemented yet. |

Exit codes are stable: `0` success, `1` config validation failure, `2` preflight failure, `3` hard limit failure, `4` worker failure, `5` target unavailable, and `6` internal failure.

## Target Requirements

The target WuKongIM process must expose the normal health/readiness endpoints and the benchmark API:

- `GET /healthz`
- `GET /readyz`
- `GET /bench/v1/capabilities`
- `GET /bench/v1/capacity-target`
- `GET /bench/v1/snapshot`
- `POST /bench/v1/users/tokens`
- `POST /bench/v1/channels`
- `POST /bench/v1/channels/subscribers`

Enable the server-side bench API in `wukongim.conf` before running wkbench:

```ini
WK_BENCH_API_ENABLE=true
```

The current bench API is intended for controlled benchmark environments. Do not expose `/bench/v1/*` on public networks.

`capacity message-event` is the exception: it uses only public `/channel`,
`/message/send`, `/message/event`, and `/metrics` endpoints. It does not require
`/bench/v1/*`, but it should still be run only against controlled benchmark
clusters because it mutates generated channels and messages.

## Minimal Workflow

Start one or more workers first:

```bash
WK_BENCH_WORKER_TOKEN=worker-secret \
  go run ./cmd/wkbench worker \
  --listen 127.0.0.1:19090 \
  --work-dir ./tmp/wkbench-worker-a
```

Validate local files without network checks:

```bash
go run ./cmd/wkbench validate \
  --target ./target.yaml \
  --workers ./workers.yaml \
  --scenario ./scenario.yaml
```

Run network preflight checks:

```bash
go run ./cmd/wkbench doctor \
  --target ./target.yaml \
  --workers ./workers.yaml \
  --scenario ./scenario.yaml
```

Run the benchmark:

```bash
go run ./cmd/wkbench run \
  --target ./target.yaml \
  --workers ./workers.yaml \
  --scenario ./scenario.yaml
```

For large runs with intentionally slow connection ramps, raise the coordinator
phase poll margin:

```bash
go run ./cmd/wkbench run \
  --target ./target.yaml \
  --workers ./workers.yaml \
  --scenario ./scenario.yaml \
  --phase-poll-timeout 30s
```

The connect phase waits for this base timeout plus `total_users/connect_rate`.

For a compiled binary, replace `go run ./cmd/wkbench` with the binary path.

## Capacity Send

`capacity send` connects to already-running WuKongIM API nodes, discovers
gateway TCP addresses through `/bench/v1/capacity-target`, starts a temporary
local worker, and searches for maximum stable ingress send QPS.

```bash
wkbench capacity send \
  --api http://127.0.0.1:15001,http://127.0.0.1:15002,http://127.0.0.1:15003
```

The command does not start Docker Compose services, build images, stop nodes, or
clean data directories. Enable `WK_BENCH_API_ENABLE=true` and configure
`WK_EXTERNAL_TCPADDR` on each target node so the capacity target endpoint returns
host-reachable gateway addresses.

Useful tuning flags:

```bash
wkbench capacity send \
  --api http://127.0.0.1:15001,http://127.0.0.1:15002,http://127.0.0.1:15003 \
  --profile mixed \
  --start-qps 100 \
  --max-qps 5000 \
  --stable-p99 200ms \
  --duration 30s \
  --group-members 10
```

To isolate one hot logical channel, use `capacity hot-channel`. It prepares one
group channel and spreads sends across the configured online sender set:

```bash
wkbench capacity hot-channel \
  --api http://127.0.0.1:5001 \
  --gateway 127.0.0.1:5100 \
  --senders 64 \
  --start-qps 1000 \
  --max-qps 50000 \
  --duration 30s \
  --stable-p99 200ms
```

`capacity hot-channel` is intended for `SEND -> SENDACK` hot-write capacity.
Timed run windows stop scheduling new messages when the window expires, so
overloaded attempts show lower actual QPS instead of silently extending the
measured run to drain the whole offered schedule.

## Capacity Activate Channels

`capacity activate-channels` is the preferred proof for simultaneous live
Channel runtime channel cardinality. It prepares group metadata through the bench API,
opens a bounded online user pool, sends exactly one WKProto group SEND per
generated channel during the activation window, holds the cluster, then probes
runtime state on every target API node.

```bash
wkbench capacity activate-channels \
  --api http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013 \
  --gateway 127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113 \
  --channels 10000 \
  --users 1000 \
  --activation-window 120s \
  --activation-concurrency 512 \
  --stable-p99 2s \
  --report-dir ./tmp/wkbench-activate-channels
```

The defaults focus on channel cardinality rather than connection-burst stress:
10,000 group channels, 1,000 online users, `--connect-rate 500`, and a 120 second
activation window. Raise `--users` or `--connect-rate` only when the experiment
intentionally includes gateway connection pressure. Tighten `--stable-p99`, for
example to `200ms`, when the run is meant to prove a strict cold-activation
latency SLA rather than live-channel cardinality.

For local `cmd/wukongim` three-node runs, prefer
`scripts/bench-wukongim-three-nodes-10kch.sh`. It wraps this command and
collects node logs, Prometheus snapshots plus per-node classification files,
pprof, and server process CPU/memory samples under `resources/`. The referenced
three-node configs use three initial physical Slots so cold channel metadata
creation is distributed across the local cluster instead of one Slot Raft group.
The activation report includes per-node active runtime distribution
(`active_leader`, `active_follower`, and `follower_parked`); on multi-node
targets, `active_leader_single_node` marks samples where all active leaders
landed on one node instead of a distributed topology.

For connection-route presence checks, use
`scripts/bench-wukongim-three-nodes-presence.sh`. It starts the same local
three-node cluster, runs a connection-only wkbench scenario with heartbeat
pings, polls `/bench/v1/presence/snapshot` while the run is active, and
validates the live peak for owner-active count, authority-active count, pending
count, hash-slot totals, touch count, and TTL expiry count. The final
`report.json` is still used for workload status and error-rate gates.

The per-node classification files include Controller Raft Step queue pressure
plus Channel runtime cold-activation stage p99s:
`channel_meta_resolve_p99_seconds`, `channel_meta_slot_read_p99_seconds`,
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
`channel_meta_create_write_p99_seconds`,
`channel_meta_final_read_p99_seconds`, `channel_meta_apply_p99_seconds`,
`channel_runtime_append_p99_seconds`,
`channel_runtime_append_reserve_wait_p99_seconds`,
`channel_runtime_append_submit_p99_seconds`, and
`channel_runtime_append_wait_p99_seconds`,
`channel_append_batch_wait_p99_seconds`, and
`channel_append_batch_records_p50`,
`channel_append_store_wait_p99_seconds`, and
`channel_append_post_store_commit_wait_p99_seconds`. Quorum post-store
breakdown is reported as
`channel_append_quorum_follower_pull_wait_p99_seconds`,
`channel_append_quorum_ack_offset_wait_p99_seconds`,
`channel_append_quorum_hw_advance_wait_p99_seconds`, and
`channel_append_quorum_final_complete_p99_seconds`. Follower replication
breakdown is reported as
`channel_replication_follower_pull_hint_to_submit_p99_seconds`,
`channel_replication_follower_pull_rpc_p99_seconds`,
`channel_need_meta_pull_rpc_p99_seconds`,
`channel_replication_follower_store_apply_p99_seconds`, and
`channel_replication_follower_apply_to_ack_return_p99_seconds`. PendingMeta
bootstrap counters are reported as
`channel_pending_meta_current_max`,
`channel_pending_meta_created_count`,
`channel_pending_meta_converted_count`,
`channel_pending_meta_released_count`, and release-class counters such as
`channel_pending_meta_timeout_release_count`. NeedMeta pull counters are
reported as `channel_need_meta_pull_submitted_count`,
`channel_need_meta_pull_ok_count`, `channel_need_meta_pull_retry_count`,
`channel_need_meta_pull_err_count`, and stable error-class counters such as
`channel_need_meta_pull_not_ready_err_count`,
`channel_need_meta_pull_not_replica_err_count`, and
`channel_need_meta_pull_timeout_err_count`. PullHint
result counters are reported as `channel_pull_hint_submitted_count`,
`channel_pull_hint_ok_count`, `channel_pull_hint_err_count`, and the stable
error-class counters such as `channel_pull_hint_stale_meta_err_count`,
`channel_pull_hint_channel_not_found_err_count`,
`channel_pull_hint_not_ready_err_count`, and
`channel_pull_hint_canceled_err_count`,
`channel_pull_hint_timeout_err_count`,
`channel_pull_hint_remote_err_count`, and
`channel_pull_hint_other_err_count`. Receiver-side PullHint stage errors are
reported as counters such as
`channel_pull_hint_receive_meta_resolve_err_count`,
`channel_pull_hint_receive_meta_hint_ok_count`,
`channel_pull_hint_receive_meta_validate_err_count`,
`channel_pull_hint_receive_meta_apply_err_count`,
`channel_pull_hint_receive_submit_err_count`,
`channel_pull_hint_receive_await_err_count`, and
`channel_pull_hint_receive_channel_not_found_err_count`; `meta_hint` means the
follower used leader-carried metadata while its local metadata read path was
still catching up. Use these counters to separate
control-plane Step backpressure, Slot metadata reads, missing metadata
placement/build, Slot metadata local vs forwarded proposals, Slot metadata
proposal submit, Slot metadata proposal wait, Slot scheduler/control wait, Raft
commit wait, FSM apply, FSM Pebble commit, MarkApplied persistence, final
rereads, runtime create/apply, append admission, reactor mailbox submit, append
future wait, append batching behavior, durable append wait, post-store
local/quorum commit wait, the follower pull/AckOffset/HW/final-completion split,
the follower-side hint/pull/apply/ack-return split, PendingMeta/NeedMeta
bootstrap health, and PullHint send/receive/error structure
before looking at pprof.

## Capacity Message Event

`capacity message-event` targets the migrated message event stream path through
public HTTP APIs. It creates generated group channels, sends stream base
messages through `/message/send`, sends cache-only `stream.delta` events, then
completes each stream with one `stream.finish`. It captures `/metrics` before
and after the run and writes `message_event_report.json` plus `summary.md`.

Small smoke run:

```bash
wkbench capacity message-event \
  --api http://127.0.0.1:5001 \
  --report-dir ./tmp/wkbench-message-event
```

Manual high-cardinality run:

```bash
wkbench capacity message-event \
  --api http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013 \
  --run-id message-event-100k \
  --channels 100000 \
  --streams-per-channel 1 \
  --lanes-per-stream 2 \
  --deltas-per-lane 4 \
  --payload-bytes 128 \
  --concurrency 4096 \
  --request-timeout 15s \
  --report-dir ./tmp/wkbench-message-event-100k
```

Use `--warm-channels` when the experiment should exclude generated `/channel`
setup from the measured before/after metrics window. Use `--warm-runtime` with
it when the experiment should also exclude first-append Channel runtime
activation: the runner sends one normal non-stream message per generated
channel before the before snapshot, then measures only stream base, delta, and
finish traffic.

For local `cmd/wukongim` three-node runs, prefer
`scripts/bench-wukongim-three-nodes-message-event.sh`. It starts the local
cluster, builds `wkbench` when needed, runs `capacity message-event`, and
collects node logs, Prometheus before/after snapshots, during-run metrics
samples, per-node `metrics classify` output, pprof snapshots, and server
process CPU/memory samples:

```bash
scripts/bench-wukongim-three-nodes-message-event.sh \
  --profile medium \
  --warm-channels \
  --warm-runtime
```

The script profiles are:

| Profile | Channels | Streams | Delta events | Purpose |
| --- | ---: | ---: | ---: | --- |
| `smoke` | 32 | 64 | 512 | Fast local evidence loop. |
| `medium` | 1,000 | 2,000 | 16,000 | Baseline with cold-start noise removed by `--warm-channels`. |
| `pressure` | 10,000 | 20,000 | 160,000 | Larger pressure sample before tuning storage or cache internals. |

Concurrency is across streams. Within a stream the runner keeps
`base -> deltas -> finish` order so the test exercises the Slot-leader stream
cache instead of racing finish ahead of cached deltas. Expected durable proposal
count is the finished stream count; expected durable event count is
`streams * (lanes_per_stream + 1)` because repeated deltas compact into one
durable lane event per lane when `stream.finish` flushes the batch.

The report has hard gates for `cache == delta_events`,
`finish_batch proposals == streams`, zero request errors, zero cache misses, and
zero message-event backpressure. A gate failure makes the command fail even when
individual HTTP requests returned success.

The report and `metrics classify` include the message event gauges and counters:
`message_event_stream_cache_sessions_max`,
`message_event_stream_cache_open_lanes_max`,
`message_event_stream_cache_payload_bytes_max`,
`message_event_append_count{path=...}`,
`message_event_append_count{event_type=...}`,
`message_event_append_count{result=...}`,
`message_event_propose_count{path=...}`,
`message_event_propose_p99_seconds{path=...}`,
`message_event_append_stage_p99_seconds{path=...,stage=...}`,
`message_event_propose_stage_p99_seconds{path=...,stage=...}`,
`message_event_propose_batch_events_p50{path=...}`,
`message_event_propose_batch_events_p99{path=...}`,
`message_event_backpressured_count`, and
`message_event_cache_miss_count`.

## Compose Development Simulator

The Docker Compose development cluster can start an optional simulator service named `wk-sim`:

```bash
docker compose --profile dev-sim up -d --build
curl http://127.0.0.1:19091/status
docker compose logs -f wk-sim
```

Plain `docker compose up -d` does not start the simulator. The `dev-sim` profile must be enabled explicitly.

To verify the local Compose stack end to end, run:

```bash
scripts/dev-sim-compose-smoke.sh
```

The script retries transient `docker compose up --build` failures, waits for
`wk-sim` to report running traffic, and checks recent node/simulator logs for
panic markers.

The service runs:

```bash
wkbench dev-sim --config /etc/wkbench/dev-sim.yaml
```

For local non-Docker runs:

```bash
GOWORK=off go run ./cmd/wkbench dev-sim --config ./docker/sim/dev-sim.yaml
```

Status endpoints:

- `GET http://127.0.0.1:19091/healthz`
- `GET http://127.0.0.1:19091/status`

Safe tuning environment variables:

| Variable | Meaning |
| --- | --- |
| `WK_SIM_USERS` | Total generated online user pool. |
| `WK_SIM_PERSON_CHANNELS` | Number of one-to-one channels. |
| `WK_SIM_GROUP_CHANNELS` | Number of group channels. |
| `WK_SIM_GROUP_MEMBERS` | Members per group channel. |
| `WK_SIM_RATE` | Per-channel person and group send rate, for example `0.5/s`. |
| `WK_SIM_TRAFFIC_CONCURRENCY` | Maximum concurrent send+sendack operations per traffic stream. |
| `WK_SIM_VERIFY_RECV` | Receive verification mode, for example `none` or `sampled`. |
| `WK_SIM_UID_PREFIX` | Prefix for generated simulator user IDs. |

The Compose profile sets high local-debug defaults: `1000` users, `500` person
channels, `500` group channels, `10` members per group, `0.25/s` per channel,
`128` traffic concurrency, and `WK_SIM_VERIFY_RECV=none`. That targets roughly
`250` ingress messages per second before group fanout and avoids receive checks
throttling local send pressure. Override the same `WK_SIM_*` variables to lower
or raise the workload; use `WK_SIM_VERIFY_RECV=sampled` when sampled receive
verification is needed.

The simulator stays within wkbench's black-box boundary: it prepares data through `/bench/v1/*` and sends messages through WKProto gateways. It does not import server internals or bypass cluster paths.

The opt-in e2e smoke starts a real three-node cluster and checks that `dev-sim`
reaches `running` with connected users and non-zero traffic:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/bench/devsim_smoke -count=1
```

## Example `target.yaml`

```yaml
name: local-single-node-cluster
api:
  addrs:
    - http://127.0.0.1:5001
gateway:
  tcp:
    addrs:
      - 127.0.0.1:5100
bench_api:
  enabled: true
  addrs:
    - http://127.0.0.1:5001
  token: bench-secret
metrics:
  enabled: false
  addrs: []
```

`bench_api.addrs` is optional. When it is empty, wkbench uses `api.addrs` for bench API requests.

## Example `workers.yaml`

```yaml
workers:
  - id: worker-a
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: worker-secret
```

For local experiments only, a worker may be started with `--insecure-control`; then set `insecure_control: true` and omit `control_token` in `workers.yaml`.

## Example `scenario.yaml`

```yaml
version: wkbench/v1
run:
  id: smoke-001
  warmup: 1s
  duration: 5s
  cooldown: 1s
  fail_fast: true
  report_dir: ./tmp/wkbench-reports/smoke-001
identity:
  uid_prefix: bench-u
  device_prefix: bench-d
  client_msg_prefix: bench-msg
  token:
    mode: bench_api
online:
  total_users: 100
  connect_rate: 50/s
  gateway_balance: round_robin
channels:
  profiles:
    - name: person-chat
      channel_type: person
      count: 10
    - name: small-group
      channel_type: group
      count: 5
      members:
        count: 20
        overlap: allowed
      online:
        member_ratio: 1
      shard:
        mode: hash
      prepare:
        subscribers_batch_size: 1000
messages:
  payload:
    size_bytes: 128
    mode: deterministic
  traffic:
    - name: person-send
      channel_ref: person-chat
      rate_per_channel: 1/s
      recv_ack: true
      verify:
        recv:
          mode: full
    - name: group-send
      channel_ref: small-group
      rate_per_channel: 1/s
      recv_ack: true
      verify:
        recv:
          mode: sampled
          sample_size_per_message: 1
limits:
  hard:
    max_worker_failed: 0
    max_connect_error_rate: 0
    max_sendack_error_rate: 0
    max_recv_verify_error_rate: 0
cleanup:
  enabled: false
```

Durations use Go duration syntax such as `500ms`, `1s`, or `2m`. Rates use `<number>/s`, for example `100/s` or `12.5/s`.

## Scenario Notes

- `online.total_users` is the generated identity pool. Person channels reserve two users per channel. Group member ranges may share this pool when `members.overlap: allowed`.
- Person profiles generate deterministic one-to-one channel IDs from the sender and recipient UID pair.
- Group profiles support normal hashed channel ranges and the `split_members_and_traffic` mode for one very large group. In split mode, exactly one logical group channel is split across workers by member ranges and traffic partitions.
- Multiple `messages.traffic` entries may target the same channel profile. Each traffic entry uses its own `rate_per_channel`.
- `verify.recv.mode: full` verifies every expected recipient in the workload shard. `sampled` verifies a deterministic recipient sample. Empty mode disables receive verification.
- `identity.token.mode: bench_api` prepares benchmark user tokens through `/bench/v1/users/tokens`. Empty or `none` uses empty tokens.

## Report Output

When `run.report_dir` is set, wkbench writes a report directory containing:

- `scenario.yaml`, `target.yaml`, and `workers.yaml`: copied effective inputs.
- `plan.json`: deterministic worker assignment.
- `report.json`: machine-readable verdict, summary, limits, metrics, and errors.
- `summary.md`: human-readable summary.
- `workers/`: raw worker reports.
- `metrics/` and `errors/`: jsonl details for metrics and error samples.

## Development Checks

Useful commands while changing wkbench:

```bash
GOWORK=off go test ./internal/bench/... ./cmd/wkbench -count=1
GOWORK=off go test ./cmd/wkbench -run TestWkbenchDoesNotImportServerInternals -count=1
GOWORK=off go test -tags=e2e ./test/e2e/bench/wkbench_smoke -count=1
```

Keep `cmd/wkbench` as a thin CLI over `internal/bench`. Do not import WuKongIM server internals into wkbench; use `internal/bench/target` and WKProto clients as black-box boundaries.
