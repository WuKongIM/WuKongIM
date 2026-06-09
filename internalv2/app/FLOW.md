# internalv2/app Flow

## Responsibility

`internalv2/app` is the only composition root for the new skeleton. It wires
phase-1 config, the internalv2 root logger, `pkg/clusterv2`, the message
usecase, the channel management usecase, the user management usecase, the
conversation list usecase, the optional delivery usecase/runtime, the presence
usecase, the gateway handler, the optional HTTP API runtime, the optional
Prometheus metrics registry, and the optional gateway runtime. The phase-1
runtime supports single-node clusters and static multi-node clusters for the
`SEND -> SENDACK` write path, legacy-compatible channel/user metadata
management, UID connection-route authority, sender/recipient UID hash-slot
authority routing, conversation authority active cache/list reads, and opt-in
local online delivery.

This package owns lifecycle ordering. Business rules stay in usecase packages,
and protocol details stay in access packages.

## Construction Flow

```text
New(Config)
  -> derive effective clusterv2 config from Config.Cluster with top-level fallbacks
  -> create a root logger from Config.Log unless a test/harness override is supplied
  -> create metrics registry when Observability.MetricsEnabled=true and attach
     runtime observers for metrics/logging
     (gateway runtime pressure, Slot scheduler pressure, ControllerV2 Raft step queue, ChannelV2 append/replication/PullHint/runtime pressure stages, message DB grouped commit pressure, and delivery fanout)
     plus conversation list request latency/page-shape metrics and conversation
     authority admit, list, cache-pressure, and handoff counters, plus
     sender-authority route and recipient-authority queue/dispatch counters
  -> when Observability.Diagnostics.Enabled=true:
       create a bounded node-local diagnostics store, runtime tracking rules,
       sampler, and sendtrace sink; install the process-wide sendtrace sink
       without exposing HTTP debug APIs
  -> create clusterv2.Node when no ClusterRuntime override is provided
  -> when the cluster exposes channel metadata APIs:
       create internalv2/usecase/channel with an infra/cluster Slot metadata adapter
       and, when exposed by the cluster, wire the same adapter as the
       UID-owned membership projection index
  -> when the cluster exposes conversation metadata reads:
       create an infra/cluster read adapter for channel-owned last visible
       message reads and DB-only UID-owned active conversation pages
       when the cluster also exposes conversation authority routing and metadata
       writes, create one local authority cache plus one routed
       ConversationAuthorityClient, register the conversation authority RPC
       adapter, create the route-authority lifecycle, and use that client as
       the conversation list Store while keeping the read adapter as Messages
  -> when the cluster exposes clusterv2 Slot metadata subscriber APIs, create
     a delivery metadata adapter backed by real storage for bench setup,
     recipient-authority scans, and optional delivery fanout
  -> when the cluster exposes presence routing:
       create owner boot ID, online.Registry, runtime/presence.Directory,
       infra/cluster.PresenceAuthorityClient, usecase/presence.App,
       and access/node presence RPC adapter
       register the presence authority and owner-action RPC handlers on clusterv2
       create the presence touch worker
  -> when the cluster exposes user metadata APIs:
       create internalv2/usecase/user with an infra/cluster Slot metadata
       adapter, owner-local online registry, optional presence lookup, and the
       channel metadata adapter as the system UID store
  -> when Delivery.Enabled=true:
       create a clusterv2-backed delivery partitioner
       when route snapshots are available, an app subscriber planner, presence
       resolver, local/cluster delivery pusher, and partition-leader fanout router
       wrap the fanout runner with a bounded in-memory retry scheduler
       create runtime/delivery Manager in bounded async mode around the runner
       attach delivery observer for metrics and async error logging
       create usecase/delivery.App backed by the manager
       register delivery push and fanout RPC handlers when node RPC is available
  -> when UID authority routing and conversation authority are available:
       create recipient authority client, local processor, dispatcher, bounded
       committed worker, and recipient authority RPC handler
  -> create message.App with clusterv2 ChannelAppender, clusterv2 committed
     message reader when exposed, node-scoped IDs, recipient committed worker,
     and append metrics observer when delivery or metrics are enabled
  -> create sender authority router for gateway/API sends and register sender
     authority RPC handler that submits only to the raw local message.App
  -> create access/gateway.Handler with sender-routed message and activation-timeout-wrapped presence usecases
  -> create access/api.Server with the channel, user, message, and conversation
     usecases, optional bench presence snapshot controller, and real benchmark
     channel/subscriber data writer when API.ListenAddr is configured
  -> create pkg/gateway.Gateway with WKProto CONNECT authentication only when listeners are configured
```

