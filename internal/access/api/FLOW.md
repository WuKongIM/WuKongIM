# internal/access/api Flow

## Responsibility

`internal/access/api` exposes the HTTP target surface needed to benchmark the
phase-1 `SEND -> SENDACK` skeleton plus compatible channel and user management
surfaces migrated from `internal/access/api`. The same listener serves the
embedded chat Demo under `/demo/`, allowing it to call product APIs on the
same origin. It owns HTTP routing,
request/response DTOs, and entry validation, but it does not mutate message,
conversation, channel, user, or management business state directly. Channel
management requests forward to the channel usecase supplied by the composition
root, `/user*` requests forward to the user usecase, and compatible message
send and channel-message sync requests forward to the message usecase.
`/message/sync` and `/message/syncack` requests forward to the CMD sync
usecase. `/conversation/list` and `/conversation/retry` requests forward to the
conversation usecase and keep directory ordering, cursor rules, hydration, and
badge calculation out of the HTTP layer. When the composition root provides a
benchmark data writer,
`/bench/v1/channels`, `/bench/v1/channels/subscribers`, and
`/bench/v1/channels/subscribers/remove` forward setup or churn membership
mutations through that writer; for `cmd/wukongim` delivery benchmarks the
writer persists real cluster Slot metadata.
When `bench.api_token` is configured, every `/bench/v1/*` request must carry
the exact `Authorization: Bearer <token>` capability. The same capability
protects every enabled `/debug` and `/debug/*` route, including pprof; health,
readiness, metrics, and product routes remain outside that middleware. An empty
token retains the existing controlled-environment debug compatibility mode.

Controller restore maintenance places a node-local middleware in front of all
product, route-discovery, and benchmark endpoints. It returns HTTP `503` with
the stable `maintenance` error before any business handler runs. Health,
readiness, metrics, debug, top, and embedded Demo assets remain reachable for
operations and diagnosis while the data plane is fenced.

## Routes

```text
GET  /healthz
GET  /readyz
GET  /demo/                           (embedded chat Demo)
HEAD /demo/                           (embedded chat Demo)
GET  /metrics                         (optional, when MetricsHandler is configured)
GET  /debug/config                    (optional, when DebugAPIEnabled is configured)
GET  /debug/cluster                   (optional, when DebugAPIEnabled is configured)
GET  /debug/goroutines                (optional, when DebugAPIEnabled is configured)
GET  /debug/pprof/*                   (optional, when DebugAPIEnabled is configured)
GET  /debug/diagnostics/trace/:trace_id (optional, when DebugAPIEnabled and Diagnostics are configured)
GET  /debug/diagnostics/message       (optional, when DebugAPIEnabled and Diagnostics are configured)
GET  /debug/diagnostics/events        (optional, when DebugAPIEnabled and Diagnostics are configured)
GET  /route
POST /route/batch
GET  /top/v1/snapshot
GET  /bench/v1/capabilities
GET  /bench/v1/capacity-target
GET  /bench/v1/snapshot
GET  /bench/v1/presence/snapshot
GET  /bench/v1/channel-runtime/snapshot
POST /bench/v1/channel-runtime/probe
POST /bench/v1/channel-runtime/evict
POST /bench/v1/terminal-fence/prepare
POST /bench/v1/users/tokens
POST /bench/v1/channels
POST /bench/v1/channels/subscribers
POST /bench/v1/channels/subscribers/remove
POST /message/send
POST /message/event
POST /message/sync
POST /message/syncack
POST /message/cmd/bind
POST /message/cmd/unbind
POST /conversation/list
POST /conversation/retry
POST /conversations/clearUnread
POST /conversations/setUnread
POST /conversations/delete
POST /conversations/activate
POST /channel
POST /channel/messagesync
POST /channel/messagesyncbatch
POST /channel/info
POST /channel/delete
POST /channel/subscriber_add
POST /channel/subscriber_remove
POST /channel/subscriber_remove_all
POST /tmpchannel/subscriber_set
POST /channel/blacklist_add
POST /channel/blacklist_set
POST /channel/blacklist_remove
POST /channel/blacklist_remove_all
POST /channel/whitelist_add
POST /channel/whitelist_set
POST /channel/whitelist_remove
POST /channel/whitelist_remove_all
GET  /channel/whitelist
POST /user/token
POST /user/device_quit
POST /user/onlinestatus
POST /user/systemuids_add
POST /user/systemuids_remove
GET  /user/systemuids
POST /user/systemuids_add_to_cache
POST /user/systemuids_remove_from_cache
```

The `/bench/v1/*` routes are enabled only when the composition root passes
`BenchEnabled=true`. When `bench.api_token` is non-empty they require the exact
bearer capability described above; an empty token retains the explicit local
benchmark compatibility mode and must be used only in controlled environments.

