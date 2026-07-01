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
POST /manager/nodes/join (node lifecycle join; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/activate (node lifecycle activation; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/onboarding/plan (bounded Slot onboarding preview; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/onboarding/start (bounded Slot onboarding task creation; requires cluster.node:w when Auth.On=true)
GET  /manager/nodes/:node_id/onboarding/status (active onboarding task status; requires cluster.node:r when Auth.On=true)
POST /manager/nodes/:node_id/onboarding/advance (bounded Slot onboarding task creation; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/scale-in/plan (bounded Slot scale-in drain preview; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/scale-in/start (mark node leaving for scale-in preparation; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/scale-in/drain (toggle target gateway drain mode; requires cluster.node:w when Auth.On=true)
POST /manager/nodes/:node_id/scale-in/remove (mark a fully drained node removed; requires cluster.node:w when Auth.On=true)
GET  /manager/nodes/:node_id/scale-in/status (fail-closed scale-in safety status; requires cluster.node:r when Auth.On=true)
POST /manager/nodes/:node_id/scale-in/advance (bounded Slot scale-in drain task creation; requires cluster.node:w when Auth.On=true)
GET  /manager/nodes/:node_id/diagnostics (read-only dynamic-node diagnostics; requires cluster.node:r when Auth.On=true)
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
GET  /manager/controller/task-audits (retained Controller task history; requires cluster.controller:r when Auth.On=true)
GET  /manager/controller/task-audits/:task_id/events (retained Controller task event timeline; requires cluster.controller:r when Auth.On=true)
GET  /manager/nodes/:node_id/controller-raft (node-local Controller Raft status; requires cluster.controller:r when Auth.On=true)
POST /manager/nodes/:node_id/controller-raft/compact (manual node-local Controller Raft compaction; requires cluster.controller:w when Auth.On=true)
POST /manager/nodes/:node_id/controller-voter/promote (online Controller voter promotion; requires cluster.controller:w when Auth.On=true)
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
PUT  /manager/nodes/:node_id/plugins/:plugin_no/config (node-local plugin desired config update; requires cluster.plugin:w when Auth.On=true)
POST /manager/nodes/:node_id/plugins/:plugin_no/restart (node-local plugin restart; requires cluster.plugin:w when Auth.On=true)
DELETE /manager/nodes/:node_id/plugins/:plugin_no (node-local plugin uninstall; requires cluster.plugin:w when Auth.On=true)
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
It exposes control-snapshot health evidence in each node's `health` object:
`fresh`, `freshness`, `runtime_ready`, report age/TTL, observed control and Slot
revisions, and an optional bounded `error_code`. `membership.schedulable` is the
shared health-gated placement predicate rather than an HTTP-only hint. It also
surfaces lightweight online/gateway runtime counters from the management
runtime-summary reader; unavailable per-node runtime summaries stay explicitly
marked `unknown` instead of failing the inventory response.
Node list action hints remain read-model hints; `can_drain` and `can_resume`
stay tied to the legacy node-operation routes and remain disabled in
internalv2 until those routes are migrated. Stage 5C gateway drain mode is
exposed through `/manager/nodes/:node_id/scale-in/drain` and scale-in status
instead of these node-list action hints. `can_promote_controller_voter` is a
read-model hint for active data nodes that can be considered for the dedicated
Controller membership write route. Node lifecycle writes are exposed as
separate join and activate routes under `cluster.node:w`.
`/manager/nodes/join` parses `node_id`, optional `name`, `addr`, and optional
`capacity_weight`, delegates to `internalv2/usecase/management`, returns
`202 Accepted` when the control writer creates state, and returns `200 OK` for
idempotent no-op results. `/manager/nodes/:node_id/activate` parses the target
node from the path, accepts an empty body, delegates to the same lifecycle
usecase, and returns `202 Accepted` when activation changes state after the
joining-node readiness gate passes. These routes do not seed node RPC, poll
peer startup, or perform Slot rebalance work. Lifecycle conflicts and
not-ready activation gates return `409 conflict`; not-ready responses preserve
the usecase's compact readiness detail in `message` so operators can see
whether transport reachability, control mirror catch-up, runtime readiness,
cluster ID, or revision fencing blocked activation. Missing activation targets
return `404 not_found`, and unwired control writers/readiness readers return
`503 service_unavailable`.

`/manager/nodes/:node_id/controller-voter/promote` is a Controller membership
write guarded by `cluster.controller:w`, not a generic node lifecycle write. It
parses the positive path `node_id` and optional `expected_revision`, delegates
all safety gates, target readiness checks, and control writes to
`management.App.PromoteControllerVoter`, returns `202 Accepted` when Controller
state changed, and returns `200 OK` for idempotent already-voter results.
Blocked safety gates return `409 conflict` with compact usecase detail, missing
targets return `404 not_found`, and unavailable cluster/control dependencies
return `503 service_unavailable`.

`/manager/nodes/:node_id/onboarding/*` exposes the first bounded Slot onboarding
surface for active data nodes. `plan`, `start`, and `advance` accept
`max_slot_moves`, delegate all planning and task creation to
`internalv2/usecase/management`, and never mutate Slot Raft or `DesiredPeers`
inside HTTP handlers. `start` and `advance` return `202 Accepted` only when at
least one Controller-backed `slot_replica_move` task is created; no-op previews
or no-create results return `200 OK`. `status` is read-only and reports active
replica-move tasks targeting the selected node. There is intentionally no
`cancel` route in this stage because no fenced Controller cancel writer exists.
The downstream flow is `SlotReplicaMoveWriter -> ControllerV2 slot_replica_move
task -> clusterv2 task executor -> Slot Raft learner/config-change flow -> final
ControllerV2 assignment commit`; HTTP never treats target learners as
`DesiredPeers` before that final commit.

`/manager/nodes/:node_id/scale-in/*` exposes scale-in preparation for
data nodes. `start` marks the target node `leaving` through
`management.App.MarkNodeLeaving`, causing future planners to stop assigning new
work to it. `drain` toggles the target node gateway's new-session admission
through `management.App.SetNodeDrainMode` and returns the target runtime
counters after the change; it is rejected unless the target is already a
durable Data-role, non-Controller `leaving` node, and it does not close existing
sessions. `status` delegates to `management.App.NodeScaleInStatus` and returns
every fail-closed safety bit from the control snapshot, target and eligible-node
health freshness, per-node runtime summaries, target gateway drain counters,
Slot runtime summaries, active Controller tasks, and bounded Channel runtime
metadata inventory. It includes bounded health blocker fields such as
`blocked_by_health`, `blocked_by_stale_revision`, `health_fresh`,
`health_freshness`, `observed_control_revision`, `required_control_revision`,
and `blocked_reasons` for operator diagnosis. `blocked_reasons` are bounded
machine-readable strings shared with the app metrics observer, which aggregates
scale-in blockers by reason only, deduplicated per node/control revision/reason,
and never by task, channel, UID, node address, or session. Channel inventory is
bounded by a per-page limit plus a total page budget and fails closed when the
budget is exceeded. `plan` and `advance` accept
`max_slot_moves` and delegate to
`management.App.PlanNodeScaleIn` / `AdvanceNodeScaleIn`; HTTP only creates
bounded Stage 3 `slot_replica_move` task intents through the usecase's
`SlotReplicaMoveWriter` path. Manager HTTP owns operator intent routing,
permission checks, and response mapping only; safety and lifecycle decisions
stay in the management usecase. `remove` delegates to
`management.App.MarkNodeRemoved`, which calls the control lifecycle writer only
when the same fail-closed status reports `safe_to_remove=true` and carries that
status revision as the final write fence; unsafe attempts and revision conflicts
return `409 conflict`. The downstream flow is:

```text
manager scale-in route
  -> management.App.MarkNodeLeaving / SetNodeDrainMode / NodeScaleInStatus / PlanNodeScaleIn / AdvanceNodeScaleIn / MarkNodeRemoved
  -> control snapshot + runtime summaries + gateway drain counters + ChannelRuntimeMeta inventory for status
  -> SlotReplicaMoveWriter
  -> Stage 3 slot_replica_move tasks
  -> NodeLifecycleWriter.MarkNodeRemoved for final safe removal
```

This scale-in surface cannot close existing gateway sessions, migrate Channel
replicas, clear Channel metadata, or cancel drain tasks. It can mark a node
removed only through the final `remove` route after `safe_to_remove=true`.
Unknown runtime, target/eligible-node health, or control-revision data keeps
status unsafe and keeps planning/advancement at no candidates rather than
guessing. Unknown Channel inventory keeps status unsafe, but HTTP still allows
Slot drain `plan`/`advance` while desired Slot peers contain the target so
operators can continue the first drain phase. Final `safe_to_remove` remains
false until target health is fresh, `alive`, and runtime-ready; live runtime
summaries have observed the required control revision; the target gateway is
in drain mode, no longer accepts new sessions, and reports zero gateway,
online, closing, and pending-activation counters; the remove route is the only
manager entrypoint that can submit the removed tombstone. A repeated remove on
an already-removed tombstone returns the writer's idempotent
`changed=false` result even when the original safe-remove fence is stale.

`/manager/nodes/:node_id/diagnostics` is the read-only root-cause surface for
dynamic add/remove operations. It parses bounded task, audit, and Slot evidence
limits, delegates all evidence assembly to
`management.App.DynamicNodeDiagnostics`, and maps the usecase response to a
single manager JSON document with `node`, optional `scale_in` or `onboarding`,
`active_tasks`, `task_audits`, `slots`, `summary`, `sources`, and `warnings`.
The handler does not create Controller tasks, retry task failures, mutate Slot
assignments, change gateway drain mode, or mark nodes removed. When manager
auth is enabled it uses the same `cluster.node:r` permission as node inventory
and scale-in status because it only reads cluster-node lifecycle evidence.

`/manager/realtime-monitor` backs the unified web realtime monitor under
cluster operations. It parses chart `window`, optional `step`, optional
positive `node_id`, and `category` (`common`, `gateway`, `internal`, `message`,
`conversation`, `channel`, `database`, `control`, `slot`, or `node`), requires
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
Database cards cover message DB commit request tail latency, commit error rate,
commit queue pressure, physical commit tail latency, and grouped commit batch
shape. Disk usage is not exposed here until internalv2 owns a database disk
sampler.
Slot cards cover Slot leader stability, proposal/apply rate and gap, proposal
admission rejects, leader-change rate, replica lag, and Slot scheduler
queue/inflight/task-latency pressure.
Node cards cover runtime workqueue pressure, process CPU, RSS memory,
goroutine count, and Go GC pause/rate/CPU/heap-goal pressure while preserving
per-node series for the global cluster view.

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

`/manager/controller/task-audits*` exposes bounded retained ControllerV2 task
history from the internalv2 task audit reader. The HTTP layer validates `kind`,
`status`, `slot_id`, `node_id`, `keyword`, and `limit`, requires
`cluster.controller:r` when manager auth is enabled, and delegates list and
exact task timeline reads to `internalv2/usecase/management`. The route is
read-only and uses applied Raft index ordering from the backend audit store, so
completed or failed historical tasks remain inspectable after they disappear
from active control state.

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
for the web connection page. The list route accepts optional `node_id` and
`limit` query parameters, with the connection page bounded to at most 100 rows
per request. In internalv2 it reads the owner-local online registry for local
or empty `node_id` filters, routes remote `node_id` filters through
internalv2 node RPC, and maps session details such as UID, device, listener,
state, timestamps, and addresses when the gateway session handle exposes them.
If a remote connection reader is not wired, non-local filters return
`service_unavailable`.

`/manager/nodes/:node_id/plugins*` exposes node-scoped plugin inventory,
detail, desired-config update, restart, and uninstall operations for one
selected node. The HTTP layer validates positive `node_id` values and non-empty
plugin numbers, requires `cluster.plugin:r` for reads and `cluster.plugin:w`
for lifecycle writes when manager auth is enabled, caps config update bodies at
1 MiB, and accepts only JSON objects for desired config. Response DTOs map
plugin status, hook methods, hook sync flags, process ID, last-seen timestamp,
latest error text, config templates, redacted desired config, and desired-state
timestamps from `internalv2/usecase/management`. Local-vs-remote node targeting
is routed below this package through manager plugin node RPC; the HTTP layer
does not inspect plugin files or call the plugin runtime directly.

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
