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
GET  /manager/realtime-monitor (unified realtime monitor cards; requires cluster.node:r when Auth.On=true)
GET  /manager/runtime/workqueues (local-node runtime pressure; requires cluster.node:r when Auth.On=true)
GET  /manager/slots   (read-only Slot list; requires cluster.slot:r when Auth.On=true)
POST /manager/slots/leader-transfer-plan (read-only Slot leader-transfer batch preview; requires cluster.slot:r when Auth.On=true)
POST /manager/slots/leader-transfer-batch (fenced Slot leader-transfer batch execute; requires cluster.slot:w when Auth.On=true)
POST /manager/slots/:slot_id/leader-transfer (Controller-backed Slot leader-transfer intent; requires cluster.slot:w when Auth.On=true)
POST /manager/nodes/:node_id/slots/:slot_id/compact (manual node-local Slot Raft compaction; requires cluster.slot:w when Auth.On=true)
GET  /manager/controller/logs (Controller distributed log page; requires cluster.controller:r when Auth.On=true)
GET  /manager/controller/tasks (active Controller task list; requires cluster.controller:r when Auth.On=true)
GET  /manager/controller/tasks/:task_id (active Controller task detail; requires cluster.controller:r when Auth.On=true)
GET  /manager/nodes/:node_id/controller-raft (node-local Controller Raft status; requires cluster.controller:r when Auth.On=true)
POST /manager/nodes/:node_id/controller-raft/compact (manual node-local Controller Raft compaction; requires cluster.controller:w when Auth.On=true)
POST /manager/controller-raft/compact (manual Controller voter compaction fan-out; requires cluster.controller:w when Auth.On=true)
GET  /manager/slots/:slot_id/logs (Slot distributed log page; requires cluster.slot:r when Auth.On=true)
GET  /manager/app-logs/sources (ordinary app log fixed source list; requires cluster.log:r when Auth.On=true)
GET  /manager/app-logs (ordinary app log page; requires cluster.log:r when Auth.On=true)
GET  /manager/app-logs/stream (ordinary app log NDJSON stream; requires cluster.log:r when Auth.On=true)
GET  /manager/diagnostics/trace/:trace_id (diagnostics trace aggregation; requires cluster.diagnostics:r when Auth.On=true)
GET  /manager/diagnostics/message (diagnostics message lookup; requires cluster.diagnostics:r when Auth.On=true)
GET  /manager/diagnostics/events (diagnostics event query; requires cluster.diagnostics:r when Auth.On=true)
GET  /manager/diagnostics/tracking-rules (diagnostics tracking rule list; requires cluster.diagnostics:r when Auth.On=true)
POST /manager/diagnostics/tracking-rules (create diagnostics tracking rule; requires cluster.diagnostics:w when Auth.On=true)
DELETE /manager/diagnostics/tracking-rules/:rule_id (delete diagnostics tracking rule; requires cluster.diagnostics:w when Auth.On=true)
GET  /manager/channel-runtime-meta (read-only channel runtime metadata list; requires cluster.channel:r when Auth.On=true)
GET  /manager/channels (read-only business channel list; requires cluster.channel:r when Auth.On=true)
GET  /manager/conversations (recent conversation list; requires cluster.channel:r when Auth.On=true)
GET  /manager/messages (channel message list; requires cluster.channel:r when Auth.On=true)
POST /manager/messages/retention (message retention request; requires cluster.channel:w when Auth.On=true)
GET  /manager/connections (connection list; requires cluster.connection:r when Auth.On=true)
GET  /manager/connections/:session_id (connection detail; requires cluster.connection:r when Auth.On=true)
GET  /manager/nodes/:node_id/plugins (node-local plugin inventory; requires cluster.plugin:r when Auth.On=true)
GET  /manager/nodes/:node_id/plugins/:plugin_no (node-local plugin detail; requires cluster.plugin:r when Auth.On=true)
GET  /manager/plugin-bindings (UID/plugin binding list; requires cluster.plugin:r when Auth.On=true)
POST /manager/plugin-bindings (create/update UID/plugin binding; requires cluster.plugin:w when Auth.On=true)
DELETE /manager/plugin-bindings (remove UID/plugin binding; requires cluster.plugin:w when Auth.On=true)
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
uses the wired Slot runtime status reader for actual Slot Raft leader counts.
Node operation action hints are false because node lifecycle/scale-in operation
routes are intentionally not migrated in this phase.

