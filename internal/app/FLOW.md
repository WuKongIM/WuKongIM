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
and protocol details stay in access packages.

## Construction Flow

```text
New(Config)
  -> derive effective cluster config from Config.Cluster with top-level fallbacks
     including ChannelMessageRetention -> cluster.ChannelRetention physical
     cleanup settings; background physical GC remains disabled by default
  -> create a root logger from Config.Log unless a test/harness override is supplied
  -> create metrics registry when Observability.MetricsEnabled=true and attach
     runtime observers for metrics/logging
     (gateway runtime pressure, Slot scheduler/proposal/apply-gap/leader-election pressure, Controller Raft step queue/apply gap, TransportV2 service RPC totals/latency, Channel runtime append/replication/PullHint/runtime pressure stages, message DB grouped commit pressure, and delivery fanout)
     plus direct ants/v2 pool occupancy gauges for instrumented runtime pools
     plus conversation list request latency/page-shape metrics, conversation
     authority admit/list/cache-pressure/handoff counters, conversation active
     cache/flush gauges and histograms, channel append and post-commit
     counters, recipient delivery worker queue/admission/process metrics,
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
     goroutine count, and thread count via gopsutil; it keeps a bounded
     in-memory sticky alert window for readiness, pressure, sendack-error, and
     gateway session-error signals with compact evidence facts so `wkcli top`
     can show why active or recently resolved warnings fired; TransportV2
     service pressure uses aliases registered with service worker pools so
     operator views do not expose raw `service_<id>` labels; top remains an
     in-memory collector and still runs when Observability.MetricsEnabled=false
  -> when Observability.Prometheus.Enabled=true:
       validate that the API metrics endpoint is enabled and create a child
       Prometheus runtime that writes prometheus.yml under the configured
       Prometheus data dir, extracts the embedded Prometheus binary when no
       external binary path is configured, and scrapes the node API /metrics endpoint
     Manager realtime monitor queries use this configured Prometheus HTTP API.
     They scope PromQL to the generated `wukongim` job and can optionally add
     a node-scoped filter; Channel runtime monitor PromQL prefers promoted
     `wukongim_channel_*` metric families and falls back to legacy
     `wukongim_channelv2_*` families at the query boundary; unified realtime
     monitor snapshots also pass the selected node into bounded control
     snapshot reads. The database monitor category is Prometheus-only and uses
     internal message DB commit request,
     grouped commit stage, commit runtime queue, and Pebble engine snapshot
     metrics. The node monitor category keeps per-node Prometheus series for
     process CPU, RSS memory, goroutines, and Go GC pause/rate/CPU/heap-goal
     pressure so global views can show the highest-pressure node without
     dropping node labels.
     Queries do not read the top collector's in-process dashboard ring buffers;
     the hidden collector only refreshes process resource and Pebble snapshot
     gauges for Prometheus when metrics are enabled.
  -> when Observability.Diagnostics.Enabled=true:
       create a bounded node-local diagnostics store, runtime tracking rules,
       sampler, and sendtrace sink; install the process-wide sendtrace sink
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
       the conversation list Store while keeping the read adapter as Messages,
       durable state reads, read-cursor writes, and delete-barrier writes
  -> when the cluster exposes cluster Slot metadata subscriber APIs, create
     a delivery metadata adapter backed by real storage for bench setup,
     channelappend subscriber scans, and optional delivery fanout
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
       create a cluster-backed delivery partitioner
       when route snapshots are available, an app subscriber planner, presence
       resolver, local/cluster delivery pusher, and partition-leader fanout router
       wrap the fanout runner with a bounded in-memory retry scheduler
       create runtime/delivery Manager in bounded async mode around the runner
       attach delivery observer for metrics and async error logging
       create usecase/delivery.App backed by the manager
       register delivery push and fanout RPC handlers when node RPC is available
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
       plugin hook metrics observer
       when metrics are enabled, expose durable commit PersistAfter events to
       channelappend, expose durable offline recipient candidates to
       channelappend's recipient delivery worker for Receive hooks, and
       register the manager plugin RPC handler when node RPC is available so
       peer managers can inspect or mutate this node's plugin lifecycle state
       and invoke this node's local /plugin/route hook for forwarded plugin
       HTTP requests
  -> when Webhook config is enabled:
       create the node-local webhook runtime with bounded workqueue admission,
       finite retry, and an HTTP sender; wire webhook adapters into
       channelappend's durable post-commit PersistAfter sink, the batch offline
       recipient observer, and the presence online-status observer
       Plugin hooks and webhook sinks coexist on the same side-effect surfaces.
       Webhook failures are best-effort side effects and must not affect
       SENDACK, durable append, recipient delivery, or conversation active
       admission.
  -> when the cluster exposes Channel runtime append plus channel append authority:
       create channelappend.Group with hash-sharded per-channel authority writers,
       cluster ChannelAppender, node-scoped message IDs, subscriber source,
       cluster-backed idempotency lookup when the cluster exposes it,
       recipient authority resolver, conversation active-batch admitter,
       optional recipient delivery worker enqueuer, optional plugin/webhook
       PersistAfter enqueuers, optional plugin/webhook offline-recipient
       observers, append metrics observer, and shared append/post-commit worker
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
  -> create access/api.Server with the channel, user, message, CMD sync, and conversation
     usecases, legacy route address lookup derived from gateway listeners and
     static cluster voters, optional debug snapshots, optional bench presence
     snapshot controller, and real benchmark channel/subscriber data writer when
     API.ListenAddr is configured
  -> create access/manager.Server with static manager JWT login when
     Manager.ListenAddr is configured; when the cluster exposes local control
     snapshots, attach internal/usecase/management for `/manager/nodes`,
     `/manager/nodes/:node_id/config`, `/manager/slots`,
     `/manager/channels`, `/manager/channel-runtime-meta`,
     `/manager/conversations`, `/manager/messages`, `/manager/connections*`,
     `/manager/nodes/:node_id/plugins*`, `/manager/plugin-bindings`,
     `/manager/users*`, and
     `/manager/system-users*`;
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
     optional presence owner-action routing, and message retention requests use
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
     control snapshots through the management usecase; the realtime monitor
     does not read from `topCollector`
  -> create pkg/gateway.Gateway with WKProto CONNECT authentication only when listeners are configured
```

