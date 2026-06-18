# internalv2/access/manager Flow

## Responsibility

`internalv2/access/manager` exposes the dedicated manager HTTP listener for
new-architecture administration routes. It owns HTTP routing, CORS, static
manager login validation, JWT issuance, manager route permission checks, and
manager-specific response envelopes. It does not own cluster, message, channel,
user, or conversation business state.

## Routes

```text
POST /manager/login   (only when Auth.On=true)
GET  /manager/nodes   (read-only node list; requires cluster.node:r when Auth.On=true)
GET  /manager/monitor/realtime (Prometheus-backed business realtime monitor cards; requires cluster.node:r when Auth.On=true)
GET  /manager/cluster-monitor/realtime (Prometheus/control-snapshot cluster operations monitor cards; requires cluster.node:r when Auth.On=true)
GET  /manager/runtime/workqueues (local-node runtime pressure; requires cluster.node:r when Auth.On=true)
GET  /manager/slots   (read-only Slot list; requires cluster.slot:r when Auth.On=true)
GET  /manager/controller/logs (Controller distributed log page; requires cluster.controller:r when Auth.On=true)
GET  /manager/slots/:slot_id/logs (Slot distributed log page; requires cluster.slot:r when Auth.On=true)
GET  /manager/app-logs/sources (ordinary app log fixed source list; requires cluster.log:r when Auth.On=true)
GET  /manager/app-logs (ordinary app log page; requires cluster.log:r when Auth.On=true)
GET  /manager/app-logs/stream (ordinary app log NDJSON stream; requires cluster.log:r when Auth.On=true)
GET  /manager/channel-runtime-meta (read-only channel runtime metadata list; requires cluster.channel:r when Auth.On=true)
GET  /manager/channels (read-only business channel list; requires cluster.channel:r when Auth.On=true)
GET  /manager/conversations (recent conversation list; requires cluster.channel:r when Auth.On=true)
GET  /manager/messages (channel message list; requires cluster.channel:r when Auth.On=true)
POST /manager/messages/retention (message retention request; requires cluster.channel:w when Auth.On=true)
GET  /manager/connections (connection list; requires cluster.connection:r when Auth.On=true)
GET  /manager/connections/:session_id (connection detail; requires cluster.connection:r when Auth.On=true)
GET  /manager/db/inspect/tables (DB Inspect table list; requires cluster.db:r when Auth.On=true)
GET  /manager/db/inspect/tables/:domain/:table (DB Inspect table schema; requires cluster.db:r when Auth.On=true)
POST /manager/db/inspect/query (DB Inspect query; requires cluster.db:r when Auth.On=true)
GET  /manager/users   (user list; requires cluster.user:r when Auth.On=true)
GET  /manager/users/:uid (user detail; requires cluster.user:r when Auth.On=true)
POST /manager/users/:uid/kick (force offline; requires cluster.user:w when Auth.On=true)
POST /manager/users/:uid/token/reset (reset token; requires cluster.user:w when Auth.On=true)
GET  /manager/system-users (system UID list; requires cluster.user:r when Auth.On=true)
POST /manager/system-users/add (add system UIDs; requires cluster.user:w when Auth.On=true)
POST /manager/system-users/remove (remove system UIDs; requires cluster.user:w when Auth.On=true)
```

`/manager/login` preserves the legacy manager response shape migrated from
`internal/access/manager`: successful responses include `username`,
`token_type`, `access_token`, `expires_in`, `expires_at`, and `permissions`;
invalid JSON returns `{"error":"invalid_request","message":"invalid request"}`;
invalid credentials return
`{"error":"invalid_credentials","message":"invalid credentials"}`.

`/manager/nodes` preserves the legacy list response shape for the web node list
view. It reads a control snapshot through `internalv2/usecase/management` and
sets node operation action hints to false because node lifecycle/scale-in
operation routes are intentionally not migrated in this phase.

`/manager/monitor/realtime` backs the web business realtime monitor card wall.
It parses the chart `window` and optional `step`, requires `cluster.node:r` when
manager auth is enabled, and delegates all metric reads to the app-wired
Prometheus monitor provider. When Prometheus is disabled or unavailable the
route still returns HTTP 200 with an explicit monitor status so the web UI can
show setup guidance instead of rendering empty charts. This route does not read
from the top collector or any in-process dashboard ring buffer.

`/manager/cluster-monitor/realtime` backs the web cluster operations realtime
monitor card wall. It uses the same chart `window` and optional `step` parsing
and bounds as the business realtime monitor, requires `cluster.node:r` when
manager auth is enabled, and delegates Prometheus plus bounded
`control_snapshot` reads to the app-wired cluster monitor provider. This route
does not read from the top collector or any in-process dashboard ring buffer.