`/manager/realtime-monitor` backs the unified web realtime monitor under
cluster operations. It parses chart `window`, optional `step`, optional
positive `node_id`, and `category` (`common`, `gateway`, `internal`, `message`,
`conversation`, `channel`, `control`, `slot`, or `node`), requires
`cluster.node:r` when manager auth is enabled, and delegates Prometheus plus
bounded `control_snapshot` reads to the app-wired realtime monitor provider.
When Prometheus is disabled or unavailable the route still returns HTTP 200
with an explicit monitor status so the web UI can show setup guidance instead
of rendering empty charts. This route does not read from the top collector or
any in-process dashboard ring buffer. PromQL is scoped to the app-managed
`wukongimv2` Prometheus job so obsolete `cmd/wukongim` metrics cannot be mixed
into the realtime cards. Conversation cards include a `conversationSync` stage
covering the `/conversation/sync` client experience, active-cache dirty age,
active flush health, and conversation authority pressure. Gateway cards cover
client ingress, active connections, async SEND queue usage, connection churn,
close-reason distribution, auth success/latency, SENDACK error rate, gateway
traffic, frame handling latency, async SEND batch shape, async auth pressure,
and transport queue/byte pressure. Internal network cards cover total and
split TX/RX transport traffic, RPC rate/success/error/inflight/tail latency,
dial success/latency, and TransportV2 queue/admission pressure. Message cards
cover SEND rate/SENDACK error rate, append commit rate/error and tail latency,
committed dispatch backlog/enqueue/overflow, online delivery rate/latency/fan-out,
delivery queue/admission pressure, retry rate/depth, route expiry, and path
error closure. Channel cards cover ChannelV2 append tail latency, active
runtime count, append batch shape, append error rate, channel writer admission
pressure, parked followers, activation rejections, reactor mailbox depth,
worker queue depth, PullHint error rate, and follower replication tail latency.
Slot cards cover Slot leader stability, proposal/apply rate and gap, proposal
admission rejects, leader-change rate, replica lag, and Slot scheduler
queue/inflight/task-latency pressure.

`/manager/runtime/workqueues` is backed by the `internalv2/app` top collector.
It is a forced runtime view of the local node only: it does not fan out to peer
nodes and does not depend on Prometheus. When the collector is not configured or
has not sampled enough data yet, the route returns `service_unavailable`.
The manager route preserves provider labels from the top collector; TransportV2
service aliases are registered with the service worker pools and resolved before
the HTTP response layer.

`/manager/slots` preserves the legacy list response shape for the web Slot list
view, including `node_id` filtering. It reads the same control snapshot through
`internalv2/usecase/management`; `runtime.preferred_leader_id` is the
Controller preferred leader from the assignment, while the selected node's
actual local Slot Raft leader is exposed as `node_log.leader_id` when
available. The route also returns the selected node's local Slot Raft role plus
commit/applied watermarks in `node_log` for the web status and log-height
columns. The response may include active task summaries and participant
progress for display only. `/manager/slots/leader-transfer-plan`
parses source/target selection, optional Slot allow-list, max task count, and
target policy, then delegates deterministic batch preview generation to
`internalv2/usecase/management`; it is read-only and requires Slot read
permission. `/manager/slots/leader-transfer-batch` accepts the same selection
plus `state_revision` and `plan_id` fences, delegates execution to the
management usecase, returns `202 Accepted` when at least one new Controller task
is created, and returns `200 OK` for existing/no-op-only outcomes.
`/manager/slots/:slot_id/leader-transfer` parses a `target_node`, delegates
snapshot/runtime validation to `internalv2/usecase/management`, returns
`202 Accepted` when a new Controller task is created, and returns `200 OK` for
no-op already-leader or existing-task responses. The single-Slot response
includes `target_node`, `preferred_leader`, `actual_leader`, `created`, and
`message` so operators can distinguish accepted work from no-op outcomes without
inspecting Controller internals. Slot detail and other operation routes remain
unmigrated.

