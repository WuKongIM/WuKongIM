# wkbench Black-Box Load Test CLI Design

Date: 2026-05-14
Status: Reviewed design draft
Scope: External black-box load testing for WuKongIM clusters

## 1. Goals

Build a standalone `wkbench` CLI that lets users deploy a WuKongIM single-node cluster or multi-node cluster, start one or more load-generator workers, and run real black-box load tests through public interfaces.

The first version focuses on:

- Real WKProto TCP connections for online-user pressure.
- Real WKProto send, recv, and recvack message paths.
- Person channels and group channels only.
- Multiple group profiles in one run, for example 50 groups with 100 members plus 1 group with 10,000 members.
- Per-profile message rates, verification rules, and online ratios.
- Distributed load generation through manually started workers.
- Required bench-only data-preparation APIs; if the target cluster has not enabled bench mode, `wkbench` fails preflight and does not start load.

The design must preserve the project deployment semantics: a single-node deployment is still treated as a single-node cluster, and the load tester must not introduce shortcuts that bypass cluster behavior.

## 2. Non-Goals

Version 1 does not:

- Import `internal/app` or call node-local internals.
- Directly read or write databases.
- Depend on `/manager/*` APIs, manager usernames, or manager passwords.
- Test every channel type. The schema leaves room for more channel types, but v1 only enables `person` and `group`.
- Provide SSH, Kubernetes, or binary distribution orchestration for workers.
- Delete benchmark data by default.
- Send benchmark traffic through HTTP message APIs. Message pressure uses WKProto.

## 3. Architecture

`wkbench` is a separate binary, not a mode inside `cmd/wukongim`.

```text
scenario.yaml + target.yaml + workers.yaml
        |
        v
wkbench run / coordinator
        |
        | plan shards, control phases, aggregate metrics
        v
+-------------------+    +-------------------+    +-------------------+
| wkbench worker A  |    | wkbench worker B  |    | wkbench worker C  |
| connections       |    | connections       |    | connections       |
| channel shards    |    | channel shards    |    | channel shards    |
| message traffic   |    | message traffic   |    | message traffic   |
+---------+---------+    +---------+---------+    +---------+---------+
          |                        |                        |
          | HTTP API / Bench API / WKProto TCP / metrics     |
          v                        v                        v
                 WuKongIM single-node or multi-node cluster
```

### 3.1 Coordinator

The coordinator is normally started by:

```bash
wkbench run --target target.yaml --scenario scenario.yaml --workers workers.yaml
```

It owns:

- Config loading and validation.
- Target and worker preflight checks.
- Global plan compilation.
- Worker shard assignment.
- Phase orchestration.
- Metrics aggregation.
- Final report generation.

The coordinator does not produce business load directly.

### 3.2 Worker

Users manually start workers on load-generator machines:

```bash
wkbench worker --listen 192.168.10.21:19090 --work-dir ./wkbench-data --control-token "$WK_BENCH_WORKER_TOKEN"
```

Each worker owns only the shard assigned by the coordinator. It creates data through target HTTP or bench APIs, opens real WKProto connections, runs message traffic, verifies recv behavior, and reports aggregated metrics.

### 3.3 Target Cluster

The target is a user-managed WuKongIM cluster. `wkbench` talks to it only through:

- Public API server routes such as `/healthz` and `/readyz`.
- Required bench-only routes under `/bench/v1/*` for benchmark data setup and lightweight benchmark snapshots.
- WKProto TCP gateway listeners.
- Optional public `/metrics` endpoints.

`wkbench` does not depend on manager APIs.

## 4. Command Model

```bash
wkbench run --target target.yaml --scenario scenario.yaml --workers workers.yaml
wkbench worker --listen 192.168.10.21:19090 --work-dir ./wkbench-data --control-token "$WK_BENCH_WORKER_TOKEN"
wkbench validate --target target.yaml --scenario scenario.yaml --workers workers.yaml
wkbench doctor --target target.yaml --workers workers.yaml
wkbench report --run-dir ./runs/bench-20260514-001
```

### 4.1 `wkbench run`

Runs the coordinator. It validates config, checks workers and target endpoints, assigns shards, drives phases, streams progress, and writes reports.

### 4.2 `wkbench worker`