`Delivery.Enabled` defaults to false. With delivery disabled, committed message
events still enter the recipient-authority worker so recent conversation state
is updated, but no online delivery is submitted. With delivery enabled, gateway
RECVACK and session close feedback flows to the delivery usecase, and recipient
processors submit only recipient-scoped committed events after conversation
updates are admitted. The foreground SEND path waits for channel authority
durable append; subscriber scan, remote recipient forwarding, conversation
mutation, and owner push execution run after SENDACK. After append, the message
usecase attempts bounded recipient queue admission. Closed or full recipient
admission returns a typed committed-sink error for observation but does not
change channel durability or the already-successful SENDACK decision. Runtime
fanout failures are counted with normalized delivery error classes. Retryable
fanout failures enter a bounded in-memory retry scheduler with a small fixed
attempt cap; retry queue overflow is surfaced as `queue_full`. Owner-local
pushes write `RecvPacket` values through `online.SessionHandle.WriteDelivery`.
Each owner push snapshots the immutable envelope payload once and reuses that
snapshot across recipient packets; closed-session and outbound-overflow write
errors are terminal drops, while unknown write errors remain retryable. The same
message observer records per-message append success/error latency and classifies
append failures with low-cardinality labels for benchmark triage, including
typed ChannelV2/cluster errors and short append results.

The recipient committed worker scopes unscoped person-channel events to the two
channel participants before recipient dispatch. For non-person unscoped
channels it pages durable subscribers through the clusterv2 Slot metadata source
or an explicitly supplied subscriber source. Recipients are grouped by exact UID
hash-slot authority target, then each recipient authority first admits active
conversation patches and only then submits delivery scoped to that recipient
group. `/bench/v1/channels` and `/bench/v1/channels/subscribers` write real
channel metadata and subscriber rows through Slot proposals. The benchmark data
writer uses bounded concurrency for independent channel/subscriber mutations
while preserving subscriber mutation order within the same channel. Scoped UID
delivery bypasses subscriber scan and flows through recipient authority,
presence resolution, and the local or RPC owner pusher.
When metrics are enabled, sender authority route decisions are counted as
`local`, `remote`, or normalized route/error results, and recipient authority
work is counted by committed-worker queue result plus dispatch phase
(`worker`, `conversation`, or `delivery`) and normalized result. These metrics
do not include UID, channel, slot, or node labels.

When the cluster runtime exposes route snapshots, delivery planning uses the
clusterv2 UID hash-slot table to create authority partitions. A fanout task
router runs local partitions through the in-process fanout worker and forwards
remote partitions through access/node Delivery Fanout RPC. The remote node then
uses its own subscriber source and still pushes resolved online routes by
owner node. Runtime fanout task, resolve, and push observations are translated
by app-level metrics/logging adapters; retry enqueue, attempt, drop, and
queue-depth observations use the same adapter. The delivery runtime itself stays
independent from Prometheus and concrete logging backends.

The ChannelV2 metrics observer also logs rare admitted-append cancellation
snapshots emitted by the reactor. These lines include the channel key, op id,
commit mode, LEO/HW/target offset, queue and in-flight counts, and quorum
progress flags plus a compact leader-visible follower summary so benchmark
timeout triage can identify the stuck append phase without adding
high-cardinality Prometheus labels.

