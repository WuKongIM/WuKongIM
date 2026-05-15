# Compose Dev Simulator Design

Date: 2026-05-15
Status: Approved design draft
Scope: Optional Docker Compose development simulator for WuKongIM clusters

## 1. Goal

Add an optional simulator service to the local Docker Compose development cluster. The simulator keeps a small, recognizable set of users online and continuously sends low-rate person and group messages through public WuKongIM interfaces, so developers can debug gateway sessions, message flow, delivery, metrics, and UI behavior without manually running ad-hoc clients.

The simulator must preserve cluster semantics: it talks to the running WuKongIM cluster through HTTP readiness/bench APIs and WKProto gateway listeners. It must not import server internals, bypass Raft/slot/channel paths, or create a separate non-cluster branch.

## 2. Non-Goals

- It is not a production traffic generator.
- It is not a replacement for benchmark reports or large-scale `wkbench run` tests.
- It does not start by default with plain `docker compose up`.
- It does not use Manager APIs or manager credentials.
- It does not write directly to Pebble, internal stores, or node-local runtime objects.
- It does not clean all previous simulator data in v1; developers can change the run/user prefix or reset Docker volumes when they want a clean slate.

## 3. User Experience

Default cluster startup remains unchanged:

```bash
docker compose up -d
```

The simulator starts only when the developer enables the profile:

```bash
docker compose --profile dev-sim up -d
```

The simulator runs as a long-lived service named `wk-sim`. It waits for `wk-node1`, `wk-node2`, and `wk-node3` to become ready, prepares a small fixture through `/bench/v1/*`, opens WKProto sessions, and emits steady low-rate traffic.

A lightweight status endpoint is exposed on the host for debugging:

```bash
curl http://127.0.0.1:19091/healthz
curl http://127.0.0.1:19091/status
```

## 4. Recommended Architecture

Reuse `wkbench` instead of creating a separate simulator binary.

```text
docker compose --profile dev-sim up
        |
        v
wk-sim container
        |
        v
wkbench dev-sim --config /etc/wkbench/dev-sim.yaml
        |
        | wait /healthz + /readyz + /bench/v1/capabilities
        | prepare users/tokens, group channels, subscribers
        | connect WKProto clients
        | loop: warmup/run/cooldown style low-rate person/group traffic
        v
wk-node1 / wk-node2 / wk-node3 through public HTTP + WKProto
```

`cmd/wkbench` gains a new `dev-sim` command. The command is a supervisor around existing `internal/bench` components:

- Load one simulator config file.
- Derive target, worker-like runtime settings, and scenario values.
- Wait until target readiness and bench capabilities are available.
- Build a deterministic one-worker plan in-process.
- Run prepare/connect once.
- Keep users online while repeatedly running low-rate traffic windows until the process receives SIGINT/SIGTERM.
- Expose health/status for developers and Compose healthchecks.

This keeps black-box boundaries intact and avoids duplicating WKProto client logic.

## 5. Compose Service

`docker-compose.yml` adds a service under a profile:

```yaml
wk-sim:
  profiles: ["dev-sim"]
  image: wukongim-dev:local
  build:
    context: .
    dockerfile: Dockerfile
  depends_on:
    - wk-node1
    - wk-node2
    - wk-node3
  command:
    - /usr/local/bin/wkbench
    - dev-sim
    - --config
    - /etc/wkbench/dev-sim.yaml
  volumes:
    - ./docker/sim:/etc/wkbench:ro
    - ./docker/dev-sim:/var/lib/wkbench
  ports:
    - "19091:19091"
```

The profile keeps normal development startup clean. Developers explicitly opt into simulator traffic.

## 6. Simulator Config

Create `docker/sim/dev-sim.yaml` with a small default profile:

