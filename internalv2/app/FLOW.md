# internalv2/app Flow

## Responsibility

`internalv2/app` is the only composition root for the new skeleton. It wires
phase-1 config, `pkg/clusterv2`, the message usecase, the optional delivery
usecase/runtime, the presence usecase, the gateway handler, the optional HTTP
API runtime, the optional Prometheus metrics registry, and the optional gateway
runtime. The phase-1 runtime supports
single-node clusters and static multi-node clusters for the `SEND -> SENDACK`
write path, UID connection-route authority, and opt-in local online delivery.

This package owns lifecycle ordering. Business rules stay in usecase packages,
and protocol details stay in access packages.

## Construction Flow

```text
New(Config)
  -> derive effective clusterv2 config from Config.Cluster with top-level fallbacks
  -> create metrics registry and runtime observers when Observability.MetricsEnabled=true
     (gateway, ControllerV2 Raft step queue, ChannelV2 append/replication/PullHint stages, message DB grouped commits, and delivery fanout)
  -> create clusterv2.Node when no ClusterRuntime override is provided
  -> when Delivery.Enabled=true and the cluster exposes clusterv2 Slot metadata
     subscriber APIs, create a delivery metadata adapter backed by real storage
  -> when the cluster exposes presence routing:
       create owner boot ID, online.Registry, runtime/presence.Directory,
       infra/cluster.PresenceAuthorityClient, usecase/presence.App,
       and access/node presence RPC adapter
       register the presence authority and owner-action RPC handlers on clusterv2
       create the presence touch worker
  -> when Delivery.Enabled=true:
       create a clusterv2-backed delivery partitioner
       when route snapshots are available, an app subscriber planner, presence
       resolver, local/cluster delivery pusher, and partition-leader fanout router
       wrap the fanout runner with a bounded in-memory retry scheduler
       create runtime/delivery Manager in bounded async mode around the runner
       attach delivery metrics observers when metrics are enabled
       create usecase/delivery.App backed by the manager
       register delivery push and fanout RPC handlers when node RPC is available
  -> create message.App with clusterv2 ChannelAppender, node-scoped IDs,
     and delivery committed sink only when delivery is enabled and messages
     were not overridden
  -> create access/gateway.Handler with message and activation-timeout-wrapped presence usecases
  -> create access/api.Server with optional bench presence snapshot controller
     and real benchmark channel/subscriber data writer when API.ListenAddr is configured
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
order within the same channel. Group fanout then pages the real channel
subscriber table and filters UIDs by the task's UID hash-slot partition before
presence resolution. Scoped UID delivery still bypasses subscriber scan and
flows through presence resolution plus the local or RPC owner pusher.

When the cluster runtime exposes route snapshots, delivery planning uses the
clusterv2 UID hash-slot table to create authority partitions. A fanout task
router runs local partitions through the in-process fanout worker and forwards
remote partitions through access/node Delivery Fanout RPC. The remote node then
uses its own subscriber source and still pushes resolved online routes by
owner node. Runtime fanout task, resolve, and push observations are translated
by app-level metrics adapters; retry enqueue, attempt, drop, and queue-depth
observations use the same adapter. The delivery runtime itself stays
independent from Prometheus.

If a test or harness supplies `WithCluster` and that runtime implements the
cluster append surface, `New` still wires a `ChannelAppender` to keep the real
send path available.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

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
  -> delivery worker group Start(ctx): retry scheduler starts before async manager
  -> api.Start()
  -> gateway.Start()

Stop(ctx)
  -> gateway.Stop()
  -> api.Stop(ctx)
  -> delivery worker group Stop(ctx): async manager drains before retry scheduler
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