Message append observations record low-cardinality metrics for every durable
append attempt and log rare append failures, including gateway deadline
timeouts, with path, error class, duration, and raw error. These diagnostics do
not change append admission, durable write, or quorum ACK rules.

If a test or harness supplies `WithCluster` and that runtime implements the
cluster append surface, `New` still wires a `ChannelAppender` to keep the real
send path available.
If that runtime also implements the committed channel message read surface,
`New` wires a `ChannelMessageReader` so `/channel/messagesync` can use the same
message usecase as the gateway send path.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

Legacy channel management requests flow from internalv2 HTTP through
`internalv2/usecase/channel` and the `internalv2/infra/cluster`
`ChannelMetadataStore` adapter to `pkg/clusterv2.Node` Slot metadata facades.
Mutations are proposed through Slot ownership; reads use the current routed Slot
metadata store. Ordinary subscriber mutations also project `(uid, channel)` rows
through the UID-owned membership facade for compatible metadata reads; the
conversation list itself pages UID-owned active conversation rows instead.

Conversation list reads flow from entry adapters through
`internalv2/usecase/conversation`. When the cluster exposes the conversation
authority surface, the list Store is the routed
`internalv2/infra/cluster.ConversationAuthorityClient`, which resolves the UID
hash-slot authority and reads the target-owned active view from the local or
remote authority cache. The Messages port remains the `ConversationStore`
adapter so last-message hydration reads committed ChannelV2 tails with
`Config.Conversation.MaxLastMessageConcurrency` as a bounded tail-read limit.
If a test or limited harness exposes conversation reads but not the authority
surface, the usecase uses `ConversationStore` for both Store and Messages as a
DB-only compatibility path. Conversation rows do not store the last message.
When metrics are enabled, the app maps API conversation-list observations to
Prometheus metrics for latency, returned items, sparse items, last-message
loads, last-message errors, active-index stale skips, and whether another active
page exists using only low-cardinality labels.

Conversation list with authority enabled:

```text
/conversation/list
  -> access/api parses the UID page request
  -> internalv2/usecase/conversation asks Store for the UID active view
  -> ConversationAuthorityClient resolves the UID hash-slot authority
  -> local authority:
       validate the exact RouteTarget
       merge unflushed authority-cache rows with UID-owned DB active rows
  -> remote authority:
       call access/node Conversation Authority List RPC for the target-owned view
  -> usecase hydrates only the returned page with channel-owned last-visible messages
  -> access/api shapes the legacy-compatible response
```

Legacy user management requests flow from internalv2 HTTP through
`internalv2/usecase/user` and the `internalv2/infra/cluster`
`UserMetadataStore` adapter to `pkg/clusterv2.Node` Slot metadata facades.
Token and device mutations are proposed through UID Slot ownership. Online
status reads use the v2 presence usecase when available, while device close
side effects are limited to owner-local sessions from `online.Registry`.
System UID persistence reuses the compatible channel metadata store's internal
subscriber-list model.

Legacy message send and channel message sync requests flow from internalv2 HTTP
through the app message facade. Sends use the sender authority router first;
local sender-authority work uses `internalv2/usecase/message` with the
clusterv2 ChannelAppender and node-scoped message IDs, while remote
sender-authority work is forwarded through access/node Sender Authority RPC.
Channel message sync stays on the raw message usecase and uses the
`internalv2/infra/cluster` ChannelMessageReader, which reads committed ChannelV2
messages through the clusterv2 Node facade and keeps legacy person-channel
response IDs in the HTTP adapter.