The DB Inspect reader is app-owned because only the composition root derives
the node-local storage locations for `pkg/db/inspect`. It is exposed to manager
usecases as a read-only diagnostics port and never accepts filesystem paths
from HTTP, web, or node RPC callers. The manager page can inspect the local
manager node by omitting `node_id`; selecting another node uses the manager DB
inspect RPC path to that node and does not combine rows from multiple nodes.

The ordinary application log reader is also app-owned because only the
composition root owns `Log.Dir` and the concrete node-local logger layout. It is
separate from the distributed Controller/Slot Raft log reader: ordinary app log
requests list fixed local log sources and parse application log entries, while
Raft log requests read cluster log storage metadata and decoded Raft payloads.
Remote ordinary app log requests use the manager app-log RPC path for the
selected node and still return only reader-owned source names and file labels,
never absolute paths.

The node-config snapshot provider is app-owned because only startup config
loading has the fully merged TOML/env effective values. `internal/config`
builds the bounded allowlist once during startup, redacts manager credentials,
cluster join tokens, static manager users, local filesystem paths, and
similarly sensitive values, then attaches that snapshot to `app.Config`.
`internal/app` only serves the supplied snapshot for the local node and returns
`ErrNodeConfigUnavailable` when the startup loader did not provide one. It is
read-only and does not watch or mutate live runtime config.