`POST /bench/v1/terminal-fence/prepare` is registered and advertised only when
the composition root supplies the complete one-shot product terminal controller
and a non-empty benchmark bearer token. Unlike read/setup compatibility routes,
this irreversible drain never has an unauthenticated compatibility mode. It accepts a
strict JSON document bounded to 4 KiB and binds one exact `run_id`,
`assignment_id`, and `expected_sessions` tuple. A successful response uses the
shared `pkg/bench/model` terminal-fence schema and returns its opaque capability
only to the authenticated caller. Logs and error responses contain only closed
result classes, request method/path, and expected-session count; they never
contain either benchmark identity, the capability, or a raw adapter error.

`GET /top/v1/snapshot` is a read-only, node-local operations snapshot used by
`wkcli top`. It is independent of Prometheus metrics and remains disabled unless
the composition root passes a Top provider; without that provider the route
returns `404`. Each node reports its own process CPU, RSS/VMS memory,
goroutine, and thread usage through gopsutil in this snapshot so multi-node
`wkcli top` can compare node-local resource pressure without requiring SSH or
Prometheus. The response also includes sticky node-local `alerts` for active
and recently resolved readiness, pressure, and sendack-error signals so
operators do not miss short-lived warnings between CLI refreshes. Alert entries
carry low-cardinality `evidence` key/value facts, such as pressure score,
queue depth/capacity, thresholds, ready part, or sendack error rate, so detail
views can explain why the alert fired without scraping Prometheus metrics.

All `/debug...` routes are enabled only when the composition root passes
`DebugAPIEnabled=true`. In `cmd/wukongim`, that switch is
`WK_DEBUG_API_ENABLE`. When `bench.api_token` is nonempty, the complete debug
subtree requires that bearer capability. Diagnostics debug routes also require
a diagnostics reader and query the node-local bounded diagnostics store for
controlled performance and troubleshooting runs. A failed live cluster
snapshot returns a fixed `503` response and never exposes its internal cause.
The node log retains that cause under the low-cardinality
`internal.access.api.debug_cluster_snapshot_failed` event so a benchmark
observer failure can be attributed to the exact snapshot validation branch.

`GET /demo` and `HEAD /demo` permanently redirect to `/demo/`. The `/demo/*`
surface serves the production Vite bundle embedded at Go build time; exact
content-hashed assets use immutable caching, while `index.html` and public root
assets require revalidation. Missing assets remain `404`, and product routes
such as `/route`, `/user/token`, and `/channel/messagesync` always use their
registered API handlers. The Demo defaults its API base to the page origin and
uses `/route` to discover the configured client WebSocket address.

The compatible `/route` and `/route/batch` routes are registered regardless of
bench mode. They keep the legacy address response envelopes and select public
or intranet gateway addresses from composition-root configuration based on
`intranet=1`. When `node_id`, `nodeId`, or `nodeID` is supplied, the adapter
returns the node-specific address set derived by the composition root from the
static cluster voters. Invalid or unknown node IDs return the legacy
`{"status":400,"msg":"节点参数有误！"}` envelope, and `/route/batch` only
accepts a JSON array of UID strings.

The compatible `/channel*` routes are registered regardless of bench mode. They
keep the existing request and response envelopes, including `{"status":200}`
mutation success responses and `{"status":400,"msg":"..."}` validation errors.
If the composition root does not provide a channel usecase, the routes fail
closed with the same error envelope.

The compatible `/user*` routes are registered regardless of bench mode. They
keep the existing request and response envelopes: token mutations use
`{"status":200}` on success and `{"status":400,"msg":"..."}` on failure,
online-status empty UID lists return `{"status":200}`, non-empty status queries
return the legacy array response, and system UID routes preserve their mutation
and list shapes. If the composition root does not provide a user usecase, these
routes fail closed with the legacy error envelope.

The compatible message routes are registered regardless of bench mode.
`/message/send` accepts the legacy base64 payload request, maps `sender_uid` to
`from_uid`, forwards `subscribers` as an explicit request-scoped command, and
returns the legacy `{"message_id","message_seq","reason"}` response with
protocol reason codes. `/message/event` accepts the legacy message-scoped event
append request, forwards raw JSON payload bytes to `internal/usecase/message`,
and returns the legacy `{"status":200,"data":...}` envelope with the projected
stream status and `msg_event_seq` when the event has reached durable projection.
In-flight `stream.open`, `stream.delta`, and `stream.snapshot` cache responses
may return `msg_event_seq=0` until a terminal stream event is proposed.
`/message/sync` and
`/message/syncack` forward durable CMD message sync and ack requests to
`internal/usecase/cmdsync`, preserving legacy validation messages and response
envelopes while keeping CMD membership reads and acknowledgement writes out of
the HTTP layer. `/message/cmd/bind` and `/message/cmd/unbind` explicitly create
or tombstone durable CMD discovery state. Sync reads
`user_cmd_channel_membership` and returns only durable command-log messages,
stripping one command-channel suffix from client-facing channel IDs.
`/channel/messagesync` keeps the legacy response shape, converts canonical
person-channel IDs back to the peer UID for the logged-in user, and maps message
event summaries to the legacy `event_meta`, `event_sync_hint`, and stream fields
when the usecase provides them. Fine-grained `/message/eventsync` is intentionally
not registered in this phase. If the composition root does not provide the
corresponding message or CMD sync usecase, these routes fail closed using their
legacy envelopes. `/channel/messagesyncbatch` first validates every requested
ordinary membership, then lets the cluster facade group the aligned committed
reads by exact Channel Leader.