Starts a worker control server. It exposes phase-control APIs to the coordinator and maintains local state in `--work-dir`.

### 4.3 `wkbench validate`

Checks config without producing load. It estimates connections, channel count, subscriber relationships, message rate, fanout rate, and per-worker resource pressure.

### 4.4 `wkbench doctor`

Checks operational readiness:

- API health and readiness.
- Gateway WKProto handshake.
- Optional metrics endpoint access.
- Optional bench API capabilities.
- Worker version and resources.
- Worker-to-target network latency.

### 4.5 `wkbench report`

Reads an existing run directory and renders a report summary or converts report formats.

## 5. Config Files

The config is split by concern.

### 5.1 `target.yaml`

```yaml
name: prod-like-3node

api:
  addrs:
    - http://10.0.1.11:5001
    - http://10.0.1.12:5001
    - http://10.0.1.13:5001

gateway:
  tcp:
    addrs:
      - 10.0.1.11:5100
      - 10.0.1.12:5100
      - 10.0.1.13:5100

metrics:
  enabled: true
  addrs:
    - http://10.0.1.11:5001/metrics
    - http://10.0.1.12:5001/metrics
    - http://10.0.1.13:5001/metrics

bench_api:
  enabled: true
```

There is no manager section in v1.

### 5.2 `workers.yaml`

```yaml
workers:
  - id: bench-a
    addr: http://192.168.10.21:19090
    weight: 1
    control_token: "${WK_BENCH_WORKER_TOKEN}"
    tags: [zone-a]
  - id: bench-b
    addr: http://192.168.10.22:19090
    weight: 1
    control_token: "${WK_BENCH_WORKER_TOKEN}"
    tags: [zone-b]
  - id: bench-c
    addr: http://192.168.10.23:19090
    weight: 2
    control_token: "${WK_BENCH_WORKER_TOKEN}"
    tags: [zone-c]
```

Weights control plan partitioning.

### 5.3 `scenario.yaml`

```yaml
version: wkbench/v1

run:
  id: bench-20260514-001
  duration: 30m
  warmup: 5m
  cooldown: 2m
  random_seed: 20260514
  fail_fast: false
  report_dir: ./runs/bench-20260514-001

limits:
  fail_on_soft: false
  hard:
    max_worker_failed: 0
    max_connect_error_rate: 0.001
    max_sendack_error_rate: 0.001
    max_recv_verify_error_rate: 0.001
  soft:
    max_sendack_p99: 2s
    max_recv_p99: 3s

prepare:
  concurrency: 200
  rate_limit: 1000/s
  retry:
    max_attempts: 3
    backoff: 200ms

identity:
  uid_prefix: bench-20260514-001-u
  device_prefix: bench-20260514-001-d
  client_msg_prefix: bench-20260514-001-msg
  token:
    mode: none

online:
  total_users: 1000000
  connect_rate: 5000/s
  gateway_balance: round_robin
  reconnect:
    enabled: true
    max_attempts: 3
    backoff: 1s
  heartbeat:
    enabled: true
    interval: 30s

channels:
  profiles:
    - name: person-chat
      channel_type: person
      count: 500000
      participants:
        pick: sequential
      online:
        sender_ratio: 1.0
        recipient_ratio: 1.0

    - name: small-groups
      channel_type: group
      count: 50
      members:
        count: 100
        pick: deterministic_hash
        overlap: allowed
      online:
        member_ratio: 1.0
      prepare:
        subscribers_batch_size: 500

    - name: huge-group
      channel_type: group
      count: 1
      members:
        count: 10000
        pick: contiguous_range
        overlap: allowed
      online:
        member_ratio: 0.8
      shard:
        mode: split_members_and_traffic
      prepare:
        subscribers_batch_size: 1000

messages:
  payload:
    size_bytes: 256
    mode: deterministic

  traffic:
    - name: person-normal
      channel_ref: person-chat
      rate_per_channel: 0.02/s
      sender_pick: alternate
      recv_ack: true
      verify:
        recv:
          mode: full

    - name: small-group-normal
      channel_ref: small-groups
      rate_per_channel: 0.2/s
      sender_pick: member
      recv_ack: true
      verify:
        recv:
          mode: full

    - name: huge-group-burst
      channel_ref: huge-group
      rate_per_channel: 20/s
      sender_pick: member
      recv_ack: true
      verify:
        recv:
          mode: sampled
          sample_size_per_message: 50

cleanup:
  enabled: false
  strategy: keep_data
```