The diagnostics store is app-owned because only the composition root knows
whether `Observability.Diagnostics.Enabled` installed the bounded event store,
tracking sampler, and process-wide sendtrace sink. Manager diagnostics routes
use that same store for local reads and tracking-rule mutations; non-local
node-scoped reads and mutations route through the manager diagnostics RPC path
without falling back to legacy `internal` diagnostics state.

Controller Raft status and manual compaction use a cluster-routed management
operator created in the app composition root. Local reads and compaction call
the local cluster node facade directly; non-local node-scoped operations use
the manager Controller Raft node RPC path. The cluster-wide manager compact
action fans out above the RPC layer by targeting every Controller voter in the
current control snapshot.

`Delivery.Enabled` remains false for app-level zero-value configs, while the
`wukongim` executable config enables `WK_DELIVERY_ENABLE` by default. With
delivery disabled, committed message effects still run inside the channel
authority writer so recent conversation state is updated, but no online
delivery is submitted. With delivery enabled, gateway RECVACK and session close
feedback flows to the delivery usecase, while channelappend post-commit effects
enqueue recipient-authority delivery batches into the recipient delivery worker.
`Config.ChannelAppend.AuthorityShardCount` defaults to a CPU-aware lookup-shard
count with a minimum of four. `ChannelAppend.AdvancePoolSize` is the direct ants
pool capacity used to activate channelappend writer state machines.
`ChannelAppend.EffectPoolSize` is the direct ants pool capacity used separately
by foreground channelappend append effects and post-append recipient effects.
Prepare runs inline on the writer advance path; append remains the foreground
durable path that determines SEND/SENDACK throughput.
`ChannelAppend.RecipientAuthorityDispatchConcurrency` defaults to a bounded
recipient-authority target fanout per post-commit envelope. The lookup-shard count controls writer map
sharding; effect workers run only blocking effects and never write channel
state concurrently with another advance for the same channel. The delivery
observer maps aggregate writer pressure and effect pool observations into
Prometheus, and also records direct ants/v2 occupancy for the channelappend
advance/append_effect/post_commit pools in the generic ants pool metrics. The three-node bench
script summarizes these in `channelappend_metrics_summary.tsv` and
`ants_pool_usage_summary.tsv`. Per-channel append ordering remains capped
by the single-writer invariant even when different channels run through
different shards or workers.
The foreground SEND path waits only for channel-authority durable append;
subscriber scan, conversation active-batch admission, recipient authority
grouping, and delivery enqueue all run after SENDACK from the authority
writer's best-effort post-commit pipeline. The recipient delivery worker later
drains accepted batches, resolves online routes, and pushes owner-node delivery
commands. Post-commit persistence and restart replay are not part of
channelappend. Post-commit enqueue failures are logged with the failing phase and
route/dispatch context, counted through effect metrics, and dropped after the
routed helper's bounded retry window; they do not change channel durability or
the already-successful SENDACK decision. Conversation active-batch admission
performs only a short bounded fresh-route retry in the routed client; failures
surface as the `conversation_active` post-commit phase before online delivery
is enqueued.
Runtime fanout failures are counted with normalized delivery error classes.
Retryable fanout failures enter
a bounded in-memory retry scheduler with a small fixed attempt cap; retry queue
overflow is surfaced as `queue_full`. Owner-local pushes write `RecvPacket` values through
`online.SessionHandle.WriteDelivery`. Each owner push snapshots the immutable
envelope payload once and reuses that snapshot across recipient packets;
closed-session and outbound-overflow write errors are terminal drops, while
unknown write errors remain retryable. The same append observer records
per-message append success/error latency and classifies append failures with
low-cardinality labels for benchmark triage, including typed Channel runtime/cluster
errors and short append results.