The conversation list, retry, and mutation routes are registered regardless of
bench mode. `/conversation/list` accepts `uid`, a candidate `limit`, an opaque
membership cursor, and completed coverage. It returns `conversations`,
`deletes`, `unresolved`, `next_cursor`, `done`, coverage, tombstone retention,
and `reset_required`. The cursor encodes the full `(activated_at, channel_id,
channel_type)` membership-index position; only `done=true` completes a pass.
The access adapter converts canonical person-channel IDs back to the peer UID.
`/conversation/retry` point-reads a bounded set of unresolved keys and reruns
server-side hydration without rewinding directory coverage. Each list request
emits low-cardinality scanned-candidate, returned-item, delete, unresolved,
latency, and result evidence.
`/conversations/clearUnread`, `/conversations/setUnread`, and
`/conversations/delete` preserve their mutation envelopes, while
`/conversations/activate` records explicit navigation priority. The adapter
normalizes person peers and delegates monotonic badge floors, hide floors, and
activation to the usecase. All durable mutations target
`user_channel_membership`; the HTTP layer writes no business state directly.

## Phase-1 Semantics

All routes inherit open browser CORS handling from the HTTP adapter. The
middleware echoes a request `Origin` when present, falls back to `*` when no
origin is supplied, and answers preflight `OPTIONS` requests with `204` before
business handlers run.

The user-token mutation route is intentionally restricted to setup
acknowledgments for black-box `wkbench` compatibility. The current
`wukongim` gateway does not enable token authentication, so this route does
not prove user-token persistence.

The bench channel and subscriber mutation routes require a benchmark data
writer from the composition root. Without that writer, capabilities do not
advertise channel mutation support and mutation requests fail closed with
`501`. With a writer, they inject real channel metadata and add or remove
subscriber rows through the composition root. Subscriber reset requests remain
unsupported. The promoted target advertises both `person` and `group` Channel
types because chat-lifecycle traffic creates canonical person relationships as
well as the prepared fixed group catalog.

`/bench/v1/presence/snapshot` is a read-only diagnostic route. It reports
owner-local route counts and authority-side virtual route counts for wkbench
reports, but it does not expose or mutate concrete gateway sessions.

`POST /bench/v1/channel-runtime/probe` accepts exactly one bounded selector:
the existing generated benchmark range or 1..1,200 unique concrete Channel
identities. Concrete IDs are validated without normalization and are forwarded
only to the read-only probe port. Snapshot and eviction keep their generated
selector contracts; concrete identities are not accepted by the eviction
route. Restricted bench authentication continues to cover all three routes.
If the runtime controller fails an explicit probe, the adapter returns and logs
only the stable `explicit channel runtime probe failed` message; raw controller
errors can contain requested identities and therefore remain private. Generated
probe failures retain their existing response behavior. Explicit failures log
only the shared closed reason code plus existing low-cardinality request-shape
fields; source errors and concrete identities are not logged.

Compatible channel and user management are adapters only. The channel adapter
validates JSON fields, defaults `/channel/subscriber_add` with missing
`channel_type` to group, rejects personal-channel subscriber mutations, and
delegates durable metadata and member-list behavior to
`internal/usecase/channel`. Subscriber cache refresh for channelappend runtime
state is triggered by the composition root through the channel usecase observer,
not by the HTTP adapter directly. The user adapter maps JSON into
`internal/usecase/user` commands and does not access storage or presence
directly. The message adapter decodes legacy HTTP payloads and trace headers
but leaves send orchestration, request-scoped command-channel derivation, and
channel message reads to `internal/usecase/message`.
The CMD sync adapter validates request shape; CMD membership selection,
message ordering, suffix stripping, binding, and monotonic `ack_seq` writes
stay in `internal/usecase/cmdsync`.
The conversation adapter validates only request shape and UID presence;
membership-index ordering, opaque cursor application, hydration, badge
calculation, and personal-state mutations stay in
`internal/usecase/conversation`. The adapter observes
successful and failed list requests without adding UID or channel labels, so
performance triage can inspect list cost without increasing metrics
cardinality.