```yaml
version: wkbench/dev-sim/v1

status:
  listen: 0.0.0.0:19091

target:
  api_addrs:
    - http://wk-node1:5001
    - http://wk-node2:5001
    - http://wk-node3:5001
  gateway_tcp_addrs:
    - wk-node1:5100
    - wk-node2:5100
    - wk-node3:5100

identity:
  uid_prefix: sim-u
  device_prefix: sim-d
  client_msg_prefix: sim-msg
  token_mode: none

online:
  total_users: 20
  connect_rate: 10/s

profiles:
  person_channels: 5
  group_channels: 2
  group_members: 10

traffic:
  payload_size_bytes: 128
  person_rate_per_channel: 0.2/s
  group_rate_per_channel: 0.2/s
  verify_recv: sampled
  window: 10s
  cooldown: 1s

retry:
  readiness_timeout: 2m
  restart_backoff: 5s
```

Environment overrides allow quick tuning without editing files:

- `WK_SIM_USERS`
- `WK_SIM_PERSON_CHANNELS`
- `WK_SIM_GROUP_CHANNELS`
- `WK_SIM_GROUP_MEMBERS`
- `WK_SIM_RATE`
- `WK_SIM_UID_PREFIX`

The defaults are intentionally small and safe for laptops.

## 7. Command Behavior

`wkbench dev-sim --config docker/sim/dev-sim.yaml` runs until canceled.

Startup sequence:

1. Load config and apply environment overrides.
2. Start status HTTP server immediately with `state=starting`.
3. Poll target health/readiness/capabilities until ready or `readiness_timeout` expires.
4. Prepare deterministic users/channels/subscribers through bench APIs.
5. Connect users to WKProto gateways with round-robin balancing.
6. Enter loop: run person/group traffic windows, then short cooldown.
7. On SIGINT/SIGTERM, stop traffic, close clients, update status to `stopped`, and exit cleanly.

If the target temporarily becomes unavailable during a traffic window, the simulator records the error, closes affected clients, backs off, and repeats readiness/prepare/connect. This makes the service useful during iterative `docker compose restart wk-node*` debugging.

## 8. Status API

The status server is intentionally small:

```text
GET /healthz -> 200 when process is alive
GET /status  -> JSON snapshot
```

Example status:

```json
{
  "state": "running",
  "run_id": "dev-sim-20260515-180000",
  "connected_users": 20,
  "person_channels": 5,
  "group_channels": 2,
  "messages_sent": 42,
  "send_errors": 0,
  "recv_errors": 0,
  "last_error": "",
  "last_transition_at": "2026-05-15T18:00:00+08:00"
}
```

The first version does not need pause/resume/burst endpoints. They can be added later without changing the Compose contract.

## 9. Error Handling

- Config errors exit with code 1.
- Readiness timeout exits with a non-zero code so Compose restart policy can retry.
- Runtime target errors do not immediately exit; the simulator backs off and reconnects.
- Unexpected internal errors are surfaced in `/status.last_error` and logs.
- The simulator uses deterministic prefixes and run IDs so developers can correlate logs with generated traffic.

## 10. Testing Strategy

Unit tests:

- Config loading and environment overrides.
- Dev-sim scenario derivation.
- Status state transitions.
- Supervisor retry behavior using fake target/runner interfaces.
- `cmd/wkbench dev-sim` CLI parsing.

Integration/manual checks:

- `docker compose --profile dev-sim up -d --build`
- `curl http://127.0.0.1:19091/status`
- Confirm simulated users connect and messages appear in node logs/metrics.
- Restart one node and confirm `wk-sim` recovers without developer intervention.

## 11. Documentation

Update:

- `cmd/wkbench/README.md`: describe `dev-sim` and Compose usage.
- `internal/bench/FLOW.md`: add simulator package/command flow if new reusable code is placed under `internal/bench`.
- Optionally add a short `docker/sim/README.md` with common commands and tuning environment variables.

## 12. Open Extension Points

Future versions can add:

- `POST /pause`, `POST /resume`, and `POST /burst`.
- Web UI debug controls.
- Multiple simulator profiles, such as `idle-online`, `person-only`, `group-only`, and `high-fanout`.
- Optional target metrics polling in the status snapshot.