### 5.4 Bench API Requirement

`wkbench` uses one benchmark data setup interface: `/bench/v1/*`. It does not switch between production data-preparation routes and bench routes.

Rules:

- `target.bench_api.enabled` must be true in `target.yaml`.
- Preflight must call `/bench/v1/capabilities` on the target API nodes.
- If capabilities are missing, unsupported, or return 404 because `WK_BENCH_API_ENABLE=false`, `wkbench run` fails before prepare/connect/warmup/run.
- Existing production routes such as `/channel`, `/channel/subscriber_add`, and `/user/token` are not used by `wkbench` v1 for benchmark data setup.
- Formal load still uses real WKProto. Bench APIs prepare data only; they do not send benchmark messages.

Profile rates are defined by one or more `messages.traffic` entries referencing the profile through `channel_ref`. Do not add competing rate fields directly under `channels.profiles`.

`rate_per_channel` is always a global logical-channel rate before sharding. The coordinator compiles worker-local schedules from that global budget. For a split huge group, worker local rate is proportional to owned traffic partitions, for example `worker_rate = rate_per_channel * owned_partitions / partition_count`. Workers must not each emit the full `rate_per_channel`.

## 6. Channel Profile Semantics

The schema is profile-based so later channel types can be added without changing the CLI shape. v1 enables only `person` and `group`.

### 6.1 Global Identity Pool

The planner builds a deterministic UID pool before it expands profiles.

Rules:

- `online.total_users` is the default shared UID pool size and the maximum target online sessions for the run.
- UIDs are generated as `identity.uid_prefix` plus a zero-padded global UID index.
- Person profiles consume pairs from the shared pool by default. `person.count=500000` requires 1,000,000 distinct participant slots unless a future profile explicitly enables user reuse.
- Group profiles select members from the same shared pool by default. This intentionally allows a user to participate in both person and group workloads unless a profile sets `members.overlap=disallowed`.
- `members.overlap=allowed` permits overlap across channels and profiles. `members.overlap=disallowed` requires the planner to reserve disjoint UID ranges and fail validation if the pool is too small.
- Online ratios select from the profile's participant set. They do not create additional UIDs.
- Token preparation deduplicates by UID after all profiles are expanded.

The planner must output the resolved UID ranges and reuse policy into `plan.json` so connection count, token count, and recv verification are reproducible.

### 6.2 Person

A `person` profile models one-to-one channels.

Rules:

- `count` means person conversation count, not user count.
- Each conversation maps to two UIDs.
- The sender sends `SendPacket{ChannelType: person, ChannelID: recipientUID}`.
- The server normalizes the internal person channel ID; `wkbench` does not depend on the internal normalized ID.
- Recv verification checks `FromUID`, `ChannelType`, payload marker, and message identity.
- `sender_pick: alternate` makes both sides send over time.
- `sender_ratio` and `recipient_ratio` control online/offline combinations.

Person profiles do not create channels or subscribers through HTTP.

### 6.3 Group

A `group` profile models subscriber-based group channels.

Rules:

- `count` is group channel count.
- `members.count` is member count per channel.
- Data preparation uses `/bench/v1/channels` and `/bench/v1/channels/subscribers`.
- Senders are selected from members.
- The sender sends `SendPacket{ChannelType: group, ChannelID: groupID}`.
- Small groups can use full recv verification.
- Large groups should use sampled verification to avoid making the load generator the bottleneck.

Supported member-pick strategies in v1:

- `contiguous_range`: deterministic contiguous UID slices, suitable for huge groups.
- `deterministic_hash`: stable hash from run/profile/channel/member indexes into the UID pool.

`random_without_replacement` can be added later.

## 7. Planning and Sharding

The coordinator compiles config into a deterministic `RunPlan` and assigns shards to workers.

### 7.1 Person Shards

For `person-chat count=500000` and worker weights `1:1:2`:

```text
bench-a: pair [0, 125000)
bench-b: pair [125000, 250000)
bench-c: pair [250000, 500000)
```