The channel append commit pipeline scopes unscoped person-channel events to the
two channel participants. For non-person unscoped channels it pages durable
subscribers through the app delivery metadata source, an explicitly supplied
subscriber source, or the cluster Slot metadata source. After each recipient
set is formed, channelappend admits a kind-aware
`conversationactive.ActiveBatch` through the shared
`ConversationAuthorityClient`; channelappend chooses normal versus CMD kind
from the committed envelope, and active admission still runs when online
delivery is disabled.
Recipients are then grouped by exact UID hash-slot authority target including
Slot leader term and Slot config epoch for delivery; when cluster exposes
batch key routing, the app recipient resolver resolves each subscriber page's
unique UIDs through one batch route lookup before grouping. When delivery is
enabled, the app wires a bounded
recipient delivery worker that drains those batches and runs the delivery-only
channelappend recipient processor outside the authority writer. `/bench/v1/channels` and
`/bench/v1/channels/subscribers` write real channel metadata and subscriber rows
through Slot proposals. The benchmark data writer uses bounded concurrency for
independent channel/subscriber mutations while preserving subscriber mutation
order within the same channel. Scoped UID delivery bypasses subscriber scan and
flows through recipient authority grouping, presence resolution, and the local
or RPC owner pusher after the recipient delivery worker accepts the batch.
These metrics do not include UID, channel, slot, or per-target labels.

When the cluster runtime exposes route snapshots, delivery planning uses the
cluster UID hash-slot table to create authority partitions. A fanout task
router runs local partitions through the in-process fanout worker and forwards
remote partitions through access/node Delivery Fanout RPC. The remote node then
uses its own subscriber source and still pushes resolved online routes by
owner node. Runtime fanout task, resolve, and push observations are translated
by app-level metrics/logging adapters; retry enqueue, attempt, drop, and
queue-depth observations use the same adapter. The delivery runtime itself stays
independent from Prometheus and concrete logging backends.

The Channel runtime metrics observer also logs rare admitted-append cancellation
snapshots emitted by the append runtime. These lines include the channel key,
op id, commit mode, LEO/HW/target offset, queue and in-flight counts, and
quorum progress flags plus a compact leader-visible follower summary so
benchmark timeout triage can identify the stuck append phase without adding
high-cardinality Prometheus labels.

Message append observations record low-cardinality metrics for every durable
append attempt and log rare append failures, including gateway deadline
timeouts, with path, error class, duration, and raw error. These diagnostics do
not change append admission, durable write, or quorum ACK rules.
When metrics are enabled, app observability also adapts cluster message event
observations into Prometheus counters, histograms, and stream-cache gauges.
The adapter preserves the cluster-provided bounded labels only; it does not add
UID, channel, slot, or per-message labels.

If a test or harness supplies `WithCluster` and that runtime implements the
cluster append surface, `New` still wires a `ChannelAppender` to keep the real
send path available.
If that runtime also implements the committed channel message read surface,
`New` wires a `ChannelMessageReader` so `/channel/messagesync` can use the same
message usecase as the gateway send path.
If that runtime also implements the message event projection surface, `New`
wires `MessageEventStore` so `/message/event` appends and `/channel/messagesync`
event summaries share the same Slot/meta reducer as other cluster-owned message
metadata. `/message/eventsync` remains outside the app surface in this phase.

If the runtime also exposes unified conversation projection writes and committed
Channel runtime reads, `New` wires `internal/usecase/cmdsync` through
`CMDSyncStore`. `/message/sync` scans only `ConversationKindCMD` rows from the
UID-owned projection, reads the corresponding command/source SyncOnce channel
logs, and returns legacy message arrays through the API adapter.
`/message/syncack` advances CMD-kind read cursors in the same kind-aware
conversation table, so CMD sync does not introduce a second metadata branch or
pending-state updater. Ordinary conversation hydration stays on
`ConversationKindNormal` rows and skips `SyncOnce`/command-channel log entries
instead of relying on suffix filtering in conversation storage or list logic.

Bench runtime controls flow from internal HTTP through `internal/infra/cluster`, `pkg/cluster.Node`, `pkg/cluster/channels.Service`, and finally the hosted Channel runtime runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

