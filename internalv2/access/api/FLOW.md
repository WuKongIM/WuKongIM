# internalv2/access/api Flow

## Responsibility

`internalv2/access/api` exposes the HTTP target surface needed to benchmark the
phase-1 `SEND -> SENDACK` skeleton plus compatible channel and user management
surfaces migrated from `internal/access/api`. It owns HTTP routing,
request/response DTOs, and entry validation, but it does not mutate message,
conversation, channel, user, or management business state directly. Channel
management requests forward to the channel usecase supplied by the composition
root, `/user*` requests forward to the user usecase, and compatible message
send/sync requests forward to the message usecase. `/conversation/list`
requests forward to the conversation usecase and keep ordering/cursor rules out
of the HTTP layer. When the composition root provides a benchmark data writer,
`/bench/v1/channels` and `/bench/v1/channels/subscribers` forward setup
mutations through that writer; for `cmd/wukongimv2` delivery benchmarks the
writer persists real clusterv2 Slot metadata.

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
POST /message/send
POST /conversation/list
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
protocol reason codes. `/channel/messagesync` keeps the legacy response shape
and converts canonical person-channel IDs back to the peer UID for the logged-in
user. If the composition root does not provide a message usecase, these routes
fail closed using their legacy envelopes.

The conversation list route is registered regardless of bench mode.
`/conversation/list` accepts `uid`, `limit`, and an optional sorted conversation
cursor based on `active_at`, `channel_id`, and `channel_type`. It delegates
ordering, cursor application, active-page reads, and current-page last-message
loads to `internalv2/usecase/conversation`, then returns `conversations`,
`next_cursor`, and `more`. Each conversation item contains the active row
fields plus `unread`; `last_message` is present only when the usecase found a
visible durable message for that row. The access adapter converts canonical
person-channel IDs back to the peer UID for the requesting user. If the
composition root does not provide a conversation usecase, the route fails
closed with the compatible JSON error envelope. Each request emits a
low-cardinality conversation-list observation containing result, latency,
returned item count, last-message hit count, and whether another active page is
available.

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

Compatible channel and user management are adapters only. The channel adapter
validates JSON fields, defaults `/channel/subscriber_add` with missing
`channel_type` to group, rejects personal-channel subscriber mutations, and
delegates durable metadata and member-list behavior to
`internalv2/usecase/channel`. The user adapter maps JSON into
`internalv2/usecase/user` commands and does not access storage or presence
directly. The message adapter decodes legacy HTTP payloads and trace headers
but leaves send orchestration, request-scoped command-channel derivation, and
channel message reads to `internalv2/usecase/message`.
The conversation adapter validates only request shape and UID presence; active
index ordering, active cursor application, and current-page last-message reads
stay in `internalv2/usecase/conversation`. The adapter observes successful and
failed list requests without adding UID or channel labels, so performance
triage can inspect list cost without increasing metrics cardinality.
