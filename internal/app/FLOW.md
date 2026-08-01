# internal/app Flow

## Responsibility

`internal/app` is the only composition root for the new skeleton. It wires
phase-1 config, the internal root logger, `pkg/cluster`, the message
usecase, the channel management usecase, the user management usecase, the
conversation list usecase, the manager management read usecase, the optional
delivery usecase/runtime, the presence usecase, the gateway handler, the optional HTTP API runtime, the optional
dedicated manager HTTP runtime, the optional plugin runtime/usecase/hook
worker, the optional webhook runtime, the optional Prometheus metrics registry, the
optional app-managed Prometheus child process, and the optional gateway runtime. The phase-1
runtime supports single-node clusters and static multi-node clusters for the
`SEND -> SENDACK` write path, legacy-compatible channel/user metadata
management, UID connection-route authority, channel-authority write routing,
per-channel authority writers, UID recipient authority inside post-commit effects,
conversation authority active cache/list reads, and opt-in local online
delivery.

This package owns lifecycle ordering. Business rules stay in usecase packages,
entry-protocol details stay in access packages, and concrete runtime adapters
stay in infra packages.

`NewIssueAgentOperations` and `NewReviewAgentOperations` are standalone
GitHub-Actions composition roots. They do not call `New`, join a WuKongIM
cluster, or start product runtimes. Review Agent composition keeps fresh
GitHub reads, deterministic lifecycle, credential-free verification, signed
state writes, and Review/Check publication behind separate role boundaries.
Terminal `collect_only` verification reads and validates the frozen ledger
without constructing the command executor used by the earlier baseline job.
It also owns strict loading and cross-layer validation of the protected Review
Agent policy before projecting narrow lifecycle and verifier configurations.

## Construction Flow

