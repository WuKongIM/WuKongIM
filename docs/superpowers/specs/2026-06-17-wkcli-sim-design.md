# wkcli sim Design

## Goal

Add `wkcli sim`, a long-running operational simulator that drives real `internalv2` / `cmd/wukongimv2` traffic against a running WuKongIM single-node cluster or multi-node cluster.

The simulator is for development and operational observation. It keeps a bounded pool of users online, prepares real benchmark group metadata through the v2 HTTP bench API, sends real WKProto `SEND -> SENDACK` traffic through the gateway, and exposes a local status endpoint for quick triage.

## Non-Goals

- Do not import or wrap `internal/bench/devsim` or any `internal/bench/*` package.
- Do not create a generic benchmark report framework inside `wkcli`.
- Do not add a fake data mode in the first version.
- Do not add personal-channel simulation until the v2 target advertises personal-channel bench support.
- Do not bypass cluster semantics. A single-node deployment is treated as a single-node cluster.

`internal/bench/devsim` is only a behavioral reference for long-running supervision, retry, status, and low-rate traffic. `wkcli sim` must own its implementation under `cmd/wkcli/internal/sim` so later removal of `internal/` does not break the command.

## Command UX

Primary examples:

```bash
go run ./cmd/wkcli sim --server http://127.0.0.1:5001
go run ./cmd/wkcli sim --context dev --users 1000 --groups 500 --group-members 10 --rate 0.25/s
go run ./cmd/wkcli sim --server http://127.0.0.1:5001 --status-listen 127.0.0.1:19091 --json
```

Target selection order:

1. `--server` supplies one or more v2 HTTP API base URLs.
2. `--context` supplies HTTP API base URLs from a named `wkcli` context.
3. The current `wkcli` context is used when neither `--server` nor `--context` is set.

Gateway selection order:

1. `--gateway` supplies one or more WKProto TCP gateway addresses.
2. Otherwise `wkcli sim` discovers gateway addresses from each target server through `GET /bench/v1/capacity-target`.

Suggested first-version flags:

```text
--server             repeatable or comma-separated v2 HTTP API URL
--context            named wkcli context
--gateway            repeatable or comma-separated WKProto TCP address override
--bench-token        optional bearer token for bench API requests
--users              generated online user count, default 100
--groups             generated group channel count, default 20
--group-members      subscribers per generated group, default 10
--rate               per-group send rate, default 0.2/s
--payload-size       deterministic payload size, default 128B
--connect-rate       gateway CONNECT rate limit, default 20/s
--concurrency        maximum concurrent send operations, default 64
--ack-timeout        SENDACK wait timeout, default 5s
--operation-timeout  connect/write control operation timeout, default 5s
--run-id             optional deterministic run id prefix
--uid-prefix         generated UID prefix, default wkcli-sim-u
--device-prefix      generated device prefix, default wkcli-sim-d
--channel-prefix     generated group channel prefix, default wkcli-sim-g
--status-listen      local status HTTP listen address, default 127.0.0.1:19091
--status-interval    terminal status refresh interval, default 2s
--max-runtime        maximum runtime before clean shutdown, default 0 for until interrupted
--json               print terminal status as JSON lines
```

## Architecture

Create a new package:

```text
cmd/wkcli/internal/sim/
  command.go       Cobra command, flags, dependency injection
  config.go        defaults, validation, parsing of rates and sizes
  target.go        v2 HTTP client, context resolution, capability preflight
  plan.go          deterministic users, groups, subscribers, sender schedule
  runtime.go       setup, connect, traffic loop, retry supervisor, shutdown
  status.go        lifecycle state and counters
  server.go        local /healthz and /status
  render.go        human and JSON status output
```

Register the command in `cmd/wkcli/cli.go` through `defaultCommandFactories`.

The implementation may depend on:

- `cmd/wkcli/internal/command`
- `cmd/wkcli/internal/context`
- `pkg/client`
- `pkg/protocol/frame`
- Go standard library packages

The implementation must not depend on:

- `internal/bench/*`
- `internalv2/access/api` server structs
- `internalv2/app` or other internalv2 implementation packages

The v2 bench HTTP contract is represented by local DTO structs in `target.go`. These DTOs mirror the public JSON shape only:

- `GET /healthz`
- `GET /readyz`
- `GET /bench/v1/capabilities`
- `GET /bench/v1/capacity-target`
- `GET /bench/v1/snapshot`
- `POST /bench/v1/channels`
- `POST /bench/v1/channels/subscribers`

## v2 Capability Preflight

`wkcli sim` targets v2 only in the first version. Before setup or traffic, it checks every configured HTTP server:

- `/healthz` returns a 2xx response.
- `/readyz` returns a 2xx response.
- `/bench/v1/capabilities` returns `enabled=true`.
- `version` is `bench/v1`.
- `supports.channels_batch=true`.
- `supports.channel_subscribers_batch=true`.
- `supports.snapshot=true`.
- `supports.channel_types` contains `group`.

If any server fails, the command exits with `command.ExitConfig` and prints an actionable message, for example:

```text
target http://127.0.0.1:5001 bench capabilities missing required support: channels_batch, channel_subscribers_batch
```

The error should mention `WK_BENCH_API_ENABLE=true` when `/bench/v1/capabilities` is missing or disabled.

## Deterministic Plan

The simulator builds a deterministic plan from normalized config:

- Users are `wkcli-sim-u-000001`, `wkcli-sim-u-000002`, and so on.
- Devices are `wkcli-sim-d-000001`, `wkcli-sim-d-000002`, and so on.
- Group channels are `wkcli-sim-g-000001`, `wkcli-sim-g-000002`, and so on.
- Each group gets `--group-members` subscribers selected by deterministic wraparound over the user pool.
- Each group sender is selected round-robin from that group subscriber list.
- Client message numbers include `run-id`, channel index, sender UID, and monotonic message index.