Legacy channel management requests flow from internal HTTP through
`internal/usecase/channel` and the `internal/infra/cluster`
`ChannelMetadataStore` adapter to `pkg/cluster.Node` Slot metadata facades.
Mutations are proposed through Slot ownership; reads use the current routed Slot
metadata store. Ordinary subscriber mutations also project `(uid, channel)` rows
through the UID-owned membership facade for compatible metadata reads; the
conversation list itself pages UID-owned active conversation rows instead. When
the channelappend group is available, the app-level subscriber mutation observer
forwards the final large-group flag and subscriber mutation version to
`channelappend.Group.ApplySubscriberMutation` so non-large channel subscriber
snapshots cached in `channelState` stay aligned with API mutations.

Conversation list reads flow from entry adapters through
`internal/usecase/conversation`. When the cluster exposes the conversation
authority surface, the list Store is the routed
`internal/infra/cluster.ConversationAuthorityClient`, which resolves the UID
hash-slot authority and reads the target-owned active view from the local or
remote authority cache. The Messages port remains the `ConversationStore`
adapter so last-message hydration reads committed Channel runtime tails with
`Config.Conversation.MaxLastMessageConcurrency` as a bounded tail-read limit;
the same adapter remains the StateStore, StateMutationStore, and DeleteStore so
legacy conversation read/delete mutations still write through UID-owned Slot
metadata instead of the authority list client.
If a test or limited harness exposes conversation reads but not the authority
surface, the usecase uses `ConversationStore` for both Store and Messages as a
DB-only compatibility path. Conversation rows do not store the last message.
When metrics are enabled, the app maps API conversation-list observations to
Prometheus metrics for latency, returned items, sparse items, last-message
loads, last-message errors, active-index stale skips, and whether another active
page exists using only low-cardinality labels. It also maps conversation active
cache observations to Prometheus gauges for cached rows, dirty rows, oldest
dirty age, fixed normal/CMD row and dirty-row counts, and flush
result/row/duration metrics.

Conversation list with authority enabled:

```text
/conversation/list
  -> access/api parses the UID page request
  -> internal/usecase/conversation asks Store for the UID active view
  -> ConversationAuthorityClient resolves the UID hash-slot authority
  -> local authority:
       validate the exact RouteTarget
       delegate cache and UID-owned DB active-view merge to runtime/conversationactive.Manager
  -> remote authority:
       call access/node Conversation Authority List RPC for the target-owned view
  -> usecase hydrates only the returned page with channel-owned last-visible messages
  -> access/api shapes the legacy-compatible response
```

Conversation active-batch admission with authority enabled:

```text
channelappend active producer
  -> emits conversationactive.ActiveBatch with explicit normal or CMD kind
  -> ConversationAuthorityClient.AdmitActiveBatch
       -> cluster groups SenderUID and recipient UIDs by exact UID authority
  -> local authority:
       validate the exact RouteTarget
       delegate ActiveBatch to runtime/conversationactive.Manager.AdmitActiveBatch
  -> remote authority:
       access/node Conversation Authority ActiveBatch RPC
       remote local authority applies the same target validation and runtime admission
```

The app authority does not regroup or reinterpret active batches and does not
normalize zero conversation kinds. It trusts the cluster-routed client to send
`SenderUID` only to the sender-owned authority target; non-sender recipient
targets arrive with an empty sender field.

Legacy user management requests flow from internal HTTP through
`internal/usecase/user` and the `internal/infra/cluster`
`UserMetadataStore` adapter to `pkg/cluster.Node` Slot metadata facades.
Token and device mutations are proposed through UID Slot ownership. Online
status reads use the v2 presence usecase when available, while device close
side effects are limited to owner-local sessions from `online.Registry`.
System UID persistence reuses the compatible channel metadata store's internal
subscriber-list model.