The worker owns connections, sends, and verification for its pair range.

### 7.2 Many-Group Shards

For `small-groups count=50` and worker weights `1:1:2`:

```text
bench-a: channel [0, 13)
bench-b: channel [13, 25)
bench-c: channel [25, 50)
```

The worker creates those channels, prepares subscribers, opens assigned member connections, and sends traffic.

### 7.3 Huge-Group Shards

For `huge-group count=1 members=10000` and worker weights `1:1:2`:

```text
bench-a:
  channel_range: [0, 1)
  member_range: [0, 2500)
  traffic_partition: 0/4

bench-b:
  channel_range: [0, 1)
  member_range: [2500, 5000)
  traffic_partition: 1/4

bench-c:
  channel_range: [0, 1)
  member_range: [5000, 10000)
  traffic_partition: 2-3/4
```

Only one assigned channel owner calls `/bench/v1/channels`. All workers can add their subscriber batches through `/bench/v1/channels/subscribers`. Traffic is partitioned by message index, for example `message_index % partition_count`, and the coordinator divides the configured global channel rate across those partitions.

Huge-group prepare ownership rules:

- The channel owner is chosen deterministically from the weighted worker list by `hash(run_id, profile, channel_index)`.
- Channel upsert must be idempotent. If the owner retries after a timeout and the channel already exists, prepare continues.
- Workers do not start subscriber batches for a huge group until the coordinator observes the owner reached `channel_prepared` for that channel.
- Each subscriber batch has a deterministic batch ID derived from `run_id`, profile, channel index, worker ID, and member range. Retrying the same batch must be safe.
- If the owner fails before `channel_prepared`, the coordinator either fails the run when `fail_fast=true` or reassigns ownership to the next deterministic worker and records the ownership change in `plan.json`.
- If a non-owner fails during subscriber preparation, its member range remains incomplete and the run is failed or degraded according to `fail_fast`; other workers must not silently claim that range unless the coordinator explicitly reassigns it.

## 8. Run Phases

A run moves through fixed phases:

```text
preflight -> plan -> prepare -> connect -> warmup -> run -> cooldown/report
```

### 8.1 Preflight

Checks:

- API `/healthz` and `/readyz`.
- Gateway TCP and one WKProto handshake.
- Optional `/metrics` access.
- Required `/bench/v1/capabilities` access.
- Worker availability, version, and resource capacity.
- Scenario capacity estimates.

If bench API capabilities are unavailable or incomplete, preflight fails and no load is started.

### 8.2 Plan

Compiles global profiles and traffic into deterministic worker shards.

### 8.3 Prepare

Prepares data through required bench APIs.

- Person: optional token preparation only.
- Group: channel upsert and subscriber add.

### 8.4 Connect

Workers establish real WKProto TCP sessions at the configured rate.

Gateway balance modes:

- `round_robin`
- `hash_uid`
- `weighted`

### 8.5 Warmup

Runs low-rate traffic to activate channel runtime metadata, delivery tags, message paths, and connection read loops. Warmup metrics are recorded separately and do not count toward final limits.

### 8.6 Run

Runs formal traffic and evaluates configured hard and soft limits.

### 8.7 Cooldown and Report

Stops new sends, waits for inflight sendack/recv verification to settle, gathers final metrics, and writes reports.

## 9. Worker Runtime

Each worker contains:

```text
worker server
  ├─ assignment store
  ├─ prepare engine
  ├─ connection manager
  ├─ traffic scheduler
  ├─ recv verifier
  └─ metrics aggregator
```

### 9.1 Assignment Store

Persists current run metadata in `--work-dir`, prevents concurrent conflicting assignments, and supports idempotent retry for the same run ID.

### 9.2 Prepare Engine

Executes `/bench/v1/*` data setup with concurrency, rate limits, retry, and bounded error samples.

### 9.3 Connection Manager

Maintains real WKProto clients, including connect, read loop, write path, ping/heartbeat, reconnect, and metrics hooks.

The production bench client should be implemented outside `test/e2e/suite` because that package is build-tagged for e2e tests. It may reuse the same protocol concepts and public protocol packages.

### 9.4 Traffic Scheduler

Generates sends from assigned traffic plans using deterministic randomness. v1 supports worker-local schedules derived from global `rate_per_channel` budgets and can later add global-rate and burst modes.

