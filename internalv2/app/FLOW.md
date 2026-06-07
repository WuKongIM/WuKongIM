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
management, UID connection-route authority, conversation list projections, and
opt-in local online delivery.

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
     plus conversation list request latency and page-shape metrics
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
       create internalv2/usecase/conversation with an infra/cluster adapter
       for UID-owned active conversation pages and channel-owned last visible
       message reads
  -> when Delivery.Enabled=true and the cluster exposes clusterv2 Slot metadata
     subscriber APIs, create a delivery metadata adapter backed by real storage
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
  -> create message.App with clusterv2 ChannelAppender, clusterv2 committed
     message reader when exposed, node-scoped IDs, channel latest committed
     sink when exposed, optional delivery committed sink, and append metrics
     observer only when delivery is enabled and messages were not overridden
  -> create access/gateway.Handler with message and activation-timeout-wrapped presence usecases
  -> create access/api.Server with the channel, user, message, and conversation
     usecases, optional bench presence snapshot controller, and real benchmark
     channel/subscriber data writer when API.ListenAddr is configured
  -> create pkg/gateway.Gateway with WKProto CONNECT authentication only when listeners are configured
```

`Delivery.Enabled` defaults to false. With delivery disabled, committed message
events are not emitted to the delivery runtime and the existing `SEND ->
SENDACK` behavior is preserved. With delivery enabled, gateway RECVACK and
session close feedback flows to the delivery usecase. Committed message events
enter the runtime manager admission queue directly, so SENDACK latency is not
coupled to subscriber scan or owner push execution. Closed admission returns a
typed error; full-queue admission waits for capacity until the caller context
expires, producing manager observations without changing the send result. Runtime
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

The delivery adapter scopes unscoped person-channel committed events to the two
channel participants before they enter runtime partition planning. This keeps a
person message to one scoped fanout task instead of one task per authority
partition. The app subscriber planner still derives both UIDs for direct
person-channel scans. For non-person unscoped channel fanout it delegates to an
optional durable subscriber source. If no source is supplied and delivery is
enabled, the composition root installs a clusterv2 Slot-metadata-backed source
when the cluster runtime exposes it. `/bench/v1/channels` and
`/bench/v1/channels/subscribers` write real channel metadata and subscriber rows
through Slot proposals. The benchmark data writer uses bounded concurrency for
independent channel/subscriber mutations while preserving subscriber mutation
order within the same channel. Group fanout then pages a cached channel
subscriber snapshot backed by the real subscriber table and filters UIDs by the
task's UID hash-slot partition before presence resolution; subscriber mutations
advance the cache version and force a fresh snapshot. Scoped UID delivery still
bypasses subscriber scan and flows through presence resolution plus the local or
RPC owner pusher.

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
`internalv2/usecase/conversation` and the `internalv2/infra/cluster`
`ConversationStore` adapter. The read model pages UID-owned active conversation
rows directly, fetches the newest visible durable message only for the returned
page, and applies the public `active_at/channel_id/channel_type` cursor without
scanning membership rows. Conversation rows do not store the last message.
When metrics are enabled, the app maps API conversation-list observations to
Prometheus metrics for latency, returned items, sparse items, last-message
loads, last-message errors, active-index stale skips, and whether another active
page exists using only low-cardinality labels.

Legacy user management requests flow from internalv2 HTTP through
`internalv2/usecase/user` and the `internalv2/infra/cluster`
`UserMetadataStore` adapter to `pkg/clusterv2.Node` Slot metadata facades.
Token and device mutations are proposed through UID Slot ownership. Online
status reads use the v2 presence usecase when available, while device close
side effects are limited to owner-local sessions from `online.Registry`.
System UID persistence reuses the compatible channel metadata store's internal
subscriber-list model.

Legacy message send and channel message sync requests flow from internalv2 HTTP
through `internalv2/usecase/message`. Sends use the clusterv2 ChannelAppender
and node-scoped message IDs. Channel message sync uses the
`internalv2/infra/cluster` ChannelMessageReader, which reads committed ChannelV2
messages through the clusterv2 Node facade and keeps legacy person-channel
response IDs in the HTTP adapter.

After a durable append succeeds, `MessageCommitted` flows through the app-level
committed sink group. When the cluster exposes `UpsertChannelLatestBatch`, the
conversation projector records one channel-owned `channel_latest` dirty row in memory
keyed by `(channel_id, channel_type)`. A background flush coalesces repeated
updates for the same channel and writes batches through clusterv2, so SEND does
not synchronously propose conversation metadata. This projection is
independent of delivery fanout: it is written even when `Delivery.Enabled=false`,
and it never fans out one row per channel member.

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
  -> presence touch worker Start(ctx)
  -> conversation projector Start(ctx)
  -> delivery worker group Start(ctx): retry scheduler starts before async manager
  -> api.Start()
  -> gateway.Start()

Stop(ctx)
  -> restore diagnostics sendtrace sink
  -> gateway.Stop()
  -> api.Stop(ctx)
  -> delivery worker group Stop(ctx): async manager drains before retry scheduler
  -> conversation projector Stop(ctx): stop ticker and flush dirty channel_latest rows
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