```text
New(Config)
  -> derive effective cluster config from Config.Cluster with top-level fallbacks
     including ChannelMessageRetention -> cluster.ChannelRetention physical
     cleanup settings; background physical GC remains disabled by default
  -> create a root logger from Config.Log unless a test/harness override is supplied
  -> attach an always-on, low-cardinality Channel runtime summary collector;
     it aggregates per-reactor Leader/Follower counts and stays unknown until
     every configured reactor has reported both roles, independently of
     Prometheus metrics and the optional Top collector
  -> create metrics registry when Observability.MetricsEnabled=true and attach
     runtime observers for metrics/logging
     (gateway runtime pressure, Slot scheduler/proposal/apply-gap/leader-election pressure and low-cardinality preferred-leader reconcile decisions/strict-wait latency, Controller Raft step queue/bounded outbound send queue/apply gap, Transport service RPC totals/latency and observed write-batch shape, Channel runtime append/replication/PullHint/PullBatch/leader-Pull/runtime pressure stages, message DB grouped commit pressure, and online delivery)
     plus direct ants/v2 pool occupancy gauges for instrumented runtime pools
     plus canonical Online Delivery local and remote owner-push attempts on the
     bounded delivery push metric families, conversation list request latency/page-shape metrics, conversation
     authority admit/list/cache-pressure/handoff counters, conversation active
     cache gauges, dirty-mutation counters, persisted/cleared/requeued/superseded
     flush conservation counters, fair dirty-queue and bounded dirty-age-index
     gauges, accepted/rejected admission cache-lock wait/hold histograms, split clear lock-wait/apply
     flush-stage histograms, and pressure-wakeup
     lifecycle metrics, channel append and post-commit
     counters, presence authority expiry cost/index gauges and bounded owner
     touch-flush route/chunk/target-group counters plus aggregate exact-target
     endpoint lookup path/outcome/retry metrics, recipient authority batch
     calls/items/physical-targets/duration, online delivery runtime
     queue/admission/process metrics plus configured worker capacity and current
     in-flight command gauges, and owner-local ACK batch bind/finish shape,
     rejection, rollback, and duration metrics,
     plugin PersistAfter and Receive hook enqueue/invoke counters and
     histograms, and synchronous plugin Send hook invoke counters and histograms
     plus node lifecycle gauges/counters from control snapshots and scale-in
     status blockers (lifecycle state, health freshness, health report age,
     onboarding task state, membership revision, and bounded blocker reasons)
     plus node resource pressure gauges backed by the local resource sampler;
     when Top.APIEnabled=false this sampler runs only for Prometheus metrics and
     does not expose the Top snapshot provider
  -> create the top collector when Top.APIEnabled=true and attach node-local
     runtime observers for Channel runtime, storage commit, delivery, Slot scheduler,
     Controller Raft, and transport pressure independently of Prometheus
     metrics; the collector also samples local process CPU, RSS/VMS memory,
     goroutine count, and thread count via gopsutil, and pulls
     `cluster.Node.StorageMetricsSnapshot` into Pebble and aggregate channel
     entry ownership/reclamation metrics under the fixed `channel_log` label;
     it keeps a bounded
     in-memory sticky alert window for readiness, pressure, sendack-error, and
     gateway session-error signals with compact evidence facts so `wkcli top`
     can show why active or recently resolved warnings fired; Transport
     service pressure uses aliases registered with service worker pools so
     operator views do not expose raw `service_<id>` labels; top remains an
     in-memory collector and still runs when Observability.MetricsEnabled=false
  -> when Observability.Prometheus.Enabled=true:
       validate that the API metrics endpoint is enabled and create a child
       Prometheus runtime that writes prometheus.yml under the configured
       Prometheus data dir, extracts the embedded Prometheus binary when no
       external binary path is configured, and scrapes the node API /metrics endpoint
     Manager realtime monitor queries use `Prometheus.QueryBaseURL` when an
     externally managed Prometheus service is configured; otherwise they use
     the app-managed Prometheus HTTP API only when `Prometheus.Enabled=true`.
     They scope PromQL to the generated `wukongim` job and can optionally add
     a node-scoped filter; Channel runtime monitor PromQL prefers promoted
     `wukongim_channel_*` metric families and falls back to legacy
     `wukongim_channelv2_*` families at the query boundary; unified realtime
     monitor snapshots also pass the selected node into bounded control
     snapshot reads. The database monitor category is Prometheus-only and uses
     internal message DB commit request,
     grouped commit stage, commit runtime queue, and Pebble engine snapshot
     metrics, plus canonical channel entry, caller lease, background pin, and
     reclamation totals. These storage metrics never use channel IDs as labels.
     The node monitor category keeps per-node Prometheus series for
     process CPU, RSS memory, goroutines, and Go GC pause/rate/CPU/heap-goal
     pressure so global views can show the highest-pressure node without
     dropping node labels.
     Queries do not read the top collector's in-process dashboard ring buffers;
     the hidden collector only refreshes process resource, Pebble snapshot, and
     channel entry ownership gauges/counters for Prometheus when metrics are
     enabled.
     The manager node runtime summary reads the independent always-on Channel
     runtime collector and returns active total, Leader, Follower, and unknown
     fields without scanning loaded channel maps.
  -> when Observability.Diagnostics.Enabled=true:
       create a bounded node-local diagnostics store, runtime tracking rules,
       sampler, and sendtrace sink; attach PreferredLeader reconciliation
       diagnostics that retain explicit physical Slot, actual/preferred leader,
       Raft term, and config epoch fields while retaining recovery-to-match
       transitions, suppressing steady matches, and coalescing identical
       30-second repeats; install the process-wide sendtrace sink
       and expose local diagnostics debug APIs only when
       Observability.DebugAPIEnabled=true
  -> when an effective node data dir is configured:
       create the app-owned Controller task audit runtime at
       `observability/task-audit/controller-tasks.jsonl`, combine its
       bounded nonblocking `TaskTransitionObserver` into cluster control
       config, and keep JSONL retention local to internal observability
       rather than `pkg/db/meta` or legacy `pkg/controller`
  -> create cluster.Node when no ClusterRuntime override is provided
  -> when the cluster exposes channel metadata APIs:
       create internal/usecase/channel with an infra/cluster Slot metadata adapter
       and the configured large-group subscriber threshold, wire a subscriber
       mutation observer that updates channelappend channel-state caches, and,
       when exposed by the cluster, wire the same adapter as the UID-owned
       membership projection index
  -> when the cluster exposes conversation metadata reads:
       create an infra/cluster read adapter for channel-owned last visible
       message reads and DB-only UID-owned active conversation pages
       when the cluster also exposes conversation authority routing and metadata
       writes, create one local authority route facade backed by
       runtime/conversationactive.Manager plus one routed
       ConversationAuthorityClient, register the conversation authority RPC
       adapter, create the route-authority lifecycle, and use that client as
       the conversation list and delete Store while keeping the read adapter as
       Messages, durable state reads, and read-cursor writes
  -> when the cluster exposes cluster Slot metadata subscriber APIs, create
     a delivery metadata adapter backed by real storage for bench setup,
     and channelappend subscriber scans
  -> when the cluster exposes presence routing:
       create owner boot ID, online.Registry, runtime/presence.Directory,
       infra/cluster.PresenceAuthorityClient, usecase/presence.App,
       and access/node presence RPC adapter
       register the presence authority and owner-action RPC handlers on cluster
       create the presence touch worker
  -> register the manager connection RPC handler when node RPC and local control
     snapshots are available, exposing this node's owner-local online registry
     and gateway admission drain primitive to peer manager readers/operators;
     runtime summaries include active and pending owner-local online counts plus
     gateway session/admission counters. The RPC receiver uses a local-only
     manager connection service: connection reads and summaries may reuse the
     management read usecase, while `set_drain_mode` directly toggles this
     node's gateway admission after the origin manager usecase has already
     checked durable scale-in safety.
  -> register the manager distributed log RPC handler when node RPC and local
     log readers are available, exposing this node's Controller/Slot Raft log
     pages to peer manager readers
  -> register the manager Controller Raft RPC handler when node RPC and local
     Controller Raft operations are available, exposing this node's Controller
     Raft status and local compaction attempt to peer manager operators
  -> register the manager Slot Raft RPC handler when node RPC and local Slot
     Raft operations are available, exposing this node's selected local Slot
     compaction attempt to peer manager operators
  -> create the app-owned ordinary application log reader from `Log.Dir`;
     register the manager app-log RPC handler when node RPC is available so
     peer manager readers can inspect this node's fixed application log sources
     without exposing local paths
  -> register the manager node-config RPC handler when node RPC is available,
     exposing this node's redacted allowlisted effective startup configuration
     snapshot to peer manager readers without exposing raw secrets or local
     config file paths
  -> register the manager channel RPC handler when node RPC and channel metadata
     scans are available, exposing this node's channel list pages to peer
     manager readers
  -> register the manager message retention RPC handler when node RPC and
     Channel runtime retention metadata APIs are available, exposing this node's
     channel-leader logical compaction boundary advance path to peer manager
     operators without allowing recursive forwarding on the receiver
  -> register the manager latest-message RPC handler when the shared message
     store and node RPC are available, exposing only this node's indexed local
     replicas so the origin manager can perform bounded cluster fan-out and
     replica deduplication
  -> create the app-level DB Inspect reader from derived node-local storage
     roots when message and Slot metadata DB paths are available; register the
     manager DB inspect RPC handler when node RPC is available so peer manager
     readers can inspect this node's local DB diagnostics
  -> register the manager diagnostics RPC handler when node RPC and the local
     diagnostics store are available, exposing this node's trace/message/event
     diagnostics reads and tracking-rule mutations to peer manager readers
  -> register the manager task audit RPC handler when node RPC and the local
     Controller task audit reader are available, exposing this node's retained
     task history and per-task timeline to peer manager readers without
     mutating Controller state
  -> register the node lifecycle RPC handler when node RPC and the management
     lifecycle writer are available, exposing seed JoinNode and readiness probe
     requests to joining peers; when seed-join config is present, create the
     app seed join loop that resolves configured seed addresses through the
     local control mirror and retries JoinNode until this node appears as
     joining or active; app lifecycle treats that observed membership record as
     an admission gate before starting HTTP, manager, gateway, or worker
     runtimes; seed-join startup deliberately skips the normal Slot write-ready
     gate only while the local mirrored membership state is `joining`, because
     a pre-activation joining node is not yet assigned writeable Slot routes,
     while `/readyz` still waits for cluster and gateway startup before
     reporting ready. Once the node is mirrored as `active`, restarts and
     readiness probes use the normal Slot write-ready gate.
  -> when the cluster exposes user metadata APIs:
       create internal/usecase/user with an infra/cluster Slot metadata
       adapter, owner-local online registry, optional presence lookup, and the
       channel metadata adapter as the system UID store
  -> when Delivery.Enabled=true:
       create the canonical runtime/delivery Runtime with bounded plan
       admission, exact-target presence resolution, owner grouping, narrow
       retry, pending-RECVACK tracking, and plugin/webhook offline observers
       supply infra/delivery.LocalSessionWriter for final exact owner-local
       packet writes and the node RPC client for remote owner pushes
       attach delivery observers for metrics, pressure, ACK state, and bounded
       terminal error logging
       expose gateway RECVACK/session-close feedback through the temporary
       usecase/delivery facade; channelappend remains the sole plan producer
       register only the owner-push RPC handler when node RPC is available
  -> when Plugin.Enable=true (default unless WK_PLUGIN_ENABLE=false is set):
       wire a node-local PDK-compatible plugin runtime with a Unix host RPC
       socket, the lifecycle plus /message/send, /channel/messages,
       /cluster/config, /cluster/channels/belongNode, and
       /conversation/channels, and /plugin/httpForward host RPC
       adapter, the v2 plugin usecase, and a bounded plugin hook worker for
       PersistAfter plus Receive side effects; pass
       WK_PLUGIN_FAIL_OPEN into the synchronous Send hook usecase; adapt the
       node-local plugin desired-state store into the usecase so StartPlugin can
       return node id, sandbox dir, startup config, and ConfigTemplate metadata;
       wire
       plugin-origin /message/send back through the v2 message usecase with the
       default system UID fallback; wire /channel/messages to the cluster
       committed-message reader when available; wire cluster host RPCs to the
       cluster control snapshot and Channel runtime append-authority readers when
       available; wire /conversation/channels to the cluster active
       conversation row reader when available without last-message joins; wire
       positive toNodeId /plugin/httpForward calls through the cluster manager
       plugin RPC forwarder; wire Receive hook binding selection to
       cluster-authoritative UID plugin bindings when available; attach the
       plugin hook metrics observer when metrics are enabled; expose durable
       commit PersistAfter events to
       channelappend, expose each durable offline recipient batch from the
       Online Delivery runtime to Receive hooks, and
       register the manager plugin RPC handler when node RPC is available so
       peer managers can inspect or mutate this node's plugin lifecycle state
       and invoke this node's local /plugin/route hook for forwarded plugin
       HTTP requests
  -> when Webhook config is enabled:
       create the node-local webhook runtime with bounded workqueue admission,
       finite retry, and an HTTP sender; wire webhook adapters into
       channelappend's durable post-commit PersistAfter sink, the Online
       Delivery runtime's batch offline-recipient observer, and the presence
       online-status observer
       Plugin hooks and webhook sinks coexist on the same side-effect surfaces.
       Webhook failures are best-effort side effects and must not affect
       SENDACK, durable append, recipient delivery, or conversation active
       admission.
  -> when the cluster exposes Channel runtime append plus channel append authority:
       create channelappend.Group with hash-sharded per-channel authority writers,
       cluster ChannelAppender, node-scoped message IDs, subscriber source,
       cluster-backed idempotency lookup when the cluster exposes it,
       infra/cluster recipient authority resolver adapter, conversation active-batch admitter,
       optional canonical Online Delivery plan enqueuer, optional plugin/webhook
       PersistAfter enqueuers, append metrics observer, and shared append/post-commit worker
       pools
       create channelappend.Router for local authority admission and remote
       channel-authority forwarding
       register Channel Append RPC so remote nodes can submit to the local
       authority writer group
  -> create message.App with channelappend.Router, cluster channel metadata
     permission reads, system UID cache, configured message permission switches,
     the optional plugin Send hook usecase when plugins are enabled, the
     cluster committed message reader when exposed for channel message sync, and
     the cluster message event projection store when exposed for `/message/event`
     and `/channel/messagesync` event metadata enrichment
  -> when the cluster exposes unified conversation metadata writes and Channel runtime
     committed reads, create internal/usecase/cmdsync with one
     infra/cluster CMDSyncStore over ConversationKindCMD rows
  -> create access/gateway.Handler with the message facade and activation-timeout-wrapped presence usecases
  -> create access/api.Server with the embedded chat Demo, channel, user,
     message, CMD sync, and conversation usecases, legacy route address lookup
     derived from gateway listeners and
     static cluster voters, optional debug snapshots, optional bench presence
     snapshot controller, and real benchmark channel/subscriber data writer when
     API.ListenAddr is configured
  -> create access/manager.Server with the embedded Manager Web UI and static
     manager JWT login when Manager.ListenAddr is configured; the same listener
     serves SPA routes and same-origin `/manager/*` requests without a separate
     web process; when the cluster exposes local control
     snapshots, attach internal/usecase/management for `/manager/nodes`,
     `/manager/nodes/:node_id/config`, `/manager/slots`,
     `/manager/channels`, `/manager/channel-runtime-meta`,
     `/manager/conversations`, `/manager/messages`, `/manager/connections*`,
     `/manager/nodes/:node_id/plugins*`, `/manager/plugin-bindings`,
     `/manager/users*`, and
     `/manager/system-users*`;
     business channel detail/member operations cross the composition root
     through `managerChannelBusinessOperator`, which adapts management-owned
     DTOs to the sibling channel usecase without coupling those usecases;
     channel, conversation, message, and user lists are attached only when the
     cluster also exposes the corresponding metadata/message page scans, while
     local connection list/detail reads use the owner-local online registry,
     remote `node_id` connection reads route through the manager connection
     node RPC reader, remote channel list reads route through the manager
     channel RPC reader, Controller/Slot log pages route through the manager
     log reader, node-scoped Controller and Slot Raft compaction operations
     route through their manager operator adapters, Slot leader transfer
     requests wire the management `LeaderTransfer` and `SlotRuntimeStatus`
     ports when cluster exposes them, use local Slot Raft runtime status for
     preflight, and submit the validated intent to cluster control, bounded
     node onboarding requests wire the management `SlotReplicaMove` port when
     cluster exposes Controller-backed staged replica-move writes and submit
     only `slot_replica_move` task intents, plugin
     inventory and lifecycle mutations use the local v2 plugin usecase for the
     local node and route peer `node_id` reads/writes plus positive-node plugin
     HTTP forwarding through the manager plugin RPC path, plugin binding
     mutations use cluster
     UID-owned Slot metadata when that facade is exposed, ordinary
     application log
     sources and pages use the app-owned
     local reader for the local node and route peer `node_id` reads through the
     manager app-log RPC reader, node config reads use the app-owned redacted
     effective-config provider for the local node and route peer `node_id`
     reads through the manager node-config RPC reader, DB Inspect reads use the
     local app inspect reader for empty or
     local `node_id` and route non-local `node_id` through the manager DB
     inspect node RPC reader, user writes reuse the internal user usecase and
     optional presence owner-action routing, unscoped message reads fan out to
     node-local latest-message indexes and deduplicate replicas, and message
     retention requests use
     the Slot-backed management retention adapter when the cluster exposes
     channel runtime metadata reads, committed message reads, and fenced
     retention advances; otherwise retention returns unavailable; diagnostics
     trace/message/event queries and
     tracking-rule mutations use the internal diagnostics store locally and
     route selected non-local nodes through the manager diagnostics RPC path;
     node lifecycle join/activation requests wire the management lifecycle
     writer when cluster exposes Controller-backed lifecycle writes, keeping
     validation in the management usecase and durable membership mutation in
     cluster control; Controller task audit list and event timeline reads
     use the app-owned JSONL task audit reader when it is available;
     when `Top.APIEnabled` creates a top collector,
     attach the local top provider so `/manager/runtime/workqueues` can expose
     local runtime pressure; attach the app as the read-only startup webhook
     config snapshot provider for `/manager/webhooks/config`; attach one
     Prometheus-backed realtime monitor
     provider so `/manager/realtime-monitor` can expose business-path and
     cluster-operations card series, including Slot proposal admission,
     leader-change, replica-lag, and scheduler pressure cards, category counts, explicit
     disabled/unavailable source states, and bounded `ListNodes`/`ListSlots`
     control snapshots through the management usecase; the `goroutines`
     category combines Prometheus node/module history with process-wide local
     snapshots and an eight-worker, per-node-timeout Manager Goroutine RPC
     fan-out, coalescing concurrent reads and caching successful peer reads for
     two seconds. Global refreshes evict removed-node entries and the cache is
     hard-capped at the same 256-node response bound; the realtime monitor
     does not read from `topCollector`
  -> when normal-mode Manager is configured, compose one Operations MCP endpoint on the
     same listener: Controller desired-state reader/writer, token verifier,
     fixed observation service, per-node call control/audit, owner forwarding,
     aggregate audit reader, and target pprof RPC share one registered typed
     node RPC; every Manager mounts `/mcp`, while only the Controller-selected
     owner executes tools
  -> create pkg/gateway.Gateway with WKProto CONNECT authentication only when listeners are configured
```

## Product Runtime Details

Detailed product-runtime composition, Online Delivery, channelappend,
conversation, and SEND flows live in
[`FLOW_PRODUCT_RUNTIME.md`](FLOW_PRODUCT_RUNTIME.md). This file remains the
canonical index for package-wide construction and lifecycle ordering.

## Lifecycle Flow

```text
Start(ctx)
  -> when Log.Console=true, write one bounded human-facing "Starting node" line;
     ANSI color is enabled only for an interactive terminal
  -> cluster.Start(ctx)
  -> task audit startup backfill: append one snapshot event for each active
     Controller task in the local control snapshot; failures are logged and
     do not block service startup
  -> seed join loop Start(ctx): retry JoinNode against stable-order seeds when seed-join config is present
  -> wait for cluster write routing when the cluster runtime exposes route snapshots; the gate also runs the cluster write probe, which proves Slot metadata writes and Channel runtime placement data-node candidates before gateway SEND admission
  -> conversation authority route lifecycle Start(ctx): watch route authorities and seed current targets
  -> conversation active flush worker Start(ctx): persist dirty active rows
     periodically or on a coalesced cache-pressure wakeup
  -> presence touch worker Start(ctx)
  -> plugin runtime Start(ctx): open the host RPC socket, scan local plugins, and start enabled processes
  -> plugin PersistAfter worker Start(ctx): accept durable commit side effects before channel append opens
  -> webhook runtime Start(ctx): accept post-commit webhook side effects before producers open
  -> Online Delivery runtime Start(ctx): open bounded plan admission and workers
  -> channel append group Start(ctx): open local channel-authority writer admission
  -> api.Start()
  -> manager.Start()
  -> prometheus.Start(ctx): write prometheus.yml and start the child Prometheus process
  -> gateway.Start()
  -> retain the structured internal.app.started event in app.log and render one
     aligned console summary from observed bound addresses, followed by Ready duration

Any component start failure
  -> render one bounded console failure with component and reason
  -> retain the full structured internal.app.lifecycle_start_failed event in error.log
  -> rollback already-started components in reverse order

Any App construction failure
  -> stop and unregister constructor-owned ChannelAppend pools
  -> restore the diagnostics sink and close construction-time audit resources

Stop(ctx)
  -> restore diagnostics sendtrace sink
  -> when Start never completed, stop constructor-owned ChannelAppend pools
     and wait only for post-baseline managed activity before returning
  -> gateway.Stop()
  -> prometheus.Stop(ctx)
  -> manager.Stop(ctx)
  -> api.Stop(ctx)
  -> top.Stop(ctx)
  -> channel append group Stop(ctx): close admission and wait for its single
     background graceful drain of accepted appends, handoff reservations,
     post-commit effects, and retry ownership
     if this caller's context expires before that drain completes:
       return the stop error immediately
       keep delivery, webhook, plugin, conversation, presence, seed-join, cluster,
       and controller-task-audit dependencies running
       retain their started flags so a later Stop(newCtx) waits for the same
       channel append drain and then resumes this dependency shutdown sequence
  -> Online Delivery runtime Stop(ctx): close admission and drain accepted plans
  -> webhook runtime Stop(ctx): stop accepting new webhook side effects after producers drain
  -> plugin PersistAfter worker Stop(ctx): stop accepting new side effects after channel append drains
  -> plugin runtime Stop(ctx): stop plugin processes and close the host RPC socket
  -> conversation active flush worker Stop(ctx): cancel periodic flush and persist remaining dirty active rows
  -> conversation authority route lifecycle Stop(ctx): cancel authority watcher
  -> presence touch worker Stop(ctx)
  -> seed join loop Stop(ctx): cancel pre-membership JoinNode retries
  -> cluster.Stop(ctx)
  -> controller task audit Stop(ctx): drain queued audit events and close the
     JSONL file after the Controller runtime can no longer emit observer calls
```

The app always installs the process-wide `pkg/goroutine` registry, passes it to
Cluster and Gateway configuration, projects it into the management goroutine
read model for Manager node RPC readers, exposes the concrete snapshot only
through the local API debug hook, and registers its Prometheus collector with constant node
identity labels. Metrics and Prometheus history remain configurable, but
goroutine lifecycle accounting has no disable switch.
After component shutdown completes, `App.Stop` waits each fixed goroutine
module with the caller context. A deadline returns bounded live task evidence
instead of silently reporting a clean stop. The wait is relative to the
process-registry launch/registration baseline captured before App-owned runtimes are constructed,
so pre-existing process tasks are not reassigned to this App.

`Start` and `Stop` are serialized by a lifecycle mutex. If API, manager, Prometheus, or gateway
startup fails after the cluster starts, `Start` attempts rollback in reverse
order; if rollback fails, state remains retryable so a later `Stop` can clean up.
Rollback uses the same channel-append drain boundary as ordinary Stop: a rollback
deadline at that boundary returns without closing post-commit dependencies, and
a later Stop with a fresh context continues the existing drain before closing
them. Entry runtimes already stopped before the boundary remain stopped.
The startup console is presentation-only: it is disabled with `Log.Console=false`,
does not add a configuration surface, and does not replace structured lifecycle
events in rolling files. API, Demo (`/demo/` on the API listener), manager, metrics,
the absolute loaded TOML path, data, and arbitrary named gateway listeners are
rendered from the same post-start snapshot used by lifecycle logs. Environment-only
startup remains explicit as `environment only`, and missing optional services remain
explicit as `disabled`. The loaded path is runtime display metadata and is not added
to the manager startup-config snapshot.
When `Plugin.Enable=true` (the default unless `WK_PLUGIN_ENABLE=false` is set),
the app wires the PDK-compatible node-local plugin
runtime, desired-state store adapter, minimal lifecycle host RPC adapter, v2
plugin usecase, and bounded PersistAfter worker before channelappend. The
channelappend group receives the PersistAfter enqueue port and a batch-capable
offline-recipient observer. One offline recipient batch becomes one plugin
worker admission and one owned copy of its payload and UID slice. The plugin
usecase snapshots eligible running Receive plugins once per batch, returns
before binding reads when none exist, and then preserves ordered per-UID binding,
dedupe, and invocation semantics only for bound recipients. The legacy scalar
observer and worker interfaces remain as compatibility fallbacks. Plugin runtime
and hook workers start before channelappend and stop after channelappend drains,
so accepted durable commits can enqueue plugin side effects until the append
runtime is stopped. Desired plugin config remains node-local in this phase and
is applied by the v2 plugin usecase during startup, local config updates, and
hook candidate selection.
When webhook delivery is enabled, the app also wires a node-local bounded
workqueue runtime with an HTTP sender before delivery and channelappend
producers open. Channelappend and presence see only small adapter ports:
post-commit PersistAfter, batch offline recipient observation, and online-status
observation. Webhook queue admission, retries, and HTTP failures remain
best-effort and do not change SENDACK, durable append, plugin hooks,
conversation active admission, or owner delivery.
The Online Delivery runtime drains accepted plans before it stops. Retryable
owner routes are narrowed and retried inside the plan's bounded execution
window. Stale pending recvacks expire during owner-local push activity. The runtime admits at
most one full pending-ack scan per second and never overlaps scans; ordinary
pushes, binds, and Recvacks therefore do not pay an O(pending acks) sweep on
every owner batch. The tracker uses second-resolution delivery timestamps, so
under an advancing clock and continuing push activity the gate adds at most one
scheduling interval before the next stale-entry scan.

## Presence Touch Worker

```text
cluster.RouteAuthorityEvent
  -> if local node becomes authority:
       runtime/presence.Directory.BecomeAuthority(target with route revision, Slot config epoch, Slot leader term, diagnostic authority epoch)
  -> if another node becomes authority:
       Directory.LoseAuthority(hashSlot)

periodic flush
  -> pull current route authorities from the cluster snapshot and repair missed watch events
  -> runtime/presence.Directory.ExpireRoutesDetailed(now, routeTTL) and observe
     one successful expiry pass with duration, due buckets, examined/expired
     routes, and remaining expiry-index route/bucket counts
  -> repeatedly drain owner-local dirty routes through online.Registry.DrainTouched,
     requesting min(touchBatchSize, remaining max-routes-per-flush budget);
     the default total budget is 65,536 routes per flush
  -> for each chunk, preserve first-seen UID order, deduplicate UIDs, and resolve
     all current UID authority targets with one aligned batch route lookup
  -> requeue every route for an unresolved UID; group successful routes in
     first-seen order by the complete fenced RouteTarget and call
     PresenceAuthorityClient.TouchRoutesTo sequentially
  -> stop after a short drain, exhausted total route budget, or context
     cancellation; cancellation requeues every already-drained unsent route
  -> accumulate unresolved, failed-target, and canceled-unsent route identities
     and call online.Registry.RequeueTouched only after the flush loop exits
  -> observe exactly one touch-flush summary across every return path with
     route-based drained/resolved/sent/requeued counts, chunk and target-group
     counts, duration, and whether drained work reached the per-flush budget
```

The app worker has one authority watch loop and one periodic touch loop. It does
not scan or replay owner-local active sessions when authority changes, and it
does not create per-hash-slot workers. Authority event ordering first compares
route revision, then Slot config epoch, then Slot leader term, and only uses
the authority epoch as a diagnostic tie-breaker for the same distributed
identity; the periodic loop pulls the current authorities so startup races or
dropped watch events self-heal. Delaying failed-route requeue until the whole
flush exits prevents the same route from being drained again inside that flush
and repeatedly consuming its bounded route budget; activity arriving during a
flush may remain dirty for the next periodic round.
Touch-flush result labels are bounded to `success`, `partial`, `canceled`,
`empty`, and `unavailable`. Context cancellation takes precedence, any
requeued route makes a non-canceled flush partial, an available flush with no
drained routes is empty, and missing owner-local or authority dependencies are
unavailable. Expiry and touch metrics contain only node labels plus these fixed
result/stage labels; they do not expose UID, session, hash-slot, or target
identity. A context canceled before the flush starts still emits one canceled
touch summary but does not run or observe expiry.

## Conversation Active Flush Worker

```text
periodic tick or coalesced cache-pressure wakeup
  -> derive an AuthorityFlushTimeout-bounded attempt context
  -> conversationAuthority.FlushActiveRows(attemptCtx, AuthorityFlushBatchRows)
  -> runtime/conversationactive.Manager selects dirty rows with version fencing
  -> batch-read durable conversation rows for receiver-only cooldown filtering
  -> skip receiver-only ActiveAt updates inside AuthorityActiveCooldown
  -> store.TouchConversationActiveAtBatch persists remaining ActiveAt/ReadSeq/UpdatedAt
  -> after a successful pressure-cycle attempt with cleared rows, the Manager
     requeues one coalesced wakeup while dirty rows remain above the 70% low
     watermark
  -> when a selected pressure batch clears no dirty marker while retryable rows
     remain, the app worker schedules one cancellation-safe delayed retry with
     bounded exponential backoff from 25ms to 250ms; progress, no work, or an error
     cancels that retry and returns continuation ownership to the normal
     Manager pressure signal or periodic tick

cache admission
  -> keep the latest receiver activity visible in cache while suppressing only
     its durable dirty work against a separately tracked clean ActiveAt baseline
     strictly inside AuthorityActiveCooldown
  -> at 80% total occupancy with dirty rows above the 70% low watermark, start
     one pressure cycle
  -> if clean-row eviction cannot satisfy the hard cache bound, reject
     atomically with cache_pressure
  -> never call the store or wait for an in-flight flush

Stop(ctx)
  -> channelappend has already closed admission and drained accepted post-commit effects
  -> cancel the periodic loop
  -> drain remaining dirty active rows in bounded batches with the caller's stop context
     and the same per-attempt timeout
  -> after a successful drain, repeated Stop calls return without flushing
     again under the restore maintenance fence; a timed-out or failed drain
     remains retryable
```

The flush worker does not construct conversation rows and does not read message
payloads. It only persists dirty active rows already admitted into the
conversationactive cache, keeping cache visibility immediate while bounding
eventual durable lag. The capacity-1 pressure channel and single worker goroutine
coalesce concurrent wakeups; every attempt remains bounded by
`AuthorityFlushBatchRows` and `AuthorityFlushTimeout`. The worker owns and reuses
the delayed pressure-retry timer, stops it on cancellation, and never immediately
re-signals a zero-progress batch, so repeated version conflicts cannot create a
busy loop.

## Conversation Authority Handoff

```text
cluster.RouteAuthorityEvent
  -> ignore stale events by hash-slot route revision, Slot config epoch, Slot leader term, and diagnostic authority epoch tie-break
  -> when handing off a previous local target, mark that exact target draining
     under the authority mutex so no new admission reservation can start
  -> hand the reservation wait and flush/purge to a bounded background drain;
     the route watcher remains free to publish a newer local tenure
  -> in that background drain, wait within the handoff timeout for only that
     exact target's accepted admission reservations to reach zero
  -> if local node becomes authority:
       mark the exact conversation authority target active
       purge clean rows for that hash slot before reusing its cache baseline
  -> if leader becomes unknown:
       drain the previous local or warming target with AuthorityHandoffTimeout
       mark the no-leader target warming
  -> if another node becomes authority:
       drain the previous local or warming target with AuthorityHandoffTimeout
       leave the remote target unroutable to the local authority
```

Foreground committed-message admission still resolves the current UID authority
through the routed `ConversationAuthorityClient`. The watcher only maintains
local cache/list readiness for targets that this node can serve. Handoff drains
only after the previous exact target's accepted cache mutations have returned;
it then drains dirty runtime rows indexed under the previous target's UID hash slot, using
`AuthorityFlushBatchRows` per iteration until the target is clean or
`AuthorityHandoffTimeout` expires. The same timeout bounds reservation wait and
durable drain together. A successful drain then purges clean rows for
that hash slot; activation also purges any clean rows retained from an older
leader tenure, so a stale durable baseline is never reused after leadership
returns. Every drain iteration and its final purge revalidate the exact draining
target. If a newer local tenure has replaced it, the obsolete drain returns
`transferred` without purging or continuing through the new tenure. Dirty rows
for other hash slots stay owned by
their current authorities and are left for their own scoped drains or the normal
conversation active flush worker. The lifecycle also periodically pulls current
authorities from the same initial route source so missed watch events and startup
races repair local authority state. The hard local authority identity is
`(HashSlot, SlotID, LeaderNodeID, Slot leader term, Slot config epoch)`; route
revision orders observations, and the authority epoch is retained only as a
local diagnostic tie-breaker for the same distributed identity.
Reservations are keyed by that complete hard identity rather than hash slot, so
an old target waiting to drain does not serialize admission to a newly published
local target for the same logical hash slot. Chained unknown or remote route
events retain every older exact target that is already draining until its
accepted reservations and scoped flush complete; a newly active local tenure
may supersede those drains because it owns any late cache mutation for the hash
slot. A successful final purge retires that exact draining target atomically,
so completed remote/unknown handoffs do not accumulate route state. A failed or
timed-out drain remains fenced for an explicit bounded retry or later local
tenure replacement.

## Cloud Analysis Gateway Composition

`NewCloudAnalysisGatewayHandler` is the composition root for the standalone
simulator-side analysis process. It wires fixed private manager,
Prometheus, and node API adapters into `internal/usecase/cloudanalysis`, then
wraps the usecase with the authenticated Streamable HTTP MCP adapter. The
runtime never joins the WuKongIM cluster and never receives a cloud credential.
`cmd/wkanalysis` verifies a short-lived GitHub OIDC identity at the separate
`internal/access/cloudanalysismcp` token-exchange entry and injects only the
resulting run-scoped Analysis Token verifier into this composition root.

`NewFakeCloudSimulationControlPlane` composes the same provider-neutral
lifecycle usecase with the persistent fake adapter. The adapter's JSON
file emulates provider inventory only; real adapters recover solely from cloud
tags and inventory APIs.

## Cloud View Composition

`NewCloudViewHandler` is the composition root for the standalone simulator-side
public browser gateway. It injects the fixed private node API, Manager,
WebSocket, and Prometheus origins into `internal/access/cloudview`. The process
never joins the cluster, decodes WKProto frames, or receives cloud credentials;
the Cloud Simulation lifecycle separately owns public TCP/19443 ingress.

## Scheduled Backup Composition

Backup composition is always present as a cluster capability but has no startup
TOML or environment policy. Manager stores the only plan in Controller state.

The composition root creates:

- a cluster-bound credential cipher and shared-file/OSS/COS/S3-compatible
  repository provider;
- a Controller-backed scheduled state store;
- Manager save-only plan, exact-revision repository test, archive, and restore
  services;
- repository visibility probes for every active data node;
- current-authority Slot/message export RPC adapters;
- one Controller-leader scheduled runtime;
- node-local staged restore plus distributed all-replica coordination.

Plan saving never opens the repository. The separately invoked test uses the
saved credential and repository provider, runs the coordinator and node-local
probe adapters against every active data node, and publishes verified state
only for the same saved plan revision. Backup admission and runner wiring share
that durable verification gate.

The scheduled runtime starts after cluster control and stops before cluster
storage. Only the current Controller Leader evaluates schedules, advances
backup batches, or advances restore. Active work is read from Controller state,
so Leader failover resumes it.

Online backup captures stable Slot metadata and Channel-leader message
snapshots while ordinary traffic continues. Each producing node writes
compressed chunks directly to the shared repository. Publication occurs only
after all 256 Slot artifacts verify.

Restore is a normal Manager operation, not a startup mode. Controller
maintenance keeps Manager reachable while Gateway/business traffic is fenced.
Every current physical Slot replica captures rollback data, stages and verifies
the selected archive, and is rechecked before switch. Failure enters the same
durable rollback phase. Successful restore increments the Manager session epoch
and preserves restored client tokens. Restore-sensitive delivery, metadata,
permission, and message-event caches are activation-fenced when maintenance
starts and reset again immediately before business runtimes resume; a slow
pre-restore cache fill therefore cannot republish stale state.

The node-local resume acknowledgement runs while Controller maintenance is
still active. It reloads the restored system-UID cache through the dedicated
maintenance-only local read, rebuilds side-effect runtimes, retargets the
stable Channel RPC gateway, and restarts paused Channel background loops.
Controller clears maintenance only after every current data node acknowledges
that resume path.

## Standalone Issue Agent Composition

`issue_agent.go` composes the JSON command operations used only by
`cmd/wkissueagent`. It is not called by `app.New`, does not join a WuKongIM
cluster, and owns no server lifecycle.

The composition root connects deterministic reconciliation, signed state-ref
storage, bounded Context Builder reads, filesystem candidate capture, the
clean Verifier, short-lived App token minting, and the sole Candidate
Publisher. Codex runs in the official Action and is not embedded here. Local
capture and verification require no GitHub configuration. GitHub reads use an
explicit read token; writes require the protected repository-scoped App token.
Publisher credentials therefore never enter the product server or Codex
Engineer process.

PR lifecycle and Review events first complete a separate credential-free
Signal Workflow. A `workflow_run` event then wakes the default-branch
Controller. This composition validates the fixed Signal Workflow name, exact
Agent PR branch, actor permission, current Review threads, and signed state
before making a decision; the Signal payload never grants authority.

Pure Bug-form admission, permission, risk, and lifecycle tracking rules live
in `internal/usecase/issueagent`; this package only gathers facts and composes
their adapters. Context construction freezes every repository `AGENTS.md` and
`FLOW.md` Git blob identity from the task's exact candidate source commit.

Controller admission requires the binary's exact checkout SHA to match the
fresh protected `main` head. The reusable task freezes that SHA for every
trusted control role while the candidate workspace uses the task's separate
exact base SHA.

After the Controller commits a signed transition, that transition and its
dispatch result remain authoritative if a GitHub status projection fails.
Status repair runs before terminal tracking-label removal; a status failure
therefore retains `ready-for-agent` so the bounded sweep retries the projection.
Projection failures are emitted as typed Workflow warnings without discarding
the committed result.