After a durable append succeeds, `MessageCommitted` flows to the app-level
recipient committed worker. Before lifecycle start, tests and harnesses run this
worker synchronously; after `Start`, it tries a non-blocking bounded enqueue and
returns to the SEND path. The worker requests committed payload bytes only when
delivery is wired; conversation-only recipient updates stay metadata-only.
Person channels are scoped to the two participants. Group and other unscoped
channels page durable subscribers progressively instead of loading the full
subscriber set. The recipient dispatcher resolves each UID to the current UID
hash-slot authority target, groups by exact target, processes local groups
in-process, and forwards remote groups through access/node Recipient Authority
RPC. The local recipient processor validates the fenced target is still current
and local, admits recent-conversation patches through the shared
`ConversationAuthorityClient`, then submits delivery scoped to that recipient
group when delivery is enabled. Conversation active rows remain working-set
hints: delayed or dropped recipient background work does not change message
durability or SENDACK success. The local authority cache still coalesces active
rows, serves list reads by merging cache rows with DB active rows, and writes
durable active patches with read/delete floors on explicit Stop, handoff drain,
or cache pressure.

SEND with sender and recipient authority enabled:

```text
gateway/API send
  -> SenderAuthorityRouter resolves FromUID hash-slot target
  -> local sender target:
       raw message.App appends to channel authority through clusterv2 ChannelAppender
  -> remote sender target:
       access/node Sender Authority RPC
       remote local sender authority validates exact target
       raw message.App appends to channel authority through clusterv2 ChannelAppender
  -> channel authority persists message and returns append result
  -> SENDACK returns to sender
  -> recipient committed worker tries bounded enqueue of MessageCommitted
  -> recipient dispatcher pages/scopes recipients and groups by UID authority target
  -> local recipient target:
       validate exact target
       ConversationAuthorityClient.AdmitPatches
       optional delivery.SubmitCommitted with MessageScopedUIDs
  -> remote recipient target:
       access/node Recipient Authority RPC then same local recipient processing
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
  -> wait for clusterv2 write routing when the cluster runtime exposes route snapshots
  -> conversation authority route lifecycle Start(ctx): watch route authorities and seed current targets
  -> presence touch worker Start(ctx)
  -> delivery worker group Start(ctx): retry scheduler starts before async manager
  -> recipient committed worker Start(ctx): open bounded post-commit queue
  -> api.Start()
  -> gateway.Start()

Stop(ctx)
  -> restore diagnostics sendtrace sink
  -> gateway.Stop()
  -> api.Stop(ctx)
  -> recipient committed worker Stop(ctx): close admission, drain accepted events,
     and cancel in-flight dispatch if Stop times out
  -> delivery worker group Stop(ctx): async manager drains before retry scheduler
  -> conversation authority route lifecycle Stop(ctx): cancel authority watcher
  -> presence touch worker Stop(ctx)
  -> cluster.Stop(ctx)
```

`Start` and `Stop` are serialized by a lifecycle mutex. If API or gateway
startup fails after the cluster starts, `Start` attempts rollback in reverse
order; if rollback fails, state remains retryable so a later `Stop` can clean up.
The manager drains accepted fanout before the retry scheduler stops, so queued
retries remain available while accepted manager work completes. Stale pending
recvacks expire during owner-local push activity.

## Presence Touch Worker

```text
clusterv2.RouteAuthorityEvent
  -> if local node becomes authority:
       runtime/presence.Directory.BecomeAuthority(target)
  -> if another node becomes authority:
       Directory.LoseAuthority(hashSlot)

periodic flush
  -> runtime/presence.Directory.ExpireRoutes(now, routeTTL)
  -> drain owner-local dirty routes through online.Registry.DrainTouched
  -> resolve the current UID authority target for each route
  -> group touches by observed target and call PresenceAuthorityClient.TouchRoutesTo
  -> requeue only failed or unresolved owner-local route identities
```

The app worker has one authority watch loop and one periodic touch loop. It does
not scan or replay owner-local active sessions when authority changes, and it
does not create per-hash-slot workers.

## Conversation Authority Handoff

```text
clusterv2.RouteAuthorityEvent
  -> ignore stale events by hash-slot route revision and authority epoch
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
local cache/list readiness for targets that this node can serve, and `Stop`
retries pending admission patches before flushing remaining local cache rows
with the caller's stop context.