Legacy message send and channel message sync requests flow from internal HTTP
through the app message facade. Sends delegate to `channelappend.Router`, which
resolves the canonical channel's append authority. Local authority sends are
admitted to the local `channelappend.Group`; remote authority sends are forwarded
through access/node Channel Append RPC to the target node, where they enter only
that node's authority writer group. Channel message sync uses the
`internal/infra/cluster` ChannelMessageReader, which reads committed Channel runtime
messages through the cluster Node facade and keeps legacy person-channel
response IDs in the HTTP adapter.

Conversation active rows remain working-set hints: delayed or dropped
post-commit work does not change message durability or SENDACK success. The
runtime/conversationactive.Manager coalesces active rows, serves list reads by
merging cached rows with UID-owned DB active rows, and flushes durable active
touch patches through the conversation active flush worker, handoff drain, or
cache pressure. The app conversation authority keeps route target fencing,
lifecycle handoff, observer mapping, and usecase/RPC type adaptation.

SEND with channel authority routing enabled:

```text
gateway/API send
  -> message.App delegates to channelappend.Router
  -> Router resolves channel append authority
  -> local channel authority:
       channelappend.Group admits the batch to the channel writer
  -> remote channel authority:
       access/node Channel Append RPC forwards the batch
       remote node admits it to its local channel writer
  -> authority writer prepares commands, allocates IDs, and calls cluster ChannelAppender
  -> Channel runtime persists messages and returns append result
  -> SENDACK returns to sender
  -> authority writer post-commit effect:
       scope person recipients or page subscribers
       ConversationAuthorityClient.AdmitActiveBatch for the expanded recipient set
       group recipients by UID authority target, including Slot leader term and config epoch, for delivery
       enqueue recipient delivery batch when delivery is enabled
       drop the in-memory post-commit envelope after one enqueue attempt
```

The bench presence snapshot controller aggregates `online.Registry.Snapshot`
and `runtime/presence.Directory.Snapshot`. It is read-only and exists so
wkbench can validate owner-route and authority-route counts after connection
runs.

The effective cluster node ID is also the message ID seed. `Config.Cluster.NodeID`
wins when set; top-level `Config.NodeID` is only the fallback.

## Lifecycle Flow

```text
Start(ctx)
  -> cluster.Start(ctx)
  -> task audit startup backfill: append one snapshot event for each active
     Controller task in the local control snapshot; failures are logged and
     do not block service startup
  -> seed join loop Start(ctx): retry JoinNode against stable-order seeds when seed-join config is present
  -> wait for cluster write routing when the cluster runtime exposes route snapshots; the gate also runs the cluster write probe, which proves Slot metadata writes and Channel runtime placement data-node candidates before gateway SEND admission
  -> conversation authority route lifecycle Start(ctx): watch route authorities and seed current targets
  -> conversation active flush worker Start(ctx): periodically persist dirty active rows
  -> presence touch worker Start(ctx)
  -> plugin runtime Start(ctx): open the host RPC socket, scan local plugins, and start enabled processes
  -> plugin PersistAfter worker Start(ctx): accept durable commit side effects before channel append opens
  -> webhook runtime Start(ctx): accept post-commit webhook side effects before producers open
  -> delivery worker group Start(ctx): retry scheduler, async manager, then recipient delivery worker
  -> channel append group Start(ctx): open local channel-authority writer admission
  -> api.Start()
  -> manager.Start()
  -> prometheus.Start(ctx): write prometheus.yml and start the child Prometheus process
  -> gateway.Start()

Stop(ctx)
  -> restore diagnostics sendtrace sink
  -> gateway.Stop()
  -> prometheus.Stop(ctx)
  -> manager.Stop(ctx)
  -> api.Stop(ctx)
  -> channel append group Stop(ctx): close admission and drain accepted appends plus post-commit effects
  -> delivery worker group Stop(ctx): recipient delivery worker drains before async manager and retry scheduler
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

`Start` and `Stop` are serialized by a lifecycle mutex. If API, manager, Prometheus, or gateway
startup fails after the cluster starts, `Start` attempts rollback in reverse
order; if rollback fails, state remains retryable so a later `Stop` can clean up.
When `Plugin.Enable=true` (the default unless `WK_PLUGIN_ENABLE=false` is set),
the app wires the PDK-compatible node-local plugin
runtime, desired-state store adapter, minimal lifecycle host RPC adapter, v2
plugin usecase, and bounded PersistAfter worker before channelappend. The
channelappend group receives only the PersistAfter enqueue port. Plugin runtime
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
The manager drains accepted fanout before the retry scheduler stops, so queued
retries remain available while accepted manager work completes. Stale pending
recvacks expire during owner-local push activity.

## Presence Touch Worker

```text
cluster.RouteAuthorityEvent
  -> if local node becomes authority:
       runtime/presence.Directory.BecomeAuthority(target with route revision, Slot config epoch, Slot leader term, diagnostic authority epoch)
  -> if another node becomes authority:
       Directory.LoseAuthority(hashSlot)

