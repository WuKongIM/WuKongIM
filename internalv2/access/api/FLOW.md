# internalv2/access/api Flow

## Responsibility

`internalv2/access/api` exposes the minimal HTTP target surface needed to
benchmark the phase-1 `SEND -> SENDACK` skeleton. It owns HTTP routing,
request/response DTOs, and benchmark-only validation, but it does not mutate
message, delivery, conversation, or management business state.

## Routes

```text
GET  /healthz
GET  /readyz
GET  /metrics                         (optional, when MetricsHandler is configured)
GET  /debug/pprof/*                   (optional, when PProfEnabled is configured)
GET  /bench/v1/capabilities
GET  /bench/v1/capacity-target
GET  /bench/v1/snapshot
POST /bench/v1/users/tokens
POST /bench/v1/channels
POST /bench/v1/channels/subscribers
```

The `/bench/v1/*` routes are enabled only when the composition root passes
`BenchEnabled=true`. They are unauthenticated and must be used only in controlled
benchmark environments.

## Phase-1 Semantics

The bench mutation routes are intentionally restricted to setup acknowledgments
for black-box `wkbench` compatibility. The current `wukongimv2` gateway does not
enable token authentication, and delivery/fanout are phase-1 non-goals, so these
routes do not prove user-token, subscriber, or delivery performance.