### 9.5 Recv Verifier

Verifies received packets according to profile and traffic rules, sends recvack when enabled, and records mismatches as bounded error samples.

### 9.6 Metrics Aggregator

Aggregates worker-local metrics and reports periodic deltas. It must not use UID, channel ID, message ID, or client message number as metric labels.

## 10. Coordinator/Worker Control API

Use HTTP+JSON for v1.

```text
GET  /healthz
GET  /v1/info
POST /v1/assign
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

### 10.1 Worker Control Security

Target `/bench/v1/*` APIs are intentionally unauthenticated when enabled, but coordinator-to-worker control is a separate trust boundary. A worker can start large outbound traffic, stop a run, and expose reports, so the control API must not be open by default.

Worker control rules:

- `wkbench worker` requires a shared `--control-token` unless the user passes an explicit `--insecure-control` flag.
- Coordinator requests include `Authorization: Bearer <control-token>`.
- `wkbench doctor` verifies the token before a run can be assigned.
- Workers should bind to a private address by default in documentation examples; `0.0.0.0` examples must mention firewall isolation.
- Use `--listen 0.0.0.0:19090` only on a private benchmark network or behind firewall rules that allow coordinator hosts only.
- If `--insecure-control` is used, the worker includes that fact in `/v1/info`, and the coordinator prints a warning in preflight and the final report.

Worker phase state is monotonic:

```text
idle
  -> assigned
  -> preparing
  -> prepared
  -> connecting
  -> connected
  -> warming
  -> warmed
  -> running
  -> cooling
  -> completed

any active phase -> stopping -> stopped
any active phase -> failed
```

A worker must reject a different active `run_id` unless explicitly forced.

## 11. Metrics and Reports

Client-side worker metrics are primary. Target metrics are optional supporting evidence.

### 11.1 Worker Counters

```text
connect_attempt_total
connect_success_total
connect_error_total
disconnect_total
reconnect_total

send_attempt_total
send_write_error_total
sendack_success_total
sendack_error_total
sendack_timeout_total

recv_total
recv_timeout_total
recv_verify_success_total
recv_verify_error_total
recvack_total
recvack_error_total

http_prepare_attempt_total
http_prepare_success_total
http_prepare_error_total

bytes_sent_total
bytes_recv_total
```

### 11.2 Worker Histograms

```text
connect_latency
sendack_latency
recv_latency
recvack_write_latency
http_prepare_latency
message_roundtrip_latency
```

### 11.3 Worker Gauges

```text
active_connections
target_connections
inflight_sends
inflight_recv_verifications
traffic_scheduler_lag
worker_cpu_percent
worker_memory_bytes
worker_fd_used
```

Allowed labels:

```text
worker_id
phase
channel_type
profile
traffic
error_kind
reason_code
```

`run_id` belongs in JSON reports, logs, and local files. It is not a default metric label because repeated timestamped runs would create unbounded time-series churn. If a future Prometheus exporter needs a run label, it must be opt-in and documented as short-lived.

Forbidden labels:

```text
uid
channel_id
client_msg_no
message_id
```

### 11.4 Error Kinds

Use stable error categories:

```text
config_invalid
preflight_target_unreachable
preflight_worker_unreachable
preflight_resource_insufficient

prepare_http_error
prepare_bad_response
prepare_bench_api_disabled
prepare_rate_limited

connect_dial_error
connect_timeout
connect_auth_failed
connect_protocol_error

send_write_error
sendack_timeout
sendack_reason_error
sendack_mismatch

recv_timeout
recv_verify_mismatch
recv_unexpected_packet
recvack_write_error

worker_resource_exhausted
worker_phase_conflict
worker_internal_error
coordinator_worker_rpc_error
target_metrics_error
```

### 11.5 Run Directory

```text
runs/
  bench-20260514-001/
    scenario.yaml
    target.yaml
    workers.yaml
    plan.json
    summary.md
    report.json
    coordinator.log
    workers/
      bench-a.report.json
      bench-b.report.json
      bench-c.report.json
    metrics/
      worker-1s.jsonl
      target-snapshots.jsonl
    errors/
      samples.jsonl
```

### 11.6 Limit Evaluation

Hard limits determine the run verdict and exit status. If any hard limit fails, `wkbench run` exits with code 3 after cooldown and report generation.

Soft limits produce warnings by default and do not change the exit status. If `limits.fail_on_soft=true`, soft-limit failures are promoted to hard-limit failures and use exit code 3.

The final report records each limit as `passed`, `warning`, or `failed` with the configured value and observed value.

### 11.7 Exit Codes

```text
0  success
1  config validation failed
2  preflight failed
3  run completed but hard limits failed
4  worker failed or unreachable during run
5  target cluster became unavailable
6  internal wkbench error
```

## 12. Bench API

`wkbench` v1 requires bench APIs for benchmark data setup and lightweight target snapshots. `WK_BENCH_API_ENABLE=true` is the only v1 bench API mode switch; when it is false, `/bench/v1/*` routes are not registered and `wkbench run` must fail preflight instead of falling back to production data-preparation routes.

Bench APIs are unauthenticated by design to avoid manager-account complexity, but they are disabled by default and must only be enabled in isolated benchmark environments.

### 12.1 Config

Add service config:

```conf
# Enables unauthenticated bench-only HTTP APIs for black-box load tests.
# Keep this disabled outside isolated benchmark environments because these
# endpoints can create large amounts of benchmark data and expose aggregate
# benchmark runtime state.
WK_BENCH_API_ENABLE=false

# Maximum number of top-level records accepted by one bench API request.
WK_BENCH_API_MAX_BATCH_SIZE=10000

# Maximum HTTP request body size accepted by bench APIs.
WK_BENCH_API_MAX_PAYLOAD_BYTES=10485760
```

When disabled, `/bench/v1/*` routes are not registered and return 404.

### 12.2 Capabilities

```text
GET /bench/v1/capabilities
```

```json
{
  "enabled": true,
  "version": "bench/v1",
  "supports": {
    "users_tokens_batch": true,
    "channels_batch": true,
    "channel_subscribers_batch": true,
    "snapshot": true,
    "channel_types": ["group"]
  },
  "limits": {
    "max_batch_size": 10000,
    "max_payload_bytes": 10485760
  }
}
```

### 12.3 Batch Tokens

```text
POST /bench/v1/users/tokens
```

Used only when token mode requires prepared user tokens.

Request:

```json
{
  "run_id": "bench-20260514-001",
  "batch_id": "bench-20260514-001-users-000001",
  "upsert": true,
  "users": [
    {
      "uid": "bench-20260514-001-u-0000000001",
      "token": "bench-token",
      "device_flag": 0,
      "device_level": 1
    }
  ]
}
```

Response:

```json
{
  "status": "ok",
  "accepted": 1,
  "created_or_updated": 1,
  "skipped": 0,
  "errors": []
}
```

Semantics:

- `run_id` and `batch_id` are required.
- The same `batch_id` with the same body is idempotent and retry-safe.
- The same `batch_id` with a different body returns `409 conflict`.
- `errors` contains bounded per-item errors; the endpoint returns non-2xx when any required item fails.

### 12.4 Batch Channels

```text
POST /bench/v1/channels
```

v1 supports only `channel_type=2` group channels. Person channels are not created.

Request:

```json
{
  "run_id": "bench-20260514-001",
  "batch_id": "bench-20260514-001-channels-small-000001",
  "upsert": true,
  "channels": [
    {
      "channel_id": "bench-20260514-001-g-small-000001",
      "channel_type": 2,
      "large": 0,
      "ban": 0,
      "disband": 0,
      "send_ban": 0,
      "allow_stranger": 0
    }
  ]
}
```

Response:

```json
{
  "status": "ok",
  "accepted": 1,
  "created_or_updated": 1,
  "skipped": 0,
  "errors": []
}
```

Semantics:

- `run_id` and `batch_id` are required.
- Channel IDs should include the run prefix so repeated runs do not collide accidentally.
- Upsert is idempotent and retry-safe.
- Unsupported `channel_type` returns `400 bad_request`.

### 12.5 Batch Subscribers

```text
POST /bench/v1/channels/subscribers
```

v1 supports only group subscribers. `reset=false` is the only accepted value so multiple workers can add disjoint member batches to the same huge group without a destructive reset race.

Request:

```json
{
  "run_id": "bench-20260514-001",
  "batch_id": "bench-20260514-001-subs-huge-000000-bench-a-000001",
  "items": [
    {
      "channel_id": "bench-20260514-001-g-huge-000000",
      "channel_type": 2,
      "reset": false,
      "subscribers": [
        "bench-20260514-001-u-0000000001",
        "bench-20260514-001-u-0000000002"
      ]
    }
  ]
}
```

Response:

```json
{
  "status": "ok",
  "accepted_channels": 1,
  "accepted_subscribers": 2,
  "created_or_updated": 2,
  "skipped": 0,
  "errors": []
}
```

Semantics:

- `run_id` and `batch_id` are required.
- `reset=false` is required. `reset=true` is rejected by bench/v1 with `400 bad_request`; destructive subscriber replacement can be introduced later as a separate explicitly named endpoint if needed.
- Re-adding an existing subscriber is success and increments `skipped` or `created_or_updated` according to implementation detail, but must not fail the batch.
- Partial success should be avoided. If a batch cannot be applied atomically, the response must include per-item errors and the worker must retry only deterministic failed batch IDs.

Common error envelope:

```json
{
  "status": "error",
  "error": "bad_request",
  "message": "unsupported channel_type",
  "items": [
    {
      "index": 0,
      "error": "unsupported_channel_type",
      "message": "bench/v1 subscribers supports group only"
    }
  ]
}
```

### 12.6 Snapshot

```text
GET /bench/v1/snapshot
```

Returns aggregate benchmark-related runtime state, not manager-style details:

```json
{
  "node_id": 1,
  "generated_at": "2026-05-14T10:00:00Z",
  "gateway": {
    "active_connections": 250000,
    "connections_by_listener": {
      "tcp-wkproto": 250000
    }
  },
  "runtime": {
    "active_channel_runtime_count": 12000
  },
  "bench": {
    "api_enabled": true,
    "max_batch_size": 10000,
    "max_payload_bytes": 10485760
  }
}
```

If a field is unavailable, capabilities should report it as unsupported and the response can omit it.

### 12.7 Placement

Suggested packages:

```text
internal/access/api/bench.go
internal/access/api/bench_test.go
internal/usecase/benchdata/
internal/app/config.go
cmd/wukongim/config.go
wukongim.conf.example
```

`internal/access/api` adapts HTTP DTOs. `internal/usecase/benchdata` owns reusable bench data preparation logic. `internal/app` remains the composition root.

## 13. Suggested Code Organization for wkbench

```text
cmd/
  wkbench/
    main.go

internal/
  bench/
    cli/
    config/
    model/
    planner/
    coordinator/
    worker/
    target/
    workload/
    metrics/
    report/
```

### 13.1 Black-Box Import Boundary

Because `wkbench` lives in the same Go module, import boundaries must be enforced by tests and review. The CLI and `internal/bench/*` packages are external-load-generator code, not server composition code.

Allowed imports for `cmd/wkbench` and `internal/bench/*`:

- Go standard library.
- Approved third-party CLI/config/HTTP libraries.
- `internal/bench/*`.
- Public protocol packages needed to speak the real wire protocol, especially `pkg/protocol/frame`, `pkg/protocol/codec`, and `pkg/protocol/jsonrpc` if WebSocket support is added later.

Forbidden imports:

- `internal/app`
- `internal/access/*`
- `internal/usecase/*`
- `internal/runtime/*`
- `internal/gateway/*`; if WKProto client crypto helpers are needed, first extract a minimal protocol-safe helper into a public package instead of importing gateway internals.
- `pkg/slot/*`
- `pkg/controller/*`
- `pkg/cluster/*`
- storage, Raft, and metadata internals.

Add a boundary test that runs `go list -deps ./cmd/wkbench` and fails if any forbidden import appears. This test is part of the acceptance criteria because it prevents the CLI from becoming an in-repo shortcut instead of a black-box client.

`internal/bench/target` may use public protocol packages:

```text
pkg/protocol/frame
pkg/protocol/codec
```

It must not use forbidden server, cluster, storage, or runtime packages.

## 14. Test Strategy

### 14.1 CLI Unit Tests

- Scenario parsing and validation.
- Target and worker config validation.
- Person and group profile validation.
- Worker-weight sharding.
- Huge-group member and traffic partitioning.
- Rate parsing and payload generation.
- Required bench API capability checks and preflight failure behavior.
- Error classification.
- Report aggregation.
- `wkbench` import-boundary test rejects server internals in `go list -deps ./cmd/wkbench`.

### 14.2 Coordinator/Worker Tests

- Worker phase state machine.
- Idempotent assignment for the same run ID.
- Conflict on different active run ID.
- Control token is required by default and invalid tokens are rejected.
- Coordinator phase progression.
- Partial worker failure behavior with `fail_fast=true/false`.
- Metrics delta aggregation.

### 14.3 WKProto Target Tests

- Connect handshake.
- Send -> sendack for person.
- Send -> sampled recv -> recvack for group.
- Timeout and mismatch classification.

### 14.4 Bench API Server Tests

- Routes are absent when disabled.
- Capabilities route is present when enabled.
- Batch size and payload limits are enforced.
- `run_id` is required for mutating routes.
- `batch_id` is required, idempotent for identical retries, and conflicts on changed bodies.
- Batch channels reject unsupported channel types.
- Batch subscribers are idempotent and accept only `reset=false`.
- Batch subscribers reject `reset=true`.

### 14.5 Focused Integration Tests

Use small scales only:

- One worker against one single-node cluster.
- Person profile with a few pairs.
- Group profile with a few groups.
- Mixed small group plus one larger group with sampled verification.
- Bench API enabled path and disabled-target preflight failure path.

Do not put large real-time load tests into default unit tests.

## 15. Implementation Phases

### Phase 1: Spec and Skeleton

- Add `cmd/wkbench`.
- Add `internal/bench/*` skeleton packages.
- Implement config parsing, validation, and `doctor` scaffolding.
- No real load yet.

### Phase 2: Worker and Coordinator Control Plane

- Implement worker HTTP server.
- Implement coordinator assignment and phase orchestration.
- Use fake workload to verify distributed control and reporting.

### Phase 3: Required Bench API and Person Workload

- Add `WK_BENCH_API_ENABLE` and bench API limits.
- Add bench capabilities, batch token, batch channel, batch subscriber, and snapshot routes.
- Make wkbench fail preflight when required bench API capabilities are unavailable.
- Implement production bench WKProto client.
- Implement connection manager.
- Implement person profile traffic and verification.

### Phase 4: Group Workload Through Bench APIs

- Implement group data preparation using `/bench/v1/channels` and `/bench/v1/channels/subscribers`.
- Implement group profile traffic.
- Implement huge-group member and traffic sharding.
- Implement full and sampled recv verification.

### Phase 5: Reporting and Operational Hardening

- Finalize report formats and limit evaluation.
- Add worker resource warnings and operator-facing diagnostics.
- Add focused integration coverage for disabled bench API preflight failure.

## 16. Post-v1 Questions

These questions are intentionally outside the v1 implementation contract:

- Should `wkbench worker` expose Prometheus metrics directly in addition to JSON metrics for the coordinator?
- Should a later version support WebSocket JSON-RPC, or should WKProto TCP remain the only transport?
- Should cleanup remain only `keep_data`, or should a best-effort run-prefix cleanup be added later under bench mode?
- What default `ulimit` safety factor should make preflight fail versus warn after real-world calibration?

## 17. Acceptance Criteria

v1 is acceptable when:

- A user can run one worker against a single-node cluster.
- A user can run multiple workers against a multi-node cluster.
- Person and group profiles work in the same run.
- Multiple group sizes work in the same run.
- Each profile can define its own message rate.
- All message pressure uses real WKProto.
- Data preparation uses `/bench/v1/*`; no production data-preparation fallback exists in v1.
- `WK_BENCH_API_ENABLE=false` or missing capabilities make `wkbench run` fail before starting load.
- Bench APIs are disabled by default on the server, unauthenticated when enabled, and do not depend on manager APIs.
- Worker control APIs require a control token unless explicitly started with insecure control mode.
- `wkbench` import-boundary tests prevent imports of server internals and cluster/storage shortcuts.
- Reports include overall, by-worker, and by-profile connection count, QPS, sendack latency, recv latency, error rate, and bounded error samples.
