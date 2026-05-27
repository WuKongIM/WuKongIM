# internalv2/app Flow

## Responsibility

`internalv2/app` is the only composition root for the new skeleton. It wires
phase-1 config, `pkg/clusterv2`, the message usecase, the gateway handler, and
the optional gateway runtime.

This package owns lifecycle ordering. Business rules stay in usecase packages,
and protocol details stay in access packages.

## Construction Flow

```text
New(Config)
  -> derive effective clusterv2 config from Config.Cluster with top-level fallbacks
  -> create clusterv2.Node when no ClusterRuntime override is provided
  -> create message.App with clusterv2 ChannelAppender and node-scoped IDs
  -> create access/gateway.Handler
  -> create pkg/gateway.Gateway only when listeners are configured
```

If a test or harness supplies `WithCluster` and that runtime implements the
cluster append surface, `New` still wires a `ChannelAppender` to keep the real
send path available.

The effective cluster node ID is also the message ID seed. `Config.Cluster.NodeID`
wins when set; top-level `Config.NodeID` is only the fallback.

## Lifecycle Flow

```text
Start(ctx)
  -> cluster.Start(ctx)
  -> gateway.Start()

Stop(ctx)
  -> gateway.Stop()
  -> cluster.Stop(ctx)
```

`Start` and `Stop` are serialized by a lifecycle mutex. If gateway startup fails
after the cluster starts, `Start` attempts cluster rollback; if rollback fails,
state remains retryable so a later `Stop` can clean up.