Validation rules:

- `--users` must be greater than zero.
- `--groups` must be greater than zero.
- `--group-members` must be greater than zero and no greater than `--users`.
- `--rate` must be greater than zero.
- `--payload-size` must be greater than zero.
- `--concurrency`, `--connect-rate`, `--ack-timeout`, `--operation-timeout`, and `--status-interval` must be positive.
- `--max-runtime` must be non-negative.

## Setup Flow

Setup uses the v2 bench HTTP API, not direct storage or internal runtime calls:

1. Build the deterministic plan.
2. Post generated group channels to `/bench/v1/channels` in batches no larger than the target `limits.max_batch_size` when that limit is positive.
3. Post generated subscribers to `/bench/v1/channels/subscribers` in batches no larger than the target `limits.max_batch_size` when that limit is positive.
4. Use `channel_type=2` for group channels.
5. Set `allow_stranger=true` for generated group channels so gateway sends from selected subscribers are accepted in development targets.

The setup client tries configured HTTP servers in deterministic order for mutation calls. A failure on one server can fall through to the next server, but a batch is reported failed if every server rejects it.

## Connection Flow

`runtime.go` creates a `pkg/client.Pool` with:

- discovered or explicit gateway TCP addresses,
- `AutoRecvAck=true`,
- configured `OperationTimeout`,
- configured `AckTimeout`,
- configured send queue and in-flight limits derived from `--concurrency`.

It connects every generated user identity through the pool at `--connect-rate`.

The first version uses empty CONNECT tokens because the current `wukongimv2` gateway does not require token authentication. `--bench-token` only protects HTTP bench API calls.

## Traffic Flow

Traffic is group-only:

1. A scheduler emits send work at `--rate` per group.
2. Work is admitted into a bounded worker pool capped by `--concurrency`.
3. Each worker sends one `pkg/client.RoutedMessage` through `Pool.SendBatch`.
4. The message uses `frame.ChannelTypeGroup`.
5. Payload bytes are deterministic and fixed-size.
6. A successful SENDACK increments `messages_sent`.
7. Non-success reason codes increment `send_errors` and record `last_error`.
8. `pkg/client.Observer` increments `recv_messages`, `recv_dropped`, and low-cardinality client error counters.

The simulator keeps running until interrupted, until `--max-runtime` expires, or until the context is canceled by tests.

## Retry And Recovery

The first version keeps recovery conservative:

- Initial target preflight or setup failure exits immediately.
- Initial connect failure exits immediately.
- Runtime send errors do not stop the simulator by default; they are counted and surfaced in status.
- If `Pool.SendBatch` returns a connection-level error, the simulator marks state `retrying`, closes the current pool, waits a short fixed backoff, reconnects the generated users, and resumes traffic with a new client-message suffix.
- Ctrl+C performs a clean shutdown and closes the pool and status server.

This keeps the operational simulator useful for watching transient target instability without adding a full benchmark coordinator.

## Status API

The local status server exposes:

```text
GET /healthz
GET /status
```

`/status` returns:

```json
{
  "state": "running",
  "run_id": "wkcli-sim-20260617-153000",
  "target_servers": ["http://127.0.0.1:5001"],
  "gateway_tcp_addrs": ["127.0.0.1:5100"],
  "users": 100,
  "active_users": 100,
  "groups": 20,
  "group_members": 10,
  "messages_sent": 1234,
  "send_errors": 0,
  "recv_messages": 9876,
  "recv_dropped": 0,
  "reconnects": 0,
  "last_error": "",
  "last_transition_at": "2026-06-17T15:30:05+08:00"
}
```

States:

- `starting`
- `preflighting`
- `setting_up`
- `connecting`
- `running`
- `retrying`
- `stopping`
- `stopped`

Terminal output renders the same status in either human form or JSON lines. Tests can set a short `--max-runtime` and `--status-interval` to avoid long-running unit tests.

## Testing

Unit tests should stay fast and not require a real WuKongIM process.

Coverage:

- Root help lists `sim`.
- `sim --help` lists target, workload, and status flags.
- Config normalization validates users, groups, group members, rate, duration, and payload size.
- Context resolution reads servers from a named or current `wkcli` context.
- Target preflight succeeds against an `httptest.Server` that implements the v2 bench API JSON shape.
- Target preflight fails with clear messages when capabilities omit group channels or batch setup support.
- Gateway discovery reads `/bench/v1/capacity-target`.
- Plan generation is deterministic and assigns group subscribers by wraparound.
- Setup posts expected `/bench/v1/channels` and `/bench/v1/channels/subscribers` payloads.
- Status model transitions and counter increments are thread-safe.
- Status HTTP server serves `/healthz` and `/status`.

Runtime tests should inject a small pool interface instead of opening real TCP sockets. A later integration or e2e test can start `cmd/wukongimv2` and run a short `wkcli sim --max-runtime` smoke, but that must use an integration/e2e tag because real process tests are slower.

## Documentation

Update `cmd/wkcli/README.md` with:

- `sim` in the command table.
- Basic v2 examples.
- Required target configuration: `WK_BENCH_API_ENABLE=true`, published `WK_EXTERNAL_TCPADDR`, and a reachable `WK_API_LISTEN_ADDR`.
- A note that single-node use is still a single-node cluster.
- Status endpoint examples.

No `wukongim.conf.example` change is required for the CLI command itself unless implementation discovers a new target-side config need.