`/manager/controller/logs` and `/manager/slots/:slot_id/logs` expose
newest-first distributed Raft log pages for the selected node. They parse
`node_id`, `limit`, and `cursor` at the HTTP boundary, delegate local/remote log
selection to `internalv2/usecase/management`, and return decoded payload
summaries only for inspection; the routes do not mutate Controller or Slot
state.

`/manager/controller/tasks*` exposes active ControllerV2 task state from the
local control snapshot. The HTTP layer validates `kind`, `status`, `slot_id`,
`node_id`, and `limit`, requires `cluster.controller:r` when manager auth is
enabled, and delegates projection/filtering to `internalv2/usecase/management`.
The route is read-only and intentionally omits completed task history because
completed tasks are removed from active cluster state.

`/manager/nodes/:node_id/slots/:slot_id/compact` is the explicit Slot Raft
operator action paired with the read-only Slot log page and list-row Slot Raft
status reads. It validates the selected node and physical Slot at the HTTP
boundary, delegates local/remote execution to the management usecase, and
returns one per-node/slot result with success, skip, or error state; it does
not fan out across Slot replicas.

`/manager/nodes/:node_id/controller-raft` exposes the selected node's
Controller Raft role, commit/apply watermarks, durable log/snapshot watermarks,
and latest compaction/restore status. The `compact` routes are explicit
operator actions separate from log paging: the node-scoped route forces one
node-local compaction attempt, while `/manager/controller-raft/compact` fans out
to Controller voter nodes through the management usecase and returns per-node
success, skip, or error results.

`/manager/app-logs*` exposes ordinary WuKongIM process logs under `WK_LOG_DIR`,
not Controller, Slot, Channel, or Raft logs. The routes use the fixed ordinary
application log sources owned by the application log reader (`app`, `warn`,
`error`, and `debug`), require an explicit positive `node_id`, and delegate
local-vs-remote node selection to `internalv2/usecase/management`. The stream
route emits lightweight NDJSON events for lines, rotations, heartbeats, and
reader errors without owning a long-running log runtime.

`/manager/diagnostics*` exposes internalv2 diagnostics tracing. The HTTP layer
parses trace/message/event query parameters, enforces the dedicated
`cluster.diagnostics` permissions, and delegates local-vs-remote fan-out plus
tracking-rule mutations to `internalv2/usecase/management`. These routes use
the internalv2 diagnostics store and do not import or query legacy
`internal/observability/diagnostics` state.

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

`/manager/nodes/:node_id/plugins*` exposes read-only plugin runtime inventory
for one selected node. The HTTP layer validates positive `node_id` values,
requires the dedicated `cluster.plugin:r` permission when manager auth is
enabled, and maps plugin status, hook methods, hook sync flags, process ID,
last-seen timestamp, and latest error text from
`internalv2/usecase/management`. Non-local node reads are routed below this
package through manager plugin node RPC. These routes do not start, stop,
reload, configure, or mutate plugin desired state.

`/manager/plugin-bindings` exposes cluster-authoritative UID/plugin bindings
used by Receive hooks. `GET` accepts exactly one selector: `uid` for
UID-owned binding reads, or `plugin_no` for plugin-centric pages when the
wired binding store supports that scan. `POST` and `DELETE` parse
`uid`/`plugin_no` from JSON bodies or query parameters, require
`cluster.plugin:w`, and delegate mutations to `internalv2/usecase/management`.
The HTTP layer owns only parsing, permission checks, and response DTO mapping;
durable ownership and timestamps stay below the access layer.

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
