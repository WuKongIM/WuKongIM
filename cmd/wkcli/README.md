# wkcli

`wkcli` is the extensible operations CLI for WuKongIM. The root package stays
thin: root wiring owns shared IO, exit-code handling, and subcommand
registration; each feature command lives in its own directory.

## Commands

```bash
go run ./cmd/wkcli <command> [flags]
```

Current commands:

| Command | Purpose |
| --- | --- |
| `context` | Manages named WuKongIM server API contexts. |
| `bench send` | Runs a lightweight WKProto SEND/SENDACK benchmark using `pkg/client`. |
| `top` | Reads live internalv2 runtime pressure snapshots. |
| `sim` | Runs a long-lived internalv2 real-traffic simulator. |
| `node` | Operates internalv2 dynamic nodes through manager HTTP. |

## Top

`top` reads the internalv2 `/top/v1/snapshot` HTTP endpoint from one or more
WuKongIM nodes, aggregates the node-local snapshots, and renders either a human
overview or pretty JSON. It does not query Prometheus; the endpoint reports
whether metrics are enabled, but metrics are not required for this view.

```bash
go run ./cmd/wkcli top --server http://127.0.0.1:5001
go run ./cmd/wkcli top --context dev --once --json
go run ./cmd/wkcli top --context dev --interval 2s --max-refresh 5
go run ./cmd/wkcli top --context dev --alerts
go run ./cmd/wkcli top --context dev --alert gateway/session_error
```

Without `--server`, `top` reads servers from `--context` or the selected current
context. By default it refreshes until interrupted. Use `--once` for a single
snapshot, or `--max-refresh` to stop after a bounded number of refreshes for
tests and scripted checks.

Use `--alerts` for a one-shot detailed sticky alert view. Use `--alert` with an
alert id, fingerprint, or `component/kind` such as `gateway/session_error` to
print one alert and its evidence fields.

## Contexts

Contexts store one or more WuKongIM HTTP API server addresses under a name. The
selected context becomes the default target for future commands that need server
addresses.

```bash
go run ./cmd/wkcli context add dev \
  --server http://127.0.0.1:5001 \
  --server http://127.0.0.1:5002 \
  --description "local two-node cluster" \
  --select

go run ./cmd/wkcli context ls
go run ./cmd/wkcli context show
go run ./cmd/wkcli context current
go run ./cmd/wkcli context select dev
go run ./cmd/wkcli context rm dev
```

`--server` can be repeated or given as a comma-separated list. Server addresses
must be absolute `http://` or `https://` API URLs.

By default, contexts are stored under the user config directory:

```text
<user-config-dir>/wukongim/wkcli/
  current_context
  contexts/
    dev.json
```

Use `--context-dir` to point tests or local experiments at an isolated store.

## Bench Send

`bench send` is a lightweight direct WKProto benchmark. It is intended for quick
client-side SEND/SENDACK throughput checks and does not replace the full
black-box `cmd/wkbench` scenario runner.

```bash
go run ./cmd/wkcli bench send \
  --gateway 127.0.0.1:5100 \
  --clients 8 \
  --msgs 100000 \
  --channels 100 \
  --channel-prefix bench-g \
  --channel-type group \
  --size 128B
```

Target selection order:

1. `--gateway` uses explicit WKProto TCP gateway addresses.
2. `--server` uses HTTP API addresses and discovers gateways through
   `/bench/v1/capacity-target`.
3. `--context` or the selected current context supplies HTTP API addresses for
   discovery.

`--msgs` is the total message count across all clients and channels. When
`--channels > 1`, channel IDs are generated as
`<channel-prefix>-000001`, `<channel-prefix>-000002`, and so on. Channel
selection defaults to `round_robin`; `random` is also available through
`--channel-pick`.

Useful output flags:

```bash
go run ./cmd/wkcli bench send --gateway 127.0.0.1:5100 --json
go run ./cmd/wkcli bench send --gateway 127.0.0.1:5100 --csv ./bench.csv
go run ./cmd/wkcli bench send --gateway 127.0.0.1:5100 --no-progress
```

## Sim

`sim` drives real traffic against a running `cmd/wukongimv2` single-node
cluster or multi-node cluster. It prepares group metadata through the v2
`/bench/v1/*` HTTP API, keeps generated users online through the WKProto
gateway, sends group `SEND -> SENDACK` traffic, and exposes local simulator
status.

```bash
go run ./cmd/wkcli sim --server http://127.0.0.1:5001
go run ./cmd/wkcli sim --context dev --users 1000 --groups 500 --group-members 10 --rate 0.25/s
curl http://127.0.0.1:19091/status
```

Without `--server` or `--gateway`, `sim` reads HTTP API servers from
`--context` or the selected current context.

The target must expose the v2 benchmark API and a published gateway address:

```text
WK_BENCH_API_ENABLE=true
WK_API_LISTEN_ADDR=127.0.0.1:5001
WK_EXTERNAL_TCPADDR=127.0.0.1:5100
```

Single-node use is still a single-node cluster. `sim` does not import or run
server internals; setup uses `/bench/v1/channels` and
`/bench/v1/channels/subscribers`, while traffic uses real WKProto gateway
connections.

Useful flags:

```bash
go run ./cmd/wkcli sim --server http://127.0.0.1:5001 --status-listen 127.0.0.1:19091
go run ./cmd/wkcli sim --server http://127.0.0.1:5001 --json --status-interval 5s
go run ./cmd/wkcli sim --server http://127.0.0.1:5001 --max-runtime 30s
```

## Node Operations

`node` operates internalv2 dynamic data nodes through manager HTTP. It does not
start or stop server processes and does not write ControllerV2 or Slot state
directly.

```bash
go run ./cmd/wkcli node ls --context dev
go run ./cmd/wkcli node activate 4 --context dev
go run ./cmd/wkcli node diagnose 4 --context dev
go run ./cmd/wkcli node diagnose 4 --context dev --json
go run ./cmd/wkcli node onboarding start 4 --context dev --max-slot-moves 1
go run ./cmd/wkcli node scale-in start 4 --context dev
go run ./cmd/wkcli node scale-in drain 4 --context dev --draining=true
go run ./cmd/wkcli node scale-in status 4 --context dev
go run ./cmd/wkcli node scale-in remove 4 --context dev
```

The command preserves manager safety evidence in output, including health
freshness, control revision, `blocked_reasons`, `safe_to_remove`, and gateway
drain counters. `node diagnose` requests root-cause diagnostics from
`GET /manager/nodes/:node_id/diagnostics` with bounded `task`, `audit`, and
`slot` evidence limits, and can print either a one-line summary plus detailed
task/audit/slot/warning lines or the raw manager JSON with `--json`. Use
`docs/superpowers/runbooks/internalv2-dynamic-node-operations.md` for the full
operator procedure.

## Extending

To add a command:

1. Create `cmd/wkcli/internal/<name>/command.go`.
2. Add `func NewCommand(deps command.Deps) *cobra.Command`.
3. Register that factory in `defaultCommandFactories`.
4. Add focused tests in `cmd/wkcli/main_test.go` or a command-specific test
   file.

Use `command.Deps` for output streams instead of touching `os.Stdout` or
`os.Stderr` directly. Shared CLI primitives live under
`cmd/wkcli/internal/command` so subcommand packages stay small and explicit.
