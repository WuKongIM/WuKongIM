# internalv2/app Flow

## Responsibility

`internalv2/app` is the only composition root for the new skeleton. It wires
phase-1 config, `pkg/clusterv2`, the message usecase, the presence usecase, the
gateway handler, the optional HTTP API runtime, the optional Prometheus metrics
registry, and the optional gateway runtime. The phase-1 runtime supports
single-node clusters and static multi-node clusters for the `SEND -> SENDACK`
write path and UID connection-route authority.

This package owns lifecycle ordering. Business rules stay in usecase packages,
and protocol details stay in access packages.

## Construction Flow

```text
New(Config)
  -> derive effective clusterv2 config from Config.Cluster with top-level fallbacks
  -> create metrics registry and runtime observers when Observability.MetricsEnabled=true
     (gateway, ControllerV2 Raft step queue, ChannelV2 append/replication/PullHint stages, and message DB grouped commits)
  -> create clusterv2.Node when no ClusterRuntime override is provided
  -> create message.App with clusterv2 ChannelAppender and node-scoped IDs
  -> when the cluster exposes presence routing:
       create owner boot ID, online.Registry, runtime/presence.Directory,
       infra/cluster.PresenceAuthorityClient, usecase/presence.App,
       and access/node presence RPC adapter
       register the presence authority and owner-action RPC handlers on clusterv2
       create the route-authority rehydrate worker
  -> create access/gateway.Handler with message and activation-timeout-wrapped presence usecases
  -> create access/api.Server when API.ListenAddr is configured
  -> create pkg/gateway.Gateway with WKProto CONNECT authentication only when listeners are configured
```

If a test or harness supplies `WithCluster` and that runtime implements the
cluster append surface, `New` still wires a `ChannelAppender` to keep the real
send path available.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

The effective cluster node ID is also the message ID seed. `Config.Cluster.NodeID`
wins when set; top-level `Config.NodeID` is only the fallback.

## Lifecycle Flow

```text
Start(ctx)
  -> cluster.Start(ctx)
  -> wait for clusterv2 write routing when the cluster runtime exposes route snapshots
  -> presence rehydrate worker Start(ctx)
  -> api.Start()
  -> gateway.Start()

Stop(ctx)
  -> gateway.Stop()
  -> api.Stop(ctx)
  -> presence rehydrate worker Stop(ctx)
  -> cluster.Stop(ctx)
```

`Start` and `Stop` are serialized by a lifecycle mutex. If API or gateway
startup fails after the cluster starts, `Start` attempts rollback in reverse
order; if rollback fails, state remains retryable so a later `Stop` can clean up.

## Presence Authority Rehydrate

```text
clusterv2.RouteAuthorityEvent
  -> if local node becomes authority:
       runtime/presence.Directory.BecomeAuthority(target)
  -> if another node becomes authority:
       Directory.LoseAuthority(hashSlot)
  -> for every authority target with a leader:
       page owner-local active sessions through online.Registry.VisitActiveByHashSlot
       call infra/cluster.PresenceAuthorityClient.RehydrateRoutesTo(target, batch)
       route returned conflict actions to their owner node
       commit or abort pending routes on the target authority
```

The app worker keeps at most one in-flight rehydrate task per hash slot. A newer
authority event cancels older epoch work before replaying the latest target, and
stale initial snapshots are ignored if a newer watched event has already been
accepted.