periodic flush
  -> pull current route authorities from the cluster snapshot and repair missed watch events
  -> runtime/presence.Directory.ExpireRoutes(now, routeTTL)
  -> drain owner-local dirty routes through online.Registry.DrainTouched
  -> resolve the current UID authority target for each route
  -> group touches by observed target and call PresenceAuthorityClient.TouchRoutesTo
  -> requeue only failed or unresolved owner-local route identities
```

The app worker has one authority watch loop and one periodic touch loop. It does
not scan or replay owner-local active sessions when authority changes, and it
does not create per-hash-slot workers. Authority event ordering first compares
route revision, then Slot config epoch, then Slot leader term, and only uses
the authority epoch as a diagnostic tie-breaker for the same distributed
identity; the periodic loop pulls the current authorities so startup races or
dropped watch events self-heal.

## Conversation Active Flush Worker

```text
periodic flush
  -> derive an AuthorityFlushTimeout-bounded attempt context
  -> conversationAuthority.FlushActiveRows(attemptCtx, AuthorityFlushBatchRows)
  -> runtime/conversationactive.Manager selects dirty rows with version fencing
  -> batch-read durable conversation rows for receiver-only cooldown filtering
  -> skip receiver-only ActiveAt updates inside AuthorityActiveCooldown
  -> store.TouchConversationActiveAtBatch persists remaining ActiveAt/ReadSeq/UpdatedAt

Stop(ctx)
  -> channelappend has already closed admission and drained accepted post-commit effects
  -> cancel the periodic loop
  -> drain remaining dirty active rows in bounded batches with the caller's stop context
     and the same per-attempt timeout
```

The flush worker does not construct conversation rows and does not read message
payloads. It only persists dirty active rows already admitted into the
conversationactive cache, keeping cache visibility immediate while bounding
eventual durable lag.

## Conversation Authority Handoff

```text
cluster.RouteAuthorityEvent
  -> ignore stale events by hash-slot route revision, Slot config epoch, Slot leader term, and diagnostic authority epoch tie-break
  -> if local node becomes authority:
       mark the exact conversation authority target active
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
only dirty runtime rows indexed under the previous target's UID hash slot, using
`AuthorityFlushBatchRows` per iteration until the target is clean or
`AuthorityHandoffTimeout` expires. Dirty rows for other hash slots stay owned by
their current authorities and are left for their own scoped drains or the normal
conversation active flush worker. The lifecycle also periodically pulls current
authorities from the same initial route source so missed watch events and startup
races repair local authority state. The hard local authority identity is
`(HashSlot, SlotID, LeaderNodeID, Slot leader term, Slot config epoch)`; route
revision orders observations, and the authority epoch is retained only as a
local diagnostic tie-breaker for the same distributed identity.
