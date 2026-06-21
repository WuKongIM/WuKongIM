# internalv2/access/api Flow

## Responsibility

`internalv2/access/api` exposes the HTTP target surface needed to benchmark the
phase-1 `SEND -> SENDACK` skeleton plus compatible channel and user management
surfaces migrated from `internal/access/api`. It owns HTTP routing,
request/response DTOs, and entry validation, but it does not mutate message,
conversation, channel, user, or management business state directly. Channel
management requests forward to the channel usecase supplied by the composition
root, `/user*` requests forward to the user usecase, and compatible message
send and channel-message sync requests forward to the message usecase.
`/message/sync` and `/message/syncack` requests forward to the CMD sync
usecase. `/conversation/list` and `/conversation/sync` requests forward to the
conversation usecase and keep ordering, cursor rules, sync candidate selection,
and message reads out of the HTTP layer. When the composition root provides a
benchmark data writer,
`/bench/v1/channels` and `/bench/v1/channels/subscribers` forward setup
mutations through that writer; for `cmd/wukongimv2` delivery benchmarks the
writer persists real clusterv2 Slot metadata.

## Routes

```text
GET  /healthz
GET  /readyz
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
POST /bench/v1/users/tokens
POST /bench/v1/channels
POST /bench/v1/channels/subscribers
POST /message/send
POST /message/sync
POST /message/syncack
POST /conversation/list
POST /conversation/sync
POST /conversations/clearUnread
POST /conversations/setUnread
POST /conversations/delete
POST /channel
POST /channel/messagesync
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
`BenchEnabled=true`. They are unauthenticated and must be used only in controlled
benchmark environments.

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
`DebugAPIEnabled=true`. In `cmd/wukongimv2`, that switch is
`WK_DEBUG_API_ENABLE`. Diagnostics debug routes also require a diagnostics reader
and query the node-local bounded diagnostics store for controlled performance and
troubleshooting runs.

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
protocol reason codes. `/message/sync` and `/message/syncack` forward durable
CMD message sync and ack requests to `internalv2/usecase/cmdsync`, preserving
legacy validation messages and response envelopes while keeping CMD projection
reads and read-cursor writes out of the HTTP layer. They operate on
`ConversationKindCMD` rows from the shared UID-owned conversation projection and
return only durable command messages, stripping one command-channel suffix from
client-facing channel IDs when present. `/channel/messagesync` keeps the legacy
response shape and converts canonical person-channel IDs back to the peer UID
for the logged-in user. If the composition root does not provide the
corresponding message or CMD sync usecase, these routes fail closed using their
legacy envelopes.

The conversation list, sync, and mutation routes are registered regardless of bench mode.
`/conversation/list` accepts `uid`, `limit`, and an optional sorted conversation
cursor based on `active_at`, `channel_id`, and `channel_type`. It delegates
ordering, cursor application, active-page reads, and current-page last-message
loads to `internalv2/usecase/conversation`, then returns `conversations`,
`next_cursor`, and `more`. Each conversation item contains the active row
fields plus `unread`; `last_message` is `null` when the usecase found no visible
durable message for that row. The access adapter converts canonical
person-channel IDs back to the peer UID for the requesting user. The underlying
usecase/infra path reads `ConversationKindNormal` rows and ordinary message
hydration skips `SyncOnce` or command-channel rows, so CMD activity cannot
replace the ordinary `last_message` or recent-message list. If the composition
root does not provide a conversation usecase, the route fails closed with the
compatible JSON error envelope. Each request emits a
low-cardinality conversation-list observation containing result, latency,
returned item count, sparse item count, last-message load count, last-message
error count, active-index stale skip count, and whether another active page is
available.
`/conversation/sync` accepts the legacy request fields `uid`, `version`,
`last_msg_seqs`, `msg_count`, `only_unread`, `exclude_channel_types`, and
`limit`. The adapter parses `last_msg_seqs`, normalizes person-channel peer IDs
to canonical channel IDs before calling the usecase, and returns the legacy
array response with `recents` when requested. Canonical person-channel IDs in
conversation rows and recent messages are converted back to the peer UID for
the requesting user. If the composition root does not provide a conversation
usecase, the route fails closed with the compatible JSON error envelope. The
adapter records one low-cardinality sync observation for each request path,
including invalid JSON, invalid `last_msg_seqs`, missing usecase, usecase
errors, and successful responses. Observation labels never include UID, channel
ID, device, message ID, or error text.
`/conversations/clearUnread`, `/conversations/setUnread`, and
`/conversations/delete` preserve the legacy mutation envelopes. The adapter
validates only request shape, normalizes personal peer IDs to canonical
conversation IDs, and delegates read-cursor or delete-barrier writes to the
conversation usecase. The usecase and infra adapter keep those writes on the
UID-owned Slot metadata path; the HTTP layer does not write conversation state
directly.

## Phase-1 Semantics

All routes inherit open browser CORS handling from the HTTP adapter. The
middleware echoes a request `Origin` when present, falls back to `*` when no
origin is supplied, and answers preflight `OPTIONS` requests with `204` before
business handlers run.

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

Compatible channel and user management are adapters only. The channel adapter
validates JSON fields, defaults `/channel/subscriber_add` with missing
`channel_type` to group, rejects personal-channel subscriber mutations, and
delegates durable metadata and member-list behavior to
`internalv2/usecase/channel`. Subscriber cache refresh for channelappend runtime
state is triggered by the composition root through the channel usecase observer,
not by the HTTP adapter directly. The user adapter maps JSON into
`internalv2/usecase/user` commands and does not access storage or presence
directly. The message adapter decodes legacy HTTP payloads and trace headers
but leaves send orchestration, request-scoped command-channel derivation, and
channel message reads to `internalv2/usecase/message`.
The CMD sync adapter validates only request shape, UID presence, non-negative
limits, and non-zero `last_message_seq` for syncack; CMD row selection,
message ordering, command suffix stripping, and read-cursor writes over
`ConversationKindCMD` stay in `internalv2/usecase/cmdsync`.
The conversation adapter validates only request shape and UID presence; active
index ordering, active cursor application, sync candidate filtering,
read/delete cursor mutation, and ordinary `ConversationKindNormal` message
reads stay in `internalv2/usecase/conversation`. The adapter observes
successful and failed list requests without adding UID or channel labels, so
performance triage can inspect list cost without increasing metrics
cardinality.