`/manager/runtime/workqueues` is backed by the `internalv2/app` top collector.
It is a forced runtime view of the local node only: it does not fan out to peer
nodes and does not depend on Prometheus. When the collector is not configured or
has not sampled enough data yet, the route returns `service_unavailable`.
The manager route preserves provider labels from the top collector; TransportV2
service aliases are registered with the service worker pools and resolved before
the HTTP response layer.

`/manager/slots` preserves the legacy list response shape for the web Slot list
view, including `node_id` filtering. It reads the same control snapshot through
`internalv2/usecase/management`; Slot detail and operation routes remain
unmigrated.

`/manager/controller/logs` and `/manager/slots/:slot_id/logs` expose
newest-first distributed Raft log pages for the selected node. They parse
`node_id`, `limit`, and `cursor` at the HTTP boundary, delegate local/remote log
selection to `internalv2/usecase/management`, and return decoded payload
summaries only for inspection; the routes do not mutate Controller or Slot
state.

`/manager/app-logs*` exposes ordinary WuKongIM process logs under `WK_LOG_DIR`,
not Controller, Slot, Channel, or Raft logs. The routes use the fixed ordinary
application log sources owned by the application log reader (`app`, `warn`,
`error`, and `debug`), require an explicit positive `node_id`, and delegate
local-vs-remote node selection to `internalv2/usecase/management`. The stream
route emits lightweight NDJSON events for lines, rotations, heartbeats, and
reader errors without owning a long-running log runtime.

`/manager/channels` preserves the legacy business channel list response shape
for the web channel list view, including `node_id`, `type`, `keyword`, `limit`,
and `cursor` query parameters. It only exposes the list display route; channel
detail, member, and mutation operation routes remain unmigrated. Non-local
`node_id` reads are delegated below the HTTP layer through the management
usecase.

`/manager/channel-runtime-meta` preserves the legacy channel cluster list
response shape for the web cluster channel page, including `node_id`,
`node_scope`, `channel_id`, `include_max_message_seq`, `limit`, and `cursor`
query parameters. It exposes read-only runtime metadata only; channel detail,
replica, leader-transfer, repair, and mutation operation routes remain
unmigrated in internalv2.

`/manager/conversations` preserves the legacy recent conversation manager
response shape for the web recent-conversation list view, including `uid`,
`limit`, `msg_count`, and `only_unread` query parameters. It reuses the
internalv2 conversation sync usecase and remains a read-only display route.

`/manager/messages` preserves the legacy channel message list response shape
for the web message page, including `channel_id`, `channel_type`, `limit`,
`cursor`, `message_id`, and `client_msg_no` query parameters. It reads
committed ChannelV2 messages through `internalv2/usecase/management`.
`/manager/messages/retention` preserves the legacy request/response envelope
and delegates to an optional retention port; when no v2 retention implementation
is configured, the route returns `service_unavailable` instead of reporting a
false success.

`/manager/connections*` preserves the legacy connection list/detail JSON shape
for the web connection page. In internalv2 it reads the owner-local online
registry for local or empty `node_id` filters, routes remote `node_id` filters
through internalv2 node RPC, and maps session details such as UID, device,
listener, state, timestamps, and addresses when the gateway session handle
exposes them. If a remote connection reader is not wired, non-local filters
return `service_unavailable`.

`/manager/db/inspect*` backs the web `/system/db` DB Inspect page. It parses
HTTP query/body parameters, enforces `cluster.db:r` when manager auth is
enabled, and delegates all local-vs-remote targeting to
`internalv2/usecase/management`. Empty `node_id` means the local manager node;
non-local `node_id` requests are routed below this package through manager DB
inspect node RPC. The routes are read-only diagnostics: they do not merge
cluster rows, expose filesystem paths, or provide storage mutation operations.

`/manager/users*` and `/manager/system-users*` preserve the legacy manager user
management response shapes for the web user and system-user pages. HTTP parsing,
cursor encoding, permissions, and response DTOs stay here; user metadata scans,
presence joins, token reset, force-offline, and system UID mutations are
delegated to `internalv2/usecase/management`.

The manager server uses its own listen address from the composition root and is
separate from `internalv2/access/api`. In `cmd/wukongimv2`, that listener is
configured by `WK_MANAGER_LISTEN_ADDR`; JWT settings and static users are
configured by the `WK_MANAGER_*` auth keys.
