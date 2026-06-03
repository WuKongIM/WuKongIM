# internalv2/access/api Flow

## Responsibility

`internalv2/access/api` exposes the minimal HTTP target surface needed to
benchmark the phase-1 `SEND -> SENDACK` skeleton. It owns HTTP routing,
request/response DTOs, and benchmark-only validation, but it does not mutate
message, conversation, or management business state directly. When the
composition root provides a benchmark data writer, `/bench/v1/channels` and
`/bench/v1/channels/subscribers` forward setup mutations through that writer;
for `cmd/wukongimv2` delivery benchmarks the writer persists real clusterv2
Slot metadata.

## Routes

```text
GET  /healthz
GET  /readyz
GET  /metrics                         (optional, when MetricsHandler is configured)
GET  /debug/pprof/*                   (optional, when PProfEnabled is configured)
GET  /bench/v1/capabilities
GET  /bench/v1/capacity-target
GET  /bench/v1/snapshot
GET  /bench/v1/presence/snapshot
GET  /bench/v1/channel-runtime/snapshot
POST /bench/v1/channel-runtime/probe
POST /bench/v1/channel-runtime/evict
POST /bench/v1/users/tokens
POST /bench/v1/channels
POST /bench/v1/channels/subscribers
```

The `/bench/v1/*` routes are enabled only when the composition root passes
`BenchEnabled=true`. They are unauthenticated and must be used only in controlled
benchmark environments.

## Phase-1 Semantics

The user-token mutation route is intentionally restricted to setup
acknowledgments for black-box `wkbench` compatibility. The current
`wukongimv2` gateway does not enable token authentication, so this route does
not prove user-token persistence.

`/bench/v1/channels` and `/bench/v1/channels/subscribers` require a benchmark
data writer from the composition root. Without that writer, capabilities do not
advertise channel mutation support and mutation requests fail closed with
`501`. With a writer, they inject real channel metadata and subscriber rows
through the composition root. Subscriber reset requests remain unsupported.

`/bench/v1/presence/snapshot` is a read-only diagnostic route. It reports
owner-local route counts and authority-side virtual route counts for wkbench
reports, but it does not expose or mutate concrete gateway sessions.
